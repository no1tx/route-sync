package planner

import (
	"fmt"
	"net/netip"
	"strings"

	"route-sync/internal/config"
	"route-sync/internal/health"
	"route-sync/internal/rtnl"
	"route-sync/internal/source"
)

type GroupPlan struct {
	Group                    string
	SourceType               string
	TargetTable              int
	Family                   string
	DesiredPrefixCount       int
	CurrentManagedRouteCount int
	CurrentManagedRuleCount  int
	RoutesToAdd              []rtnl.Route
	RoutesToRemove           []rtnl.Route
	RulesToAdd               []rtnl.Rule
	RulesToRemove            []rtnl.Rule
	SourceFromFallback       bool
}

type Plan struct{ Groups []GroupPlan }

type InputGroup struct {
	Config            config.RouteGroup
	Prefixes          []netip.Prefix
	ThrowPrefixes     []netip.Prefix
	CoveredLocalAddrs []netip.Addr
	SourceType        string
	FromFallback      bool
	LinkIndex         int
	DefaultLinkIndex  int
	TargetHops        []health.ResolvedHop
	DefaultHops       []health.ResolvedHop
	CurrentRoutes     []rtnl.Route
	CurrentRules      []rtnl.Rule
}

func Build(routeProtocol int, groups []InputGroup) Plan {
	var p Plan
	for _, g := range groups {
		family := g.Config.Target.Family
		if family == "" {
			family = "dual"
		}
		prefixes := source.FilterFamily(g.Prefixes, family)
		prefixes = removePrefixesCoveringAddrs(prefixes, filterAddrsByFamily(g.CoveredLocalAddrs, family))
		desiredRoutes := desiredRoutes(routeProtocol, g, prefixes)
		desiredRules := desiredRules(g)
		gp := GroupPlan{
			Group: g.Config.Name, SourceType: g.SourceType, TargetTable: g.Config.Target.Table, Family: family,
			DesiredPrefixCount: len(prefixes), CurrentManagedRouteCount: len(g.CurrentRoutes),
			CurrentManagedRuleCount: len(g.CurrentRules), SourceFromFallback: g.FromFallback,
		}
		gp.RoutesToAdd, gp.RoutesToRemove = diffRoutes(desiredRoutes, g.CurrentRoutes)
		gp.RulesToAdd, gp.RulesToRemove = diffRules(desiredRules, g.CurrentRules)
		p.Groups = append(p.Groups, gp)
	}
	return p
}

func desiredRoutes(routeProtocol int, g InputGroup, prefixes []netip.Prefix) []rtnl.Route {
	out := make([]rtnl.Route, 0, len(prefixes)+len(g.ThrowPrefixes)+2)
	for _, p := range prefixes {
		hops := matchingHops(g.TargetHops, p)
		if len(hops) == 0 {
			out = append(out, rtnl.Route{Group: g.Config.Name, Table: g.Config.Target.Table, Protocol: routeProtocol, Dev: g.Config.Target.Dev, LinkIndex: g.LinkIndex, Via: routeVia(g.Config.Target, p), OnLink: g.Config.Target.OnLink, Dst: p})
			continue
		}
		for _, hop := range hops {
			out = append(out, rtnl.Route{Group: g.Config.Name, Table: g.Config.Target.Table, Protocol: routeProtocol, Dev: hop.Dev, LinkIndex: hop.LinkIndex, Via: hop.Via, OnLink: hop.OnLink, Metric: hop.Metric, Dst: p})
		}
	}
	for _, p := range source.FilterFamily(g.ThrowPrefixes, g.Config.Target.Family) {
		out = append(out, rtnl.Route{Group: g.Config.Name, Table: g.Config.Target.Table, Protocol: routeProtocol, Type: rtnl.RouteTypeThrow, Dst: p})
	}
	if g.Config.Target.Default != nil {
		for _, p := range defaultPrefixes(g.Config.Target.Family) {
			hops := matchingHops(g.DefaultHops, p)
			if len(hops) == 0 {
				out = append(out, rtnl.Route{Group: g.Config.Name, Table: g.Config.Target.Table, Protocol: routeProtocol, Dev: g.Config.Target.Default.Dev, LinkIndex: g.DefaultLinkIndex, Via: nextHopVia(*g.Config.Target.Default, p), OnLink: g.Config.Target.Default.OnLink, Dst: p})
				continue
			}
			for _, hop := range hops {
				out = append(out, rtnl.Route{Group: g.Config.Name, Table: g.Config.Target.Table, Protocol: routeProtocol, Dev: hop.Dev, LinkIndex: hop.LinkIndex, Via: hop.Via, OnLink: hop.OnLink, Metric: hop.Metric, Dst: p})
			}
		}
	}
	return out
}

func matchingHops(hops []health.ResolvedHop, p netip.Prefix) []health.ResolvedHop {
	if len(hops) == 0 {
		return nil
	}
	want4 := p.Addr().Is4()
	out := make([]health.ResolvedHop, 0, len(hops))
	for _, hop := range hops {
		if hop.Via.IsValid() && hop.Via.Is4() == want4 {
			out = append(out, hop)
		}
	}
	return out
}

func removePrefixesCoveringAddrs(prefixes []netip.Prefix, addrs []netip.Addr) []netip.Prefix {
	if len(prefixes) == 0 || len(addrs) == 0 {
		return prefixes
	}
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		covered := false
		for _, addr := range addrs {
			if p.Addr().Is4() == addr.Is4() && p.Contains(addr) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

func filterAddrsByFamily(addrs []netip.Addr, family string) []netip.Addr {
	if family == "" || family == "dual" {
		return addrs
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		if family == "ipv4" && addr.Is4() {
			out = append(out, addr)
		}
		if family == "ipv6" && addr.Is6() {
			out = append(out, addr)
		}
	}
	return out
}

func routeVia(t config.TargetConfig, p netip.Prefix) netip.Addr {
	return nextHopVia(config.NextHopConfig{Via: t.Via, Via4: t.Via4, Via6: t.Via6}, p)
}

func nextHopVia(h config.NextHopConfig, p netip.Prefix) netip.Addr {
	if p.Addr().Is4() && h.Via4 != "" {
		addr, _ := netip.ParseAddr(h.Via4)
		return addr
	}
	if p.Addr().Is6() && h.Via6 != "" {
		addr, _ := netip.ParseAddr(h.Via6)
		return addr
	}
	if h.Via == "" {
		return netip.Addr{}
	}
	addr, _ := netip.ParseAddr(h.Via)
	if (p.Addr().Is4() && addr.Is4()) || (p.Addr().Is6() && addr.Is6()) {
		return addr
	}
	return netip.Addr{}
}

func defaultPrefixes(family string) []netip.Prefix {
	switch family {
	case "ipv4":
		return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	case "ipv6":
		return []netip.Prefix{netip.MustParsePrefix("::/0")}
	default:
		return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	}
}

func desiredRules(g InputGroup) []rtnl.Rule {
	if !g.Config.Rule.Enabled {
		return nil
	}
	var from netip.Prefix
	if g.Config.Rule.From != "" {
		from, _ = netip.ParsePrefix(g.Config.Rule.From)
	}
	return []rtnl.Rule{{Group: g.Config.Name, Priority: g.Config.Rule.Priority, Table: g.Config.Rule.Table, HasFWMark: g.Config.Rule.Mask != 0, FWMark: g.Config.Rule.FWMark, Mask: g.Config.Rule.Mask, From: from}}
}

func diffRoutes(desired, current []rtnl.Route) ([]rtnl.Route, []rtnl.Route) {
	dm := map[string]rtnl.Route{}
	cm := map[string]rtnl.Route{}
	for _, r := range desired {
		dm[routeKey(r)] = r
	}
	for _, r := range current {
		cm[routeKey(r)] = r
	}
	var add, remove []rtnl.Route
	for k, r := range dm {
		if _, ok := cm[k]; !ok {
			add = append(add, r)
		}
	}
	for k, r := range cm {
		if _, ok := dm[k]; !ok {
			remove = append(remove, r)
		}
	}
	return add, remove
}

func diffRules(desired, current []rtnl.Rule) ([]rtnl.Rule, []rtnl.Rule) {
	dm := map[string]rtnl.Rule{}
	cm := map[string]rtnl.Rule{}
	for _, r := range desired {
		dm[ruleKey(r)] = r
	}
	for _, r := range current {
		cm[ruleKey(r)] = r
	}
	var add, remove []rtnl.Rule
	for k, r := range dm {
		if _, ok := cm[k]; !ok {
			add = append(add, r)
		}
	}
	for k, r := range cm {
		if _, ok := dm[k]; !ok {
			remove = append(remove, r)
		}
	}
	return add, remove
}

func routeKey(r rtnl.Route) string {
	return fmt.Sprintf("%d|%d|%d|%d|%s|%t|%d|%s", r.Table, r.Protocol, r.Type, r.LinkIndex, r.Via, r.OnLink, r.Metric, r.Dst)
}

func ruleKey(r rtnl.Rule) string {
	return fmt.Sprintf("%d|%d|%t|%d|%d|%s", r.Priority, r.Table, r.HasFWMark, r.FWMark, r.Mask, r.From)
}

func (p Plan) String() string {
	var b strings.Builder
	for _, g := range p.Groups {
		fb := ""
		if g.SourceFromFallback {
			fb = " fallback=true"
		}
		fmt.Fprintf(&b, "group=%s source=%s table=%d family=%s desired_prefixes=%d current_routes=%d current_rules=%d%s\n", g.Group, g.SourceType, g.TargetTable, g.Family, g.DesiredPrefixCount, g.CurrentManagedRouteCount, g.CurrentManagedRuleCount, fb)
		fmt.Fprintf(&b, "  routes: add=%d remove=%d rules: add=%d remove=%d\n", len(g.RoutesToAdd), len(g.RoutesToRemove), len(g.RulesToAdd), len(g.RulesToRemove))
	}
	return b.String()
}
