// L2 hot-event contention (shared-iterations) and L3 duplicates.
//
// MODE=single     : N users, one booking request each (L2).
// MODE=duplicates : N users, DUP parallel identical requests each (L3).
//
// RETRY=1 makes each request behave like a client that honours
// "event is busy, please retry" (retry on that 409 only, jittered 5-25 ms
// backoff, at most MAX_ATTEMPTS). RETRY=0 sends exactly one request.
import http from 'k6/http';
import exec from 'k6/execution';
import { book, bookParams, bookWithRetry, gaveUp, record, retryBackoff, summaryWriter, TREND_STATS, BASE } from './lib.js';

const N = parseInt(__ENV.N, 10);
const VUS = parseInt(__ENV.VUS || __ENV.N, 10);
const EVENT_ID = __ENV.EVENT_ID;
const MODE = __ENV.MODE || 'single';
const RETRY = __ENV.RETRY === '1';
const MAX_ATTEMPTS = parseInt(__ENV.MAX_ATTEMPTS || '200', 10);
const DUP = parseInt(__ENV.DUP || '5', 10);

export const options = {
  scenarios: {
    hot: { executor: 'shared-iterations', vus: VUS, iterations: N, maxDuration: '10m' },
  },
  // http.batch fires up to this many requests per host in parallel.
  batch: DUP,
  batchPerHost: DUP,
  summaryTrendStats: TREND_STATS,
  discardResponseBodies: false,
};

export default function () {
  const u = exec.scenario.iterationInTest; // user index, unique per iteration
  if (MODE === 'single') {
    if (RETRY) bookWithRetry(EVENT_ID, u, MAX_ATTEMPTS);
    else record(book(EVENT_ID, u));
    return;
  }
  if (RETRY) {
    // DUP concurrent logical bookings for the same user. k6 VUs are single
    // threaded, so parallelism comes from http.batch; busy responses are
    // re-batched together, after the same jittered backoff as bookWithRetry,
    // until none are busy or MAX_ATTEMPTS is reached.
    let pending = Array.from({ length: DUP }, (_, i) => i);
    for (let attempt = 1; pending.length > 0 && attempt <= MAX_ATTEMPTS; attempt++) {
      if (attempt > 1) retryBackoff();
      const reqs = pending.map(() => ['POST', `${BASE}/events/${EVENT_ID}/book`, null, bookParams(u)]);
      const res = http.batch(reqs);
      pending = pending.filter((_, i) => record(res[i]) === 'busy');
    }
    if (pending.length > 0) gaveUp.add(pending.length);
    return;
  }
  const reqs = Array.from({ length: DUP }, () => ['POST', `${BASE}/events/${EVENT_ID}/book`, null, bookParams(u)]);
  http.batch(reqs).forEach((r) => record(r));
}

export function handleSummary(data) {
  return summaryWriter(data);
}
