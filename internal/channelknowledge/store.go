package channelknowledge

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore persists channel digest text and incremental harvest structures in Redis.
type RedisStore struct {
	client *redis.Client
}

// NewRedisStoreFromClient shares the bot's Redis connection.
func NewRedisStoreFromClient(c *redis.Client) *RedisStore {
	if c == nil {
		return nil
	}
	return &RedisStore{client: c}
}

// Delete removes all channel knowledge keys for the channel (full digest, recent, events, state).
func (r *RedisStore) Delete(ctx context.Context, channelID string) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	keys := []string{
		redisMarkdownKey(ch),
		redisMarkdownRecentKey(ch),
		redisEventsKey(ch),
		redisMsgHashKey(ch),
		redisStateKey(ch),
	}
	return r.client.Del(ctx, keys...).Err()
}

// Get returns stored full digest markdown for the channel, or empty if missing.
func (r *RedisStore) Get(ctx context.Context, channelID string) (string, error) {
	if r == nil || r.client == nil {
		return "", nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return "", nil
	}
	raw, err := r.client.Get(ctx, redisMarkdownKey(ch)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return raw, nil
}

// GetRecent returns the prompt-oriented recent digest markdown, or empty if missing.
func (r *RedisStore) GetRecent(ctx context.Context, channelID string) (string, error) {
	if r == nil || r.client == nil {
		return "", nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return "", nil
	}
	raw, err := r.client.Get(ctx, redisMarkdownRecentKey(ch)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return raw, nil
}

// Set stores full digest markdown and optional TTL (0 = no expiry).
func (r *RedisStore) Set(ctx context.Context, channelID, markdown string, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	md := strings.TrimSpace(markdown)
	key := redisMarkdownKey(ch)
	if md == "" {
		return r.client.Del(ctx, key).Err()
	}
	if ttl > 0 {
		return r.client.Set(ctx, key, md, ttl).Err()
	}
	return r.client.Set(ctx, key, md, 0).Err()
}

// SetRecent stores the recent digest markdown and optional TTL (0 = no expiry).
func (r *RedisStore) SetRecent(ctx context.Context, channelID, markdown string, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	md := strings.TrimSpace(markdown)
	key := redisMarkdownRecentKey(ch)
	if md == "" {
		return r.client.Del(ctx, key).Err()
	}
	if ttl > 0 {
		return r.client.Set(ctx, key, md, ttl).Err()
	}
	return r.client.Set(ctx, key, md, 0).Err()
}

// ClearIncrementalStore removes events, msg hash, and harvest state (markdown keys untouched).
func (r *RedisStore) ClearIncrementalStore(ctx context.Context, channelID string) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	return r.client.Del(ctx, redisEventsKey(ch), redisMsgHashKey(ch), redisStateKey(ch)).Err()
}

// ExpireIncrementalKeys sets TTL on all channel knowledge keys for the channel.
func (r *RedisStore) ExpireIncrementalKeys(ctx context.Context, channelID string, ttl time.Duration) error {
	if r == nil || r.client == nil || ttl <= 0 {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	keys := []string{
		redisMarkdownKey(ch),
		redisMarkdownRecentKey(ch),
		redisEventsKey(ch),
		redisMsgHashKey(ch),
		redisStateKey(ch),
	}
	pipe := r.client.Pipeline()
	for _, k := range keys {
		pipe.Expire(ctx, k, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}
