package orchestratorevent

import "strings"

const (
	SchemaVersion         = "3"
	SchemaVersionPipeline = "4"
	ExecutionModePipeline = "pipeline"
)

type EventV1 struct {
	SchemaVersion  string     `json:"schema_version"`
	TraceID        string     `json:"trace_id,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	TriggerSource  string     `json:"trigger_source,omitempty"`
	SlackEventID   string     `json:"slack_event_id,omitempty"`
	TargetEmployee string     `json:"target_employee"`
	Decision       DecisionV1 `json:"decision"`
	Message        MessageV1  `json:"message"`
	// Continuation is set when another worker forwarded work via Redis-backed handoff (see handoffremote).
	Continuation *ContinuationV1 `json:"continuation,omitempty"`
}

// ContinuationV1 references durable state written before publishing to slack.work.<employee>.events.
type ContinuationV1 struct {
	HandoffID string `json:"handoff_id"`
}

type DecisionV1 struct {
	Trigger   string   `json:"trigger"`
	Kind      string   `json:"kind"`
	ToolID    string   `json:"tool_id,omitempty"`
	Employees []string `json:"employees,omitempty"`

	DispatchMode    string `json:"dispatch_mode,omitempty"`
	PrimaryEmployee string `json:"primary_employee,omitempty"`

	ExecutionMode     string         `json:"execution_mode,omitempty"`
	PipelineSteps     []PipelineStep `json:"pipeline_steps,omitempty"`
	PipelineStepIndex int            `json:"pipeline_step_index,omitempty"`
	ChainID           string         `json:"chain_id,omitempty"`
}

type PipelineStep struct {
	TargetEmployee string `json:"target_employee"`
	StepText       string `json:"step_text"`
	Kind           string `json:"kind"`
	ToolID         string `json:"tool_id,omitempty"`
}

type MessageV1 struct {
	ChannelID          string   `json:"channel_id"`
	ThreadTS           string   `json:"thread_ts"`
	MessageTS          string   `json:"message_ts"`
	UserID             string   `json:"user_id"`
	Text               string   `json:"text"`
	SlackImageFileIDs  []string `json:"slack_image_file_ids,omitempty"`
	PipelineAnchorText string   `json:"pipeline_anchor_text,omitempty"`
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

// EnsureRunAndTraceIDs keeps trace/run ids aligned for pipeline events.
func EnsureRunAndTraceIDs(e *EventV1) {
	if e == nil {
		return
	}
	if strings.TrimSpace(e.TraceID) == "" {
		if r := strings.TrimSpace(e.RunID); r != "" {
			e.TraceID = r
		}
	}
}
