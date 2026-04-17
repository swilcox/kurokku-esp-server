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
| `KUROKKU_TM1637_ALERT_SCROLL_SPEED_MS` | `150` | Scroll speed (ms/col) for alert messages on tm1637 devices |
| `KUROKKU_TM1637_ALERT_REPEATS` | `3` | Number of times alert messages repeat on tm1637 devices |

## Architecture

### All-Redis Storage

Everything lives in Redis — device config, playlists, and ephemeral playlist state. No SQLite, no CGo dependency. Redis keys:

- `kurokku:device:{id}` — device JSON
- `kurokku:devices` — set of device IDs (index)
- `kurokku:playlist:{id}` — playlist JSON (includes entries)
- `kurokku:playlists` — set of playlist IDs (index)
- `device:{id}:playlist_state` — ephemeral cursor (index, started_at, version)
- `kurokku:alert:<id>` — alert JSON (AlertConfig with message, priority, display_duration)
- `kurokku:ota_pending:{id}` — admin-queued OTA command (JSON `{url, queued_at}`), TTL 10 min, consumed via GETDEL on next device poll
- `kurokku:firmware:{display_type}` — default firmware URL for a display type (admin-editable)

### Playlist Model

Each device is assigned a playlist — an ordered list of widget entries, each with a duration. Every widget (including clock) has a finite duration. The server cycles through entries, advancing when the duration elapses.

### Server-Owned Timing

The server controls when to advance the playlist. On poll:
1. If playlist version changed → reset to entry 0, send instruction
2. If current entry's duration elapsed → advance to next entry (wrapping), send instruction
3. Otherwise → respond with brightness/poll_ms only (no instruction)

### Alert Flow

Alerts are stored as individual Redis keys at `kurokku:alert:<id>`, each containing a JSON `AlertConfig` (id, message, priority, display_duration, delete_after_display). The server detects changes via Redis keyspace notifications and resets all devices to their alert widget position. Multiple alerts are sorted by priority and concatenated. Low-priority alerts can be filtered by a cron schedule. If no alerts are active (after filtering), the alert entry is skipped and the resolver advances to the next playlist entry. The [nalssi](https://github.com/swilcox/nalssi) weather service can push temperature and alerts automatically.

### OTA Flow

OTA is an out-of-band one-shot command, not a playlist entry. Admin queues via the UI or JSON API; the device poll handler calls `PopOTA` (GETDEL) before the resolver. If pending, the server returns `{type:"ota", url}` and skips playlist resolution. The device downloads, flashes, and reboots; on next poll the pending key is gone and normal playlist resumes. If the device is offline, the TTL drops the command after ~10 minutes.

Devices report their running firmware version on each poll via `?firmware_version=...`, captured into `DeviceStatus.FirmwareVersion` and shown in the admin UI. Note: the firmware sends `+` literally in the semver build-metadata separator, which Go's `url.Query()` decodes as space, so the handler normalizes spaces back to `+`.

### API

#### Device Polling (ESP firmware contract)
```
GET /api/v1/devices/{device_id}/instruction?display_type=max7219&firmware_version=0.1.0+abc123
```

#### Admin CRUD
```
GET/PUT/DELETE /api/v1/admin/devices/{device_id}
GET            /api/v1/admin/devices
GET/PUT/DELETE /api/v1/admin/playlists/{playlist_id}
GET            /api/v1/admin/playlists
```

#### OTA
```
POST   /api/v1/admin/devices/{device_id}/ota    body {"url": "..."} — queue OTA (10 min TTL)
DELETE /api/v1/admin/devices/{device_id}/ota    — cancel pending OTA
GET    /api/v1/admin/firmware/{display_type}    — get saved default URL
PUT    /api/v1/admin/firmware/{display_type}    body {"url": "..."} — set/clear default URL
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
