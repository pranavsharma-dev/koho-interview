# CLAUDE.md

## What this is

A take-home for KOHO (Canadian fintech, Subscriptions team): "Move the Money", a small
money-transfer API. Four-hour cap. The reviewer grades correctness under concurrency,
retries and partial failure, plus how I use AI. Not feature count.

Read `SPEC.md` for the design and `BRIEF.md` for KOHO's original assignment. If they
disagree, `BRIEF.md` wins: tell me about the conflict, do not resolve it silently.

## Stack

STACK: Go   <!-- change to Python if I switch; see "Python variant" below -->

- Go 1.22+, standard library `net/http` (Go 1.22 pattern routing), `database/sql` with the
  pgx stdlib driver (`github.com/jackc/pgx/v5/stdlib`)
- PostgreSQL 16 via Docker Compose
- No ORM, no router library, no migration tool, no config framework, no logging library
- Tests run against the real database: `go test ./... -race -count=1`

### Python variant (only if STACK says Python)

FastAPI + psycopg 3 (sync, with a connection pool) + pytest, same schema, same SQL, same
endpoints, same tests. `with conn.transaction():` replaces `defer tx.Rollback()`. Keep
Decimal and float out of every amount path; amounts are `int`.

## Layout

```
cmd/server/main.go            config, open pool, apply schema.sql, register routes
internal/bank/store.go        ALL SQL and ALL transactions
internal/bank/store_test.go   invariant tests against real Postgres
internal/api/handlers.go      decode, validate, call store, map errors to HTTP
internal/api/handlers_test.go HTTP-level tests incl. competing transfers (httptest.NewServer)
schema.sql  docker-compose.yml  Makefile  README.md  BUILDLOG.md
```

Rule: handlers never touch SQL; the store never touches HTTP.

## How we work (important)

I am the engineer; you are a pair. I must be able to explain and modify every line live
in the interview.

1. Do ONE increment at a time (list below). Do not start the next one until I say so.
2. Make the smallest change that satisfies the increment. Touch only the files it needs.
3. Every feature comes with its tests in the same increment.
4. After each increment: run the tests, show me the real output, summarise the diff in a
   few lines, and flag anything you were unsure about. Then stop.
5. Do NOT run `git commit` unless I ask. I commit myself after reading the diff.
6. Never change a test to make it pass. If a test looks wrong, say why and stop.
7. If you add a dependency, name it and say why before adding it.
8. If something in SPEC.md looks wrong to you, say so. Disagreement is useful; silent
   deviation is not.

## Hard rules (the invariants)

- Every statement inside a transfer runs on `tx`, never on `db`/the pool.
- `defer tx.Rollback()` immediately after `BeginTx`. Every early return must leave nothing.
- Lock both account rows with `SELECT ... WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`
  BEFORE inserting the transfer row. (Key-first deadlocks via FK share locks; verified.)
- Balance check happens on the locked row. Updates are relative:
  `balance_cents = balance_cents - $1`. Never compute a new balance in Go and write it.
- Idempotency: `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id` inside
  the same transaction. Never SELECT-then-INSERT. Never an in-memory map.
- No `sync.Mutex` or any process-local lock for correctness. It fails with two replicas.
- Money is integer cents: `int64` in Go, `BIGINT` in SQL, integer in JSON. `grep -rn float`
  must return nothing outside tests.
- Never ignore an error, including from `Commit`, `Rows.Err`, `Scan`.
- Never `SetMaxOpenConns(1)` or anything else that serialises connections.

## Increments (in order)

1. Skeleton: go.mod, docker-compose.yml, schema.sql, server boots, Makefile (`db`, `run`,
   `test`), one test that connects to Postgres. `.gitignore`.
2. Accounts: POST /accounts, GET /accounts/{id}, validation. Tests 1 and 5 (account part).
3. Transfer core (no idempotency yet): lock, check, debit, credit. Tests 2, 3, 4, 5, 6.
4. Competing-transfers test (test 7), exactly as SPEC.md describes. Then I will break the
   lock on purpose to see it fail; help me do that and restore it.
5. Idempotency key handling. Tests 8, 9, 10. Test 7 must stay green.
6. Atomicity test seam + test 11.
7. History endpoint + test 12.
8. Adversarial review (I'll do this in a fresh session).
9. README.

## Traps to avoid (things an assistant typically gets wrong here)

- Check-then-write with no lock (passes every sequential test).
- `s.db.QueryRow` in the middle of a tx block.
- A "concurrency" test that runs requests one after another or shares one connection, or
  only asserts "no error".
- An atomicity test that fails validation before any write and then asserts "unchanged".
- Accepting `"700"` or `10.5`; using float64 anywhere.
- Adding auth, pagination, retries, config frameworks, or anything not in SPEC.md.
- Explanations like "READ COMMITTED prevents this" without naming which lock, on which
  row, taken by which statement.

## Commands

```
make db      # docker compose up -d
make run     # go run ./cmd/server
make test    # go test ./... -race -count=1
```

DATABASE_URL defaults to `postgres://postgres:postgres@localhost:5432/bank?sslmode=disable`.
