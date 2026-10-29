// L9 graceful shutdown: a closed-loop booking burst across many events while
// run.sh sends SIGTERM to the app mid-run. Every response is printed as
//   R <start_ms> <end_ms> <outcome> <status> <k6_error_code>
// (host clock) so run.sh can count, for requests that were in flight when
// the signal was sent, how many completed and how many failed. After a
// transport error a VU pauses 100 ms so a dead server is not hammered with
// hundreds of thousands of refused connects.
import { sleep } from 'k6';
import exec from 'k6/execution';
import { book, record, summaryWriter, TREND_STATS } from './lib.js';

const N = parseInt(__ENV.N || '1000', 10);
const EVENTS = parseInt(__ENV.EVENTS || '50', 10);
const VUS = parseInt(__ENV.VUS || '50', 10);

export const options = {
  scenarios: { burst: { executor: 'constant-vus', vus: VUS, duration: __ENV.DURATION || '20s' } },
  summaryTrendStats: TREND_STATS,
};

export default function () {
  const k = exec.scenario.iterationInTest;
  const event = 1 + (k % EVENTS);
  const user = Math.floor(k / EVENTS) % N;
  const t0 = Date.now();
  const res = book(event, user);
  const kind = record(res);
  console.log(`R ${t0} ${Date.now()} ${kind} ${res.status} ${res.error_code}`);
  if (kind === 'netErr' || kind === 'timeout') sleep(0.1);
}

export function handleSummary(data) {
  return summaryWriter(data);
}
