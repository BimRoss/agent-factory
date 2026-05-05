package channelknowledgerefresh

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/bimross/agent-factory/internal/slacktext"
	"github.com/slack-go/slack"
)

// BuildMarkdownDigest renders the channel digest markdown from messages (chronological oldest first).
func BuildMarkdownDigest(msgs []slack.Message, p Params, botUserID, botID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Channel digest (auto-generated %s UTC)\n\n", time.Now().UTC().Format(time.RFC3339))
	for _, m := range msgs {
		if m.SubType == "message_changed" || m.SubType == "message_deleted" {
			continue
		}
		if IsKnowledgeCronSelfNoise(m, botUserID, botID) {
			continue
		}
		text := strings.TrimSpace(slacktext.MessagePlainTextForLLM(m))
		if text == "" {
			continue
		}
		who := strings.TrimSpace(m.User)
		if who == "" {
			who = "bot"
		}
		prefix := "- "
		if IsThreadReplyForDigest(m) {
			prefix = "  - "
		}
		line := fmt.Sprintf("%s**%s**: %s", prefix, who, strings.ReplaceAll(text, "\n", " "))
		if p.DigestThreadMarkers {
			ts := strings.TrimSpace(m.Timestamp)
			thr := strings.TrimSpace(m.ThreadTimestamp)
			if ts != "" && thr != "" && ts != thr {
				line += fmt.Sprintf(" <!--dac m=%s t=%s-->", ts, thr)
			} else if ts != "" {
				line += fmt.Sprintf(" <!--dac m=%s-->", ts)
			}
		}
		fmt.Fprintln(&b, line)
	}
	return strings.TrimSpace(b.String())
}

// TailTruncateMarkdown keeps the tail of s to at most maxRunes runes (digest store cap).
func TailTruncateMarkdown(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	s = string(r[len(r)-maxRunes:])
	return "…(truncated)\n\n" + s
}

// FilterMessagesSinceSlackTS returns messages whose Timestamp is >= minTS (lex Slack order).
func FilterMessagesSinceSlackTS(msgs []slack.Message, minTS string) []slack.Message {
	minTS = strings.TrimSpace(minTS)
	if minTS == "" {
		out := make([]slack.Message, len(msgs))
		copy(out, msgs)
		return out
	}
	var out []slack.Message
	for _, m := range msgs {
		if channelknowledge.SlackTSCompare(strings.TrimSpace(m.Timestamp), minTS) >= 0 {
			out = append(out, m)
		}
	}
	return out
}

// PersistChannelMarkdownPair builds full and recent digests from allMsgs and writes Redis.
func PersistChannelMarkdownPair(
	ctx context.Context,
	store *channelknowledge.RedisStore,
	channelID string,
	allMsgs []slack.Message,
	p Params,
	botUserID, botID string,
) (full string, err error) {
	ch := strings.TrimSpace(channelID)
	if store == nil || ch == "" {
		return "", nil
	}
	fullRaw := BuildMarkdownDigest(allMsgs, p, botUserID, botID)
	full = TailTruncateMarkdown(fullRaw, p.MaxStoreRunes)
	cutoff := channelknowledge.SlackTSCutoffForAge(int64(p.RecentWindowHours) * 3600)
	recentMsgs := FilterMessagesSinceSlackTS(allMsgs, cutoff)
	recentRaw := BuildMarkdownDigest(recentMsgs, p, botUserID, botID)
	recent := TailTruncateMarkdown(recentRaw, p.RecentMaxStoreRunes)
	if err := store.Set(ctx, ch, full, p.TTL); err != nil {
		return "", err
	}
	if err := store.SetRecent(ctx, ch, recent, p.TTL); err != nil {
		return "", err
	}
	return full, nil
}
