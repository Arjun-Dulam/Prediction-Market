#!/usr/bin/env python3
"""Per-order stage costs from before/after metric deltas; no server percentile claim."""
import collections
import json
import pathlib
import re
import statistics
import sys

pattern = re.compile(r'^exchange_stage_duration_seconds_(count|sum)\{stage="(\w+)"\} ([0-9.]+)$', re.M)
groups = collections.defaultdict(list)
for name in sys.argv[1:]:
    for line in pathlib.Path(name).read_text().splitlines():
        row = json.loads(line)
        before = {(kind, stage): float(value) for kind, stage, value in pattern.findall(row['metrics_before'])}
        after = {(kind, stage): float(value) for kind, stage, value in pattern.findall(row['metrics_after'])}
        delta = {key: value - before.get(key, 0) for key, value in after.items()}
        costs = [1000 * delta.get(('sum', stage), 0) / row['orders'] for stage in ('lock_wait', 'wal', 'projection', 'engine', 'quote')]
        phases = delta.get(('count', 'wal'), 0)
        costs.append(2 * row['orders'] / phases if phases else 0)
        groups[(row['label'], row['concurrency'])].append(costs)
print('| Build | Clients | Wait ms/order | WAL ms/order | SQL ms/order | Engine ms/order | Quote ms/order | Orders/group |')
print('|---|---:|---:|---:|---:|---:|---:|---:|')
for (label, clients), rows in sorted(groups.items()):
    medians = [statistics.median(column) for column in zip(*rows)]
    print(f'| {label} | {clients} | ' + ' | '.join(f'{value:.3f}' for value in medians) + ' |')
