package discover

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const containersFixture = `[
  {
    "Names": ["/myapp-db"],
    "Image": "postgres:16",
    "State": "running",
    "Ports": [{"PrivatePort": 5432, "PublicPort": 55432, "Type": "tcp"}],
    "Labels": {"com.docker.compose.project": "myapp"},
    "NetworkSettings": {"Networks": {"bridge": {"IPAddress": "172.17.0.9"}, "myapp_default": {"IPAddress": "172.20.0.2"}}}
  },
  {
    "Names": ["/cache"],
    "Image": "redis:7-alpine",
    "State": "running",
    "Ports": [{"PrivatePort": 6379, "Type": "tcp"}],
    "Labels": {},
    "NetworkSettings": {"Networks": {"bridge": {"IPAddress": "172.17.0.3"}}}
  },
  {
    "Names": ["/monup-prometheus"],
    "Image": "prom/prometheus:v2.53.0",
    "State": "running",
    "Ports": [{"PrivatePort": 9090, "PublicPort": 9090, "Type": "tcp"}],
    "Labels": {"monup.managed": "true"},
    "NetworkSettings": {"Networks": {"monup_monup": {}}}
  }
]`

// startFakeDocker serves the fixture on a unix socket and returns its path.
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
	mux := http.NewServeMux()
	mux.HandleFunc("/"+dockerAPIVersion+"/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(containersFixture))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestListContainers(t *testing.T) {
	sock := startFakeDocker(t)
	c := NewDockerClient(sock)

	got, err := c.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error: %v", err)
	}
	// The monup-managed container must be filtered out; results sorted by name.
	if len(got) != 2 {
		t.Fatalf("expected 2 services, got %d: %+v", len(got), got)
	}

	db := got[1] // "myapp-db" sorts after "cache"
	if db.Name != "myapp-db" || db.Image != "postgres:16" {
		t.Errorf("unexpected service: %+v", db)
	}
	if !reflect.DeepEqual(db.Ports, []int{5432}) {
		t.Errorf("ports = %v, want [5432]", db.Ports)
	}
	if db.Published[5432] != 55432 {
		t.Errorf("published = %v, want 5432→55432", db.Published)
	}
	if !reflect.DeepEqual(db.Networks, []string{"myapp_default"}) {
		t.Errorf("networks = %v, want [myapp_default]", db.Networks)
	}
	// The user-defined network's address wins over bridge.
	if db.IP != "172.20.0.2" {
		t.Errorf("IP = %q, want 172.20.0.2", db.IP)
	}

	cache := got[0]
	if cache.Name != "cache" {
		t.Errorf("unexpected first service: %+v", cache)
	}
	// Default bridge network must be excluded.
	if len(cache.Networks) != 0 {
		t.Errorf("networks = %v, want empty (bridge excluded)", cache.Networks)
	}
	// But its address is still a valid probe target.
	if cache.IP != "172.17.0.3" {
		t.Errorf("IP = %q, want 172.17.0.3", cache.IP)
	}
	if len(cache.Published) != 0 {
		t.Errorf("published = %v, want empty", cache.Published)
	}
}

func TestFindDockerSocketEnv(t *testing.T) {
	dir, err := os.MkdirTemp("", "monup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "custom.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	if got := FindDockerSocket(); got != sock {
		t.Errorf("FindDockerSocket() = %q, want %q", got, sock)
	}
}
