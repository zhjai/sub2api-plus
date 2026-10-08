# Codex Evaluation Context

Source: https://github.com/haowang02/cpa-plugin-codex-candy-eval
Pinned revision: `876d1ab480ca297b82367f09392dbbcb785e86c8`.

`codex_context.json` is the unmodified public request fixture from
`internal/plugin/data/codex_context.json`. Its captured paths use
`/home/user/workspace`; no local prompts, credentials or conversations are
embedded. The upstream MIT license is retained in `LICENSE` and embedded
with the fixture. The original builder is `internal/plugin/codex.go`.

OAuth Candy, ModelTrace and Fingerprint requests use this context, a stable
credential-scoped installation and a new independent thread for every sample.
The configured Codex identity, fingerprint convergence, mapped-model Lite
compatibility and canonical headers still apply. Internal tests do not acquire
the API Key authorization required by `preserve_client`.

Declared tools are never executed. Any returned tool call invalidates the
sample diagnostically, not as a model-quality failure. State Probe keeps its
own paired-session protocol; API-key and Chat Completions tests are unchanged.

Deliberate differences from the plugin builder: Sub2API preserves explicit
reasoning efforts (including `none`; only empty means unspecified), retains
its canonical top-level OAuth instructions and identity projection, and adds
a no-external-tools prefix to the test prompt. Tools are decoded per sample
and re-encoded; their IDs are derived from the pinned fixture representation.
Even a mixed text/tool response is an invalid diagnostic sample. These choices
are identified by `request_profile=codex-turn-cpa-876d1ab4-v1` in run outcomes.

The fixed context adds input tokens to every physical request. Existing RPM,
concurrency, send budget, Retry-After and sampling-window controls still apply;
token usage is measured from the response. Historical results and the pinned
classification banks are retained. Classification under a different request
context remains behavioral evidence, not proof of upstream model identity or
of CPA/Sub2API wire-level equivalence. Validate with controlled real-account
comparisons before drawing quality conclusions.
