# Kurokku ESP Server

## Overview

Go HTTP server that manages ESP32-powered LED display devices. Serves widget instructions to polling devices based on a playlist model with server-owned timing. Sister project to `led-kurokku-esp` (device firmware).

## Build & Run

```bash
go build -o kurokku-esp-server ./cmd/server
./kurokku-esp-server
```

Requires Redis for all state (config persistence and ephemeral playlist state). Configure Redis with `appendonly yes` for durability.

### Environment Variables

| Var | Default | Description |
|-----|---------|-------------|
| `KUROKKU_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `KUROKKU_REDIS_ADDR` | `localhost:6379` | Redis address |

## Architecture

### All-Redis Storage

Everything lives in Redis — device config, playlists, and ephemeral playlist state. No SQLite, no CGo dependency. Redis keys:

- `kurokku:device:{id}` — device JSON
- `kurokku:devices` — set of device IDs (index)
- `kurokku:playlist:{id}` — playlist JSON (includes entries)
- `kurokku:playlists` — set of playlist IDs (index)
- `device:{id}:playlist_state` — ephemeral cursor (index, started_at, version)
- `kurokku:alerts` — list of pending alert texts
- `kurokku:alert_notifications` — pub/sub channel for alert triggers

### Playlist Model

Each device is assigned a playlist — an ordered list of widget entries, each with a duration. Every widget (including clock) has a finite duration. The server cycles through entries, advancing when the duration elapses.

### Server-Owned Timing

The server controls when to advance the playlist. On poll:
1. If playlist version changed → reset to entry 0, send instruction
2. If current entry's duration elapsed → advance to next entry (wrapping), send instruction
3. Otherwise → respond with brightness/poll_ms only (no instruction)

### Alert Flow

External weather service pushes alerts onto `kurokku:alerts` (Redis list) and publishes to `kurokku:alert_notifications` (Redis pub/sub). On notification, server resets all devices to their alert widget position. The alert widget reads from the Redis list at resolve time — if no alerts exist, it falls back to clock for its duration.

### API

#### Device Polling (ESP firmware contract)
```
GET /api/v1/devices/{device_id}/instruction?display_type=max7219
```

#### Admin CRUD
```
GET/PUT/DELETE /api/v1/admin/devices/{device_id}
GET            /api/v1/admin/devices
GET/PUT/DELETE /api/v1/admin/playlists/{playlist_id}
GET            /api/v1/admin/playlists
```

### Modules

- `cmd/server/` — entrypoint, wiring
- `internal/api/` — HTTP handlers for device polling and admin CRUD
- `internal/config/` — environment-based configuration
- `internal/model/` — data types (Device, Playlist, PlaylistEntry, Widget, ServerResponse)
- `internal/playlist/` — Resolver: evaluates playlist state per device on each poll
- `internal/store/` — Redis-backed persistence for devices and playlists
- `internal/alert/` — Redis pub/sub listener for weather alerts

## Dependencies

- `github.com/redis/go-redis/v9` — Redis client
- Standard library `net/http`, `log/slog`
