package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"route-sync/internal/config"
	"route-sync/internal/rtnl"
)

func TestValidateReloadKeepsCurrentOnInvalid(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(good, []byte(minimalConfig("text")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("global:\n  log_format: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := loadConfig(Options{ConfigPath: good})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValidateReload(current, Options{ConfigPath: bad})
	if err == nil {
		t.Fatal("expected invalid reload")
	}
	if got != current {
		t.Fatal("expected current config to be preserved")
	}
}

func TestBuildCleanupPlanRemovesOwnedState(t *testing.T) {
	k := cleanupFakeKernel{
		routes: []rtnl.Route{{Table: 100, Protocol: 99, Dst: mustPrefix("10.0.0.0/24")}},
		rules:  []rtnl.Rule{{Priority: 1500, Table: 100, From: mustPrefix("100.64.0.0/10")}},
	}
	cfg := &config.Config{
		Global:   config.GlobalConfig{RouteProtocol: 99, RulePriorityBase: 1000, RulePriorityStep: 10},
		Defaults: config.Defaults{EnableRUBuiltinSource: boolPtr(true)},
		Routing: config.Routing{RUDefault: config.RouteGroup{
			Name: "ru_default", Enabled: true,
			Source: config.SourceConfig{Type: "ripe_country", Country: "RU"},
			Target: config.TargetConfig{Table: 100, Dev: "eth0", Family: "ipv4"},
			Rule:   config.RuleConfig{Enabled: true, Priority: 1500, Table: 100},
		}},
	}
	plan, err := BuildCleanupPlan(cfg, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Groups) != 1 || len(plan.Groups[0].RoutesToRemove) != 1 || len(plan.Groups[0].RulesToRemove) != 1 {
		t.Fatalf("bad cleanup plan: %+v", plan)
	}
}

type cleanupFakeKernel struct {
	routes []rtnl.Route
	rules  []rtnl.Rule
}

func (f cleanupFakeKernel) LinkIndexByName(string) (int, error) { return 0, nil }
func (f cleanupFakeKernel) ListRoutes(int, int, int) ([]rtnl.Route, error) {
	return f.routes, nil
}
func (f cleanupFakeKernel) AddRoute(rtnl.Route) error    { return nil }
func (f cleanupFakeKernel) DeleteRoute(rtnl.Route) error { return nil }
func (f cleanupFakeKernel) ListRules(int) ([]rtnl.Rule, error) {
	return f.rules, nil
}
func (f cleanupFakeKernel) AddRule(rtnl.Rule) error    { return nil }
func (f cleanupFakeKernel) DeleteRule(rtnl.Rule) error { return nil }

func boolPtr(v bool) *bool { return &v }

func mustPrefix(raw string) netip.Prefix {
	p, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return p
}

func minimalConfig(logFormat string) string {
	return `global:
  state_dir: /tmp/route-sync-test
  log_format: ` + logFormat + `
routing:
  ru_default:
    enabled: false
    source:
      type: ripe_country
      country: RU
    target:
      table: 100
      dev: eth0
      family: dual
    rule:
      enabled: false
`
}
