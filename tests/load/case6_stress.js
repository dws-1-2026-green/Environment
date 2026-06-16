// Case 6 — Стресс-тест.
//
// Ramps the arrival rate from START up to PEAK in steps, holding each step,
// to find where the system starts to degrade (latency climbs, errors appear,
// Kafka lag grows). Measures latency, throughput and error rate throughout.
//
// Configurable via env (see tests/load/lib/config.js):
//   E2E_STRESS_START_RPS (50), E2E_STRESS_PEAK_RPS (1000),
//   E2E_STRESS_STAGE (1m, duration of each ramp step),
//   E2E_STRESS_MAX_VUS (2000)
//
// Run:
//   k6 run -e E2E_EVENT_RECEIVER_URL=http://staging.dws.sidey383.ru \
//          -e E2E_STRESS_PEAK_RPS=2000 -e E2E_STRESS_STAGE=90s \
//          tests/load/case6_stress.js

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { CONFIG, authParams, buildEvent, isAccepted, thresholds } from './lib/config.js';
import { htmlReport, textSummary } from './lib/report.js';

const accepted = new Counter('accepted_events');
const rejected = new Counter('rejected_events');

// Build a staircase from start → peak in 4 equal steps, then ramp down.
function buildStages() {
  const start = CONFIG.stressStartRPS;
  const peak = CONFIG.stressPeakRPS;
  const step = CONFIG.stressStageDuration;
  const levels = [
    Math.round(start + (peak - start) * 0.25),
    Math.round(start + (peak - start) * 0.5),
    Math.round(start + (peak - start) * 0.75),
    peak,
  ];
  const stages = levels.map((target) => ({ target, duration: step }));
  stages.push({ target: 0, duration: '30s' }); // ramp down
  return stages;
}

export const options = {
  scenarios: {
    ramping_stress: {
      executor: 'ramping-arrival-rate',
      startRate: CONFIG.stressStartRPS,
      timeUnit: '1s',
      stages: buildStages(),
      preAllocatedVUs: Math.min(200, CONFIG.stressMaxVUs),
      maxVUs: CONFIG.stressMaxVUs,
    },
  },
  // Thresholds here do not abort the run; they mark pass/fail in the report so
  // you can see at which point the system breached the SLO.
  thresholds: thresholds(),
};

export function setup() {
  console.log(
    `[case6/stress] target=${CONFIG.baseURL} ramp ${CONFIG.stressStartRPS}→${CONFIG.stressPeakRPS} rps ` +
      `step=${CONFIG.stressStageDuration} maxVUs=${CONFIG.stressMaxVUs}`
  );
}

export default function () {
  const [url, body] = buildEvent();
  const res = http.post(url, body, authParams({ scenario: 'stress' }));

  check(res, {
    'accepted (200/202)': (r) => isAccepted(r.status),
  });

  if (isAccepted(res.status)) accepted.add(1);
  else rejected.add(1);
}

export function handleSummary(data) {
  const meta = {
    title: 'Case 6 — Стресс-тест (ramping RPS)',
    target: CONFIG.baseURL,
    scenario: `${CONFIG.stressStartRPS}→${CONFIG.stressPeakRPS} rps`,
  };
  return {
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
    [CONFIG.reportHTML]: htmlReport(data, meta),
    [CONFIG.reportJSON]: JSON.stringify(data, null, 2),
  };
}
