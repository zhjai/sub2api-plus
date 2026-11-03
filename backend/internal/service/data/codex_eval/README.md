# Codex Evaluation Context

Source: https://github.com/haowang02/cpa-plugin-codex-candy-eval
Version: v0.4.4
Pinned revision: `179d6dae48a0f0a735637ea2f373345b91caaca8`.

`codex_context.json` is the byte-identical public fixture from
`internal/plugin/data/codex_context.json`. The upstream MIT license is retained
unchanged in `LICENSE`. The pure builder is ported to `../../cpaeval/codex.go`;
the service adapter is `../../openai_eval_codex_request.go`.

Fixture SHA-256:
`65f976b3a7cb410e50312e5b4096686e5b1fda3e4fa1942ee3974d7cb1e7cd42`.
The independent package embeds a second byte-identical copy; its parity test
requires both copies to stay synchronized.

Each physical call creates new thread, turn, context-window and message UUIDv7
identities, and a new prompt-cache key. Installation identity remains stable in
the service's credential namespace. Explicit efforts, including `none`, are
preserved; empty effort remains absent. Nonempty developer instructions lead the
first captured context message. The user prompt is sent without an added English
prefix. Tools are sent as compact RawMessage bytes from the same source as their
prefix ID hash. JSON object key order is not a behavioral inference.

OAuth canonical identity projection, UA and header enforcement remain caller
responsibilities. API-key requests retain the service's request-ID metadata.
The actual selected model is preserved; detection aliases never change routing.
Profiles: `codex-turn-cpa-179d6dae-v1` (OAuth) and
`codex-turn-cpa-179d6dae-apikey-v1` (native API-key Responses).

This local port does not establish upstream model identity or quality. Live
inference, deployment and credential reads are outside this implementation task.
