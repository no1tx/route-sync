package health

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"route-sync/internal/config"
	"route-sync/internal/metrics"
)

const (
	metricBase            = 100
	metricStep            = 10
	unhealthyMetricOffset = 1000
)

var (
	defaultIPv4Targets = []string{"8.8.8.8", "1.1.1.1"}
	defaultIPv6Targets = []string{"2001:4860:4860::8888", "2606:4700:4700::1111"}
)

type Pinger interface {
	Ping(ctx context.Context, dev string, target netip.Addr, timeout time.Duration) error
}

type ExecPinger struct{}

func (ExecPinger) Ping(ctx context.Context, dev string, target netip.Addr, timeout time.Duration) error {
	family := "-4"
	if target.Is6() {
		family = "-6"
	}
	waitSeconds := int(math.Ceil(timeout.Seconds()))
	if waitSeconds < 1 {
		waitSeconds = 1
	}
	args := []string{family, "-c", "1", "-W", strconv.Itoa(waitSeconds)}
	if dev != "" {
		args = append(args, "-I", dev)
	}
	args = append(args, target.String())
	cmd := exec.CommandContext(ctx, "ping", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

type Candidate struct {
	Name        string
	Dev         string
	OnLink      bool
	Via         netip.Addr
	HealthCheck config.HealthCheck
}

type ResolvedHop struct {
	Name       string
	Dev        string
	LinkIndex  int
	OnLink     bool
	Via        netip.Addr
	Metric     int
	Healthy    bool
	CheckedVia netip.Addr
}

func Select(ctx context.Context, family string, scope string, candidates []Candidate, linkIndexByName func(string) (int, error), pinger Pinger, reg *metrics.Registry, log *slog.Logger) ([]ResolvedHop, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	type item struct {
		ResolvedHop
		index int
	}
	items := make([]item, 0, len(candidates))
	for i, c := range candidates {
		linkIndex, err := linkIndexByName(c.Dev)
		if err != nil {
			return nil, err
		}
		healthy, checkedVia := evaluateCandidate(ctx, family, c, pinger)
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", scope, i+1)
		}
		items = append(items, item{
			index: i,
			ResolvedHop: ResolvedHop{
				Name:       name,
				Dev:        c.Dev,
				LinkIndex:  linkIndex,
				OnLink:     c.OnLink,
				Via:        c.Via,
				Healthy:    healthy,
				CheckedVia: checkedVia,
			},
		})
		reg.Set(fmt.Sprintf(`gateway_health{scope="%s",gateway="%s"}`, sanitize(scope), sanitize(name)), boolGauge(healthy))
	}
	hasHealthy := false
	for _, it := range items {
		if it.Healthy {
			hasHealthy = true
			break
		}
	}
	ordered := make([]item, 0, len(items))
	if hasHealthy {
		for _, it := range items {
			if it.Healthy {
				ordered = append(ordered, it)
			}
		}
		for _, it := range items {
			if !it.Healthy {
				ordered = append(ordered, it)
			}
		}
	} else {
		ordered = append(ordered, items...)
		log.Warn("all gateway health checks failed; preserving configured order", "scope", scope, "family", family)
	}
	out := make([]ResolvedHop, 0, len(ordered))
	for i, it := range ordered {
		metric := metricBase + i*metricStep
		if hasHealthy && !it.Healthy {
			metric += unhealthyMetricOffset
		}
		it.Metric = metric
		out = append(out, it.ResolvedHop)
		reg.Set(fmt.Sprintf(`gateway_metric{scope="%s",gateway="%s"}`, sanitize(scope), sanitize(it.Name)), float64(metric))
		log.Info("gateway health evaluated", "scope", scope, "family", family, "gateway", it.Name, "dev", it.Dev, "via", it.Via, "healthy", it.Healthy, "metric", metric)
	}
	return out, nil
}

func evaluateCandidate(ctx context.Context, family string, c Candidate, pinger Pinger) (bool, netip.Addr) {
	timeout := c.HealthCheck.Timeout.Duration
	if timeout == 0 {
		timeout = time.Second
	}
	targets := c.HealthCheck.Targets
	if len(targets) == 0 {
		if family == "ipv6" {
			targets = defaultIPv6Targets
		} else {
			targets = defaultIPv4Targets
		}
	}
	for _, raw := range targets {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		if family == "ipv4" && !addr.Is4() {
			continue
		}
		if family == "ipv6" && !addr.Is6() {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout+time.Second)
		err = pinger.Ping(checkCtx, c.Dev, addr, timeout)
		cancel()
		if err == nil {
			return true, addr
		}
	}
	return false, netip.Addr{}
}

func boolGauge(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func sanitize(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "_").Replace(s)
}
