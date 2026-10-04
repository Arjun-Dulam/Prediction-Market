package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusMetrics(t *testing.T) {
	registry := New()
	handler := registry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))
	registry.OrderAccepted()
	registry.Snapshot()
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{"exchange_http_requests_total 1", "exchange_http_errors_total 1", "exchange_orders_accepted_total 1", "exchange_snapshots_total 1"} {
		if !strings.Contains(recorder.Body.String(), metric) {
			t.Fatalf("missing %q in metrics:\n%s", metric, recorder.Body.String())
		}
	}
}
