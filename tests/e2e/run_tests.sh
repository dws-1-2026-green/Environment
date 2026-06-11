#!/usr/bin/env bash
# E2E test runner for demo.dws.sidey383.ru
# Usage:
#   ./run_tests.sh                    — runs all: basic + load + stress
#   ./run_tests.sh basic              — health + CRUD + verified E2E only
#   ./run_tests.sh load               — load test only (default 100 RPS, 5 min)
#   ./run_tests.sh load 50            — load test with custom RPS (50 RPS, 5 min)
#   ./run_tests.sh stress             — stress test only (default 100 RPS, 150 events)
#   ./run_tests.sh stress 200         — stress test with custom RPS (200 RPS, 3000 events)
#   ./run_tests.sh stress 200 3000    — stress test with custom RPS and event count
#   ./run_tests.sh stress 100 150 10m — stress test with custom RPS, events, timeout

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────

export E2E_EVENT_RECEIVER_URL="${E2E_EVENT_RECEIVER_URL:-http://demo.dws.sidey383.ru}"
export E2E_SUBSCRIPTIONS_URL="${E2E_SUBSCRIPTIONS_URL:-http://subscriptions.demo.dws.sidey383.ru}"
export E2E_BASIC_AUTH_USER="${E2E_BASIC_AUTH_USER:-admin}"
export E2E_BASIC_AUTH_PASS="${E2E_BASIC_AUTH_PASS:-tVbfSvowVeJXPqW3mvYf}"
export E2E_WEBHOOK_HOST="${E2E_WEBHOOK_HOST:-home.sidey383.ru}"
export E2E_WEBHOOK_PORT="${E2E_WEBHOOK_PORT:-8089}"  # stress uses 8090-8094

# Test mode RPS defaults
LOAD_RPS_DEFAULT=100
STRESS_RPS_DEFAULT=100
STRESS_EVENTS_DEFAULT=150
STRESS_TIMEOUT_DEFAULT="4m"

STRESS_BASE_PORT=8090
LOG_DIR="${LOG_DIR:-/tmp/e2e-logs}"
SUITE="${1:-all}"

# Parse RPS and other parameters
LOAD_RPS="${LOAD_RPS:-$LOAD_RPS_DEFAULT}"
STRESS_RPS="${STRESS_RPS:-$STRESS_RPS_DEFAULT}"
STRESS_EVENTS="${STRESS_EVENTS:-$STRESS_EVENTS_DEFAULT}"
STRESS_TIMEOUT="${STRESS_TIMEOUT:-$STRESS_TIMEOUT_DEFAULT}"

mkdir -p "$LOG_DIR"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
info() { echo -e "${YELLOW}[INFO]${NC} $*"; }

show_help() {
    cat << EOF
Usage: $0 [COMMAND] [RPS] [EVENTS] [TIMEOUT]

Commands:
  basic                     Run only basic tests
  load [RPS]                Run load test with specified RPS (default: $LOAD_RPS_DEFAULT)
  stress [RPS] [EVENTS] [TIMEOUT]
                            Run stress test with specified parameters
                            RPS: requests per second (default: $STRESS_RPS_DEFAULT)
                            EVENTS: number of events (default: $STRESS_EVENTS_DEFAULT)
                            TIMEOUT: wait timeout (default: $STRESS_TIMEOUT_DEFAULT)
  all                       Run all tests (basic + load + stress)

Environment variables:
  LOAD_RPS                  Default RPS for load test
  STRESS_RPS                Default RPS for stress test
  STRESS_EVENTS             Default event count for stress test
  STRESS_TIMEOUT            Default timeout for stress test
  E2E_EVENT_RECEIVER_URL    Override event receiver URL
  E2E_SUBSCRIPTIONS_URL     Override subscriptions URL
  E2E_BASIC_AUTH_USER       Basic auth username
  E2E_BASIC_AUTH_PASS       Basic auth password
  E2E_WEBHOOK_HOST          Webhook callback host
  E2E_WEBHOOK_PORT          Webhook callback port
  LOG_DIR                   Directory for test logs

Examples:
  $0 load                   # Load test with default 100 RPS
  $0 load 250               # Load test with 250 RPS
  $0 stress                 # Stress test with default 100 RPS, 150 events
  $0 stress 200             # Stress test with 200 RPS, default events
  $0 stress 200 3000        # Stress test with 200 RPS, 3000 events
  $0 stress 100 150 10m     # Stress test with 100 RPS, 150 events, 10m timeout
  LOAD_RPS=500 $0 load      # Load test with 500 RPS via env var
EOF
}

# ── Pre-flight: connectivity check with retries ───────────────────────────────

check_connectivity() {
    info "Checking connectivity to $E2E_EVENT_RECEIVER_URL ..."
    local attempts=0
    local max=5
    while (( attempts < max )); do
        code=$(curl -s -o /dev/null -w "%{http_code}" \
               --max-time 5 \
               -u "$E2E_BASIC_AUTH_USER:$E2E_BASIC_AUTH_PASS" \
               "$E2E_EVENT_RECEIVER_URL/health" 2>/dev/null || echo "000")
        if [[ "$code" == "200" ]]; then
            pass "Event Receiver reachable (HTTP $code)"
            return 0
        fi
        (( attempts++ ))
        info "Attempt $attempts/$max failed (HTTP $code), retrying in 5s..."
        sleep 5
    done
    fail "Event Receiver not reachable after $max attempts. Check your network."
    exit 1
}

check_subscriptions_api() {
    info "Checking connectivity to $E2E_SUBSCRIPTIONS_URL ..."
    local attempts=0
    local max=5
    while (( attempts < max )); do
        code=$(curl -s -o /dev/null -w "%{http_code}" \
               --max-time 10 \
               -u "$E2E_BASIC_AUTH_USER:$E2E_BASIC_AUTH_PASS" \
               "$E2E_SUBSCRIPTIONS_URL/api/v1/subscriptions" 2>/dev/null || echo "000")
        if [[ "$code" == "200" ]]; then
            pass "Subscriptions API reachable (HTTP $code)"
            return 0
        fi
        (( attempts++ ))
        info "Attempt $attempts/$max failed (HTTP $code), retrying in 5s..."
        sleep 5
    done
    fail "Subscriptions API not reachable after $max attempts."
    exit 1
}

# ── Test runners ─────────────────────────────────────────────────────────────

run_basic() {
    local log="$LOG_DIR/basic_$(date +%H%M%S).log"
    info "Running basic tests → $log"
    go test ./tests/e2e/... -v \
        -run "TestStagingHealthCheck|TestStagingEventReceiverPublishesEvent|TestStagingSubscriptionsAPICRUD|TestStagingEndToEndWebhookDelivery$|TestStagingEndToEndWebhookDeliveryVerified" \
        -timeout 3m 2>&1 | tee "$log"
    local rc=${PIPESTATUS[0]}
    [[ $rc -eq 0 ]] && pass "Basic tests passed" || fail "Basic tests failed (rc=$rc)"
    return $rc
}

run_load() {
    local rps="${1:-$LOAD_RPS}"
    local log="$LOG_DIR/load_${rps}rps_$(date +%H%M%S).log"
    info "Running load test (${rps} RPS, 5 min) → $log"
    info "Monitor scaling: kubectl get hpa -n webhooks -w"
    
    # Map RPS to appropriate test function
    local test_name=""
    case "$rps" in
        10)   test_name="TestStagingLoad_10RPS" ;;
        50)   test_name="TestStagingLoad_50RPS" ;;
        100)  test_name="TestStagingLoad_100RPS" ;;
        200)  test_name="TestStagingLoad_200RPS" ;;
        500)  test_name="TestStagingLoad_500RPS" ;;
        *)
            # For custom RPS values, we need to use a different approach
            info "Custom RPS $rps - using generic load test with -test.run flag"
            go test ./tests/e2e/... -v \
                -run "TestStagingLoad" \
                -timeout 20m \
                -args -rps="$rps" 2>&1 | tee "$log"
            local rc=${PIPESTATUS[0]}
            [[ $rc -eq 0 ]] && pass "Load test (${rps} RPS) passed" || fail "Load test (${rps} RPS) failed (rc=$rc)"
            return $rc
            ;;
    esac
    
    go test ./tests/e2e/... -v \
        -run "$test_name" \
        -timeout 20m 2>&1 | tee "$log"
    local rc=${PIPESTATUS[0]}
    [[ $rc -eq 0 ]] && pass "Load test (${rps} RPS) passed" || fail "Load test (${rps} RPS) failed (rc=$rc)"
    return $rc
}

run_stress() {
    local rps="${1:-$STRESS_RPS}"
    local events="${2:-$STRESS_EVENTS}"
    local timeout="${3:-$STRESS_TIMEOUT}"
    local log="$LOG_DIR/stress_${rps}rps_${events}ev_$(date +%H%M%S).log"
    
    info "Running stress test (${rps} RPS, ${events} events, timeout ${timeout}) → $log"
    info "Mocks on ports $STRESS_BASE_PORT-$((STRESS_BASE_PORT+4))"
    info "Monitor scaling: kubectl get hpa,scaledobject -n webhooks -w"
    
    # Map RPS to appropriate test function
    local test_name=""
    case "$rps" in
        10)   test_name="TestStagingStress_10RPS" ;;
        50)   test_name="TestStagingStress_50RPS" ;;
        100)  test_name="TestStagingStress_100RPS" ;;
        200)  test_name="TestStagingStress_200RPS" ;;
        500)  test_name="TestStagingStress_500RPS" ;;
        *)
            info "Custom RPS $rps - using generic stress test with parameters"
            export STRESS_CUSTOM_RPS="$rps"
            export STRESS_CUSTOM_EVENTS="$events"
            export STRESS_CUSTOM_TIMEOUT="$timeout"
            E2E_WEBHOOK_PORT=$STRESS_BASE_PORT \
            go test ./tests/e2e/... -v \
                -run "TestStagingStress" \
                -timeout 30m 2>&1 | tee "$log"
            local rc=${PIPESTATUS[0]}
            [[ $rc -eq 0 ]] && pass "Stress test (${rps} RPS, ${events} events) passed" || fail "Stress test (${rps} RPS, ${events} events) failed (rc=$rc)"
            return $rc
            ;;
    esac
    
    E2E_WEBHOOK_PORT=$STRESS_BASE_PORT \
    go test ./tests/e2e/... -v \
        -run "$test_name" \
        -timeout 30m 2>&1 | tee "$log"
    local rc=${PIPESTATUS[0]}
    [[ $rc -eq 0 ]] && pass "Stress test (${rps} RPS) passed" || fail "Stress test (${rps} RPS) failed (rc=$rc)"
    return $rc
}

# ── Main ──────────────────────────────────────────────────────────────────────

# Parse command line arguments
case "$SUITE" in
    help|-h|--help)
        show_help
        exit 0
        ;;
    basic)
        # No additional params
        ;;
    load)
        # Optional RPS parameter
        if [[ $# -ge 2 ]]; then
            LOAD_RPS="$2"
        fi
        ;;
    stress)
        # Optional RPS, events, timeout parameters
        if [[ $# -ge 2 ]]; then
            STRESS_RPS="$2"
        fi
        if [[ $# -ge 3 ]]; then
            STRESS_EVENTS="$3"
        fi
        if [[ $# -ge 4 ]]; then
            STRESS_TIMEOUT="$4"
        fi
        ;;
    all)
        # Optional RPS parameters for load and stress
        if [[ $# -ge 2 ]]; then
            LOAD_RPS="$2"
        fi
        if [[ $# -ge 3 ]]; then
            STRESS_RPS="$3"
        fi
        if [[ $# -ge 4 ]]; then
            STRESS_EVENTS="$4"
        fi
        if [[ $# -ge 5 ]]; then
            STRESS_TIMEOUT="$5"
        fi
        ;;
esac

echo "========================================================"
echo "  E2E Test Suite — $(date)"
echo "  Event Receiver : $E2E_EVENT_RECEIVER_URL"
echo "  Subscriptions  : $E2E_SUBSCRIPTIONS_URL"
echo "  Webhook host   : $E2E_WEBHOOK_HOST"
echo "  Suite          : $SUITE"
[[ "$SUITE" == "load" || "$SUITE" == "all" ]] && echo "  Load RPS       : $LOAD_RPS"
[[ "$SUITE" == "stress" || "$SUITE" == "all" ]] && echo "  Stress RPS     : $STRESS_RPS"
[[ "$SUITE" == "stress" || "$SUITE" == "all" ]] && echo "  Stress events  : $STRESS_EVENTS"
[[ "$SUITE" == "stress" || "$SUITE" == "all" ]] && echo "  Stress timeout : $STRESS_TIMEOUT"
echo "  Logs           : $LOG_DIR"
echo "========================================================"

check_connectivity

overall=0

case "$SUITE" in
    basic)
        check_subscriptions_api
        run_basic || overall=1
        ;;
    load)
        run_load "$LOAD_RPS" || overall=1
        ;;
    stress)
        check_subscriptions_api
        run_stress "$STRESS_RPS" "$STRESS_EVENTS" "$STRESS_TIMEOUT" || overall=1
        ;;
    all)
        check_subscriptions_api

        info "--- Phase 1: basic tests ---"
        run_basic || overall=1

        info "--- Phase 2: load test (background) with ${LOAD_RPS} RPS ---"
        run_load "$LOAD_RPS" &
        load_pid=$!

        # Give load test 30s head-start before adding stress on top
        sleep 30

        info "--- Phase 3: stress test with ${STRESS_RPS} RPS, ${STRESS_EVENTS} events ---"
        run_stress "$STRESS_RPS" "$STRESS_EVENTS" "$STRESS_TIMEOUT" || overall=1

        info "--- Waiting for load test (PID $load_pid) ---"
        wait $load_pid || overall=1
        ;;
    *)
        echo "Unknown suite: $SUITE. Use: basic | load | stress | all | help"
        exit 1
        ;;
esac

echo "========================================================"
if [[ $overall -eq 0 ]]; then
    pass "All tests finished successfully"
else
    fail "Some tests failed — check logs in $LOG_DIR"
fi
echo "Logs saved to: $LOG_DIR"
exit $overall