package slackthread

import (
	"context"
	"sort"
	"strings"

	"github.com/slack-go/slack"
)

const (
	defaultPageLimit = 200
	maxReplyPages    = 15
	defaultMaxRunes  = 12000
)

// Transcript loads prior messages in a Slack thread (conversations.replies) and
// returns a compact transcript for LLM context. currentMsgTS is the triggering
// message and is excluded. Returns "" when not in a thread or on fetch errors.
func Transcript(ctx context.Context, api *slack.Client, botUserID, channelID, threadTS, currentMsgTS string, maxRunes int) string {
	ch := strings.TrimSpace(channelID)
	root := strings.TrimSpace(threadTS)
	if api == nil || ch == "" || root == "" {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = defaultMaxRunes
	}
	msgs, err := fetchThreadMessages(ctx, api, ch, root)
	if err != nil || len(msgs) == 0 {
		return ""
	}
	sortMessagesOldestFirst(msgs)
	self := strings.TrimSpace(botUserID)
	cur := strings.TrimSpace(currentMsgTS)

	var lines []string
	for _, m := range msgs {
		if cur != "" && strings.TrimSpace(m.Timestamp) == cur {
			continue
		}
		st := strings.TrimSpace(m.SubType)
		if st == slack.MsgSubTypeMessageChanged || st == slack.MsgSubTypeMessageDeleted {
			continue
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		role := "assistant"
		if !isAssistantMessage(m, self) {
			role = "human"
		}
		lines = append(lines, role+": "+text)
	}
	if len(lines) == 0 {
		return ""
	}
	out := strings.Join(lines, "\n")
	out = "Thread so far (oldest first, excluding the message you are answering):\n" + out
	return trimByRunesTail(out, maxRunes)
}

func isAssistantMessage(m slack.Message, selfBotUserID string) bool {
	if strings.TrimSpace(m.BotID) != "" {
		return true
	}
	if selfBotUserID != "" && strings.TrimSpace(m.User) == selfBotUserID {
		return true
	}
	return false
}

func fetchThreadMessages(ctx context.Context, api *slack.Client, channelID, threadTS string) ([]slack.Message, error) {
	var all []slack.Message
	cursor := ""
	for page := 0; page < maxReplyPages; page++ {
		params := &slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
			Cursor:    cursor,
			Limit:     defaultPageLimit,
		}
		msgs, hasMore, next, err := api.GetConversationRepliesContext(ctx, params)
		if err != nil {
			return nil, err
		}
		all = append(all, msgs...)
		if !hasMore || strings.TrimSpace(next) == "" {
			break
		}
		cursor = strings.TrimSpace(next)
	}
	return all, nil
}

func sortMessagesOldestFirst(msgs []slack.Message) {
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Timestamp < msgs[j].Timestamp
	})
}

func trimByRunesTail(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	const prefix = "…[older thread lines omitted]\n"
	p := []rune(prefix)
	if len(p) >= maxRunes {
		return string(r[len(r)-maxRunes:])
	}
	tailLen := maxRunes - len(p)
	if tailLen < 1 {
		return string(r[len(r)-maxRunes:])
	}
	start := len(r) - tailLen
	if start < 0 {
		start = 0
	}
	return string(p) + string(r[start:])
}
