// monup — terraform-plan for monitoring. Discovers services on the host
// and generates a tailored Prometheus + Grafana stack as plain files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/YusufDrymz/monup/internal/ai"
	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
	"github.com/YusufDrymz/monup/internal/render"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `monup — terraform-plan for monitoring

Usage:
  monup plan     Discover services and preview what would be generated
  monup apply    Generate the monitoring stack files (see --out)
  monup diff     Compare the output directory with the current plan
  monup watch    Poll Docker and report plan changes as containers come and go
  monup catalog  List built-in service definitions
  monup version  Print version

Flags (plan, apply, diff and watch):
  --docker-socket path   Docker socket (default: auto-detect)
  --no-host-scan         Skip host TCP listener scan (linux only)
  --only a,b             Only include these catalog entries
  --exclude a,b          Exclude these catalog entries

Flags (plan, apply and diff):
  --ai                   Use an LLM to classify unknown services and generate
                         dashboards from custom /metrics endpoints
                         (needs ANTHROPIC_API_KEY, OPENAI_API_KEY or OLLAMA_HOST)

Flags (apply, diff and watch):
  --out dir              Output directory (default "monup")

Flags (apply):
  --start                Run 'docker compose up -d' after writing files

Flags (watch):
  --interval dur         Poll interval (default 30s)
  --auto-apply           Write files whenever the plan changes

Exit codes (diff): 0 no differences, 1 differences found, 2 error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "plan":
		return cmdPlan(rest, stdout, stderr)
	case "apply":
		return cmdApply(rest, stdout, stderr)
	case "diff":
		return cmdDiff(rest, stdout, stderr)
	case "watch":
		return cmdWatch(rest, stdout, stderr)
	case "catalog":
		return cmdCatalog(stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "monup %s\n", buildVersion())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// commonFlags are shared by plan and apply.
type commonFlags struct {
	dockerSocket string
	noHostScan   bool
	only         string
	exclude      string
	ai           bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.dockerSocket, "docker-socket", "", "docker socket path (default: auto-detect)")
	fs.BoolVar(&c.noHostScan, "no-host-scan", false, "skip host TCP listener scan")
	fs.StringVar(&c.only, "only", "", "comma-separated catalog entries to include")
	fs.StringVar(&c.exclude, "exclude", "", "comma-separated catalog entries to exclude")
	fs.BoolVar(&c.ai, "ai", false, "classify unknown services and generate dashboards with an LLM")
}

func (c *commonFlags) planOptions() plan.Options {
	return plan.Options{Only: splitList(c.only), Exclude: splitList(c.exclude)}
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildPlan runs discovery and matching; notes are non-fatal findings
// (missing docker socket, unsupported host scan) surfaced to the user.
func buildPlan(cf *commonFlags, stderr io.Writer) (*plan.Plan, *catalog.Catalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var services []discover.Service
	socket := cf.dockerSocket
	if socket == "" {
		socket = discover.FindDockerSocket()
	}
	if socket == "" {
		fmt.Fprintln(stderr, "note: no docker socket found, skipping container discovery")
	} else {
		var err error
		services, err = discover.NewDockerClient(socket).ListContainers(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "note: container discovery failed: %v\n", err)
		}
	}

	cat, err := catalog.Load()
	if err != nil {
		return nil, nil, err
	}
	return plan.Build(services, scanHostPorts(cf, services, stderr), cat, cf.planOptions()), cat, nil
}

// scanHostPorts returns non-loopback host listeners, minus ports already
// published by discovered containers (those belong to the containers,
// not to host services).
func scanHostPorts(cf *commonFlags, services []discover.Service, stderr io.Writer) []int {
	if cf.noHostScan {
		return nil
	}
	ports, err := discover.ListeningPorts()
	switch {
	case errors.Is(err, discover.ErrHostScanUnsupported):
		// Expected off-linux; stay quiet.
		return nil
	case err != nil:
		fmt.Fprintf(stderr, "note: host port scan failed: %v\n", err)
		return nil
	}
	published := map[int]bool{}
	for _, svc := range services {
		for _, hp := range svc.Published {
			published[hp] = true
		}
	}
	var hostPorts []int
	for _, p := range ports {
		if !published[p] {
			hostPorts = append(hostPorts, p)
		}
	}
	return hostPorts
}

// runAI applies the optional AI enrichment step to a built plan.
func runAI(p *plan.Plan, cat *catalog.Catalog, stdout, stderr io.Writer) int {
	client, err := ai.New()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	aiEnrich(ctx, p, cat, client, stdout)
	return 0
}

func cmdPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, cat, err := buildPlan(&cf, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if cf.ai {
		if code := runAI(p, cat, stdout, stderr); code != 0 {
			return code
		}
	}
	files, err := render.Files(p)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := render.CheckRules(files); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	p.Print(stdout, render.Paths(files), colorEnabled())
	fmt.Fprintln(stdout, `Run "monup apply" to write these files, "monup apply --start" to also start the stack.`)
	return 0
}

func cmdApply(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.register(fs)
	out := fs.String("out", "monup", "output directory")
	start := fs.Bool("start", false, "run 'docker compose up -d' after writing files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, cat, err := buildPlan(&cf, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if cf.ai {
		if code := runAI(p, cat, stdout, stderr); code != 0 {
			return code
		}
	}
	files, err := render.Files(p)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := render.CheckRules(files); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	p.Print(stdout, nil, colorEnabled())

	if err := writeFiles(*out, files, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	envPath := filepath.Join(*out, ".env")
	if err := seedEnv(*out, files, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *start {
		fmt.Fprintf(stdout, "\nStarting stack in %s ...\n", *out)
		cmd := exec.Command("docker", "compose", "up", "-d")
		cmd.Dir = *out
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "error: docker compose up: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "\nDone. Grafana: http://localhost:3000  Prometheus: http://localhost:9090")
	} else {
		fmt.Fprintf(stdout, "\nNext: review %s, fill %s, then run:\n  docker compose -f %s up -d\n",
			*out, envPath, filepath.Join(*out, "docker-compose.yml"))
	}
	return 0
}

// seedEnv writes .env from .env.example on the first apply; a missing
// .env would make credential-based exporters crash-loop.
func seedEnv(outDir string, files map[string][]byte, stdout io.Writer) error {
	envPath := filepath.Join(outDir, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(envPath, files[".env.example"], 0o600); err != nil {
		return fmt.Errorf("seed .env: %w", err)
	}
	fmt.Fprintf(stdout, "\nSeeded %s from .env.example — fill in the empty values.\n", envPath)
	return nil
}

// writeFiles writes the rendered tree, reporting per-file status.
func writeFiles(outDir string, files map[string][]byte, stdout io.Writer) error {
	fmt.Fprintf(stdout, "Writing to %s:\n", outDir)
	for _, path := range render.Paths(files) {
		full := filepath.Join(outDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		existing, err := os.ReadFile(full)
		status := "created"
		switch {
		case err == nil && string(existing) == string(files[path]):
			status = "unchanged"
		case err == nil:
			status = "updated"
		}
		if status != "unchanged" {
			if err := os.WriteFile(full, files[path], 0o644); err != nil {
				return err
			}
		}
		fmt.Fprintf(stdout, "  %-9s %s\n", status, path)
	}
	return nil
}

func cmdCatalog(stdout, stderr io.Writer) int {
	cat, err := catalog.Load()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Built-in catalog (%d entries):\n\n", len(cat.Entries))
	for _, e := range cat.Entries {
		how := "direct scrape"
		if e.Exporter != nil {
			how = "exporter " + e.Exporter.Image
		}
		match := "always included"
		if !e.Always {
			var parts []string
			if len(e.Match.Images) > 0 {
				parts = append(parts, "images: "+strings.Join(e.Match.Images, ", "))
			}
			if len(e.Match.Ports) > 0 {
				parts = append(parts, fmt.Sprintf("ports: %v", e.Match.Ports))
			}
			match = strings.Join(parts, " · ")
		}
		panels := 0
		if e.Dashboard != nil {
			panels = len(e.Dashboard.Panels)
		}
		fmt.Fprintf(stdout, "  %-10s %s\n             %s · %d alerts · %d panels\n",
			e.Name, match, how, len(e.Alerts), panels)
	}
	return 0
}

// buildVersion prefers the ldflags value, falling back to the module
// version stamped by `go install pkg@version`.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
