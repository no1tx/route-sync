package rules

import (
	"testing"

	"route-sync/internal/rtnl"
)

type fakeKernel struct{ rules []rtnl.Rule }

func (f fakeKernel) LinkIndexByName(string) (int, error)            { return 0, nil }
func (f fakeKernel) ListRoutes(int, int, int) ([]rtnl.Route, error) { return nil, nil }
func (f fakeKernel) AddRoute(rtnl.Route) error                      { return nil }
func (f fakeKernel) DeleteRoute(rtnl.Route) error                   { return nil }
func (f fakeKernel) ListRules(int) ([]rtnl.Rule, error)             { return f.rules, nil }
func (f fakeKernel) AddRule(rtnl.Rule) error                        { return nil }
func (f fakeKernel) DeleteRule(rtnl.Rule) error                     { return nil }

func TestCurrentOwnedFiltersPriorityRangeAndTable(t *testing.T) {
	got, err := CurrentOwned(fakeKernel{rules: []rtnl.Rule{
		{Priority: 1000, Table: 200},
		{Priority: 1000, Table: 999},
		{Priority: 9999, Table: 200},
	}}, 200, 1000, 1000, 10, "ipv4")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Table != 200 {
		t.Fatalf("got %+v", got)
	}
}
