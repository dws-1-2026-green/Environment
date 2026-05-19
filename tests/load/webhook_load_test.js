import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

/**
 * k6 Load Test for EventReceiver
 *
 * This script simulates multiple scenarios to test the distributed WebHook delivery engine.
 * Run with: k6 run webhook_load_test.js
 * Or override BASE_URL: k6 run -e BASE_URL=http://your-server:8080 webhook_load_test.js
 */

export const options = {
    scenarios: {
        // Scenario 1: Smoke Test
        // Goal: Verify the system is alive and handles a small baseline load.
        smoke_test: {
            executor: 'constant-arrival-rate',
            rate: 10,
            timeUnit: '1s',
            duration: '30s',
            preAllocatedVUs: 5,
            maxVUs: 20,
        },

        // Scenario 2: Ramping Load (Stress Test)
        // Goal: Observe how the system (API -> Kafka) scales under increasing pressure.
        stress_test: {
            executor: 'ramping-arrival-rate',
            startTime: '40s', // Start after smoke test finishes
            startRate: 20,
            timeUnit: '1s',
            stages: [
                { target: 100, duration: '1m' },  // Ramp up to 100 RPS
                { target: 100, duration: '2m' },  // Sustain 100 RPS
                { target: 400, duration: '1m' },  // Spike to 400 RPS (Pressure Kafka Producer)
                { target: 0, duration: '30s' },   // Ramp down
            ],
            preAllocatedVUs: 50,
            maxVUs: 300,
        },

        // Scenario 3: High Concurrency / Low Latency
        // Goal: Test connection handling and goroutine efficiency.
        concurrency_test: {
            executor: 'constant-vus',
            startTime: '5m',
            vus: 100,
            duration: '1m',
        }
    },
    thresholds: {
        // Fail the test if more than 1% of requests fail
        'http_req_failed': ['rate<0.01'],
        // 95% of requests should be processed by the API (sent to Kafka) within 200ms
        // 99% should be within 500ms (accounting for Kafka producer buffer flushes)
        'http_req_duration': ['p(95)<200', 'p(99)<500'],
    },
};

// Configuration
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const SOURCE_NAMES = ['ecommerce-app', 'payment-gateway', 'auth-service', 'inventory-monitor'];
const EVENT_TYPES = ['order.created', 'payment.success', 'user.login', 'stock.low'];
// For simple setup in event-receiver app:
// const SOURCE_NAMES = ['app1'];
// const EVENT_TYPES = ['test.event'];


export default function () {
    const sourceName = SOURCE_NAMES[Math.floor(Math.random() * SOURCE_NAMES.length)];
    const eventType = EVENT_TYPES[Math.floor(Math.random() * EVENT_TYPES.length)];
    const url = `${BASE_URL}/sources/${sourceName}/events`;

    // Matches the models.APIRequest structure in EventReceiver
    const payload = JSON.stringify({
        id: uuidv4(),
        type: eventType,
        created_at: new Date().toISOString(),
        data: {
            load_test: true,
            version: '1.0',
            payload_size: 'small',
            attributes: {
                priority: Math.random() > 0.8 ? 'high' : 'normal',
                internal_id: Math.floor(Math.random() * 1000000)
            }
        }
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
        },
    };

    const res = http.post(url, payload, params);

    // Validation
    const checkResult = check(res, {
        'status is 200': (r) => r.status === 200,
        'is json response': (r) => r.headers['Content-Type'] && r.headers['Content-Type'].includes('application/json'),
        'status field is OK': (r) => {
            try { return r.json().status === 'OK'; } catch (e) { return false; }
        },
        'message field is correct': (r) => {
            try { return r.json().message === 'Event sended'; } catch (e) { return false; }
        }
    });

    // If we get 500s, it's likely Kafka backpressure or producer failure
    if (res.status === 500) {
        console.error(`Internal Error for source ${sourceName}: ${res.body}`);
    }

    // Optional: add a tiny sleep for the VUs scenario to prevent tight-looping
    // sleep(0.01);
}
