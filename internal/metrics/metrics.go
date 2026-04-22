package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	mu       sync.Mutex
	counters map[string]float64
	gauges   map[string]float64
}

func New() *Registry {
	return &Registry{counters: map[string]float64{}, gauges: map[string]float64{}}
}

func (r *Registry) Inc(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name]++
}

func (r *Registry) Set(name string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[name] = value
}

func (r *Registry) SetGroupGauge(name, group string, value float64) {
	r.Set(fmt.Sprintf(`%s{group="%s"}`, name, sanitize(group)), value)
}

func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		for k, v := range r.counters {
			fmt.Fprintf(w, "route_sync_%s %.0f\n", k, v)
		}
		for k, v := range r.gauges {
			fmt.Fprintf(w, "route_sync_%s %f\n", k, v)
		}
	})
}

func Serve(ctx context.Context, addr string, reg *Registry, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("metrics endpoint starting", "listen", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics endpoint failed", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv
}

func sanitize(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "_").Replace(s)
}
