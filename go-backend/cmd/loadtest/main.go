// loadtest is a closed-loop, synthetic authenticated HTTP workload. Setup and
// accounting verification are outside the measured order-placement interval.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type identity struct {
	ID    string `json:"user_id"`
	Token string `json:"token"`
}
type order struct {
	ID       string `json:"id"`
	Market   string `json:"market_id"`
	Side     string `json:"side"`
	Price    int    `json:"price"`
	Quantity int    `json:"quantity"`
}
type metricSample struct {
	Seconds float64 `json:"seconds"`
	Metrics string  `json:"metrics"`
}
type result struct {
	MarketCount           int            `json:"market_count"`
	AccountScope          string         `json:"account_scope"`
	Markets               []string       `json:"market_ids"`
	Market                string         `json:"market_id"`
	UserIDs               []string       `json:"user_ids"`
	MetricsSamples        []metricSample `json:"metrics_samples,omitempty"`
	Label                 string         `json:"label"`
	Environment           string         `json:"environment"`
	Client                string         `json:"client"`
	Started               time.Time      `json:"started_utc"`
	Repeat                int            `json:"repeat"`
	Concurrency           int            `json:"concurrency"`
	Orders                int            `json:"orders"`
	DurationSeconds       float64        `json:"duration_seconds"`
	TargetDurationSeconds float64        `json:"target_duration_seconds"`
	Successful            int            `json:"successful"`
	Failures              int            `json:"failures"`
	ErrorRate             float64        `json:"error_rate"`
	Throughput            float64        `json:"successful_orders_per_second"`
	AttemptedThroughput   float64        `json:"attempted_orders_per_second"`
	P50                   float64        `json:"p50_ms"`
	P95                   float64        `json:"p95_ms"`
	P99                   float64        `json:"p99_ms"`
	Statuses              map[string]int `json:"statuses"`
	Errors                []string       `json:"error_samples,omitempty"`
	Verified              bool           `json:"accounting_verified"`
	VerificationError     string         `json:"verification_error,omitempty"`
	MetricsBefore         string         `json:"metrics_before,omitempty"`
	MetricsAfter          string         `json:"metrics_after,omitempty"`
	Windows               []window       `json:"windows"`
}
type sample struct {
	latency time.Duration
	end     time.Duration
	status  string
	err     string
}
type window struct {
	Start      float64 `json:"start_seconds"`
	Duration   float64 `json:"duration_seconds"`
	Orders     int     `json:"orders"`
	Failures   int     `json:"failures"`
	Throughput float64 `json:"successful_orders_per_second"`
	P99        float64 `json:"p99_ms"`
}

func main() {
	base := flag.String("base", "http://localhost:8080", "API base URL")
	orders := flag.Int("orders", 5000, "even order count per run (ignored with -duration)")
	concurrency := flag.Int("concurrency", 32, "clients, unless -levels is set")
	levels := flag.String("levels", "", "comma-separated client counts, e.g. 1,8,32,64")
	repeats := flag.Int("repeats", 1, "runs per concurrency level")
	duration := flag.Duration("duration", 0, "submit for this duration, then drain outstanding requests")
	label := flag.String("label", "local", "baseline or improved build label")
	environment := flag.String("environment", "unspecified", "hardware, Docker resources, server configuration")
	markets := flag.Int("markets", 1, "number of independent books; each paired YES/NO order uses one book")
	accountScope := flag.String("accounts", "shared", "shared or independent account pair per market")
	flag.Parse()
	counts, err := parseLevels(*levels, *concurrency)
	if err != nil || *orders < 2 || *orders%2 != 0 || *repeats < 1 || *duration < 0 || *markets < 1 || (*accountScope != "shared" && *accountScope != "independent") {
		fmt.Fprintln(os.Stderr, "require even orders >= 2, positive clients/repeats, nonnegative duration:", err)
		os.Exit(2)
	}
	failed := false
	for repeat := 1; repeat <= *repeats; repeat++ {
		for _, count := range counts {
			r, err := runMarkets(strings.TrimRight(*base, "/"), *orders, count, *duration, *markets, *accountScope)
			if err != nil {
				fmt.Fprintln(os.Stderr, "setup:", err)
				os.Exit(1)
			}
			r.Repeat, r.Label, r.Environment = repeat, *label, *environment
			r.Client = runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
			if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if r.Failures > 0 || !r.Verified {
				failed = true
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}
func parseLevels(s string, fallback int) ([]int, error) {
	if s == "" {
		s = strconv.Itoa(fallback)
	}
	var counts []int
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid concurrency %q", part)
		}
		counts = append(counts, n)
	}
	return counts, nil
}
func request(client *http.Client, method, url, token string, payload any, want int, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf("%s: status %d: %.200s", url, resp.StatusCode, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
func run(base string, orders, concurrency int, duration time.Duration) (result, error) {
	return runMarkets(base, orders, concurrency, duration, 1, "shared")
}
func runMarkets(base string, orders, concurrency int, duration time.Duration, marketCount int, scope string) (result, error) {
	r := result{Concurrency: concurrency, TargetDurationSeconds: duration.Seconds(), Statuses: map[string]int{}, MarketCount: marketCount, AccountScope: scope}
	transport := &http.Transport{MaxIdleConns: concurrency * 2, MaxIdleConnsPerHost: concurrency * 2}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	market := "load-" + stamp
	markets := make([]string, marketCount)
	usersCount := 2
	if scope == "independent" {
		usersCount = 2 * marketCount
	}
	users := make([]identity, usersCount)
	for i := range users {
		name := fmt.Sprintf("load-%s-%d", stamp, i)
		if err := request(client, "POST", base+"/api/v1/users/register", "", map[string]string{"email": name + "@example.com", "username": name, "password": "benchmark-password"}, 201, &users[i]); err != nil {
			return r, err
		}
		if users[i].ID == "" || users[i].Token == "" {
			return r, fmt.Errorf("invalid registration response")
		}
	}
	for i := range markets {
		markets[i] = fmt.Sprintf("%s-%d", market, i)
		if err := request(client, "POST", base+"/api/v1/markets", users[0].Token, map[string]string{"id": markets[i], "symbol": fmt.Sprintf("LOAD_%s_%d", stamp, i)}, 201, nil); err != nil {
			return r, err
		}
	}
	funding := int64(orders) * 100
	if duration > 0 {
		funding = 1 << 50
	}
	for _, u := range users {
		if err := request(client, "POST", base+"/api/v1/balances/deposit", u.Token, map[string]int64{"cents": funding}, 204, nil); err != nil {
			return r, err
		}
	}
	metrics := func() string {
		var b strings.Builder
		resp, err := client.Get(base + "/metrics")
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ""
		}
		_, _ = io.Copy(&b, io.LimitReader(resp.Body, 65536))
		return b.String()
	}
	r.Market = markets[0]
	r.Markets = markets
	for _, u := range users {
		r.UserIDs = append(r.UserIDs, u.ID)
	}
	r.MetricsBefore = metrics()
	jobs := make(chan int)
	samples := make([][]sample, concurrency)
	var wg sync.WaitGroup
	r.Started = time.Now().UTC()
	start := time.Now()
	monitorStop, monitorDone := make(chan struct{}), make(chan struct{})
	if duration > 0 {
		go func() {
			defer close(monitorDone)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					r.MetricsSamples = append(r.MetricsSamples, metricSample{Seconds: time.Since(start).Seconds(), Metrics: metrics()})
				case <-monitorStop:
					return
				}
			}
		}()
	} else {
		close(monitorDone)
	}
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range jobs {
				side := "buy_yes"
				if i%2 == 1 {
					side = "buy_no"
				}
				marketIndex := (i / 2) % marketCount
				userIndex := i % 2
				if scope == "independent" {
					userIndex += marketIndex * 2
				}
				o := order{ID: fmt.Sprintf("load-order-%s-%d", stamp, i), Market: markets[marketIndex], Side: side, Price: 50, Quantity: 1}
				b, _ := json.Marshal(o)
				req, _ := http.NewRequest("POST", base+"/api/v1/trading/orders", bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+users[userIndex].Token)
				t0 := time.Now()
				resp, err := client.Do(req)
				status := "transport_error"
				if err == nil {
					status = strconv.Itoa(resp.StatusCode)
					body, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					if readErr != nil {
						err = readErr
					} else if resp.StatusCode != 201 {
						err = fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, body)
					} else {
						var got struct {
							ID string `json:"order_id"`
						}
						if decodeErr := json.Unmarshal(body, &got); decodeErr != nil || got.ID != o.ID {
							err = fmt.Errorf("invalid order acknowledgement")
						}
					}
				}
				// Includes body consumption and acknowledgement validation, unlike the old tool.
				s := sample{latency: time.Since(t0), end: time.Since(start), status: status}
				if err != nil {
					s.err = err.Error()
				}
				samples[w] = append(samples[w], s)
			}
		}(w)
	}
	count := 0
	for {
		if duration == 0 && count >= orders || duration > 0 && time.Since(start) >= duration {
			break
		}
		jobs <- count
		jobs <- count + 1
		count += 2
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	close(monitorStop)
	<-monitorDone
	r.Orders, r.DurationSeconds = count, elapsed.Seconds()
	var all []sample
	var latencies []time.Duration
	for _, ss := range samples {
		for _, s := range ss {
			all = append(all, s)
			latencies = append(latencies, s.latency)
			r.Statuses[s.status]++
			if s.err != "" {
				r.Failures++
				if len(r.Errors) < 5 {
					r.Errors = append(r.Errors, s.err)
				}
			}
		}
	}
	r.Successful = count - r.Failures
	r.ErrorRate = float64(r.Failures) / float64(count)
	r.Throughput = float64(r.Successful) / elapsed.Seconds()
	r.AttemptedThroughput = float64(count) / elapsed.Seconds()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	r.P50, r.P95, r.P99 = percentile(latencies, 50), percentile(latencies, 95), percentile(latencies, 99)
	r.Windows = windows(all, elapsed, 10*time.Second)
	r.MetricsAfter = metrics()
	if r.Failures == 0 {
		if err := verifyMarkets(client, base, markets, users, funding, count, scope); err != nil {
			r.VerificationError = err.Error()
		} else {
			r.Verified = true
		}
	}
	return r, nil
}
func verifyMarkets(client *http.Client, base string, markets []string, users []identity, funding int64, count int, scope string) error {
	pairs := count / 2
	for i, u := range users {
		expectedPairs := pairs
		marketIndices := make([]int, 0, len(markets))
		if scope == "independent" {
			m := i / 2
			expectedPairs = pairs / len(markets)
			if m < pairs%len(markets) {
				expectedPairs++
			}
			marketIndices = append(marketIndices, m)
		} else {
			for m := range markets {
				marketIndices = append(marketIndices, m)
			}
		}
		var balance struct {
			Cents int64 `json:"cents"`
		}
		if err := request(client, "GET", base+"/api/v1/balances/"+u.ID, u.Token, nil, 200, &balance); err != nil {
			return err
		}
		if want := funding - int64(expectedPairs)*50; balance.Cents != want {
			return fmt.Errorf("account %d balance=%d, want %d", i, balance.Cents, want)
		}
		for _, m := range marketIndices {
			n := pairs / len(markets)
			if m < pairs%len(markets) {
				n++
			}
			var p struct {
				Yes int `json:"yes"`
				No  int `json:"no"`
			}
			if err := request(client, "GET", base+"/api/v1/positions/"+u.ID+"/"+markets[m], u.Token, nil, 200, &p); err != nil {
				return err
			}
			yes, no := n, 0
			if i%2 == 1 {
				yes, no = 0, n
			}
			if p.Yes != yes || p.No != no {
				return fmt.Errorf("account %d market %d position=%+v, want %d/%d", i, m, p, yes, no)
			}
		}
	}
	return nil
}
func percentile(sorted []time.Duration, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return float64(sorted[(len(sorted)*p+99)/100-1]) / float64(time.Millisecond)
}
func windows(samples []sample, elapsed, width time.Duration) []window {
	n := int((elapsed + width - 1) / width)
	out := make([]window, n)
	latencies := make([][]time.Duration, n)
	for i := range out {
		d := width
		if elapsed-time.Duration(i)*width < d {
			d = elapsed - time.Duration(i)*width
		}
		out[i] = window{Start: float64(i) * width.Seconds(), Duration: d.Seconds()}
	}
	for _, s := range samples {
		i := int(s.end / width)
		if i >= n {
			i = n - 1
		}
		out[i].Orders++
		if s.err != "" {
			out[i].Failures++
		}
		latencies[i] = append(latencies[i], s.latency)
	}
	for i := range out {
		sort.Slice(latencies[i], func(a, b int) bool { return latencies[i][a] < latencies[i][b] })
		out[i].P99 = percentile(latencies[i], 99)
		out[i].Throughput = float64(out[i].Orders-out[i].Failures) / out[i].Duration
	}
	return out
}
