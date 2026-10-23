package metrics

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const dashboardPath = "../../examples/grafana/express-botx.json"

// identRe matches whole PromQL identifiers, so a name is never cut short at a
// digit or upper-case letter and never taken from the middle of another name.
var identRe = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)

// TestDashboardMetricNames keeps the example Grafana dashboard in step with
// the registry: every express_botx_* name in a panel or variable query must be
// exported by the program. It checks names only, not PromQL or labels.
func TestDashboardMetricNames(t *testing.T) {
	raw, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels     []dashboardPanel `json:"panels"`
		Templating struct {
			List []struct {
				Name  string          `json:"name"`
				Query json.RawMessage `json:"query"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("%s: %v", dashboardPath, err)
	}

	var queries []string
	var walk func([]dashboardPanel)
	walk = func(ps []dashboardPanel) {
		for _, p := range ps {
			for _, tg := range p.Targets {
				queries = append(queries, tg.Expr)
			}
			walk(p.Panels) // panels inside collapsed rows
		}
	}
	walk(dash.Panels)
	panelQueries := len(queries)
	for _, v := range dash.Templating.List {
		var q struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(v.Query, &q) == nil && q.Query != "" {
			queries = append(queries, q.Query)
		}
	}
	if panelQueries == 0 || len(queries) == panelQueries {
		t.Fatalf("found %d panel and %d variable queries, the dashboard layout is not what the test expects",
			panelQueries, len(queries)-panelQueries)
	}

	known := exportedNames(t)
	used := map[string]bool{}
	for _, q := range queries {
		for _, name := range identRe.FindAllString(q, -1) {
			if strings.HasPrefix(name, "express_botx_") {
				used[name] = true
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("no express_botx_* names found in dashboard queries")
	}
	var missing []string
	for name := range used {
		if !known[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s uses metrics the program does not export: %v", dashboardPath, missing)
	}
}

type dashboardPanel struct {
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
	Panels []dashboardPanel `json:"panels"`
}

// exportedNames returns the names a scrape can contain. Vectors only appear
// once a series exists, so every family gets one observation first.
func exportedNames(t *testing.T) map[string]bool {
	t.Helper()
	m := New()
	m.SetBuildInfo("0.0.0", "sync")
	m.InitSendRoute("/api/v1/send")
	m.ObserveHTTP("POST", "/api/v1/send", 200, time.Millisecond)
	m.Delivery("/api/v1/send", Delivered)
	m.PartialDelivery("/api/v1/send")
	m.Edit("/api/v1/edit", Delivered)
	m.ObserveBotX(httptest.NewRequest("POST", "/api/v4/botx/notifications/direct", nil),
		&http.Response{StatusCode: 202}, nil, time.Millisecond)
	m.MembershipCheck(Member, SourceLookup)
	m.LookupStarted()
	m.LookupDone(Member)
	m.LookupRejected()
	m.QueuePublish(nil)
	m.QueuePublish(errors.New("broker down"))

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range families {
		// A classic histogram is scraped only as _bucket, _sum and _count.
		if mf.GetType().String() == "HISTOGRAM" {
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				names[mf.GetName()+suffix] = true
			}
		} else {
			names[mf.GetName()] = true
		}
	}
	return names
}
