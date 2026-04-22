package source

import "net/netip"

func Aggregate(prefixes []netip.Prefix) []netip.Prefix {
	if len(prefixes) < 2 {
		return prefixes
	}
	current := removeCovered(prefixes)
	for {
		next, changed := mergeOnePass(current)
		next = removeCovered(next)
		if !changed && len(next) == len(current) {
			return next
		}
		current = next
	}
}

func removeCovered(prefixes []netip.Prefix) []netip.Prefix {
	sortPrefixes(prefixes)
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		covered := false
		for _, kept := range out {
			if kept.Addr().Is4() == p.Addr().Is4() && kept.Bits() <= p.Bits() && kept.Contains(p.Addr()) {
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

func mergeOnePass(prefixes []netip.Prefix) ([]netip.Prefix, bool) {
	sortPrefixes(prefixes)
	out := make([]netip.Prefix, 0, len(prefixes))
	changed := false
	for i := 0; i < len(prefixes); i++ {
		if i+1 < len(prefixes) {
			a, b := prefixes[i], prefixes[i+1]
			if parent, ok := mergePair(a, b); ok {
				out = append(out, parent)
				changed = true
				i++
				continue
			}
		}
		out = append(out, prefixes[i])
	}
	return out, changed
}

func mergePair(a, b netip.Prefix) (netip.Prefix, bool) {
	if a.Addr().Is4() != b.Addr().Is4() || a.Bits() != b.Bits() || a.Bits() == 0 {
		return netip.Prefix{}, false
	}
	parent := netip.PrefixFrom(a.Addr(), a.Bits()-1).Masked()
	if parent.Contains(a.Addr()) && parent.Contains(b.Addr()) {
		pa := netip.PrefixFrom(a.Addr(), a.Bits()).Masked()
		pb := netip.PrefixFrom(b.Addr(), b.Bits()).Masked()
		if pa != pb && netip.PrefixFrom(b.Addr(), b.Bits()-1).Masked() == parent {
			return parent, true
		}
	}
	return netip.Prefix{}, false
}
