package slackrender

import (
	"strings"

	"github.com/slack-go/slack"
)

// AgentReplyBlocks renders final agent text using LimitsFromEnv (optional SLACK_RENDER_MRKDWN_MAX_RUNES).
func AgentReplyBlocks(fullText string) ([]slack.Block, string) {
	return AgentReplyBlocksWithLimits(fullText, LimitsFromEnv())
}

// AgentReplyBlocksWithLimits is the deterministic entry point for tests and explicit caps.
func AgentReplyBlocksWithLimits(fullText string, lim Limits) ([]slack.Block, string) {
	fullText = strings.TrimSpace(fullText)
	if fullText == "" {
		return nil, ""
	}
	norm := NormalizeModelTextToSlackMrkdwn(fullText)
	fallback := PlainNotificationFallback(norm)
	if fallback == "" {
		fallback = "New message from the agent"
	}

	const sourcesSep = "\n\nSources:\n"
	var main, sources string
	if i := strings.Index(norm, sourcesSep); i >= 0 {
		main = strings.TrimSpace(norm[:i])
		sources = strings.TrimSpace(norm[i+len(sourcesSep):])
	} else {
		main = norm
	}
	main = NormalizeMarkdownListLinesToSlackBullets(main)

	var blocks []slack.Block
	blocks = append(blocks, mainToBlocks(main, lim)...)

	if sources != "" {
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, sourcesRichTextBlock(sources, lim))
	}

	return blocks, fallback
}
