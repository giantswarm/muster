#!/bin/bash
# Stress Test Script for BDD Test Suite
# Phase 1: Reliable Reproduction (Issue #309)
#
# This script runs the test suite N times with high parallelism to establish
# a baseline failure rate and identify flaky tests.
#
# Usage:
#   ./scripts/stress-test.sh [iterations] [parallelism]
#
# Examples:
#   ./scripts/stress-test.sh           # Run 20 iterations with 50 workers (default)
#   ./scripts/stress-test.sh 50        # Run 50 iterations with 50 workers
#   ./scripts/stress-test.sh 20 25     # Run 20 iterations with 25 workers
#   ./scripts/stress-test.sh 100 50    # Run 100 iterations (like CI) with 50 workers

set -euo pipefail

# Configuration
ITERATIONS="${1:-20}"
PARALLEL="${2:-50}"
BASE_PORT="${3:-30000}"  # Use higher port range to avoid conflicts
RESULTS_DIR="stress-test-results"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RUN_DIR="${RESULTS_DIR}/${TIMESTAMP}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Known flaky scenarios (from issue #309)
LIFECYCLE_SCENARIOS=(
    "mcpserver-tool-call-lifecycle"
    "mcpserver-streamable-http-tool-call-lifecycle"
    "oauth-sso-state-sync-after-login"
)

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}  Muster BDD Test Suite Stress Test${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""
echo -e "Configuration:"
echo -e "  Iterations:  ${YELLOW}${ITERATIONS}${NC}"
echo -e "  Parallelism: ${YELLOW}${PARALLEL}${NC}"
echo -e "  Base Port:   ${YELLOW}${BASE_PORT}${NC}"
echo -e "  Results Dir: ${YELLOW}${RUN_DIR}${NC}"
echo ""

# Ensure muster binary is up to date
echo -e "${BLUE}[Pre-flight] Building muster...${NC}"
go install
echo -e "${GREEN}[Pre-flight] Build complete${NC}"
echo ""

# Create results directory
mkdir -p "${RUN_DIR}/logs"

# Initialize counters
PASSED=0
FAILED=0
FAILED_SCENARIOS=()

# Summary file for detailed results
SUMMARY_FILE="${RUN_DIR}/summary.txt"
FAILURES_FILE="${RUN_DIR}/failures.json"

# Write header to summary
cat > "${SUMMARY_FILE}" << EOF
Muster BDD Stress Test Summary
==============================
Started: $(date)
Iterations: ${ITERATIONS}
Parallelism: ${PARALLEL}
Host: $(hostname)
Go Version: $(go version)

Results:
--------
EOF

# Initialize failures JSON
echo "[]" > "${FAILURES_FILE}"

# Run stress test iterations
echo -e "${BLUE}Starting stress test...${NC}"
echo ""

for i in $(seq 1 "${ITERATIONS}"); do
    ITERATION_LOG="${RUN_DIR}/logs/iteration_${i}.log"
    
    printf "[%3d/%3d] Running test suite... " "${i}" "${ITERATIONS}"
    
    # Capture start time (seconds only for portability)
    START_TIME=$(date +%s)
    
    # Run the test suite
    if timeout 10m muster test --parallel "${PARALLEL}" --base-port "${BASE_PORT}" > "${ITERATION_LOG}" 2>&1; then
        END_TIME=$(date +%s)
        DURATION=$((END_TIME - START_TIME))
        
        echo -e "${GREEN}PASSED${NC} (${DURATION}s)"
        PASSED=$((PASSED + 1))
        echo "Iteration ${i}: PASSED (${DURATION}s)" >> "${SUMMARY_FILE}"
    else
        END_TIME=$(date +%s)
        DURATION=$((END_TIME - START_TIME))
        
        # Extract failed scenario names from the log
        FAILED_SCENARIOS_THIS_RUN=$(grep -E "^❌|FAILED.*:.*" "${ITERATION_LOG}" 2>/dev/null | head -10 || true)
        
        # Look for specific lifecycle test failures
        for SCENARIO in "${LIFECYCLE_SCENARIOS[@]}"; do
            if grep -q "${SCENARIO}" "${ITERATION_LOG}" 2>/dev/null; then
                if grep -A5 "${SCENARIO}" "${ITERATION_LOG}" | grep -qE "FAILED|timeout|error" 2>/dev/null; then
                    FAILED_SCENARIOS+=("${SCENARIO}")
                fi
            fi
        done
        
        echo -e "${RED}FAILED${NC} (${DURATION}s)"
        FAILED=$((FAILED + 1))
        echo "Iteration ${i}: FAILED (${DURATION}s)" >> "${SUMMARY_FILE}"
        
        # Save detailed failure info
        {
            echo "=== Iteration ${i} Failure Details ==="
            echo "Duration: ${DURATION}s"
            echo "Failed scenarios detected:"
            echo "${FAILED_SCENARIOS_THIS_RUN}"
            echo ""
        } >> "${RUN_DIR}/failure_details.txt"
        
        # Extract and append to failures JSON
        # Look for timeout patterns or specific error messages
        if grep -q "timeout" "${ITERATION_LOG}" 2>/dev/null; then
            TIMEOUT_SCENARIO=$(grep -B5 "timeout" "${ITERATION_LOG}" | grep -oE "(mcpserver-[a-z-]+|oauth-[a-z-]+)" | head -1 || echo "unknown")
            jq --arg iter "$i" --arg scenario "$TIMEOUT_SCENARIO" --arg dur "$DURATION" \
                '. += [{"iteration": ($iter|tonumber), "scenario": $scenario, "duration": $dur, "type": "timeout"}]' \
                "${FAILURES_FILE}" > "${FAILURES_FILE}.tmp" && mv "${FAILURES_FILE}.tmp" "${FAILURES_FILE}" 2>/dev/null || true
        fi
    fi
done

echo ""
echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}  Stress Test Results${NC}"
echo -e "${BLUE}======================================${NC}"

# Calculate failure rate (integer math, sufficient for this purpose)
TOTAL=$((PASSED + FAILED))
if [ "${TOTAL}" -gt 0 ]; then
    FAILURE_RATE=$((FAILED * 100 / TOTAL))
else
    FAILURE_RATE=0
fi

echo ""
echo -e "Summary:"
echo -e "  Total Runs:   ${YELLOW}${TOTAL}${NC}"
echo -e "  Passed:       ${GREEN}${PASSED}${NC}"
echo -e "  Failed:       ${RED}${FAILED}${NC}"
echo -e "  Failure Rate: ${YELLOW}${FAILURE_RATE}%${NC}"

# Analyze which scenarios failed most
echo ""
echo -e "${BLUE}Failed Scenario Analysis:${NC}"

if [ ${#FAILED_SCENARIOS[@]} -gt 0 ]; then
    # Count occurrences of each failed scenario
    echo "${FAILED_SCENARIOS[@]}" | tr ' ' '\n' | sort | uniq -c | sort -rn | while read COUNT SCENARIO; do
        echo -e "  ${SCENARIO}: ${RED}${COUNT}${NC} failures"
    done
else
    if [ "${FAILED}" -gt 0 ]; then
        echo -e "  ${YELLOW}No specific lifecycle scenarios identified in failures${NC}"
        echo -e "  Check detailed logs: ${RUN_DIR}/logs/"
    else
        echo -e "  ${GREEN}No failures detected!${NC}"
    fi
fi

# Write final summary
cat >> "${SUMMARY_FILE}" << EOF

Final Results:
--------------
Total: ${TOTAL}
Passed: ${PASSED}
Failed: ${FAILED}
Failure Rate: ${FAILURE_RATE}%

Completed: $(date)
EOF

echo ""
echo -e "Detailed results saved to:"
echo -e "  Summary:  ${YELLOW}${SUMMARY_FILE}${NC}"
echo -e "  Logs:     ${YELLOW}${RUN_DIR}/logs/${NC}"
echo -e "  Failures: ${YELLOW}${FAILURES_FILE}${NC}"

# Exit with error if failure rate exceeds threshold
if [ "${FAILED}" -gt 0 ]; then
    echo ""
    echo -e "${YELLOW}Recommendation: Review failure logs to identify patterns${NC}"
    echo ""
    echo "Quick analysis commands:"
    echo "  # View recent failures:"
    echo "  grep -l 'FAILED' ${RUN_DIR}/logs/*.log"
    echo ""
    echo "  # Check for timeout patterns:"
    echo "  grep -h 'timeout' ${RUN_DIR}/logs/*.log | sort | uniq -c"
    echo ""
    echo "  # Check specific scenario:"
    echo "  grep -l 'mcpserver-tool-call-lifecycle' ${RUN_DIR}/logs/*.log"
fi

# Comparison with CI
echo ""
echo -e "${BLUE}Comparison with CI:${NC}"
echo "  CI runs with: --parallel 50"
echo "  CI observed:  ~12% failure rate (4/33 runs)"
if [ "${FAILED}" -gt 0 ]; then
    echo -e "  Local result: ${YELLOW}${FAILURE_RATE}% failure rate${NC}"
else
    echo -e "  Local result: ${GREEN}0% failure rate${NC}"
fi

exit 0
