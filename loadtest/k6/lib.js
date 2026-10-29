// Shared helpers for the booking scenarios.
import http from 'k6/http';
import { sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { Counter, Trend } from 'k6/metrics';

export const BASE = __ENV.BASE_URL || 'http://127.0.0.1:18080';

// Tokens come from loadtest/.state/tokens.json (written by cmd/setup-users,
// gitignored). Loaded once in the init context, so no login happens during
// the measured phase.
const tokens = new SharedArray('users', () => JSON.parse(open(__ENV.TOKENS)).users);
export function userToken(i) {
  return tokens[i].token;
}
export function userCount() {
  return tokens.length;
}

export const TREND_STATS = ['min', 'med', 'avg', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'];

// One counter per outcome, so the summary JSON carries the full status-code
// distribution. 409 is split by the error body because the API uses 409 for
// four different reasons (internal/booking/handler.go).
export const outcomes = {
  s201: new Counter('st_201'),
  busy: new Counter('st_409_busy'),
  already: new Counter('st_409_already_booked'),
  full: new Counter('st_409_full'),
  notOpen: new Counter('st_409_not_open'),
  other409: new Counter('st_409_other'),
  s429: new Counter('st_429'),
  s5xx: new Counter('st_5xx'),
  timeout: new Counter('st_timeout'),
  netErr: new Counter('st_network_error'),
  other: new Counter('st_other'),
};
export const bookOk = new Trend('book_201_duration', true);

export function classify(res) {
  if (res.status === 0) {
    // k6 error code 1050 = request timeout; anything else is a transport error.
    return res.error_code === 1050 ? 'timeout' : 'netErr';
  }
  if (res.status === 201) return 's201';
  if (res.status === 429) return 's429';
  if (res.status >= 500) return 's5xx';
  if (res.status === 409) {
    const body = res.body || '';
    if (body.includes('event is busy')) return 'busy';
    if (body.includes('already booked')) return 'already';
    if (body.includes('fully booked')) return 'full';
    if (body.includes('not open for booking')) return 'notOpen';
    return 'other409';
  }
  return 'other';
}

export function record(res, tags) {
  const kind = classify(res);
  outcomes[kind].add(1, tags);
  if (kind === 's201') bookOk.add(res.timings.duration, tags);
  return kind;
}

export function bookParams(userIdx, tags) {
  return {
    headers: { Authorization: `Bearer ${userToken(userIdx)}` },
    tags: Object.assign({ name: 'POST /events/{id}/book' }, tags || {}),
    timeout: __ENV.REQ_TIMEOUT || '30s',
  };
}

export function book(eventId, userIdx, tags) {
  return http.post(`${BASE}/events/${eventId}/book`, null, bookParams(userIdx, tags));
}

// bookWithRetry models a client that honours "event is busy, please retry":
// it retries only on the busy 409, with jittered backoff, up to maxAttempts.
// Returns the final outcome kind and the number of attempts.
export const attempts = new Trend('book_attempts');
// Logical bookings that were still "busy" after maxAttempts (client gave up).
export const gaveUp = new Counter('st_gave_up_after_max_attempts');
export function retryBackoff() {
  sleep(0.005 + Math.random() * 0.02);
}
export const e2e = new Trend('book_end_to_end_duration', true);
export function bookWithRetry(eventId, userIdx, maxAttempts, tags) {
  const start = Date.now();
  let kind = 'busy';
  let n = 0;
  while (kind === 'busy' && n < maxAttempts) {
    n++;
    kind = record(book(eventId, userIdx, tags), tags);
    if (kind === 'busy') retryBackoff();
  }
  if (kind === 'busy') gaveUp.add(1, tags);
  attempts.add(n, tags);
  e2e.add(Date.now() - start, tags);
  return kind;
}

export function summaryWriter(data) {
  const out = {};
  if (__ENV.SUMMARY_OUT) out[__ENV.SUMMARY_OUT] = JSON.stringify(data, null, 2);
  out.stdout = compactText(data);
  return out;
}

function compactText(data) {
  const lines = [`testRunDurationMs=${data.state.testRunDurationMs}`];
  for (const [name, m] of Object.entries(data.metrics).sort()) {
    const v = Object.entries(m.values)
      .map(([k, x]) => `${k}=${typeof x === 'number' ? +x.toFixed(3) : x}`)
      .join(' ');
    lines.push(`${name}: ${v}`);
  }
  return lines.join('\n') + '\n';
}
