package channelknowledgerefresh

import (
	"strings"

	"github.com/bimross/agent-factory/internal/channelknowledge"
	"github.com/slack-go/slack"
)

func threadRootTS(m slack.Message) string {
	ts := strings.TrimSpace(m.Timestamp)
	tt := strings.TrimSpace(m.ThreadTimestamp)
	if tt != "" {
		return tt
	}
	if IsChannelThreadParent(m) {
		return ts
	}
	return ""
}

func maxMessageTimestamp(msgs []slack.Message) string {
	var m string
	for _, msg := range msgs {
		ts := strings.TrimSpace(msg.Timestamp)
		m = channelknowledge.SlackTSMax(m, ts)
	}
	return m
}

func threadCursorsFromMessages(msgs []slack.Message) map[string]string {
	threadMax := make(map[string]string)
	for _, msg := range msgs {
		ts := strings.TrimSpace(msg.Timestamp)
		if ts == "" {
			continue
		}
		root := threadRootTS(msg)
		if root == "" {
			continue
		}
		threadMax[root] = channelknowledge.SlackTSMax(threadMax[root], ts)
	}
	return threadMax
}

// harvestStateFromBootstrap builds harvest state from timeline-only history (for hw) and merged messages (for thread cursors).
func harvestStateFromBootstrap(histOldestFirst, mergedOldestFirst []slack.Message) *channelknowledge.HarvestState {
	return &channelknowledge.HarvestState{
		HistoryWatermark: maxMessageTimestamp(histOldestFirst),
		ThreadCursors:    threadCursorsFromMessages(mergedOldestFirst),
		ThreadPollRR:     0,
	}
}
