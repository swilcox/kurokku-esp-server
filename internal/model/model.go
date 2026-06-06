package model

import "time"

// DisplayType represents the type of LED display hardware.
type DisplayType string

const (
	DisplayMAX7219 DisplayType = "max7219"
	DisplayTM1637  DisplayType = "tm1637"
)

// Device represents a registered ESP32 display device.
type Device struct {
	ID              string      `json:"id" db:"id"`
	Name            string      `json:"name" db:"name"`
	DisplayType     DisplayType `json:"display_type" db:"display_type"`
	Location        string      `json:"location" db:"location"`
	Brightness      int         `json:"brightness" db:"brightness"`
	Latitude        *float64    `json:"latitude,omitempty" db:"latitude"`
	Longitude       *float64    `json:"longitude,omitempty" db:"longitude"`
	BrightnessDay   int         `json:"brightness_day" db:"brightness_day"`
	BrightnessNight int         `json:"brightness_night" db:"brightness_night"`
	PollMs          int         `json:"poll_ms" db:"poll_ms"`
	PlaylistID      string      `json:"playlist_id" db:"playlist_id"`
	// LowPriorityAlertCron / LowPriorityThreshold override the server-wide
	// low-priority alert gating when set. Nil falls back to the Resolver default.
	LowPriorityAlertCron *string `json:"low_priority_alert_cron,omitempty" db:"low_priority_alert_cron"`
	LowPriorityThreshold *int    `json:"low_priority_threshold,omitempty" db:"low_priority_threshold"`
	// SyslogHost / Tz are pushed to the device as a remote `config` update in
	// the poll response, so the firmware can persist them to NVS without serial
	// re-provisioning. Nil = not managed (the firmware keeps its current value).
	// SyslogHost is "host:port" for UDP syslog; Tz is a POSIX TZ string.
	SyslogHost *string   `json:"syslog_host,omitempty" db:"syslog_host"`
	Tz         *string   `json:"tz,omitempty" db:"tz"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

// Playlist is an ordered list of widget entries that cycle on a device.
type Playlist struct {
	ID        string          `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Entries   []PlaylistEntry `json:"entries"`
	Version   int             `json:"version" db:"version"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

// PlaylistEntry is a single widget in a playlist with a display duration.
type PlaylistEntry struct {
	ID          string `json:"id" db:"id"`
	PlaylistID  string `json:"playlist_id" db:"playlist_id"`
	Position    int    `json:"position" db:"position"`
	DurationSec int    `json:"duration_secs" db:"duration_secs"`
	CronExpr    string `json:"cron_expr,omitempty" db:"cron_expr"`
	Widget      Widget `json:"widget"`
}

// Widget is a display instruction. The Type field determines which
// sub-fields are relevant.
type Widget struct {
	Type string `json:"type"`

	// clock
	Format24h bool `json:"format_24h,omitempty"`

	// message
	Text          string `json:"text,omitempty"`
	ScrollSpeedMs int    `json:"scroll_speed_ms,omitempty"`
	Repeats       int    `json:"repeats,omitempty"`
	RedisKey      string `json:"redis_key,omitempty"`

	// animation
	Animation string `json:"animation,omitempty"`

	// alert — content populated at resolve time from redis
	AlertSeverity string `json:"alert_severity,omitempty"`

	// raw_pixel
	PixelData []byte `json:"data,omitempty"`

	// raw_segment
	Segments []uint16 `json:"segments,omitempty"`
	Colon    bool     `json:"colon,omitempty"`
}

// ServerResponse is the envelope returned to polling devices.
// Matches the contract in the ESP firmware's CLAUDE.md.
type ServerResponse struct {
	Instruction *Instruction  `json:"instruction,omitempty"`
	Brightness  *int          `json:"brightness,omitempty"`
	PollMs      *int          `json:"poll_interval_ms,omitempty"`
	Config      *DeviceConfig `json:"config,omitempty"`
}

// DeviceConfig is the `config` block in a poll response. It carries persisted
// device settings the firmware applies live and stores in NVS (syslog target,
// timezone), so they can change without re-provisioning over serial. The
// firmware deduplicates, so it's safe to send on every poll. A nil field is
// omitted from the JSON (the firmware leaves it unchanged); an explicit empty
// SyslogHost ("") tells the firmware to disable syslog.
type DeviceConfig struct {
	SyslogHost *string `json:"syslog_host,omitempty"`
	Tz         *string `json:"tz,omitempty"`
}

// Instruction is the wire format for a widget instruction sent to devices.
type Instruction struct {
	Type          string   `json:"type"`
	Format24h     *bool    `json:"format_24h,omitempty"`
	Text          string   `json:"text,omitempty"`
	ScrollSpeedMs int      `json:"scroll_speed_ms,omitempty"`
	Repeats       int      `json:"repeats,omitempty"`
	Animation     string   `json:"animation,omitempty"`
	DurationSecs  int      `json:"duration_secs,omitempty"`
	Data          []byte   `json:"data,omitempty"`
	Segments      []uint16 `json:"segments,omitempty"`
	Colon         bool     `json:"colon,omitempty"`
	URL           string   `json:"url,omitempty"`
}

// PlaylistState is the ephemeral per-device state stored in Redis.
type PlaylistState struct {
	PlaylistVersion int       `json:"playlist_version"`
	CurrentIndex    int       `json:"current_index"`
	StartedAt       time.Time `json:"started_at"`
	ForceAdvance    bool      `json:"force_advance,omitempty"`
}

// DeviceStatus captures the most recent observed state of a device, updated
// on every poll. Stored in Redis at device:{id}:status.
type DeviceStatus struct {
	LastSeen          time.Time    `json:"last_seen"`
	LastRemoteAddr    string       `json:"last_remote_addr,omitempty"`
	LastInstruction   *Instruction `json:"last_instruction,omitempty"`
	LastInstructionAt time.Time    `json:"last_instruction_at,omitempty"`
	FirmwareVersion   string       `json:"firmware_version,omitempty"`
	LastOtaAt         time.Time    `json:"last_ota_at,omitempty"`
	LastOtaURL        string       `json:"last_ota_url,omitempty"`
}

// PendingOTA is an admin-queued OTA command for a device. Stored in Redis
// at kurokku:ota_pending:{device_id} with a short TTL. Consumed (GETDEL)
// by the device poll handler and converted to an "ota" instruction.
type PendingOTA struct {
	URL      string    `json:"url"`
	QueuedAt time.Time `json:"queued_at"`
}

// AlertConfig matches the alert structure in led-kurokku-go.
// Stored in Redis as kurokku:alert:<id>.
type AlertConfig struct {
	ID                 string `json:"id"`
	Message            string `json:"message"`
	Priority           int    `json:"priority"`
	DisplayDurationStr string `json:"display_duration"`
	DeleteAfterDisplay bool   `json:"delete_after_display"`
}
