package channelregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bimross/agent-factory/internal/companychannel"
	"github.com/redis/go-redis/v9"
)

// DefaultRedisHashKey is the Redis HASH storing per-channel JSON blobs (field = channel_id).
const DefaultRedisHashKey = "agent-factory:company_channels"

// CompanyChannelsInvalidateChannel is the Redis Pub/Sub channel used to push registry refresh to all
// Slack worker pods after HASH writes. Payload is the Slack channel id (or "*" if unknown).
// Keep in sync with makeacompany-ai backend publish after admin PATCH.
const CompanyChannelsInvalidateChannel = "agent-factory:company_channels:invalidate"

// PublishCompanyChannelsInvalidation notifies subscribers to reload the company channel HASH from Redis.
func PublishCompanyChannelsInvalidation(ctx context.Context, client *redis.Client, slackChannelID string) error {
	if client == nil {
		return nil
	}
	payload := strings.TrimSpace(slackChannelID)
	if payload == "" {
		payload = "*"
	}
	return client.Publish(ctx, CompanyChannelsInvalidateChannel, payload).Err()
}

// LoadChannelMapFromRedis reads HGETALL and unmarshals each value as CompanyChannelRuntime JSON.
func LoadChannelMapFromRedis(ctx context.Context, client *redis.Client, key string) (map[string]companychannel.CompanyChannelRuntime, error) {
	if client == nil {
		return nil, fmt.Errorf("company channels redis: nil client")
	}
	k := strings.TrimSpace(key)
	if k == "" {
		k = DefaultRedisHashKey
	}
	raw, err := client.HGetAll(ctx, k).Result()
	if err != nil {
		return nil, fmt.Errorf("company channels redis HGETALL %q: %w", k, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]companychannel.CompanyChannelRuntime, len(raw))
	for field, val := range raw {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		var e companychannel.CompanyChannelRuntime
		if err := json.Unmarshal([]byte(val), &e); err != nil {
			return nil, fmt.Errorf("company channels redis field %q: %w", field, err)
		}
		e = companychannel.NormalizeCompanyChannelRuntime(e)
		cid := e.ChannelID
		if cid == "" {
			cid = strings.TrimSpace(field)
			e.ChannelID = cid
		}
		if cid == "" {
			return nil, fmt.Errorf("company channels redis field %q: missing channel_id", field)
		}
		if cid != strings.TrimSpace(field) {
			return nil, fmt.Errorf("company channels redis field %q must match channel_id %q", field, cid)
		}
		out[cid] = e
	}
	return out, nil
}
