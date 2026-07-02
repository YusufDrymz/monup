package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const containersFixture = `[
  {
    "Names": ["/shop-db"],
    "Image": "postgres:16",
    "State": "running",
    "Ports": [{"PrivatePort": 5432, "Type": "tcp"}],
    "Labels": {},
    "NetworkSettings": {"Networks": {"shop_default": {}}}
  },
  {
    "Names": ["/shop-api"],
    "Image": "shop/api:1.0",
    "State": "running",
    "Ports": [{"PrivatePort": 8080, "Type": "tcp"}],
    "Labels": {},
    "NetworkSettings": {"Networks": {"shop_default": {}}}
  }
]`

func startFakeDocker(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "monup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(containersFixture))
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestPlanCommand(t *testing.T) {
	sock := startFakeDocker(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"plan", "--docker-socket", sock, "--no-host-scan"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan exited %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"postgres", "shop-db",
		"no catalog match", "shop-api",
		"docker-compose.yml",
		"prometheus/prometheus.yml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestApplyWritesFiles(t *testing.T) {
	sock := startFakeDocker(t)
	out := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := run([]string{"apply", "--docker-socket", sock, "--no-host-scan", "--out", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply exited %d, stderr: %s", code, stderr.String())
	}
	for _, f := range []string{
		"docker-compose.yml",
		".env",
		".env.example",
		"prometheus/prometheus.yml",
		"prometheus/rules/postgres.yml",
		"grafana/dashboards/postgres.json",
		"grafana/provisioning/datasources/monup.yml",
	} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}

	// Second apply must be idempotent: everything unchanged.
	stdout.Reset()
	code = run([]string{"apply", "--docker-socket", sock, "--no-host-scan", "--out", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second apply exited %d", code)
	}
	if strings.Contains(stdout.String(), "updated") || strings.Contains(stdout.String(), "created") {
		t.Errorf("second apply not idempotent:\n%s", stdout.String())
	}
}

func TestCatalogCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"catalog"}, &stdout, &stderr); code != 0 {
		t.Fatalf("catalog exited %d", code)
	}
	for _, want := range []string{"postgres", "redis", "mysql", "nginx", "node", "rabbitmq"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("catalog output missing %q", want)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}
}
