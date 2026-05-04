package handoffremote

import "github.com/bimross/agent-factory/internal/orchestratorevent"

const RecordSchemaVersion = "1"

// Record is stored at agentfactory:handoff:<id> before publishing the continuation NATS message.
type Record struct {
	SchemaVersion string `json:"schema_version"`

	HandoffID    string `json:"handoff_id"`
	FromEmployee string `json:"from_employee"`
	ToEmployee   string `json:"to_employee"`
	CapabilityID string `json:"capability_id"`

	TraceID       string `json:"trace_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	SlackEventID  string `json:"slack_event_id,omitempty"`
	TriggerSource string `json:"trigger_source,omitempty"`

	OriginatingTaskID string `json:"originating_task_id,omitempty"`

	Message  orchestratorevent.MessageV1  `json:"message"`
	Decision orchestratorevent.DecisionV1 `json:"decision"`

	EventSchemaVersion string `json:"event_schema_version,omitempty"`
}
