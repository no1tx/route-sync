//go:build !linux

package rtnl

import "errors"

type NetlinkKernel struct{}

var ErrUnsupported = errors.New("netlink reconciliation is supported on linux only")

func (NetlinkKernel) LinkIndexByName(string) (int, error)       { return 0, ErrUnsupported }
func (NetlinkKernel) ListRoutes(int, int, int) ([]Route, error) { return nil, ErrUnsupported }
func (NetlinkKernel) AddRoute(Route) error                      { return ErrUnsupported }
func (NetlinkKernel) DeleteRoute(Route) error                   { return ErrUnsupported }
func (NetlinkKernel) ListRules(int) ([]Rule, error)             { return nil, ErrUnsupported }
func (NetlinkKernel) AddRule(Rule) error                        { return ErrUnsupported }
func (NetlinkKernel) DeleteRule(Rule) error                     { return ErrUnsupported }
