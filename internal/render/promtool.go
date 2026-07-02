package render

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckRules validates rendered rule files with promtool when it is on
// PATH, and is a no-op otherwise. The renderer already produces valid
// YAML; this pass catches bad PromQL, which matters most for
// AI-generated alerts.
func CheckRules(files map[string][]byte) error {
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		return nil
	}

	var paths []string
	dir := ""
	for _, path := range Paths(files) {
		if !strings.HasPrefix(path, "prometheus/rules/") {
			continue
		}
		if dir == "" {
			dir, err = os.MkdirTemp("", "monup-rules")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
		}
		full := filepath.Join(dir, filepath.Base(path))
		if err := os.WriteFile(full, files[path], 0o600); err != nil {
			return err
		}
		paths = append(paths, full)
	}
	if len(paths) == 0 {
		return nil
	}

	out, err := exec.Command(promtool, append([]string{"check", "rules"}, paths...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("promtool check rules failed: %v\n%s", err, out)
	}
	return nil
}
