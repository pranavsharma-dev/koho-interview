package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pranavsharma-dev/koho-interview/internal/bank"
)

func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/bank?sslmode=disable"
	}

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`TRUNCATE accounts, transfers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	h := New(bank.New(db))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /accounts", h.CreateAccount)
	mux.HandleFunc("GET /accounts/{id}", h.GetAccount)
	mux.HandleFunc("POST /transfers", h.CreateTransfer)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, db
}

func postJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func postTransfer(t *testing.T, srv *httptest.Server, idempotencyKey, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/transfers", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func createAccount(t *testing.T, srv *httptest.Server, startingBalanceCents int64) int64 {
	t.Helper()
	resp := postJSON(t, srv.URL+"/accounts", fmt.Sprintf(`{"starting_balance_cents":%d}`, startingBalanceCents))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create account: expected 201, got %d", resp.StatusCode)
	}
	var created accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create account response: %v", err)
	}
	return created.ID
}

func getBalance(t *testing.T, srv *httptest.Server, id int64) int64 {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/accounts/%d", srv.URL, id))
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get account: expected 200, got %d", resp.StatusCode)
	}
	var acc accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&acc); err != nil {
		t.Fatalf("decode get account response: %v", err)
	}
	return acc.BalanceCents
}

// Test 1: create and read an account.
func TestCreateAndGetAccount(t *testing.T) {
	srv, _ := newTestServer(t)

	resp := postJSON(t, srv.URL+"/accounts", `{"starting_balance_cents":1000}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", resp.StatusCode)
	}
	var created accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.BalanceCents != 1000 {
		t.Fatalf("expected balance 1000, got %d", created.BalanceCents)
	}

	getResp, err := http.Get(fmt.Sprintf("%s/accounts/%d", srv.URL, created.ID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", getResp.StatusCode)
	}
	var fetched accountResponse
	if err := json.NewDecoder(getResp.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if fetched.ID != created.ID || fetched.BalanceCents != 1000 {
		t.Fatalf("expected {%d, 1000}, got %+v", created.ID, fetched)
	}
}

func TestGetUnknownAccount(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/accounts/999")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// Test 5 (account part): invalid starting_balance_cents bodies all get 400,
// and nothing is written.
func TestCreateAccountInvalidBodies(t *testing.T) {
	srv, db := newTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"negative", `{"starting_balance_cents":-5}`},
		{"float", `{"starting_balance_cents":10.5}`},
		{"string", `{"starting_balance_cents":"700"}`},
		{"oversize", `{"starting_balance_cents":1000000000001}`},
		{"unknown_field", `{"starting_balance_cents":100,"unexpected":"field"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, srv.URL+"/accounts", c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", resp.StatusCode)
			}
		})
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&count); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no accounts written, got %d", count)
	}
}

// Starting balance 0, or the field omitted entirely, are both valid (unlike
// transfer amounts, which must be > 0) — documented in SPEC.md's Validation
// section.
func TestCreateAccountZeroOrMissingBalanceIsValid(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"explicit_zero", `{"starting_balance_cents":0}`},
		{"missing_field", `{}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, srv.URL+"/accounts", c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("expected 201, got %d", resp.StatusCode)
			}
			var created accountResponse
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if created.BalanceCents != 0 {
				t.Fatalf("expected balance 0, got %d", created.BalanceCents)
			}
		})
	}
}
