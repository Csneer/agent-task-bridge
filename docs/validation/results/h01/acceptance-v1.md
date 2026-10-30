# Review acceptance v1

Produce a UTF-8 JSON object with these fields:
- input_sha256: mapping from each input filename (proposal-v1.md,
  observations-v1.csv, acceptance-v1.md) to its verified digest.
- totals: baseline and candidate objects containing attempts, http_2xx,
  http_409, http_5xx, client_timeouts, and error_basis_points.
  error_basis_points = round(10000 * http_5xx / attempts), rounding .5 up.
- candidate_within_budget: boolean; true iff candidate 5xx / attempts < 1%.
- recommendation: hold or proceed, justified in findings.
- findings: array of at least three objects with claim, evidence, implication.
  Cite row names or proposal text. Cover the numerical error-budget claim,
  retry/idempotency risk, and the distinction between attempts and orders.
- next_validation: concrete bounded experiment or evidence collection plan.

Do not claim observed duplicate charges: the supplied data does not measure
charges, orders or idempotency. Do not infer all 409 errors are retryable without
an upstream contract. Distinguish observed numerical facts from unmeasured
failure risks. Recommendations must follow the supplied evidence. A reviewer
will recalculate the metrics, inspect the narrative and fetch the exact artifact
before deciding. Do not execute side effects against payment systems.
