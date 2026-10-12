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
