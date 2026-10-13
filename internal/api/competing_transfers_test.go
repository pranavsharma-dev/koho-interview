package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Test 7: two transfers from the same account, racing for the same money.
// Fires both over real HTTP (httptest.NewServer + the production handler and
// pool, so each request gets its own connection), releasing both goroutines
// together via close(start). Exactly one must succeed (201) and the other
// must fail with exactly 409 (not merely "not 201") — and the DB must show
// no money created or destroyed.
//
// Break-it check (see BUILDLOG.md for the captured failing output): replacing
// the FOR UPDATE read + relative update in Store.Transfer with a plain read +
// absolute write makes this fail on total money within a few iterations,
// because both racing requests can read the same pre-transfer balance and
// each write their own "balance - 700" independently, losing one debit.
func TestCompetingTransfersNeverOverdraw(t *testing.T) {
	srv, db := newTestServer(t)

	for i := 0; i < 50; i++ {
		i := i
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			runCompetingTransfersOnce(t, srv, db, i)
		})
	}
}

func runCompetingTransfersOnce(t *testing.T, srv *httptest.Server, db *sql.DB, i int) {
	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)
	c := createAccount(t, srv, 0)

	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make([]int, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		resp := postTransfer(t, srv, fmt.Sprintf("test7-%d-to-b", i), fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, b))
		statuses[0] = resp.StatusCode
		resp.Body.Close()
	}()
	go func() {
		defer wg.Done()
		<-start
		resp := postTransfer(t, srv, fmt.Sprintf("test7-%d-to-c", i), fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":%d,"amount_cents":700}`, a, c))
		statuses[1] = resp.StatusCode
		resp.Body.Close()
	}()
	close(start)
	wg.Wait()

	counts := map[int]int{}
	for _, s := range statuses {
		counts[s]++
	}
	if counts[http.StatusCreated] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("expected exactly one 201 and one 409, got statuses %v", statuses)
	}

	balanceA := getBalance(t, srv, a)
	balanceB := getBalance(t, srv, b)
	balanceC := getBalance(t, srv, c)

	if balanceA != 300 {
		t.Fatalf("expected A=300, got %d", balanceA)
	}
	if balanceB+balanceC != 700 {
		t.Fatalf("expected B+C=700, got B=%d C=%d", balanceB, balanceC)
	}
	if total := balanceA + balanceB + balanceC; total != 1000 {
		t.Fatalf("expected total=1000, got %d (money created or destroyed)", total)
	}

	var transferCount int
	if err := db.QueryRow(`SELECT count(*) FROM transfers WHERE from_account_id = $1`, a).Scan(&transferCount); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if transferCount != 1 {
		t.Fatalf("expected exactly one transfer row, got %d", transferCount)
	}
}
