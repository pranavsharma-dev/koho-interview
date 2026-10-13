package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Test 8: same key, same body, submitted twice -> 201 then 200, same id,
// account debited only once.
func TestTransferReplaySameKeyAndBody(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)
	body := fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b)

	resp1 := postTransfer(t, srv, "test-8-key", body)
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: expected 201, got %d", resp1.StatusCode)
	}
	var created transferResponse
	if err := json.NewDecoder(resp1.Body).Decode(&created); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	resp2 := postTransfer(t, srv, "test-8-key", body)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d", resp2.StatusCode)
	}
	var replayed transferResponse
	if err := json.NewDecoder(resp2.Body).Decode(&replayed); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}

	if replayed.ID != created.ID {
		t.Fatalf("expected same transfer id, got %d and %d", created.ID, replayed.ID)
	}
	if balance := getBalance(t, srv, a); balance != 300 {
		t.Fatalf("expected A debited exactly once (300), got %d", balance)
	}
}

// Test 9: same key, different body -> 422, nothing applied.
func TestTransferSameKeyDifferentBody(t *testing.T) {
	srv, db := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)

	resp1 := postTransfer(t, srv, "test-9-key", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b))
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: expected 201, got %d", resp1.StatusCode)
	}

	resp2 := postTransfer(t, srv, "test-9-key", fmt.Sprintf(
		`{"from_account_id":%d,"to_account_id":%d,"amount_cents":800}`, a, b))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reused key, different body: expected 422, got %d", resp2.StatusCode)
	}

	if balance := getBalance(t, srv, a); balance != 300 {
		t.Fatalf("expected A debited only by the first transfer (300), got %d", balance)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM transfers WHERE idempotency_key = $1`, "test-9-key").Scan(&count); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 transfer row for the key, got %d", count)
	}
}

// Test 10: ten goroutines, one key, one body -> all succeed, one transfer id,
// account debited once.
func TestTransferConcurrentDuplicates(t *testing.T) {
	srv, _ := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)
	body := fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b)

	const n = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make([]int, n)
	ids := make([]int64, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			resp := postTransfer(t, srv, "test-10-key", body)
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
			if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
				var tr transferResponse
				if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
					t.Errorf("goroutine %d: decode: %v", i, err)
					return
				}
				ids[i] = tr.ID
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, status := range statuses {
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("goroutine %d: expected 201 or 200, got %d", i, status)
		}
	}

	firstID := ids[0]
	for i, id := range ids {
		if id != firstID {
			t.Fatalf("goroutine %d: expected transfer id %d, got %d", i, firstID, id)
		}
	}

	if balance := getBalance(t, srv, a); balance != 300 {
		t.Fatalf("expected A debited exactly once (300), got %d", balance)
	}
}
