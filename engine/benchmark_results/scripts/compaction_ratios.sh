set -e

RATIOS=(0.15 0.25 0.35 0.45 0.55 0.65 0.75 0.85 0.95)
OUTPUT_FILE="benchmark_results/results/compaction_ratios_run4"
CPP_FILE="src/orderbook.cpp"
BUILD_DIR="build"
# TODO: Mention this in the readme
# Create output directory if it doesn't exist
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
    cmake --build "$BUILD_DIR" --config Release > /dev/null 2>&1

    # Run benchmarks
    echo "Running benchmarks..." | tee -a "$OUTPUT_FILE"
    "$BUILD_DIR"/OrderBookBenchmark >> "$OUTPUT_FILE" 2>&1

    echo "" >> "$OUTPUT_FILE"
done

# Restore original ratio
echo "Restoring original ratio: $ORIGINAL_RATIO" | tee -a "$OUTPUT_FILE"
sed -i '' "s/#define COMPACTION_RATIO.*/#define COMPACTION_RATIO $ORIGINAL_RATIO/" "$CPP_FILE"
cmake --build "$BUILD_DIR" --config Release > /dev/null 2>&1

echo "========================================" | tee -a "$OUTPUT_FILE"
echo "Study complete! Results: $OUTPUT_FILE" | tee -a "$OUTPUT_FILE"
echo "Original ratio restored: $ORIGINAL_RATIO" | tee -a "$OUTPUT_FILE"
