package bank

import (
	"context"
	"database/sql"
	"errors"
)

var ErrAccountNotFound = errors.New("account not found")

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
