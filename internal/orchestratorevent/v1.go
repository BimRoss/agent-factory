package orchestratorevent

import "strings"

type EventV1 struct {
	SchemaVersion  string     `json:"schema_version"`
	TraceID        string     `json:"trace_id,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	TriggerSource  string     `json:"trigger_source,omitempty"`
	SlackEventID   string     `json:"slack_event_id,omitempty"`
	TargetEmployee string     `json:"target_employee"`
	Decision       DecisionV1 `json:"decision"`
	Message        MessageV1  `json:"message"`
}

type DecisionV1 struct {
	Trigger string `json:"trigger"`
	Kind    string `json:"kind"`
	ToolID  string `json:"tool_id,omitempty"`
	// Included for compatibility with orchestrator schema; not used yet.
	Employees []string `json:"employees,omitempty"`
}

type MessageV1 struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	MessageTS string `json:"message_ts"`
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
}

func (e EventV1) EffectiveTraceID() string {
	if s := strings.TrimSpace(e.TraceID); s != "" {
		return s
	}
	if s := strings.TrimSpace(e.RunID); s != "" {
		return s
	}
	if s := strings.TrimSpace(e.SlackEventID); s != "" {
		return s
	}
	return ""
}
