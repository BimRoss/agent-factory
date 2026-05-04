package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ToolSpec struct {
	ToolID      string `json:"toolId"`
	ToolType    string `json:"toolType"`
	RuntimeTool string `json:"runtimeTool"`
	Description string `json:"description"`
}

func LoadToolSpecsFromDir(dir string) (map[string]ToolSpec, error) {
	root := strings.TrimSpace(dir)
	if root == "" {
		return map[string]ToolSpec{}, nil
	}

	pattern := filepath.Join(root, "*.tool.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob tool specs: %w", err)
	}

	out := map[string]ToolSpec{}
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read tool spec %s: %w", path, err)
		}
		var spec ToolSpec
		if err := json.Unmarshal(content, &spec); err != nil {
			return nil, fmt.Errorf("parse tool spec %s: %w", path, err)
		}
		spec.ToolID = normalizeID(spec.ToolID)
		if spec.ToolID == "" {
			return nil, fmt.Errorf("tool spec %s missing toolId", path)
		}
		out[spec.ToolID] = spec
	}
	return out, nil
}
