# Codex Evaluation Context

Source: https://github.com/haowang02/cpa-plugin-codex-candy-eval
Pinned revision: `876d1ab480ca297b82367f09392dbbcb785e86c8`.

`codex_context.json` is the unmodified public request fixture from
`internal/plugin/data/codex_context.json`. Its captured paths use
`/home/user/workspace`; no local prompts, credentials or conversations are
embedded. The upstream MIT license is retained in `LICENSE` and embedded
with the fixture. The original builder is `internal/plugin/codex.go`.

Native Responses Candy, ModelTrace and Fingerprint requests, for both OAuth
and API-key accounts, use this context, a stable credential-scoped installation
and a new independent thread for every sample and physical retry. Session,
turn, window, message IDs and prompt-cache keys are regenerated together.
OAuth tests retain their configured Codex identity, fingerprint convergence,
mapped-model Lite compatibility and canonical headers. API-key tests send the
independent first-turn IDs to the configured compatible endpoint without OAuth
account headers or account-convergence transforms.

Declared tools are never executed. Any returned tool call invalidates the
sample diagnostically, not as a model-quality failure. State Probe keeps its
own paired-session protocol; Chat Completions tests keep their native protocol.

Deliberate differences from the plugin builder: Sub2API preserves explicit
reasoning efforts (including `none`; only empty means unspecified), retains
its canonical top-level OAuth instructions and identity projection, and adds
a no-external-tools prefix to the test prompt. Tools are decoded per sample
and re-encoded; their IDs are derived from the pinned fixture representation.
Even a mixed text/tool response is an invalid diagnostic sample. These choices
are identified by `request_profile=codex-turn-cpa-876d1ab4-v1` for OAuth and
`codex-turn-cpa-876d1ab4-apikey-v1` for native API-key Responses in run outcomes.
New session identifiers reduce reuse of an upstream session binding; they
cannot force an upstream gateway to choose a different credential or disable
provider-side prefix caching. The pinned question and scoring rules are not
randomized, and no expected answer is added to the request.

The fixed context adds input tokens to every physical request. Existing RPM,
concurrency, send budget, Retry-After and sampling-window controls still apply;
token usage is measured from the response. Historical results and the pinned
classification banks are retained. Classification under a different request
context remains behavioral evidence, not proof of upstream model identity or
of CPA/Sub2API wire-level equivalence. Validate with controlled real-account
comparisons before drawing quality conclusions.
