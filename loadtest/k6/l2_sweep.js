// L2 concurrency sweep on a hot event: a stepped ramp of VUs.
//
// Each stage runs LEVELS[i] VUs (constant-vus) for STAGE_S seconds in a
// closed loop. All VUs in a stage target the same event, so the per-event
// Redis lock serialises them; that is the behaviour being measured.
// Iteration k of stage i books user (k mod N) on event
// FIRST_EVENT + i*EVENTS_PER_STAGE + floor(k / N), so a (user, event) pair is
// never reused within a run unless a stage exceeds EVENTS_PER_STAGE * N
// iterations (that would show up as st_409_already_booked, not be hidden).
//
// RETRY=0: one request per iteration. RETRY=1: retry busy 409s (see lib.js).
import exec from 'k6/execution';
import { book, bookWithRetry, record, summaryWriter, TREND_STATS } from './lib.js';

const N = parseInt(__ENV.N || '1000', 10);
const LEVELS = (__ENV.LEVELS || '1,5,10,25,50,100,200').split(',').map(Number);
const STAGE_S = parseInt(__ENV.STAGE_S || '10', 10);
const GAP_S = 3;
const FIRST_EVENT = parseInt(__ENV.FIRST_EVENT || '1', 10);
const EVENTS_PER_STAGE = parseInt(__ENV.EVENTS_PER_STAGE || '200', 10);
const RETRY = __ENV.RETRY === '1';
const MAX_ATTEMPTS = parseInt(__ENV.MAX_ATTEMPTS || '200', 10);

const scenarios = {};
const thresholds = {};
const perStage = [
  'http_reqs', 'http_req_duration', 'book_201_duration', 'book_end_to_end_duration', 'book_attempts',
  'st_201', 'st_409_busy', 'st_409_already_booked', 'st_409_full', 'st_409_not_open', 'st_409_other',
  'st_429', 'st_5xx', 'st_timeout', 'st_network_error', 'st_other', 'iterations',
];
LEVELS.forEach((vus, i) => {
  const stage = `c${vus}`;
  scenarios[stage] = {
    executor: 'constant-vus',
    vus,
    duration: `${STAGE_S}s`,
    startTime: `${i * (STAGE_S + GAP_S)}s`,
    gracefulStop: '30s',
    tags: { stage },
    env: { STAGE_INDEX: String(i) },
  };
  // A always-true threshold forces k6 to export the per-stage submetric.
  for (const m of perStage) thresholds[`${m}{stage:${stage}}`] = m.startsWith('st_') || m === 'http_reqs' || m === 'iterations' ? ['count>=0'] : ['max>=0'];
});

export const options = { scenarios, thresholds, summaryTrendStats: TREND_STATS };

export default function () {
  const stageIndex = parseInt(__ENV.STAGE_INDEX, 10);
  const k = exec.scenario.iterationInTest;
  const user = k % N;
  const event = FIRST_EVENT + stageIndex * EVENTS_PER_STAGE + (Math.floor(k / N) % EVENTS_PER_STAGE);
  if (RETRY) bookWithRetry(event, user, MAX_ATTEMPTS);
  else record(book(event, user));
}

export function handleSummary(data) {
  return summaryWriter(data);
}
