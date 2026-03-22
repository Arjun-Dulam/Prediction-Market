#!/usr/bin/env python3
"""
Compaction Ratio Performance Analysis
Parses Google Benchmark results and generates performance graphs.
"""

import re
import matplotlib.pyplot as plt
from collections import defaultdict
import sys
import os

def parse_benchmark_results(file_path):
    """Parse Google Benchmark output and extract performance data."""
    throughput_data = defaultdict(lambda: defaultdict(dict))
    latency_data = defaultdict(lambda: defaultdict(lambda: defaultdict(dict)))
    current_ratio = None

    with open(file_path, 'r') as f:
        for line in f:
            # Extract current compaction ratio
            ratio_match = re.search(r'Testing Compaction Ratio:\s+([\d.]+)', line)
            if ratio_match:
                current_ratio = float(ratio_match.group(1))
                continue

            if current_ratio is None:
                continue

            # Parse benchmark lines with throughput
            # Format: BM_AddOrder_No_Match/1000   337 ns   337 ns   2202227 items_per_second=2.97052M/s
            # Note: Times can be decimal (3.28 ns) for fast operations
            bench_match = re.search(
                r'(BM_\w+)/(\d+)\s+[\d.]+\s+ns\s+[\d.]+\s+ns\s+\d+\s+items_per_second=([\d.]+)([MK])/s',
                line
            )
            if bench_match:
                benchmark_name = bench_match.group(1)
                depth = int(bench_match.group(2))
                throughput = float(bench_match.group(3))
                unit = bench_match.group(4)

                # Convert to millions/sec
                if unit == 'K':
                    throughput /= 1000

                throughput_data[benchmark_name][current_ratio][depth] = throughput

                # Check for latency percentiles in the same line
                p50_match = re.search(r'p50=([\d.]+)k?', line)
                p99_match = re.search(r'p99=([\d.]+)k?', line)
                p999_match = re.search(r'p999=([\d.]+)k?', line)

                if p50_match:
                    p50 = float(p50_match.group(1))
                    if 'k' in p50_match.group(0):
                        p50 *= 1000
                    latency_data[benchmark_name][current_ratio][depth]['p50'] = p50

                if p99_match:
                    p99 = float(p99_match.group(1))
                    if 'k' in p99_match.group(0):
                        p99 *= 1000
                    latency_data[benchmark_name][current_ratio][depth]['p99'] = p99

                if p999_match:
                    p999 = float(p999_match.group(1))
                    if 'k' in p999_match.group(0):
                        p999 *= 1000
                    latency_data[benchmark_name][current_ratio][depth]['p999'] = p999

    return throughput_data, latency_data

def consolidate_benchmarks(throughput_data, latency_data):
    """Consolidate benchmarks into 3 core operations."""
    # Mapping from raw benchmark names to clean display names
    name_mapping = {
        'BM_AddOrder_No_Match': 'Add Order',
        'BM_AddOrder_Latency': 'Add Order',
        'BM_RemoveOrder_VaryDepth': 'Remove Order',
        'BM_MixedWorkload': 'Mixed Workload'
    }

    consolidated_throughput = defaultdict(lambda: defaultdict(dict))
    consolidated_latency = defaultdict(lambda: defaultdict(lambda: defaultdict(dict)))

    # Consolidate throughput data - prefer No_Match over Latency for Add Order
    for benchmark_name, ratios in throughput_data.items():
        clean_name = name_mapping.get(benchmark_name, benchmark_name)

        # For Add Order, prefer No_Match data (skip Latency throughput)
        if benchmark_name == 'BM_AddOrder_Latency' and 'BM_AddOrder_No_Match' in throughput_data:
            continue

        for ratio, depths in ratios.items():
            for depth, value in depths.items():
                consolidated_throughput[clean_name][ratio][depth] = value

    # Consolidate latency data (only from BM_AddOrder_Latency)
    for benchmark_name, ratios in latency_data.items():
        clean_name = name_mapping.get(benchmark_name, benchmark_name)
        for ratio, depths in ratios.items():
            for depth, percentiles in depths.items():
                for percentile, value in percentiles.items():
                    consolidated_latency[clean_name][ratio][depth][percentile] = value

    return dict(consolidated_throughput), dict(consolidated_latency)

def create_throughput_graphs(data, output_dir):
    """Generate throughput performance visualization graphs."""

    for benchmark_name, ratios in data.items():
        # Extract all unique depths and ratios
        all_depths = sorted(set(depth for ratio_data in ratios.values() for depth in ratio_data.keys()))
        all_ratios = sorted(ratios.keys())

        # Graph 1: Throughput vs Compaction Ratio (separate line per depth)
        plt.figure(figsize=(12, 7))
        for depth in all_depths:
            ratio_vals = []
            throughput_vals = []
            for ratio in all_ratios:
                if depth in ratios[ratio]:
                    ratio_vals.append(ratio)
                    throughput_vals.append(ratios[ratio][depth])

            if ratio_vals:
                label = f"{depth:,} orders" if depth > 0 else "cold start"
                plt.plot(ratio_vals, throughput_vals, marker='o', linewidth=2, label=label)

        plt.xlabel('Compaction Ratio', fontsize=12)
        plt.ylabel('Throughput (M ops/sec)', fontsize=12)
        plt.title(f'{benchmark_name}: Throughput vs Compaction Ratio', fontsize=14, fontweight='bold')
        plt.legend(loc='best', fontsize=10)
        plt.grid(True, alpha=0.3)
        plt.tight_layout()

        output_file = f"{output_dir}/{benchmark_name}_ratio_comparison.png"
        plt.savefig(output_file, dpi=300, bbox_inches='tight')
        print(f"✓ Saved: {output_file}")
        plt.close()

        # Graph 2: Throughput vs Depth (separate line per ratio)
        plt.figure(figsize=(12, 7))
        for ratio in all_ratios:
            depths = []
            throughputs = []
            for depth in all_depths:
                if depth in ratios[ratio]:
                    depths.append(depth)
                    throughputs.append(ratios[ratio][depth])

            if depths:
                plt.plot(depths, throughputs, marker='o', linewidth=2, label=f"Ratio {ratio:.2f}")

        plt.xlabel('Order Book Depth', fontsize=12)
        plt.ylabel('Throughput (M ops/sec)', fontsize=12)
        plt.title(f'{benchmark_name}: Throughput vs Depth', fontsize=14, fontweight='bold')
        plt.xscale('log')
        plt.legend(loc='best', fontsize=10)
        plt.grid(True, alpha=0.3)
        plt.tight_layout()

        output_file = f"{output_dir}/{benchmark_name}_depth_comparison.png"
        plt.savefig(output_file, dpi=300, bbox_inches='tight')
        print(f"✓ Saved: {output_file}")
        plt.close()

def create_latency_graphs(latency_data, output_dir):
    """Generate latency percentile visualization graphs."""

    for benchmark_name, ratios in latency_data.items():
        all_depths = sorted(set(depth for ratio_data in ratios.values() for depth in ratio_data.keys()))
        all_ratios = sorted(ratios.keys())

        # Create graphs for each percentile
        for percentile in ['p50', 'p99', 'p999']:
            # Graph: Percentile vs Compaction Ratio
            plt.figure(figsize=(12, 7))
            for depth in all_depths:
                ratio_vals = []
                latency_vals = []
                for ratio in all_ratios:
                    if depth in ratios[ratio] and percentile in ratios[ratio][depth]:
                        ratio_vals.append(ratio)
                        latency_vals.append(ratios[ratio][depth][percentile])

                if ratio_vals:
                    label = f"{depth:,} orders" if depth > 0 else "cold start"
                    plt.plot(ratio_vals, latency_vals, marker='o', linewidth=2, label=label)

            plt.xlabel('Compaction Ratio', fontsize=12)
            plt.ylabel(f'{percentile.upper()} Latency (ns)', fontsize=12)
            plt.title(f'{benchmark_name}: {percentile.upper()} Latency vs Compaction Ratio', fontsize=14, fontweight='bold')
            plt.legend(loc='best', fontsize=10)
            plt.grid(True, alpha=0.3)
            plt.tight_layout()

            output_file = f"{output_dir}/{benchmark_name}_{percentile}_ratio_comparison.png"
            plt.savefig(output_file, dpi=300, bbox_inches='tight')
            print(f"✓ Saved: {output_file}")
            plt.close()

def print_summary(data):
    """Print summary statistics."""
    print("\n" + "="*60)
    print("PERFORMANCE SUMMARY")
    print("="*60)
    
    for benchmark_name, ratios in data.items():
        print(f"\n{benchmark_name}:")
        print("-" * 60)
        
        # Find best ratio for each depth
        all_depths = sorted(set(depth for ratio_data in ratios.values() for depth in ratio_data.keys()))
        
        for depth in all_depths:
            best_ratio = None
            best_throughput = 0
            
            for ratio, depths_data in ratios.items():
                if depth in depths_data and depths_data[depth] > best_throughput:
                    best_throughput = depths_data[depth]
                    best_ratio = ratio
            
            depth_label = f"{depth:,} orders" if depth > 0 else "cold start"
            print(f"  {depth_label:20} → Best: Ratio {best_ratio:.2f} ({best_throughput:.2f} M/s)")

if __name__ == "__main__":
    # Configuration - paths relative to project root
    results_file = "benchmark_results/results/compaction_ratios_run2"
    output_dir = "benchmark_results/results/run_3"

    print("Compaction Ratio Performance Analysis")
    print("=" * 60)

    # Ensure output directory exists
    if not os.path.exists(output_dir):
        os.makedirs(output_dir)
        print(f"Created directory: {output_dir}")

    # Parse results
    print(f"\nParsing: {results_file}")
    raw_throughput, raw_latency = parse_benchmark_results(results_file)

    if not raw_throughput:
        print("ERROR: No benchmark data found in results file!")
        sys.exit(1)

    # Consolidate into 3 core operations
    print(f"Consolidating {len(raw_throughput)} benchmarks into 3 core operations...")
    throughput_data, latency_data = consolidate_benchmarks(raw_throughput, raw_latency)

    print(f"Operations: {', '.join(throughput_data.keys())}")
    if latency_data:
        print(f"Latency data available for: {', '.join(latency_data.keys())}")

    # Generate throughput graphs
    print(f"\nGenerating throughput graphs → {output_dir}/")
    create_throughput_graphs(throughput_data, output_dir)

    # Generate latency graphs if available
    if latency_data:
        print(f"\nGenerating latency graphs → {output_dir}/")
        create_latency_graphs(latency_data, output_dir)

    # Print summary
    print_summary(throughput_data)

    print("\n" + "="*60)
    print("Analysis complete!")
    print("="*60)
