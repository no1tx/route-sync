package rules

import (
	"log/slog"

	"route-sync/internal/planner"
	"route-sync/internal/rtnl"
)

func CurrentOwned(k rtnl.Kernel, table int, priority, base, step int, family string) ([]rtnl.Rule, error) {
	low, high := base, base+(step*1000)
	var out []rtnl.Rule
	addFamily := func(f int) error {
		rs, err := k.ListRules(f)
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.Table == table && r.Priority == priority && r.Priority >= low && r.Priority < high {
				out = append(out, r)
			}
		}
		return nil
	}
	if family == "dual" || family == "" || family == "ipv4" {
		if err := addFamily(4); err != nil {
			return nil, err
		}
	}
	if family == "dual" || family == "" || family == "ipv6" {
		if err := addFamily(6); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func Apply(k rtnl.Kernel, plans []planner.GroupPlan, log *slog.Logger) error {
	for _, gp := range plans {
		for _, r := range gp.RulesToAdd {
			log.Info("adding owned rule", "group", gp.Group, "priority", r.Priority, "table", r.Table, "has_fwmark", r.HasFWMark, "fwmark", r.FWMark, "mask", r.Mask, "from", r.From)
			if err := k.AddRule(r); err != nil {
				return err
			}
		}
		for _, r := range gp.RulesToRemove {
			log.Info("removing obsolete owned rule", "group", gp.Group, "priority", r.Priority, "table", r.Table, "has_fwmark", r.HasFWMark, "fwmark", r.FWMark, "mask", r.Mask, "from", r.From)
			if err := k.DeleteRule(r); err != nil {
				return err
			}
		}
	}
	return nil
}
