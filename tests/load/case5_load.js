// Case 5 — Нагрузочный тест.
//
// Holds a constant target RPS for a fixed duration and measures latency,
// throughput and error rate of the event-receiver ingestion path.
// Everything is configurable via env (see tests/load/lib/config.js):
//
//   E2E_EVENT_RECEIVER_URL, E2E_BASIC_AUTH_USER/PASS,
//   E2E_LOAD_RPS (default 100), E2E_LOAD_DURATION (default 5m),
//   E2E_THRESHOLD_P95_MS, E2E_THRESHOLD_P99_MS, E2E_THRESHOLD_ERROR_RATE
//
// Run:
//   k6 run -e E2E_EVENT_RECEIVER_URL=http://staging.dws.sidey383.ru \
//          -e E2E_LOAD_RPS=200 -e E2E_LOAD_DURATION=10m \
//          tests/load/case5_load.js

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { CONFIG, authParams, buildEvent, isAccepted, thresholds } from './lib/config.js';
import { htmlReport, textSummary } from './lib/report.js';

const accepted = new Counter('accepted_events');
const rejected = new Counter('rejected_events');

export const options = {
  scenarios: {
    constant_load: {
      executor: 'constant-arrival-rate',
      rate: CONFIG.loadRPS,
      timeUnit: '1s',
      duration: CONFIG.loadDuration,
      preAllocatedVUs: CONFIG.loadPreVUs,
      maxVUs: CONFIG.loadMaxVUs,
    },
  },
  thresholds: thresholds(),
};

export function setup() {
  console.log(
    `[case5/load] target=${CONFIG.baseURL} rps=${CONFIG.loadRPS} ` +
      `duration=${CONFIG.loadDuration} sources=${CONFIG.sources.join(',')}`
  );
}

export default function () {
  const [url, body] = buildEvent();
  const res = http.post(url, body, authParams({ scenario: 'load' }));

  const ok = check(res, {
    'accepted (200/202)': (r) => isAccepted(r.status),
    'status field OK': (r) => {
      try {
        return r.json().status === 'OK';
      } catch (e) {
        return true; // not all deployments echo a body; don't penalise
      }
    },
  });

  if (isAccepted(res.status)) accepted.add(1);
  else {
    rejected.add(1);
    if (res.status >= 500) console.error(`5xx from ${url}: ${res.status} ${res.body}`);
  }
  return ok;
}

export function handleSummary(data) {
  const meta = {
    title: 'Case 5 — Нагрузочный тест (constant RPS)',
    target: CONFIG.baseURL,
    scenario: `${CONFIG.loadRPS} rps × ${CONFIG.loadDuration}`,
  };
  return {
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
    [CONFIG.reportHTML]: htmlReport(data, meta),
    [CONFIG.reportJSON]: JSON.stringify(data, null, 2),
  };
}
