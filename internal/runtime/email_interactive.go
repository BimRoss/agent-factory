package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
)

// HandleEmailInteraction processes Block Kit Confirm/Cancel for create-email (email_send).
func HandleEmailInteraction(ctx context.Context, api *slack.Client, cb slack.InteractionCallback) bool {
	if api == nil {
		return false
	}
	action, ok := ParseEmailSkillConfirmationAction(cb)
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
			"Only the teammate who queued this email can confirm or cancel.")
		return true
	}

	messageTS := strings.TrimSpace(cb.Container.MessageTs)
	if messageTS == "" {
		messageTS = strings.TrimSpace(cb.MessageTs)
	}

	rdb, cleanup := redisClientFromEnvOptional(ctx)
	defer cleanup()

	if rdb != nil {
		_, active, ttlErr := EmailSkillPendingTTL(ctx, rdb, ch, strings.TrimSpace(action.RequestUserID), threadTS)
		if ttlErr != nil {
			log.Printf("email_interactive: redis pending read err=%v", ttlErr)
		} else if !active {
			postTermsEphemeral(ctx, api, ch, clickUser, threadTS, termsSkillConfirmationExpiredMsg)
			return true
		}
	}

	switch action.Decision {
	case skillConfirmationDecisionConfirm:
		if rdb == nil {
			postTermsThreadMessage(ctx, api, ch, threadTS, "Redis is not configured — I cannot complete that confirmation from here.")
			return true
		}
		payload, err := loadEmailPendingPayloadJSON(ctx, rdb, ch, action.RequestUserID, threadTS)
		if err != nil {
			log.Printf("email_interactive: load payload err=%v", err)
			postTermsThreadMessage(ctx, api, ch, threadTS, "That queued email expired or could not be loaded. Start a fresh send request.")
			return true
		}
		cfg := LoadGmailOAuthConfigForEmployee(gmailEmployeeIDFromEnv())
		if err := cfg.Validate(); err != nil {
			postTermsThreadMessage(ctx, api, ch, threadTS, fmt.Sprintf("Gmail is not configured yet: %v", err))
			return true
		}
		if err := SendQueuedCreateEmail(ctx, cfg, payload); err != nil {
			log.Printf("email_interactive: send err=%v", err)
			postTermsThreadMessage(ctx, api, ch, threadTS, fmt.Sprintf("Send failed: %v", err))
			return true
		}
		_ = ClearEmailSkillPendingWithClient(ctx, rdb, ch, action.RequestUserID, threadTS)
		_ = clearCreateEmailDraftState(ctx, ch, action.RequestUserID, threadTS)
		finalizeSkillConfirmationButtonMessage(ctx, api, ch, messageTS, cb, skillConfirmationDecisionConfirm, clickUser)
		n := len(payload.Recipients)
		label := "Email"
		if n != 1 {
			label = "Emails"
		}
		postTermsThreadMessage(ctx, api, ch, threadTS, fmt.Sprintf("Sent %d %s.", n, label))
		return true

	case skillConfirmationDecisionCancel:
		if rdb != nil {
			_ = ClearEmailSkillPendingWithClient(ctx, rdb, ch, action.RequestUserID, threadTS)
		}
		_ = clearCreateEmailDraftState(ctx, ch, action.RequestUserID, threadTS)
		finalizeSkillConfirmationButtonMessage(ctx, api, ch, messageTS, cb, skillConfirmationDecisionCancel, clickUser)
		postTermsThreadMessage(ctx, api, ch, threadTS, "Stopped. I canceled that queued email.")
		return true
	default:
		return false
	}
}

// MaybeHandleEmailConfirmationPlaintext handles typed confirm/cancel while an email preview is pending (parity with employee-factory).
func MaybeHandleEmailConfirmationPlaintext(ctx context.Context, api *slack.Client, channelID, requestUserID, threadTS, rawText string) bool {
	text := strings.TrimSpace(strings.ToLower(rawText))
	if !isWriteEmailConfirmText(text) && !isWriteEmailCancelText(text) {
		return false
	}
	channelID = strings.TrimSpace(channelID)
	requestUserID = strings.TrimSpace(requestUserID)
	threadTS = strings.TrimSpace(threadTS)
	if channelID == "" || requestUserID == "" || threadTS == "" {
		return false
	}
	rdb, cleanup := redisClientFromEnvOptional(ctx)
	defer cleanup()
	if rdb == nil {
		return false
	}
	_, active, err := EmailSkillPendingTTL(ctx, rdb, channelID, requestUserID, threadTS)
	if err != nil || !active {
		return false
	}

	decision := skillConfirmationDecisionConfirm
	if isWriteEmailCancelText(text) {
		decision = skillConfirmationDecisionCancel
	}

	switch decision {
	case skillConfirmationDecisionCancel:
		_ = ClearEmailSkillPendingWithClient(ctx, rdb, channelID, requestUserID, threadTS)
		_ = clearCreateEmailDraftState(ctx, channelID, requestUserID, threadTS)
		if api != nil {
			postTermsThreadMessage(ctx, api, channelID, threadTS, "Stopped. I canceled that queued email.")
		}
		return true
	default:
		payload, err := loadEmailPendingPayloadJSON(ctx, rdb, channelID, requestUserID, threadTS)
		if err != nil {
			log.Printf("email_plaintext_confirm: load payload err=%v", err)
			if api != nil {
				postTermsThreadMessage(ctx, api, channelID, threadTS, "That queued email expired or could not be loaded.")
			}
			return true
		}
		cfg := LoadGmailOAuthConfigForEmployee(gmailEmployeeIDFromEnv())
		if err := cfg.Validate(); err != nil {
			if api != nil {
				postTermsThreadMessage(ctx, api, channelID, threadTS, fmt.Sprintf("Gmail is not configured: %v", err))
			}
			return true
		}
		if err := SendQueuedCreateEmail(ctx, cfg, payload); err != nil {
			log.Printf("email_plaintext_confirm: send err=%v", err)
			if api != nil {
				postTermsThreadMessage(ctx, api, channelID, threadTS, fmt.Sprintf("Send failed: %v", err))
			}
			return true
		}
		_ = ClearEmailSkillPendingWithClient(ctx, rdb, channelID, requestUserID, threadTS)
		_ = clearCreateEmailDraftState(ctx, channelID, requestUserID, threadTS)
		if api != nil {
			n := len(payload.Recipients)
			label := "Email"
			if n != 1 {
				label = "Emails"
			}
			postTermsThreadMessage(ctx, api, channelID, threadTS, fmt.Sprintf("Sent %d %s.", n, label))
		}
		return true
	}
}

func redisClientFromEnvOptional(ctx context.Context) (*redis.Client, func()) {
	_ = ctx
	url := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if url == "" {
		return nil, func() {}
	}
	c, err := redisOpen(url)
	if err != nil {
		log.Printf("email redis open err=%v", err)
		return nil, func() {}
	}
	return c, func() { _ = c.Close() }
}

func isWriteEmailConfirmText(v string) bool {
	text := strings.TrimSpace(strings.ToLower(v))
	switch text {
	case "confirm", "confirm send", "send now":
		return true
	default:
		return false
	}
}

func isWriteEmailCancelText(v string) bool {
	text := strings.TrimSpace(strings.ToLower(v))
	switch text {
	case "cancel", "cancel send", "stop":
		return true
	default:
		return false
	}
}

func gmailEmployeeIDFromEnv() string {
	e := strings.TrimSpace(os.Getenv("EMPLOYEE_ID"))
	if e != "" {
		return e
	}
	return "joanne"
}
