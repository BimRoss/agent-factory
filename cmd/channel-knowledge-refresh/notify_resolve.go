package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bimross/agent-factory/internal/companychannel"
	"github.com/slack-go/slack"
)

// slackHashChannelName returns "#" + the canonical Slack channel name from conversations.info.
func slackHashChannelName(ctx context.Context, api *slack.Client, channelID string) string {
	ch, err := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
		ChannelID: strings.TrimSpace(channelID),
	})
	if err != nil || strings.TrimSpace(ch.Name) == "" {
		return ""
	}
	return "#" + strings.TrimSpace(ch.Name)
}

// registryNotifyLabel derives a "#channel" label from Redis company-channel metadata when Slack API is unavailable.
func registryNotifyLabel(meta map[string]companychannel.CompanyChannelRuntime, channelID string) string {
	if len(meta) == 0 {
		return ""
	}
	e, ok := meta[strings.TrimSpace(channelID)]
	if !ok {
		return ""
	}
	dn := strings.TrimSpace(e.DisplayName)
	if strings.HasPrefix(dn, "#") && dn != "" {
		return dn
	}
	if s := strings.TrimSpace(e.CompanySlug); s != "" {
		return "#" + s
	}
	if dn != "" {
		return "#" + strings.ToLower(strings.TrimPrefix(dn, "#"))
	}
	return ""
}

func notifyLabelForChannel(ctx context.Context, api *slack.Client, channelID string, meta map[string]companychannel.CompanyChannelRuntime) string {
	if label := slackHashChannelName(ctx, api, channelID); label != "" {
		return label
	}
	return registryNotifyLabel(meta, channelID)
}

const knowledgeFailureMsgMaxRunes = 400

func formatKnowledgeRefreshFailureMessage(displayName string, refreshErr error, messageOverride string) string {
	msg := strings.TrimSpace(messageOverride)
	if msg != "" {
		return msg
	}
	label := strings.TrimSpace(displayName)
	if label == "" {
		label = "this channel"
	}
	errStr := refreshErr.Error()
	if knowledgeFailureMsgMaxRunes > 0 && len([]rune(errStr)) > knowledgeFailureMsgMaxRunes {
		r := []rune(errStr)
		errStr = string(r[:knowledgeFailureMsgMaxRunes]) + "…"
	}
	return fmt.Sprintf("❗ Channel knowledge refresh failed for %s: %s", label, errStr)
}

func postKnowledgeRefreshFailure(ctx context.Context, api *slack.Client, channelID, displayName string, refreshErr error, messageOverride string) error {
	msg := formatKnowledgeRefreshFailureMessage(displayName, refreshErr, messageOverride)
	_, _, err := api.PostMessageContext(ctx, channelID, slack.MsgOptionText(msg, false))
	return err
}

// resolveChannelIDs picks which Slack channels to scrape:
//   - If CHANNEL_KNOWLEDGE_CHANNEL_IDS is set (comma-separated), use that explicit list.
//   - Else Slack users.conversations: every public + private channel the token’s bot user is in.
func resolveChannelIDs(ctx context.Context, api *slack.Client) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("CHANNEL_KNOWLEDGE_CHANNEL_IDS"))
	if raw != "" {
		var out []string
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("CHANNEL_KNOWLEDGE_CHANNEL_IDS is empty")
		}
		return out, nil
	}
	return listMemberChannelsSlack(ctx, api)
}

// listMemberChannelsSlack lists public + private channels the bot user is in (users.conversations).
func listMemberChannelsSlack(ctx context.Context, api *slack.Client) ([]string, error) {
	var out []string
	seen := make(map[string]struct{})
	cursor := ""
	for {
		ch, next, err := api.GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
			Cursor:          cursor,
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			ExcludeArchived: true,
		})
		if err != nil {
			return nil, fmt.Errorf("users.conversations: %w", err)
		}
		for _, c := range ch {
			id := strings.TrimSpace(c.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if strings.TrimSpace(next) == "" {
			break
		}
		cursor = next
	}
	if len(out) == 0 {
		log.Printf("channel_knowledge: users.conversations returned no public/private channels — orchestrator bot may not be in any channels yet")
	} else {
		log.Printf("channel_knowledge: discovered %d channel(s) via users.conversations (public + private)", len(out))
	}
	return out, nil
}
