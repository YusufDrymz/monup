package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const twoContainersFixture = `[
  {
    "Names": ["/shop-db"],
    "Image": "postgres:16",
    "State": "running",
    "Ports": [{"PrivatePort": 5432, "Type": "tcp"}],
    "Labels": {},
    "NetworkSettings": {"Networks": {"shop_default": {}}}
  },
  {
    "Names": ["/shop-cache"],
    "Image": "redis:7",
    "State": "running",
    "Ports": [{"PrivatePort": 6379, "Type": "tcp"}],
    "Labels": {},
    "NetworkSettings": {"Networks": {"shop_default": {}}}
  }
]`

// switchableDocker is a fake Docker API whose container list can be
// swapped mid-test, to simulate containers coming and going.
type switchableDocker struct {
	mu      sync.Mutex
	fixture string
}

func (d *switchableDocker) set(fixture string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fixture = fixture
}

func (d *switchableDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(d.fixture))
}

func startSwitchableDocker(t *testing.T, fixture string) (*switchableDocker, string) {
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
	fd := &switchableDocker{fixture: fixture}
	srv := &http.Server{Handler: fd}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return fd, sock
}

// syncBuffer guards a bytes.Buffer written by the watch goroutine and
// read by the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in output:\n%s", want, buf.String())
}

func TestWatchLoop(t *testing.T) {
	fd, sock := startSwitchableDocker(t, containersFixture) // postgres + unmatched api
	out := filepath.Join(t.TempDir(), "monup")
	var stdout, stderr syncBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cf := &commonFlags{dockerSocket: sock, noHostScan: true}
	done := make(chan int, 1)
	go func() {
		done <- watchLoop(ctx, cf, 20*time.Millisecond, true, out, &stdout, &stderr)
	}()

	waitFor(t, &stdout, "initial plan")
	waitFor(t, &stdout, "shop-db")
	waitFor(t, &stdout, "docker-compose.yml") // initial auto-apply

	fd.set(twoContainersFixture) // shop-api gone, redis appears
	waitFor(t, &stdout, "plan changed")
	waitFor(t, &stdout, "+ redis")
	waitFor(t, &stdout, "- unknown")
	waitFor(t, &stdout, "prometheus/rules/redis.yml")

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("watchLoop exited %d, want 0\nstderr:\n%s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not stop after cancel")
	}
	if !strings.Contains(stdout.String(), "watch stopped") {
		t.Errorf("missing shutdown message:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(out, "prometheus", "rules", "redis.yml")); err != nil {
		t.Errorf("auto-apply did not write redis rules: %v", err)
	}
}

func TestWatchFlagValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"watch", "--ai"}, &stdout, &stderr); code != 2 {
		t.Errorf("watch --ai exited %d, want 2", code)
	}
	if code := run([]string{"watch", "--interval", "10ms"}, &stdout, &stderr); code != 2 {
		t.Errorf("watch --interval 10ms exited %d, want 2", code)
	}
}
