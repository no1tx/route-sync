package routes

import (
	"log/slog"

	"route-sync/internal/planner"
	"route-sync/internal/rtnl"
)

func CurrentOwned(k rtnl.Kernel, table, protocol int, family string) ([]rtnl.Route, error) {
	var out []rtnl.Route
	if family == "dual" || family == "" || family == "ipv4" {
		rs, err := k.ListRoutes(table, 4, protocol)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	if family == "dual" || family == "" || family == "ipv6" {
		rs, err := k.ListRoutes(table, 6, protocol)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

func Apply(k rtnl.Kernel, plans []planner.GroupPlan, log *slog.Logger) error {
	for _, gp := range plans {
		preRemove := conflictingRouteRemovals(gp)
		for _, r := range preRemove {
			log.Info("removing owned route before replacement", "group", gp.Group, "table", r.Table, "dst", r.Dst, "type", routeType(r), "dev", r.Dev, "via", r.Via, "onlink", r.OnLink)
			if err := k.DeleteRoute(r); err != nil {
				return err
			}
		}
		for _, r := range gp.RoutesToAdd {
			log.Info("adding owned route", "group", gp.Group, "table", r.Table, "dst", r.Dst, "type", routeType(r), "dev", r.Dev, "via", r.Via, "onlink", r.OnLink)
			if err := k.AddRoute(r); err != nil {
				return err
			}
		}
	}
	for _, gp := range plans {
		preRemoved := routeIdentitySet(conflictingRouteRemovals(gp))
		for _, r := range gp.RoutesToRemove {
			if preRemoved[routeIdentity(r)] {
				continue
			}
			log.Info("removing obsolete owned route", "group", gp.Group, "table", r.Table, "dst", r.Dst, "type", routeType(r), "dev", r.Dev, "via", r.Via, "onlink", r.OnLink)
			if err := k.DeleteRoute(r); err != nil {
				return err
			}
		}
	}
	return nil
}

func conflictingRouteRemovals(gp planner.GroupPlan) []rtnl.Route {
	adds := routeIdentitySet(gp.RoutesToAdd)
	var out []rtnl.Route
	for _, r := range gp.RoutesToRemove {
		if adds[routeIdentity(r)] {
			out = append(out, r)
		}
	}
	return out
}

func routeIdentitySet(routes []rtnl.Route) map[string]bool {
	out := make(map[string]bool, len(routes))
	for _, r := range routes {
		out[routeIdentity(r)] = true
	}
	return out
}

func routeIdentity(r rtnl.Route) string {
	return r.Dst.String()
}

func routeType(r rtnl.Route) string {
	if r.Type == rtnl.RouteTypeThrow {
		return "throw"
	}
	return "unicast"
}
