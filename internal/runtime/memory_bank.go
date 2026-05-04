package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const memoryBankContractVersion = "memory_bank.v1"

type MemoryBank struct {
	path string

	mu         sync.RWMutex
	modTimeUTC time.Time
	data       memoryBankDocument
}

type memoryBankDocument struct {
	ContractVersion string                          `json:"contractVersion"`
	Employees       map[string]memoryEmployeeRecord `json:"employees"`
	Channels        map[string]memoryChannelRecord  `json:"channels"`
	Threads         map[string]memoryThreadRecord   `json:"threads"`
}

type memoryEmployeeRecord struct {
	Intent               string   `json:"intent"`
	Expertise            []string `json:"expertise"`
	ChallengeStyle       string   `json:"challengeStyle"`
	DefaultResponseStyle string   `json:"defaultResponseStyle"`
	AssignedSkills       []string `json:"assignedSkills"`
}

type memoryChannelRecord struct {
	Summary     string   `json:"summary"`
	Decisions   []string `json:"decisions"`
	ActiveTasks []string `json:"activeTasks"`
	UpdatedAt   string   `json:"updatedAt"`
}

type memoryThreadRecord struct {
	Summary          string   `json:"summary"`
	RecentHighlights []string `json:"recentHighlights"`
	UpdatedAt        string   `json:"updatedAt"`
}

func LoadMemoryBank(path string) (*MemoryBank, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return &MemoryBank{
			path: "",
			data: memoryBankDocument{
				Employees: map[string]memoryEmployeeRecord{},
				Channels:  map[string]memoryChannelRecord{},
				Threads:   map[string]memoryThreadRecord{},
			},
		}, nil
	}
	bank := &MemoryBank{
		path: trimmed,
		data: memoryBankDocument{
			Employees: map[string]memoryEmployeeRecord{},
			Channels:  map[string]memoryChannelRecord{},
			Threads:   map[string]memoryThreadRecord{},
		},
	}
	if err := bank.reloadIfChanged(); err != nil {
		return nil, err
	}
	return bank, nil
}

func (m *MemoryBank) BuildPromptContext(task Task) string {
	if m == nil {
		return ""
	}
	if err := m.reloadIfChanged(); err != nil {
		return ""
	}

	employeeID := normalizeID(task.OwnerEmployeeID)
	channelID := strings.TrimSpace(task.ChannelID)
	threadTS := strings.TrimSpace(task.ThreadTS)
	threadKey := strings.TrimSpace(channelID + ":" + threadTS)

	m.mu.RLock()
	defer m.mu.RUnlock()

	var sections []string

	if emp, ok := m.data.Employees[employeeID]; ok {
		var employeeLines []string
		if v := strings.TrimSpace(emp.Intent); v != "" {
			employeeLines = append(employeeLines, "Intent: "+v)
		}
		if len(emp.Expertise) > 0 {
			employeeLines = append(employeeLines, "Expertise: "+strings.Join(emp.Expertise, ", "))
		}
		if v := strings.TrimSpace(emp.ChallengeStyle); v != "" {
			employeeLines = append(employeeLines, "Challenge style: "+v)
		}
		if v := strings.TrimSpace(emp.DefaultResponseStyle); v != "" {
			employeeLines = append(employeeLines, "Default response style: "+v)
		}
		if len(emp.AssignedSkills) > 0 {
			employeeLines = append(employeeLines, "Assigned skills: "+strings.Join(emp.AssignedSkills, ", "))
		}
		if len(employeeLines) > 0 {
			sections = append(sections, "Employee memory:\n- "+strings.Join(employeeLines, "\n- "))
		}
	}

	if ch, ok := m.data.Channels[channelID]; ok {
		var channelLines []string
		if v := strings.TrimSpace(ch.Summary); v != "" {
			channelLines = append(channelLines, "Summary: "+v)
		}
		if len(ch.Decisions) > 0 {
			channelLines = append(channelLines, "Decisions: "+strings.Join(ch.Decisions, " | "))
		}
		if len(ch.ActiveTasks) > 0 {
			channelLines = append(channelLines, "Active tasks: "+strings.Join(ch.ActiveTasks, " | "))
		}
		if v := strings.TrimSpace(ch.UpdatedAt); v != "" {
			channelLines = append(channelLines, "Updated at: "+v)
		}
		if len(channelLines) > 0 {
			sections = append(sections, "Channel memory:\n- "+strings.Join(channelLines, "\n- "))
		}
	}

	if th, ok := m.data.Threads[threadKey]; ok {
		var threadLines []string
		if v := strings.TrimSpace(th.Summary); v != "" {
			threadLines = append(threadLines, "Summary: "+v)
		}
		if len(th.RecentHighlights) > 0 {
			threadLines = append(threadLines, "Recent highlights: "+strings.Join(th.RecentHighlights, " | "))
		}
		if v := strings.TrimSpace(th.UpdatedAt); v != "" {
			threadLines = append(threadLines, "Updated at: "+v)
		}
		if len(threadLines) > 0 {
			sections = append(sections, "Thread memory:\n- "+strings.Join(threadLines, "\n- "))
		}
	}

	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func (m *MemoryBank) reloadIfChanged() error {
	if m == nil || strings.TrimSpace(m.path) == "" {
		return nil
	}
	info, err := os.Stat(m.path)
	if err != nil {
		return fmt.Errorf("stat memory bank file: %w", err)
	}
	mod := info.ModTime().UTC()

	m.mu.RLock()
	current := m.modTimeUTC
	m.mu.RUnlock()
	if !mod.After(current) {
		return nil
	}

	content, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("read memory bank file: %w", err)
	}
	var parsed memoryBankDocument
	if err := json.Unmarshal(content, &parsed); err != nil {
		return fmt.Errorf("parse memory bank file: %w", err)
	}
	if strings.TrimSpace(parsed.ContractVersion) != memoryBankContractVersion {
		return fmt.Errorf("memory bank contractVersion must be %s", memoryBankContractVersion)
	}
	if parsed.Employees == nil {
		parsed.Employees = map[string]memoryEmployeeRecord{}
	}
	if parsed.Channels == nil {
		parsed.Channels = map[string]memoryChannelRecord{}
	}
	if parsed.Threads == nil {
		parsed.Threads = map[string]memoryThreadRecord{}
	}

	m.mu.Lock()
	m.data = parsed
	m.modTimeUTC = mod
	m.mu.Unlock()
	return nil
}
