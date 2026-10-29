// L4: CONCURRENCY simultaneous POST /events/{id}/draw for one closed event.
// per-vu-iterations with 1 iteration per VU: all VUs start together.
// The body of every 200 response is printed (marker DRAW_200_BODY) so
// run.sh can compare it with the database and with GET /results.
import http from 'k6/http';
import { Counter } from 'k6/metrics';
import { summaryWriter, TREND_STATS, BASE } from './lib.js';

const ADMIN = JSON.parse(open(__ENV.TOKENS)).admin;
const CONCURRENCY = parseInt(__ENV.CONCURRENCY || '20', 10);

const st200 = new Counter('draw_200');
const already = new Counter('draw_409_already_drawn');
const notDue = new Counter('draw_409_not_due');
const other409 = new Counter('draw_409_other');
const s5xx = new Counter('draw_5xx');
const other = new Counter('draw_other');

export const options = {
  scenarios: { draws: { executor: 'per-vu-iterations', vus: CONCURRENCY, iterations: 1, maxDuration: '2m' } },
  summaryTrendStats: TREND_STATS,
};

export default function () {
  const res = http.post(`${BASE}/events/${__ENV.EVENT_ID}/draw`, null, {
    headers: { Authorization: `Bearer ${ADMIN}` },
    timeout: '60s',
  });
  if (res.status === 200) {
    st200.add(1);
    console.log(`DRAW_200_BODY ${res.body.trim()}`);
  } else if (res.status === 409 && res.body.includes('"already_drawn"')) already.add(1);
  else if (res.status === 409 && res.body.includes('"draw_not_due"')) notDue.add(1);
  else if (res.status === 409) other409.add(1);
  else if (res.status >= 500) s5xx.add(1);
  else {
    other.add(1);
    console.log(`DRAW_OTHER status=${res.status} body=${res.body}`);
  }
}

export function handleSummary(data) {
  return summaryWriter(data);
}
