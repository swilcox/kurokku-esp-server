# Kurokku ESP Server

HTTP server for managing ESP32-powered LED display devices. Serves widget instructions to polling devices based on a playlist model with server-owned timing.

## Quick Start

```bash
docker compose up -d
```

The server will be available at `http://localhost:8080`.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `KUROKKU_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `KUROKKU_REDIS_ADDR` | `localhost:6379` | Redis/Valkey address |

### Recommended Redis/Valkey Configuration

All state (device config, playlists, and ephemeral playlist cursors) lives in Redis. Persistence must be enabled or you'll lose device and playlist configuration on restart.

The included `docker-compose.yml` runs [Valkey](https://valkey.io/) (Redis-compatible) with these recommended settings:

```
# AOF persistence — logs every write, replayed on restart
appendonly yes
appendfsync everysec

# RDB snapshots as a secondary safety net
save 900 1
save 300 10
save 60 10000
```

`appendonly yes` with `appendfsync everysec` gives you at most 1 second of data loss on a crash — fine for a home server managing clock playlists. The RDB snapshots provide a fallback if the AOF file is ever corrupted.

If running Redis/Valkey outside of Docker, add these directives to your `redis.conf` or `valkey.conf`.

## Development

```bash
# Build and run locally (requires Go 1.25+ and a running Redis/Valkey instance)
go build -o kurokku-esp-server ./cmd/server
./kurokku-esp-server

# Or with Docker
docker compose up --build
```

## API

### Device Polling

Used by ESP32 firmware:

```
GET /api/v1/devices/{device_id}/instruction?display_type=max7219
```

### Admin CRUD

```
GET    /api/v1/admin/devices              # list all devices
GET    /api/v1/admin/devices/{device_id}  # get device
PUT    /api/v1/admin/devices/{device_id}  # create/update device
DELETE /api/v1/admin/devices/{device_id}  # delete device

GET    /api/v1/admin/playlists                # list all playlists
GET    /api/v1/admin/playlists/{playlist_id}  # get playlist
PUT    /api/v1/admin/playlists/{playlist_id}  # create/update playlist
DELETE /api/v1/admin/playlists/{playlist_id}  # delete playlist
```

### Example: Create a Playlist and Device

```bash
# Create a playlist that cycles: 30s clock → 10s weather alert check → 15s animation
curl -X PUT http://localhost:8080/api/v1/admin/playlists/living-room \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Living Room",
    "entries": [
      {"id": "e1", "position": 0, "duration_secs": 30, "widget": {"type": "clock", "format_24h": true}},
      {"id": "e2", "position": 1, "duration_secs": 10, "widget": {"type": "alert"}},
      {"id": "e3", "position": 2, "duration_secs": 15, "widget": {"type": "animation", "animation": "pong"}}
    ]
  }'

# Register a device using that playlist
curl -X PUT http://localhost:8080/api/v1/admin/devices/esp32-001 \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Living Room Clock",
    "display_type": "max7219",
    "location": "living room",
    "brightness": 8,
    "poll_ms": 5000,
    "playlist_id": "living-room"
  }'
```

## Alert Integration

Alerts are stored as individual Redis keys at `kurokku:alert:<id>`, each containing a JSON object:

```json
{
  "id": "tornado-warning",
  "message": "TORNADO WARNING - Take shelter",
  "priority": 1,
  "display_duration": "15s",
  "delete_after_display": false
}
```

The server detects alert changes via Redis keyspace notifications and resets all device playlists to their alert widget position so alerts are displayed promptly. When multiple alerts are active, they are sorted by priority (lower = more urgent) and concatenated into a single scrolling message.

```bash
# Example: push a weather alert via redis-cli
redis-cli SET kurokku:alert:tornado-warning '{"id":"tornado-warning","message":"TORNADO WARNING - Take shelter","priority":1,"display_duration":"15s","delete_after_display":false}'
```

The [nalssi](https://github.com/swilcox/nalssi) weather service can push temperature data and weather alerts to kurokku automatically.
