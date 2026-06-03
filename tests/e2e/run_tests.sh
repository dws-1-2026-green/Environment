#!/usr/bin/env bash
# E2E test runner for demo.dws.sidey383.ru
# Usage:
#   ./run_tests.sh          — runs all: basic + load + stress
#   ./run_tests.sh basic    — health + CRUD + verified E2E only
#   ./run_tests.sh load     — load test only (50 RPS, 5 min)
#   ./run_tests.sh stress   — stress test only (10 RPS, 150 events)

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────

export E2E_EVENT_RECEIVER_URL="${E2E_EVENT_RECEIVER_URL:-http://demo.dws.sidey383.ru}"
export E2E_SUBSCRIPTIONS_URL="${E2E_SUBSCRIPTIONS_URL:-http://subscriptions.demo.dws.sidey383.ru}"
export E2E_BASIC_AUTH_USER="${E2E_BASIC_AUTH_USER:-admin}"
export E2E_BASIC_AUTH_PASS="${E2E_BASIC_AUTH_PASS:-tVbfSvowVeJXPqW3mvYf}"
export E2E_WEBHOOK_HOST="${E2E_WEBHOOK_HOST:-home.sidey383.ru}"
export E2E_WEBHOOK_PORT="${E2E_WEBHOOK_PORT:-8089}"  # stress uses 8090-8094

STRESS_BASE_PORT=8090
LOG_DIR="${LOG_DIR:-/tmp/e2e-logs}"
SUITE="${1:-all}"

mkdir -p "$LOG_DIR"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
info() { echo -e "${YELLOW}[INFO]${NC} $*"; }

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
    local log="$LOG_DIR/load_$(date +%H%M%S).log"
    info "Running load test (500 RPS, 5 min) → $log"
    info "Monitor scaling: kubectl get hpa -n webhooks -w"
    go test ./tests/e2e/... -v \
        -run "TestStagingLoad_500RPS" \
        -timeout 20m 2>&1 | tee "$log"
    local rc=${PIPESTATUS[0]}
    [[ $rc -eq 0 ]] && pass "Load test passed" || fail "Load test failed (rc=$rc)"
    return $rc
}

run_stress() {
    local log="$LOG_DIR/stress_$(date +%H%M%S).log"
    info "Running stress test (500 RPS, 7500 events, mocks on ports $STRESS_BASE_PORT-$((STRESS_BASE_PORT+4))) → $log"
    info "Monitor scaling: kubectl get hpa,scaledobject -n webhooks -w"
    E2E_WEBHOOK_PORT=$STRESS_BASE_PORT \
    go test ./tests/e2e/... -v \
        -run "TestStagingStress_500RPS" \
        -timeout 30m 2>&1 | tee "$log"
    local rc=${PIPESTATUS[0]}
    [[ $rc -eq 0 ]] && pass "Stress test passed" || fail "Stress test failed (rc=$rc)"
    return $rc
}

# ── Main ──────────────────────────────────────────────────────────────────────

echo "========================================================"
echo "  E2E Test Suite — $(date)"
echo "  Event Receiver : $E2E_EVENT_RECEIVER_URL"
echo "  Subscriptions  : $E2E_SUBSCRIPTIONS_URL"
echo "  Webhook host   : $E2E_WEBHOOK_HOST"
echo "  Suite          : $SUITE"
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
        run_load || overall=1
        ;;
    stress)
        check_subscriptions_api
        run_stress || overall=1
        ;;
    all)
        check_subscriptions_api

        info "--- Phase 1: basic tests ---"
        run_basic || overall=1

        info "--- Phase 2: load test (background) ---"
        run_load &
        load_pid=$!

        # Give load test 30s head-start before adding stress on top
        sleep 30

        info "--- Phase 3: stress test ---"
        run_stress || overall=1

        info "--- Waiting for load test (PID $load_pid) ---"
        wait $load_pid || overall=1
        ;;
    *)
        echo "Unknown suite: $SUITE. Use: basic | load | stress | all"
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
