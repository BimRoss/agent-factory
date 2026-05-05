package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/slackrender"
	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

// HandleTermsInteraction processes Block Kit clicks for terms_accept (I Agree / I Do Not Agree).
func HandleTermsInteraction(ctx context.Context, api *slack.Client, cb slack.InteractionCallback) bool {
	if api == nil {
		return false
	}
	action, ok := ParseTermsSkillConfirmationAction(cb)
	if !ok {
		return false
	}
	clickUser := strings.TrimSpace(cb.User.ID)
	if clickUser == "" {
		return false
	}

	ch := strings.TrimSpace(action.Channel)
	if ch == "" {
		ch = strings.TrimSpace(cb.Channel.ID)
	}
	if ch == "" {
		ch = strings.TrimSpace(cb.Container.ChannelID)
	}
	threadTS := strings.TrimSpace(action.ThreadTS)
	if threadTS == "" {
		threadTS = strings.TrimSpace(cb.Container.ThreadTs)
	}
	if threadTS == "" {
		threadTS = strings.TrimSpace(cb.Message.ThreadTimestamp)
	}
	if threadTS == "" {
		threadTS = strings.TrimSpace(cb.Container.MessageTs)
	}

	if clickUser != strings.TrimSpace(action.RequestUserID) {
		postTermsEphemeral(ctx, api, ch, clickUser, threadTS,
			"Only the teammate who opened this terms prompt can confirm or cancel.")
		return true
	}

	messageTS := strings.TrimSpace(cb.Container.MessageTs)
	if messageTS == "" {
		messageTS = strings.TrimSpace(cb.MessageTs)
	}

	var rdb *redis.Client
	defer func() {
		if rdb != nil {
			_ = rdb.Close()
		}
	}()
	if redisURL := strings.TrimSpace(os.Getenv("REDIS_URL")); redisURL != "" {
		c, err := redisOpen(redisURL)
		if err != nil {
			log.Printf("terms_interactive: redis open err=%v — skipping pending gate/clear paths", err)
		} else {
			rdb = c
		}
	}
	if rdb != nil {
		_, active, ttlErr := TermsSkillPendingTTL(ctx, rdb, ch, strings.TrimSpace(action.RequestUserID), threadTS)
		if ttlErr != nil {
			log.Printf("terms_interactive: redis pending read err=%v — skipping pending gate", ttlErr)
		} else if !active {
			postTermsEphemeral(ctx, api, ch, clickUser, threadTS, termsSkillConfirmationExpiredMsg)
			return true
		}
	}

	switch action.Decision {
	case skillConfirmationDecisionConfirm:
		if err := recordHumansTermsAcceptedWithRetry(ctx, action.RequestUserID, messageTS); err != nil {
			log.Printf("terms_interactive: record accept slack_user=%s err=%v", action.RequestUserID, err)
			postTermsThreadMessage(ctx, api, ch, threadTS,
				"I could not record your agreement yet because your workspace profile index is still syncing. Please tap *I Agree* again in a few seconds, or reply with *I Agree* here.")
			return true
		}
		if rdb != nil {
			_ = ClearTermsSkillPendingWithClient(ctx, rdb, ch, action.RequestUserID, threadTS)
		}
		finalizeSkillConfirmationButtonMessage(ctx, api, ch, messageTS, cb, skillConfirmationDecisionConfirm, clickUser)
		epilogue := ""
		if line, cerr := RunCreateCompanyAfterTermsAccept(ctx, api, clickUser); cerr != nil {
			log.Printf("terms_interactive: auto create-company slack_user=%s err=%v", clickUser, cerr)
		} else {
			epilogue = line
		}
		postTermsThreadMessage(ctx, api, ch, threadTS, FormatHumansTermsAcceptThankYou(epilogue))
		return true

	case skillConfirmationDecisionCancel:
		if rdb != nil && strings.TrimSpace(action.RequestUserID) != "" {
			if err := ClearHumansTermsAccepted(ctx, rdb, "", "", action.RequestUserID); err != nil {
				log.Printf("terms_interactive: clear declined slack_user=%s err=%v", action.RequestUserID, err)
			}
		}
		if rdb != nil {
			_ = ClearTermsSkillPendingWithClient(ctx, rdb, ch, action.RequestUserID, threadTS)
		}
		finalizeSkillConfirmationButtonMessage(ctx, api, ch, messageTS, cb, skillConfirmationDecisionCancel, clickUser)
		postTermsThreadMessage(ctx, api, ch, threadTS, humansTermsRejectedText(joanneWorkspaceMention()))
		return true
	default:
		return false
	}
}

func finalizeSkillConfirmationButtonMessage(parent context.Context, api *slack.Client, channelID, messageTS string, cb slack.InteractionCallback, decision skillConfirmationDecision, clickUserID string) {
	channelID = strings.TrimSpace(channelID)
	messageTS = strings.TrimSpace(messageTS)
	if channelID == "" || messageTS == "" || api == nil {
		return
	}

	blocks := pruneTermsActionBlocks(cb.Message.Blocks.BlockSet)
	actionLabel := "Confirmed"
	if decision == skillConfirmationDecisionCancel {
		actionLabel = "Canceled"
	}
	statusText := fmt.Sprintf(
		"Status: *%s* by <@%s> %s.",
		actionLabel,
		strings.TrimSpace(clickUserID),
		slackDateTimeToken(time.Now().UTC()),
	)
	blocks = append(blocks, slack.NewContextBlock(
		"skill_confirmation_state",
		slack.NewTextBlockObject("mrkdwn", statusText, false, false),
	))

	fallback := confirmationSummaryFallbackFromInteraction(cb)
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	_, _, _, err := api.UpdateMessageContext(ctx, channelID, messageTS,
		slack.MsgOptionText(fallback, false),
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		log.Printf("skill_confirmation: update confirmation message ts=%s err=%v", messageTS, err)
	}
}

func confirmationSummaryFallbackFromInteraction(cb slack.InteractionCallback) string {
	for _, b := range cb.Message.Blocks.BlockSet {
		sec, ok := b.(*slack.SectionBlock)
		if !ok || sec.Text == nil {
			continue
		}
		t := strings.TrimSpace(sec.Text.Text)
		if t != "" {
			return t
		}
	}
	return "Terms of Use"
}

func postTermsEphemeral(parent context.Context, api *slack.Client, channelID, userID, threadTS, text string) {
	channelID = strings.TrimSpace(channelID)
	userID = strings.TrimSpace(userID)
	text = strings.TrimSpace(text)
	if api == nil || channelID == "" || userID == "" || text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	_, err := api.PostEphemeralContext(ctx, channelID, userID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionPostMessageParameters(slack.PostMessageParameters{
			ThreadTimestamp: strings.TrimSpace(threadTS),
		}),
	)
	if err != nil {
		log.Printf("terms_interactive: ephemeral err=%v", err)
	}
}

func postTermsThreadMessage(parent context.Context, api *slack.Client, channelID, threadTS, text string) {
	channelID = strings.TrimSpace(channelID)
	threadTS = strings.TrimSpace(threadTS)
	text = strings.TrimSpace(text)
	if api == nil || channelID == "" || threadTS == "" || text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	blocks, fallback := slackrender.AgentReplyBlocks(text)
	opts := []slack.MsgOption{slack.MsgOptionText(fallback, false)}
	if len(blocks) > 0 {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	opts = append(opts, slack.MsgOptionTS(threadTS))
	if _, _, err := api.PostMessageContext(ctx, channelID, opts...); err != nil {
		log.Printf("terms_interactive: thread follow-up err=%v", err)
	}
}
