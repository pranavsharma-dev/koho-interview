# INTERVIEW prep

The goal is not just a working API. It is to explain and change every line live, and to show
good judgment about AI. Personal notes; do not hand this in unless you want to.

## The 60-second story

"Money transfer API in Go and Postgres. Four invariants: no negative balance, atomic transfers,
exactly-once per idempotency key, exact amounts. Each has a mechanism and a test that proves it.
The hard part is concurrency, so I lock first, check on the locked row, and use relative updates."

## Be able to explain without looking

| Question | Answer to have ready |
| --- | --- |
| Why lock before inserting the transfer row? | The insert takes FK share locks on both accounts. Two transfers from one sender each hold that share lock, then wait on each other's `FOR UPDATE`: deadlock. Hit it in Postgres during planning. |
| Why `ORDER BY id`? | A->B and B->A would lock in opposite orders and deadlock. A consistent order removes the cycle. |
| Why relative updates? | `balance = balance - $1` is applied to the locked current value. Absolute writes lose updates: both compute 300 and write 300, so 700 cents appear from nothing. |
| Why isn't the CHECK constraint enough? | It stops negatives only. The lost-update case never goes negative. Only a total-money assertion catches it. It is a backstop, not the mechanism. |
| Does READ COMMITTED prevent this? | No. Say which lock: `FOR UPDATE` on the sender row, taken by the SELECT in step 2, held until commit. |
| How does idempotency work? | UNIQUE key, `INSERT ... ON CONFLICT DO NOTHING RETURNING` inside the same tx, after the locks. Concurrent duplicates wait on the unique index, then see the original. |
| Why not SELECT-then-INSERT or an in-memory map? | Race between the two steps; a map breaks with two replicas. The database is the only shared truth. |
| Why can't I use a mutex? | Process-local. Fails with a second instance. |
| Same key after a rejected transfer? | Rollback removes the row, so the key is not consumed. The retry is evaluated from scratch. Deliberate. |
| Same key, different body? | 422 `idempotency_key_reused`, nothing applied. |
| Why integer cents? | Exact; floats can't represent 0.1. `int64`/`BIGINT`/JSON integer. `DisallowUnknownFields` plus `int64` rejects `10.5`, `"700"`, `1e2`. |
| Why does test 7 mean something? | Real HTTP server, separate connection per request, `close(start)` releases goroutines together, 50 fresh iterations, loser must be exactly 409, asserts balances from the DB and total money. |
| How do I know test 7 can fail? | I broke the lock on purpose and saw it fail on total money. Output is in BUILDLOG.md. |
| Why a test seam in `Store.Transfer`? | To fail between debit and credit and prove rollback. Cost: production code exists for a test. Named as a discomfort. |

## Known limits (say them before they ask)

- Idempotency keys are global and never expire; no per-client scoping.
- Test 7 is probabilistic: not every interleaving, not two server instances.
- No auth, pagination, multi-currency, ledger, outbox. Listed as out of scope in the README.
- Single Postgres node. Cross-service atomicity would need an outbox or workflow engine.
- No `SetMaxOpenConns`/`SetMaxIdleConns` tuning, and no retry/backoff on transient DB
  errors (e.g. "too many clients") — a raw 500 surfaces instead. Haven't needed to tune
  this at this scale; know it, don't pretend it's handled.

## Live-change picks: which ones are actually harder than they look

From the "predict live-change requests" list below, the three most likely for a
Subscriptions-team interviewer — fee, key expiry, per-client scoping — are each more
work than "which file, which SQL" implies. Know the real friction *before* being asked
live, not while being asked:

- **Add a fee.** `Store.Transfer`'s lock query is hardcoded to exactly two rows
  (`WHERE id IN ($1, $2)`). A fee leg is a third account balance mutation inside the
  same tx, so the two-row lock pattern needs generalizing, not just one more `UPDATE`.
  Also: no migration tool (CLAUDE.md), and `schema.sql` is applied via
  `CREATE TABLE IF NOT EXISTS` — adding a `fee_cents` column needs a hand-written
  `ALTER TABLE` that won't retroactively apply without recreating the DB. This is where
  the "no migration tool" tradeoff stops being hypothetical.
- **Expire idempotency keys after 24h.** `idempotency_key` is a plain DB-level `UNIQUE`
  constraint with no time dimension. "Expire after 24h" isn't a one-line `WHERE` added
  to the replay-lookup SELECT — the `UNIQUE` constraint itself still blocks reinserting
  an expired key's value. Needs either a cleanup job deleting expired rows, or turning it
  into a partial/expression unique index. More schema surgery than it sounds.
- **Scope keys per client.** Same `ALTER TABLE` problem, plus the constraint becomes
  composite (`UNIQUE(client_id, idempotency_key)`), not just an added column.
- **By contrast, `GET /transfers/{id}` really is easy** — mirrors `GetAccount` almost
  exactly, no schema change. Good one if a warm-up before a harder pick is offered.

## Questions not already answered above

- **Why Postgres over SQLite, really?** README answers "why Postgres" at the surface
  level; the sharper follow-up is "doesn't SQLite's `BEGIN IMMEDIATE` also lock?" — yes,
  but it's one write lock for the *whole database*, not per-row. SQLite would serialize
  every transfer process-wide, not just the ones actually contending for the same
  accounts. That's the real distinction, not just "Postgres is a real database."
- **What's the migration story given no migration tool?** Honest answer: hand-run
  `ALTER`s against `schema.sql` plus a fresh `CREATE TABLE IF NOT EXISTS` apply on boot —
  fine for a take-home, not what I'd ship for a team iterating on schema weekly. Know
  this is a real gap, not a non-answer.
- **How would you load-test this?** Nothing exists today — test 7 is 2 goroutines x 50
  iterations, which proves correctness, not throughput. Would reach for `pgbench` against
  the DB directly or `k6`/`hey` against the HTTP layer at real concurrency.
- **The commit-succeeds-but-the-response-never-arrives scenario** (classic idempotency
  edge case): `Store.Transfer` commits the DB transaction *before* the handler writes the
  HTTP response. If that write fails or the client times out waiting, the transfer is
  already durable. A client retry with the same key hits the `ON CONFLICT` no-row branch,
  compares from/to/amount, matches, and correctly replays with 200 (not 201, since this
  one didn't originate the transfer). Traced this for real — it's correct as built. Worth
  bringing up proactively rather than waiting to be asked, since nothing currently says so
  anywhere else in these docs.

## What to say about AI

- Real example: the assistant's first transfer design claimed the key before locking and deadlocked
  in Postgres. Found by running it, fixed, then proved by tests 7 and 10.
- Show the BUILDLOG sections: where AI helped, where it was wrong, what I rejected.
- Rules I set: one increment at a time, tests with every feature, I commit after reading the diff,
  never edit a test to make it pass.
- Only write real entries. If a section is empty, that is more honest than invented ones.

## Practice before the interview

- [ ] Re-derive the lock-first deadlock on a whiteboard, with two sessions.
- [ ] Open `store.go` cold and walk through Transfer top to bottom, naming the lock at each step.
- [ ] Break each invariant once yourself and watch the matching test fail.
- [ ] Predict live-change requests: add a fee, add a currency field, add `GET /transfers/{id}`,
      expire idempotency keys after 24h, scope keys per client, cap daily transfer total.
      For each: which file, which SQL, which test.
- [ ] Be ready to answer "what would you do with another day?" (lock-holder test, key expiry,
      ledger table, metrics).
- [ ] Run a fresh clone and `make db && make test` the day before.
- [ ] Commit Phase 8's work (`internal/api/adversarial_test.go`, the `go.mod` fix, the
      `schema.sql` CHECK additions, and the BUILDLOG/PLAN/INTERVIEW doc updates) after
      reading the diff — not done yet as of this entry.
- [ ] Reproduce the Phase 8 `EXPLAIN` finding live: remove `ORDER BY id`, watch the
      opposite-direction deadlock test still pass, then run `EXPLAIN` to show *why*
      (ascending index scan on the PK, independent of `ORDER BY`, at this table size) —
      good answer if asked "how do you know your test actually proves what it claims?"
