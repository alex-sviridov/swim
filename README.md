# SWIM - ScaleWay Instance Manager

Automated provisioning system for short-lived VM instances in Scaleway cloud. Part of linux-lab project.

## Purpose

SWIM provisions temporary Scaleway VMs with automatic TTL-based cleanup. It operates in service mode, continuously monitoring a Redis queue for provisioning requests and managing the full lifecycle of instances.

## Workflow

### Provisioning
1. **Request Submission**: Push provisioning requests to Redis queue
2. **Provisioning**: SWIM picks up requests, creates Scaleway instances with cloud-init
3. **State Tracking**: Server states cached in Redis with deletion timestamps
4. **Cleanup**: Automatic deletion when TTL expires
5. **Error Handling**: Failed provisions trigger automatic cleanup and error state caching

### Decommissioning
1. **Request Submission**: Push decommission requests to Redis queue
2. **Server Selection**: SWIM queries cached servers by username (and optionally LabID)
3. **Deletion**: Matching servers are deleted immediately
4. **Cache Cleanup**: Deleted servers are removed from Redis cache

## Quick Start

### Build
```bash
go build -o swim ./cmd/swim
```

### Run Service
```bash
./swim
```

### Submit Provisioning Request
```bash
redis-cli -a $REDIS_PASSWORD LPUSH swim:provision:queue '{"WebUsername":"test-user","LabID":1234,"CloudInitFile":"./cloud-init.yml","TTLMinutes":30}'
```

### Submit Decommissioning Request
```bash
# Decommission all VMs for a user
redis-cli -a $REDIS_PASSWORD LPUSH swim:decomission:queue '{"WebUsername":"test-user"}'

# Decommission VMs for a specific user and lab
redis-cli -a $REDIS_PASSWORD LPUSH swim:decomission:queue '{"WebUsername":"test-user","LabID":1234}'
```

## Configuration

### Required Environment Variables
- `SCW_ACCESS_KEY` - Scaleway API access key
- `SCW_SECRET_KEY` - Scaleway API secret key
- `SCW_ORGANIZATION_ID` - Scaleway organization ID
- `SCW_PROJECT_ID` - Scaleway project ID
- `SCW_DEFAULT_ZONE` - Default zone (e.g., `fr-par-1`)

### Optional Environment Variables

**Redis Configuration:**
- `REDIS_CONNECTION_STRING` - Redis connection string (default: `localhost:6379`)
- `REDIS_PASSWORD` - Redis authentication password

**Provisioning Defaults** (can be overridden in request, request values take priority):
- `SWIM_PROVISION_DEFAULT_SECURITY_GROUP` - Default security group for instances
- `SWIM_PROVISION_DEFAULT_IMAGE_ID` - Default Scaleway image ID
- `SWIM_PROVISION_DEFAULT_INSTANCE_TYPE` - Default instance type (e.g., `DEV1-S`)

## Request Format

### Provisioning Request

**Required fields:**
```json
{
  "WebUsername": "student-name",
  "LabID": 1234,
  "CloudInitFile": "./cloud-init.yml",
  "TTLMinutes": 60
}
```

**Optional fields** (override environment defaults):
- `SecurityGroupName` - Security group name
- `ImageID` - Scaleway image ID
- `InstanceType` - Instance type (e.g., `DEV1-S`, `DEV1-M`)

### Decommissioning Request

**Required fields:**
```json
{
  "WebUsername": "student-name"
}
```

**Optional fields:**
- `LabID` - If provided, only VMs matching both username and LabID are deleted. If omitted, all VMs for the user are deleted.

## Redis Keys

**Queues:**
- `swim:provision:queue` - Provisioning request queue
- `swim:decomission:queue` - Decommissioning request queue

**Cache:**
- `swim:server:<id>` - Server state cache (24h TTL)
