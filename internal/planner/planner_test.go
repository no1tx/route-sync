package planner

import (
	"net/netip"
	"testing"

	"route-sync/internal/config"
	"route-sync/internal/rtnl"
)

func TestRouteDiffPlanning(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	g := InputGroup{Config: config.RouteGroup{Name: "x", Target: config.TargetConfig{Table: 100, Dev: "eth0", Family: "ipv4"}, Rule: config.RuleConfig{Enabled: true, Priority: 1000, Table: 100, FWMark: 1, Mask: 255}}, Prefixes: []netip.Prefix{prefix}, LinkIndex: 2}
	p := Build(99, []InputGroup{g})
	if len(p.Groups[0].RoutesToAdd) != 1 || len(p.Groups[0].RulesToAdd) != 1 {
		t.Fatalf("bad plan: %+v", p.Groups[0])
	}
	g.CurrentRoutes = []rtnl.Route{{Table: 100, Protocol: 99, LinkIndex: 2, Dst: prefix}}
	g.CurrentRules = []rtnl.Rule{{Priority: 1000, Table: 100, HasFWMark: true, FWMark: 1, Mask: 255}}
	p = Build(99, []InputGroup{g})
	if len(p.Groups[0].RoutesToAdd) != 0 || len(p.Groups[0].RoutesToRemove) != 0 || len(p.Groups[0].RulesToAdd) != 0 {
		t.Fatalf("expected empty diff: %+v", p.Groups[0])
	}
}

func TestReverseModeAddsDefaultRouteAndPrefixExceptions(t *testing.T) {
	prefix := netip.MustParsePrefix("91.142.141.0/24")
	throwPrefix := netip.MustParsePrefix("100.64.0.0/10")
	g := InputGroup{
		Config: config.RouteGroup{
			Name: "ru_default",
			Target: config.TargetConfig{
				Table:  100,
				Dev:    "enp1s0",
				Family: "ipv4",
				Via4:   "172.23.0.1",
				Default: &config.NextHopConfig{
					Dev:  "wg-south",
					Via4: "10.77.0.2",
				},
			},
			Rule: config.RuleConfig{Enabled: true, Priority: 1500, Table: 100, From: "100.64.0.0/10"},
		},
		Prefixes:         []netip.Prefix{prefix},
		ThrowPrefixes:    []netip.Prefix{throwPrefix},
		LinkIndex:        2,
		DefaultLinkIndex: 7,
	}
	p := Build(99, []InputGroup{g})
	if len(p.Groups[0].RoutesToAdd) != 3 {
		t.Fatalf("expected prefix route plus throw exclusion plus default route, got %+v", p.Groups[0].RoutesToAdd)
	}
	var sawRU, sawDefault, sawThrow bool
	for _, r := range p.Groups[0].RoutesToAdd {
		switch r.Dst {
		case prefix:
			sawRU = r.LinkIndex == 2 && r.Via.String() == "172.23.0.1"
		case throwPrefix:
			sawThrow = r.Type == rtnl.RouteTypeThrow
		case netip.MustParsePrefix("0.0.0.0/0"):
			sawDefault = r.LinkIndex == 7 && r.Via.String() == "10.77.0.2"
		}
	}
	if !sawRU || !sawDefault || !sawThrow {
		t.Fatalf("bad reverse routes: %+v", p.Groups[0].RoutesToAdd)
	}
	if len(p.Groups[0].RulesToAdd) != 1 || p.Groups[0].RulesToAdd[0].HasFWMark {
		t.Fatalf("expected source-only rule, got %+v", p.Groups[0].RulesToAdd)
	}
}

func TestLocalAddressCoverageRemovesWholeSourcePrefix(t *testing.T) {
	covered := netip.MustParsePrefix("91.142.141.0/24")
	kept := netip.MustParsePrefix("91.142.144.0/20")
	g := InputGroup{
		Config: config.RouteGroup{
			Name: "ru_default",
			Target: config.TargetConfig{
				Table:  100,
				Dev:    "enp1s0",
				Family: "ipv4",
				Via4:   "172.23.0.1",
				Default: &config.NextHopConfig{
					Dev:  "wg-south",
					Via4: "10.77.0.2",
				},
			},
		},
		Prefixes:          []netip.Prefix{covered, kept},
		CoveredLocalAddrs: []netip.Addr{netip.MustParseAddr("91.142.141.5")},
		LinkIndex:         2,
		DefaultLinkIndex:  7,
	}
	p := Build(99, []InputGroup{g})
	for _, r := range p.Groups[0].RoutesToAdd {
		if r.Dst == covered {
			t.Fatalf("covered local address prefix should be excluded from desired routes: %+v", p.Groups[0].RoutesToAdd)
		}
	}
	var sawKept bool
	for _, r := range p.Groups[0].RoutesToAdd {
		if r.Dst == kept {
			sawKept = true
		}
	}
	if !sawKept {
		t.Fatalf("uncovered prefix should remain: %+v", p.Groups[0].RoutesToAdd)
	}
}

func TestDualStackRoutesUseFamilySpecificGatewaysAndOnlink(t *testing.T) {
	v4 := netip.MustParsePrefix("10.0.0.0/24")
	v6 := netip.MustParsePrefix("2001:db8::/32")
	g := InputGroup{Config: config.RouteGroup{Name: "x", Target: config.TargetConfig{Table: 100, Dev: "eth0", Family: "dual", Via4: "192.0.2.1", Via6: "2001:db8::1", OnLink: true}}, Prefixes: []netip.Prefix{v4, v6}, LinkIndex: 2}
	p := Build(99, []InputGroup{g})
	if len(p.Groups[0].RoutesToAdd) != 2 {
		t.Fatalf("bad plan: %+v", p.Groups[0])
	}
	seen4, seen6 := false, false
	for _, r := range p.Groups[0].RoutesToAdd {
		if !r.OnLink {
			t.Fatalf("expected onlink route: %+v", r)
		}
		switch r.Dst {
		case v4:
			seen4 = r.Via.String() == "192.0.2.1"
		case v6:
			seen6 = r.Via.String() == "2001:db8::1"
		}
	}
	if !seen4 || !seen6 {
		t.Fatalf("family gateways not selected correctly: %+v", p.Groups[0].RoutesToAdd)
	}
}
