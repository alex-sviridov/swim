# SWIM VMManager Interface Definition

## Overview

SWIM acts as the VMManager component in the LabMan architecture. It consumes requests from Redis queues and maintains VM state in Redis cache for LabMan to read.

## Redis Queue Inputs

### Provisioning Queue: `vmmanager:provision`

**Purpose**: Request VM provisioning for a user's lab.

**Input Format**:
```json
{
  "webuserid": "string",
  "labId": number
}
```

**Fields**:
- `webuserid` (required): Keycloak user ID for isolation
- `labId` (required): Lab identifier (1-N)

**Example**:
```json
{
  "webuserid": "550e8400-e29b-41d4-a716-446655440000",
  "labId": 5
}
```

---

### Decommissioning Queue: `vmmanager:decommission`

**Purpose**: Request VM decommissioning for a user's lab.

**Input Format**:
```json
{
  "webuserid": "string",
  "labId": number | undefined
}
```

**Fields**:
- `webuserid` (required): Keycloak user ID
- `labId` (optional): Lab identifier for validation
  - If provided: Validates that the cached labId matches before decommissioning (prevents stale requests)
  - If omitted: Decommissions whatever lab the user has running (unconditional decommission)

**Examples**:

*Decommission with labId validation (recommended):*
```json
{
  "webuserid": "550e8400-e29b-41d4-a716-446655440000",
  "labId": 5
}
```
This will only decommission if the user's cached lab is lab 5. If the user switched to lab 7, this request is ignored.

*Unconditional decommission:*
```json
{
  "webuserid": "550e8400-e29b-41d4-a716-446655440000"
}
```
This will decommission whatever lab the user has running, regardless of which lab it is.

---

## Redis Cache Output

### Server State Cache: `vmmanager:servers:{webuserid}`

**Purpose**: Store VM state for LabMan to read and connect SSH. Since each user can only have one active lab at a time, the cache key only includes the user ID.

**Output Format**:
```json
{
  "user": "string",
  "address": "string",
  "status": "string",
  "available": boolean,
  "cloudStatus": "string",
  "serverId": "string",
  "expiresAt": "ISO8601 timestamp",
  "webUserId": "string",
  "labId": number
}
```

**Fields**:

**LabMan-visible fields** (used for SSH connection):
- `user`: SSH username (e.g., `"student"`)
- `address`: IPv6 address for SSH connection (e.g., `"2a01:4f8:c17:abcd::1"`)
- `status`: Normalized VM lifecycle state - `"provisioning"`, `"running"`, or `"stopping"`
- `available`: Boolean indicating if server is ready for SSH connections (true when server is actually available, which depends on cloud provider)
- `cloudStatus`: Raw cloud provider status (e.g., `"running"`, `"starting"`, `"initializing"` for Hetzner Cloud)

**Internal fields** (used by SWIM internally):
- `serverId`: Cloud provider server ID for deletion operations
- `expiresAt`: UTC timestamp when VM expires for cleanup worker
- `webUserId`: User ID for cleanup worker to generate decommission requests
- `labId`: Lab ID for cleanup worker to generate decommission requests

**Example**:
```json
{
  "user": "student",
  "address": "2a01:4f8:c17:abcd::1",
  "status": "running",
  "available": true,
  "cloudStatus": "running",
  "serverId": "hcloud-12345678",
  "expiresAt": "2025-10-22T14:30:00Z",
  "webUserId": "550e8400-e29b-41d4-a716-446655440000",
  "labId": 5
}
```

**Status Values**:
- `status` (normalized): `"provisioning"`, `"running"`, or `"stopping"`
  - `"provisioning"`: VM is being created or starting
  - `"running"`: VM has reached running state (but may not be available yet)
  - `"stopping"`: VM is being deleted
- `available` (boolean): `true` only when server is ready for SSH connections
  - For Hetzner Cloud: `true` when `cloudStatus == "running"`
  - For other providers: availability logic may differ based on their status values
- `cloudStatus` (provider-specific): Raw status from cloud provider
  - Hetzner Cloud examples: `"running"`, `"starting"`, `"initializing"`, `"stopping"`, `"off"`, `"deleting"`
  - Other providers will have their own status values

**Cache TTL**: 24 hours (auto-expires if not refreshed)

---

## Workflows

### Provisioning Workflow

```
1. LabMan → RPUSH vmmanager:provision '{"webuserid":"...","labId":5}'
2. SWIM  → BLPOP vmmanager:provision (blocking read)
3. SWIM  → SET vmmanager:servers:... '{"status":"provisioning","available":false,"labId":5,...}'
4. SWIM  → Create VM on cloud provider
5. SWIM  → Poll cloud provider until status = "running"
6. SWIM  → SET vmmanager:servers:... '{"status":"running","available":true,"address":"...","labId":5,...}'
7. LabMan → GET vmmanager:servers:... (check labId field, read address when available==true for SSH)
```

### Decommissioning Workflow

```
1. LabMan → RPUSH vmmanager:decommission '{"webuserid":"...","labId":5}'
2. SWIM  → BLPOP vmmanager:decommission (blocking read)
3. SWIM  → GET vmmanager:servers:... (check if labId matches, read serverId)
4. SWIM  → SET vmmanager:servers:... '{"status":"stopping","available":false,...}'
5. SWIM  → Delete VM on cloud provider
6. SWIM  → DEL vmmanager:servers:... (remove from cache)
```

**Note**:
- If labId is provided and doesn't match the cached labId, the request is ignored (prevents stale decommission messages)
- If labId is omitted, the decommission proceeds unconditionally for whatever lab is running

### Automatic Cleanup Workflow

```
1. SWIM Cleanup Worker → SCAN vmmanager:servers:* (every 5 minutes)
2. For each expired server (expiresAt < now):
3. SWIM → RPUSH vmmanager:decommission '{"webuserid":"...","labId":N}'
   (labId read from the cache entry)
4. Decommissioning workflow executes
```

---

## User Isolation Guarantees

### One Lab Per User
- At any time, a user can have **at most one** active lab
- Enforced by cache key pattern: `vmmanager:servers:{webuserid}`
- Switching labs requires explicit decommission of old lab first
- The cache stores which lab is currently running for the user

### Separate Cache Namespaces
- User A's lab: `vmmanager:servers:userA`
- User B's lab: `vmmanager:servers:userB`
- These are completely isolated cache entries

### Resource Control
- TTL enforcement via `expiresAt` timestamp
- Cleanup worker prevents "zombie" VMs
- No resource leaks on lab switching

---

## Error Handling

### Provisioning Errors
- **VM creation fails**: Server deleted, cache removed
- **Timeout (10 min)**: Server deleted, cache removed
- **Status check fails**: Server deleted, cache removed

### Decommissioning Errors
- **Server not found in cache**: Log warning, continue (idempotent)
- **Server already deleted on provider**: Remove from cache, continue
- **Delete operation fails**: Log error, no retry (cleanup worker will retry)

---

## Redis Operations Summary

| Operation | Key/Queue | Direction | Purpose |
|-----------|-----------|-----------|---------|
| `RPUSH` | `vmmanager:provision` | LabMan → SWIM | Request provisioning |
| `BLPOP` | `vmmanager:provision` | SWIM reads | Pop provision request |
| `RPUSH` | `vmmanager:decommission` | LabMan → SWIM | Request decommission |
| `BLPOP` | `vmmanager:decommission` | SWIM reads | Pop decommission request |
| `SET` | `vmmanager:servers:{u}` | SWIM → LabMan | Write VM state |
| `GET` | `vmmanager:servers:{u}` | LabMan reads | Read VM state for SSH |
| `DEL` | `vmmanager:servers:{u}` | SWIM cleanup | Remove VM state |
| `SCAN` | `vmmanager:servers:*` | SWIM cleanup | Find expired VMs |

---

## Integration with LabMan

### What LabMan Needs to Do

**Provisioning**:
1. Push request to `vmmanager:provision` queue with desired `labId`
2. Poll `vmmanager:servers:{webuserid}` until `available == true`
3. Verify the `labId` field matches the requested lab
4. Read `address` and `user` fields
5. Connect SSH to `{user}@{address}`

**Note**: LabMan should check `available` field instead of `status` to determine when server is ready for SSH connections, as the availability logic can vary by cloud provider.

**Decommissioning**:
1. Push request to `vmmanager:decommission` queue with current `labId`
2. Poll `vmmanager:servers:{webuserid}` until key is deleted
3. Update UI to show lab stopped

**Switching Labs**:
1. Push decommission request for old lab: `{"webuserid":"...","labId":3}`
2. Wait for cache key deletion
3. Push provision request for new lab: `{"webuserid":"...","labId":5}`
4. Poll `vmmanager:servers:{webuserid}` until new lab reaches `running` status
5. Verify `labId` field is 5

**Checking Running Lab**:
1. Check if `vmmanager:servers:{webuserid}` exists
2. Read the `labId` field to see which lab is running
3. If it matches desired lab and `available == true`, read `address` and connect SSH
4. If it's a different lab, follow switching labs workflow
5. If no cache entry exists, user has no running lab

---

## Testing Interface Compliance

See [internal/integration/README.md](internal/integration/README.md) for comprehensive integration tests that validate:

- User isolation (one lab per user)
- Resource control (TTL enforcement, cleanup)
- Cache format compliance
- All workflows (provision, decommission, cleanup)
- All TASK.md scenarios

Run tests:
```bash
cd internal/integration
docker-compose -f docker-compose.test.yml up -d
go test ./internal/integration/... -v
```
