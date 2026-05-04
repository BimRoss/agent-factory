package slackrender

import (
	"net/url"
	"strings"

	"github.com/slack-go/slack"
)

// mainToBlocks turns normalized main body text into SectionBlocks (mrkdwn paragraphs)
// and RichTextBlocks with rich_text_list for consecutive • bullet lines — Slack’s native list UI.
func mainToBlocks(main string, lim Limits) []slack.Block {
	main = strings.TrimSpace(main)
	if main == "" {
		return nil
	}
	maxMrkdwn := lim.MrkdwnSectionRunes
	if maxMrkdwn <= 0 {
		maxMrkdwn = DefaultLimits().MrkdwnSectionRunes
	}
	maxRich := lim.RichTextElementRunes
	if maxRich <= 0 {
		maxRich = DefaultLimits().RichTextElementRunes
	}

	lines := strings.Split(main, "\n")
	var blocks []slack.Block
	var para []string
	var listItems []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		text := strings.Join(para, "\n")
		para = nil
		for _, chunk := range ChunkMrkdwnSectionText(text, maxMrkdwn) {
			blocks = append(blocks, slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", chunk, false, false),
				nil, nil,
			))
		}
	}

	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		elems := make([]slack.RichTextElement, 0, len(listItems))
		for _, item := range listItems {
			chunks := ChunkMrkdwnSectionText(item, maxRich)
			secElems := make([]slack.RichTextSectionElement, len(chunks))
			for i, c := range chunks {
				secElems[i] = slack.NewRichTextSectionTextElement(c, nil)
			}
			elems = append(elems, slack.NewRichTextSection(secElems...))
		}
		rtList := slack.NewRichTextList(slack.RTEListBullet, 0, elems...)
		blocks = append(blocks, slack.NewRichTextBlock("", rtList))
		listItems = nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flushList()
			flushPara()
			continue
		}
		if content, ok := parseBulletContentLine(line); ok {
			flushPara()
			listItems = append(listItems, content)
			continue
		}
		flushList()
		para = append(para, line)
	}
	flushList()
	flushPara()
	return blocks
}

func parseBulletContentLine(line string) (string, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	rest := line[i:]
	if !strings.HasPrefix(rest, "• ") {
		return "", false
	}
	return strings.TrimSpace(rest[len("• "):]), true
}

// sourcesRichTextBlock renders the trailing Sources list as a rich_text block: bold title +
// native bullet list of link items (same hostname labels as legacy mrkdwn linkify).
func sourcesRichTextBlock(sources string, lim Limits) slack.Block {
	maxRich := lim.RichTextElementRunes
	if maxRich <= 0 {
		maxRich = DefaultLimits().RichTextElementRunes
	}

	sources = strings.TrimSpace(sources)
	title := slack.NewRichTextSection(
		slack.NewRichTextSectionTextElement("Sources", &slack.RichTextSectionTextStyle{Bold: true}),
	)
	if sources == "" {
		return slack.NewRichTextBlock("", title)
	}

	lines := strings.Split(sources, "\n")
	var linkItems []slack.RichTextElement
	seenExactURL := make(map[string]struct{})
	seenDisplayLabel := make(map[string]struct{})

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "• ")
		line = strings.TrimSpace(line)
		if isHTTPURL(line) {
			appendSourceLinkDeduped(&linkItems, line, "", maxRich, seenExactURL, seenDisplayLabel)
			continue
		}
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) > 0 && isHTTPURL(parts[0]) {
				suffix := ""
				if len(parts) == 2 {
					suffix = parts[1]
				}
				appendSourceLinkDeduped(&linkItems, parts[0], suffix, maxRich, seenExactURL, seenDisplayLabel)
			}
			continue
		}
		for _, seg := range ChunkMrkdwnSectionText(line, maxRich) {
			linkItems = append(linkItems, slack.NewRichTextSection(
				slack.NewRichTextSectionTextElement(seg, nil),
			))
		}
	}

	if len(linkItems) == 0 {
		return slack.NewRichTextBlock("", title)
	}
	list := slack.NewRichTextList(slack.RTEListBullet, 0, linkItems...)
	return slack.NewRichTextBlock("", title, list)
}

// appendSourceLinkDeduped skips duplicate source lines: exact URL repeats, and repeats that only
// differ by opaque redirect tokens but render as the same Slack link label (same hostname),
// which happens often with Gemini grounding URLs (many bullets → same vertexaisearch… label).
func appendSourceLinkDeduped(out *[]slack.RichTextElement, rawURL, suffix string, maxRich int, seenExact map[string]struct{}, seenLabel map[string]struct{}) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return
	}
	if _, dup := seenExact[rawURL]; dup {
		return
	}
	label := slackLinkLabel(rawURL)
	if label != "" {
		if _, dup := seenLabel[label]; dup {
			return
		}
	}

	seenExact[rawURL] = struct{}{}
	if label != "" {
		seenLabel[label] = struct{}{}
	}
	*out = append(*out, richTextLinkListItem(rawURL, suffix, maxRich))
}

func richTextLinkListItem(url, suffix string, maxRich int) *slack.RichTextSection {
	label := slackLinkLabel(url)
	if suffix == "" {
		return slack.NewRichTextSection(
			slack.NewRichTextSectionLinkElement(url, label, nil),
		)
	}
	full := " — " + suffix
	chunks := ChunkMrkdwnSectionText(full, maxRich)
	if len(chunks) == 1 {
		return slack.NewRichTextSection(
			slack.NewRichTextSectionLinkElement(url, label, nil),
			slack.NewRichTextSectionTextElement(chunks[0], nil),
		)
	}
	secElems := make([]slack.RichTextSectionElement, 0, 1+len(chunks))
	secElems = append(secElems, slack.NewRichTextSectionLinkElement(url, label, nil))
	for _, c := range chunks {
		secElems = append(secElems, slack.NewRichTextSectionTextElement(c, nil))
	}
	return slack.NewRichTextSection(secElems...)
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func slackLinkLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return raw
	}
	label := strings.TrimPrefix(u.Hostname(), "www.")
	if len(label) > 48 {
		label = label[:45] + "…"
	}
	return label
}
