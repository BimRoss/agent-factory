package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	employeeInstancesContractVersion = "employee.instances.v1"
	skillInstancesContractVersion    = "skill.instances.v1"
)

type employeeInstancesDocument struct {
	ContractVersion string `json:"contractVersion"`
	CoreEmployees   []struct {
		EmployeeID     string   `json:"employeeId"`
		Label          string   `json:"label"`
		PackagedSkills []string `json:"packagedSkills"`
	} `json:"coreEmployees"`
	RuntimeBuiltInCapabilities []string `json:"runtimeBuiltInCapabilities"`
}

type skillInstancesDocument struct {
	ContractVersion            string   `json:"contractVersion"`
	PackagedSkills             []string `json:"packagedSkills"`
	RuntimeBuiltInCapabilities []string `json:"runtimeBuiltInCapabilities"`
}

func LoadRegistryFromContractFiles(employeeInstancesPath, skillInstancesPath string) (Registry, error) {
	employeeContent, err := os.ReadFile(strings.TrimSpace(employeeInstancesPath))
	if err != nil {
		return Registry{}, fmt.Errorf("read employee instances: %w", err)
	}
	skillContent, err := os.ReadFile(strings.TrimSpace(skillInstancesPath))
	if err != nil {
		return Registry{}, fmt.Errorf("read skill instances: %w", err)
	}

	var employeeDoc employeeInstancesDocument
	if err := json.Unmarshal(employeeContent, &employeeDoc); err != nil {
		return Registry{}, fmt.Errorf("parse employee instances: %w", err)
	}
	if strings.TrimSpace(employeeDoc.ContractVersion) != employeeInstancesContractVersion {
		return Registry{}, fmt.Errorf("employee instances contractVersion must be %s", employeeInstancesContractVersion)
	}

	var skillDoc skillInstancesDocument
	if err := json.Unmarshal(skillContent, &skillDoc); err != nil {
		return Registry{}, fmt.Errorf("parse skill instances: %w", err)
	}
	if strings.TrimSpace(skillDoc.ContractVersion) != skillInstancesContractVersion {
		return Registry{}, fmt.Errorf("skill instances contractVersion must be %s", skillInstancesContractVersion)
	}

	packagedCatalog := setFromSlice(skillDoc.PackagedSkills)
	if len(packagedCatalog) == 0 {
		return Registry{}, fmt.Errorf("skill instances must define at least one packaged skill")
	}

	employees := map[string]Employee{}
	order := make([]string, 0, len(employeeDoc.CoreEmployees))
	for _, row := range employeeDoc.CoreEmployees {
		employeeID := normalizeID(row.EmployeeID)
		if employeeID == "" {
			return Registry{}, fmt.Errorf("employee row has empty employeeId")
		}
		packagedSkills := setFromSlice(row.PackagedSkills)
		for skillID := range packagedSkills {
			if _, ok := packagedCatalog[skillID]; !ok {
				return Registry{}, fmt.Errorf("employee %s references unknown packaged skill %s", employeeID, skillID)
			}
		}
		employees[employeeID] = Employee{
			ID:            employeeID,
			PackagedSkill: packagedSkills,
		}
		order = append(order, employeeID)
	}

	builtInFromEmployees := setFromSlice(employeeDoc.RuntimeBuiltInCapabilities)
	builtInFromSkills := setFromSlice(skillDoc.RuntimeBuiltInCapabilities)
	if !sameSet(builtInFromEmployees, builtInFromSkills) {
		return Registry{}, fmt.Errorf("runtime built-in capabilities mismatch between employee and skill instances")
	}

	return Registry{
		Employees:            employees,
		BuiltInCapabilities:  builtInFromEmployees,
		PackagedSkillCatalog: packagedCatalog,
		order:                order,
	}, nil
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}
