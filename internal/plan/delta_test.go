package plan

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/YusufDrymz/monup/internal/discover"
)

func TestDeltaFrom(t *testing.T) {
	pg := Match{Instance: "postgres", Service: discover.Service{
		Name: "shop-db", Image: "postgres:16", Source: "docker"}}
	redis := Match{Instance: "redis", Service: discover.Service{
		Name: "shop-cache", Image: "redis:7", Source: "docker"}}
	api := discover.Service{Name: "shop-api", Image: "shop/api:1.0", Source: "docker"}

	old := &Plan{Matches: []Match{pg}, Unmatched: []discover.Service{api}}
	cur := &Plan{Matches: []Match{pg, redis}}

	d := cur.DeltaFrom(old)
	wantAdded := []string{"redis  container shop-cache (redis:7)"}
	wantRemoved := []string{"unknown  container shop-api (shop/api:1.0)"}
	if !reflect.DeepEqual(d.Added, wantAdded) {
		t.Errorf("Added = %v, want %v", d.Added, wantAdded)
	}
	if !reflect.DeepEqual(d.Removed, wantRemoved) {
		t.Errorf("Removed = %v, want %v", d.Removed, wantRemoved)
	}
	if d.Empty() {
		t.Error("Empty() = true, want false")
	}

	if d := cur.DeltaFrom(cur); !d.Empty() {
		t.Errorf("delta against self not empty: %+v", d)
	}

	var buf bytes.Buffer
	d.Print(&buf, false)
	for _, want := range []string{"+ redis", "- unknown"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("Print output missing %q:\n%s", want, buf.String())
		}
	}
}
