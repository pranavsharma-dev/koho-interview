# Move the Money

A small money-transfer API: accounts hold a balance, money moves between them. Built for
a KOHO take-home; see `BRIEF.md` for the original assignment and `SPEC.md` for the detailed
design this implementation follows.

## Running it

Prerequisites: Go 1.22+, Docker Desktop.

```
make db      # docker compose up -d --wait, then applies schema.sql via psql
make run     # go run ./cmd/server, listens on :8080
make test    # go test ./... -race -count=1
```

`DATABASE_URL` defaults to `postgres://postgres:postgres@localhost:5432/bank?sslmode=disable`
and can be overridden. `make db` is idempotent (`CREATE TABLE IF NOT EXISTS`) — safe to
re-run.

**Note on `-race`:** Go's race detector needs `cgo`, which needs an actual C compiler —
nothing to do with this codebase, a toolchain fact. If one isn't available, `go test` can
run inside the official `golang` Docker image instead, joined to the same Docker network as
the `postgres` service:

```
docker run --rm -v "$(pwd):/app" -w /app --network <compose-network-name> \
  -e DATABASE_URL="postgres://postgres:postgres@<postgres-container-name>:5432/bank?sslmode=disable" \
  golang:1.25 go test ./... -race -count=1
```

(`docker compose ps` / `docker network ls` show the actual container and network names,
which `docker compose` derives from the project directory name.)

## API

| Endpoint | Request | Success | Errors |
| --- | --- | --- | --- |
| `POST /accounts` | `{"starting_balance_cents": 1000}` | 201 `{"id":1,"balance_cents":1000}` | 400 |
| `GET /accounts/{id}` | | 200 `{"id":1,"balance_cents":300}` | 404 |
| `POST /transfers` | Header `Idempotency-Key`; body `{"from_account_id":1,"to_account_id":2,"amount_cents":700}` | 201 new transfer; 200 replay of an identical request | 400 / 404 / 409 `insufficient_funds` / 422 `idempotency_key_reused` |
| `GET /accounts/{id}/transactions` | | 200 list, newest first | 404 |

All amounts are integer cents, one currency. Errors are always
`{"error":{"code":"...","message":"..."}}`.

## Design

```
cmd/server/main.go     boots, opens the pool, applies schema.sql, registers routes
internal/bank/store.go all SQL, all transactions (handlers never touch SQL)
internal/api/handlers.go  decode, validate, call the store, map errors to HTTP
schema.sql              accounts + transfers tables
```

Postgres 16 via Docker Compose, no ORM/router/migration tool — plain `database/sql` with
the `pgx` stdlib driver. Chosen because the hard part of this assignment is concurrency
correctness under a real transactional database, not framework plumbing; a relational
database with row locking is the simplest tool that actually gives the guarantees the
invariants below need. (SQLite or in-memory storage were both allowed by the brief, but
neither gives real cross-connection row locking, which is the actual mechanism this system
relies on — so neither would have let me prove invariant 1 the way test 7 does.)

## Invariants and how each is proven

| # | Invariant | Mechanism | Proven by |
| --- | --- | --- | --- |
| 1 | Balance never goes negative | Row lock on the sender before the balance check; `CHECK` constraint as backstop | Test 7 — 50 iterations of two concurrent transfers racing for the same money, real HTTP, real goroutines |
| 2 | A transfer is atomic | Idempotency-key insert, debit, and credit all run inside one transaction, with `defer tx.Rollback()` immediately after `BeginTx` | Test 11 — a failure injected between debit and credit via a test-only hook; verifies both balances unchanged and no transfer row after |
| 3 | A transfer with a given key applies once | `UNIQUE` constraint on `idempotency_key`, claimed via `INSERT ... ON CONFLICT DO NOTHING` *after* the row locks | Tests 8, 9, 10 — replay, reuse-with-different-body, and ten concurrent goroutines submitting the same key |
| 4 | Amounts are exact | Integer cents end to end (`int64` / `BIGINT` / JSON integer); `DisallowUnknownFields` plus strict `int64` decoding rejects floats, strings, and unknown fields | Test 5 |

### Why lock before inserting the transfer row

Inserting a transfer row takes an FK share lock on both referenced accounts. If the account
rows were locked *after* the insert instead of before, two transfers sent from the same
account would each hold their own insert's share lock and then block waiting for each
other's `FOR UPDATE` on the sender row — a deadlock. (This is not hypothetical: it's exactly
what happened when this was first designed, in a planning session before the clock started
— see `BUILDLOG.md`.) Locking first, in a consistent order (`ORDER BY id`, so opposite-
direction transfers can't lock in opposite sequences), removes the cycle entirely.

### Why relative updates, not absolute

`balance_cents = balance_cents - $1`, applied to the row *as locked*, not a value computed
in Go from an earlier read. An absolute write (`balance_cents = $1` from a value read before
the lock, or without a lock at all) lets two concurrent transfers both read the same stale
balance, both compute their own "correct-looking" result, and have the second write clobber
the first — no negative balance ever appears (so the `CHECK` constraint never fires), but
money is created or destroyed. Verified for real: see `BUILDLOG.md`'s "Test 7 break-it
result" for the actual failing output from deliberately reintroducing this bug.

## Out of scope

Auth, pagination, multi-currency, idempotent account creation, idempotency-key expiry and
per-client key scoping, a full double-entry ledger, metrics, migration tooling, and
cross-service atomicity (would need an outbox or workflow engine — this is a single
Postgres node).

## What I'd do next

- A deterministic version of the lock contention test: hold `FOR UPDATE` on one connection,
  fire both requests, assert neither returns within some bound, then release and assert.
  Test 7 as written is real but probabilistic — it doesn't prove every interleaving.
- Idempotency key expiry (e.g. 24h) and per-client scoping — keys are currently global and
  never expire.
- A proper double-entry ledger table instead of mutating `balance_cents` directly; would
  make reconciliation and audit trivial instead of a derived query.
- Basic metrics (transfer latency, lock wait time) — nothing is instrumented right now.
- The history query's two single-direction indexes don't actually serve its `ORDER BY`
  once combined with `OR` (confirmed via `EXPLAIN` — see `BUILDLOG.md`); a covering index
  or a `UNION ALL` of two indexed sub-queries would fix this if history pages got large.

## Build log

`BUILDLOG.md` has the full timeline, where AI helped, what it got wrong (or didn't, this
time — logged honestly either way), what was rejected, and the real output from
deliberately breaking and restoring the lock in test 7.
