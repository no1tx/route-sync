package txt

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

type Provider struct {
	URL    string
	Client *http.Client
	Log    *slog.Logger
}

func New(rawurl string, timeout time.Duration, log *slog.Logger) *Provider {
	return &Provider{URL: rawurl, Client: &http.Client{Timeout: timeout}, Log: log}
}

func (p *Provider) Type() string { return "txt" }

func (p *Provider) Fetch(ctx context.Context) ([]netip.Prefix, error) {
	r, closeFn, err := p.open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return Parse(r, p.Log), nil
}

func (p *Provider) open(ctx context.Context) (io.Reader, func(), error) {
	u, err := url.Parse(p.URL)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
		if err != nil {
			return nil, nil, err
		}
		resp, err := p.Client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode/100 != 2 {
			resp.Body.Close()
			return nil, nil, fmt.Errorf("txt source returned %s", resp.Status)
		}
		return resp.Body, func() { resp.Body.Close() }, nil
	}
	path := p.URL
	if err == nil && u.Scheme == "file" {
		path = u.Path
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

func Parse(r io.Reader, log *slog.Logger) []netip.Prefix {
	var out []netip.Prefix
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ";") {
			continue
		}
		if i := strings.IndexAny(s, "#;"); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			if log != nil {
				log.Warn("invalid cidr in txt source", "line", line, "value", s, "error", err)
			}
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}
