// L7 read load: GET /events, GET /events/{id}, GET /events/{id}/results at a
// constant arrival rate (open model: new requests start on schedule whether
// or not earlier ones finished). RATE is the TOTAL rate, split evenly.
import http from 'k6/http';
import { Counter } from 'k6/metrics';
import { summaryWriter, TREND_STATS, BASE } from './lib.js';

// Status-class distribution (http_req_failed alone cannot tell a timeout
// from a 5xx).
const cls = {
  ok: new Counter('st_2xx'),
  s4xx: new Counter('st_4xx'),
  s5xx: new Counter('st_5xx'),
  timeout: new Counter('st_timeout'),
  netErr: new Counter('st_network_error'),
};

const RATE = parseInt(__ENV.RATE || '300', 10);
const DURATION = __ENV.DURATION || '60s';
const EVENT_ID = __ENV.EVENT_ID; // a drawn event, so /results returns 200

const endpoints = {
  list: { url: `${BASE}/events`, name: 'GET /events' },
  get: { url: `${BASE}/events/${EVENT_ID}`, name: 'GET /events/{id}' },
  results: { url: `${BASE}/events/${EVENT_ID}/results`, name: 'GET /events/{id}/results' },
};

const scenarios = {};
const thresholds = { http_req_failed: ['rate>=0'], dropped_iterations: ['count>=0'] };
for (const key of Object.keys(endpoints)) {
  scenarios[key] = {
    executor: 'constant-arrival-rate',
    rate: Math.round(RATE / 3),
    timeUnit: '1s',
    duration: DURATION,
    preAllocatedVUs: 50,
    maxVUs: 1000,
    exec: 'read',
    env: { EP: key },
    tags: { ep: key },
  };
  thresholds[`http_req_duration{ep:${key}}`] = ['max>=0'];
  thresholds[`http_req_failed{ep:${key}}`] = ['rate>=0'];
  thresholds[`http_reqs{ep:${key}}`] = ['count>=0'];
  thresholds[`data_received{ep:${key}}`] = ['count>=0'];
}

export const options = { scenarios, thresholds, summaryTrendStats: TREND_STATS };

export function read() {
  const ep = endpoints[__ENV.EP];
  const res = http.get(ep.url, { tags: { name: ep.name }, timeout: '30s' });
  if (res.status === 0) (res.error_code === 1050 ? cls.timeout : cls.netErr).add(1);
  else if (res.status >= 500) cls.s5xx.add(1);
  else if (res.status >= 400) cls.s4xx.add(1);
  else cls.ok.add(1);
}

export function handleSummary(data) {
  return summaryWriter(data);
}
