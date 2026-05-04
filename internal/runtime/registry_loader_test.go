package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistryFromContractFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	employeePath := filepath.Join(dir, "employees.instances.v1.json")
	skillPath := filepath.Join(dir, "skills.instances.v1.json")

	employeeJSON := `{
  "contractVersion": "employee.instances.v1",
  "coreEmployees": [
    {"employeeId":"joanne","label":"Joanne","packagedSkills":["create-company"]},
    {"employeeId":"ross","label":"Ross","packagedSkills":["create-issue"]}
  ],
  "runtimeBuiltInCapabilities": ["read-web","read-skills"]
}`
	skillJSON := `{
  "contractVersion": "skill.instances.v1",
  "packagedSkills": ["create-company","create-issue"],
  "runtimeBuiltInCapabilities": ["read-web","read-skills"]
}`

	if err := os.WriteFile(employeePath, []byte(employeeJSON), 0o644); err != nil {
		t.Fatalf("write employees: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte(skillJSON), 0o644); err != nil {
		t.Fatalf("write skills: %v", err)
	}

	registry, err := LoadRegistryFromContractFiles(employeePath, skillPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	if !registry.EmployeeHasCapability("joanne", "create-company") {
		t.Fatalf("expected joanne to have create-company")
	}
	if !registry.EmployeeHasCapability("ross", "create-issue") {
		t.Fatalf("expected ross to have create-issue")
	}
	if !registry.EmployeeHasCapability("joanne", "read-web") {
		t.Fatalf("expected read-web built-in capability")
	}
}
