# SWIM - VMManager for LabMan

Automated VM provisioning and decommissioning system for LabMan architecture. Integrates with Hetzner Cloud to provision temporary lab instances on demand.

## Documentation

- **[INTERFACE.md](INTERFACE.md)** - Complete interface definition (inputs/outputs, cache format, workflows)
- **[TASK.md](TASK.md)** - LabMan architecture specification

## Purpose

SWIM acts as the **VMManager** component in the LabMan architecture. It:
- Listens to Redis queues for provisioning and decommissioning requests from LabMan
- Provisions VMs on Hetzner Cloud with configured settings
- Caches VM state in Redis (SSH address, username, status) for LabMan to consume
- Automatically cleans up expired VMs based on TTL

## Architecture

```
LabMan → Redis Queues → SWIM (VMManager) → Hetzner Cloud
              ↓
         Redis Cache ← SWIM writes VM state
              ↑
         LabMan reads VM state
```

## Quick Start

### Build
```bash
go build -o swim ./cmd/swim
```

### Run Service
```bash
./swim --redis=localhost:6379
```

### Submit Provisioning Request
```bash
redis-cli -a $REDIS_PASSWORD RPUSH vmmanager:provision '{"webuserid":"keycloak-user-id","labId":5}'
```

### Submit Decommissioning Request
```bash
redis-cli -a $REDIS_PASSWORD RPUSH vmmanager:decommission '{"webuserid":"keycloak-user-id","labId":5}'
```

## Configuration

### Required Environment Variables

**Hetzner Cloud:**
- `HCLOUD_TOKEN` - Hetzner Cloud API token
- `HCLOUD_DEFAULT_IMAGE` - Image ID (e.g., `ubuntu-22.04`)
- `HCLOUD_DEFAULT_SERVER_TYPE` - Server type (e.g., `cx11`, `cx21`, `cx31`)
- `HCLOUD_DEFAULT_LOCATION` - Location (e.g., `nbg1`, `fsn1`, `hel1`)
- `HCLOUD_DEFAULT_FIREWALL` - Firewall ID
- `HCLOUD_DEFAULT_SSH_KEY` - SSH key name or ID
- `HCLOUD_DEFAULT_CLOUD_INIT_FILE` - Path to cloud-init file (e.g., `./cloud-init.yml`)

### Optional Environment Variables

**Redis Configuration:**
- `REDIS_CONNECTION_STRING` - Redis connection string (can also use `--redis` flag)
- `REDIS_PASSWORD` - Redis authentication password

**VMManager Configuration:**
- `SSH_USERNAME` - SSH username for LabMan to use (default: `student`)
- `DEFAULT_TTL_MINUTES` - Time-to-live in minutes (default: `30`)

## Request Format

### Provisioning Request

Minimal format from LabMan:
```json
{
  "webuserid": "keycloak-user-id",
  "labId": 5
}
```

All other configuration comes from environment variables.

### Decommissioning Request

```json
{
  "webuserid": "keycloak-user-id",
  "labId": 5
}
```

## Redis Integration

### Queues

SWIM listens on these queues:
- `vmmanager:provision` - Provisioning requests from LabMan
- `vmmanager:decommission` - Decommissioning requests from LabMan

### Cache Format

SWIM writes VM state to: `vmmanager:servers:{webuserid}:{labId}`

**Cache value (what LabMan reads):**
```json
{
  "user": "student",
  "address": "2a01:4f8:c17:abcd::1",
  "status": "provisioning|running|stopping"
}
```

**Internal fields (not used by LabMan):**
- `serverId` - Hetzner Cloud server ID for deletion
- `expiresAt` - TTL timestamp for cleanup worker
- `webUserId`, `labId` - For cleanup worker to generate decommission requests

## Workflow

### Provisioning
1. LabMan pushes `{webuserid, labId}` to `vmmanager:provision` queue
2. SWIM pops request, caches initial state with `status: "provisioning"`
3. SWIM creates Hetzner Cloud server with cloud-init
4. SWIM updates cache with IPv6 address and `status: "running"`
5. LabMan reads cache and connects SSH to the address

### Decommissioning
1. LabMan pushes `{webuserid, labId}` to `vmmanager:decommission` queue
2. SWIM pops request, looks up cache key `vmmanager:servers:{webuserid}:{labId}`
3. SWIM extracts `serverId` from cache
4. SWIM deletes VM from Hetzner Cloud
5. SWIM removes cache entry

### Automatic Cleanup
1. Background worker runs every 5 minutes
2. Scans all `vmmanager:servers:*` keys
3. For expired VMs (expiresAt < now), pushes to `vmmanager:decommission` queue
4. Decommissioner handles cleanup (reuses same logic as manual decommission)

## Status Mapping

SWIM maps Hetzner Cloud states to VMManager statuses:
- `starting`, `initializing` → `provisioning`
- `running` → `running`
- `stopping`, `off`, `deleting` → `stopping`

## Error Handling

- **Provisioning errors**: VM is deleted, cache is removed
- **Decommission errors**: Logged, no retry (cleanup worker will retry on next run)
- **VM not found**: Cache is removed (VM already deleted manually)

## Testing

### Integration Tests

Comprehensive integration tests validate user isolation, resource control, and LabMan integration:

```bash
# Start Redis for testing
cd internal/integration
docker-compose -f docker-compose.test.yml up -d

# Run all integration tests
go test ./internal/integration/... -v

# Run specific test categories
go test ./internal/integration/... -v -run TestUserIsolation
go test ./internal/integration/... -v -run TestResourceControl
go test ./internal/integration/... -v -run TestLabManScenario

# Stop Redis
docker-compose -f docker-compose.test.yml down
```

See [internal/integration/README.md](internal/integration/README.md) for detailed test documentation.

### Unit Tests

```bash
# Run all tests (unit + integration)
go test ./... -v

# Run only unit tests (skip integration)
go test ./... -short
```
