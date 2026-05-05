package channelknowledgerefresh

import (
	"context"
	"log"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/bimross/agent-factory/internal/slacktext"
	"github.com/slack-go/slack"
)

// knowledgeCronNoisePrefixes are leading runes for bot-only cron lines we never store in the digest
// (legacy 📝 success posts; ❗ failure alerts).
var knowledgeCronNoisePrefixes = []string{"📝", "❗"}

// IsKnowledgeCronSelfNoise is true for this bot's own channel lines from the knowledge cron
// (legacy 📝 success copy, ❗ failure alerts). They must be omitted from the stored digest.
func IsKnowledgeCronSelfNoise(m slack.Message, botUserID, botID string) bool {
	text := strings.TrimSpace(slacktext.MessagePlainTextForLLM(m))
	if text == "" {
		return false
	}
	okPrefix := false
	for _, p := range knowledgeCronNoisePrefixes {
		if strings.HasPrefix(text, p) {
			okPrefix = true
			break
		}
	}
	if !okPrefix {
		return false
	}
	u := strings.TrimSpace(m.User)
	b := strings.TrimSpace(m.BotID)
	if botUserID != "" && u == botUserID {
		return true
	}
	if botID != "" && b == botID {
		return true
	}
	return false
}

// IsThreadReplyForDigest is true for messages that belong to a thread under a parent (indent in markdown).
func IsThreadReplyForDigest(m slack.Message) bool {
	tt := strings.TrimSpace(m.ThreadTimestamp)
	if tt == "" {
		return false
	}
	return strings.TrimSpace(m.Timestamp) != tt
}

// ForceFullFromEnv is true when CHANNEL_KNOWLEDGE_FORCE_FULL requests a bootstrap rebuild.
func ForceFullFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CHANNEL_KNOWLEDGE_FORCE_FULL")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// BootstrapChannelKnowledge performs a full Slack fetch, repopulates incremental Redis structures,
// and writes full + recent markdown digests.
func BootstrapChannelKnowledge(
	ctx context.Context,
	api *slack.Client,
	store *channelknowledge.RedisStore,
	channelID string,
	p Params,
	botUserID, botID string,
) (string, error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return "", nil
	}
	if err := store.ClearIncrementalStore(ctx, ch); err != nil {
		return "", err
	}
	msgs, err := FetchChannelHistoryChronological(ctx, api, ch, p.HistLimit, p.HistPageSize, p.HistMaxPages)
	if err != nil {
		return "", err
	}
	historyCount := len(msgs)
	merged := msgs
	if p.IncludeThreads {
		merged, err = MergeChannelHistoryWithThreads(ctx, api, ch, msgs, p.ThreadMaxRoots, p.ThreadPageSize, p.ThreadMaxPages)
		if err != nil {
			return "", err
		}
	}
	st := harvestStateFromBootstrap(msgs, merged)
	if err := store.UpsertSlackMessages(ctx, ch, merged); err != nil {
		return "", err
	}
	if err := store.SetHarvestState(ctx, ch, st, p.TTL); err != nil {
		return "", err
	}
	if p.MaxStoredEvents > 0 {
		if err := store.TrimOldestMessages(ctx, ch, p.MaxStoredEvents); err != nil {
			return "", err
		}
	}
	stored, err := store.ListSlackMessagesChronological(ctx, ch)
	if err != nil {
		return "", err
	}
	full, err := PersistChannelMarkdownPair(ctx, store, ch, stored, p, botUserID, botID)
	if err != nil {
		return "", err
	}
	_ = store.ExpireIncrementalKeys(ctx, ch, p.TTL)
	log.Printf("channel_knowledge_bootstrap: ok channel=%s history_msgs=%d digest_msgs=%d include_threads=%v runes=%d",
		ch, historyCount, len(merged), p.IncludeThreads, utf8.RuneCountInString(full))
	return full, nil
}

// RefreshOneChannel runs incremental harvest when state exists, otherwise bootstraps.
// Set CHANNEL_KNOWLEDGE_FORCE_FULL=1 to always bootstrap.
func RefreshOneChannel(
	ctx context.Context,
	api *slack.Client,
	store *channelknowledge.RedisStore,
	channelID string,
	p Params,
	botUserID, botID string,
) (string, error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return "", nil
	}
	if ForceFullFromEnv() {
		return BootstrapChannelKnowledge(ctx, api, store, ch, p, botUserID, botID)
	}
	ok, err := store.HarvestStateExists(ctx, ch)
	if err != nil {
		return "", err
	}
	if !ok {
		return BootstrapChannelKnowledge(ctx, api, store, ch, p, botUserID, botID)
	}
	return incrementalChannelKnowledge(ctx, api, store, ch, p, botUserID, botID)
}
