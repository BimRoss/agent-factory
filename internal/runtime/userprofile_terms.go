package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

// Keep Redis key layout aligned with makeacompany-ai / employee-factory userprofile package.
const (
	defaultMakeACProfilePrefix = "makeacompany:user_profile:"
	defaultMakeACBySlackPrefix = "makeacompany:user_by_slack:"
)

func normalizeTermsEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func makeacompanyProfileKey(prefix, email string) string {
	pp := strings.TrimSpace(prefix)
	if pp == "" {
		pp = defaultMakeACProfilePrefix
	}
	return strings.TrimSuffix(pp, ":") + ":" + normalizeTermsEmail(email)
}

func makeacompanyBySlackKey(bySlackPrefix, slackUserID string) string {
	bp := strings.TrimSpace(bySlackPrefix)
	if bp == "" {
		bp = defaultMakeACBySlackPrefix
	}
	return strings.TrimSuffix(bp, ":") + ":" + strings.TrimSpace(slackUserID)
}

// upsertMakeacompanySlackUserIndex writes makeacompany:user_by_slack:<id> and slack fields on
// makeacompany:user_profile:<email>, matching makeacompany-ai Store.UpsertUserProfileSlackID.
func upsertMakeacompanySlackUserIndex(ctx context.Context, rdb *redis.Client, slackUserID, rawEmail string) error {
	if rdb == nil {
		return fmt.Errorf("nil redis client")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	email := normalizeTermsEmail(strings.TrimSpace(rawEmail))
	if slackUserID == "" || email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("missing slack user id or invalid email")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pk := makeacompanyProfileKey("", email)
	pipe := rdb.TxPipeline()
	pipe.HSet(ctx, pk, map[string]any{
		"email":                    email,
		"slack_user_id":            slackUserID,
		"slack_profile_updated_at": now,
		"profile_updated_at":       now,
	})
	pipe.Set(ctx, makeacompanyBySlackKey("", slackUserID), email, 0)
	_, err := pipe.Exec(ctx)
	return err
}

// tryUpsertMakeacompanySlackIndexFromBotUserInfo calls users.info with the bot token and indexes
// the member when profile.email is visible (same visibility rules as the welcome eligibility check).
func tryUpsertMakeacompanySlackIndexFromBotUserInfo(ctx context.Context, api *slack.Client, rdb *redis.Client, slackUserID string) error {
	if api == nil || rdb == nil {
		return fmt.Errorf("nil slack client or redis")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}
	infoCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u, err := api.GetUserInfoContext(infoCtx, slackUserID)
	if err != nil {
		return fmt.Errorf("slack users.info: %w", err)
	}
	if u == nil || u.Deleted || u.IsBot {
		return fmt.Errorf("ineligible slack user")
	}
	return upsertMakeacompanySlackUserIndex(ctx, rdb, slackUserID, u.Profile.Email)
}

// WaitForMakeacompanyBySlackIndex polls until makeacompany:user_by_slack:<U…> exists or ctx ends.
func WaitForMakeacompanyBySlackIndex(ctx context.Context, rdb *redis.Client, slackUserID string, poll time.Duration) error {
	if rdb == nil {
		return fmt.Errorf("nil redis client")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	key := makeacompanyBySlackKey("", slackUserID)
	for {
		_, err := rdb.Get(ctx, key).Result()
		if err == nil {
			return nil
		}
		if err != redis.Nil {
			return fmt.Errorf("get %s: %w", key, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for slack index: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// RecordHumansTermsAccepted writes platform terms acceptance onto the makeacompany profile HASH.
func RecordHumansTermsAccepted(ctx context.Context, rdb *redis.Client, profileKeyPrefix, bySlackPrefix, slackUserID, slackMessageTS string) error {
	if rdb == nil {
		return fmt.Errorf("nil redis client")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}
	pp := strings.TrimSpace(profileKeyPrefix)
	if pp == "" {
		pp = defaultMakeACProfilePrefix
	}
	bp := strings.TrimSpace(bySlackPrefix)
	if bp == "" {
		bp = defaultMakeACBySlackPrefix
	}
	emailRaw, err := rdb.Get(ctx, makeacompanyBySlackKey(bp, slackUserID)).Result()
	if err == redis.Nil {
		return fmt.Errorf("no slack→email index for slack user %s", slackUserID)
	}
	if err != nil {
		return fmt.Errorf("get slack→email: %w", err)
	}
	email := normalizeTermsEmail(emailRaw)
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid email from index for slack user %s", slackUserID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pk := makeacompanyProfileKey(pp, email)
	fields := map[string]any{
		"email":                                  email,
		"humans_terms_accepted_at":               now,
		"humans_terms_accepted_slack_message_ts": strings.TrimSpace(slackMessageTS),
		"profile_updated_at":                     now,
	}
	return rdb.HSet(ctx, pk, fields).Err()
}

// ClearHumansTermsAccepted removes humans terms acceptance fields from the makeacompany profile HASH.
func ClearHumansTermsAccepted(ctx context.Context, rdb *redis.Client, profileKeyPrefix, bySlackPrefix, slackUserID string) error {
	if rdb == nil {
		return fmt.Errorf("nil redis client")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}
	pp := strings.TrimSpace(profileKeyPrefix)
	if pp == "" {
		pp = defaultMakeACProfilePrefix
	}
	bp := strings.TrimSpace(bySlackPrefix)
	if bp == "" {
		bp = defaultMakeACBySlackPrefix
	}
	emailRaw, err := rdb.Get(ctx, makeacompanyBySlackKey(bp, slackUserID)).Result()
	if err == redis.Nil {
		return fmt.Errorf("no slack→email index for slack user %s", slackUserID)
	}
	if err != nil {
		return fmt.Errorf("get slack→email: %w", err)
	}
	email := normalizeTermsEmail(emailRaw)
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid email from index for slack user %s", slackUserID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pk := makeacompanyProfileKey(pp, email)
	if err := rdb.HDel(ctx, pk, "humans_terms_accepted_at", "humans_terms_accepted_slack_message_ts").Err(); err != nil {
		return err
	}
	return rdb.HSet(ctx, pk, "profile_updated_at", now).Err()
}
