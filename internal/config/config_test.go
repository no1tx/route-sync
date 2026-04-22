package config

import (
	"path/filepath"
	"testing"
)

func TestValidateSpecialPriority(t *testing.T) {
	v := true
	c := Config{
		Global:       GlobalConfig{RouteProtocol: 99, RulePriorityBase: 1000, RulePriorityStep: 10, LogFormat: "text"},
		Defaults:     Defaults{EnableRUBuiltinSource: &v},
		Routing:      Routing{RUDefault: RouteGroup{Enabled: true, Name: "ru_default", Source: SourceConfig{Type: "ripe_country", Country: "RU"}, Target: TargetConfig{Table: 100, Dev: "eth0", Family: "dual"}, Rule: RuleConfig{Enabled: true, Priority: 2000, Table: 100}}},
		SpecialZones: []RouteGroup{{Name: "telegram", Enabled: true, Source: SourceConfig{Type: "txt", URL: "file:///tmp/x"}, Target: TargetConfig{Table: 200, Dev: "wg0", Family: "ipv4"}, Rule: RuleConfig{Enabled: true, Priority: 1000, Table: 200}}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.SpecialZones[0].Rule.Priority = 3000
	if err := c.Validate(); err == nil {
		t.Fatal("expected priority validation error")
	}
}

func TestValidateDualStackViaRequiresFamilySpecificGateway(t *testing.T) {
	v := true
	c := Config{
		Global:   GlobalConfig{RouteProtocol: 99, RulePriorityBase: 1000, RulePriorityStep: 10, LogFormat: "text"},
		Defaults: Defaults{EnableRUBuiltinSource: &v},
		Routing:  Routing{RUDefault: RouteGroup{Enabled: true, Name: "ru_default", Source: SourceConfig{Type: "ripe_country", Country: "RU"}, Target: TargetConfig{Table: 100, Dev: "eth0", Via: "192.0.2.1", Family: "dual"}, Rule: RuleConfig{Enabled: true, Priority: 2000, Table: 100}}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected ambiguous dual-stack via validation error")
	}
	c.Routing.RUDefault.Target.Via = ""
	c.Routing.RUDefault.Target.Via4 = "192.0.2.1"
	c.Routing.RUDefault.Target.Via6 = "2001:db8::1"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateViaFamily(t *testing.T) {
	v := true
	c := Config{
		Global:   GlobalConfig{RouteProtocol: 99, RulePriorityBase: 1000, RulePriorityStep: 10, LogFormat: "text"},
		Defaults: Defaults{EnableRUBuiltinSource: &v},
		Routing:  Routing{RUDefault: RouteGroup{Enabled: true, Name: "ru_default", Source: SourceConfig{Type: "ripe_country", Country: "RU"}, Target: TargetConfig{Table: 100, Dev: "eth0", Via4: "2001:db8::1", Family: "ipv4"}, Rule: RuleConfig{Enabled: true, Priority: 2000, Table: 100}}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid via4 family")
	}
}

func TestExampleConfigsValidate(t *testing.T) {
	for _, name := range []string{
		"minimal-ru-default.yaml",
		"ru-plus-telegram.yaml",
		"ru-plus-multiple-zones.yaml",
		"reverse-ru-local-rest-tunnel.yaml",
		"txt-only.yaml",
		"local-dev.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(filepath.Join("..", "..", "examples", "configs", name))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
