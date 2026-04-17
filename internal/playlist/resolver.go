package playlist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
	"github.com/swilcox/kurokku-esp-server/internal/alert"
	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/solar"
)

// Resolver determines what a device should display based on its
// playlist and ephemeral Redis state.
type Resolver struct {
	rdb                    *redis.Client
	logger                 *slog.Logger
	lowPriorityCron        string
	lowPriorityThreshold   int
	tm1637AlertScrollSpeed int
	tm1637AlertRepeats     int
}

type Options struct {
	LowPriorityCron        string
	LowPriorityThreshold   int
	TM1637AlertScrollSpeed int
	TM1637AlertRepeats     int
}

func NewResolver(rdb *redis.Client, logger *slog.Logger, opts Options) *Resolver {
	return &Resolver{
		rdb:                    rdb,
		logger:                 logger,
		lowPriorityCron:        opts.LowPriorityCron,
		lowPriorityThreshold:   opts.LowPriorityThreshold,
		tm1637AlertScrollSpeed: opts.TM1637AlertScrollSpeed,
		tm1637AlertRepeats:     opts.TM1637AlertRepeats,
	}
}

func stateKey(deviceID string) string {
	return fmt.Sprintf("device:%s:playlist_state", deviceID)
}


// Resolve returns the instruction (if any) for a device poll.
// Returns nil instruction if the current widget's duration hasn't elapsed.
//
// Indices in PlaylistState always refer to the full entries list so that
// ResetToAlert and Resolve agree on positions. Cron-inactive and empty-alert
// entries are skipped during the advancement loop.
func (r *Resolver) Resolve(ctx context.Context, device *model.Device, playlist *model.Playlist) (*model.ServerResponse, error) {
	log := r.logger.With("device", device.ID)

	now := time.Now()
	brightness := effectiveBrightness(device, now)

	if playlist == nil || len(playlist.Entries) == 0 {
		log.Debug("no playlist or empty entries, returning default")
		return r.defaultResponse(device, brightness), nil
	}

	n := len(playlist.Entries)
	log.Debug("resolving playlist", "playlist", playlist.ID, "version", playlist.Version, "entries", n)

	state, err := r.getState(ctx, device.ID)
	if err != nil {
		return nil, err
	}

	advanced := false

	if state == nil || state.PlaylistVersion != playlist.Version {
		reason := "no prior state"
		if state != nil {
			reason = fmt.Sprintf("version changed %d->%d", state.PlaylistVersion, playlist.Version)
		}
		log.Debug("resetting playlist state", "reason", reason)
		state = &model.PlaylistState{
			PlaylistVersion: playlist.Version,
			CurrentIndex:    0,
			StartedAt:       now,
		}
		advanced = true
	} else if state.ForceAdvance {
		log.Debug("force advance triggered (alert reset)", "index", state.CurrentIndex)
		state.ForceAdvance = false
		advanced = true
	} else {
		idx := state.CurrentIndex % n
		entry := playlist.Entries[idx]
		elapsed := now.Sub(state.StartedAt)
		log.Debug("checking duration",
			"index", idx,
			"widget_type", entry.Widget.Type,
			"elapsed_sec", int(elapsed.Seconds()),
			"duration_sec", entry.DurationSec,
		)
		if elapsed >= time.Duration(entry.DurationSec)*time.Second {
			state.CurrentIndex = (idx + 1) % n
			state.StartedAt = now
			advanced = true
			log.Debug("duration elapsed, advancing", "new_index", state.CurrentIndex)
		}
	}

	if !advanced {
		if err := r.setState(ctx, device.ID, state); err != nil {
			return nil, err
		}
		log.Debug("no change, returning brightness/poll only")
		return r.noChangeResponse(device, brightness), nil
	}

	// Walk entries starting from CurrentIndex, skipping cron-inactive
	// entries and entries that return nil instructions (e.g. alert with
	// no active alerts). Stop after checking every entry once.
	var instruction *model.Instruction
	for range n {
		idx := state.CurrentIndex % n
		entry := playlist.Entries[idx]

		if entry.CronExpr != "" && !cronMatchesNow(entry.CronExpr, now) {
			log.Debug("skipping entry (cron inactive)", "index", idx, "widget_type", entry.Widget.Type, "cron", entry.CronExpr)
			state.CurrentIndex = (idx + 1) % n
			state.StartedAt = now
			continue
		}

		log.Debug("building instruction", "index", idx, "widget_type", entry.Widget.Type)
		inst, err := r.buildInstruction(ctx, device, &entry)
		if err != nil {
			return nil, err
		}
		if inst != nil {
			log.Debug("instruction built", "instruction_type", inst.Type, "duration_secs", inst.DurationSecs, "text", inst.Text)
			instruction = inst
			break
		}
		log.Debug("skipping entry (nil instruction)", "index", idx, "widget_type", entry.Widget.Type)
		state.CurrentIndex = (idx + 1) % n
		state.StartedAt = now
	}

	if err := r.setState(ctx, device.ID, state); err != nil {
		return nil, err
	}

	if instruction == nil {
		log.Debug("all entries skipped, returning default")
		return r.defaultResponse(device, brightness), nil
	}

	pollMs := device.PollMs
	return &model.ServerResponse{
		Instruction: instruction,
		Brightness:  &brightness,
		PollMs:      &pollMs,
	}, nil
}

// entryActive reports whether a playlist entry's cron schedule (if any)
// matches the given time. Entries with no cron expression are always active.
func entryActive(e *model.PlaylistEntry, now time.Time) bool {
	return e.CronExpr == "" || cronMatchesNow(e.CronExpr, now)
}

// cronMatchesNow checks if a cron expression would have fired within the
// last 60 seconds relative to now. This gives a 1-minute window for matching.
func cronMatchesNow(expr string, now time.Time) bool {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return true // invalid cron = always active
	}
	// Check if the schedule would fire in the current minute
	startOfMinute := now.Truncate(time.Minute)
	prev := startOfMinute.Add(-1 * time.Second)
	next := sched.Next(prev)
	return next.Equal(startOfMinute) || next.Before(startOfMinute.Add(time.Minute))
}

// ResetToAlert resets a device's playlist index to the alert widget position
// (or index 0 if no alert widget found). Called when a new alert arrives.
func (r *Resolver) ResetToAlert(ctx context.Context, deviceID string, playlist *model.Playlist) error {
	if playlist == nil {
		r.logger.Debug("reset to alert: no playlist", "device", deviceID)
		return nil
	}

	targetIndex := 0
	found := false
	for i, entry := range playlist.Entries {
		if entry.Widget.Type == "alert" {
			targetIndex = i
			found = true
			break
		}
	}

	r.logger.Debug("reset to alert",
		"device", deviceID,
		"playlist", playlist.ID,
		"alert_widget_found", found,
		"target_index", targetIndex,
	)

	state := &model.PlaylistState{
		PlaylistVersion: playlist.Version,
		CurrentIndex:    targetIndex,
		StartedAt:       time.Now(),
		ForceAdvance:    true,
	}
	return r.setState(ctx, deviceID, state)
}

func (r *Resolver) buildInstruction(ctx context.Context, device *model.Device, entry *model.PlaylistEntry) (*model.Instruction, error) {
	w := &entry.Widget

	switch w.Type {
	case "alert":
		return r.buildAlertInstruction(ctx, device, entry)
	case "message":
		return r.buildMessageInstruction(ctx, entry)
	default:
		return widgetToInstruction(w, entry.DurationSec), nil
	}
}

func (r *Resolver) buildMessageInstruction(ctx context.Context, entry *model.PlaylistEntry) (*model.Instruction, error) {
	inst := widgetToInstruction(&entry.Widget, entry.DurationSec)

	if entry.Widget.RedisKey != "" {
		val, err := r.rdb.Get(ctx, entry.Widget.RedisKey).Result()
		if err != nil && err != redis.Nil {
			return nil, fmt.Errorf("reading message key %s from redis: %w", entry.Widget.RedisKey, err)
		}
		if err == nil && val != "" {
			inst.Text = val
		}
	}

	return inst, nil
}

func (r *Resolver) buildAlertInstruction(ctx context.Context, device *model.Device, entry *model.PlaylistEntry) (*model.Instruction, error) {
	allAlerts, err := alert.FetchAlerts(ctx, r.rdb)
	if err != nil {
		return nil, fmt.Errorf("fetching alerts from redis: %w", err)
	}

	r.logger.Debug("fetched alerts from redis", "count", len(allAlerts))
	for i, a := range allAlerts {
		r.logger.Debug("alert", "index", i, "id", a.ID, "priority", a.Priority, "message", a.Message, "display_duration", a.DisplayDurationStr)
	}

	cronExpr, threshold := r.lowPriorityCron, r.lowPriorityThreshold
	if device != nil {
		if device.LowPriorityAlertCron != nil {
			cronExpr = *device.LowPriorityAlertCron
		}
		if device.LowPriorityThreshold != nil {
			threshold = *device.LowPriorityThreshold
		}
	}

	filtered := alert.FilterAndSortAlerts(allAlerts, cronExpr, threshold, time.Now())
	r.logger.Debug("filtered alerts", "before", len(allAlerts), "after", len(filtered), "low_priority_cron", cronExpr, "threshold", threshold)

	ai := alert.BuildAlertInstruction(filtered)

	if ai == nil {
		r.logger.Debug("no active alerts, skipping alert widget")
		return nil, nil
	}

	scrollSpeed := 50
	repeats := 2
	if device != nil && device.DisplayType == model.DisplayTM1637 {
		scrollSpeed = r.tm1637AlertScrollSpeed
		repeats = r.tm1637AlertRepeats
	}

	r.logger.Debug("alert instruction built", "text", ai.Text, "duration_sec", ai.DurationSec, "scroll_speed_ms", scrollSpeed, "repeats", repeats)
	return &model.Instruction{
		Type:          "message",
		Text:          ai.Text,
		ScrollSpeedMs: scrollSpeed,
		Repeats:       repeats,
		DurationSecs:  ai.DurationSec,
	}, nil
}

func widgetToInstruction(w *model.Widget, durationSec int) *model.Instruction {
	inst := &model.Instruction{
		Type:         w.Type,
		DurationSecs: durationSec,
	}

	switch w.Type {
	case "clock":
		f := w.Format24h
		inst.Format24h = &f
	case "message":
		inst.Text = w.Text
		inst.ScrollSpeedMs = w.ScrollSpeedMs
		if inst.ScrollSpeedMs == 0 {
			inst.ScrollSpeedMs = 50
		}
		inst.Repeats = w.Repeats
		if inst.Repeats == 0 {
			inst.Repeats = -1
		}
	case "animation":
		inst.Animation = w.Animation
	case "raw_pixel":
		inst.Data = w.PixelData
	case "raw_segment":
		inst.Segments = w.Segments
		inst.Colon = w.Colon
	}

	return inst
}

func effectiveBrightness(device *model.Device, now time.Time) int {
	if device.Latitude == nil || device.Longitude == nil {
		return device.Brightness
	}
	if solar.IsDaytime(now, *device.Latitude, *device.Longitude) {
		return device.BrightnessDay
	}
	return device.BrightnessNight
}

func (r *Resolver) defaultResponse(device *model.Device, brightness int) *model.ServerResponse {
	f := true
	pollMs := device.PollMs
	return &model.ServerResponse{
		Instruction: &model.Instruction{
			Type:      "clock",
			Format24h: &f,
		},
		Brightness: &brightness,
		PollMs:     &pollMs,
	}
}

func (r *Resolver) noChangeResponse(device *model.Device, brightness int) *model.ServerResponse {
	pollMs := device.PollMs
	return &model.ServerResponse{
		Brightness: &brightness,
		PollMs:     &pollMs,
	}
}

func (r *Resolver) getState(ctx context.Context, deviceID string) (*model.PlaylistState, error) {
	data, err := r.rdb.Get(ctx, stateKey(deviceID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading playlist state: %w", err)
	}

	var state model.PlaylistState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling playlist state: %w", err)
	}
	return &state, nil
}

func (r *Resolver) setState(ctx context.Context, deviceID string, state *model.PlaylistState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling playlist state: %w", err)
	}
	return r.rdb.Set(ctx, stateKey(deviceID), data, 24*time.Hour).Err()
}
