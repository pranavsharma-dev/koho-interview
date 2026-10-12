# SPEC: Move the Money

## Invariants

| # | Invariant | Mechanism | Proven by |
| --- | --- | --- | --- |
| 1 | A balance never goes negative | Row lock on sender before the check; CHECK constraint as backstop | Test 7 (competing transfers) |
| 2 | A transfer is atomic | Key, debit and credit in one transaction; deferred rollback | Test 11 (injected failure) |
| 3 | A transfer with a given key applies once | UNIQUE key inserted inside the transaction, after the locks | Tests 8, 9, 10 |
| 4 | Amounts are exact | Integer cents end to end | Test 5 |

Note: the CHECK constraint alone is not enough. With an unlocked read and an absolute
write, two transfers both compute 300 and both write 300: no negative, but 700 cents
created from nothing. Only a total-money assertion catches this.

## Schema (schema.sql)

```sql
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
```

Reconciliation rule: balance = opening + transfers in - transfers out.

## API

All amounts are integer cents. One currency (CAD). JSON in and out.

| Endpoint | Request | Success | Errors |
| --- | --- | --- | --- |
| `POST /accounts` | `{"starting_balance_cents": 1000}` | 201 `{"id":1,"balance_cents":1000}` | 400 invalid body / negative |
| `GET /accounts/{id}` | | 200 `{"id":1,"balance_cents":300}` | 404 |
| `POST /transfers` | Header `Idempotency-Key`; body `{"from_account_id":1,"to_account_id":2,"amount_cents":700}` | 201 transfer; 200 same body on replay | 400 missing key / invalid body / self-transfer; 404 unknown account; 409 `insufficient_funds`; 422 `idempotency_key_reused` |
| `GET /accounts/{id}/transactions` | | 200 list, newest first: `transfer_id`, `direction` (`in`/`out`), `counterparty_account_id`, `amount_cents`, `created_at` | 404 |

Transfer response body: `{"id","from_account_id","to_account_id","amount_cents","created_at"}`.

Error body, always: `{"error":{"code":"insufficient_funds","message":"..."}}`.
Codes: `invalid_request`, `missing_idempotency_key`, `account_not_found`,
`insufficient_funds`, `idempotency_key_reused`, `internal`.

### Validation

- Decode with `DisallowUnknownFields` into `int64` fields. That already rejects `10.5`,
  `7.0`, `1e2`, `"700"`, and values beyond int64.
- Missing field and `null` decode silently to 0, and negatives decode fine, so also check
  explicitly: `amount_cents > 0`, `starting_balance_cents >= 0`, and an upper bound of
  10^12 cents on both.
- Use pointer fields (`*int64`) if you want to distinguish missing from 0; otherwise the
  `> 0` check covers transfers. For starting balance, 0 is valid, so missing vs 0 is a
  choice: document it.
- `from == to` is 400, not a no-op.
- Idempotency-Key: required, non-empty, max 255 chars.

## The transfer transaction (Store.Transfer)

Default READ COMMITTED. Every step on `tx`.

1. `BeginTx`; `defer tx.Rollback()`.
2. `SELECT id, balance_cents FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`.
   Fewer than 2 rows: `ErrAccountNotFound`.
3. `INSERT INTO transfers (idempotency_key, from_account_id, to_account_id, amount_cents)
   VALUES (...) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id, created_at`.
4. No row returned: key exists. SELECT that transfer. Same from/to/amount: return it as a
   replay (handler sends 200). Different: `ErrKeyReused`. Return (rollback is harmless).
5. Sender's locked balance < amount: `ErrInsufficientFunds`.
6. `UPDATE accounts SET balance_cents = balance_cents - $1 WHERE id = $2`;
   `UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $3`.
   (Test seam: optional hook called between these two; nil in production.)
7. `Commit`, check its error.

Every early return rolls back, removing the step-3 row. So a rejected transfer does not
consume its key and the same key can be retried. (Deliberate design choice.)

Why lock first: inserting a transfer row takes FK share locks on both accounts. Two
transfers from one sender would each hold that share lock and then wait on each other's
FOR UPDATE: deadlock. Why ORDER BY id: opposite-direction transfers would otherwise lock
in opposite orders and deadlock.

### Duplicate cases

| Case | Result |
| --- | --- |
| Same key, same body, after first finished | 200, original transfer, balances unchanged |
| Same key, same body, while first running | Waits on the unique index, then 200 original |
| Same key, different body | 422, nothing applied |
| Same key after first was rejected | Evaluated again from scratch |

## Tests (all against real Postgres; truncate tables `RESTART IDENTITY CASCADE` per test)

| # | Test | Expected | Inv. |
| --- | --- | --- | --- |
| 1 | Create and read account | 201, then balance 1000 | |
| 2 | Transfer succeeds A=1000,B=0, send 700 | 201; A=300, B=700 | 2,4 |
| 3 | Insufficient funds A=100, send 700 | 409; unchanged; no transfer row | 1 |
| 4 | Exact balance A=700, send 700 | 201; A=0 | 1 |
| 5 | Invalid amounts: 0, -5, 10.5, "700", missing, oversize, unknown field | 400 each; nothing written | 4 |
| 6 | Unknown account (999); self-transfer | 404; 400 | |
| 7 | **Competing transfers** A=1000, two 700s to B and C at once | Exactly one 201, one 409; A=300; B+C=700; total 1000; one transfer row | 1 |
| 8 | Replay: same key and body twice | 201 then 200, same id; A debited once | 3 |
| 9 | Same key, amount 800 | 422; nothing applied | 3 |
| 10 | Ten goroutines, one key, one body | All succeed, one transfer id; debited once | 3 |
| 11 | Injected failure between debit and credit | Error; both balances unchanged; no row; same key then succeeds | 2 |
| 12 | History for B after three transfers | Correct direction, counterparty, amount, newest first | |

Optional if time: 13 reconciliation after 200 random transfers; 14 opposite-direction
transfers in 20 goroutines, no deadlock, total conserved; 15 missing key = 400.

### Making test 7 mean something

- Go through HTTP with `httptest.NewServer` and the production handler and pool, so each
  request has its own connection.
- Goroutines wait on a shared channel; `close(start)` releases them together.
- Loop 50 times with fresh accounts. Assert status codes (loser must be exactly 409, not
  merely non-201), balances read from the DB, total money, and transfer count.
- Break-it check: replace `FOR UPDATE` read + relative update with a plain read + absolute
  update; the test must fail (on total money) within a few iterations. Restore.
- Optional deterministic version: a separate connection holds `FOR UPDATE` on A; fire both
  requests; assert neither returns within 200 ms; commit the holder; assert as above.

## Out of scope (state in README)

Auth, pagination, multi-currency, idempotent account creation, key expiry and per-client
key scoping, full double-entry ledger, metrics, migration tooling, cross-service
atomicity (outbox/workflow engine).
