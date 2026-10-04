package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "http://localhost:8080", "API base URL")
	orders := flag.Int("orders", 1000, "total orders (rounded down to an even number)")
	concurrency := flag.Int("concurrency", 16, "concurrent clients")
	flag.Parse()
	if *orders < 2 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "orders must be >= 2 and concurrency must be >= 1")
		os.Exit(2)
	}
	*orders -= *orders % 2

	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConns: *concurrency * 2, MaxIdleConnsPerHost: *concurrency * 2}}
	stamp := time.Now().UnixNano()
	market, symbol := fmt.Sprintf("load-%d", stamp), fmt.Sprintf("LOAD_%d", stamp)
	yes := register(client, *base, fmt.Sprintf("yes-%d@example.com", stamp), fmt.Sprintf("yes-%d", stamp))
	no := register(client, *base, fmt.Sprintf("no-%d@example.com", stamp), fmt.Sprintf("no-%d", stamp))
	mustPost(client, *base+"/api/v1/markets", yes.Token, map[string]any{"id": market, "symbol": symbol}, http.StatusCreated)
	for _, user := range []identity{yes, no} {
		mustPost(client, *base+"/api/v1/balances/deposit", user.Token, map[string]any{"cents": int64(*orders * 100)}, http.StatusNoContent)
	}

	jobs := make(chan int)
	latencies := make([]time.Duration, *orders)
	var failures atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				side, user := "buy_yes", yes
				if i%2 == 1 {
					side, user = "buy_no", no
				}
				payload := map[string]any{"id": fmt.Sprintf("load-order-%d-%d", stamp, i), "market_id": market, "side": side, "price": 50, "quantity": 1}
				body, _ := json.Marshal(payload)
				t0 := time.Now()
				request, _ := http.NewRequest(http.MethodPost, *base+"/api/v1/trading/orders", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+user.Token)
				resp, err := client.Do(request)
				latencies[i] = time.Since(t0)
				if err != nil {
					failures.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusCreated {
					failures.Add(1)
				}
			}
		}()
	}
	for i := 0; i < *orders; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	fmt.Printf("orders=%d concurrency=%d failures=%d throughput=%.1f orders/sec p50=%s p95=%s p99=%s elapsed=%s\n",
		*orders, *concurrency, failures.Load(), float64(*orders)/elapsed.Seconds(), percentile(latencies, 50), percentile(latencies, 95), percentile(latencies, 99), elapsed.Round(time.Millisecond))
	if failures.Load() != 0 {
		os.Exit(1)
	}
}

type identity struct {
	ID    string `json:"user_id"`
	Token string `json:"token"`
}

func register(client *http.Client, base, email, username string) identity {
	body, _ := json.Marshal(map[string]string{"email": email, "username": username, "password": "benchmark-password"})
	resp, err := client.Post(base+"/api/v1/users/register", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusCreated {
		fmt.Fprintf(os.Stderr, "register: response=%v error=%v\n", resp, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var user identity
	if json.NewDecoder(resp.Body).Decode(&user) != nil || user.Token == "" {
		fmt.Fprintln(os.Stderr, "register: invalid response")
		os.Exit(1)
	}
	return user
}

func mustPost(client *http.Client, url, token string, payload any, want int) {
	body, _ := json.Marshal(payload)
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup %s: %v\n", url, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		message, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "setup %s: got %s: %s\n", url, resp.Status, message)
		os.Exit(1)
	}
}

func percentile(values []time.Duration, p int) time.Duration {
	index := (len(values)*p + 99) / 100
	if index < 1 {
		index = 1
	}
	return values[index-1]
}
