set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="$ENGINE_DIR/benchmarks/results"
BENCHMARK_BIN="$ENGINE_DIR/../build/engine/OrderBookBenchmark"

RATIOS=(0.15 0.25 0.35 0.45 0.55 0.65 0.75 0.85 0.95)
OUTPUT_FILE="$RESULTS_DIR/compaction_ratios_run4"
CPP_FILE="$ENGINE_DIR/src/orderbook.cpp"

mkdir -p "$(dirname "$OUTPUT_FILE")"

echo "Starting Compaction Study - $(date)" > "$OUTPUT_FILE"
echo "Testing ratios: ${RATIOS[@]}" | tee -a "$OUTPUT_FILE"
echo "" >> "$OUTPUT_FILE"

ORIGINAL_RATIO=$(grep "#define COMPACTION_RATIO" "$CPP_FILE" | awk '{print $3}')

for RATIO in "${RATIOS[@]}"
do
    echo "========================================" | tee -a "$OUTPUT_FILE"
    echo "Testing Compaction Ratio: $RATIO" | tee -a "$OUTPUT_FILE"
    echo "========================================" | tee -a "$OUTPUT_FILE"

    # Update the #define
    sed -i '' "s/#define COMPACTION_RATIO.*/#define COMPACTION_RATIO $RATIO/" "$CPP_FILE"

    # Rebuild
    echo "Rebuilding..." | tee -a "$OUTPUT_FILE"
    cmake --build "$ENGINE_DIR/../build" --config Release > /dev/null 2>&1

    # Run benchmarks
    echo "Running benchmarks..." | tee -a "$OUTPUT_FILE"
    "$BENCHMARK_BIN" >> "$OUTPUT_FILE" 2>&1

    echo "" >> "$OUTPUT_FILE"
done

# Restore original ratio
echo "Restoring original ratio: $ORIGINAL_RATIO" | tee -a "$OUTPUT_FILE"
sed -i '' "s/#define COMPACTION_RATIO.*/#define COMPACTION_RATIO $ORIGINAL_RATIO/" "$CPP_FILE"
cmake --build "$ENGINE_DIR/../build" --config Release > /dev/null 2>&1

echo "========================================" | tee -a "$OUTPUT_FILE"
echo "Study complete! Results: $OUTPUT_FILE" | tee -a "$OUTPUT_FILE"
echo "Original ratio restored: $ORIGINAL_RATIO" | tee -a "$OUTPUT_FILE"
