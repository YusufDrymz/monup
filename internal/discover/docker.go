package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// dockerAPIVersion is intentionally old for broad daemon compatibility.
const dockerAPIVersion = "v1.24"

// defaultNetworks are Docker's built-in networks; being attached to them
// does not give other containers a DNS route to the service.
var defaultNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// DockerClient talks to the Docker Engine API over a unix socket using
// only the standard library.
type DockerClient struct {
	httpc  *http.Client
	socket string
}

// FindDockerSocket returns the first usable Docker socket path, checking
// $DOCKER_HOST (unix:// only), the system default and the Docker Desktop
// per-user location. Returns "" when none exists.
func FindDockerSocket() string {
	var candidates []string
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		candidates = append(candidates, strings.TrimPrefix(h, "unix://"))
	}
	candidates = append(candidates, "/var/run/docker.sock")
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// NewDockerClient creates a client for the given unix socket path.
func NewDockerClient(socket string) *DockerClient {
	return &DockerClient{
		socket: socket,
		httpc: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		},
	}
}

// containerJSON mirrors the fields we need from GET /containers/json.
type containerJSON struct {
	Names           []string `json:"Names"`
	Image           string   `json:"Image"`
	State           string   `json:"State"`
	Ports           []portJSON
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type portJSON struct {
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// ListContainers returns running containers as discovery candidates.
// Containers created by monup itself (ManagedLabel) are skipped.
func (c *DockerClient) ListContainers(ctx context.Context) ([]Service, error) {
	url := fmt.Sprintf("http://unix/%s/containers/json", dockerAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker socket %s: %w", c.socket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned %s", resp.Status)
	}
	var raw []containerJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode docker response: %w", err)
	}

	var services []Service
	for _, ct := range raw {
		if ct.Labels[ManagedLabel] == "true" {
			continue
		}
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		svc := Service{
			Name:      name,
			Image:     ct.Image,
			Published: map[int]int{},
			Labels:    ct.Labels,
			Source:    "docker",
		}
		seen := map[int]bool{}
		for _, p := range ct.Ports {
			if p.Type != "" && p.Type != "tcp" {
				continue
			}
			if !seen[p.PrivatePort] {
				seen[p.PrivatePort] = true
				svc.Ports = append(svc.Ports, p.PrivatePort)
			}
			if p.PublicPort != 0 {
				svc.Published[p.PrivatePort] = p.PublicPort
			}
		}
		sort.Ints(svc.Ports)
		netNames := make([]string, 0, len(ct.NetworkSettings.Networks))
		for netName := range ct.NetworkSettings.Networks {
			netNames = append(netNames, netName)
		}
		sort.Strings(netNames)
		for _, netName := range netNames {
			if !defaultNetworks[netName] {
				svc.Networks = append(svc.Networks, netName)
				if svc.IP == "" {
					svc.IP = ct.NetworkSettings.Networks[netName].IPAddress
				}
			}
		}
		if svc.IP == "" {
			// No user-defined network: fall back to bridge & co.
			for _, netName := range netNames {
				if ip := ct.NetworkSettings.Networks[netName].IPAddress; ip != "" {
					svc.IP = ip
					break
				}
			}
		}
		services = append(services, svc)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services, nil
}
