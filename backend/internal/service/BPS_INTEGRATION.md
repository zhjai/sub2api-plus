# BPS OAuth integration

BPS accounts are ordinary OpenAI OAuth accounts identified by the official
Excel OAuth `client_id`. A Codex refresh token cannot be converted into an
Excel grant by changing its client ID. Create a separate account using the BPS
login entry or an existing Excel refresh token.

The browser flow uses PKCE and the official fixed callback. Paste the complete
callback URL into the admin dialog. The opaque state must match the generated
authorization session; a valid exchange consumes that session once, even when
the exchange fails. Reauthorization and refresh retain the Excel client.

Supported: HTTP Responses, streaming or JSON output, client function/custom
tools, model mapping, existing proxy/RPM/concurrency and usage accounting.
Connection tests and manual/automatic evaluations use the same BPS conversion.

Not exposed by this integration: native WebSocket, compact, dedicated images,
native Chat Completions/Messages bridges, inline-image relay, automatic tool
repair or BPS/native switching. Full expanded history is required instead of
`previous_response_id`. HTTPS image input is handled by the imported adapter;
it does not imply image generation or upload support.

The request is sent to the fixed official BPS Responses URL with a fresh header
allowlist and redirects disabled. Client cookies and Codex identity metadata
are not forwarded. Requested and effective effort remain separate. OpenAI
decides model access and the actual response model; there is no guarantee of
entitlement or quality, and offline tests do not prove successful live generation.

The protocol package is imported from ranxi2001/sub2api production commit
`3e07fa89096cb58b260c032aa6de2cb2a2aa98b7` with NOTICE and LICENSE preserved.
Its README documents the upstream integration, including features not enabled
here. The cpa-bps reference commit
`aaee30218e174e5a6c495b935d20d4014d432f49` distributes binaries; no business
source, installer or binary from that distribution is included or executed.


## Codex collaboration compatibility

The gateway preserves client tool namespaces, custom input and expanded tool
history. Synthesized plaintext collaboration calls carry an explicit empty
`encrypted_function_args` array in JSON and every corresponding SSE tool item.
Direct calls that explicitly declare encrypted collaboration arguments are
rejected before any tool in that response batch is dispatched. Current encrypted
agent assignments are rejected before preparing or sending an upstream request;
the error includes a safe `param` path, never assignment content. Refresh the
model catalog and start a new plaintext-compatible conversation when recovering
from stale encrypted-v2 client state. Repeating the ciphertext cannot decrypt it.

Collaboration detection covers namespace declarations and flattened dotted or
double-underscore names, including the functions wrapper namespace. Client tool
names and namespaces remain exactly as declared. Encrypted historical agent
content requires a new plaintext conversation; this integration exposes no
historical-omission switch.

Replay and deferred-tool catalogs are bounded in-memory caches, scoped by account,
API key, thread and execution lane. Sibling threads sharing a session remain
separate. Anonymous requests do not share those caches. No persistent state store
or historical-encryption omission option is exposed by this integration.

Third-party API-key routes do not infer encrypted multi-agent support from a
model name. Explicit upstream declarations, including null, are retained. The
administrator's model synchronization can fetch the actual Codex manifest to
save capability evidence; merely listing configured/pinned catalogs does not
probe additional accounts. Official native defaults remain available.

Offline coverage includes synthetic two-gateway JSON/SSE collaboration and
parent continuation. It does not establish BPS entitlement or full real Codex
multi-agent acceptance. Native hosted services and private encrypted state are
not reconstructed by this bridge.

The owned BPS adapter advertises v2 collaboration only for routes served entirely
by this plaintext-capable adapter. Native/BPS mixed pools retain the portable
fallback declaration for new client sessions. Existing encrypted assignments
still require a fresh plaintext conversation. Explicit null restrictions from selected upstream manifests stay
null, and unknown third-party API-key routes still need capability evidence.
The adapter does not force a separate subagent reasoning tier.
The mixed-pool declaration also applies when the next selected account is native;
use separate groups or explicit model routing to a pure transport pool when
that client session needs its transport-specific v2 declaration.

BPS accounts cannot fetch a live native Codex model manifest. A pinned BPS source
now supplies a local catalog from its explicit public model mappings or concrete
group model selections, without any upstream discovery request. The same local
policy applies to pinned standard model lists and BPS account model discovery.
Wildcard mappings need concrete group selections; absent explicit configuration
returns an actionable configuration error. A configured catalog filtered to an
empty list remains authoritative. Public aliases are projected once, and final
group filtering, mixed-pool capability restrictions and ETag/304 remain in force.
These catalogs describe configured routing, not verified upstream entitlement.
Composite live catalogs apply the BPS/native capability intersection using the
actual Responses routes without changing other platforms' metadata.

The forwarding bridge observes the forwarder's supplied context, preserving its
existing billing/drain policy. Connection/evaluation bridges observe their own
request contexts. Cancelling these contexts closes a blocked upstream body;
none of these changes enables automatic tool correction or network replay.

Collaboration recognition targets Codex's declared
collaboration namespace and documented flattened/functions-wrapper forms; other
host namespaces and private encrypted agent state are not established support.

Isolated runtime acceptance with Codex CLI 0.167.1 covers two real BPS child
agents reading separate synthetic local files, returning their markers, and
reusing the same agents for another tool round. Both the portable v1 path and
native collaboration.spawn_agent/send_message/followup_task path completed.
Five real CLI child threads also completed against deterministic BPS responses;
that is broader runtime/protocol coverage, not five-child live BPS acceptance.
A prior live v2 attempt disconnected before tool dispatch and is retained as a
failed attempt; a later instrumented attempt completed without gateway errors.
These isolated checks do not establish production deployment, long-duration
stability or support for private encrypted OpenAI state.
