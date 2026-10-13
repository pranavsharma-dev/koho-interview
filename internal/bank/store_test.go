package bank

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func openTestDB(t *testing.T) *sql.DB {
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
	return db
}

func TestConnect(t *testing.T) {
	db := openTestDB(t)

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping db: %v", err)
	}
}

// Test 11: a failure injected between the debit and credit must roll back
// the whole transaction (both balances unchanged, no transfer row), and the
// idempotency key must not be consumed, so the same key then succeeds.
func TestTransferAtomicityOnMidTransferFailure(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(`TRUNCATE accounts, transfers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	store := New(db)

	var aID, bID int64
	var err error
	aID, _, err = store.CreateAccount(context.Background(), 1000)
	if err != nil {
		t.Fatalf("create account A: %v", err)
	}
	bID, _, err = store.CreateAccount(context.Background(), 0)
	if err != nil {
		t.Fatalf("create account B: %v", err)
	}

	injectedErr := errors.New("injected failure between debit and credit")
	store.midTransferHook = func() error { return injectedErr }

	_, _, err = store.Transfer(context.Background(), "test-11-key", aID, bID, 700)
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected error, got %v", err)
	}

	balanceA, err := store.GetAccount(context.Background(), aID)
	if err != nil {
		t.Fatalf("get account A: %v", err)
	}
	if balanceA != 1000 {
		t.Fatalf("expected A unchanged at 1000, got %d", balanceA)
	}
	balanceB, err := store.GetAccount(context.Background(), bID)
	if err != nil {
		t.Fatalf("get account B: %v", err)
	}
	if balanceB != 0 {
		t.Fatalf("expected B unchanged at 0, got %d", balanceB)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM transfers WHERE idempotency_key = $1`, "test-11-key").Scan(&count); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no transfer row after rollback, got %d", count)
	}

	// Key was never consumed: the same key now succeeds normally.
	store.midTransferHook = nil
	transfer, replayed, err := store.Transfer(context.Background(), "test-11-key", aID, bID, 700)
	if err != nil {
		t.Fatalf("retry after rollback: unexpected error: %v", err)
	}
	if replayed {
		t.Fatalf("expected a fresh transfer, not a replay")
	}
	if transfer.AmountCents != 700 {
		t.Fatalf("expected amount 700, got %d", transfer.AmountCents)
	}

	balanceA, err = store.GetAccount(context.Background(), aID)
	if err != nil {
		t.Fatalf("get account A: %v", err)
	}
	if balanceA != 300 {
		t.Fatalf("expected A=300 after successful retry, got %d", balanceA)
	}
}
