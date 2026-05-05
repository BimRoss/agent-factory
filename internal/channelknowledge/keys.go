package channelknowledge

import (
	"fmt"
	"strings"
)

const (
	keyFmtMarkdown       = "agent-factory:channel_knowledge:%s:markdown"
	keyFmtMarkdownRecent = "agent-factory:channel_knowledge:%s:markdown_recent"
	keyFmtEvents         = "agent-factory:channel_knowledge:%s:events"
	keyFmtMsg            = "agent-factory:channel_knowledge:%s:msg"
	keyFmtState          = "agent-factory:channel_knowledge:%s:state"
)

func redisMarkdownKey(channelID string) string {
	return fmt.Sprintf(keyFmtMarkdown, strings.TrimSpace(channelID))
}

func redisMarkdownRecentKey(channelID string) string {
	return fmt.Sprintf(keyFmtMarkdownRecent, strings.TrimSpace(channelID))
}

func redisEventsKey(channelID string) string {
	return fmt.Sprintf(keyFmtEvents, strings.TrimSpace(channelID))
}

func redisMsgHashKey(channelID string) string {
	return fmt.Sprintf(keyFmtMsg, strings.TrimSpace(channelID))
}

func redisStateKey(channelID string) string {
	return fmt.Sprintf(keyFmtState, strings.TrimSpace(channelID))
}
