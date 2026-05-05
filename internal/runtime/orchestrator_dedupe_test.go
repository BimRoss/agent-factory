package runtime

import (
	"testing"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
)

func TestOrchestratorDedupeCanonical_pipelineStep(t *testing.T) {
	base := orchestratorevent.EventV1{
		RunID:          "run-a",
		TargetEmployee: "alex",
		InnerType:      "message",
		Decision: orchestratorevent.DecisionV1{
			Kind:              "tool",
			ToolID:            "read-web",
			PipelineStepIndex: 0,
		},
		Message: orchestratorevent.MessageV1{
			ChannelID: "C1",
			MessageTS: "1.0",
		},
	}
	a := orchestratorDedupeCanonical(base)
	base.Decision.PipelineStepIndex = 1
	b := orchestratorDedupeCanonical(base)
	if a == b {
		t.Fatalf("expected different canonical for different step: %q", a)
	}
}

func TestOrchestratorDedupeCanonical_handoff(t *testing.T) {
	ev := orchestratorevent.EventV1{
		Continuation: &orchestratorevent.ContinuationV1{HandoffID: "h42"},
	}
	got := orchestratorDedupeCanonical(ev)
	if got != "handoff:h42" {
		t.Fatalf("got %q", got)
	}
}
