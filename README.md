# SWIM - ScaleWay Instance Manager

Automated provisioning system for short-life VM instances in Scaleway cloud.

**Written in Go 1.25** | Part of the linuxlab project

## Features

- **Interactive Mode**: One-shot provisioning from JSON payload with immediate output
- **Service Mode**: Redis queue-based continuous provisioning with state caching
- **TTL-based Lifecycle**: Servers are provisioned with a time-to-live (TTL) in minutes
- **Error Handling**: Automatic server deletion and error state caching on provisioning failures
- **State Tracking**: Real-time server state monitoring with Redis cache updates

## Project Structure

```
swim/
├── cmd/swim/          # Main application entry point
├── internal/
│   ├── connector/     # Cloud provider interface and implementations
│   │   └── scaleway/  # Scaleway-specific connector
│   ├── logger/        # Logging utilities
│   └── redis/         # Redis client for queue and cache
├── cloud-init.yml     # Default cloud-init configuration
├── go.mod             # Go module dependencies
└── README.md
```

## Building

```bash
go build -o swim ./cmd/swim
```

## Usage

### Interactive Mode
```bash
./swim --interactive '{"ServerName":"test-vm","ServerType":"DEV1-S","SecurityGroupName":"default","ImageID":"<image-id>","WebUsername":"user","WebLabID":1,"TTLMinutes":60,"CloudInitFile":"cloud-init.yml"}'
```

### Service Mode (Redis Queue)
```bash
./swim --redis localhost:6379
```

### Options
- `--interactive <json>` - Run in one-shot mode with JSON payload
- `--redis <connection>` - Run in service mode with Redis connection string
- `--verbose` - Enable verbose logging (info level)
- `--dry-run` - Validate without creating real instances

## Environment Variables

Required Scaleway credentials:
- `SCW_ACCESS_KEY` - Scaleway API access key
- `SCW_SECRET_KEY` - Scaleway API secret key
- `SCW_ORGANIZATION_ID` - Scaleway organization ID
- `SCW_PROJECT_ID` - Scaleway project ID
- `SCW_DEFAULT_ZONE` - Default zone (e.g., `fr-par-1`)

Optional:
- `REDIS_PASSWORD` - Redis authentication password

## Provision Request Format

The JSON payload for provisioning must include:

```json
{
  "ServerName": "my-test-server",
  "ServerType": "DEV1-S",
  "SecurityGroupName": "default",
  "ImageID": "ubuntu_jammy",
  "WebUsername": "student",
  "WebLabID": 123,
  "TTLMinutes": 60,
  "CloudInitFile": "cloud-init.yml"
}
```

### Required Fields

- `ServerName` - Name for the server instance
- `ServerType` - Scaleway instance type (e.g., `DEV1-S`, `DEV1-M`)
- `SecurityGroupName` - Name of the security group to use
- `ImageID` - Scaleway image ID or name
- `WebUsername` - Username for tagging (tracking purposes)
- `WebLabID` - Lab ID for tagging (tracking purposes)
- `TTLMinutes` - Time-to-live in minutes (server lifetime)
- `CloudInitFile` - Path to cloud-init configuration file

## Server State Caching

When running in service mode, server states are cached in Redis with the following information:

- **Key**: `swim:server:<server-id>`
- **TTL**: 24 hours
- **Data**:
  - `id` - Server ID
  - `name` - Server name
  - `ipv6` - IPv6 address
  - `state` - Current state (`running`, `starting`, `deleted-error`, etc.)
  - `provisioned_at` - Timestamp when provisioned
  - `deletion_at` - Calculated deletion timestamp based on TTL

### Error Handling

If any error occurs during provisioning or state polling:
1. The server is automatically deleted from Scaleway
2. State is set to `deleted-error` in Redis cache
3. Error details are logged