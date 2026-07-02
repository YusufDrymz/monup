package render

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakePromtool(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "promtool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

var ruleFiles = map[string][]byte{
	"prometheus/rules/postgres.yml": []byte("groups: []\n"),
	"docker-compose.yml":            []byte("services: {}\n"), // must be ignored
}

func TestCheckRulesNoPromtool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := CheckRules(ruleFiles); err != nil {
		t.Errorf("CheckRules without promtool = %v, want nil", err)
	}
}

func TestCheckRulesPass(t *testing.T) {
	fakePromtool(t, "#!/bin/sh\nexit 0\n")
	if err := CheckRules(ruleFiles); err != nil {
		t.Errorf("CheckRules = %v, want nil", err)
	}
}

func TestCheckRulesFail(t *testing.T) {
	fakePromtool(t, "#!/bin/sh\necho 'FAILED: bad expr' >&2\nexit 1\n")
	err := CheckRules(ruleFiles)
	if err == nil {
		t.Fatal("CheckRules = nil, want error")
	}
	if !strings.Contains(err.Error(), "promtool") || !strings.Contains(err.Error(), "bad expr") {
		t.Errorf("error missing promtool output: %v", err)
	}
}

func TestCheckRulesNoRuleFiles(t *testing.T) {
	fakePromtool(t, "#!/bin/sh\nexit 1\n") // would fail if invoked
	if err := CheckRules(map[string][]byte{"docker-compose.yml": []byte("x")}); err != nil {
		t.Errorf("CheckRules with no rule files = %v, want nil", err)
	}
}
