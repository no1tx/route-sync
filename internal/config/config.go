package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	dd, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dd
	return nil
}

type Config struct {
	Global       GlobalConfig `yaml:"global"`
	Defaults     Defaults     `yaml:"defaults"`
	Routing      Routing      `yaml:"routing"`
	SpecialZones []RouteGroup `yaml:"special_zones"`
}

type GlobalConfig struct {
	RefreshInterval         Duration `yaml:"refresh_interval"`
	HTTPTimeout             Duration `yaml:"http_timeout"`
	StateDir                string   `yaml:"state_dir"`
	LogFormat               string   `yaml:"log_format"`
	MetricsListen           string   `yaml:"metrics_listen"`
	RouteProtocol           int      `yaml:"route_protocol"`
	RulePriorityBase        int      `yaml:"rule_priority_base"`
	RulePriorityStep        int      `yaml:"rule_priority_step"`
	EnablePrefixAggregation bool     `yaml:"enable_prefix_aggregation"`
	CleanupOnShutdown       bool     `yaml:"cleanup_on_shutdown"`
}

type Defaults struct {
	EnableRUBuiltinSource *bool `yaml:"enable_ru_builtin_source"`
}

type Routing struct {
	RUDefault RouteGroup `yaml:"ru_default"`
}

type RouteGroup struct {
	Name    string       `yaml:"name"`
	Enabled bool         `yaml:"enabled"`
	Source  SourceConfig `yaml:"source"`
	Target  TargetConfig `yaml:"target"`
	Rule    RuleConfig   `yaml:"rule"`
}

type SourceConfig struct {
	Type    string `yaml:"type"`
	Country string `yaml:"country"`
	URL     string `yaml:"url"`
}

type TargetConfig struct {
	Table           int            `yaml:"table"`
	Dev             string         `yaml:"dev"`
	Via             string         `yaml:"via"`
	Via4            string         `yaml:"via4"`
	Via6            string         `yaml:"via6"`
	OnLink          bool           `yaml:"onlink"`
	Family          string         `yaml:"family"`
	Default         *NextHopConfig `yaml:"default"`
	ExcludeLocalIPs bool           `yaml:"exclude_local_ips"`
	ExcludePrefixes []string       `yaml:"exclude_prefixes"`
}

type NextHopConfig struct {
	Dev    string `yaml:"dev"`
	Via    string `yaml:"via"`
	Via4   string `yaml:"via4"`
	Via6   string `yaml:"via6"`
	OnLink bool   `yaml:"onlink"`
}

type RuleConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Priority int    `yaml:"priority"`
	FWMark   uint32 `yaml:"fwmark"`
	Mask     uint32 `yaml:"mask"`
	Table    int    `yaml:"table"`
	From     string `yaml:"from"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	ApplyDefaults(&c)
	return &c, c.Validate()
}

func ApplyDefaults(c *Config) {
	if c.Global.RefreshInterval.Duration == 0 {
		c.Global.RefreshInterval.Duration = time.Hour
	}
	if c.Global.HTTPTimeout.Duration == 0 {
		c.Global.HTTPTimeout.Duration = 30 * time.Second
	}
	if c.Global.StateDir == "" {
		c.Global.StateDir = "/var/lib/route-sync"
	}
	if c.Global.LogFormat == "" {
		c.Global.LogFormat = "text"
	}
	if c.Global.MetricsListen == "" {
		c.Global.MetricsListen = "127.0.0.1:9108"
	}
	if c.Global.RouteProtocol == 0 {
		c.Global.RouteProtocol = 99
	}
	if c.Global.RulePriorityBase == 0 {
		c.Global.RulePriorityBase = 1000
	}
	if c.Global.RulePriorityStep == 0 {
		c.Global.RulePriorityStep = 10
	}
	if c.Defaults.EnableRUBuiltinSource == nil {
		v := true
		c.Defaults.EnableRUBuiltinSource = &v
	}
	if c.Routing.RUDefault.Name == "" {
		c.Routing.RUDefault.Name = "ru_default"
	}
	if c.Routing.RUDefault.Source.Type == "" {
		c.Routing.RUDefault.Source.Type = "ripe_country"
		c.Routing.RUDefault.Source.Country = "RU"
	}
}

func (c *Config) DisableRUDefault() {
	v := false
	c.Defaults.EnableRUBuiltinSource = &v
	c.Routing.RUDefault.Enabled = false
}

func (c *Config) Groups() []RouteGroup {
	var out []RouteGroup
	if c.Routing.RUDefault.Enabled && c.Defaults.EnableRUBuiltinSource != nil && *c.Defaults.EnableRUBuiltinSource {
		g := c.Routing.RUDefault
		g.Name = "ru_default"
		out = append(out, g)
	}
	for _, g := range c.SpecialZones {
		if g.Enabled {
			out = append(out, g)
		}
	}
	return out
}

func (c *Config) Validate() error {
	var errs []string
	if c.Global.LogFormat != "text" && c.Global.LogFormat != "json" {
		errs = append(errs, "global.log_format must be text or json")
	}
	if c.Global.RouteProtocol < 1 || c.Global.RouteProtocol > 255 {
		errs = append(errs, "global.route_protocol must be 1..255")
	}
	if c.Global.RulePriorityBase <= 0 || c.Global.RulePriorityStep <= 0 {
		errs = append(errs, "rule priority base and step must be positive")
	}
	for _, g := range c.Groups() {
		if err := validateGroup(g); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", g.Name, err))
		}
	}
	if c.Routing.RUDefault.Enabled && c.Defaults.EnableRUBuiltinSource != nil && *c.Defaults.EnableRUBuiltinSource {
		for _, z := range c.SpecialZones {
			if z.Enabled && z.Rule.Enabled && c.Routing.RUDefault.Rule.Enabled && z.Rule.Priority >= c.Routing.RUDefault.Rule.Priority {
				errs = append(errs, fmt.Sprintf("%s: special zone priority %d must be numerically lower than RU default priority %d", z.Name, z.Rule.Priority, c.Routing.RUDefault.Rule.Priority))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func validateGroup(g RouteGroup) error {
	var errs []string
	if g.Name == "" {
		errs = append(errs, "name is required")
	}
	switch g.Source.Type {
	case "ripe_country":
		if g.Source.Country == "" {
			errs = append(errs, "source.country is required")
		}
	case "txt":
		if g.Source.URL == "" {
			errs = append(errs, "source.url is required")
		}
	default:
		errs = append(errs, "source.type must be ripe_country or txt")
	}
	if g.Target.Table <= 0 {
		errs = append(errs, "target.table is required")
	}
	if g.Target.Dev == "" {
		errs = append(errs, "target.dev is required")
	}
	if g.Target.Family == "" {
		g.Target.Family = "dual"
	}
	if g.Target.Family != "dual" && g.Target.Family != "ipv4" && g.Target.Family != "ipv6" {
		errs = append(errs, "target.family must be dual, ipv4, or ipv6")
	}
	errs = append(errs, validateNextHop("target", g.Target.Family, NextHopConfig{Dev: g.Target.Dev, Via: g.Target.Via, Via4: g.Target.Via4, Via6: g.Target.Via6, OnLink: g.Target.OnLink}, false)...)
	if g.Target.Default != nil {
		errs = append(errs, validateNextHop("target.default", g.Target.Family, *g.Target.Default, true)...)
	}
	for _, raw := range g.Target.ExcludePrefixes {
		if _, err := netip.ParsePrefix(raw); err != nil {
			errs = append(errs, fmt.Sprintf("target.exclude_prefixes contains invalid CIDR %q", raw))
		}
	}
	if g.Rule.Enabled {
		if g.Rule.Priority <= 0 || g.Rule.Table <= 0 {
			errs = append(errs, "rule priority and table are required")
		}
		if g.Rule.Table != g.Target.Table {
			errs = append(errs, "rule.table must match target.table")
		}
		if g.Rule.From != "" {
			if _, err := netip.ParsePrefix(g.Rule.From); err != nil {
				errs = append(errs, "rule.from must be a CIDR prefix")
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func validateNextHop(path, family string, h NextHopConfig, requireDev bool) []string {
	var errs []string
	if requireDev && h.Dev == "" {
		errs = append(errs, path+".dev is required")
	}
	if h.Via != "" {
		addr, err := netip.ParseAddr(h.Via)
		if err != nil {
			errs = append(errs, path+".via must be an IP address")
		} else if family == "dual" && h.Via4 == "" && h.Via6 == "" {
			errs = append(errs, fmt.Sprintf("%s.via is ambiguous for family dual; use via4/via6 instead of %s", path, addr))
		}
	}
	if h.Via4 != "" {
		addr, err := netip.ParseAddr(h.Via4)
		if err != nil {
			errs = append(errs, path+".via4 must be an IP address")
		} else if !addr.Is4() {
			errs = append(errs, path+".via4 must be an IPv4 address")
		}
	}
	if h.Via6 != "" {
		addr, err := netip.ParseAddr(h.Via6)
		if err != nil {
			errs = append(errs, path+".via6 must be an IP address")
		} else if !addr.Is6() {
			errs = append(errs, path+".via6 must be an IPv6 address")
		}
	}
	return errs
}
