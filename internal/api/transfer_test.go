package api

import (
	"fmt"
	"net/http"
	"testing"
)

// Test 2: transfer succeeds, A=1000 B=0, send 700 -> A=300 B=700.
func TestTransferSucceeds(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)

	resp := postTransfer(t, srv, "test-2-key", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	if balance := getBalance(t, srv, a); balance != 300 {
		t.Fatalf("expected A=300, got %d", balance)
	}
	if balance := getBalance(t, srv, b); balance != 700 {
		t.Fatalf("expected B=700, got %d", balance)
	}
}

// Test 3: insufficient funds, A=100, send 700 -> 409, unchanged, no transfer row.
func TestTransferInsufficientFunds(t *testing.T) {
	srv, db := newTestServer(t)

	a := createAccount(t, srv, 100)
	b := createAccount(t, srv, 0)

	resp := postTransfer(t, srv, "test-3-key", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}

	if balance := getBalance(t, srv, a); balance != 100 {
		t.Fatalf("expected A unchanged at 100, got %d", balance)
	}
	if balance := getBalance(t, srv, b); balance != 0 {
		t.Fatalf("expected B unchanged at 0, got %d", balance)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM transfers`).Scan(&count); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no transfer row, got %d", count)
	}
}

// Test 4: exact balance, A=700, send 700 -> A=0.
func TestTransferExactBalance(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 700)
	b := createAccount(t, srv, 0)

	resp := postTransfer(t, srv, "test-4-key", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	if balance := getBalance(t, srv, a); balance != 0 {
		t.Fatalf("expected A=0, got %d", balance)
	}
}

// Test 5 (transfer part): invalid amount_cents bodies all get 400, nothing written.
func TestTransferInvalidAmounts(t *testing.T) {
	srv, db := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)

	cases := []struct {
		name string
		body string
	}{
		{"zero", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":0}`, a, b)},
		{"negative", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":-5}`, a, b)},
		{"float", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":10.5}`, a, b)},
		{"string", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":"700"}`, a, b)},
		{"missing", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d}`, a, b)},
		{"oversize", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":1000000000001}`, a, b)},
		{"unknown_field", fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700,"unexpected":"field"}`, a, b)},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postTransfer(t, srv, fmt.Sprintf("test-5-key-%d", i), c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", resp.StatusCode)
			}
		})
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM transfers`).Scan(&count); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no transfers written, got %d", count)
	}
}

// Test 6: unknown account -> 404; self-transfer -> 400.
func TestTransferUnknownAccountAndSelfTransfer(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)

	t.Run("unknown_account", func(t *testing.T) {
		resp := postTransfer(t, srv, "test-6-unknown", fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":999,"amount_cents":100}`, a))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("self_transfer", func(t *testing.T) {
		resp := postTransfer(t, srv, "test-6-self", fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":%d,"amount_cents":100}`, a, a))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

// Missing Idempotency-Key header -> 400 (optional test 15, cheap to cover now).
func TestTransferMissingIdempotencyKey(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)

	resp := postTransfer(t, srv, "", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":100}`, a, b))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
