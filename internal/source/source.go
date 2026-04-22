package source

import (
	"context"
	"log/slog"
	"net/netip"
	"sort"
)

type FetchResult struct {
	Group     string
	Type      string
	Prefixes  []netip.Prefix
	FromCache bool
}

type Provider interface {
	Fetch(ctx context.Context) ([]netip.Prefix, error)
	Type() string
}

func Normalize(prefixes []netip.Prefix, aggregate bool, log *slog.Logger) []netip.Prefix {
	seen := make(map[netip.Prefix]struct{}, len(prefixes))
	var valid []netip.Prefix
	for _, p := range prefixes {
		if !p.IsValid() {
			continue
		}
		p = p.Masked()
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		valid = append(valid, p)
	}
	sortPrefixes(valid)
	before := len(valid)
	if aggregate {
		valid = Aggregate(valid)
	}
	if log != nil {
		log.Info("normalized prefixes", "before", len(prefixes), "deduplicated", before, "after", len(valid), "aggregation", aggregate)
	}
	return valid
}

func FilterFamily(prefixes []netip.Prefix, family string) []netip.Prefix {
	if family == "" || family == "dual" {
		return prefixes
	}
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if family == "ipv4" && p.Addr().Is4() {
			out = append(out, p)
		}
		if family == "ipv6" && p.Addr().Is6() {
			out = append(out, p)
		}
	}
	return out
}

func sortPrefixes(prefixes []netip.Prefix) {
	sort.Slice(prefixes, func(i, j int) bool {
		a, b := prefixes[i], prefixes[j]
		if a.Addr().Less(b.Addr()) {
			return true
		}
		if b.Addr().Less(a.Addr()) {
			return false
		}
		return a.Bits() < b.Bits()
	})
}
