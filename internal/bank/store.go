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
// deadlocks), inserts the transfer row, checks the sender's locked balance,
// then applies relative debit/credit updates. Idempotency-key deduplication
// is not implemented yet (Phase 5): a reused key currently surfaces as a
// raw unique-constraint error.
func (s *Store) Transfer(ctx context.Context, idempotencyKey string, fromID, toID, amountCents int64) (Transfer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, balance_cents FROM accounts WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`,
		fromID, toID,
	)
	if err != nil {
		return Transfer{}, err
	}
	balances := make(map[int64]int64, 2)
	for rows.Next() {
		var id, balance int64
		if err := rows.Scan(&id, &balance); err != nil {
			rows.Close()
			return Transfer{}, err
		}
		balances[id] = balance
	}
	if err := rows.Err(); err != nil {
		return Transfer{}, err
	}
	if len(balances) < 2 {
		return Transfer{}, ErrAccountNotFound
	}

	var transferID int64
	var createdAt time.Time
	err = tx.QueryRowContext(ctx,
		`INSERT INTO transfers (idempotency_key, from_account_id, to_account_id, amount_cents)
		 VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
		idempotencyKey, fromID, toID, amountCents,
	).Scan(&transferID, &createdAt)
	if err != nil {
		return Transfer{}, err
	}

	if balances[fromID] < amountCents {
		return Transfer{}, ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance_cents = balance_cents - $1 WHERE id = $2`,
		amountCents, fromID,
	); err != nil {
		return Transfer{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $2`,
		amountCents, toID,
	); err != nil {
		return Transfer{}, err
	}

	if err := tx.Commit(); err != nil {
		return Transfer{}, err
	}

	return Transfer{
		ID:            transferID,
		FromAccountID: fromID,
		ToAccountID:   toID,
		AmountCents:   amountCents,
		CreatedAt:     createdAt,
	}, nil
}
