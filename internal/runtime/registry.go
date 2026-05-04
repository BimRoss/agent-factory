package runtime

import "strings"

type Employee struct {
	ID            string
	PackagedSkill map[string]struct{}
}

type Registry struct {
	Employees            map[string]Employee
	BuiltInCapabilities  map[string]struct{}
	PackagedSkillCatalog map[string]struct{}
	order                []string
}

func DefaultRegistry() Registry {
	employees := map[string]Employee{
		"alex": {
			ID:            "alex",
			PackagedSkill: setFromSlice(nil),
		},
		"tim": {
			ID:            "tim",
			PackagedSkill: setFromSlice(nil),
		},
		"ross": {
			ID:            "ross",
			PackagedSkill: setFromSlice([]string{"create-issue", "read-issue", "read-github", "read-backend", "update-issue"}),
		},
		"garth": {
			ID:            "garth",
			PackagedSkill: setFromSlice([]string{"read-twitter", "read-trends"}),
		},
		"joanne": {
			ID: "joanne",
			PackagedSkill: setFromSlice([]string{
				"create-email",
				"create-doc",
				"create-company",
				"delete-company",
				"read-user",
				"update-terms",
				"create-connect",
				"update-company",
			}),
		},
		"anna": {
			ID:            "anna",
			PackagedSkill: setFromSlice([]string{"create-image"}),
		},
	}

	catalog := setFromSlice([]string{
		"create-email",
		"create-doc",
		"create-company",
		"delete-company",
		"read-company",
		"read-user",
		"read-twitter",
		"read-trends",
		"update-terms",
		"create-image",
		"create-connect",
		"create-issue",
		"read-issue",
		"read-github",
		"read-backend",
		"update-issue",
		"update-company",
	})

	return Registry{
		Employees:            employees,
		BuiltInCapabilities:  setFromSlice([]string{"read-web", "read-skills"}),
		PackagedSkillCatalog: catalog,
		order:                []string{"alex", "tim", "ross", "garth", "joanne", "anna"},
	}
}

func (r Registry) EmployeeHasCapability(employeeID, capabilityID string) bool {
	employeeID = normalizeID(employeeID)
	capabilityID = normalizeID(capabilityID)
	if employeeID == "" || capabilityID == "" {
		return false
	}
	if _, ok := r.BuiltInCapabilities[capabilityID]; ok {
		return true
	}
	emp, ok := r.Employees[employeeID]
	if !ok {
		return false
	}
	_, ok = emp.PackagedSkill[capabilityID]
	return ok
}

func (r Registry) IsPackagedSkill(skillID string) bool {
	_, ok := r.PackagedSkillCatalog[normalizeID(skillID)]
	return ok
}

func (r Registry) FindEmployeeForCapability(capabilityID, excludeEmployeeID string) (string, bool) {
	capabilityID = normalizeID(capabilityID)
	excludeEmployeeID = normalizeID(excludeEmployeeID)
	if capabilityID == "" {
		return "", false
	}
	if _, ok := r.BuiltInCapabilities[capabilityID]; ok {
		return excludeEmployeeID, excludeEmployeeID != ""
	}
	for _, employeeID := range r.order {
		if employeeID == excludeEmployeeID {
			continue
		}
		if r.EmployeeHasCapability(employeeID, capabilityID) {
			return employeeID, true
		}
	}
	return "", false
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func setFromSlice(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		id := normalizeID(value)
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}
