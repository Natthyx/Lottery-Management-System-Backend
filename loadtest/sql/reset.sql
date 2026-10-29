-- Reset between scenarios. Runs only against the lottery-loadtest stack
-- (run.sh checks container labels first). Users are kept so the JWTs in
-- .state/tokens.json stay valid; everything that scenarios create is wiped.
-- TRUNCATE order is irrelevant inside one statement; CASCADE is not needed
-- because all three referencing tables are listed.
TRUNCATE lottery_draws, bookings, events RESTART IDENTITY;
