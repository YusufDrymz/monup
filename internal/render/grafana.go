package render

import (
	"encoding/json"

	"github.com/YusufDrymz/monup/internal/catalog"
)

const grafanaDatasource = generatedHeader + `apiVersion: 1
datasources:
  - name: Prometheus (monup)
    uid: monup-prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
`

const grafanaProvider = generatedHeader + `apiVersion: 1
providers:
  - name: monup
    folder: monup
    type: file
    disableDeletion: false
    options:
      path: /var/lib/grafana/dashboards
`

// Minimal Grafana dashboard model, just enough for provisioning.
type gfDashboard struct {
	UID           string    `json:"uid"`
	Title         string    `json:"title"`
	Tags          []string  `json:"tags"`
	Timezone      string    `json:"timezone"`
	SchemaVersion int       `json:"schemaVersion"`
	Refresh       string    `json:"refresh"`
	Time          gfTime    `json:"time"`
	Panels        []gfPanel `json:"panels"`
}

type gfTime struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type gfPanel struct {
	ID          int           `json:"id"`
	Type        string        `json:"type"`
	Title       string        `json:"title"`
	GridPos     gfGridPos     `json:"gridPos"`
	Datasource  gfDatasource  `json:"datasource"`
	FieldConfig gfFieldConfig `json:"fieldConfig"`
	Targets     []gfTarget    `json:"targets"`
}

type gfGridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

type gfDatasource struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type gfFieldConfig struct {
	Defaults  gfFieldDefaults `json:"defaults"`
	Overrides []struct{}      `json:"overrides"`
}

type gfFieldDefaults struct {
	Unit string `json:"unit,omitempty"`
}

type gfTarget struct {
	Expr  string `json:"expr"`
	RefID string `json:"refId"`
}

var promDS = gfDatasource{Type: "prometheus", UID: "monup-prometheus"}

// dashboardJSON renders a catalog dashboard into Grafana provisioning
// JSON: two panels per row, stat panels kept narrow.
func dashboardJSON(e catalog.Entry) ([]byte, error) {
	d := gfDashboard{
		UID:           "monup-" + e.Name,
		Title:         e.Dashboard.Title + " · monup",
		Tags:          []string{"monup"},
		Timezone:      "browser",
		SchemaVersion: 39,
		Refresh:       "30s",
		Time:          gfTime{From: "now-1h", To: "now"},
	}
	for i, p := range e.Dashboard.Panels {
		typ := p.Type
		if typ == "" {
			typ = "timeseries"
		}
		d.Panels = append(d.Panels, gfPanel{
			ID:    i + 1,
			Type:  typ,
			Title: p.Title,
			GridPos: gfGridPos{
				H: 8, W: 12,
				X: (i % 2) * 12,
				Y: (i / 2) * 8,
			},
			Datasource:  promDS,
			FieldConfig: gfFieldConfig{Defaults: gfFieldDefaults{Unit: p.Unit}, Overrides: []struct{}{}},
			Targets:     []gfTarget{{Expr: p.Expr, RefID: "A"}},
		})
	}
	return json.MarshalIndent(d, "", "  ")
}
