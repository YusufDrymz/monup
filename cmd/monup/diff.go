package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/YusufDrymz/monup/internal/diff"
	"github.com/YusufDrymz/monup/internal/render"
)

// cmdDiff compares what the current plan would generate with the files
// in the output directory. Exit codes follow diff(1): 0 no differences,
// 1 differences found, 2 trouble.
func cmdDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf commonFlags
	cf.register(fs)
	out := fs.String("out", "monup", "output directory to compare against")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, cat, err := buildPlan(&cf, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if cf.ai {
		if code := runAI(p, cat, stdout, stderr); code != 0 {
			return 2
		}
	}
	files, err := render.Files(p)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	result, err := diff.Dir(*out, files)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "\nComparing plan against %s:\n\n", *out)
	diff.Print(stdout, result, colorEnabled())
	if diff.Changed(result) {
		return 1
	}
	return 0
}
