package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPercentileAndLevels(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	if percentile(values, 50) != 2 || percentile(values, 95) != 4 || percentile(nil, 99) != 0 {
		t.Fatal("nearest-rank percentile")
	}
	if _, err := parseLevels("1,0", 32); err == nil {
		t.Fatal("accepted zero clients")
	}
	got, err := parseLevels("1, 8,32", 1)
	if err != nil || len(got) != 3 || got[2] != 32 {
		t.Fatalf("%v %v", got, err)
	}
}
func TestLoadReportsFailuresAndChecksAccounting(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "errors", false: "success"}[fail], func(t *testing.T) {
			var registrations, submitted atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/register"):
					n := registrations.Add(1)
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(identity{ID: map[int64]string{1: "yes", 2: "no"}[n], Token: "test"})
				case strings.HasSuffix(r.URL.Path, "/markets"):
					w.WriteHeader(201)
				case strings.HasSuffix(r.URL.Path, "/deposit"):
					w.WriteHeader(204)
				case strings.HasSuffix(r.URL.Path, "/orders"):
					if r.Header.Get("Authorization") != "Bearer test" {
						t.Error("missing authentication")
					}
					submitted.Add(1)
					if fail {
						http.Error(w, "injected", 503)
						return
					}
					var o order
					_ = json.NewDecoder(r.Body).Decode(&o)
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(map[string]string{"order_id": o.ID})
				case strings.Contains(r.URL.Path, "/balances/"):
					_ = json.NewEncoder(w).Encode(map[string]int64{"cents": 100*100 - 50*50})
				case strings.Contains(r.URL.Path, "/positions/"):
					p := map[string]int{"yes": 50, "no": 0}
					if strings.Contains(r.URL.Path, "/no/") {
						p = map[string]int{"yes": 0, "no": 50}
					}
					_ = json.NewEncoder(w).Encode(p)
				default:
					_, _ = w.Write([]byte("metrics"))
				}
			}))
			defer srv.Close()
			r, err := run(srv.URL, 100, 8, 0)
			if err != nil {
				t.Fatal(err)
			}
			if r.Orders != 100 || submitted.Load() != 100 {
				t.Fatalf("count %+v", r)
			}
			if fail {
				if r.Failures != 100 || r.Throughput != 0 || r.ErrorRate != 1 || r.Verified {
					t.Fatalf("incorrect failure report %+v", r)
				}
			} else if r.Failures != 0 || !r.Verified || r.Throughput <= 0 || r.P99 < r.P50 {
				t.Fatalf("incorrect success report %+v", r)
			}
		})
	}
}
func TestWindows(t *testing.T) {
	got := windows([]sample{{end: time.Second, latency: time.Millisecond}, {end: 11 * time.Second, latency: 2 * time.Millisecond, err: "failed"}}, 12*time.Second, 10*time.Second)
	if len(got) != 2 || got[0].Throughput != 0.1 || got[1].Duration != 2 || got[1].Failures != 1 || got[1].P99 != 2 {
		t.Fatalf("%+v", got)
	}
}
