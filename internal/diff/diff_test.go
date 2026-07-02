package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLines(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{
			name: "equal",
			a:    "one\ntwo\n",
			b:    "one\ntwo\n",
			want: "",
		},
		{
			name: "changed line with context",
			a:    "a\nb\nc\nd\ne\nf\ng\n",
			b:    "a\nb\nc\nD\ne\nf\ng\n",
			want: "@@ -1,7 +1,7 @@\n a\n b\n c\n-d\n+D\n e\n f\n g\n",
		},
		{
			name: "append",
			a:    "a\nb\n",
			b:    "a\nb\nc\n",
			want: "@@ -1,2 +1,3 @@\n a\n b\n+c\n",
		},
		{
			name: "remove from empty result",
			a:    "a\n",
			b:    "",
			want: "@@ -1,1 +1,0 @@\n-a\n",
		},
		{
			name: "two distant changes make two hunks",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n",
			b:    "1\nX\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\nY\n15\n",
			want: "@@ -1,5 +1,5 @@\n 1\n-2\n+X\n 3\n 4\n 5\n" +
				"@@ -11,5 +11,5 @@\n 11\n 12\n 13\n-14\n+Y\n 15\n",
		},
		{
			name: "close changes merge into one hunk",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n",
			b:    "1\nX\n3\n4\n5\nY\n7\n8\n",
			want: "@@ -1,8 +1,8 @@\n 1\n-2\n+X\n 3\n 4\n 5\n-6\n+Y\n 7\n 8\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Lines([]byte(tt.a), []byte(tt.b))
			if got != tt.want {
				t.Errorf("Lines() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestDir(t *testing.T) {
	out := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docker-compose.yml", "services: {}\n")
	write("prometheus/prometheus.yml", "edited by hand\n")
	write("prometheus/rules/mysql.yml", "groups: []\n") // stale
	write(".env", "SECRET=x\n")                         // user file, never reported

	rendered := map[string][]byte{
		"docker-compose.yml":            []byte("services: {}\n"),
		"prometheus/prometheus.yml":     []byte("generated\n"),
		"prometheus/rules/postgres.yml": []byte("groups:\n"),
	}

	files, err := Dir(out, rendered)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Status{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	want := map[string]Status{
		"docker-compose.yml":            Unchanged,
		"prometheus/prometheus.yml":     Update,
		"prometheus/rules/postgres.yml": Create,
		"prometheus/rules/mysql.yml":    Stale,
	}
	if len(got) != len(want) {
		t.Errorf("got %d results, want %d: %v", len(got), len(want), got)
	}
	for path, status := range want {
		if got[path] != status {
			t.Errorf("%s = %q, want %q", path, got[path], status)
		}
	}
	if !Changed(files) {
		t.Error("Changed() = false, want true")
	}

	for _, f := range files {
		if f.Path == "prometheus/prometheus.yml" && !strings.Contains(f.Diff, "-edited by hand") {
			t.Errorf("update diff missing removed line:\n%s", f.Diff)
		}
	}
}

func TestDirMissingOutDir(t *testing.T) {
	rendered := map[string][]byte{"docker-compose.yml": []byte("x\n")}
	files, err := Dir(filepath.Join(t.TempDir(), "nope"), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Status != Create {
		t.Errorf("want single create, got %+v", files)
	}
}

func TestDirAllUnchanged(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "a.yml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := Dir(out, map[string][]byte{"a.yml": []byte("x\n")})
	if err != nil {
		t.Fatal(err)
	}
	if Changed(files) {
		t.Errorf("Changed() = true for identical tree: %+v", files)
	}
}
