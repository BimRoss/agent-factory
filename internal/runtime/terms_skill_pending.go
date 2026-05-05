package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Mirrors employee-factory internal/slackbot/write_flow_confirmation.go Redis keys for terms_accept.
const (
	humansTermsConfirmTTL            = 7 * 24 * time.Hour
	writeEmailConfirmTTL             = 20 * time.Minute
	skillConfirmationRedisPrefixEF   = "employee-factory:skill_confirmation"
	emailPendingPayloadRedisPrefix   = "agent-factory:create_email_pending_payload"
	skillConfirmationRedisOpTimeout  = 750 * time.Millisecond
	termsSkillConfirmationExpiredMsg = "This action has expired or was already completed."
)

// TermsSkillPendingAnchor is stored on RenderPayload so PublishFinal can set Redis after the Slack post succeeds.
type TermsSkillPendingAnchor struct {
	ChannelID     string
	RequestUserID string
	ThreadAnchor  string
}

// EmailSkillPendingAnchor mirrors employee-factory create-email queued send state (Redis-backed for agent-factory).
type EmailSkillPendingAnchor struct {
	ChannelID     string
	RequestUserID string
	ThreadAnchor  string
	Recipients    []string
	Subject       string
	BodyHTML      string
}

type emailPendingPayloadJSON struct {
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`
	BodyHTML   string   `json:"body_html"`
}

func threadFlowPendingKey(channel, requestUserID, threadTS string) string {
	return strings.TrimSpace(channel) + "|" + strings.TrimSpace(requestUserID) + "|" + strings.TrimSpace(threadTS)
}

// TermsSkillConfirmationRedisKey matches employee-factory skillConfirmationRedisKey(task=terms_accept, ...).
func TermsSkillConfirmationRedisKey(channelID, requestUserID, threadTS string) string {
	inner := skillConfirmationTaskTermsWire + "|" + threadFlowPendingKey(channelID, requestUserID, threadTS)
	return skillConfirmationRedisPrefixEF + ":" + inner
}

// EmailSkillConfirmationRedisKey matches employee-factory skillConfirmationRedisKey(task=email_send, ...).
func EmailSkillConfirmationRedisKey(channelID, requestUserID, threadTS string) string {
	inner := skillConfirmationTaskEmailWire + "|" + threadFlowPendingKey(channelID, requestUserID, threadTS)
	return skillConfirmationRedisPrefixEF + ":" + inner
}

func emailPendingPayloadRedisKey(channelID, requestUserID, threadTS string) string {
	return emailPendingPayloadRedisPrefix + ":" + threadFlowPendingKey(channelID, requestUserID, threadTS)
}

// CommitEmailSkillPendingAnchor persists gate + serialized payload after the Slack confirmation posts.
func CommitEmailSkillPendingAnchor(ctx context.Context, a *EmailSkillPendingAnchor) error {
	if a == nil {
		return nil
	}
	ch := strings.TrimSpace(a.ChannelID)
	u := strings.TrimSpace(a.RequestUserID)
	anchor := strings.TrimSpace(a.ThreadAnchor)
	if ch == "" || u == "" || anchor == "" || len(a.Recipients) == 0 {
		return nil
	}
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return nil
	}
	payload := emailPendingPayloadJSON{
		Recipients: append([]string(nil), a.Recipients...),
		Subject:    strings.TrimSpace(a.Subject),
		BodyHTML:   strings.TrimSpace(a.BodyHTML),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client, err := redisOpen(url)
	if err != nil {
		return err
	}
	defer client.Close()
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	gate := EmailSkillConfirmationRedisKey(ch, u, anchor)
	pKey := emailPendingPayloadRedisKey(ch, u, anchor)
	pipe := client.Pipeline()
	pipe.Set(opCtx, gate, "1", writeEmailConfirmTTL)
	pipe.Set(opCtx, pKey, raw, writeEmailConfirmTTL)
	if _, err := pipe.Exec(opCtx); err != nil {
		return err
	}
	return nil
}

// ClearEmailSkillPendingWithClient deletes gate + payload keys.
func ClearEmailSkillPendingWithClient(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) error {
	if client == nil {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	ch := strings.TrimSpace(channelID)
	u := strings.TrimSpace(requestUserID)
	th := strings.TrimSpace(threadTS)
	return client.Del(opCtx, EmailSkillConfirmationRedisKey(ch, u, th), emailPendingPayloadRedisKey(ch, u, th)).Err()
}

// EmailSkillPendingTTL mirrors TermsSkillPendingTTL for the email_send gate key.
func EmailSkillPendingTTL(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) (ttl time.Duration, ok bool, err error) {
	if client == nil {
		return 0, false, nil
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	key := EmailSkillConfirmationRedisKey(channelID, requestUserID, threadTS)
	d, err := client.TTL(opCtx, key).Result()
	if err != nil {
		return 0, false, err
	}
	if d <= 0 {
		return d, false, nil
	}
	return d, true, nil
}

func loadEmailPendingPayloadJSON(ctx context.Context, client *redis.Client, channelID, requestUserID, threadTS string) (emailPendingPayloadJSON, error) {
	var out emailPendingPayloadJSON
	if client == nil {
		return out, fmt.Errorf("redis client is nil")
	}
	opCtx, cancel := context.WithTimeout(ctx, skillConfirmationRedisOpTimeout)
	defer cancel()
	raw, err := client.Get(opCtx, emailPendingPayloadRedisKey(channelID, requestUserID, threadTS)).Result()
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	return out, nil
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
