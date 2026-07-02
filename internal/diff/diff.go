// Package diff compares rendered plan output with the files already in
// an output directory, surfacing pending plan changes and drift (hand
// edits, stale leftovers) before an apply.
package diff

import (
	"os"
	"path/filepath"
	"sort"
)

// Status classifies one file, from the point of view of what apply
// would do with it.
type Status string

const (
	Create    Status = "create"    // in the plan, missing on disk
	Update    Status = "update"    // on disk with different content
	Unchanged Status = "unchanged" // identical
	// Stale files live in a directory monup owns but are no longer part
	// of the plan. Apply never deletes, so they linger until removed by
	// hand.
	Stale Status = "stale"
)

// File is the comparison result for one path, relative to the output dir.
type File struct {
	Path   string
	Status Status
	// Diff is a unified diff from disk content to plan content, set for
	// Update only.
	Diff string
}

// ownedDirs are wholly generated: anything in them the plan no longer
// produces is stale. Other locations may hold user files (.env, notes)
// and are left alone.
var ownedDirs = []string{"prometheus/rules", "grafana/dashboards"}

// Dir compares rendered files against outDir. A missing outDir is not an
// error: every file simply comes back as Create.
func Dir(outDir string, rendered map[string][]byte) ([]File, error) {
	paths := make([]string, 0, len(rendered))
	for p := range rendered {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []File
	for _, path := range paths {
		disk, err := os.ReadFile(filepath.Join(outDir, path))
		switch {
		case os.IsNotExist(err):
			out = append(out, File{Path: path, Status: Create})
		case err != nil:
			return nil, err
		case string(disk) == string(rendered[path]):
			out = append(out, File{Path: path, Status: Unchanged})
		default:
			out = append(out, File{Path: path, Status: Update, Diff: Lines(disk, rendered[path])})
		}
	}

	for _, dir := range ownedDirs {
		entries, err := os.ReadDir(filepath.Join(outDir, dir))
		if err != nil {
			continue // missing dir: nothing stale in it
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			rel := dir + "/" + e.Name()
			if _, ok := rendered[rel]; !ok {
				out = append(out, File{Path: rel, Status: Stale})
			}
		}
	}
	return out, nil
}

// Maps compares two rendered trees in memory (old → new), without
// touching disk. Paths only in old come back Stale, mirroring what an
// apply after the change would leave behind on disk.
func Maps(old, cur map[string][]byte) []File {
	paths := make([]string, 0, len(cur))
	for p := range cur {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []File
	for _, path := range paths {
		prev, ok := old[path]
		switch {
		case !ok:
			out = append(out, File{Path: path, Status: Create})
		case string(prev) == string(cur[path]):
			out = append(out, File{Path: path, Status: Unchanged})
		default:
			out = append(out, File{Path: path, Status: Update, Diff: Lines(prev, cur[path])})
		}
	}

	var removed []string
	for path := range old {
		if _, ok := cur[path]; !ok {
			removed = append(removed, path)
		}
	}
	sort.Strings(removed)
	for _, path := range removed {
		out = append(out, File{Path: path, Status: Stale})
	}
	return out
}

// Changed reports whether any file differs from the plan.
func Changed(files []File) bool {
	for _, f := range files {
		if f.Status != Unchanged {
			return true
		}
	}
	return false
}
