package ripe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const endpoint = "https://stat.ripe.net/data/country-resource-list/data.json?resource=%s&v4_format=prefix"

type Provider struct {
	Country string
	Client  *http.Client
}

func New(country string, timeout time.Duration) *Provider {
	return &Provider{Country: strings.ToUpper(country), Client: &http.Client{Timeout: timeout}}
}

func (p *Provider) Type() string { return "ripe_country" }

func (p *Provider) Fetch(ctx context.Context) ([]netip.Prefix, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(endpoint, p.Country), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ripe stat returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return Parse(body)
}

func Parse(body []byte) ([]netip.Prefix, error) {
	var doc struct {
		Data struct {
			Resources struct {
				IPv4 []string `json:"ipv4"`
				IPv6 []string `json:"ipv6"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var prefixes []netip.Prefix
	for _, raw := range append(doc.Data.Resources.IPv4, doc.Data.Resources.IPv6...) {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		prefixes = append(prefixes, p.Masked())
	}
	return prefixes, nil
}
