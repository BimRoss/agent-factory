package channelknowledgerefresh

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/slack-go/slack"
)

const (
	slackHistoryMaxPerPage = 1000

	// slackAPIErrorThreadNotFound is the Slack error when the thread parent was deleted
	// or is otherwise not visible to conversations.replies.
	slackAPIErrorThreadNotFound = "thread_not_found"

	// slackRateLimitMaxAttempts caps retries when Slack returns HTTP 429 / RateLimitedError.
	slackRateLimitMaxAttempts = 32
	slackRateLimitSleepCap    = 2 * time.Minute
)

// errSlackThreadGone is returned by FetchConversationRepliesSinceChronological when Slack
// answers thread_not_found. Incremental refresh should delete that root from thread cursors.
var errSlackThreadGone = errors.New("channel knowledgerefresh: slack thread_not_found")

func isSlackThreadNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var serr slack.SlackErrorResponse
	if errors.As(err, &serr) && serr.Err == slackAPIErrorThreadNotFound {
		return true
	}
	return strings.Contains(err.Error(), slackAPIErrorThreadNotFound)
}

// SlackDo runs fn until it succeeds, ctx is cancelled, or Slack stops returning rate limits.
// Other errors are returned immediately.
func SlackDo(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt < slackRateLimitMaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		var rl *slack.RateLimitedError
		if errors.As(err, &rl) {
			if attempt == slackRateLimitMaxAttempts-1 {
				return err
			}
			d := rl.RetryAfter
			if d <= 0 {
				d = time.Second
			}
			if d > slackRateLimitSleepCap {
				d = slackRateLimitSleepCap
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
			continue
		}
		return err
	}
	return fmt.Errorf("slack: rate limit retry exhausted")
}

// clampHistoryPageSize returns a Slack conversations.history page size in [1, 1000].
func clampHistoryPageSize(n int) int {
	if n <= 0 {
		return defaultHistoryPageSize
	}
	if n > slackHistoryMaxPerPage {
		return slackHistoryMaxPerPage
	}
	return n
}

// FetchChannelHistoryChronological loads up to totalLimit messages from Slack using
// conversations.history with cursor pagination (newest-first pages), then returns
// them in chronological order (oldest first), matching the digest builder.
func FetchChannelHistoryChronological(
	ctx context.Context,
	api *slack.Client,
	channelID string,
	totalLimit int,
	pageSize int,
	maxPages int,
) ([]slack.Message, error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil, fmt.Errorf("channel id empty")
	}
	if totalLimit <= 0 {
		return nil, nil
	}
	ps := clampHistoryPageSize(pageSize)
	if maxPages <= 0 {
		maxPages = defaultHistoryMaxPages
	}

	var collected []slack.Message
	cursor := ""
	for page := 0; page < maxPages && len(collected) < totalLimit; page++ {
		need := totalLimit - len(collected)
		limit := ps
		if need < limit {
			limit = need
		}
		if limit <= 0 {
			break
		}
		params := &slack.GetConversationHistoryParameters{
			ChannelID: ch,
			Limit:     limit,
			Cursor:    cursor,
		}
		var resp *slack.GetConversationHistoryResponse
		err := SlackDo(ctx, func() error {
			var e error
			resp, e = api.GetConversationHistoryContext(ctx, params)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("conversations.history: %w", err)
		}
		if len(resp.Messages) == 0 {
			break
		}
		collected = append(collected, resp.Messages...)
		if len(collected) >= totalLimit {
			break
		}
		if !resp.HasMore {
			break
		}
		next := strings.TrimSpace(resp.ResponseMetaData.NextCursor)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(collected) > totalLimit {
		collected = collected[:totalLimit]
	}
	reverseMessagesInPlace(collected)
	return collected, nil
}

// FetchChannelHistorySinceChronological loads up to maxMsgs messages strictly newer than oldestExclusive
// (Slack conversations.history with Oldest + Inclusive=false), then returns oldest first.
func FetchChannelHistorySinceChronological(
	ctx context.Context,
	api *slack.Client,
	channelID string,
	oldestExclusive string,
	maxMsgs int,
	pageSize int,
	maxPages int,
) ([]slack.Message, error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return nil, fmt.Errorf("channel id empty")
	}
	oldestExclusive = strings.TrimSpace(oldestExclusive)
	if oldestExclusive == "" {
		return nil, nil
	}
	if maxMsgs <= 0 {
		return nil, nil
	}
	ps := clampHistoryPageSize(pageSize)
	if maxPages <= 0 {
		maxPages = defaultHistoryMaxPages
	}
	var collected []slack.Message
	cursor := ""
	for page := 0; page < maxPages && len(collected) < maxMsgs; page++ {
		need := maxMsgs - len(collected)
		limit := ps
		if need < limit {
			limit = need
		}
		if limit <= 0 {
			break
		}
		params := &slack.GetConversationHistoryParameters{
			ChannelID: ch,
			Limit:     limit,
			Cursor:    cursor,
			Oldest:    oldestExclusive,
			Inclusive: false,
		}
		var resp *slack.GetConversationHistoryResponse
		err := SlackDo(ctx, func() error {
			var e error
			resp, e = api.GetConversationHistoryContext(ctx, params)
			return e
		})
		if err != nil {
			return nil, fmt.Errorf("conversations.history: %w", err)
		}
		if len(resp.Messages) == 0 {
			break
		}
		collected = append(collected, resp.Messages...)
		if len(collected) >= maxMsgs {
			break
		}
		if !resp.HasMore {
			break
		}
		next := strings.TrimSpace(resp.ResponseMetaData.NextCursor)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(collected) > maxMsgs {
		collected = collected[:maxMsgs]
	}
	reverseMessagesInPlace(collected)
	return collected, nil
}

func reverseMessagesInPlace(msgs []slack.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

// IsChannelThreadParent is true for a channel-root message that has replies (expand with conversations.replies).
func IsChannelThreadParent(m slack.Message) bool {
	if m.ReplyCount < 1 {
		return false
	}
	if m.SubType == "message_changed" || m.SubType == "message_deleted" {
		return false
	}
	tt := strings.TrimSpace(m.ThreadTimestamp)
	if tt == "" {
		return true
	}
	return tt == strings.TrimSpace(m.Timestamp)
}

func sortMessagesByTimestampAsc(msgs []slack.Message) {
	sort.Slice(msgs, func(i, j int) bool {
		return compareSlackMessageTS(msgs[i].Timestamp, msgs[j].Timestamp) < 0
	})
}

func compareSlackMessageTS(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	}
	fa, fb := channelknowledge.SlackTSScore(a), channelknowledge.SlackTSScore(b)
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	default:
		return 0
	}
}

// FetchConversationRepliesChronological loads all messages in a thread via conversations.replies (paginated), oldest first.
func FetchConversationRepliesChronological(
	ctx context.Context,
	api *slack.Client,
	channelID string,
	threadTS string,
	pageSize int,
	maxPages int,
) ([]slack.Message, error) {
	ch := strings.TrimSpace(channelID)
	ts := strings.TrimSpace(threadTS)
	if ch == "" || ts == "" {
		return nil, fmt.Errorf("channel id or thread_ts empty")
	}
	ps := clampHistoryPageSize(pageSize)
	if maxPages <= 0 {
		maxPages = defaultThreadMaxPages
	}
	var collected []slack.Message
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := &slack.GetConversationRepliesParameters{
			ChannelID: ch,
			Timestamp: ts,
			Cursor:    cursor,
			Limit:     ps,
		}
		var msgs []slack.Message
		var hasMore bool
		var next string
		err := SlackDo(ctx, func() error {
			var e error
			msgs, hasMore, next, e = api.GetConversationRepliesContext(ctx, params)
			return e
		})
		if err != nil {
			if isSlackThreadNotFoundError(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("conversations.replies: %w", err)
		}
		collected = append(collected, msgs...)
		if !hasMore || strings.TrimSpace(next) == "" {
			break
		}
		cursor = strings.TrimSpace(next)
	}
	sortMessagesByTimestampAsc(collected)
	return collected, nil
}

// FetchConversationRepliesSinceChronological loads thread messages with ts strictly greater than oldestExclusive
// (Slack conversations.replies with Oldest + Inclusive=false), paginated, oldest first.
func FetchConversationRepliesSinceChronological(
	ctx context.Context,
	api *slack.Client,
	channelID string,
	threadTS string,
	oldestExclusive string,
	pageSize int,
	maxPages int,
) ([]slack.Message, error) {
	ch := strings.TrimSpace(channelID)
	ts := strings.TrimSpace(threadTS)
	oldestExclusive = strings.TrimSpace(oldestExclusive)
	if ch == "" || ts == "" || oldestExclusive == "" {
		return nil, nil
	}
	ps := clampHistoryPageSize(pageSize)
	if maxPages <= 0 {
		maxPages = defaultThreadMaxPages
	}
	var collected []slack.Message
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := &slack.GetConversationRepliesParameters{
			ChannelID: ch,
			Timestamp: ts,
			Cursor:    cursor,
			Limit:     ps,
			Oldest:    oldestExclusive,
			Inclusive: false,
		}
		var msgs []slack.Message
		var hasMore bool
		var next string
		err := SlackDo(ctx, func() error {
			var e error
			msgs, hasMore, next, e = api.GetConversationRepliesContext(ctx, params)
			return e
		})
		if err != nil {
			if isSlackThreadNotFoundError(err) {
				return nil, errSlackThreadGone
			}
			return nil, fmt.Errorf("conversations.replies: %w", err)
		}
		collected = append(collected, msgs...)
		if !hasMore || strings.TrimSpace(next) == "" {
			break
		}
		cursor = strings.TrimSpace(next)
	}
	sortMessagesByTimestampAsc(collected)
	return collected, nil
}

// MergeChannelHistoryWithThreads adds messages that exist only inside threads (not on the channel timeline)
// by calling conversations.replies for up to maxThreadRoots thread parents. Parents are chosen from the
// newest threads in the history window so recent discussion is prioritized.
func MergeChannelHistoryWithThreads(
	ctx context.Context,
	api *slack.Client,
	channelID string,
	historyOldestFirst []slack.Message,
	maxThreadRoots int,
	repliesPageSize int,
	maxPagesPerThread int,
) ([]slack.Message, error) {
	if maxThreadRoots <= 0 {
		return historyOldestFirst, nil
	}
	byTS := make(map[string]slack.Message, len(historyOldestFirst)+256)
	for _, m := range historyOldestFirst {
		byTS[m.Timestamp] = m
	}
	var parents []slack.Message
	for _, m := range historyOldestFirst {
		if IsChannelThreadParent(m) {
			parents = append(parents, m)
		}
	}
	if len(parents) == 0 {
		return historyOldestFirst, nil
	}
	sort.Slice(parents, func(i, j int) bool {
		return compareSlackMessageTS(parents[i].Timestamp, parents[j].Timestamp) > 0
	})
	if len(parents) > maxThreadRoots {
		parents = parents[:maxThreadRoots]
	}
	ch := strings.TrimSpace(channelID)
	for _, p := range parents {
		threadTS := strings.TrimSpace(p.Timestamp)
		replies, err := FetchConversationRepliesChronological(ctx, api, ch, threadTS, repliesPageSize, maxPagesPerThread)
		if err != nil {
			return nil, fmt.Errorf("thread_ts=%s: %w", threadTS, err)
		}
		// replies is empty when Slack returns thread_not_found (deleted / invisible thread).
		for _, tm := range replies {
			byTS[tm.Timestamp] = tm
		}
	}
	out := make([]slack.Message, 0, len(byTS))
	for _, m := range byTS {
		out = append(out, m)
	}
	sortMessagesByTimestampAsc(out)
	return out, nil
}
