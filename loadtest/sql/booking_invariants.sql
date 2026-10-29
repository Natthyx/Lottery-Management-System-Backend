-- Booking invariants over the whole (freshly reset) database.
-- Each check returns one row: check name, observed value, pass/fail.
-- Optional: -v expected_bookings=<n>  (the 201 count k6 observed; pass -1 to skip)

\if :{?expected_bookings}
\else
  \set expected_bookings -1
\endif

SELECT 'per_event' AS section, e.id AS event_id, e.capacity, count(b.id) AS bookings,
       count(DISTINCT b.user_id) AS distinct_users
FROM events e LEFT JOIN bookings b ON b.event_id = e.id
GROUP BY e.id HAVING count(b.id) > 0
ORDER BY e.id
LIMIT 20;

WITH dup AS (
  SELECT event_id, user_id, count(*) c FROM bookings GROUP BY 1, 2 HAVING count(*) > 1
), over AS (
  SELECT e.id FROM events e JOIN bookings b ON b.event_id = e.id
  GROUP BY e.id, e.capacity HAVING count(*) > e.capacity
), tot AS (SELECT count(*) n FROM bookings)
SELECT 'duplicate (event,user) pairs' AS check, (SELECT count(*) FROM dup)::text AS observed,
       CASE WHEN (SELECT count(*) FROM dup) = 0 THEN 'PASS' ELSE 'FAIL' END AS result
UNION ALL
SELECT 'events over capacity', (SELECT count(*) FROM over)::text,
       CASE WHEN (SELECT count(*) FROM over) = 0 THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'total bookings vs client-observed 201s',
       (SELECT n FROM tot) || ' vs ' || :expected_bookings,
       CASE WHEN :expected_bookings < 0 THEN 'SKIPPED'
            WHEN (SELECT n FROM tot) = :expected_bookings THEN 'PASS' ELSE 'FAIL' END;
