package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReplayResults 从 trace 目录回放，不发真实模型请求（E7）。
func ReplayResults(traceDir string) ([]*RunResult, error) {
	entries, err := os.ReadDir(traceDir)
	if err != nil {
		return nil, fmt.Errorf("eval: 读 trace 目录: %w", err)
	}
	out := make([]*RunResult, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(traceDir, entry.Name()))
		if err != nil {
			continue
		}
		var run RunResult
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		run.Replay = true
		out = append(out, &run)
	}
	return out, nil
}
