package state

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

type GroupState struct {
	Group        string    `json:"group"`
	SourceType   string    `json:"source_type"`
	Prefixes     []string  `json:"prefixes"`
	FetchedAt    time.Time `json:"fetched_at"`
	FromFallback bool      `json:"from_fallback,omitempty"`
}

type Store struct{ Dir string }

func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) SaveGroup(group, sourceType string, prefixes []netip.Prefix) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	st := GroupState{Group: group, SourceType: sourceType, FetchedAt: time.Now()}
	for _, p := range prefixes {
		st.Prefixes = append(st.Prefixes, p.String())
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(group), b, 0o644)
}

func (s *Store) LoadGroup(group string) ([]netip.Prefix, GroupState, error) {
	b, err := os.ReadFile(s.path(group))
	if err != nil {
		return nil, GroupState{}, err
	}
	var st GroupState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, GroupState{}, err
	}
	var prefixes []netip.Prefix
	for _, raw := range st.Prefixes {
		p, err := netip.ParsePrefix(raw)
		if err == nil {
			prefixes = append(prefixes, p.Masked())
		}
	}
	return prefixes, st, nil
}

func (s *Store) path(group string) string {
	return filepath.Join(s.Dir, group+".json")
}
