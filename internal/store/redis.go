package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/alert"
	"github.com/swilcox/kurokku-esp-server/internal/model"
)

type RedisStore struct {
	rdb *redis.Client
}

func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb}
}

func deviceKey(id string) string     { return fmt.Sprintf("kurokku:device:%s", id) }
func playlistKey(id string) string   { return fmt.Sprintf("kurokku:playlist:%s", id) }
func statusKey(id string) string     { return fmt.Sprintf("device:%s:status", id) }
func otaPendingKey(id string) string { return fmt.Sprintf("kurokku:ota_pending:%s", id) }
func firmwareKey(displayType string) string {
	return fmt.Sprintf("kurokku:firmware:%s", displayType)
}

func imageMetaKey(id string) string  { return fmt.Sprintf("kurokku:image:%s", id) }
func imageQOIKey(id string) string   { return fmt.Sprintf("kurokku:image:%s:qoi", id) }
func imageThumbKey(id string) string { return fmt.Sprintf("kurokku:image:%s:thumb", id) }

func soundMetaKey(id string) string { return fmt.Sprintf("kurokku:sound:%s", id) }
func soundWAVKey(id string) string  { return fmt.Sprintf("kurokku:sound:%s:wav", id) }

const imageIndexKey = "kurokku:images"
const soundIndexKey = "kurokku:sounds"
const deviceIndexKey = "kurokku:devices"
const playlistIndexKey = "kurokku:playlists"

const deviceStatusTTL = 7 * 24 * time.Hour
const otaPendingTTL = 10 * time.Minute

// --- Devices ---

func (s *RedisStore) GetDevice(ctx context.Context, id string) (*model.Device, error) {
	data, err := s.rdb.Get(ctx, deviceKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d model.Device
	return &d, json.Unmarshal(data, &d)
}

func (s *RedisStore) ListDevices(ctx context.Context) ([]model.Device, error) {
	ids, err := s.rdb.SMembers(ctx, deviceIndexKey).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = deviceKey(id)
	}

	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	var devices []model.Device
	for _, v := range vals {
		if v == nil {
			continue
		}
		var d model.Device
		if err := json.Unmarshal([]byte(v.(string)), &d); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (s *RedisStore) UpsertDevice(ctx context.Context, d *model.Device) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, deviceKey(d.ID), data, 0)
	pipe.SAdd(ctx, deviceIndexKey, d.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisStore) DeleteDevice(ctx context.Context, id string) error {
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, deviceKey(id))
	pipe.Del(ctx, statusKey(id))
	pipe.Del(ctx, otaPendingKey(id))
	pipe.SRem(ctx, deviceIndexKey, id)
	_, err := pipe.Exec(ctx)
	return err
}

// --- OTA ---

// QueueOTA stores a pending OTA command for a device. The command is
// consumed (GETDEL) on the device's next poll. Overwrites any prior
// pending command.
func (s *RedisStore) QueueOTA(ctx context.Context, deviceID, url string) error {
	data, err := json.Marshal(&model.PendingOTA{URL: url, QueuedAt: time.Now()})
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, otaPendingKey(deviceID), data, otaPendingTTL).Err()
}

// PeekOTA returns the pending OTA for a device without consuming it, or
// nil if none is queued.
func (s *RedisStore) PeekOTA(ctx context.Context, deviceID string) (*model.PendingOTA, error) {
	data, err := s.rdb.Get(ctx, otaPendingKey(deviceID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p model.PendingOTA
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PopOTA atomically reads and deletes the pending OTA for a device, so it
// is dispatched exactly once. Returns nil if none is queued.
func (s *RedisStore) PopOTA(ctx context.Context, deviceID string) (*model.PendingOTA, error) {
	data, err := s.rdb.GetDel(ctx, otaPendingKey(deviceID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p model.PendingOTA
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CancelOTA removes any pending OTA for a device.
func (s *RedisStore) CancelOTA(ctx context.Context, deviceID string) error {
	return s.rdb.Del(ctx, otaPendingKey(deviceID)).Err()
}

// --- Firmware URLs ---

// GetFirmwareURL returns the saved default firmware URL for a display
// type, or the empty string if none is set.
func (s *RedisStore) GetFirmwareURL(ctx context.Context, displayType string) (string, error) {
	v, err := s.rdb.Get(ctx, firmwareKey(displayType)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

// SetFirmwareURL stores a default firmware URL for a display type. An
// empty url clears the entry.
func (s *RedisStore) SetFirmwareURL(ctx context.Context, displayType, url string) error {
	if url == "" {
		return s.rdb.Del(ctx, firmwareKey(displayType)).Err()
	}
	return s.rdb.Set(ctx, firmwareKey(displayType), url, 0).Err()
}

// --- Alerts ---

// ListActiveAlerts returns all currently stored alerts (kurokku:alert:*),
// regardless of cron filtering. Useful for admin UIs that want to see the
// full alert queue.
func (s *RedisStore) ListActiveAlerts(ctx context.Context) ([]model.AlertConfig, error) {
	return alert.FetchAlerts(ctx, s.rdb)
}

// --- Device Status ---

func (s *RedisStore) GetDeviceStatus(ctx context.Context, id string) (*model.DeviceStatus, error) {
	data, err := s.rdb.Get(ctx, statusKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st model.DeviceStatus
	return &st, json.Unmarshal(data, &st)
}

func (s *RedisStore) SetDeviceStatus(ctx context.Context, id string, st *model.DeviceStatus) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, statusKey(id), data, deviceStatusTTL).Err()
}

// --- Playlists ---

func (s *RedisStore) GetPlaylist(ctx context.Context, id string) (*model.Playlist, error) {
	data, err := s.rdb.Get(ctx, playlistKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p model.Playlist
	return &p, json.Unmarshal(data, &p)
}

func (s *RedisStore) ListPlaylists(ctx context.Context) ([]model.Playlist, error) {
	ids, err := s.rdb.SMembers(ctx, playlistIndexKey).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = playlistKey(id)
	}

	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	var playlists []model.Playlist
	for _, v := range vals {
		if v == nil {
			continue
		}
		var p model.Playlist
		if err := json.Unmarshal([]byte(v.(string)), &p); err != nil {
			return nil, err
		}
		playlists = append(playlists, p)
	}
	return playlists, nil
}

func (s *RedisStore) UpsertPlaylist(ctx context.Context, p *model.Playlist) error {
	existing, err := s.GetPlaylist(ctx, p.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		p.Version = existing.Version + 1
	} else {
		p.Version = 1
	}

	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, playlistKey(p.ID), data, 0)
	pipe.SAdd(ctx, playlistIndexKey, p.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisStore) DeletePlaylist(ctx context.Context, id string) error {
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, playlistKey(id))
	pipe.SRem(ctx, playlistIndexKey, id)
	_, err := pipe.Exec(ctx)
	return err
}

// --- Images ---

// SaveImage stores a library image: metadata, QOI data and thumbnail.
func (s *RedisStore) SaveImage(ctx context.Context, img *model.Image, qoi, thumb []byte) error {
	data, err := json.Marshal(img)
	if err != nil {
		return err
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, imageMetaKey(img.ID), data, 0)
		p.Set(ctx, imageQOIKey(img.ID), qoi, 0)
		p.Set(ctx, imageThumbKey(img.ID), thumb, 0)
		p.SAdd(ctx, imageIndexKey, img.ID)
		return nil
	})
	return err
}

func (s *RedisStore) GetImage(ctx context.Context, id string) (*model.Image, error) {
	data, err := s.rdb.Get(ctx, imageMetaKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var img model.Image
	return &img, json.Unmarshal(data, &img)
}

// ListImages returns library images, newest first.
func (s *RedisStore) ListImages(ctx context.Context) ([]model.Image, error) {
	ids, err := s.rdb.SMembers(ctx, imageIndexKey).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = imageMetaKey(id)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	var images []model.Image
	for _, v := range vals {
		if v == nil {
			continue
		}
		var img model.Image
		if err := json.Unmarshal([]byte(v.(string)), &img); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].CreatedAt.After(images[j].CreatedAt) })
	return images, nil
}

// GetImageData returns the QOI data (thumb=false) or JPEG thumbnail, or nil
// if the image doesn't exist.
func (s *RedisStore) GetImageData(ctx context.Context, id string, thumb bool) ([]byte, error) {
	key := imageQOIKey(id)
	if thumb {
		key = imageThumbKey(id)
	}
	data, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return data, err
}

func (s *RedisStore) DeleteImage(ctx context.Context, id string) error {
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, imageMetaKey(id), imageQOIKey(id), imageThumbKey(id))
		p.SRem(ctx, imageIndexKey, id)
		return nil
	})
	return err
}

// --- Sounds ---

// SaveSound stores a library sound: metadata and WAV data.
func (s *RedisStore) SaveSound(ctx context.Context, snd *model.Sound, wav []byte) error {
	data, err := json.Marshal(snd)
	if err != nil {
		return err
	}
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, soundMetaKey(snd.ID), data, 0)
		p.Set(ctx, soundWAVKey(snd.ID), wav, 0)
		p.SAdd(ctx, soundIndexKey, snd.ID)
		return nil
	})
	return err
}

// ListSounds returns library sounds, newest first.
func (s *RedisStore) ListSounds(ctx context.Context) ([]model.Sound, error) {
	ids, err := s.rdb.SMembers(ctx, soundIndexKey).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = soundMetaKey(id)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	var sounds []model.Sound
	for _, v := range vals {
		if v == nil {
			continue
		}
		var snd model.Sound
		if err := json.Unmarshal([]byte(v.(string)), &snd); err != nil {
			return nil, err
		}
		sounds = append(sounds, snd)
	}
	sort.Slice(sounds, func(i, j int) bool { return sounds[i].CreatedAt.After(sounds[j].CreatedAt) })
	return sounds, nil
}

// GetSoundData returns a sound's WAV data, or nil if it doesn't exist.
func (s *RedisStore) GetSoundData(ctx context.Context, id string) ([]byte, error) {
	data, err := s.rdb.Get(ctx, soundWAVKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return data, err
}

func (s *RedisStore) DeleteSound(ctx context.Context, id string) error {
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, soundMetaKey(id), soundWAVKey(id))
		p.SRem(ctx, soundIndexKey, id)
		return nil
	})
	return err
}
