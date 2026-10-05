# Account Quality Reference

Under `avoid_degradation`, requests without configured diagnostic tests for the
requested model and reasoning effort can use the last evaluated account-wide
pass rate as a routing reference. Ordinary request error-rate and first-output
latency samples do not erase that reference.

This applies to both the default policy and model-specific policy rules. A
model-specific `avoid_degradation` rule can use the reference even when the
default policy is different; the global account overview order is not copied.

The order is effective quality descending, exact diagnostic evidence before an
equal account reference, then the existing operational score and account ID.
Known zero percent remains ahead of unknown quality. Other policy presets and
custom weights are unchanged in this patch.

## Evidence Boundaries

- Configured exact diagnostics take precedence, including stale, missing or
  insufficient results. An account reference cannot replace these results.
- The reference is separate from current-model quality. `quality_ratio`, quality
  state and diagnostic counts remain exact-only. Admin request records expose
  `account_quality_prior` with source dimensions, evaluation ID and expiry.
- References come from the published account evaluation. Other-model test
  changes enter the reference on the next scheduled evaluation or manual
  evaluation, not on every request. Reference validity is bounded by both the
  evaluation interval and the earliest contributing evidence expiry.
- Only configured tests participate, as before. Running a diagnostic manually
  without configuring its corresponding target/test does not enroll it in
  scheduling evidence.
- Current group membership, enabled account state, model compatibility, RPM,
  concurrency, cooldowns and required response ownership remain authoritative.
  No account is added to a group and no partially emitted response is replayed.

The existing fully cold account-overview ordering is retained. When request
metrics become available, the new reference preserves quality tiers while the
requested model/effort's operational data determines ordering within each tier.
An account reference is a preference, not proof that every model or reasoning
effort served by that account has the same quality.
