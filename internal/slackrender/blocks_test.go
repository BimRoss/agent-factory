package slackrender

import (
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func TestAgentReplyBlocks_SplitsSourcesAndLinkifies(t *testing.T) {
	in := "Lead **bold**\n\nSources:\n- https://example.com/path?q=1\n- https://news.site/long-story"
	blocks, fb := AgentReplyBlocks(in)
	if fb == "" {
		t.Fatal("expected fallback")
	}
	if len(blocks) < 3 {
		t.Fatalf("expected body section(s), divider, sources rich_text; got %d blocks", len(blocks))
	}

	dividerIdx := -1
	for i, b := range blocks {
		if _, ok := b.(*slack.DividerBlock); ok {
			dividerIdx = i
			break
		}
	}
	if dividerIdx < 0 {
		t.Fatal("expected divider before sources")
	}

	var sections []string
	for _, b := range blocks {
		if sb, ok := b.(*slack.SectionBlock); ok && sb.Text != nil {
			sections = append(sections, sb.Text.Text)
		}
	}
	if len(sections) < 1 {
		t.Fatalf("expected at least one mrkdwn section for body: %#v", sections)
	}
	first := sections[0]
	if !strings.Contains(first, "*bold*") || strings.Contains(first, "**") {
		t.Fatalf("expected normalized bold in first section: %q", first)
	}

	var tail []slack.Block
	for i := dividerIdx + 1; i < len(blocks); i++ {
		tail = append(tail, blocks[i])
	}
	if !richTextBlocksContainLinkURL(tail, "https://example.com/path?q=1") {
		t.Fatalf("expected rich_text link to example.com in sources blocks: %+v", tail)
	}
}

func TestSourcesDedupesSameHostnameAndExactURL(t *testing.T) {
	// Many Gemini grounding lines differ by opaque path but map to the same link label
	// (vertexaisearch.cloud.google.com) — only one bullet should show.
	in := "Summary\n\nSources:\n" +
		"- https://vertexaisearch.cloud.google.com/grounding-api-redirect/AAA\n" +
		"- https://vertexaisearch.cloud.google.com/grounding-api-redirect/BBB\n" +
		"- https://example.com/article\n" +
		"- https://example.com/article\n"
	blocks, _ := AgentReplyBlocksWithLimits(in, DefaultLimits())
	n := countRichTextListLinkItems(blocks)
	if n != 2 {
		t.Fatalf("want 2 unique source links after dedupe, got %d", n)
	}
}

func countRichTextListLinkItems(blocks []slack.Block) int {
	var n int
	for _, b := range blocks {
		rt, ok := b.(*slack.RichTextBlock)
		if !ok {
			continue
		}
		for _, e := range rt.Elements {
			list, ok := e.(*slack.RichTextList)
			if !ok {
				continue
			}
			for _, item := range list.Elements {
				sec, ok := item.(*slack.RichTextSection)
				if !ok {
					continue
				}
				for _, se := range sec.Elements {
					if _, ok := se.(*slack.RichTextSectionLinkElement); ok {
						n++
					}
				}
			}
		}
	}
	return n
}

func TestAgentReplyBlocks_ListUsesRichTextList(t *testing.T) {
	in := "Summary line.\n\n- First point\n- Second point\n\nSources:\n- https://example.com/a"
	blocks, _ := AgentReplyBlocks(in)
	if !richTextBlocksContainBulletText(blocks, "First point") ||
		!richTextBlocksContainBulletText(blocks, "Second point") {
		t.Fatalf("expected native rich_text_list items with bullet text: %+v", blocks)
	}
}

func richTextBlocksContainLinkURL(blocks []slack.Block, wantURL string) bool {
	for _, b := range blocks {
		rt, ok := b.(*slack.RichTextBlock)
		if !ok {
			continue
		}
		if richTextElementsContainLink(rt.Elements, wantURL) {
			return true
		}
	}
	return false
}

func richTextElementsContainLink(elems []slack.RichTextElement, wantURL string) bool {
	for _, e := range elems {
		switch x := e.(type) {
		case *slack.RichTextSection:
			for _, se := range x.Elements {
				le, ok := se.(*slack.RichTextSectionLinkElement)
				if ok && strings.Contains(le.URL, wantURL) {
					return true
				}
			}
		case *slack.RichTextList:
			for _, child := range x.Elements {
				if sec, ok := child.(*slack.RichTextSection); ok {
					for _, se := range sec.Elements {
						le, ok := se.(*slack.RichTextSectionLinkElement)
						if ok && strings.Contains(le.URL, wantURL) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func richTextBlocksContainBulletText(blocks []slack.Block, want string) bool {
	for _, b := range blocks {
		rt, ok := b.(*slack.RichTextBlock)
		if !ok {
			continue
		}
		for _, e := range rt.Elements {
			list, ok := e.(*slack.RichTextList)
			if !ok || list.Style != slack.RTEListBullet {
				continue
			}
			for _, item := range list.Elements {
				sec, ok := item.(*slack.RichTextSection)
				if !ok {
					continue
				}
				for _, se := range sec.Elements {
					te, ok := se.(*slack.RichTextSectionTextElement)
					if ok && te.Text == want {
						return true
					}
				}
			}
		}
	}
	return false
}
