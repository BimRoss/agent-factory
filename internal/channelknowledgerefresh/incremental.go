package channelknowledgerefresh

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/slack-go/slack"
)

func incrementalChannelKnowledge(
	ctx context.Context,
	api *slack.Client,
	store *channelknowledge.RedisStore,
	channelID string,
	p Params,
	botUserID, botID string,
) (string, error) {
	ch := strings.TrimSpace(channelID)
	if ch == "" {
		return "", fmt.Errorf("channel id empty")
	}
	st0, err := store.GetHarvestState(ctx, ch)
	if err != nil {
		return "", err
	}
	if st0 == nil || strings.TrimSpace(st0.HistoryWatermark) == "" {
		return "", fmt.Errorf("incremental: missing harvest state for channel=%s", ch)
	}
	st := st0.Copy()
	threadPollsThisTick := 0

	deltaHist, err := FetchChannelHistorySinceChronological(
		ctx, api, ch, st.HistoryWatermark, p.IncrHistoryMaxMsgs, p.HistPageSize, p.HistMaxPages,
	)
	if err != nil {
		return "", err
	}
	if len(deltaHist) > 0 {
		if err := store.UpsertSlackMessages(ctx, ch, deltaHist); err != nil {
			return "", err
		}
		st.HistoryWatermark = channelknowledge.SlackTSMax(st.HistoryWatermark, maxMessageTimestamp(deltaHist))
		for _, m := range deltaHist {
			if !p.IncludeThreads {
				break
			}
			if !IsChannelThreadParent(m) {
				continue
			}
			root := strings.TrimSpace(m.Timestamp)
			if root == "" {
				continue
			}
			if _, ok := st.ThreadCursors[root]; !ok {
				st.ThreadCursors[root] = root
			}
		}
	}

	if p.IncludeThreads && len(st.ThreadCursors) > 0 {
		roots := sortedThreadRoots(st.ThreadCursors)
		nPoll := p.IncrThreadPollMax
		if nPoll <= 0 {
			nPoll = 1
		}
		// No new timeline messages: still poll threads for silent replies, but fewer Slack RTTs per tick.
		if len(deltaHist) == 0 && p.IncrThreadPollMaxWhenIdle > 0 && nPoll > p.IncrThreadPollMaxWhenIdle {
			nPoll = p.IncrThreadPollMaxWhenIdle
		}
		if nPoll > len(roots) {
			nPoll = len(roots)
		}
		start := st.ThreadPollRR % len(roots)
		threadPollsThisTick = nPoll
		for i := 0; i < nPoll; i++ {
			root := roots[(start+i)%len(roots)]
			cursor := strings.TrimSpace(st.ThreadCursors[root])
			if cursor == "" {
				cursor = root
			}
			replies, err := FetchConversationRepliesSinceChronological(
				ctx, api, ch, root, cursor, p.ThreadPageSize, p.ThreadMaxPages,
			)
			if err != nil {
				if errors.Is(err, errSlackThreadGone) {
					delete(st.ThreadCursors, root)
					continue
				}
				return "", fmt.Errorf("thread_ts=%s: %w", root, err)
			}
			if len(replies) > 0 {
				if err := store.UpsertSlackMessages(ctx, ch, replies); err != nil {
					return "", err
				}
				mx := cursor
				for _, m := range replies {
					mx = channelknowledge.SlackTSMax(mx, strings.TrimSpace(m.Timestamp))
				}
				st.ThreadCursors[root] = mx
			}
		}
		st.ThreadPollRR = (st.ThreadPollRR + nPoll) % maxInt(1, len(roots))
	}

	channelknowledge.CapThreadCursors(st.ThreadCursors, p.MaxTrackedThreads)
	if p.MaxStoredEvents > 0 {
		if err := store.TrimOldestMessages(ctx, ch, p.MaxStoredEvents); err != nil {
			return "", err
		}
	}
	allMsgs, err := store.ListSlackMessagesChronological(ctx, ch)
	if err != nil {
		return "", err
	}
	full, err := PersistChannelMarkdownPair(ctx, store, ch, allMsgs, p, botUserID, botID)
	if err != nil {
		return "", err
	}
	if err := store.SetHarvestState(ctx, ch, st, p.TTL); err != nil {
		return "", err
	}
	_ = store.ExpireIncrementalKeys(ctx, ch, p.TTL)
	log.Printf("channel_knowledge_incremental: ok channel=%s hist_delta=%d thread_polls_this_tick=%d thread_roots=%d stored_msgs=%d full_runes=%d",
		ch, len(deltaHist), threadPollsThisTick, len(st.ThreadCursors), len(allMsgs), utf8.RuneCountInString(full))
	return full, nil
}

func sortedThreadRoots(m map[string]string) []string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	sort.Slice(s, func(i, j int) bool {
		return channelknowledge.SlackTSScore(s[i]) < channelknowledge.SlackTSScore(s[j])
	})
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
