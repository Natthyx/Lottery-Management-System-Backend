-- L4: after ~20 concurrent POST /events/{id}/draw, exactly one draw must
-- have happened. Requires -v event_id=<id>
--
-- Note: the code writes ONE lottery_draws ROW PER ENTRANT (winners and
-- waitlist, draw_rank 1..n), not one row per draw (internal/lottery/service.go
-- RunDraw). "Exactly one draw" is therefore checked as: one drawn_at, ranks
-- 1..n each once, n = number of bookings, every entrant exactly once.

SELECT e.id, e.status, e.winner_count, e.capacity,
       (SELECT count(*) FROM bookings WHERE event_id = e.id) AS bookings,
       (SELECT count(*) FROM lottery_draws WHERE event_id = e.id) AS draw_rows
FROM events e WHERE e.id = :event_id;

WITH d AS (SELECT * FROM lottery_draws WHERE event_id = :event_id),
     b AS (SELECT user_id FROM bookings WHERE event_id = :event_id),
     e AS (SELECT * FROM events WHERE id = :event_id),
     expect AS (SELECT least((SELECT winner_count FROM e), (SELECT count(*) FROM b)) AS winners,
                       (SELECT count(*) FROM b) AS entrants)
SELECT 'event status is drawn' AS check, (SELECT status FROM e) AS observed,
       CASE WHEN (SELECT status FROM e) = 'drawn' THEN 'PASS' ELSE 'FAIL' END AS result
UNION ALL
SELECT 'distinct drawn_at (draw executions)', (SELECT count(DISTINCT drawn_at) FROM d)::text,
       CASE WHEN (SELECT count(DISTINCT drawn_at) FROM d) = 1 THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'draw rows = entrants', (SELECT count(*) FROM d) || ' vs ' || (SELECT entrants FROM expect),
       CASE WHEN (SELECT count(*) FROM d) = (SELECT entrants FROM expect) THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'ranks are exactly 1..n', min(draw_rank) || '..' || max(draw_rank) || ' distinct=' || count(DISTINCT draw_rank),
       CASE WHEN min(draw_rank) = 1 AND max(draw_rank) = count(*) AND count(DISTINCT draw_rank) = count(*) THEN 'PASS' ELSE 'FAIL' END
FROM d
UNION ALL
SELECT 'drawn users = booked users (symmetric difference)',
       (SELECT count(*) FROM ((SELECT winner_user_id FROM d EXCEPT SELECT user_id FROM b)
                              UNION ALL (SELECT user_id FROM b EXCEPT SELECT winner_user_id FROM d)) x)::text,
       CASE WHEN (SELECT count(*) FROM ((SELECT winner_user_id FROM d EXCEPT SELECT user_id FROM b)
                              UNION ALL (SELECT user_id FROM b EXCEPT SELECT winner_user_id FROM d)) x) = 0
                 AND (SELECT count(DISTINCT winner_user_id) FROM d) = (SELECT count(*) FROM d) THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'exactly one draw seal', (SELECT count(*) FROM lottery_draw_seals WHERE event_id = :event_id)::text,
       CASE WHEN (SELECT count(*) FROM lottery_draw_seals WHERE event_id = :event_id) = 1 THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'winner rows (rank <= winner_count)',
       (SELECT count(*) FROM d WHERE draw_rank <= (SELECT winner_count FROM e)) || ' vs ' || (SELECT winners FROM expect),
       CASE WHEN (SELECT count(*) FROM d WHERE draw_rank <= (SELECT winner_count FROM e)) = (SELECT winners FROM expect) THEN 'PASS' ELSE 'FAIL' END
UNION ALL
SELECT 'waitlist rows (rank > winner_count)',
       (SELECT count(*) FROM d WHERE draw_rank > (SELECT winner_count FROM e)) || ' vs ' || ((SELECT entrants FROM expect) - (SELECT winners FROM expect)),
       CASE WHEN (SELECT count(*) FROM d WHERE draw_rank > (SELECT winner_count FROM e)) = (SELECT entrants FROM expect) - (SELECT winners FROM expect) THEN 'PASS' ELSE 'FAIL' END;
