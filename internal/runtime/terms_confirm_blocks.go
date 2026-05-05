package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// Stable wire strings — keep aligned with employee-factory internal/slackbot/write_flow_confirmation.go.
const (
	skillConfirmationActionIDConfirm = "joanne_confirmation_confirm"
	skillConfirmationActionIDCancel  = "joanne_confirmation_cancel"
	skillConfirmationActionBlockID   = "joanne_confirmation_actions"
	skillConfirmationTaskTermsWire   = "terms_accept"
	skillConfirmationTaskEmailWire   = "email_send"
)

type skillConfirmationDecision string

const (
	skillConfirmationDecisionConfirm skillConfirmationDecision = "confirm"
	skillConfirmationDecisionCancel  skillConfirmationDecision = "cancel"
)

// TermsSkillConfirmationAction is encoded into button values (pipe-separated).
type TermsSkillConfirmationAction struct {
	Decision      skillConfirmationDecision
	Channel       string
	RequestUserID string
	ThreadTS      string
}

// HumansTermsThreadSummaryMrkdwn matches employee-factory humansTermsThreadSummary.
func HumansTermsThreadSummaryMrkdwn(webOrigin string) string {
	origin := strings.TrimSuffix(strings.TrimSpace(webOrigin), "/")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	termsURL := origin + "/terms"
	return fmt.Sprintf(
		"Here are the <%s|*terms*>. Please read them, and then answer below.",
		termsURL,
	)
}

func makeacompanyWebOriginForTerms() string {
	webOrigin := strings.TrimSuffix(strings.TrimSpace(firstNonEmptyEnv(
		"MAKEACOMPANY_APP_BASE_URL", "APP_BASE_URL", "NEXT_PUBLIC_SITE_URL",
	)), "/")
	if webOrigin == "" {
		return "http://localhost:3000"
	}
	return webOrigin
}

// TermsToolThreadAnchor matches employee-factory replyThreadTS(..., useToolThread=true).
func TermsToolThreadAnchor(incomingThreadTS, channelRootMessageTS string) string {
	if ts := strings.TrimSpace(incomingThreadTS); ts != "" {
		return ts
	}
	return strings.TrimSpace(channelRootMessageTS)
}

// BuildTermsAcceptanceBlocks returns the threaded terms section + I Agree / I Do Not Agree buttons.
func BuildTermsAcceptanceBlocks(channel, requestUserID, threadAnchor string) []slack.Block {
	webOrigin := makeacompanyWebOriginForTerms()
	summary := HumansTermsThreadSummaryMrkdwn(webOrigin)
	return BuildTermsAcceptanceBlocksWithSummary(channel, requestUserID, threadAnchor, summary)
}

// BuildTermsAcceptanceBlocksWithSummary allows admin trigger paths to pass an explicit terms URL base.
func BuildTermsAcceptanceBlocksWithSummary(channel, requestUserID, threadAnchor, summaryMrkdwn string) []slack.Block {
	channel = strings.TrimSpace(channel)
	requestUserID = strings.TrimSpace(requestUserID)
	threadAnchor = strings.TrimSpace(threadAnchor)
	summaryMrkdwn = strings.TrimSpace(summaryMrkdwn)

	confirmValue := encodeTermsSkillConfirmationValue(TermsSkillConfirmationAction{
		Decision:      skillConfirmationDecisionConfirm,
		Channel:       channel,
		RequestUserID: requestUserID,
		ThreadTS:      threadAnchor,
	})
	cancelValue := encodeTermsSkillConfirmationValue(TermsSkillConfirmationAction{
		Decision:      skillConfirmationDecisionCancel,
		Channel:       channel,
		RequestUserID: requestUserID,
		ThreadTS:      threadAnchor,
	})
	confirmBtn := slack.NewButtonBlockElement(skillConfirmationActionIDConfirm, confirmValue, slack.NewTextBlockObject("plain_text", "I Agree", false, false))
	confirmBtn.Style = slack.StylePrimary
	cancelBtn := slack.NewButtonBlockElement(skillConfirmationActionIDCancel, cancelValue, slack.NewTextBlockObject("plain_text", "I Do Not Agree", false, false))
	cancelBtn.Style = slack.StyleDanger

	return []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", summaryMrkdwn, false, false), nil, nil),
		slack.NewActionBlock(skillConfirmationActionBlockID, confirmBtn, cancelBtn),
	}
}

func encodeTermsSkillConfirmationValue(a TermsSkillConfirmationAction) string {
	parts := []string{
		skillConfirmationTaskTermsWire,
		string(a.Decision),
		strings.TrimSpace(a.Channel),
		strings.TrimSpace(a.RequestUserID),
		strings.TrimSpace(a.ThreadTS),
	}
	return strings.Join(parts, "|")
}

// ParseTermsSkillConfirmationAction decodes the first block action on a terms acceptance message.
func ParseTermsSkillConfirmationAction(cb slack.InteractionCallback) (TermsSkillConfirmationAction, bool) {
	if cb.Type != slack.InteractionTypeBlockActions {
		return TermsSkillConfirmationAction{}, false
	}
	if len(cb.ActionCallback.BlockActions) == 0 {
		return TermsSkillConfirmationAction{}, false
	}
	actionInput := cb.ActionCallback.BlockActions[0]
	action, ok := decodeTermsSkillConfirmationValue(actionInput.Value)
	if !ok {
		return TermsSkillConfirmationAction{}, false
	}
	switch strings.TrimSpace(actionInput.ActionID) {
	case skillConfirmationActionIDConfirm:
		action.Decision = skillConfirmationDecisionConfirm
	case skillConfirmationActionIDCancel:
		action.Decision = skillConfirmationDecisionCancel
	default:
		return TermsSkillConfirmationAction{}, false
	}
	if action.Channel == "" {
		action.Channel = strings.TrimSpace(cb.Channel.ID)
	}
	if action.Channel == "" {
		action.Channel = strings.TrimSpace(cb.Container.ChannelID)
	}
	if action.ThreadTS == "" {
		action.ThreadTS = strings.TrimSpace(cb.Container.ThreadTs)
	}
	if action.ThreadTS == "" {
		action.ThreadTS = strings.TrimSpace(cb.Message.ThreadTimestamp)
	}
	if action.ThreadTS == "" {
		action.ThreadTS = strings.TrimSpace(cb.Container.MessageTs)
	}
	return action, true
}

func decodeTermsSkillConfirmationValue(raw string) (TermsSkillConfirmationAction, bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), "|", 5)
	if len(parts) != 5 {
		return TermsSkillConfirmationAction{}, false
	}
	if strings.TrimSpace(parts[0]) != skillConfirmationTaskTermsWire {
		return TermsSkillConfirmationAction{}, false
	}
	action := TermsSkillConfirmationAction{
		Decision:      skillConfirmationDecision(strings.TrimSpace(parts[1])),
		Channel:       strings.TrimSpace(parts[2]),
		RequestUserID: strings.TrimSpace(parts[3]),
		ThreadTS:      strings.TrimSpace(parts[4]),
	}
	switch action.Decision {
	case skillConfirmationDecisionConfirm, skillConfirmationDecisionCancel:
	default:
		return TermsSkillConfirmationAction{}, false
	}
	return action, true
}

func pruneTermsActionBlocks(blocks []slack.Block) []slack.Block {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]slack.Block, 0, len(blocks))
	for _, block := range blocks {
		ab, ok := block.(*slack.ActionBlock)
		if ok && strings.TrimSpace(ab.BlockID) == skillConfirmationActionBlockID {
			continue
		}
		out = append(out, block)
	}
	return out
}

func slackDateTimeToken(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("<!date^%d^{date_short_pretty} at {time}|just now>", t.Unix())
}
