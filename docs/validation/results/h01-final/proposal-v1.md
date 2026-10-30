# Checkout retry policy proposal — candidate v1

The checkout team proposes retrying every failed upstream charge request once,
including HTTP 409 conflicts, without adding an idempotency key. The goal is
reducing customer-visible transient failures while staying under a 1% upstream
5xx error budget. The table in observations-v1.csv describes actual upstream
attempts, not distinct orders. Each row represents a five-minute window.

The candidate states: "The experiment is safe to roll out. Aggregating the
windows gives fewer than 1% 5xx attempts; 409 is a transient error and retries
cannot cause double charging because the original request failed."

This is a review task only. Do not invoke a payment service or deploy anything.
Assess what the data supports, distinguish observed facts from risks that have
not been measured, and suggest a bounded next validation before rollout.
