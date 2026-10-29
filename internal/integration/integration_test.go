package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Natthyx/lottery-system/internal/booking"
	"github.com/Natthyx/lottery-system/internal/db"
	"github.com/Natthyx/lottery-system/internal/lottery"
)

const runtimeRolePassword = "integration-test-only"

var (
	owner   *pgxpool.Pool // schema owner / superuser, like MIGRATION_DATABASE_URL
	runtime *pgxpool.Pool // least-privilege lottery_app, like DATABASE_URL
	rdb     *redis.Client
)

func TestMain(m *testing.M) {
	dsn, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		fmt.Println("integration: TEST_DATABASE_URL / TEST_REDIS_URL not set, skipping")
		os.Exit(0)
	}
	ctx := context.Background()
	var err error
	if owner, err = db.NewPool(ctx, dsn, 10, 0); err != nil {
		fail("owner pool", err)
	}
	// The runtime role must exist before migrations so 002 grants to it.
	if _, err := owner.Exec(ctx, fmt.Sprintf(`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lottery_app') THEN
			CREATE ROLE lottery_app LOGIN PASSWORD '%s';
		ELSE
			ALTER ROLE lottery_app LOGIN PASSWORD '%s';
		END IF; END $$`, runtimeRolePassword, runtimeRolePassword)); err != nil {
		fail("create runtime role", err)
	}
	if err := db.RunMigrations(ctx, owner); err != nil {
		fail("migrations", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		fail("parse dsn", err)
	}
	u.User = url.UserPassword("lottery_app", runtimeRolePassword)
	if runtime, err = db.NewPool(ctx, u.String(), 30, 0); err != nil {
		fail("runtime pool", err)
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		fail("redis url", err)
	}
	rdb = redis.NewClient(opts)
	code := m.Run()
	runtime.Close()
	owner.Close()
	_ = rdb.Close()
	os.Exit(code)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "integration setup: %s: %v\n", what, err)
	os.Exit(1)
}

// ── fixtures (written as the owner, bypassing API validation) ─────────

var seq int64

func newUsers(t *testing.T, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range ids {
		seq++
		err := owner.QueryRow(context.Background(),
			`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', 'it') RETURNING id`,
			fmt.Sprintf("it-%d-%d-%d@test.local", time.Now().UnixNano(), seq, i)).Scan(&ids[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func newEvent(t *testing.T, capacity, winners int, drawAt time.Time) int64 {
	t.Helper()
	admin := newUsers(t, 1)[0]
	var id int64
	err := owner.QueryRow(context.Background(), `
		INSERT INTO events (title, capacity, winner_count, draw_at, created_by)
		VALUES ('it', $1, $2, $3, $4) RETURNING id`, capacity, winners, drawAt, admin).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func closedEventWithEntrants(t *testing.T, entrants, winners int) int64 {
	t.Helper()
	ev := newEvent(t, entrants, winners, time.Now().Add(-time.Second))
	for _, u := range newUsers(t, entrants) {
		if _, err := owner.Exec(context.Background(), `INSERT INTO bookings (event_id, user_id) VALUES ($1, $2)`, ev, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(context.Background(), `UPDATE events SET status = 'closed' WHERE id = $1`, ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ── F1: audit trail ──────────────────────────────────────────────────

func TestAuditTrail_RuntimeRoleCannotModify(t *testing.T) {
	ctx := context.Background()
	ev := closedEventWithEntrants(t, 5, 2)
	if _, err := lottery.NewService(runtime).RunDraw(ctx, ev); err != nil {
		t.Fatalf("draw as runtime role: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE event_id = $1`,
		`DELETE FROM lottery_draws WHERE event_id = $1`,
		`UPDATE lottery_draw_seals SET digest = 'x' WHERE event_id = $1`,
		`DELETE FROM lottery_draw_seals WHERE event_id = $1`,
	} {
		if _, err := runtime.Exec(ctx, stmt, ev); pgCode(err) != "42501" {
			t.Errorf("%q as runtime role: want 42501 permission denied, got %v", stmt, err)
		}
	}
	for _, stmt := range []string{`TRUNCATE lottery_draws`, `TRUNCATE lottery_draw_seals`, `DROP TRIGGER trg_lottery_draws_append_only ON lottery_draws`} {
		if _, err := runtime.Exec(ctx, stmt); err == nil {
			t.Errorf("%q succeeded as runtime role", stmt)
		}
	}
}

func TestAuditTrail_TriggersBlockEvenTheOwner(t *testing.T) {
	ctx := context.Background()
	ev := closedEventWithEntrants(t, 4, 1)
	if _, err := lottery.NewService(runtime).RunDraw(ctx, ev); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		fmt.Sprintf(`UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE event_id = %d`, ev),
		fmt.Sprintf(`DELETE FROM lottery_draws WHERE event_id = %d`, ev),
		`TRUNCATE lottery_draws`,
		fmt.Sprintf(`DELETE FROM lottery_draw_seals WHERE event_id = %d`, ev),
		`TRUNCATE lottery_draw_seals`,
		fmt.Sprintf(`UPDATE events SET status = 'closed' WHERE id = %d`, ev),
		fmt.Sprintf(`UPDATE events SET winner_count = 2 WHERE id = %d`, ev),
	} {
		if _, err := owner.Exec(ctx, stmt); pgCode(err) != "42501" {
			t.Errorf("%q as owner: want trigger rejection (42501), got %v", stmt, err)
		}
	}
}

func TestAuditTrail_SealDetectsSuperuserTampering(t *testing.T) {
	ctx := context.Background()
	svc := lottery.NewService(runtime)
	ev := closedEventWithEntrants(t, 6, 2)
	drawn, err := svc.RunDraw(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.GetResults(ctx, ev)
	if err != nil || res.Integrity != lottery.IntegrityVerified || res.Digest != drawn.Digest {
		t.Fatalf("fresh draw: integrity=%q digest match=%v err=%v", res.Integrity, res.Digest == drawn.Digest, err)
	}

	// A superuser can bypass triggers; the seal must still expose it.
	swap := func(a, b int64) {
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE lottery_draws SET winner_user_id = CASE winner_user_id WHEN $2 THEN $3 ELSE $2 END
			WHERE event_id = $1 AND winner_user_id IN ($2, $3)`, ev, a, b); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	winner, loser := drawn.Winners[0].WinnerUserID, drawn.Waitlist[0].WinnerUserID
	swap(winner, loser)
	if res, err := svc.GetResults(ctx, ev); err != nil || res.Integrity != lottery.IntegrityMismatch {
		t.Fatalf("after tampering: integrity=%q err=%v, want mismatch", res.Integrity, err)
	}
	swap(winner, loser) // restore
	if res, err := svc.GetResults(ctx, ev); err != nil || res.Integrity != lottery.IntegrityVerified {
		t.Fatalf("after restore: integrity=%q err=%v", res.Integrity, err)
	}
}

// ── F3/F4/F5/F7: booking ─────────────────────────────────────────────

func TestBooking_ConcurrentRequestsQueueAndNeverOversell(t *testing.T) {
	const users, capacity = 300, 150
	svc := booking.NewService(runtime, rdb, 5*time.Second, 30*time.Second)
	ev := newEvent(t, capacity, 1, time.Now().Add(time.Hour))
	ids := newUsers(t, users)

	var mu sync.Mutex
	outcome := map[string]int{}
	var wg sync.WaitGroup
	for _, u := range ids {
		wg.Add(1)
		go func(u int64) {
			defer wg.Done()
			err := svc.BookWithLock(context.Background(), ev, u)
			key := "ok"
			switch {
			case err == nil:
			case errors.Is(err, booking.ErrEventFull):
				key = "full"
			case errors.Is(err, booking.ErrEventBusy):
				key = "busy"
			default:
				key = err.Error()
			}
			mu.Lock()
			outcome[key]++
			mu.Unlock()
		}(u)
	}
	wg.Wait()

	if outcome["ok"] != capacity || outcome["full"] != users-capacity || len(outcome) != 2 {
		t.Fatalf("outcomes %v, want %d ok and %d full, nothing else", outcome, capacity, users-capacity)
	}
	var n int
	if err := owner.QueryRow(context.Background(), `SELECT count(*) FROM bookings WHERE event_id = $1`, ev).Scan(&n); err != nil || n != capacity {
		t.Fatalf("bookings in DB = %d (err %v), want %d", n, err, capacity)
	}
}

func TestBooking_ParallelDuplicatesYieldExactlyOne(t *testing.T) {
	svc := booking.NewService(runtime, rdb, 5*time.Second, 10*time.Second)
	ev := newEvent(t, 10, 1, time.Now().Add(time.Hour))
	u := newUsers(t, 1)[0]
	var ok, dup, other int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.BookWithLock(context.Background(), ev, u)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, booking.ErrAlreadyBooked):
				dup++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	if ok != 1 || dup != 9 || other != 0 {
		t.Fatalf("ok=%d already_booked=%d other=%d, want 1/9/0", ok, dup, other)
	}
}

func TestBooking_DuplicateReportedBeforeFullOrClosed(t *testing.T) {
	ctx := context.Background()
	svc := booking.NewService(runtime, rdb, 5*time.Second, 10*time.Second)
	ev := newEvent(t, 1, 1, time.Now().Add(time.Hour))
	u := newUsers(t, 1)[0]
	if err := svc.BookWithLock(ctx, ev, u); err != nil {
		t.Fatal(err)
	}
	if err := svc.BookWithLock(ctx, ev, u); !errors.Is(err, booking.ErrAlreadyBooked) {
		t.Fatalf("duplicate on full event: got %v, want already booked", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE events SET status = 'closed' WHERE id = $1`, ev); err != nil {
		t.Fatal(err)
	}
	if err := svc.BookWithLock(ctx, ev, u); !errors.Is(err, booking.ErrAlreadyBooked) {
		t.Fatalf("duplicate on closed event: got %v, want already booked", err)
	}
}

func TestBooking_BusyOnlyAfterBoundedWait(t *testing.T) {
	ctx := context.Background()
	svc := booking.NewService(runtime, rdb, 5*time.Second, 100*time.Millisecond)
	ev := newEvent(t, 10, 1, time.Now().Add(time.Hour))
	key := fmt.Sprintf("lock:booking:event:%d", ev)
	// Simulate another replica holding the lock.
	if err := rdb.Set(ctx, key, "other-replica", 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	defer rdb.Del(ctx, key)
	start := time.Now()
	err := svc.BookWithLock(ctx, ev, newUsers(t, 1)[0])
	if !errors.Is(err, booking.ErrEventBusy) {
		t.Fatalf("got %v, want ErrEventBusy", err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("gave up after %v, before the 100ms wait budget", waited)
	}
}

// ── L4 / F10: draws ──────────────────────────────────────────────────

func TestDraw_ConcurrentDrawsProduceExactlyOne(t *testing.T) {
	svc := lottery.NewService(runtime)
	ev := closedEventWithEntrants(t, 50, 10)
	var ok, already, other int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RunDraw(context.Background(), ev)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, lottery.ErrEventAlreadyDrawn):
				already++
			default:
				other++
				t.Log(err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || already != 19 || other != 0 {
		t.Fatalf("ok=%d already=%d other=%d, want 1/19/0", ok, already, other)
	}
	res, err := svc.GetResults(context.Background(), ev)
	if err != nil || len(res.Winners) != 10 || len(res.Waitlist) != 40 || res.Integrity != lottery.IntegrityVerified {
		t.Fatalf("results: winners=%d waitlist=%d integrity=%q err=%v", len(res.Winners), len(res.Waitlist), res.Integrity, err)
	}
}

func TestDraw_RefusedBeforeDrawAt(t *testing.T) {
	ctx := context.Background()
	ev := newEvent(t, 5, 1, time.Now().Add(time.Hour))
	u := newUsers(t, 1)[0]
	if _, err := owner.Exec(ctx, `INSERT INTO bookings (event_id, user_id) VALUES ($1, $2)`, ev, u); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE events SET status = 'closed' WHERE id = $1`, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := lottery.NewService(runtime).RunDraw(ctx, ev); !errors.Is(err, lottery.ErrDrawNotDue) {
		t.Fatalf("got %v, want ErrDrawNotDue", err)
	}
}
