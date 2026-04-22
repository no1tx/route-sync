package rtnl

import "net/netip"

const (
	RouteTypeUnicast = iota
	RouteTypeThrow
)

type Route struct {
	Group     string
	Table     int
	Protocol  int
	Type      int
	Dev       string
	LinkIndex int
	Via       netip.Addr
	OnLink    bool
	Dst       netip.Prefix
}

type Rule struct {
	Group     string
	Priority  int
	Table     int
	HasFWMark bool
	FWMark    uint32
	Mask      uint32
	From      netip.Prefix
}

type Kernel interface {
	LinkIndexByName(name string) (int, error)
	ListRoutes(table, family, protocol int) ([]Route, error)
	AddRoute(Route) error
	DeleteRoute(Route) error
	ListRules(family int) ([]Rule, error)
	AddRule(Rule) error
	DeleteRule(Rule) error
}
