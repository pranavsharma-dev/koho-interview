CREATE TABLE IF NOT EXISTS accounts (
  id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  opening_balance_cents BIGINT NOT NULL CHECK (opening_balance_cents >= 0),
  balance_cents         BIGINT NOT NULL CHECK (balance_cents >= 0),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transfers (
  id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  idempotency_key  TEXT   NOT NULL UNIQUE,
  from_account_id  BIGINT NOT NULL REFERENCES accounts(id),
  to_account_id    BIGINT NOT NULL REFERENCES accounts(id),
  amount_cents     BIGINT NOT NULL CHECK (amount_cents > 0),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_account_id <> to_account_id)
);
CREATE INDEX IF NOT EXISTS transfers_from_idx ON transfers (from_account_id, id);
CREATE INDEX IF NOT EXISTS transfers_to_idx   ON transfers (to_account_id, id);
