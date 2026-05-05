package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/bimross/agent-factory/internal/runtime"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// startJoanneInteractionSocket listens for Slack interactivity over Socket Mode so Block Kit terms
// buttons (employee-factory–compatible action ids) work for Joanne. Channel ingress stays NATS-backed;
// subscribed Events API messages are ACK-only so Slack keeps the socket healthy.
func startJoanneInteractionSocket(parent context.Context, employeeID string) {
	if normalizeID(employeeID) != "joanne" {
		return
	}
	appToken := joanneAppTokenFromEnv()
	if appToken == "" || !strings.HasPrefix(appToken, "xapp-") {
		log.Printf("joanne socket: JOANNE_SLACK_APP_TOKEN/SLACK_APP_TOKEN missing or not an app-level token — terms confirmation buttons need Socket Mode")
		return
	}
	botTok := joanneBotTokenFromEnv()
	if botTok == "" {
		log.Printf("joanne socket: JOANNE_SLACK_BOT_TOKEN/SLACK_BOT_TOKEN missing — cannot attach Socket Mode listener")
		return
	}

	api := slack.New(botTok, slack.OptionAppLevelToken(appToken))
	sm := socketmode.New(api)

	go joanneDrainSocketLoop(parent, sm, api)
	go func() {
		if err := sm.RunContext(parent); err != nil && parent.Err() == nil && !strings.Contains(strings.ToLower(err.Error()), "canceled") {
			log.Printf("joanne socket: RunContext ended: %v", err)
		}
	}()
	log.Printf("joanne socket: interactivity listener started")
}

func joanneBotTokenFromEnv() string {
	if tok := strings.TrimSpace(os.Getenv("JOANNE_SLACK_BOT_TOKEN")); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
}

func joanneAppTokenFromEnv() string {
	if tok := strings.TrimSpace(os.Getenv("JOANNE_SLACK_APP_TOKEN")); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("SLACK_APP_TOKEN"))
}

func joanneDrainSocketLoop(ctx context.Context, sm *socketmode.Client, api *slack.Client) {
	if sm == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sm.Events:
			if !ok {
				return
			}
			handleJoanneSocketEvent(ctx, sm, api, evt)
		}
	}
}

func handleJoanneSocketEvent(ctx context.Context, sm *socketmode.Client, api *slack.Client, evt socketmode.Event) {
	if sm == nil {
		return
	}
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		log.Printf("joanne socket: connecting")
	case socketmode.EventTypeConnected:
		log.Printf("joanne socket: connected")
	case socketmode.EventTypeHello:
		// Slack sends `hello` after the WebSocket opens. Do not Ack it — the official slack-go
		// example only logs; Ack-ing (especially with an empty envelope_id from the default
		// branch) corrupts the session, causes reconnect churn, and breaks interactive delivery.
		if evt.Request != nil {
			appID := strings.TrimSpace(evt.Request.ConnectionInfo.AppID)
			log.Printf("joanne socket: hello app_id=%s num_connections=%d host=%s",
				appID, evt.Request.NumConnections, strings.TrimSpace(evt.Request.DebugInfo.Host))
		} else {
			log.Printf("joanne socket: hello (no request metadata)")
		}
	case socketmode.EventTypeDisconnect:
		log.Printf("joanne socket: disconnect: %v", evt.Data)
	case socketmode.EventTypeConnectionError:
		log.Printf("joanne socket: connection error: %v", evt.Data)
	case socketmode.EventTypeInvalidAuth:
		log.Printf("joanne socket: invalid auth: %v", evt.Data)
	case socketmode.EventTypeIncomingError:
		log.Printf("joanne socket: incoming error: %v", evt.Data)
	case socketmode.EventTypeErrorWriteFailed:
		log.Printf("joanne socket: write error: %v", evt.Data)
	case socketmode.EventTypeErrorBadMessage:
		log.Printf("joanne socket: bad message: %v", evt.Data)
	case socketmode.EventTypeInteractive:
		var envelopeID string
		if evt.Request != nil {
			envelopeID = evt.Request.EnvelopeID
		}
		log.Printf("joanne socket: interactive received envelope_id=%s data_type=%T", envelopeID, evt.Data)
		// ACK first (Slack warns in the UI if the handshake is late or missing).
		if evt.Request == nil {
			log.Printf("joanne socket: interactive missing envelope (cannot Ack)")
			return
		}
		if err := sm.Ack(*evt.Request); err != nil {
			log.Printf("joanne socket: Ack failed envelope_id=%s err=%v — check Socket Mode pairing (JOANNE_SLACK_APP_TOKEN + SLACK_BOT_TOKEN), only one Socket client per Joanne app, and remove any stale Interactivity Request URL in api.slack.com for the Joanne app.", envelopeID, err)
			return
		}
		log.Printf("joanne socket: interactive Ack ok envelope_id=%s", envelopeID)
		interaction, coerceOK := interactionCallbackFromSocketData(evt.Data)
		if !coerceOK {
			log.Printf("joanne socket: interactive unrecognized Data type=%T envelope_id=%s", evt.Data, envelopeID)
			return
		}
		firstActionID := ""
		if len(interaction.ActionCallback.BlockActions) > 0 {
			firstActionID = strings.TrimSpace(interaction.ActionCallback.BlockActions[0].ActionID)
		}
		log.Printf("joanne socket: interactive decoded envelope_id=%s type=%s callback_id=%s action_count=%d first_action_id=%s",
			envelopeID,
			strings.TrimSpace(string(interaction.Type)),
			strings.TrimSpace(interaction.CallbackID),
			len(interaction.ActionCallback.BlockActions),
			firstActionID,
		)
		if interaction.Type != slack.InteractionTypeBlockActions {
			log.Printf("joanne socket: interactive type=%s (expected block_actions) envelope_id=%s", interaction.Type, envelopeID)
		}
		// Post-Ack side effects — run async so websocket response scheduling stays unobstructed.
		go func(ic slack.InteractionCallback) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("joanne socket: HandleTermsInteraction panic: %v", r)
				}
			}()
			handled := runtime.HandleTermsInteraction(context.Background(), api, ic)
			if !handled {
				handled = runtime.HandleEmailInteraction(context.Background(), api, ic)
			}
			if !handled && ic.Type != "" {
				log.Printf("joanne socket: ignored interactive payload type=%s callback_id=%s envelope_id=%s",
					ic.Type, ic.CallbackID, envelopeID)
			}
		}(interaction)
	case socketmode.EventTypeEventsAPI:
		if evt.Request != nil {
			sm.Ack(*evt.Request)
		}
	case socketmode.EventTypeSlashCommand:
		if evt.Request != nil {
			if err := sm.Ack(*evt.Request); err != nil {
				log.Printf("joanne socket: slash command Ack failed err=%v", err)
			}
		}
	default:
		// Only acknowledge envelopes that include an envelope_id (events_api, interactive, slash_commands).
		// Never Ack connection lifecycle messages (e.g. hello) or anything without an id.
		if evt.Request == nil {
			return
		}
		eid := strings.TrimSpace(evt.Request.EnvelopeID)
		if eid == "" {
			log.Printf("joanne socket: unhandled socket event type=%s (no envelope_id; not acking)", evt.Type)
			return
		}
		if err := sm.Ack(*evt.Request); err != nil {
			log.Printf("joanne socket: Ack failed for unhandled type=%s envelope_id=%s err=%v", evt.Type, eid, err)
		}
	}
}

func interactionCallbackFromSocketData(data interface{}) (slack.InteractionCallback, bool) {
	switch v := data.(type) {
	case slack.InteractionCallback:
		return v, true
	case *slack.InteractionCallback:
		if v == nil {
			return slack.InteractionCallback{}, false
		}
		return *v, true
	default:
		return slack.InteractionCallback{}, false
	}
}
