# SWIM - ScaleWay Instance Manager

Automated provisioning system for short-lived VM instances in Scaleway cloud. Part of linux-lab project.

## Purpose

SWIM provisions temporary Scaleway VMs with automatic TTL-based cleanup. It operates in service mode, continuously monitoring a Redis queue for provisioning requests and managing the full lifecycle of instances.

## Workflow

1. **Request Submission**: Push provisioning requests to Redis queue
2. **Provisioning**: SWIM picks up requests, creates Scaleway instances with cloud-init
3. **State Tracking**: Server states cached in Redis with deletion timestamps
4. **Cleanup**: Automatic deletion when TTL expires
5. **Error Handling**: Failed provisions trigger automatic cleanup and error state caching

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
redis-cli -a $REDIS_PASSWORD LPUSH swim:provision:queue '{"WebUsername":"test-user","WebLabID":1234,"CloudInitFile":"./cloud-init.yml","TTLMinutes":30}'
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

**Required fields:**
```json
{
  "WebUsername": "student-name",
  "WebLabID": 1234,
  "CloudInitFile": "./cloud-init.yml",
  "TTLMinutes": 60
}
```

**Optional fields** (override environment defaults):
- `SecurityGroupName` - Security group name
- `ImageID` - Scaleway image ID
- `InstanceType` - Instance type (e.g., `DEV1-S`, `DEV1-M`)

## Redis Keys

- `swim:provision:queue` - Provisioning request queue
- `swim:server:<id>` - Server state cache (24h TTL)
