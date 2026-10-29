// Command fairness-api is scenario L5 (service level): for each draw it
// creates an event through the API, books the same K test users in the same
// order, closes the event and calls POST /draw, then tallies who won. The
// participants are booked in a fixed order, so any bias tied to booking
// order (booked_at) would show up as a bias towards particular users.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Natthyx/lottery-system/loadtest/stats"
)

type tokens struct {
	Admin string `json:"admin"`
	Users []struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	} `json:"users"`
}

var client = &http.Client{Timeout: 30 * time.Second}

// transportErrors counts requests that got no HTTP response at all (e.g. EOF
// on a reused keep-alive connection). They are retried, and reported.
var transportErrors atomic.Int64

// call sends one request and decodes the JSON body into out. On a transport
// error (no HTTP response) it retries up to 5 times. Callers handle the case
// where the lost attempt had in fact been applied (book: 409 "already
// booked"; close: idempotent; draw: 409 "already_drawn").
func call(method, url, token, body string, out any) (int, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequest(method, url, strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			transportErrors.Add(1)
			fmt.Fprintf(os.Stderr, "transport error (attempt %d): %s %s: %v\n", attempt+1, method, url, err)
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return resp.StatusCode, err
			}
		}
		return resp.StatusCode, nil
	}
	return 0, lastErr
}

type apiError struct {
	Error string `json:"error"`
}

type drawResp struct {
	Code string `json:"code"`
	Data struct {
		Winners []struct {
			WinnerUserID int64 `json:"winner_user_id"`
		} `json:"winners"`
		Waitlist []struct {
			WinnerUserID int64 `json:"winner_user_id"`
		} `json:"waitlist"`
	} `json:"data"`
}

func oneDraw(base string, t *tokens, k, w int) (winners []int64, err error) {
	var ev struct {
		Data struct{ ID int64 } `json:"data"`
	}
	// Draws are refused until draw_at, so schedule it just ahead and wait.
	due := time.Now().Add(1500 * time.Millisecond)
	drawAt := due.UTC().Format(time.RFC3339Nano)
	code, err := call("POST", base+"/events", t.Admin,
		fmt.Sprintf(`{"title":"fairness","capacity":%d,"winner_count":%d,"draw_at":%q}`, k, w, drawAt), &ev)
	if err != nil || code != http.StatusCreated {
		return nil, fmt.Errorf("create event: %d %v", code, err)
	}
	for i := 0; i < k; i++ {
		// Different events never contend for the same lock; retry on the
		// busy 409 anyway so a transient lock never skews the sample.
		for attempt := 0; ; attempt++ {
			var e apiError
			code, err = call("POST", fmt.Sprintf("%s/events/%d/book", base, ev.Data.ID), t.Users[i].Token, "", &e)
			if err == nil && (code == http.StatusCreated ||
				(code == http.StatusConflict && strings.Contains(e.Error, "already booked"))) {
				break
			}
			if attempt > 50 {
				return nil, fmt.Errorf("book user %d: %d %v", i, code, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if code, err = call("PUT", fmt.Sprintf("%s/events/%d/close", base, ev.Data.ID), t.Admin, "", nil); err != nil || code != http.StatusOK {
		return nil, fmt.Errorf("close: %d %v", code, err)
	}
	time.Sleep(time.Until(due) + 100*time.Millisecond)
	var dr drawResp
	for attempt := 0; ; attempt++ {
		dr = drawResp{}
		code, err = call("POST", fmt.Sprintf("%s/events/%d/draw", base, ev.Data.ID), t.Admin, "", &dr)
		// Host and database clocks can differ by a few ms.
		if err != nil || code != http.StatusConflict || dr.Code != "draw_not_due" || attempt > 20 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err == nil && code == http.StatusConflict && dr.Code == "already_drawn" {
		// Only reachable if an earlier attempt of this draw was applied but
		// its response was lost; read the committed result instead.
		dr = drawResp{}
		code, err = call("GET", fmt.Sprintf("%s/events/%d/results", base, ev.Data.ID), t.Admin, "", &dr)
	}
	if err != nil || code != http.StatusOK {
		return nil, fmt.Errorf("draw: %d %v", code, err)
	}
	if len(dr.Data.Winners) != w || len(dr.Data.Winners)+len(dr.Data.Waitlist) != k {
		return nil, fmt.Errorf("draw returned %d winners and %d waitlist", len(dr.Data.Winners), len(dr.Data.Waitlist))
	}
	for _, x := range dr.Data.Winners {
		winners = append(winners, x.WinnerUserID)
	}
	return winners, nil
}

func main() {
	base := flag.String("base", "http://127.0.0.1:18080", "API base URL")
	tokensPath := flag.String("tokens", "", "tokens.json from setup-users")
	k := flag.Int("k", 10, "participants")
	w := flag.Int("w", 5, "winners per draw")
	draws := flag.Int("draws", 1000, "number of draws")
	workers := flag.Int("c", 8, "parallel workers (each works on its own event)")
	flag.Parse()

	raw, err := os.ReadFile(*tokensPath)
	if err != nil {
		fail(err)
	}
	var t tokens
	if err := json.Unmarshal(raw, &t); err != nil {
		fail(err)
	}
	index := map[int64]int{}
	for i := 0; i < *k; i++ {
		index[t.Users[i].ID] = i
	}

	wins := make([]int, *k)
	firsts := make([]int, *k)
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan struct{})
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				winners, err := oneDraw(*base, &t, *k, *w)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				for r, id := range winners {
					idx, ok := index[id]
					if !ok && firstErr == nil {
						firstErr = fmt.Errorf("unknown winner id %d", id)
					}
					wins[idx]++
					if r == 0 {
						firsts[idx]++
					}
				}
				mu.Unlock()
			}
		}()
	}
	for d := 0; d < *draws; d++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		fail(firstErr)
	}

	fmt.Printf("source=api (POST /events, /book x%d, /close, /draw) participants=%d winners_per_draw=%d draws=%d elapsed_s=%.1f\n",
		*k, *k, *w, *draws, time.Since(start).Seconds())
	fmt.Println("participants are booked in index order 0..k-1 for every event (index 0 = earliest booked_at)")
	fmt.Printf("transport_errors_retried=%d (requests that got no HTTP response; details on stderr)\n", transportErrors.Load())
	p, a := stats.WinFrequency(wins, *draws, *w)
	fmt.Println(p)
	fmt.Println(a)
	fmt.Println(stats.Uniform("rank-1 frequency", firsts))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fairness-api:", err)
	os.Exit(1)
}
