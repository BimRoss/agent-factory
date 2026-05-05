package channelknowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

// GetHarvestState loads incremental harvest state, or nil if missing.
func (r *RedisStore) GetHarvestState(ctx context.Context, channelID string) (*HarvestState, error) {
	if r == nil || r.client == nil {
		return nil, nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil, nil
	}
	raw, err := r.client.Get(ctx, redisStateKey(ch)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return unmarshalHarvestState(raw)
}

// SetHarvestState persists harvest state (empty HistoryWatermark deletes the key).
func (r *RedisStore) SetHarvestState(ctx context.Context, channelID string, s *HarvestState, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	s = normalizeHarvestState(s)
	if s == nil || strings.TrimSpace(s.HistoryWatermark) == "" {
		return r.client.Del(ctx, redisStateKey(ch)).Err()
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	key := redisStateKey(ch)
	if ttl > 0 {
		return r.client.Set(ctx, key, b, ttl).Err()
	}
	return r.client.Set(ctx, key, b, 0).Err()
}

// HarvestStateExists is true when Redis has a harvest state key.
func (r *RedisStore) HarvestStateExists(ctx context.Context, channelID string) (bool, error) {
	if r == nil || r.client == nil {
		return false, nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return false, nil
	}
	n, err := r.client.Exists(ctx, redisStateKey(ch)).Result()
	return n > 0, err
}

// UpsertSlackMessages merges messages into the events ZSET and msg HASH.
func (r *RedisStore) UpsertSlackMessages(ctx context.Context, channelID string, msgs []slack.Message) error {
	if r == nil || r.client == nil {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" || len(msgs) == 0 {
		return nil
	}
	zkey := redisEventsKey(ch)
	hkey := redisMsgHashKey(ch)
	pipe := r.client.Pipeline()
	for _, m := range msgs {
		ts := strings.TrimSpace(m.Timestamp)
		if ts == "" {
			continue
		}
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal slack message ts=%s: %w", ts, err)
		}
		pipe.ZAdd(ctx, zkey, redis.Z{Score: SlackTSScore(ts), Member: ts})
		pipe.HSet(ctx, hkey, ts, b)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// ListSlackMessagesChronological returns all stored messages sorted by Slack ts ascending.
func (r *RedisStore) ListSlackMessagesChronological(ctx context.Context, channelID string) ([]slack.Message, error) {
	if r == nil || r.client == nil {
		return nil, nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil, nil
	}
	zkey := redisEventsKey(ch)
	hkey := redisMsgHashKey(ch)
	tsList, err := r.client.ZRange(ctx, zkey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	if len(tsList) == 0 {
		return nil, nil
	}
	const batch = 256
	out := make([]slack.Message, 0, len(tsList))
	for i := 0; i < len(tsList); i += batch {
		j := i + batch
		if j > len(tsList) {
			j = len(tsList)
		}
		chunk := tsList[i:j]
		vals, err := r.client.HMGet(ctx, hkey, chunk...).Result()
		if err != nil {
			return nil, err
		}
		for idx := range chunk {
			if idx >= len(vals) || vals[idx] == nil {
				continue
			}
			s, ok := vals[idx].(string)
			if !ok || strings.TrimSpace(s) == "" {
				continue
			}
			var m slack.Message
			if json.Unmarshal([]byte(s), &m) != nil {
				continue
			}
			if strings.TrimSpace(m.Timestamp) == "" {
				m.Timestamp = chunk[idx]
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// TrimOldestMessages removes the oldest messages until at most targetKeep remain.
func (r *RedisStore) TrimOldestMessages(ctx context.Context, channelID string, targetKeep int) error {
	if r == nil || r.client == nil || targetKeep <= 0 {
		return nil
	}
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil
	}
	zkey := redisEventsKey(ch)
	hkey := redisMsgHashKey(ch)
	card, err := r.client.ZCard(ctx, zkey).Result()
	if err != nil || card <= int64(targetKeep) {
		return err
	}
	toRemove := int(card) - targetKeep
	if toRemove <= 0 {
		return nil
	}
	old, err := r.client.ZRange(ctx, zkey, 0, int64(toRemove-1)).Result()
	if err != nil {
		return err
	}
	if len(old) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	pipe.ZRem(ctx, zkey, interfaceSlice(old)...)
	pipe.HDel(ctx, hkey, old...)
	_, err = pipe.Exec(ctx)
	return err
}

func interfaceSlice(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i := range s {
		out[i] = s[i]
	}
	return out
}

// CapThreadCursors keeps at most maxThreads entries (drops oldest thread roots by Slack ts numerically).
func CapThreadCursors(m map[string]string, maxThreads int) {
	if maxThreads <= 0 || len(m) <= maxThreads {
		return
	}
	roots := make([]string, 0, len(m))
	for r := range m {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool {
		return SlackTSScore(roots[i]) < SlackTSScore(roots[j])
	})
	for len(roots) > maxThreads {
		delete(m, roots[0])
		roots = roots[1:]
	}
}
