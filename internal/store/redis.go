package store

import (
	"context"
	"encoding/json"
	"fmt"
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

func deviceKey(id string) string    { return fmt.Sprintf("kurokku:device:%s", id) }
func playlistKey(id string) string  { return fmt.Sprintf("kurokku:playlist:%s", id) }
func statusKey(id string) string    { return fmt.Sprintf("device:%s:status", id) }
const deviceIndexKey = "kurokku:devices"
const playlistIndexKey = "kurokku:playlists"

const deviceStatusTTL = 7 * 24 * time.Hour

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
	pipe.SRem(ctx, deviceIndexKey, id)
	_, err := pipe.Exec(ctx)
	return err
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
