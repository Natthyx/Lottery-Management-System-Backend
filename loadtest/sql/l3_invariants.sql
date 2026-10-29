-- L3: after every user fired the same booking several times in parallel,
-- exactly one booking per user must exist for the event.
-- Requires -v event_id=<id> -v n_users=<N>
-- Optional -v require_exactly_one=0 for the no-retry variant: there a user
-- whose requests all got "event is busy" legitimately has zero bookings, so
-- the checked property is "at most one" and zero-booking users are counted.
\if :{?require_exactly_one}
\else
  \set require_exactly_one 1
\endif

SELECT bookings_per_user, count(*) AS users
FROM (
  SELECT u.id, count(b.id) AS bookings_per_user
  FROM (SELECT id FROM users WHERE email LIKE 'lt-user-%' ORDER BY id LIMIT :n_users) u
  LEFT JOIN bookings b ON b.user_id = u.id AND b.event_id = :event_id
  GROUP BY u.id
) x
GROUP BY 1 ORDER BY 1;

WITH per_user AS (
  SELECT u.id, count(b.id) AS c
  FROM (SELECT id FROM users WHERE email LIKE 'lt-user-%' ORDER BY id LIMIT :n_users) u
  LEFT JOIN bookings b ON b.user_id = u.id AND b.event_id = :event_id
  GROUP BY u.id
)
SELECT CASE WHEN :require_exactly_one = 1 THEN 'users with exactly one booking'
            ELSE 'users with zero bookings (all attempts busy; informational)' END AS check,
       CASE WHEN :require_exactly_one = 1 THEN (SELECT count(*) FROM per_user WHERE c = 1) || ' of ' || :n_users
            ELSE (SELECT count(*) FROM per_user WHERE c = 0) || ' of ' || :n_users END AS observed,
       CASE WHEN :require_exactly_one = 0 THEN 'INFO'
            WHEN (SELECT count(*) FROM per_user WHERE c = 1) = :n_users THEN 'PASS' ELSE 'FAIL' END AS result
UNION ALL
SELECT 'users with more than one booking', (SELECT count(*) FROM per_user WHERE c > 1)::text,
       CASE WHEN (SELECT count(*) FROM per_user WHERE c > 1) = 0 THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'bookings on event from non-test users',
       (SELECT count(*) FROM bookings b WHERE b.event_id = :event_id
          AND b.user_id NOT IN (SELECT id FROM users WHERE email LIKE 'lt-user-%' ORDER BY id LIMIT :n_users))::text,
       CASE WHEN (SELECT count(*) FROM bookings b WHERE b.event_id = :event_id
          AND b.user_id NOT IN (SELECT id FROM users WHERE email LIKE 'lt-user-%' ORDER BY id LIMIT :n_users)) = 0
            THEN 'PASS' ELSE 'FAIL' END;
