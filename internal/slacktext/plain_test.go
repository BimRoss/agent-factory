package slacktext

import (
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func TestMessagePlainTextForLLM_BlocksOnly(t *testing.T) {
	msg := slack.Message{
		Msg: slack.Msg{
			Blocks: slack.Blocks{BlockSet: []slack.Block{
				slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", "visible *body*", false, false), nil, nil),
			}},
		},
	}
	got := strings.TrimSpace(MessagePlainTextForLLM(msg))
	if got != "visible *body*" {
		t.Fatalf("got %q", got)
	}
}

func TestMessagePlainTextForLLM_MergesNotifyAndCards(t *testing.T) {
	title := slack.NewTextBlockObject("plain_text", "Card title", false, false)
	body := slack.NewTextBlockObject("mrkdwn", "Full digest line one.\nLine two.", false, false)
	card := slack.NewCardBlock().WithTitle(title).WithBody(body)
	msg := slack.Message{
		Msg: slack.Msg{
			Text: "Recent company activity (1 highlight)",
			Blocks: slack.Blocks{BlockSet: []slack.Block{
				card,
			}},
		},
	}
	got := MessagePlainTextForLLM(msg)
	if !strings.Contains(got, "Recent company activity") {
		t.Fatalf("expected notify line in output: %q", got)
	}
	if !strings.Contains(got, "Full digest line one") {
		t.Fatalf("expected block body in output: %q", got)
	}
	if !strings.Contains(got, "Card title") {
		t.Fatalf("expected card title in output: %q", got)
	}
}

func TestMessagePlainTextForLLM_MergesNotifyAndSectionStack(t *testing.T) {
	msg := slack.Message{
		Msg: slack.Msg{
			Text: "Recent company activity (2 highlights)",
			Blocks: slack.Blocks{BlockSet: []slack.Block{
				slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", "*Successful Production Deployment*\n\nBuild went out clean.", false, false), nil, nil),
				slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", "*Soft Launch Preparation*\n\nTargeting May 1.", false, false), nil, nil),
			}},
		},
	}
	got := MessagePlainTextForLLM(msg)
	if !strings.Contains(got, "Recent company activity (2 highlights)") {
		t.Fatalf("expected notify line in output: %q", got)
	}
	if !strings.Contains(got, "Successful Production Deployment") || !strings.Contains(got, "Soft Launch Preparation") {
		t.Fatalf("expected section titles in output: %q", got)
	}
}

func TestMessagePlainTextForLLM_RichTextTable(t *testing.T) {
	rt := slack.NewRichTextBlock("",
		slack.NewRichTextSection(
			slack.NewRichTextSectionTextElement("cell-a", nil),
		),
	)
	tb := slack.NewTableBlock("").AddRow(rt)
	msg := slack.Message{Msg: slack.Msg{Blocks: slack.Blocks{BlockSet: []slack.Block{tb}}}}
	got := strings.TrimSpace(MessagePlainTextForLLM(msg))
	if got != "cell-a" {
		t.Fatalf("got %q", got)
	}
}

func TestMessagePlainTextForLLM_OmitsButtonCallbackValue(t *testing.T) {
	msg := slack.Message{
		Msg: slack.Msg{
			Text: "How does this look?",
			Blocks: slack.Blocks{BlockSet: []slack.Block{
				slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", "Summary line", false, false), nil, nil),
				slack.NewActionBlock("actions",
					slack.NewButtonBlockElement("confirm", "secret|callback|payload", slack.NewTextBlockObject("plain_text", "Confirm", false, false)),
					slack.NewButtonBlockElement("cancel", "other-secret", slack.NewTextBlockObject("plain_text", "Cancel", false, false)),
				),
			}},
		},
	}
	got := MessagePlainTextForLLM(msg)
	if strings.Contains(got, "secret|callback") || strings.Contains(got, "other-secret") {
		t.Fatalf("button values must not appear in LLM plaintext: %q", got)
	}
	if !strings.Contains(got, "Confirm") || !strings.Contains(got, "Cancel") {
		t.Fatalf("expected visible button labels: %q", got)
	}
}

func TestMessagePlainTextForLLM_CarouselCards(t *testing.T) {
	card := slack.NewCardBlock().
		WithTitle(slack.NewTextBlockObject("mrkdwn", "*@isfjcutebear*", false, false)).
		WithBody(slack.NewTextBlockObject("mrkdwn", "Privacy law post text", false, false)).
		WithActions(
			slack.NewButtonBlockElement(
				"see_tweet",
				"",
				slack.NewTextBlockObject("plain_text", "View Tweet", false, false),
			).WithURL("https://x.com/i/web/status/123"),
		)
	carousel := slack.NewCarouselBlock(card)
	msg := slack.Message{
		Msg: slack.Msg{
			Blocks: slack.Blocks{BlockSet: []slack.Block{carousel}},
		},
	}

	got := MessagePlainTextForLLM(msg)
	if !strings.Contains(got, "@isfjcutebear") {
		t.Fatalf("expected card title handle in output: %q", got)
	}
	if !strings.Contains(got, "Privacy law post text") {
		t.Fatalf("expected card body in output: %q", got)
	}
	if !strings.Contains(got, "https://x.com/i/web/status/123") {
		t.Fatalf("expected action URL in output: %q", got)
	}
}
