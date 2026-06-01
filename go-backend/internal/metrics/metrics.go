package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Registry struct {
	requests  atomic.Uint64
	errors    atomic.Uint64
	duration  atomic.Uint64
	orders    atomic.Uint64
	snapshots atomic.Uint64
}

func New() *Registry { return &Registry{} }

func (m *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		observer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(observer, r)
		m.requests.Add(1)
		m.duration.Add(uint64(time.Since(start)))
		if observer.status >= 400 {
			m.errors.Add(1)
		}
	})
}

func (m *Registry) OrderAccepted() { m.orders.Add(1) }
func (m *Registry) Snapshot()      { m.snapshots.Add(1) }

func (m *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	requests := m.requests.Load()
	seconds := float64(m.duration.Load()) / float64(time.Second)
	_, _ = fmt.Fprintf(w, "# TYPE exchange_http_requests_total counter\nexchange_http_requests_total %d\n", requests)
	_, _ = fmt.Fprintf(w, "# TYPE exchange_http_errors_total counter\nexchange_http_errors_total %d\n", m.errors.Load())
	_, _ = fmt.Fprintf(w, "# TYPE exchange_http_request_duration_seconds summary\nexchange_http_request_duration_seconds_count %d\nexchange_http_request_duration_seconds_sum %.9f\n", requests, seconds)
	_, _ = fmt.Fprintf(w, "# TYPE exchange_orders_accepted_total counter\nexchange_orders_accepted_total %d\n", m.orders.Load())
	_, _ = fmt.Fprintf(w, "# TYPE exchange_snapshots_total counter\nexchange_snapshots_total %d\n", m.snapshots.Load())
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
