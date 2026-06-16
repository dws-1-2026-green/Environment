// Shared, env-driven configuration for the k6 load & stress scripts.
//
// Everything is overridable from the environment (k6 run -e KEY=VALUE ...),
// so the same script and the same Docker image run unchanged against
// docker-compose, an in-cluster deployment, or staging.

import encoding from 'k6/encoding';

// Local uuid v4 (no remote jslib import — keeps the image fully offline).
export function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

function env(key, def) {
  const v = __ENV[key];
  return v === undefined || v === '' ? def : v;
}

function envInt(key, def) {
  const v = parseInt(env(key, ''), 10);
  return Number.isNaN(v) ? def : v;
}

export const CONFIG = {
  // Target
  baseURL: env('E2E_EVENT_RECEIVER_URL', 'http://localhost:8080'),

  // Basic auth (empty user => no Authorization header)
  authUser: env('E2E_BASIC_AUTH_USER', ''),
  authPass: env('E2E_BASIC_AUTH_PASS', ''),

  // Which sources / event types to spread traffic across. These should already
  // have subscriptions registered (see tests/k8s/mock-setup.sh or compose).
  sources: env('E2E_SOURCES', 'load-test').split(',').map((s) => s.trim()),
  eventTypes: env('E2E_EVENT_TYPES', 'order.created').split(',').map((s) => s.trim()),

  // Acceptance: HTTP statuses meaning "accepted" (local=200, staging=202).
  acceptStatuses: env('E2E_ACCEPT_STATUSES', '200,202')
    .split(',')
    .map((s) => parseInt(s.trim(), 10)),

  // --- Load knobs (case5) ---
  loadRPS: envInt('E2E_LOAD_RPS', 100),
  loadDuration: env('E2E_LOAD_DURATION', '5m'),
  loadPreVUs: envInt('E2E_LOAD_PRE_VUS', 50),
  loadMaxVUs: envInt('E2E_LOAD_MAX_VUS', 500),

  // --- Stress knobs (case6): ramp from START to PEAK over STAGES ---
  stressStartRPS: envInt('E2E_STRESS_START_RPS', 50),
  stressPeakRPS: envInt('E2E_STRESS_PEAK_RPS', 1000),
  stressStageDuration: env('E2E_STRESS_STAGE', '1m'),
  stressMaxVUs: envInt('E2E_STRESS_MAX_VUS', 2000),

  // --- Thresholds (used to mark the run pass/fail) ---
  thrP95: envInt('E2E_THRESHOLD_P95_MS', 200),
  thrP99: envInt('E2E_THRESHOLD_P99_MS', 500),
  thrErrorRate: parseFloat(env('E2E_THRESHOLD_ERROR_RATE', '0.01')),

  // Output report paths
  reportHTML: env('E2E_K6_REPORT_HTML', 'k6-report.html'),
  reportJSON: env('E2E_K6_REPORT_JSON', 'k6-summary.json'),
};

// authParams returns k6 request params with Content-Type and optional basic auth.
export function authParams(tags) {
  const headers = { 'Content-Type': 'application/json' };
  if (CONFIG.authUser !== '') {
    const token = encoding.b64encode(`${CONFIG.authUser}:${CONFIG.authPass}`);
    headers['Authorization'] = `Basic ${token}`;
  }
  return { headers, tags: tags || {} };
}

// buildEvent returns [url, body] for a randomly chosen source/event type,
// matching the documented event-receiver request format.
export function buildEvent() {
  const source = CONFIG.sources[Math.floor(Math.random() * CONFIG.sources.length)];
  const eventType = CONFIG.eventTypes[Math.floor(Math.random() * CONFIG.eventTypes.length)];
  const url = `${CONFIG.baseURL}/sources/${source}/events`;
  const body = JSON.stringify({
    id: uuidv4(),
    type: eventType,
    created_at: new Date().toISOString(),
    data: {
      order_id: `${Math.floor(Math.random() * 1000000)}`,
      amount: 1990,
      currency: 'RUB',
    },
  });
  return [url, body];
}

// isAccepted reports whether a status code counts as accepted.
export function isAccepted(status) {
  return CONFIG.acceptStatuses.indexOf(status) !== -1;
}

// thresholds builds the k6 thresholds object from CONFIG.
export function thresholds() {
  return {
    http_req_failed: [`rate<${CONFIG.thrErrorRate}`],
    http_req_duration: [`p(95)<${CONFIG.thrP95}`, `p(99)<${CONFIG.thrP99}`],
  };
}
