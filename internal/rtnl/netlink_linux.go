//go:build linux

package rtnl

import (
	"errors"
	"net"
	"net/netip"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type NetlinkKernel struct{}

func (NetlinkKernel) LinkIndexByName(name string) (int, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return 0, err
	}
	return l.Attrs().Index, nil
}

func (NetlinkKernel) ListRoutes(table, family, protocol int) ([]Route, error) {
	filter := &netlink.Route{Table: table, Protocol: netlink.RouteProtocol(protocol)}
	nlfam := netlink.FAMILY_V4
	if family == 6 {
		nlfam = netlink.FAMILY_V6
	}
	nlrs, err := netlink.RouteListFiltered(nlfam, filter, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_PROTOCOL)
	if err != nil {
		return nil, err
	}
	var out []Route
	for _, r := range nlrs {
		if r.Dst == nil {
			continue
		}
		p, ok := ipNetToPrefix(r.Dst)
		if !ok {
			continue
		}
		rr := Route{Table: r.Table, Protocol: int(r.Protocol), LinkIndex: r.LinkIndex, Dst: p, OnLink: r.Flags&int(netlink.FLAG_ONLINK) != 0}
		if r.Type == unix.RTN_THROW {
			rr.Type = RouteTypeThrow
		}
		if r.Gw != nil {
			if addr, ok := netip.AddrFromSlice(r.Gw); ok {
				rr.Via = addr.Unmap()
			}
		}
		out = append(out, rr)
	}
	return out, nil
}

func (NetlinkKernel) AddRoute(r Route) error {
	nlr := netlink.Route{Table: r.Table, Protocol: netlink.RouteProtocol(r.Protocol), LinkIndex: r.LinkIndex, Dst: prefixToIPNet(r.Dst)}
	if r.Type == RouteTypeThrow {
		nlr.Type = unix.RTN_THROW
		nlr.LinkIndex = 0
		return ignoreExists(netlink.RouteAdd(&nlr))
	}
	if r.Via.IsValid() {
		nlr.Gw = net.IP(r.Via.AsSlice())
	}
	if r.OnLink {
		nlr.Flags |= int(netlink.FLAG_ONLINK)
	}
	return ignoreExists(netlink.RouteAdd(&nlr))
}

func (NetlinkKernel) DeleteRoute(r Route) error {
	nlr := netlink.Route{Table: r.Table, Protocol: netlink.RouteProtocol(r.Protocol), LinkIndex: r.LinkIndex, Dst: prefixToIPNet(r.Dst)}
	if r.Type == RouteTypeThrow {
		nlr.Type = unix.RTN_THROW
		nlr.LinkIndex = 0
		return ignoreNotFound(netlink.RouteDel(&nlr))
	}
	if r.Via.IsValid() {
		nlr.Gw = net.IP(r.Via.AsSlice())
	}
	if r.OnLink {
		nlr.Flags |= int(netlink.FLAG_ONLINK)
	}
	return ignoreNotFound(netlink.RouteDel(&nlr))
}

func (NetlinkKernel) ListRules(family int) ([]Rule, error) {
	nlfam := netlink.FAMILY_V4
	if family == 6 {
		nlfam = netlink.FAMILY_V6
	}
	nlrs, err := netlink.RuleList(nlfam)
	if err != nil {
		return nil, err
	}
	var out []Rule
	for _, r := range nlrs {
		rr := Rule{Priority: r.Priority, Table: r.Table, FWMark: r.Mark}
		if r.Mask != nil {
			rr.HasFWMark = true
			rr.Mask = *r.Mask
		}
		if r.Src != nil {
			if p, ok := ipNetToPrefix(r.Src); ok {
				rr.From = p
			}
		}
		out = append(out, rr)
	}
	return out, nil
}

func (NetlinkKernel) AddRule(r Rule) error {
	nlr := netlink.NewRule()
	nlr.Priority = r.Priority
	nlr.Table = r.Table
	if r.HasFWMark {
		nlr.Mark = r.FWMark
		nlr.Mask = &r.Mask
	}
	if r.From.IsValid() {
		nlr.Src = prefixToIPNet(r.From)
	}
	return ignoreExists(netlink.RuleAdd(nlr))
}

func (NetlinkKernel) DeleteRule(r Rule) error {
	nlr := netlink.NewRule()
	nlr.Priority = r.Priority
	nlr.Table = r.Table
	if r.HasFWMark {
		nlr.Mark = r.FWMark
		nlr.Mask = &r.Mask
	}
	if r.From.IsValid() {
		nlr.Src = prefixToIPNet(r.From)
	}
	return ignoreNotFound(netlink.RuleDel(nlr))
}

func prefixToIPNet(p netip.Prefix) *net.IPNet {
	bits := 32
	if p.Addr().Is6() {
		bits = 128
	}
	return &net.IPNet{IP: net.IP(p.Masked().Addr().AsSlice()), Mask: net.CIDRMask(p.Bits(), bits)}
}

func ipNetToPrefix(n *net.IPNet) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := n.Mask.Size()
	return netip.PrefixFrom(addr.Unmap(), ones).Masked(), true
}

func ignoreExists(err error) error {
	if errors.Is(err, syscall.EEXIST) {
		return nil
	}
	return err
}

func ignoreNotFound(err error) error {
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}
