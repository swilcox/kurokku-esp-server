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
	ID          string      `json:"id" db:"id"`
	Name        string      `json:"name" db:"name"`
	DisplayType DisplayType `json:"display_type" db:"display_type"`
	Location    string      `json:"location" db:"location"`
	Brightness      int         `json:"brightness" db:"brightness"`
	Latitude        *float64    `json:"latitude,omitempty" db:"latitude"`
	Longitude       *float64    `json:"longitude,omitempty" db:"longitude"`
	BrightnessDay   int         `json:"brightness_day" db:"brightness_day"`
	BrightnessNight int         `json:"brightness_night" db:"brightness_night"`
	PollMs          int         `json:"poll_ms" db:"poll_ms"`
	PlaylistID  string      `json:"playlist_id" db:"playlist_id"`
	CreatedAt   time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at" db:"updated_at"`
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
	Instruction *Instruction `json:"instruction,omitempty"`
	Brightness  *int         `json:"brightness,omitempty"`
	PollMs      *int         `json:"poll_interval_ms,omitempty"`
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

// AlertConfig matches the alert structure in led-kurokku-go.
// Stored in Redis as kurokku:alert:<id>.
type AlertConfig struct {
	ID                 string  `json:"id"`
	Message            string  `json:"message"`
	Priority           int     `json:"priority"`
	DisplayDurationStr string  `json:"display_duration"`
	DeleteAfterDisplay bool    `json:"delete_after_display"`
}
