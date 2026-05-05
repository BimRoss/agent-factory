package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

const (
	humansWelcomeDedupeKeyPrefix = "agent-factory:humans_terms_welcome:"
	humansWelcomeDedupeTTL       = 400 * 24 * time.Hour
	snapshotHTTPTimeout          = 90 * time.Second
)

// agent-factory-admin joanne welcome trigger. Dedupe uses agent-factory:humans_terms_welcome:<slackUserID>.
func PostHumansChannelWelcome(ctx context.Context, api *slack.Client, slackUserID string) error {
	if api == nil {
		return fmt.Errorf("nil slack api")
	}
	ch := strings.TrimSpace(os.Getenv("ONBOARDING_CHANNEL"))
	if ch == "" {
		return fmt.Errorf("ONBOARDING_CHANNEL is not set")
	}
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("missing slack user id")
	}

	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return fmt.Errorf("REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("redis parse: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	dedupeKey := humansWelcomeDedupeKeyPrefix + slackUserID
	if _, err := rdb.Get(ctx, dedupeKey).Result(); err == nil {
		return fmt.Errorf("welcome already recorded for this user")
	} else if err != redis.Nil {
		return fmt.Errorf("redis dedupe: %w", err)
	}

	infoCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u, err := api.GetUserInfoContext(infoCtx, slackUserID)
	if err != nil {
		return fmt.Errorf("slack users.info: %w", err)
	}
	if u == nil || u.Deleted || u.IsBot || u.IsStranger {
		return fmt.Errorf("slack user is not eligible")
	}

	if err := upsertMakeacompanySlackUserIndex(ctx, rdb, slackUserID, u.Profile.Email); err != nil {
		log.Printf("humans welcome: slack→email index upsert user=%s err=%v", slackUserID, err)
	}

	rootText := fmt.Sprintf("Hey, welcome to the company <@%s>! We need you to accept our *terms of use* before you begin working with us. Open the thread on this message to read them.", slackUserID)
	rootCtx, rootCancel := context.WithTimeout(ctx, 15*time.Second)
	defer rootCancel()
	_, rootTS, err := api.PostMessageContext(rootCtx, ch, slack.MsgOptionText(strings.TrimSpace(rootText), false))
	if err != nil {
		return fmt.Errorf("post welcome: %w", err)
	}
	rootTS = strings.TrimSpace(rootTS)
	if rootTS == "" {
		return fmt.Errorf("slack returned empty welcome timestamp")
	}

	threadText := "Terms of Use"
	blocks := BuildTermsAcceptanceBlocks(ch, slackUserID, rootTS)
	threadCtx, threadCancel := context.WithTimeout(ctx, 15*time.Second)
	defer threadCancel()
	if _, _, err := api.PostMessageContext(threadCtx, ch,
		slack.MsgOptionText(strings.TrimSpace(threadText), false),
		slack.MsgOptionTS(rootTS),
		slack.MsgOptionBlocks(blocks...),
	); err != nil {
		return fmt.Errorf("post terms thread: %w", err)
	}

	if err := SetTermsSkillPendingWithClient(ctx, rdb, ch, slackUserID, rootTS); err != nil {
		log.Printf("humans welcome: redis terms pending failed user=%s err=%v", slackUserID, err)
	}

	if err := rdb.Set(ctx, dedupeKey, "1", humansWelcomeDedupeTTL).Err(); err != nil {
		log.Printf("humans welcome: dedupe_set user=%s err=%v", slackUserID, err)
	}

	refreshURL := strings.TrimSpace(os.Getenv("SLACK_USERS_SNAPSHOT_REFRESH_URL"))
	token := strings.TrimSpace(os.Getenv("BACKEND_INTERNAL_SERVICE_TOKEN"))
	if refreshURL != "" && token != "" {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), snapshotHTTPTimeout)
			defer cancel()
			if err := postSlackUsersSnapshotRefresh(bg, refreshURL, token); err != nil {
				log.Printf("humans welcome: async snapshot refresh: %v", err)
			}
		}()
	}
	return nil
}

func postSlackUsersSnapshotRefresh(ctx context.Context, url, bearer string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	client := &http.Client{Timeout: snapshotHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("humans welcome: slack snapshot refresh POST err=%v", err)
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("humans welcome: slack snapshot refresh HTTP %d", resp.StatusCode)
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	log.Printf("humans welcome: slack_users_snapshot_refresh ok")
	return nil
}
