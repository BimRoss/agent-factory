package adminapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type catalogService struct {
	sharedContractsDir string
	toolSpecsDir       string
}

type capabilityCatalog struct {
	CoreEmployees    []catalogEmployee   `json:"coreEmployees"`
	Skills           []catalogSkill      `json:"skills"`
	EmployeeSkillIDs map[string][]string `json:"employeeSkillIds"`
	UpdatedAt        string              `json:"updatedAt,omitempty"`
	Source           string              `json:"source,omitempty"`
}

type catalogEmployee struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type catalogSkill struct {
	ID             string            `json:"id"`
	Label          string            `json:"label"`
	Description    string            `json:"description"`
	RuntimeTool    string            `json:"runtimeTool"`
	RequiredParams []string          `json:"requiredParams"`
	OptionalParams []string          `json:"optionalParams"`
	ParamDefaults  map[string]string `json:"paramDefaults,omitempty"`
}

type employeeInstancesDoc struct {
	CoreEmployees []struct {
		EmployeeID     string   `json:"employeeId"`
		Label          string   `json:"label"`
		PackagedSkills []string `json:"packagedSkills"`
	} `json:"coreEmployees"`
	RuntimeBuiltInCapabilities []string `json:"runtimeBuiltInCapabilities"`
}

type skillInstancesDoc struct {
	PackagedSkills []string `json:"packagedSkills"`
}

type toolSpec struct {
	ToolID      string `json:"toolId"`
	RuntimeTool string `json:"runtimeTool"`
	Description string `json:"description"`
	InputSchema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"inputSchema"`
}

func (c *catalogService) buildCatalog() (capabilityCatalog, error) {
	employeeDoc, err := c.readEmployeeInstances()
	if err != nil {
		return capabilityCatalog{}, err
	}
	skillDoc, err := c.readSkillInstances()
	if err != nil {
		return capabilityCatalog{}, err
	}
	specs, err := c.readToolSpecs()
	if err != nil {
		return capabilityCatalog{}, err
	}

	out := capabilityCatalog{
		CoreEmployees:    make([]catalogEmployee, 0, len(employeeDoc.CoreEmployees)),
		Skills:           make([]catalogSkill, 0),
		EmployeeSkillIDs: map[string][]string{},
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
		Source:           "agent-factory",
	}

	skillOwners := map[string][]string{}
	skillSet := map[string]struct{}{}
	for _, row := range employeeDoc.CoreEmployees {
		employeeID := normalizeID(row.EmployeeID)
		if employeeID == "" {
			continue
		}
		label := strings.TrimSpace(row.Label)
		if label == "" {
			label = strings.Title(employeeID)
		}
		out.CoreEmployees = append(out.CoreEmployees, catalogEmployee{
			ID:          employeeID,
			Label:       label,
			Description: label + " runtime in agent-factory",
		})
		out.EmployeeSkillIDs[employeeID] = []string{}
		for _, skillID := range row.PackagedSkills {
			skillID = normalizeID(skillID)
			if skillID == "" {
				continue
			}
			skillSet[skillID] = struct{}{}
			out.EmployeeSkillIDs[employeeID] = append(out.EmployeeSkillIDs[employeeID], skillID)
			skillOwners[skillID] = appendIfMissing(skillOwners[skillID], employeeID)
		}
		for _, builtIn := range employeeDoc.RuntimeBuiltInCapabilities {
			builtIn = normalizeID(builtIn)
			if builtIn == "" {
				continue
			}
			skillSet[builtIn] = struct{}{}
			out.EmployeeSkillIDs[employeeID] = appendIfMissing(out.EmployeeSkillIDs[employeeID], builtIn)
			skillOwners[builtIn] = appendIfMissing(skillOwners[builtIn], employeeID)
		}
		sort.Strings(out.EmployeeSkillIDs[employeeID])
	}

	for _, skillID := range skillDoc.PackagedSkills {
		skillID = normalizeID(skillID)
		if skillID != "" {
			skillSet[skillID] = struct{}{}
		}
	}

	skillIDs := make([]string, 0, len(skillSet))
	for skillID := range skillSet {
		skillIDs = append(skillIDs, skillID)
	}
	sort.Strings(skillIDs)
	for _, skillID := range skillIDs {
		spec := specs[skillID]
		required := uniqueParams(spec.InputSchema.Required)
		optional := make([]string, 0, len(spec.InputSchema.Properties))
		for name := range spec.InputSchema.Properties {
			name = strings.TrimSpace(name)
			if name == "" || contains(required, name) {
				continue
			}
			optional = append(optional, name)
		}
		sort.Strings(optional)
		desc := strings.TrimSpace(spec.Description)
		if desc == "" {
			desc = fmt.Sprintf("%s capability in agent-factory runtime.", prettyLabel(skillID))
		}
		runtimeTool := strings.TrimSpace(spec.RuntimeTool)
		if runtimeTool == "" {
			owners := skillOwners[skillID]
			if len(owners) > 0 {
				runtimeTool = owners[0] + "-" + skillID
			}
		}
		out.Skills = append(out.Skills, catalogSkill{
			ID:             skillID,
			Label:          prettyLabel(skillID),
			Description:    desc,
			RuntimeTool:    runtimeTool,
			RequiredParams: required,
			OptionalParams: optional,
		})
	}
	return out, nil
}

func (c *catalogService) readEmployeeInstances() (employeeInstancesDoc, error) {
	path := filepath.Join(c.sharedContractsDir, "employees.instances.v1.json")
	var out employeeInstancesDoc
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func (c *catalogService) readSkillInstances() (skillInstancesDoc, error) {
	path := filepath.Join(c.sharedContractsDir, "skills.instances.v1.json")
	var out skillInstancesDoc
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

func (c *catalogService) readToolSpecs() (map[string]toolSpec, error) {
	pattern := filepath.Join(c.toolSpecsDir, "*.tool.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := map[string]toolSpec{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var spec toolSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		skillID := normalizeID(spec.ToolID)
		if skillID == "" {
			continue
		}
		out[skillID] = spec
	}
	return out, nil
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func prettyLabel(skillID string) string {
	parts := strings.Split(strings.TrimSpace(skillID), "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func uniqueParams(params []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(params))
	for _, p := range params {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, needle string) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

func appendIfMissing(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}
