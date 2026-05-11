package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"time"

	"route-sync/internal/config"
	"route-sync/internal/health"
	"route-sync/internal/logging"
	"route-sync/internal/metrics"
	"route-sync/internal/planner"
	"route-sync/internal/reconcile/routes"
	"route-sync/internal/reconcile/rules"
	"route-sync/internal/rtnl"
	"route-sync/internal/signals"
	"route-sync/internal/source"
	"route-sync/internal/source/ripe"
	txtsrc "route-sync/internal/source/txt"
	"route-sync/internal/state"
)

type Options struct {
	ConfigPath        string
	DryRun            bool
	DisableRUDefault  bool
	LogFormat         string
	Interval          time.Duration
	MetricsListen     string
	CleanupOnShutdown bool
}

func Run(ctx context.Context, args []string, version string) error {
	if len(args) == 0 {
		return usage()
	}
	cmd := args[0]
	if cmd == "version" {
		fmt.Println(version)
		return nil
	}
	opts, err := parseFlags(cmd, args[1:])
	if err != nil {
		return err
	}
	if opts.ConfigPath == "" {
		return errors.New("--config is required")
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		return err
	}
	log := logging.New(selectLogFormat(opts, cfg))
	reg := metrics.New()
	kernel := rtnl.NetlinkKernel{}
	switch cmd {
	case "check":
		plan, err := BuildPlan(ctx, cfg, kernel, log, reg)
		if err != nil {
			return err
		}
		fmt.Print(plan.String())
		return nil
	case "apply":
		return reconcileOnce(ctx, cfg, kernel, log, reg, opts.DryRun)
	case "cleanup":
		return cleanupOwned(cfg, kernel, log, reg, opts.DryRun)
	case "daemon":
		return runDaemon(ctx, opts, cfg, kernel, log, reg)
	default:
		return usage()
	}
}

func parseFlags(cmd string, args []string) (Options, error) {
	var opts Options
	fs := flag.NewFlagSet("route-sync "+cmd, flag.ContinueOnError)
	fs.StringVar(&opts.ConfigPath, "config", "", "config path")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print plan without applying")
	fs.BoolVar(&opts.DisableRUDefault, "disable-ru-default", false, "disable built-in RU default route-set")
	fs.StringVar(&opts.LogFormat, "log-format", "", "text or json")
	fs.DurationVar(&opts.Interval, "interval", 0, "daemon refresh interval override")
	fs.StringVar(&opts.MetricsListen, "metrics-listen", "", "metrics listen address override")
	fs.BoolVar(&opts.CleanupOnShutdown, "cleanup-on-shutdown", false, "remove owned routes and rules when daemon exits")
	return opts, fs.Parse(args)
}

func usage() error {
	return errors.New("usage: route-sync {check|apply|cleanup|daemon|version} --config /etc/route-sync.yaml [--dry-run] [--disable-ru-default] [--log-format text|json] [--interval 1h] [--metrics-listen 127.0.0.1:9108] [--cleanup-on-shutdown]")
}

func loadConfig(opts Options) (*config.Config, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	if opts.DisableRUDefault {
		cfg.DisableRUDefault()
	}
	if opts.LogFormat != "" {
		cfg.Global.LogFormat = opts.LogFormat
	}
	if opts.Interval != 0 {
		cfg.Global.RefreshInterval.Duration = opts.Interval
	}
	if opts.MetricsListen != "" {
		cfg.Global.MetricsListen = opts.MetricsListen
	}
	if opts.CleanupOnShutdown {
		cfg.Global.CleanupOnShutdown = true
	}
	return cfg, cfg.Validate()
}

func selectLogFormat(opts Options, cfg *config.Config) string {
	if opts.LogFormat != "" {
		return opts.LogFormat
	}
	return cfg.Global.LogFormat
}

func BuildPlan(ctx context.Context, cfg *config.Config, kernel rtnl.Kernel, log *slog.Logger, reg *metrics.Registry) (planner.Plan, error) {
	st := state.New(cfg.Global.StateDir)
	pinger := health.ExecPinger{}
	var inputs []planner.InputGroup
	for _, g := range cfg.Groups() {
		groupLog := log.With("group", g.Name, "source", g.Source.Type)
		provider, err := providerFor(g, cfg, groupLog)
		if err != nil {
			return planner.Plan{}, err
		}
		raw, err := provider.Fetch(ctx)
		fromFallback := false
		if err != nil {
			reg.Inc("source_fetch_failure_total")
			groupLog.Error("source fetch failed", "error", err)
			raw, _, err = st.LoadGroup(g.Name)
			if err != nil {
				groupLog.Error("no last known good state available; skipping group", "error", err)
				continue
			}
			fromFallback = true
			groupLog.Warn("using last known good source state")
		} else {
			reg.Inc("source_fetch_success_total")
			groupLog.Info("source fetch succeeded", "prefixes", len(raw))
		}
		prefixes := source.Normalize(raw, cfg.Global.EnablePrefixAggregation, groupLog)
		if !fromFallback {
			if err := st.SaveGroup(g.Name, g.Source.Type, prefixes); err != nil {
				groupLog.Warn("failed to persist last known good source state", "error", err)
			}
		}
		linkIndex := 0
		if g.Target.Dev != "" {
			linkIndex, err = kernel.LinkIndexByName(g.Target.Dev)
			if err != nil {
				return planner.Plan{}, fmt.Errorf("%s: resolve target dev %s: %w", g.Name, g.Target.Dev, err)
			}
		}
		defaultLinkIndex := 0
		if g.Target.Default != nil && g.Target.Default.Dev != "" {
			defaultLinkIndex, err = kernel.LinkIndexByName(g.Target.Default.Dev)
			if err != nil {
				return planner.Plan{}, fmt.Errorf("%s: resolve target default dev %s: %w", g.Name, g.Target.Default.Dev, err)
			}
		}
		targetHops, err := resolveTargetHops(ctx, g.Name, "target", g.Target.Family, g.Target, kernel, pinger, reg, groupLog)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: resolve target gateways: %w", g.Name, err)
		}
		defaultHops, err := resolveDefaultHops(ctx, g.Name, g.Target.Family, g.Target.Default, kernel, pinger, reg, groupLog)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: resolve default gateways: %w", g.Name, err)
		}
		currentRoutes, err := routes.CurrentOwned(kernel, g.Target.Table, cfg.Global.RouteProtocol, g.Target.Family)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: list owned routes: %w", g.Name, err)
		}
		currentRules, err := rules.CurrentOwned(kernel, g.Target.Table, g.Rule.Priority, cfg.Global.RulePriorityBase, cfg.Global.RulePriorityStep, g.Target.Family)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: list owned rules: %w", g.Name, err)
		}
		throwPrefixes, coveredLocalAddrs, err := exclusionInputs(g.Target, groupLog)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: build exclusion inputs: %w", g.Name, err)
		}
		inputs = append(inputs, planner.InputGroup{Config: g, Prefixes: prefixes, ThrowPrefixes: throwPrefixes, CoveredLocalAddrs: coveredLocalAddrs, SourceType: provider.Type(), FromFallback: fromFallback, LinkIndex: linkIndex, DefaultLinkIndex: defaultLinkIndex, TargetHops: targetHops, DefaultHops: defaultHops, CurrentRoutes: currentRoutes, CurrentRules: currentRules})
	}
	plan := planner.Build(cfg.Global.RouteProtocol, inputs)
	for _, gp := range plan.Groups {
		reg.SetGroupGauge("managed_prefixes", gp.Group, float64(gp.DesiredPrefixCount))
		reg.SetGroupGauge("managed_routes", gp.Group, float64(gp.CurrentManagedRouteCount+len(gp.RoutesToAdd)-len(gp.RoutesToRemove)))
		reg.SetGroupGauge("managed_rules", gp.Group, float64(gp.CurrentManagedRuleCount+len(gp.RulesToAdd)-len(gp.RulesToRemove)))
		log.Info("reconciliation plan summary", "group", gp.Group, "route_add", len(gp.RoutesToAdd), "route_remove", len(gp.RoutesToRemove), "rule_add", len(gp.RulesToAdd), "rule_remove", len(gp.RulesToRemove))
	}
	return plan, nil
}

func exclusionInputs(target config.TargetConfig, log *slog.Logger) ([]netip.Prefix, []netip.Addr, error) {
	var throwPrefixes []netip.Prefix
	for _, raw := range target.ExcludePrefixes {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, nil, err
		}
		throwPrefixes = append(throwPrefixes, p.Masked())
	}
	throwPrefixes = source.Normalize(source.FilterFamily(throwPrefixes, target.Family), false, nil)
	if !target.ExcludeLocalIPs {
		return throwPrefixes, nil, nil
	}
	local, err := localNonPrivatePrefixes()
	if err != nil {
		return nil, nil, err
	}
	localAddrs := prefixAddrs(source.FilterFamily(local, target.Family))
	log.Info("route exclusion inputs prepared", "throw_prefixes", len(throwPrefixes), "covered_local_addrs", len(localAddrs), "exclude_local_ips", target.ExcludeLocalIPs)
	return throwPrefixes, localAddrs, nil
}

func resolveTargetHops(ctx context.Context, group, scope, family string, target config.TargetConfig, kernel rtnl.Kernel, pinger health.Pinger, reg *metrics.Registry, log *slog.Logger) ([]health.ResolvedHop, error) {
	candidates := gatewaysFromTarget(target)
	return resolveCandidates(ctx, group, scope, family, candidates, kernel, pinger, reg, log)
}

func resolveDefaultHops(ctx context.Context, group, family string, next *config.NextHopConfig, kernel rtnl.Kernel, pinger health.Pinger, reg *metrics.Registry, log *slog.Logger) ([]health.ResolvedHop, error) {
	if next == nil {
		return nil, nil
	}
	candidates := gatewaysFromNextHop(*next)
	return resolveCandidates(ctx, group, "default", family, candidates, kernel, pinger, reg, log)
}

func resolveCandidates(ctx context.Context, group, scope, family string, candidates []health.Candidate, kernel rtnl.Kernel, pinger health.Pinger, reg *metrics.Registry, log *slog.Logger) ([]health.ResolvedHop, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	var out []health.ResolvedHop
	if family == "" || family == "dual" || family == "ipv4" {
		v4, err := health.Select(ctx, "ipv4", group+"."+scope, filterCandidatesByFamily(candidates, 4), kernel.LinkIndexByName, pinger, reg, log)
		if err != nil {
			return nil, err
		}
		out = append(out, v4...)
	}
	if family == "dual" || family == "ipv6" {
		v6, err := health.Select(ctx, "ipv6", group+"."+scope, filterCandidatesByFamily(candidates, 6), kernel.LinkIndexByName, pinger, reg, log)
		if err != nil {
			return nil, err
		}
		out = append(out, v6...)
	}
	return out, nil
}

func gatewaysFromTarget(target config.TargetConfig) []health.Candidate {
	if len(target.Gateways) == 0 {
		return gatewaysFromNextHop(config.NextHopConfig{Dev: target.Dev, Via: target.Via, Via4: target.Via4, Via6: target.Via6, OnLink: target.OnLink, HealthCheck: target.HealthCheck})
	}
	out := make([]health.Candidate, 0, len(target.Gateways)*2)
	for i, gw := range target.Gateways {
		out = append(out, candidatesFromGateway(gw, target.Dev, target.OnLink, target.HealthCheck, i)...)
	}
	return out
}

func gatewaysFromNextHop(next config.NextHopConfig) []health.Candidate {
	if len(next.Gateways) == 0 {
		return candidatesFromHop("primary", next.Dev, next.OnLink, next.HealthCheck, next.Via, next.Via4, next.Via6)
	}
	out := make([]health.Candidate, 0, len(next.Gateways)*2)
	for i, gw := range next.Gateways {
		out = append(out, candidatesFromGateway(gw, next.Dev, next.OnLink, next.HealthCheck, i)...)
	}
	return out
}

func candidatesFromGateway(gw config.GatewayConfig, fallbackDev string, fallbackOnLink bool, parentHC config.HealthCheck, index int) []health.Candidate {
	name := gw.Name
	if name == "" {
		name = fmt.Sprintf("gw%d", index+1)
	}
	return candidatesFromHop(name, firstNonEmpty(gw.Dev, fallbackDev), gw.OnLink || fallbackOnLink, mergeHealthChecks(parentHC, gw.HealthCheck), gw.Via, gw.Via4, gw.Via6)
}

func mergeHealthChecks(parent, child config.HealthCheck) config.HealthCheck {
	out := parent
	if len(child.Targets) > 0 {
		out.Targets = child.Targets
	}
	if child.Timeout.Duration != 0 {
		out.Timeout = child.Timeout
	}
	return out
}

func filterCandidatesByFamily(candidates []health.Candidate, family int) []health.Candidate {
	out := make([]health.Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.Via.IsValid() {
			continue
		}
		if family == 4 && c.Via.Is4() {
			out = append(out, c)
		}
		if family == 6 && c.Via.Is6() {
			out = append(out, c)
		}
	}
	return out
}

func candidatesFromHop(name, dev string, onLink bool, hc config.HealthCheck, any, v4, v6 string) []health.Candidate {
	var out []health.Candidate
	if v4 != "" {
		if addr, err := netip.ParseAddr(v4); err == nil {
			out = append(out, health.Candidate{Name: name + "-ipv4", Dev: dev, OnLink: onLink, Via: addr, HealthCheck: hc})
		}
	}
	if v6 != "" {
		if addr, err := netip.ParseAddr(v6); err == nil {
			out = append(out, health.Candidate{Name: name + "-ipv6", Dev: dev, OnLink: onLink, Via: addr, HealthCheck: hc})
		}
	}
	if len(out) > 0 {
		return out
	}
	if any != "" {
		if addr, err := netip.ParseAddr(any); err == nil {
			out = append(out, health.Candidate{Name: name, Dev: dev, OnLink: onLink, Via: addr, HealthCheck: hc})
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func localNonPrivatePrefixes() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, a := range addrs {
		prefix, ok := addrPrefix(a)
		if !ok {
			continue
		}
		addr := prefix.Addr()
		if !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || !addr.IsGlobalUnicast() {
			continue
		}
		if addr.Is4() {
			out = append(out, netip.PrefixFrom(addr, 32))
		} else {
			out = append(out, netip.PrefixFrom(addr, 128))
		}
	}
	return out, nil
}

func prefixAddrs(prefixes []netip.Prefix) []netip.Addr {
	out := make([]netip.Addr, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.Addr())
	}
	return out
}

func addrPrefix(a net.Addr) (netip.Prefix, bool) {
	switch v := a.(type) {
	case *net.IPNet:
		addr, ok := netip.AddrFromSlice(v.IP)
		if !ok {
			return netip.Prefix{}, false
		}
		ones, _ := v.Mask.Size()
		return netip.PrefixFrom(addr.Unmap(), ones).Masked(), true
	default:
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			return netip.Prefix{}, false
		}
		return p.Masked(), true
	}
}

func providerFor(g config.RouteGroup, cfg *config.Config, log *slog.Logger) (source.Provider, error) {
	switch g.Source.Type {
	case "ripe_country":
		return ripe.New(g.Source.Country, cfg.Global.HTTPTimeout.Duration), nil
	case "txt":
		return txtsrc.New(g.Source.URL, cfg.Global.HTTPTimeout.Duration, log), nil
	default:
		return nil, fmt.Errorf("unsupported source type %q", g.Source.Type)
	}
}

func reconcileOnce(ctx context.Context, cfg *config.Config, kernel rtnl.Kernel, log *slog.Logger, reg *metrics.Registry, dryRun bool) error {
	plan, err := BuildPlan(ctx, cfg, kernel, log, reg)
	if err != nil {
		reg.Inc("failed_reconcile_total")
		return err
	}
	fmt.Print(plan.String())
	if dryRun {
		return nil
	}
	if err := rules.Apply(kernel, plan.Groups, log); err != nil {
		reg.Inc("failed_reconcile_total")
		return err
	}
	if err := routes.Apply(kernel, plan.Groups, log); err != nil {
		reg.Inc("failed_reconcile_total")
		return err
	}
	reg.Inc("successful_reconcile_total")
	reg.Set("last_success_timestamp", float64(time.Now().Unix()))
	return nil
}

func BuildCleanupPlan(cfg *config.Config, kernel rtnl.Kernel) (planner.Plan, error) {
	var plan planner.Plan
	for _, g := range cfg.Groups() {
		currentRoutes, err := routes.CurrentOwned(kernel, g.Target.Table, cfg.Global.RouteProtocol, g.Target.Family)
		if err != nil {
			return planner.Plan{}, fmt.Errorf("%s: list owned routes for cleanup: %w", g.Name, err)
		}
		var currentRules []rtnl.Rule
		if g.Rule.Enabled {
			currentRules, err = rules.CurrentOwned(kernel, g.Target.Table, g.Rule.Priority, cfg.Global.RulePriorityBase, cfg.Global.RulePriorityStep, g.Target.Family)
			if err != nil {
				return planner.Plan{}, fmt.Errorf("%s: list owned rules for cleanup: %w", g.Name, err)
			}
		}
		plan.Groups = append(plan.Groups, planner.GroupPlan{
			Group:                    g.Name,
			SourceType:               g.Source.Type,
			TargetTable:              g.Target.Table,
			Family:                   g.Target.Family,
			CurrentManagedRouteCount: len(currentRoutes),
			CurrentManagedRuleCount:  len(currentRules),
			RoutesToRemove:           currentRoutes,
			RulesToRemove:            currentRules,
		})
	}
	return plan, nil
}

func cleanupOwned(cfg *config.Config, kernel rtnl.Kernel, log *slog.Logger, reg *metrics.Registry, dryRun bool) error {
	plan, err := BuildCleanupPlan(cfg, kernel)
	if err != nil {
		reg.Inc("failed_cleanup_total")
		return err
	}
	fmt.Print(plan.String())
	if dryRun {
		return nil
	}
	log.Info("cleanup started")
	if err := rules.Apply(kernel, plan.Groups, log); err != nil {
		reg.Inc("failed_cleanup_total")
		return err
	}
	if err := routes.Apply(kernel, plan.Groups, log); err != nil {
		reg.Inc("failed_cleanup_total")
		return err
	}
	reg.Inc("successful_cleanup_total")
	log.Info("cleanup completed")
	return nil
}

func runDaemon(ctx context.Context, opts Options, cfg *config.Config, kernel rtnl.Kernel, log *slog.Logger, reg *metrics.Registry) error {
	sig := signals.Notify(ctx)
	defer sig.Stop()
	metrics.Serve(sig.Context, cfg.Global.MetricsListen, reg, log)
	log.Info("daemon startup", "interval", cfg.Global.RefreshInterval.Duration, "health_check_interval", cfg.Global.HealthCheckInterval.Duration)
	refreshTicker := time.NewTicker(cfg.Global.RefreshInterval.Duration)
	healthTicker := time.NewTicker(cfg.Global.HealthCheckInterval.Duration)
	defer refreshTicker.Stop()
	defer healthTicker.Stop()
	active := cfg
	run := func(reason string) {
		log.Debug("reconcile triggered", "reason", reason)
		if err := reconcileOnce(sig.Context, active, kernel, log, reg, opts.DryRun); err != nil {
			log.Error("reconciliation failed", "error", err)
		}
	}
	run("startup")
	for {
		select {
		case <-sig.Context.Done():
			if active.Global.CleanupOnShutdown {
				log.Info("daemon shutdown cleanup enabled")
				if err := cleanupOwned(active, kernel, log, reg, opts.DryRun); err != nil {
					log.Error("shutdown cleanup failed", "error", err)
				}
			}
			log.Info("daemon shutdown")
			return nil
		case <-refreshTicker.C:
			run("refresh_interval")
		case <-healthTicker.C:
			run("health_check_interval")
		case <-sig.Reload:
			log.Info("SIGHUP received; reloading config")
			next, err := loadConfig(opts)
			if err != nil {
				log.Error("new config invalid; keeping last valid config", "error", err)
				continue
			}
			active = next
			refreshTicker.Reset(active.Global.RefreshInterval.Duration)
			healthTicker.Reset(active.Global.HealthCheckInterval.Duration)
			run("reload")
		}
	}
}

func ValidateReload(current *config.Config, opts Options) (*config.Config, error) {
	next, err := loadConfig(opts)
	if err != nil {
		return current, err
	}
	return next, nil
}

func init() {
	_ = os.ErrNotExist
}
