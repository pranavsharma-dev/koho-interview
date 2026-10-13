package bank

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrAccountNotFound   = errors.New("account not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrKeyReused         = errors.New("idempotency key reused with a different request")
)

type Transfer struct {
	ID            int64
	FromAccountID int64
	ToAccountID   int64
	AmountCents   int64
	CreatedAt     time.Time
}

type Store struct {
	db *sql.DB

	// midTransferHook is called between the debit and credit updates, inside
	// the transaction. Nil in production; tests use it to inject a failure
	// and prove the transaction rolls back atomically (test 11). Unexported
	// so it never appears in Store's public API.
	midTransferHook func() error
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateAccount(ctx context.Context, startingBalanceCents int64) (id int64, balanceCents int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO accounts (opening_balance_cents, balance_cents) VALUES ($1, $1) RETURNING id, balance_cents`,
		startingBalanceCents,
	).Scan(&id, &balanceCents)
	return id, balanceCents, err
}

func (s *Store) GetAccount(ctx context.Context, id int64) (balanceCents int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT balance_cents FROM accounts WHERE id = $1`,
		id,
	).Scan(&balanceCents)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrAccountNotFound
	}
	return balanceCents, err
}

// Transfer locks both accounts (ORDER BY id, to avoid opposite-direction
// deadlocks), then attempts to claim the idempotency key. If the key already
// exists, it compares the existing transfer to this request: an identical
// request returns the original (replayed=true, handler sends 200); a
// different request returns ErrKeyReused (handler sends 422) without
// touching any balance. Only a genuinely new key proceeds to the balance
// check and relative debit/credit.
//
// Concurrent duplicates: a second request with the same key blocks on the
// unique index until the first transaction concludes. If the first commits,
// the second's INSERT is suppressed and it takes the replay/reuse path
// above. If the first rolls back (e.g. insufficient funds), the key was
// never actually claimed, so the second's INSERT succeeds and it proceeds
// as a new transfer.
//
// midTransferHook (nil in production) runs between the debit and credit;
// test 11 uses it to prove a failure there rolls back both updates and the
// transfer row, leaving the key unconsumed.
func (s *Store) Transfer(ctx context.Context, idempotencyKey string, fromID, toID, amountCents int64) (transfer Transfer, replayed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, false, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, balance_cents FROM accounts WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`,
		fromID, toID,
	)
	if err != nil {
		return Transfer{}, false, err
	}
	balances := make(map[int64]int64, 2)
	for rows.Next() {
		var id, balance int64
		if err := rows.Scan(&id, &balance); err != nil {
			rows.Close()
			return Transfer{}, false, err
		}
		balances[id] = balance
	}
	if err := rows.Err(); err != nil {
		return Transfer{}, false, err
	}
	if len(balances) < 2 {
		return Transfer{}, false, ErrAccountNotFound
	}

	var transferID int64
	var createdAt time.Time
	err = tx.QueryRowContext(ctx,
		`INSERT INTO transfers (idempotency_key, from_account_id, to_account_id, amount_cents)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (idempotency_key) DO NOTHING
		 RETURNING id, created_at`,
		idempotencyKey, fromID, toID, amountCents,
	).Scan(&transferID, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		// Key already claimed by an earlier (now-committed) transfer.
		var existing Transfer
		selErr := tx.QueryRowContext(ctx,
			`SELECT id, from_account_id, to_account_id, amount_cents, created_at
			 FROM transfers WHERE idempotency_key = $1`,
			idempotencyKey,
		).Scan(&existing.ID, &existing.FromAccountID, &existing.ToAccountID, &existing.AmountCents, &existing.CreatedAt)
		if selErr != nil {
			return Transfer{}, false, selErr
		}
		if existing.FromAccountID == fromID && existing.ToAccountID == toID && existing.AmountCents == amountCents {
			return existing, true, nil
		}
		return Transfer{}, false, ErrKeyReused
	}
	if err != nil {
		return Transfer{}, false, err
	}

	if balances[fromID] < amountCents {
		return Transfer{}, false, ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance_cents = balance_cents - $1 WHERE id = $2`,
		amountCents, fromID,
	); err != nil {
		return Transfer{}, false, err
	}

	if s.midTransferHook != nil {
		if err := s.midTransferHook(); err != nil {
			return Transfer{}, false, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $2`,
		amountCents, toID,
	); err != nil {
		return Transfer{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Transfer{}, false, err
	}

	return Transfer{
		ID:            transferID,
		FromAccountID: fromID,
		ToAccountID:   toID,
		AmountCents:   amountCents,
		CreatedAt:     createdAt,
	}, false, nil
}
