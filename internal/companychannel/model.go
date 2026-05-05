// Package companychannel holds the JSON shape stored in the company-channels Redis HASH.
// Duplicated minimally from legacy employee-factory for channel-knowledge-refresh (no full worker config).
package companychannel

import "strings"

// CompanyChannelRuntime defines the runtime contract for one Slack channel/company row in Redis.
type CompanyChannelRuntime struct {
	CompanySlug        string   `json:"company_slug"`
	ChannelID          string   `json:"channel_id"`
	DisplayName        string   `json:"display_name,omitempty"`
	OwnerIDs           []string `json:"owner_ids,omitempty"`
	PrimaryOwner       string   `json:"primary_owner,omitempty"`
	AllowedOperatorIDs []string `json:"allowed_operator_ids,omitempty"`
	ThreadsEnabled     bool     `json:"threads_enabled"`
	GeneralAutoReactionEnabled bool `json:"general_auto_reaction_enabled"`
	GeneralResponsesMuted      bool `json:"general_responses_muted,omitempty"`
	OutOfOfficeEnabled         bool `json:"out_of_office_enabled"`
}

func effectiveOwnerIDs(e CompanyChannelRuntime) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, id := range e.OwnerIDs {
		add(id)
	}
	if len(out) > 0 {
		return out
	}
	for _, id := range e.AllowedOperatorIDs {
		add(id)
	}
	if len(out) > 0 {
		return out
	}
	if po := strings.TrimSpace(e.PrimaryOwner); po != "" {
		return []string{po}
	}
	return nil
}

// NormalizeCompanyChannelRuntime trims fields, merges legacy owner fields into OwnerIDs, and clears legacy keys.
func NormalizeCompanyChannelRuntime(e CompanyChannelRuntime) CompanyChannelRuntime {
	e.ChannelID = strings.TrimSpace(e.ChannelID)
	e.CompanySlug = strings.TrimSpace(e.CompanySlug)
	e.DisplayName = strings.TrimSpace(e.DisplayName)
	e.OwnerIDs = effectiveOwnerIDs(e)
	e.PrimaryOwner = ""
	e.AllowedOperatorIDs = nil
	e.ThreadsEnabled = true
	return e
}
