package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/diff"
	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
	"github.com/YusufDrymz/monup/internal/render"
)

// cmdWatch polls Docker and reports plan changes as containers come and
// go; with --auto-apply it keeps the output directory in sync.
func cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.register(fs)
	interval := fs.Duration("interval", 30*time.Second, "poll interval")
	autoApply := fs.Bool("auto-apply", false, "write files whenever the plan changes")
	out := fs.String("out", "monup", "output directory (used by --auto-apply)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if cf.ai {
		// Re-running an LLM on every poll would be slow and expensive;
		// run plan/apply --ai by hand when a new unknown service shows up.
		fmt.Fprintln(stderr, "error: --ai is not supported with watch")
		return 2
	}
	if *interval < time.Second {
		fmt.Fprintln(stderr, "error: --interval must be at least 1s")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return watchLoop(ctx, &cf, *interval, *autoApply, *out, stdout, stderr)
}

// snapshot is one polled state: the plan and the files it renders to.
type snapshot struct {
	plan  *plan.Plan
	files map[string][]byte
}

func watchLoop(ctx context.Context, cf *commonFlags, interval time.Duration,
	autoApply bool, out string, stdout, stderr io.Writer) int {

	cat, err := catalog.Load()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	socket := cf.dockerSocket
	if socket == "" {
		socket = discover.FindDockerSocket()
	}
	if socket == "" {
		fmt.Fprintln(stderr, "error: no docker socket found")
		return 1
	}
	client := discover.NewDockerClient(socket)
	color := colorEnabled()

	apply := func(files map[string][]byte) {
		if err := render.CheckRules(files); err != nil {
			fmt.Fprintf(stderr, "%s apply skipped: %v\n", stamp(), err)
			return
		}
		if err := writeFiles(out, files, stdout); err != nil {
			fmt.Fprintf(stderr, "%s apply failed: %v\n", stamp(), err)
			return
		}
		if err := seedEnv(out, files, stdout); err != nil {
			fmt.Fprintf(stderr, "%s apply failed: %v\n", stamp(), err)
		}
	}

	var prev *snapshot
	tick := func() {
		tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		services, err := client.ListContainers(tctx)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			// A Docker hiccup must not read as "all containers are gone",
			// so the tick is skipped instead of diffing an empty plan.
			fmt.Fprintf(stderr, "%s discovery failed, skipping: %v\n", stamp(), err)
			return
		}
		p := plan.Build(services, scanHostPorts(cf, services, stderr), cat, cf.planOptions())
		files, err := render.Files(p)
		if err != nil {
			fmt.Fprintf(stderr, "%s render failed, skipping: %v\n", stamp(), err)
			return
		}
		cur := &snapshot{plan: p, files: files}

		if prev == nil {
			fmt.Fprintf(stdout, "%s watching docker (every %s, ctrl-c to stop); initial plan:\n", stamp(), interval)
			p.Print(stdout, nil, color)
			if autoApply {
				apply(files)
			}
			prev = cur
			return
		}

		delta := p.DeltaFrom(prev.plan)
		changes := diff.Maps(prev.files, files)
		if delta.Empty() && !diff.Changed(changes) {
			prev = cur
			return
		}
		fmt.Fprintf(stdout, "\n%s plan changed:\n", stamp())
		delta.Print(stdout, color)
		if diff.Changed(changes) {
			fmt.Fprintln(stdout)
			diff.Print(stdout, changes, color)
			if autoApply {
				apply(files)
			}
		}
		prev = cur
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		tick()
		select {
		case <-ctx.Done():
			fmt.Fprintf(stdout, "\n%s watch stopped\n", stamp())
			return 0
		case <-t.C:
		}
	}
}

func stamp() string {
	return time.Now().Format("15:04:05")
}
