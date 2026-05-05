// Command channel-knowledge-refresh fetches Slack channel history and writes markdown digests
// to Redis for prompt injection (see CHANNEL_KNOWLEDGE_* env on channel-knowledge-refresh pods).
// Intended to run frequently (e.g. every minute) from Kubernetes CronJob; uses incremental
// harvest when Redis harvest state exists, otherwise bootstraps a full window.
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/bimross/agent-factory/internal/channelknowledgerefresh"
	"github.com/bimross/agent-factory/internal/channelregistry"
	"github.com/bimross/agent-factory/internal/companychannel"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

func main() {
	_ = godotenv.Load()
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx := context.Background()
	token := strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
	if token == "" {
		log.Fatal("SLACK_BOT_TOKEN required")
	}
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		log.Fatal("REDIS_URL required")
	}

	api := slack.New(token)
	channels, err := resolveChannelIDs(ctx, api)
	if err != nil {
		log.Fatal(err)
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	rdb := redis.NewClient(opt)
	defer func() { _ = rdb.Close() }()
	store := channelknowledge.NewRedisStoreFromClient(rdb)
	if store == nil {
		log.Fatal("redis client init failed")
	}

	var channelMeta map[string]companychannel.CompanyChannelRuntime
	if parseBoolEnvDefaultTrue("COMPANY_CHANNELS_REDIS_ENABLED") {
		key := redisKeyOrDefault(strings.TrimSpace(os.Getenv("COMPANY_CHANNELS_REDIS_KEY")))
		meta, err := channelregistry.LoadChannelMapFromRedis(ctx, rdb, key)
		if err != nil {
			log.Printf("channel_knowledge: company channel registry (notify metadata): %v", err)
		} else {
			channelMeta = meta
		}
	}

	p := channelknowledgerefresh.ParamsFromEnv()
	// When true (default), post to the channel only if refresh fails for that channel (❗ alert).
	slackNotify := parseBoolEnvDefaultTrue("CHANNEL_KNOWLEDGE_SLACK_NOTIFY")
	failureNotifyOverride := strings.TrimSpace(os.Getenv("CHANNEL_KNOWLEDGE_SLACK_NOTIFY_MESSAGE"))

	auth, authErr := api.AuthTestContext(ctx)
	botUserID, botID := "", ""
	if authErr != nil {
		log.Printf("channel_knowledge: auth.test failed: %v (cron self-posts will not be filtered from digest; hourly notify may repeat)", authErr)
	} else if auth != nil {
		botUserID = strings.TrimSpace(auth.UserID)
		botID = strings.TrimSpace(auth.BotID)
		log.Printf("channel_knowledge: slack identity user_id=%s bot_id=%s", botUserID, botID)
	}
	for _, chID := range channels {
		_, err := channelknowledgerefresh.RefreshOneChannel(ctx, api, store, chID, p, botUserID, botID)
		if err == nil {
			continue
		}
		log.Printf("channel=%s err=%v", chID, err)
		if !slackNotify {
			continue
		}
		dn := notifyLabelForChannel(ctx, api, chID, channelMeta)
		if err := postKnowledgeRefreshFailure(ctx, api, chID, dn, err, failureNotifyOverride); err != nil {
			log.Printf("channel_knowledge_failure_notify: channel=%s err=%v", chID, err)
		}
	}
}

func parseBoolEnvDefaultTrue(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func redisKeyOrDefault(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return channelregistry.DefaultRedisHashKey
	}
	return k
}
