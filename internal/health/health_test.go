package health

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"route-sync/internal/config"
	"route-sync/internal/metrics"
)

func TestSelectPromotesHealthyGatewaysAndAssignsMetrics(t *testing.T) {
	pinger := fakeExecPinger{ok: map[string]bool{"8.8.8.8": true}}
	candidates := []Candidate{
		{Name: "primary", Dev: "wg-a", Via: netip.MustParseAddr("10.77.0.1"), HealthCheck: config.HealthCheck{Targets: []string{"203.0.113.1"}}},
		{Name: "backup", Dev: "wg-b", Via: netip.MustParseAddr("10.88.0.1"), HealthCheck: config.HealthCheck{Targets: []string{"8.8.8.8"}}},
	}
	got, err := Select(context.Background(), "ipv4", "ru_default.target", candidates, func(string) (int, error) { return 7, nil }, pinger, metrics.New(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two hops, got %+v", got)
	}
	if got[0].Name != "backup" || got[0].Metric != 100 || !got[0].Healthy {
		t.Fatalf("expected healthy backup promoted first, got %+v", got)
	}
	if got[1].Metric < 1000 || got[1].Healthy {
		t.Fatalf("expected unhealthy primary demoted, got %+v", got)
	}
}

type fakeExecPinger struct {
	ok map[string]bool
}

func (f fakeExecPinger) Ping(_ context.Context, _ string, target netip.Addr, _ time.Duration) error {
	if f.ok[target.String()] {
		return nil
	}
	return errors.New("down")
}
