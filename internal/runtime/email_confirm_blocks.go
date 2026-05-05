package runtime

import (
	"strings"

	"github.com/slack-go/slack"
)

// BuildEmailConfirmationBlocks matches employee-factory buildSkillConfirmationBlocks for skillConfirmationTaskEmail.
func BuildEmailConfirmationBlocks(summaryMrkdwn, channel, requestUserID, threadAnchor string) []slack.Block {
	channel = strings.TrimSpace(channel)
	requestUserID = strings.TrimSpace(requestUserID)
	threadAnchor = strings.TrimSpace(threadAnchor)
	summaryMrkdwn = strings.TrimSpace(summaryMrkdwn)

	confirmValue := encodeEmailSkillConfirmationValue(skillConfirmationDecisionConfirm, channel, requestUserID, threadAnchor)
	cancelValue := encodeEmailSkillConfirmationValue(skillConfirmationDecisionCancel, channel, requestUserID, threadAnchor)

	confirmBtn := slack.NewButtonBlockElement(skillConfirmationActionIDConfirm, confirmValue, slack.NewTextBlockObject("plain_text", "Confirm", false, false))
	confirmBtn.Style = slack.StylePrimary
	cancelBtn := slack.NewButtonBlockElement(skillConfirmationActionIDCancel, cancelValue, slack.NewTextBlockObject("plain_text", "Cancel", false, false))
	cancelBtn.Style = slack.StyleDanger

	return []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", summaryMrkdwn, false, false), nil, nil),
		slack.NewActionBlock(skillConfirmationActionBlockID, confirmBtn, cancelBtn),
	}
}

func encodeEmailSkillConfirmationValue(decision skillConfirmationDecision, channel, requestUserID, threadAnchor string) string {
	parts := []string{
		skillConfirmationTaskEmailWire,
		string(decision),
		strings.TrimSpace(channel),
		strings.TrimSpace(requestUserID),
		strings.TrimSpace(threadAnchor),
	}
	return strings.Join(parts, "|")
}

// EmailSkillConfirmationAction is decoded from Block Kit button values for email_send.
type EmailSkillConfirmationAction struct {
	Decision      skillConfirmationDecision
	Channel       string
	RequestUserID string
	ThreadTS      string
}

// ParseEmailSkillConfirmationAction decodes create-email Confirm/Cancel interactions.
func ParseEmailSkillConfirmationAction(cb slack.InteractionCallback) (EmailSkillConfirmationAction, bool) {
	if cb.Type != slack.InteractionTypeBlockActions {
		return EmailSkillConfirmationAction{}, false
	}
	if len(cb.ActionCallback.BlockActions) == 0 {
		return EmailSkillConfirmationAction{}, false
	}
	actionInput := cb.ActionCallback.BlockActions[0]
	action, ok := decodeEmailSkillConfirmationValue(actionInput.Value)
	if !ok {
		return EmailSkillConfirmationAction{}, false
	}
	switch strings.TrimSpace(actionInput.ActionID) {
	case skillConfirmationActionIDConfirm:
		action.Decision = skillConfirmationDecisionConfirm
	case skillConfirmationActionIDCancel:
		action.Decision = skillConfirmationDecisionCancel
	default:
		return EmailSkillConfirmationAction{}, false
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

func decodeEmailSkillConfirmationValue(raw string) (EmailSkillConfirmationAction, bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), "|", 5)
	if len(parts) != 5 {
		return EmailSkillConfirmationAction{}, false
	}
	if strings.TrimSpace(parts[0]) != skillConfirmationTaskEmailWire {
		return EmailSkillConfirmationAction{}, false
	}
	action := EmailSkillConfirmationAction{
		Decision:      skillConfirmationDecision(strings.TrimSpace(parts[1])),
		Channel:       strings.TrimSpace(parts[2]),
		RequestUserID: strings.TrimSpace(parts[3]),
		ThreadTS:      strings.TrimSpace(parts[4]),
	}
	switch action.Decision {
	case skillConfirmationDecisionConfirm, skillConfirmationDecisionCancel:
	default:
		return EmailSkillConfirmationAction{}, false
	}
	return action, true
}
