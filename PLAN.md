# PLAN: phases and commits

One phase = one increment from `CLAUDE.md` = one commit. Loop for every phase:

1. Ask the assistant for the increment (it stops when done).
2. `make test`; read the real output.
3. Read the whole diff. Be able to explain every line. Ask about anything unclear.
4. Add a line to `BUILDLOG.md` (timeline row, plus "AI was wrong" / "rejected" if anything happened).
5. Commit yourself (`git add -p` is a good habit), then `git push`.

## Status

- [x] Docs commit: SPEC, CLAUDE, BUILDLOG (`f54a094`)
- [x] Phase 0: before the clock (Go/Docker gates cleared, stray `koho-interview/`
      deleted, `.gitattributes` added, clock started 2026-10-12 14:56 PDT.
      `BRIEF.md` added but bundled into the Phase 1 commit instead of its own
      `docs: add original brief` commit — note this in BUILDLOG's commit-honesty
      section.)
- [x] Phase 1: Skeleton (`ef5160b`)
- [x] Phase 2: Accounts (`88d8d7f`)
- [x] Phase 3: Transfer core (`be331c7`)
- [x] Phase 4: Competing transfers, test 7 (break-it output in BUILDLOG.md)
- [x] Phase 5: Idempotency (tests 8, 9, 10; test 7 regression-checked green)
- [x] Phase 6: Atomicity test seam, test 11
- [ ] Phase 7 .. 9 below

## Phase 0: before the clock

- Install Go 1.22+ (`go version`) and start Docker Desktop (`docker compose version`).
  This is the "Go gate" in the build log. If Go is a problem, switch `STACK` in CLAUDE.md to Python.
- Add `BRIEF.md` (KOHO's original assignment). CLAUDE.md says it wins over SPEC.md.
  Read it against SPEC.md and write down any conflict before coding.
- Delete the stray empty `koho-interview/` folder (a nested git clone).
- Add `.gitattributes` with `* text=auto eol=lf` (Windows CRLF warnings).
- `git push -u origin main`. Needs a GitHub login (`gh auth login`).
- Commit: `docs: add original brief`
- Start the clock. Write the start time in `BUILDLOG.md`.

## Phase 1: Skeleton

Build: `go.mod`, `docker-compose.yml`, `schema.sql`, `cmd/server/main.go` (boots, applies schema),
Makefile (`db`, `run`, `test`), `.gitignore`, one test that connects to Postgres.
Done when: `make db && make test` passes.
Commit: `feat: project skeleton with postgres and schema`

## Phase 2: Accounts

Build: `POST /accounts`, `GET /accounts/{id}`, validation, error body format. Tests 1 and 5 (account part).
Done when: tests green; invalid bodies (`10.5`, `"700"`, negative, unknown field) get 400.
Commit: `feat: create and fetch accounts`

## Phase 3: Transfer core (no idempotency yet)

Build: `Store.Transfer`: lock both rows `ORDER BY id FOR UPDATE`, check balance on the locked row,
relative debit and credit. Tests 2, 3, 4, 5, 6.
Done when: tests green. Check that every statement runs on `tx`.
Commit: `feat: transfers with row locking`

## Phase 4: Competing transfers (test 7)

Build: test 7 over `httptest.NewServer`, goroutines released by `close(start)`, 50 iterations.
Then break the lock on purpose (plain read + absolute write), watch it fail on total money, and
paste the real failing output into `BUILDLOG.md`. Restore the lock and re-run.
Done when: test 7 green after restore; failure output saved.
Commit: `test: competing transfers never overdraw (test 7)`

## Phase 5: Idempotency

Build: `Idempotency-Key` header, `INSERT ... ON CONFLICT DO NOTHING RETURNING` after the locks,
200 on replay, 422 on reuse with a different body. Tests 8, 9, 10. Test 7 stays green.
Commit: `feat: idempotent transfers via Idempotency-Key`

## Phase 6: Atomicity

Build: nil-in-production hook between debit and credit, test 11 (must fail after a real write).
Commit: `test: injected mid-transfer failure rolls back (test 11)`

## Phase 7: History

Build: `GET /accounts/{id}/transactions`, newest first. Test 12.
Optional tests 13-15 go in a separate commit if time allows: `test: reconciliation and deadlock stress`
Commit: `feat: account transaction history`

## Phase 8: Adversarial review

New session, fresh context: ask it to break the invariants and find the traps from CLAUDE.md.
Fix real findings, one commit each with a test. Log what it got wrong and what you rejected.
Commits: `fix: <finding>` or `docs: review notes`

## Phase 9: README and wrap-up

README: how to run, design, invariants, why lock-first, out of scope (list from SPEC.md).
Finish `BUILDLOG.md` honestly (total time, say so if over 4 hours). Fresh-clone check:
clone into a temp folder, `make db && make test`.
Commit: `docs: README and final build log`

## Cut order if behind

Optional tests, deterministic lock-holder test, history ordering, test 11, history.
Never cut: test 7 with break-it, tests 8 and 9, README, build log, fresh-clone check.
