package api

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Phase 8 adversarial review.
//
// Gap 1: test 10 proves concurrent duplicates serialize correctly when both
// requests target the SAME account pair -- but BUILDLOG.md's own analysis
// says that case is dominated by the FOR UPDATE row lock, not the unique
// index on idempotency_key. The unique-index wait path (the thing SPEC.md's
// duplicate-cases table actually describes) is only exercised when two
// requests share a key but touch DISJOINT accounts, which no existing test
// does. This drives that case directly.
func TestTransferConcurrentSameKeyDisjointAccounts(t *testing.T) {
	srv, db := newTestServer(t)

	a := createAccount(t, srv, 1000)
	b := createAccount(t, srv, 0)
	c := createAccount(t, srv, 1000)
	d := createAccount(t, srv, 0)

	const key = "disjoint-shared-key"
	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make([]int, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		resp := postTransfer(t, srv, key, fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":%d,"amount_cents":100}`, a, b))
		statuses[0] = resp.StatusCode
		resp.Body.Close()
	}()
	go func() {
		defer wg.Done()
		<-start
		resp := postTransfer(t, srv, key, fmt.Sprintf(
			`{"from_account_id":%d,"to_account_id":%d,"amount_cents":200}`, c, d))
		statuses[1] = resp.StatusCode
		resp.Body.Close()
	}()
	close(start)
	wg.Wait()

	counts := map[int]int{}
	for _, s := range statuses {
		counts[s]++
	}
	// Exactly one request originates the key (201); the other must see it
	// as a reuse with a different body (422) -- never both 201 (that would
	// mean the unique constraint failed to serialize them), and never both
	// 422 (one of them has to win).
	if counts[http.StatusCreated] != 1 || counts[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("expected exactly one 201 and one 422, got statuses %v", statuses)
	}

	var transferCount int
	if err := db.QueryRow(`SELECT count(*) FROM transfers WHERE idempotency_key = $1`, key).Scan(&transferCount); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if transferCount != 1 {
		t.Fatalf("expected exactly one transfer row for the shared key, got %d", transferCount)
	}

	// Whichever pair's transfer actually won, the other pair must be
	// completely untouched -- the 422 path must not have moved any money.
	balA, balB, balC, balD := getBalance(t, srv, a), getBalance(t, srv, b), getBalance(t, srv, c), getBalance(t, srv, d)
	abMoved := balA == 900 && balB == 100
	abUntouched := balA == 1000 && balB == 0
	cdMoved := balC == 800 && balD == 200
	cdUntouched := balC == 1000 && balD == 0

	switch {
	case abMoved && cdUntouched:
	case cdMoved && abUntouched:
	default:
		t.Fatalf("expected exactly one pair to move money and the other untouched, got A=%d B=%d C=%d D=%d", balA, balB, balC, balD)
	}
}

// Gap 2: "ORDER BY id" is assumed to prevent opposite-direction transfers
// from deadlocking (see SPEC.md's lock-ordering rationale), but no test ever
// actually fires A->B and B->A at each other. If the lock order were wrong,
// Postgres would detect the deadlock (error 40P01) and abort one side --
// Store.Transfer has no special handling for that error, so it would
// surface as a generic 500, not a
// clean 201/409. This is SPEC.md's optional test 14, run for real with a
// hard timeout so a true deadlock (which Postgres itself resolves in ~1s via
// its deadlock_timeout, aborting one side) still shows up as a failure
// rather than a hang.
func TestOppositeDirectionTransfersNoDeadlock(t *testing.T) {
	srv, _ := newTestServer(t)

	for i := 0; i < 20; i++ {
		i := i
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			a := createAccount(t, srv, 1000)
			b := createAccount(t, srv, 1000)

			start := make(chan struct{})
			done := make(chan [2]int, 2)

			go func() {
				<-start
				resp := postTransfer(t, srv, fmt.Sprintf("deadlock-%d-ab", i), fmt.Sprintf(
					`{"from_account_id":%d,"to_account_id":%d,"amount_cents":300}`, a, b))
				defer resp.Body.Close()
				done <- [2]int{0, resp.StatusCode}
			}()
			go func() {
				<-start
				resp := postTransfer(t, srv, fmt.Sprintf("deadlock-%d-ba", i), fmt.Sprintf(
					`{"from_account_id":%d,"to_account_id":%d,"amount_cents":300}`, b, a))
				defer resp.Body.Close()
				done <- [2]int{1, resp.StatusCode}
			}()
			close(start)

			statuses := make([]int, 2)
			for n := 0; n < 2; n++ {
				select {
				case r := <-done:
					statuses[r[0]] = r[1]
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for both transfers -- possible deadlock, neither side returned")
				}
			}

			for which, s := range statuses {
				if s != http.StatusCreated {
					t.Fatalf("leg %d: expected 201 (sufficient funds both ways, no reason to fail), got %d -- a real deadlock would surface here as 500", which, s)
				}
			}

			if balA := getBalance(t, srv, a); balA != 1000 {
				t.Fatalf("expected A=1000 after +300-300, got %d", balA)
			}
			if balB := getBalance(t, srv, b); balB != 1000 {
				t.Fatalf("expected B=1000 after -300+300, got %d", balB)
			}
		})
	}
}
