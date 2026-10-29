// Command setup-users is scenario L1: it registers N test users (idempotent),
// logs each in once, logs the bootstrap admin in, and writes every JWT to a
// gitignored tokens file. This is setup, not a measured scenario: bcrypt
// cost 12 makes it deliberately slow, which is why scenarios reuse tokens.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type userToken struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Token string `json:"token"`
}

type tokensFile struct {
	Admin string      `json:"admin"`
	Users []userToken `json:"users"`
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

var client = &http.Client{Timeout: 60 * time.Second}

func post(base, path string, body any) (int, envelope, error) {
	b, _ := json.Marshal(body)
	resp, err := client.Post(base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, envelope{}, err
	}
	defer resp.Body.Close()
	var env envelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env, nil
}

func login(base, email, password string) (userToken, error) {
	code, env, err := post(base, "/auth/login", map[string]string{"email": email, "password": password})
	if err != nil {
		return userToken{}, err
	}
	if code != http.StatusOK {
		return userToken{}, fmt.Errorf("login %s: status %d: %s", email, code, env.Error)
	}
	var data struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return userToken{}, err
	}
	return userToken{ID: data.User.ID, Email: email, Token: data.Token}, nil
}

func main() {
	base := flag.String("base", "http://127.0.0.1:18080", "API base URL")
	n := flag.Int("n", 1000, "number of users")
	concurrency := flag.Int("c", 8, "parallel workers")
	passwordFile := flag.String("password-file", "", "file holding the shared test-user password")
	adminEmail := flag.String("admin-email", "", "bootstrap admin email")
	adminPasswordEnv := flag.String("admin-password-env", "BOOTSTRAP_ADMIN_PASSWORD", "env var holding the admin password")
	out := flag.String("out", "", "tokens JSON output path")
	flag.Parse()

	pw, err := os.ReadFile(*passwordFile)
	if err != nil {
		fail(err)
	}
	password := strings.TrimSpace(string(pw))

	start := time.Now()
	admin, err := login(*base, *adminEmail, os.Getenv(*adminPasswordEnv))
	if err != nil {
		fail(err)
	}

	users := make([]userToken, *n)
	var (
		mu                      sync.Mutex
		firstErr                error
		registered, preexisting int
	)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				email := fmt.Sprintf("lt-user-%04d@loadtest.local", i)
				code, env, err := post(*base, "/auth/register", map[string]string{
					"email": email, "password": password, "full_name": fmt.Sprintf("Loadtest User %d", i),
				})
				if err == nil && code != http.StatusCreated && code != http.StatusConflict {
					err = fmt.Errorf("register %s: status %d: %s", email, code, env.Error)
				}
				var tok userToken
				if err == nil {
					tok, err = login(*base, email, password)
				}
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				if code == http.StatusCreated {
					registered++
				} else if code == http.StatusConflict {
					preexisting++
				}
				users[i] = tok
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < *n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		fail(firstErr)
	}

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(f).Encode(tokensFile{Admin: admin.Token, Users: users}); err != nil {
		fail(err)
	}
	if err := f.Close(); err != nil {
		fail(err)
	}

	// Printed (no secrets) so run.sh can save it as the L1 result.
	fmt.Printf("users_requested=%d registered_now=%d already_existed=%d logged_in=%d workers=%d elapsed_s=%.1f\n",
		*n, registered, preexisting, len(users), *concurrency, time.Since(start).Seconds())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "setup-users:", err)
	os.Exit(1)
}
