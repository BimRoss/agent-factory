package runtime

import (
	"fmt"
	"strings"
)

type CapabilityExecutionResult struct {
	ProgressUpdates []string
	FinalPayload    RenderPayload
}

type CapabilityAdapter interface {
	CapabilityID() string
	BuildPlan(task Task) CapabilityExecutionResult
}

func defaultAdapters() map[string]CapabilityAdapter {
	adapters := map[string]CapabilityAdapter{}
	for _, adapter := range []CapabilityAdapter{
		createIssueAdapter{},
		createCompanyAdapter{},
		readWebAdapter{},
	} {
		adapters[normalizeID(adapter.CapabilityID())] = adapter
	}
	return adapters
}

func capabilityExecutionPlan(task Task, capabilityID string, toolSpecs map[string]ToolSpec) CapabilityExecutionResult {
	capabilityID = normalizeID(capabilityID)
	spec, hasSpec := toolSpecs[capabilityID]
	if adapter, ok := defaultAdapters()[capabilityID]; ok {
		plan := adapter.BuildPlan(task)
		return applyToolSpecDefaults(task, capabilityID, plan, spec, hasSpec)
	}
	plan := CapabilityExecutionResult{
		ProgressUpdates: []string{
			fmt.Sprintf("Running %s...", capabilityID),
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-%s", task.ID, capabilityID),
			FallbackText: fmt.Sprintf("Completed %s via %s.", capabilityID, task.OwnerEmployeeID),
			FinalSummary: fmt.Sprintf("Capability %s completed", capabilityID),
			Transport:    "slack",
		},
	}
	return applyToolSpecDefaults(task, capabilityID, plan, spec, hasSpec)
}

func applyToolSpecDefaults(task Task, capabilityID string, plan CapabilityExecutionResult, spec ToolSpec, hasSpec bool) CapabilityExecutionResult {
	if !hasSpec {
		return plan
	}
	if strings.TrimSpace(plan.FinalPayload.FallbackText) == "" && strings.TrimSpace(spec.Description) != "" {
		plan.FinalPayload.FallbackText = strings.TrimSpace(spec.Description)
	}
	if strings.TrimSpace(plan.FinalPayload.FinalSummary) == "" && strings.TrimSpace(spec.Description) != "" {
		plan.FinalPayload.FinalSummary = strings.TrimSpace(spec.Description)
	}
	if strings.TrimSpace(plan.FinalPayload.OutputID) == "" {
		plan.FinalPayload.OutputID = fmt.Sprintf("%s-%s", task.ID, capabilityID)
	}
	if strings.TrimSpace(plan.FinalPayload.Transport) == "" {
		plan.FinalPayload.Transport = "slack"
	}
	return plan
}
