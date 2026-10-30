# Repository working rules

- Read `README.md`, `protocol/m1/README.md`, and the relevant contract before changing behavior. `docs/implementation-plan.md` records work that is still to do; it is not evidence of implemented capabilities.
- This is the independent Agent↔Task Bridge project. Do not import p-drive runtime, old v9 drafts, private Agent sessions, or internal SQLite access into the public protocol.
- Treat the frozen file set in `protocol/m1/manifest.json` as a baseline. Follow `docs/versioning.md` for changes; do not regenerate checksums to conceal an unintended edit or silently alter the same protocol version.
- Keep Task/Assignment/Run/Submission/Decision semantics separate. Parent relations never imply permissions or state aggregation. Public tests must not bypass auth, lease, revision, or RPC boundaries.
- Validate protocol artifacts with `tools/verify-package.py`. Static validation is distinct from runtime and independent-client acceptance; record each honestly.
- Give parallel contributors clear file/module ownership, preserve others' edits, and reconcile shared contracts before implementation branches diverge.
- Use host-level collaboration instructions and p-drive recording when available; these tools are not runtime or validation dependencies of the project. Never store real credentials, raw lease tokens, local database files, or private transcripts in Git.
- If `.codegraph/` exists at repository root, use CodeGraph before code search. Otherwise skip CodeGraph and do not auto-index.
