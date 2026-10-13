package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func doTransfer(t *testing.T, srv *httptest.Server, key string, fromID, toID, amountCents int64) {
	t.Helper()
	resp := postTransfer(t, srv, key, fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":%d}`, fromID, toID, amountCents))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("transfer %s: expected 201, got %d", key, resp.StatusCode)
	}
}

// Test 12: history for B after three transfers, newest first, correct
// direction/counterparty/amount per entry.
func TestAccountTransactionHistory(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)
	c := createAccount(t, srv, 0)

	doTransfer(t, srv, "hist-1", a, b, 300) // B: in, from A, 300
	doTransfer(t, srv, "hist-2", b, c, 100) // B: out, to C, 100
	doTransfer(t, srv, "hist-3", a, b, 200) // B: in, from A, 200

	resp, err := http.Get(fmt.Sprintf("%s/accounts/%d/transactions", srv.URL, b))
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var history []transactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(history))
	}

	want := []struct {
		direction    string
		counterparty int64
		amount       int64
	}{
		{"in", a, 200},  // hist-3, newest
		{"out", c, 100}, // hist-2
		{"in", a, 300},  // hist-1, oldest
	}
	for i, w := range want {
		got := history[i]
		if got.Direction != w.direction || got.CounterpartyAccountID != w.counterparty || got.AmountCents != w.amount {
			t.Fatalf("entry %d: expected {%s, %d, %d}, got {%s, %d, %d}",
				i, w.direction, w.counterparty, w.amount,
				got.Direction, got.CounterpartyAccountID, got.AmountCents)
		}
	}

	// Newest first, strictly: each entry's created_at >= the next one's.
	for i := 1; i < len(history); i++ {
		if history[i-1].CreatedAt.Before(history[i].CreatedAt) {
			t.Fatalf("entries not newest-first at index %d", i)
		}
	}
}

func TestAccountHistoryUnknownAccount(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/accounts/999/transactions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
