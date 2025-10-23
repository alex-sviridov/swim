# LabMan Architecture

## Overview

LabMan is a distributed lab management service written in Go that enforces **one lab per user** and **one WebSocket connection per user**. It wraps the SSH relay functionality and manages lab lifecycle across multiple instances using Redis as shared state.

## Core Principles

- **Per-user isolation**: Each user can only run one lab at a time
- **Single WebSocket**: Only one active WebSocket connection per user
- **Distributed**: Multiple LabMan instances share state via Redis
- **Stateless REST**: HTTP endpoints are stateless, read from Redis
- **Graceful handling**: 5-minute grace period on disconnect before lab shutdown

## Redis State

### Single Key Per User
```
labman:{userId} → Hash
```

**Fields:**
- `labId` - Currently running lab ID (empty string if none)
- `status` - `idle` | `provisioning` | `running` | `stopping`
- `wsInstanceId` - Which LabMan instance holds the WebSocket
- `wsConnectedAt` - Timestamp when WebSocket connected
- `disconnectTimerAt` - Timestamp when disconnect timer started (empty if connected)
- `lastActivity` - Last activity timestamp

## API Endpoints

### GET /status
Returns current lab status for authenticated user.

**Response:**
```json
{
  "status": "running",
  "labId": 123,
  "message": "Lab is running"
}
```

**Logic:**
1. Extract userId from JWT token
2. Read `labman:{userId}` from Redis
3. Return formatted status

### POST /stop
Stops currently running lab for authenticated user.

**Logic:**
1. Extract userId from JWT token
2. Get current labId from `labman:{userId}`
3. Publish to `vmmanager:decommission` queue: `{webuserid, labId}`
4. Update Redis: set `labId=""`, `status="idle"`, `disconnectTimerAt=""`
5. If this instance has the WebSocket (`wsInstanceId` matches), close it
6. Return success

### WebSocket /
Main connection endpoint for lab access.

**Query params:**
- `labId` - Requested lab ID
- `token` - Keycloak JWT token

**Connection Logic:**
1. Authenticate token, extract userId
2. Read current state from `labman:{userId}`
3. Determine action:
   - **No lab running** (labId empty) → Start provisioning new lab
   - **Same lab running** (labId matches) → Take over WebSocket (old connection will fail on next heartbeat)
   - **Different lab running** (labId differs) → Stop old lab, start new lab
4. Update Redis with new state (labId, wsInstanceId, timestamps)
5. Handle the action:
   - **Provision new lab**:
     - Check `vmmanager:servers:{webuserid}:{labId}` cache
     - If not running, publish to `vmmanager:provision` queue
     - Poll cache until status = "running" (send provisioning updates via WebSocket)
     - Connect SSH using address from cache
   - **Connect to existing lab**:
     - Read `vmmanager:servers:{webuserid}:{labId}` from cache
     - Connect SSH to existing session
   - **Stop and start**:
     - Publish to `vmmanager:decommission` for old lab
     - Follow provision flow for new lab
   - Start bidirectional relay (SSH ↔ WebSocket)

**Message Protocol:**

*Client → Server:*
- `{type: "input", data: "..."}` - Terminal input
- `{type: "resize", cols: 80, rows: 24}` - Terminal resize

*Server → Client:*
- `{type: "output", data: "..."}` - Terminal output from SSH
- `{type: "info", message: "..."}` - Informational messages (lab switching, provisioning updates)
- `{type: "connected"}` - SSH connection established
- `{type: "provisioning", message: "..."}` - Provisioning progress
- `{type: "error", message: "..."}` - Errors

**On Disconnect:**
1. Set `disconnectTimerAt` to now + 5 minutes in Redis
2. Keep lab running (VM stays up)
3. Close SSH connection on this instance

**On Reconnect (within 5 min):**
1. Clear `disconnectTimerAt`
2. Reconnect to existing VM via SSH
3. Resume session

## Background Workers

### Timeout Checker
Runs every 30 seconds on each LabMan instance:
1. Scan all `labman:*` keys in Redis
2. For each user with `disconnectTimerAt` set:
   - If current time > disconnectTimerAt → Stop lab
   - Publish to `vmmanager:decommission` queue
   - Update Redis: `labId=""`, `status="idle"`, `disconnectTimerAt=""`
3. Multiple instances may check simultaneously, but Redis atomic operations prevent conflicts

## WebSocket Takeover

When a new WebSocket connection is established and `wsInstanceId` in Redis points to a different instance:

1. New instance updates `wsInstanceId` to itself atomically
2. Old WebSocket connection becomes "orphaned"
3. Old connection detects it's no longer the owner on next heartbeat (every 30s)
4. Old connection closes gracefully
5. User continues with new connection seamlessly

**No coordination needed** - Redis state is the single source of truth.

## Lab Lifecycle States

```
idle → provisioning → running → stopping → idle
          ↓              ↓
       (error)       (disconnect)
          ↓              ↓
        idle      (wait 5min) → stopping
```

## Frontend Flow

### Components

**LabPage.vue**
- Container that orchestrates LabInfo ↔ LabConsole
- Decides which component to show based on lab status

**LabInfo.vue** (NEW)
- Polls `GET /status` every 5 seconds
- Shows different UI based on status:
  - **Different lab running**: Show message with 2 buttons:
    - "Go to Lab {id}" - Navigate to running lab page
    - "Stop and Connect" - Call `POST /stop`, wait for idle
  - **Provisioning/Connecting**: Show spinner with message
- When status becomes `idle` or `running` (same lab) → Hide LabInfo, show LabConsole

**LabConsole.vue** (MODIFIED)
- Shows only when lab is ready to connect
- Connects to WebSocket immediately when mounted
- Handles new message types: `info`, `provisioning`

### Composables

**useLabStatus.js** (NEW)
```javascript
// Polls GET /status every 5 seconds
// Returns: { status, labId, message, isLoading }
// Emits events when status changes
```

**useSSHConnection.js** (MODIFIED)
- Remove auto-provisioning logic (backend handles it)
- Add handlers for `info` and `provisioning` message types
- Simplified: just connect and relay

### User Journey

**Scenario 1: No lab running**
1. User opens LabPage for lab 5
2. LabInfo polls `/status` → `{status: "idle", labId: null}`
3. LabInfo automatically hides, LabConsole shows
4. LabConsole connects WebSocket with labId=5
5. LabMan provisions lab, sends provisioning messages
6. User sees provisioning updates in terminal
7. Lab ready, SSH connected, user can interact

**Scenario 2: Different lab running**
1. User has lab 3 running, opens LabPage for lab 5
2. LabInfo polls `/status` → `{status: "running", labId: 3}`
3. LabInfo shows: "Lab 3 is running" with 2 buttons
4. User clicks "Stop and Connect"
5. Frontend calls `POST /stop`
6. LabInfo keeps polling, status becomes `idle`
7. LabInfo hides, LabConsole shows and connects
8. WebSocket connects to lab 5, backend provisions it

**Scenario 3: Same lab running**
1. User has lab 5 running in one tab
2. Opens lab 5 in another tab
3. LabInfo polls `/status` → `{status: "running", labId: 5}`
4. LabInfo immediately hides, LabConsole shows
5. LabConsole connects WebSocket
6. LabMan detects same lab, forces old WebSocket to close
7. New WebSocket connects to existing SSH session
8. User sees in terminal: "Reconnected to session"

**Scenario 4: Disconnect and timeout**
1. User closes browser tab
2. WebSocket disconnects
3. LabMan sets 5-minute disconnect timer
4. User reopens within 5 minutes
5. WebSocket reconnects, timer cleared, session resumed
6. If 5 minutes pass → Background worker stops lab

## VMManager Integration

LabMan communicates with VMManager (separate component) via Redis queues and cache.

### Redis Queues

**vmmanager:provision** - Request VM provisioning
```json
{
  "webuserid": "keycloak-user-id",
  "labId": 5
}
```

**vmmanager:decommission** - Request VM decommissioning
```json
{
  "webuserid": "keycloak-user-id",
  "labId": 5
}
```

### Redis Cache

**vmmanager:servers:{webuserid}** - VM server info (written by VMManager)
```json
{
  "user": "student",
  "address": "192.168.1.100",
  "status": "provisioning|running|stopping",
  "available": true,
  "cloudStatus": "running",
  "labId": 5
}
```

### Provisioning Flow

1. LabMan checks Redis: `vmmanager:servers:{webuserid}`
2. If not exists or labId doesn't match or available != true:
   - Publish to `vmmanager:provision` queue with desired labId
   - Poll cache every 2 seconds until available = true and labId matches (max 10 minutes)
   - Send provisioning progress to WebSocket: `{type: "provisioning", message: "..."}`
3. When available = true and labId matches:
   - Extract user and address from cache
   - Connect SSH using default SSH key
   - Send `{type: "connected"}` to WebSocket

### Decommissioning Flow

1. LabMan publishes to `vmmanager:decommission` queue: `{webuserid, labId}`
2. LabMan closes SSH connection if active
3. VMManager handles:
   - Verifies labId in request matches labId in cache (prevents stale decommissions)
   - VM cleanup/shutdown
   - Deletes `vmmanager:servers:{webuserid}` from Redis cache when done

### SSH Connection

- **Host**: From `address` field in cache
- **Port**: 22 (default)
- **User**: From `user` field in cache (typically "student")
- **Auth**: Default SSH private key (configured in LabMan)

## Deployment

### Architecture
```
Load Balancer (nginx/traefik)
        ↓
[LabMan-1] [LabMan-2] [LabMan-3]
        ↓
    Redis (shared state + queues)
        ↓
VMManager (provisions/decommissions VMs)
```

### Configuration
- No sticky sessions required (state in Redis)
- WebSocket support on load balancer
- Health check: `GET /health`

### Scaling
- Add more LabMan instances as needed
- Redis handles coordination via shared state
- No inter-instance communication required

## Error Handling

- **VM provisioning timeout** (10 min): Send error via WebSocket, set status to `idle`
- **VM status = "error"**: Send error message, publish to decommission queue, set status to `idle`
- **SSH connection fails**: Retry 3 times with exponential backoff, then error message
- **Redis unavailable**: Return 503, cannot serve requests
- **Token invalid**: Close WebSocket with 401
- **User unauthorized for lab**: Close WebSocket with 403

## Security

- JWT token validation via Keycloak
- Per-request authorization (user can access labId?)
- Rate limiting on `/status` endpoint
- WebSocket origin validation
- Redis authentication if exposed

## Observability

- Structured logging (JSON)
- Metrics: active labs, active WebSockets, provisioning time
- Health endpoint: `GET /health`
- Ready endpoint: `GET /ready` (checks Redis connectivity)

## Future Enhancements

- Multi-server labs
- Admin API for monitoring
