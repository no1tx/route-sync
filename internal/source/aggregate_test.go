package source

import (
	"net/netip"
	"testing"
)

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

func TestNormalizeDedupAndAggregate(t *testing.T) {
	got := Normalize([]netip.Prefix{
		mustPrefix("10.0.0.0/25"),
		mustPrefix("10.0.0.128/25"),
		mustPrefix("10.0.0.0/25"),
		mustPrefix("2001:db8::/33"),
		mustPrefix("2001:db8:8000::/33"),
	}, true, nil)
	want := map[netip.Prefix]bool{mustPrefix("10.0.0.0/24"): true, mustPrefix("2001:db8::/32"): true}
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("unexpected %v in %v", p, got)
		}
	}
}

func TestAggregateDoesNotBroadenUnrelated(t *testing.T) {
	got := Aggregate([]netip.Prefix{mustPrefix("10.0.0.0/25"), mustPrefix("10.0.1.128/25")})
	if len(got) != 2 {
		t.Fatalf("unexpected aggregation: %v", got)
	}
}
