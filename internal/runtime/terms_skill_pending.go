package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Mirrors employee-factory internal/slackbot/write_flow_confirmation.go Redis keys for terms_accept.
const (
	humansTermsConfirmTTL            = 7 * 24 * time.Hour
	skillConfirmationRedisPrefixEF   = "employee-factory:skill_confirmation"
	skillConfirmationRedisOpTimeout  = 750 * time.Millisecond
	termsSkillConfirmationExpiredMsg = "This action has expired or was already completed."
)

// TermsSkillPendingAnchor is stored on RenderPayload so PublishFinal can set Redis after the Slack post succeeds.
type TermsSkillPendingAnchor struct {
	ChannelID     string
	RequestUserID string
	ThreadAnchor  string
}

func threadFlowPendingKey(channel, requestUserID, threadTS string) string {
	return strings.TrimSpace(channel) + "|" + strings.TrimSpace(requestUserID) + "|" + strings.TrimSpace(threadTS)
}

// TermsSkillConfirmationRedisKey matches employee-factory skillConfirmationRedisKey(task=terms_accept, ...).
func TermsSkillConfirmationRedisKey(channelID, requestUserID, threadTS string) string {
	inner := skillConfirmationTaskTermsWire + "|" + threadFlowPendingKey(channelID, requestUserID, threadTS)
	return skillConfirmationRedisPrefixEF + ":" + inner
}

// SetTermsSkillPendingWithClient mirrors employee-factory Bot.setSkillPendingConfirmation for terms (Redis only).
func SetTermsSkillPendingWithClient(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) error {
	if client == nil {
		return nil
	}
	channelID = strings.TrimSpace(channelID)
	requestUserID = strings.TrimSpace(requestUserID)
	threadTS = strings.TrimSpace(threadTS)
	if channelID == "" || requestUserID == "" || threadTS == "" {
		return fmt.Errorf("terms pending: missing channel requester or thread anchor")
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	key := TermsSkillConfirmationRedisKey(channelID, requestUserID, threadTS)
	return client.Set(opCtx, key, "1", humansTermsConfirmTTL).Err()
}

// CommitTermsSkillPendingAnchor opens REDIS_URL from env and sets pending (no-op if anchor nil or empty fields).
func CommitTermsSkillPendingAnchor(ctx context.Context, a *TermsSkillPendingAnchor) error {
	if a == nil {
		return nil
	}
	ch := strings.TrimSpace(a.ChannelID)
	u := strings.TrimSpace(a.RequestUserID)
	anchor := strings.TrimSpace(a.ThreadAnchor)
	if ch == "" || u == "" || anchor == "" {
		return nil
	}
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return nil
	}
	client, err := redisOpen(url)
	if err != nil {
		return err
	}
	defer client.Close()
	return SetTermsSkillPendingWithClient(ctx, client, ch, u, anchor)
}

// ClearTermsSkillPendingWithClient clears pending (employee-factory clearSkillPendingConfirmation Redis path).
func ClearTermsSkillPendingWithClient(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) error {
	if client == nil {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	key := TermsSkillConfirmationRedisKey(channelID, requestUserID, threadTS)
	return client.Del(opCtx, key).Err()
}

// TermsSkillPendingTTL returns redis TTL for the pending key; ok is false when key is missing or expired.
func TermsSkillPendingTTL(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) (ttl time.Duration, ok bool, err error) {
	if client == nil {
		return 0, false, nil
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	key := TermsSkillConfirmationRedisKey(channelID, requestUserID, threadTS)
	d, err := client.TTL(opCtx, key).Result()
	if err != nil {
		return 0, false, err
	}
	if d <= 0 {
		return d, false, nil
	}
	return d, true, nil
}
