# Black-box coverage and evidence boundaries

Status: the final runtime suite and M1 acceptance passed at implementation commit f537f936fafa95fd74be63801fe4b5ac057ce1b4. See ../../docs/validation/results/README.md for the combined evidence.

## Public-interface coverage

- V01–V25 have named runtime tests for their principal lifecycle, authorization, concurrency, recovery, and read scenarios. Names identify the vector, not a claim that every prose subcase is exhausted.
- V19 expands 8 control methods across 9 lifecycle states (72 cells), plus grant management across all states.
- V22 terminates actual child processes at confirmed before-commit and after-commit-before-response hooks, reopens storage, and retries the exact request.
- V05/V16 pause commands before the write transaction and commit revocation first; stale retry with the latest revision remains fenced.
- Draft creation envelopes survive capability expansion and restart. Audit records identify the actual action authority and execution binding.
- Normal periodic maintenance expires leases within 5 seconds without client writes; backward clock returns HTTP 503 and does not commit a report.
- Snapshot quotas cover Principal 2 and authority 16, completed/invalidation reclamation, and exact 300-second expiry.
- Paging accepts 1/100 and rejects 0/101. Initial grants accept 16 and reject 17. Lease checks cover global 5/300 and narrower frozen policy boundaries.
- Snapshot size tests create all legitimate records through HTTP: 10000 records accepted, 10001 rejected; exact 33554432-byte snapshot accepted and 33554433 rejected, measured from public records.
- Storage-gap tests intentionally corrupt only the event table while the daemon is stopped (interior/tail/all deletion). This is unsupported-storage fault injection, never business fixture construction. Old cursor continuation must publicly return REINITIALIZE_REQUIRED; canonical Task remains readable.
- HTTP binding negatives include method/content type/body size, JSON shape, duplicate keys, numeric precision, batch/notification rejection, and authentication ordering.

## Validation

Before the resource/gap additions: full `go test -race ./tests/blackbox ./tests/fixtures -count=1` passed in 27.148 seconds; vet/build passed.

Resource/gap additions: targeted basic tests passed; 10000-record and 32 MiB real HTTP fixtures passed in 7.140 seconds. The resource/gap target set passed with `-race` in 89.714 seconds. After strengthening the byte test to exact equality and one-byte overflow, that test passed normally in 4.522 seconds and with `-race` in 61.036 seconds. Final vet/build checks passed.

## Remaining proof boundaries

- This harness is not H01: independent Go/TypeScript clients, actual agent work, artifact access and actual output acceptance require separate evidence.
- V09 tests server snapshot/replay continuity, not independent consumer checkpoint crash recovery; that belongs to consumer tests.
- Full per-vector evidence is archived under docs/validation/results/runtime/V01.jsonl.gz through V25.jsonl.gz with protocol checksum, implementation commit, redacted request/response, commit/event identifiers, and scheduling points.
- Exact encoded 1 MiB limits are covered in internal/application/capacity_test.go; maintenance_capacity_test.go proves atomic large-contract rejection and successful mandatory expiry with near-limit escaped content through HTTP.
- Startup protocol-version protection is an internal integration guard, not a public black-box assertion.
- Resource corruption tests prove refusal for the injected gaps, not arbitrary old-backup rollback detection or recovery of removed events.
