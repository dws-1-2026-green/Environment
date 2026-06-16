#!/usr/bin/env bash
# Entrypoint for the portable test image.
#
# Suites (first arg):
#   functional   — Go cases 1-4 (correctness), emit /reports/functional.html
#   load         — Go closed-loop load test (sink + reconcile), /reports/load.html
#   stress       — Go closed-loop stress test (ramping),        /reports/stress.html
#   all          — functional, then load, then stress
#   k6-load      — k6 ingestion-only load (HTTP percentiles),   /reports/k6-report.html
#   k6-stress    — k6 ingestion-only stress,                    /reports/k6-stress.html
#
# Extra args after the suite name pass straight through.
#
# NOTE: functional / load / stress need delivery-service to reach this
# container's webhook sink at E2E_CALLBACK_HOST:E2E_CALLBACK_PORT — publish the
# port (-p) and make it reachable from the cluster/network.
set -uo pipefail

SUITE="${1:-functional}"
shift || true

REPORTS_DIR="${REPORTS_DIR:-/reports}"
mkdir -p "$REPORTS_DIR"

# run_go <html-name> <go-test-run-regex> [extra test flags...]
run_go() {
  local name="$1"; shift
  local runexpr="$1"; shift
  local json="$REPORTS_DIR/${name}.json"
  local html="$REPORTS_DIR/${name}.html"
  local title="${REPORT_TITLE:-Тесты — система доставки вебхуков}"
  local fmt="${E2E_REPORT_FORMAT:-html}"   # html | text | both
  go tool test2json -t /usr/local/bin/suite.test \
    -test.v -test.timeout "${E2E_TEST_TIMEOUT:-40m}" -test.run "$runexpr" "$@" | tee "$json"
  local rc=${PIPESTATUS[0]}

  # Text report → straight into the logs (no file needed; ideal for k8s Jobs).
  if [ "$fmt" = "text" ] || [ "$fmt" = "both" ]; then
    report-gen -in "$json" -out - -format text -target "${E2E_EVENT_RECEIVER_URL:-}" -title "$title" || true
  fi
  # HTML report → file in /reports (copy out with kubectl cp or a mounted volume).
  if [ "$fmt" = "html" ] || [ "$fmt" = "both" ]; then
    report-gen -in "$json" -out "$html" -format html -target "${E2E_EVENT_RECEIVER_URL:-}" -title "$title" || true
    echo ">> $name → $html"
  fi
  echo ">> $name done (rc=$rc)"
  return "$rc"
}

run_functional() { REPORT_TITLE="Функциональные тесты (кейсы 1-6)" run_go functional '^TestCase' "$@"; }
run_load()       { REPORT_TITLE="Нагрузочный тест (closed-loop)" E2E_RUN_LOAD=true   run_go load   '^TestLoadClosedLoop'   "$@"; }
run_stress()     { REPORT_TITLE="Стресс-тест (closed-loop)" E2E_RUN_STRESS=true run_go stress '^TestStressClosedLoop' "$@"; }

run_k6_load() {
  echo ">> k6 load (case 5) → ${E2E_K6_REPORT_HTML}"
  k6 run "$@" /app/tests/load/case5_load.js
}
run_k6_stress() {
  echo ">> k6 stress (case 6) → ${E2E_K6_REPORT_HTML/k6-report/k6-stress}"
  E2E_K6_REPORT_HTML="${E2E_K6_REPORT_HTML/k6-report/k6-stress}" \
  E2E_K6_REPORT_JSON="${E2E_K6_REPORT_JSON/k6-summary/k6-stress-summary}" \
    k6 run "$@" /app/tests/load/case6_stress.js
}

rc=0
case "$SUITE" in
  functional) run_functional "$@" || rc=$? ;;
  load)       run_load "$@" || rc=$? ;;
  stress)     run_stress "$@" || rc=$? ;;
  k6-load)    run_k6_load "$@" || rc=$? ;;
  k6-stress)  run_k6_stress "$@" || rc=$? ;;
  all)
    run_functional || rc=1
    run_load || rc=1
    run_stress || rc=1
    ;;
  *)
    echo "unknown suite: $SUITE (functional | load | stress | all | k6-load | k6-stress)" >&2
    exit 2
    ;;
esac

exit "$rc"
