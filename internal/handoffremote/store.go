package handoffremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store persists handoff payloads keyed by handoff id.
type Store struct {
	rdb    *redis.Client
	prefix string
	ttl    time.Duration
}

func NewStore(redisURL, keyPrefix string, ttl time.Duration) (*Store, error) {
	redisURL = strings.TrimSpace(redisURL)
	if redisURL == "" {
		return nil, fmt.Errorf("handoffremote: redis url is required")
	}
	if keyPrefix == "" {
		keyPrefix = "agentfactory:handoff:"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("handoffremote: parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("handoffremote: redis ping: %w", err)
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Store{rdb: rdb, prefix: keyPrefix, ttl: ttl}, nil
}

func (s *Store) key(id string) string {
	return s.prefix + strings.TrimSpace(id)
}

func (s *Store) Put(ctx context.Context, id string, rec *Record) error {
	if rec == nil {
		return fmt.Errorf("handoffremote: nil record")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("handoffremote: empty handoff id")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key(id), b, s.ttl).Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("handoffremote: empty handoff id")
	}
	raw, err := s.rdb.Get(ctx, s.key(id)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return s.rdb.Del(ctx, s.key(id)).Err()
}

// Close releases the Redis client.
func (s *Store) Close() error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Close()
}
