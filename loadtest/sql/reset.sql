-- Reset between scenarios. Runs only against the lottery-loadtest stack
-- (run.sh checks container labels first). Users are kept so the JWTs in
-- .state/tokens.json stay valid; everything that scenarios create is wiped.
--
-- The audit tables now refuse TRUNCATE through triggers (migration 002).
-- A superuser can skip triggers with session_replication_role = replica;
-- that is exactly the residual risk the draw seals exist to detect, and it
-- is used here only to wipe the throwaway test database.
SET session_replication_role = replica;
TRUNCATE lottery_draw_seals, lottery_draws, bookings, events RESTART IDENTITY;
RESET session_replication_role;
