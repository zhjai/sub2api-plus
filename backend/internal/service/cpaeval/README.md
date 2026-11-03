# CPA evaluation port

Source: https://github.com/haowang02/cpa-plugin-codex-candy-eval
Version: v0.4.4
Revision: `179d6dae48a0f0a735637ea2f373345b91caaca8`
License: MIT, Copyright (c) 2026 Hao Wang; original bytes in `LICENSE`.

The detector derives from Ikaleio/lm-detector revision
`a4ef212df06817e8071acd05f00219f4c9eb2f9a` (MIT).
Its original attribution, README and license are preserved byte for byte in
`data/lm-detector/`. The detector binary, models, enrollment suite and golden
fixture are copied unchanged from the pinned CPA revision.

## Source boundaries

- `fingerprint_detector.go`: unchanged apart from `package cpaeval`.
- `fingerprint.go`: unchanged suite, random draw, aliases, model normalization,
  constants and `fingerprintText` sections; plugin runners/HTTP handlers omitted.
- `candy.go`: unchanged prompt and `hasStandalone21`.
- `pelican.go`: unchanged variants, lookup, `page`, extraction and size limit.
- `codex.go`: upstream types, compact RawMessage tools, prefix hashes, UUIDv7,
  context injection and turn builder. The turn body is factored into
  `codexTurnWithInstallationAt` so the service supplies installation and time;
  the original credential-derived installation behavior remains in `codexTurn`
  for the upstream tests. Request/thread/turn/message IDs remain fresh per call.
- `decode.go`: upstream `mustDecode` helper.
- `preview.go`: evaluated `PREVIEW_FREEZE` string from `web/pelican.js`.
- `api.go`: exported aliases and adapters only; returned catalogs/tools are copies.
- Upstream detector golden, parsing/challenge, Codex prefix/turn/instructions,
  standalone-21 and code-extraction tests retain their original function bodies.
  Plugin host, persistence and HTTP handler tests are not applicable to this package.

The service-compatible map adapter keeps tools as `json.RawMessage` from the
same compact bytes used by the upstream prefix hash. Serialize with HTML escaping
disabled, as `marshalOpenAIEvalPayload` does. JSON object key ordering is not used
as evidence about model behavior.

The context fixture also resides in `../data/codex_eval/` for service compatibility.
A parity test requires both copies to match; update both from the same revision.
SHA-256: `65f976b3a7cb410e50312e5b4096686e5b1fda3e4fa1942ee3974d7cb1e7cd42`.

The exported integration API is documented in `api.go`; upstream parity and
concurrent isolation are covered by this package's tests.
