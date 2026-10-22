package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/metrics"
)

func scrapeMetrics(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func requireMetric(t *testing.T, body, line string) {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if l == line {
			return
		}
	}
	name := strings.SplitN(line, "{", 2)[0]
	var got []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, name) {
			got = append(got, l)
		}
	}
	t.Errorf("missing %q; have:\n%s", line, strings.Join(got, "\n"))
}

func TestMetrics_SendOutcomes(t *testing.T) {
	m := metrics.New()
	srv := newAccessServer([]ResolvedKey{{Name: "open", Key: "open"}}, map[string]error{
		otherUUID: errors.New("send failed: HTTP 500"),
	}, WithMetrics(m))

	sendAs(srv, "open", ownUUID)               // delivered
	sendAs(srv, "open", ownUUID+","+otherUUID) // partial
	sendAs(srv, "open", otherUUID)             // all failed, 502
	doRequest(srv, "GET", "/no/such/route", nil, nil)

	body := scrapeMetrics(t, m)
	requireMetric(t, body, `express_botx_deliveries_total{handler="/api/v1/send",outcome="delivered"} 2`)
	requireMetric(t, body, `express_botx_deliveries_total{handler="/api/v1/send",outcome="failed"} 2`)
	requireMetric(t, body, `express_botx_deliveries_total{handler="/api/v1/send",outcome="queued"} 0`)
	requireMetric(t, body, `express_botx_partial_deliveries_total{handler="/api/v1/send"} 1`)
	requireMetric(t, body, `express_botx_http_requests_total{code="200",handler="/api/v1/send",method="POST"} 2`)
	requireMetric(t, body, `express_botx_http_requests_total{code="502",handler="/api/v1/send",method="POST"} 1`)
	requireMetric(t, body, `express_botx_http_requests_total{code="404",handler="other",method="GET"} 1`)
	if strings.Contains(body, ownUUID) || strings.Contains(body, "/no/such/route") {
		t.Error("a chat or a raw path became a label value")
	}
}

func TestMetrics_AccessStatuses(t *testing.T) {
	m := metrics.New()
	srv := newAccessServer([]ResolvedKey{{Name: "open", Key: "open"}}, map[string]error{
		ownUUID:   ErrAccessDenied,
		otherUUID: ErrAccessUnavailable,
	}, WithMetrics(m))
	sendAs(srv, "open", ownUUID)
	sendAs(srv, "open", otherUUID)

	body := scrapeMetrics(t, m)
	requireMetric(t, body, `express_botx_http_requests_total{code="403",handler="/api/v1/send",method="POST"} 1`)
	requireMetric(t, body, `express_botx_http_requests_total{code="503",handler="/api/v1/send",method="POST"} 1`)
}

func TestMetrics_Membership(t *testing.T) {
	m := metrics.New()
	f := &fakeMembership{members: map[string]bool{"/" + ownUUID: true}}
	srv := newMemberServer(f, memberServerOpts{}, WithMetrics(m))

	sendAs(srv, "member", ownUUID)   // lookup, then cache at the re-check
	sendAs(srv, "member", ownUUID)   // cache twice
	sendAs(srv, "member", otherUUID) // lookup, not a member
	f.setErr(errors.New("botx: HTTP 503"))
	sendAs(srv, "member", "5d1c7f3a-9e2b-4a60-8c1d-2b3e4f506172") // unknown to the resolver: 403 before any check

	body := scrapeMetrics(t, m)
	requireMetric(t, body, `express_botx_membership_checks_total{outcome="member",source="lookup"} 1`)
	requireMetric(t, body, `express_botx_membership_checks_total{outcome="member",source="cache"} 3`)
	requireMetric(t, body, `express_botx_membership_checks_total{outcome="not_member",source="lookup"} 1`)
	requireMetric(t, body, `express_botx_membership_lookups_total{outcome="member"} 1`)
	requireMetric(t, body, `express_botx_membership_lookups_total{outcome="not_member"} 1`)
	requireMetric(t, body, `express_botx_membership_lookups_inflight 0`)
}

func TestMetrics_MattermostCreate(t *testing.T) {
	m := metrics.New()
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "open", Key: "open"}}}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "sync-id", nil }
	resolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
	srv := New(cfg, sendFn, resolver, WithMetrics(m), WithMattermost(&MattermostConfig{}))

	body := strings.NewReader(`{"channel_id":"` + ownUUID + `","message":"disk 94%"}`)
	w := doRequest(srv, "POST", "/api/v1/mattermost/api/v4/posts", body, map[string]string{
		"Content-Type": "application/json", "Authorization": "Bearer open",
	})
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	requireMetric(t, scrapeMetrics(t, m), `express_botx_deliveries_total{handler="/api/v1/mattermost/api/v4/posts",outcome="delivered"} 1`)
}
