#!/usr/bin/env python3
"""Summarize committed JSONL runs; medians are per-run, not pooled percentiles."""
import json
import statistics
import sys

print('| Build | Clients | Runs | Orders | Median orders/s (range) | p50 ms | p95 ms | p99 ms | Error rate | Median seconds | Verified |')
print('|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|')
for path in sys.argv[1:]:
    rows = [json.loads(line) for line in open(path) if line.strip()]
    for clients in sorted({r['concurrency'] for r in rows}):
        group = [r for r in rows if r['concurrency'] == clients]
        median = lambda key: statistics.median(r[key] for r in group)
        throughput = [r['successful_orders_per_second'] for r in group]
        orders = sum(r['orders'] for r in group)
        errors = sum(r['failures'] for r in group)
        print(f"| {group[0]['label']} | {clients} | {len(group)} | {orders} | {median('successful_orders_per_second'):.1f} ({min(throughput):.1f}–{max(throughput):.1f}) | {median('p50_ms'):.2f} | {median('p95_ms'):.2f} | {median('p99_ms'):.2f} | {100*errors/orders:.3f}% | {median('duration_seconds'):.2f} | {sum(r['accounting_verified'] for r in group)}/{len(group)} |")
