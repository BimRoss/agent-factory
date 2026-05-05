// Package slacktext extracts human/LLM-oriented plaintext from Slack messages, including Block Kit.
// Used by slackbot (thread/history context) and channel-knowledge-refresh (Redis digest) for consistency.
package slacktext

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// MessagePlainTextForLLM returns text suitable for digests, thread history, and downstream agents.
// It merges the top-level message Text field with plaintext extracted from Block Kit blocks so
// block-only or card-heavy posts (e.g. read-company) do not collapse to notification fallbacks.
func MessagePlainTextForLLM(m slack.Message) string {
	top := strings.TrimSpace(m.Text)
	fromBlocks := strings.TrimSpace(extractPlainTextFromBlocks(m.Blocks.BlockSet))
	fileHint := slackFileHintForLLM(m.Files)
	var merged string
	if top == "" {
		merged = fromBlocks
	} else if fromBlocks == "" {
		merged = top
	} else if top == fromBlocks {
		merged = top
	} else if strings.Contains(fromBlocks, top) {
		merged = fromBlocks
	} else if strings.Contains(top, fromBlocks) {
		merged = top
	} else {
		merged = top + "\n" + fromBlocks
	}
	if fileHint == "" {
		return merged
	}
	if merged == "" {
		return fileHint
	}
	return merged + "\n" + fileHint
}

func slackFileHintForLLM(files []slack.File) string {
	if len(files) == 0 {
		return ""
	}
	var parts []string
	for _, f := range files {
		id := strings.TrimSpace(f.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(f.Name)
		mt := strings.TrimSpace(f.Mimetype)
		parts = append(parts, fmt.Sprintf("[slack file id=%s name=%q mimetype=%q]", id, name, mt))
	}
	return strings.Join(parts, "\n")
}

func extractPlainTextFromBlocks(blocks []slack.Block) string {
	if len(blocks) == 0 {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b == nil {
			continue
		}
		if s := blockPlainText(b); strings.TrimSpace(s) != "" {
			parts = append(parts, strings.TrimSpace(s))
		}
	}
	return strings.Join(parts, "\n")
}

func blockPlainText(b slack.Block) string {
	switch v := b.(type) {
	case *slack.SectionBlock:
		return joinNonEmpty(
			textBlockObjectPlain(v.Text),
			fieldsPlain(v.Fields),
			accessoryPlain(v.Accessory),
		)
	case *slack.HeaderBlock:
		return textBlockObjectPlain(v.Text)
	case *slack.ContextBlock:
		return contextElementsPlain(v.ContextElements.Elements)
	case *slack.ActionBlock:
		return blockElementsPlain(v.Elements)
	case *slack.DividerBlock:
		return ""
	case *slack.ImageBlock:
		return joinNonEmpty(
			strings.TrimSpace(v.AltText),
			textBlockObjectPlain(v.Title),
		)
	case *slack.RichTextBlock:
		return richTextElementsPlain(v.Elements)
	case *slack.MarkdownBlock:
		return strings.TrimSpace(v.Text)
	case *slack.TableBlock:
		return tableRowsPlain(v.Rows)
	case *slack.CardBlock:
		return joinNonEmpty(
			textBlockObjectPlain(v.Title),
			textBlockObjectPlain(v.Subtitle),
			textBlockObjectPlain(v.Body),
			imageElementAlt(v.HeroImage),
			imageElementAlt(v.Icon),
			blockElementsPlain(v.Actions),
		)
	case *slack.CarouselBlock:
		var sub []string
		for _, c := range v.Elements {
			if c == nil {
				continue
			}
			if s := blockPlainText(c); s != "" {
				sub = append(sub, s)
			}
		}
		return strings.Join(sub, "\n")
	case *slack.TaskCardBlock:
		return joinNonEmpty(
			strings.TrimSpace(v.Title),
			strings.TrimSpace(string(v.Status)),
			richTextBlockPlain(v.Details),
			richTextBlockPlain(v.Output),
			taskCardSourcesPlain(v.Sources),
		)
	case *slack.PlanBlock:
		var sub []string
		sub = append(sub, strings.TrimSpace(v.Title))
		for i := range v.Tasks {
			t := v.Tasks[i]
			sub = append(sub, blockPlainText(&t))
		}
		return joinNonEmpty(sub...)
	case *slack.InputBlock:
		return joinNonEmpty(
			textBlockObjectPlain(v.Label),
			textBlockObjectPlain(v.Hint),
			blockElementPlain(v.Element),
		)
	case *slack.FileBlock:
		return joinNonEmpty(v.Source, v.ExternalID)
	case *slack.VideoBlock:
		return joinNonEmpty(
			strings.TrimSpace(v.AltText),
			textBlockObjectPlain(v.Title),
			textBlockObjectPlain(v.Description),
			strings.TrimSpace(v.AuthorName),
			strings.TrimSpace(v.ProviderName),
		)
	case *slack.CallBlock:
		if v.Call != nil && v.Call.V1 != nil && strings.TrimSpace(v.Call.V1.Name) != "" {
			return strings.TrimSpace(v.Call.V1.Name)
		}
		return strings.TrimSpace(v.CallID)
	default:
		return ""
	}
}

func taskCardSourcesPlain(src []slack.TaskCardSource) string {
	var parts []string
	for _, s := range src {
		parts = append(parts, joinNonEmpty(s.Type, s.URL, s.Text))
	}
	return joinNonEmpty(parts...)
}

func tableRowsPlain(rows [][]*slack.RichTextBlock) string {
	var lines []string
	for _, row := range rows {
		var cells []string
		for _, cell := range row {
			if cell == nil {
				continue
			}
			if t := richTextElementsPlain(cell.Elements); t != "" {
				cells = append(cells, t)
			}
		}
		if len(cells) > 0 {
			lines = append(lines, strings.Join(cells, " | "))
		}
	}
	return strings.Join(lines, "\n")
}

func contextElementsPlain(elements []slack.MixedElement) string {
	var parts []string
	for _, el := range elements {
		switch v := el.(type) {
		case *slack.TextBlockObject:
			parts = append(parts, textBlockObjectPlain(v))
		case *slack.ImageBlockElement:
			parts = append(parts, strings.TrimSpace(v.AltText))
		}
	}
	return joinNonEmpty(parts...)
}

func fieldsPlain(fields []*slack.TextBlockObject) string {
	var parts []string
	for _, f := range fields {
		parts = append(parts, textBlockObjectPlain(f))
	}
	return joinNonEmpty(parts...)
}

func textBlockObjectPlain(t *slack.TextBlockObject) string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.Text)
}

func richTextBlockPlain(b *slack.RichTextBlock) string {
	if b == nil {
		return ""
	}
	return richTextElementsPlain(b.Elements)
}

func richTextElementsPlain(elems []slack.RichTextElement) string {
	var parts []string
	for _, e := range elems {
		if e == nil {
			continue
		}
		switch v := e.(type) {
		case *slack.RichTextSection:
			parts = append(parts, richTextSectionElementsPlain(v.Elements))
		case *slack.RichTextList:
			parts = append(parts, richTextElementsPlain(v.Elements))
		case *slack.RichTextQuote:
			parts = append(parts, richTextSectionElementsPlain(v.Elements))
		case *slack.RichTextPreformatted:
			parts = append(parts, richTextSectionElementsPlain(v.Elements))
		}
	}
	return joinNonEmpty(parts...)
}

func richTextSectionElementsPlain(elems []slack.RichTextSectionElement) string {
	var parts []string
	for _, e := range elems {
		if e == nil {
			continue
		}
		switch v := e.(type) {
		case *slack.RichTextSectionTextElement:
			parts = append(parts, strings.TrimSpace(v.Text))
		case *slack.RichTextSectionLinkElement:
			parts = append(parts, joinNonEmpty(strings.TrimSpace(v.Text), v.URL))
		case *slack.RichTextSectionChannelElement:
			parts = append(parts, v.ChannelID)
		case *slack.RichTextSectionUserElement:
			parts = append(parts, v.UserID)
		case *slack.RichTextSectionEmojiElement:
			if v.Unicode != "" {
				parts = append(parts, v.Unicode)
			} else {
				parts = append(parts, ":"+v.Name+":")
			}
		case *slack.RichTextSectionTeamElement:
			parts = append(parts, v.TeamID)
		case *slack.RichTextSectionUserGroupElement:
			parts = append(parts, v.UsergroupID)
		case *slack.RichTextSectionDateElement:
			if v.Fallback != nil {
				parts = append(parts, strings.TrimSpace(*v.Fallback))
			}
		case *slack.RichTextSectionBroadcastElement:
			parts = append(parts, v.Range)
		case *slack.RichTextSectionColorElement:
			parts = append(parts, v.Value)
		}
	}
	return joinNonEmpty(parts...)
}

func blockElementsPlain(be *slack.BlockElements) string {
	if be == nil {
		return ""
	}
	var parts []string
	for _, e := range be.ElementSet {
		if s := blockElementPlain(e); strings.TrimSpace(s) != "" {
			parts = append(parts, strings.TrimSpace(s))
		}
	}
	return joinNonEmpty(parts...)
}

func blockElementPlain(e slack.BlockElement) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *slack.ImageBlockElement:
		return strings.TrimSpace(v.AltText)
	case *slack.ButtonBlockElement:
		// Omit v.Value: it is an opaque callback payload (e.g. skill confirmation tokens). Including it
		// in digests/LLM context teaches models to echo "Confirm\n<value>" as plain text instead of
		// using Block Kit actions. Link buttons still surface v.URL.
		return joinNonEmpty(textBlockObjectPlain(v.Text), v.URL)
	case *slack.OverflowBlockElement:
		return overflowOptionsPlain(v.Options)
	case *slack.DatePickerBlockElement:
		return textBlockObjectPlain(v.Placeholder)
	case *slack.TimePickerBlockElement:
		return textBlockObjectPlain(v.Placeholder)
	case *slack.DateTimePickerBlockElement:
		return confirmationPlainText(v.Confirm)
	case *slack.PlainTextInputBlockElement:
		return textBlockObjectPlain(v.Placeholder)
	case *slack.RichTextInputBlockElement:
		return textBlockObjectPlain(v.Placeholder)
	case *slack.SelectBlockElement:
		return joinNonEmpty(
			textBlockObjectPlain(v.Placeholder),
			optionBlockPlain(v.InitialOption),
			optionsPlain(v.Options),
			optionGroupsPlain(v.OptionGroups),
		)
	case *slack.MultiSelectBlockElement:
		return joinNonEmpty(
			textBlockObjectPlain(v.Placeholder),
			optionsPlain(v.Options),
			optionGroupsPlain(v.OptionGroups),
		)
	case *slack.CheckboxGroupsBlockElement:
		return optionsPlain(v.Options)
	case *slack.RadioButtonsBlockElement:
		return optionsPlain(v.Options)
	case *slack.WorkflowButtonBlockElement:
		return joinNonEmpty(textBlockObjectPlain(v.Text), v.AccessibilityLabel)
	default:
		return ""
	}
}

func optionGroupsPlain(groups []*slack.OptionGroupBlockObject) string {
	var parts []string
	for _, g := range groups {
		parts = append(parts, joinNonEmpty(textBlockObjectPlain(g.Label), optionsPlain(g.Options)))
	}
	return joinNonEmpty(parts...)
}

func optionsPlain(opts []*slack.OptionBlockObject) string {
	var parts []string
	for _, o := range opts {
		parts = append(parts, optionBlockPlain(o))
	}
	return joinNonEmpty(parts...)
}

func optionBlockPlain(o *slack.OptionBlockObject) string {
	if o == nil {
		return ""
	}
	return joinNonEmpty(textBlockObjectPlain(o.Text), textBlockObjectPlain(o.Description), o.Value, o.URL)
}

func overflowOptionsPlain(opts []*slack.OptionBlockObject) string {
	return optionsPlain(opts)
}

func accessoryPlain(a *slack.Accessory) string {
	if a == nil {
		return ""
	}
	if a.ImageElement != nil {
		return strings.TrimSpace(a.ImageElement.AltText)
	}
	if a.ButtonElement != nil {
		return blockElementPlain(a.ButtonElement)
	}
	if a.OverflowElement != nil {
		return blockElementPlain(a.OverflowElement)
	}
	if a.DatePickerElement != nil {
		return blockElementPlain(a.DatePickerElement)
	}
	if a.TimePickerElement != nil {
		return blockElementPlain(a.TimePickerElement)
	}
	if a.PlainTextInputElement != nil {
		return blockElementPlain(a.PlainTextInputElement)
	}
	if a.RichTextInputElement != nil {
		return blockElementPlain(a.RichTextInputElement)
	}
	if a.RadioButtonsElement != nil {
		return blockElementPlain(a.RadioButtonsElement)
	}
	if a.SelectElement != nil {
		return blockElementPlain(a.SelectElement)
	}
	if a.MultiSelectElement != nil {
		return blockElementPlain(a.MultiSelectElement)
	}
	if a.CheckboxGroupsBlockElement != nil {
		return blockElementPlain(a.CheckboxGroupsBlockElement)
	}
	if a.WorkflowButtonElement != nil {
		return blockElementPlain(a.WorkflowButtonElement)
	}
	return ""
}

func imageElementAlt(img *slack.ImageBlockElement) string {
	if img == nil {
		return ""
	}
	return strings.TrimSpace(img.AltText)
}

func confirmationPlainText(c *slack.ConfirmationBlockObject) string {
	if c == nil {
		return ""
	}
	return joinNonEmpty(
		textBlockObjectPlain(c.Title),
		textBlockObjectPlain(c.Text),
		textBlockObjectPlain(c.Confirm),
		textBlockObjectPlain(c.Deny),
	)
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}
