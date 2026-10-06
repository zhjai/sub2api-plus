# Prism source attribution

The native Prism integration incorporates code from [zhjai/oai-prism](https://github.com/zhjai/oai-prism), pinned to commit `a97dbdc60bbaa22e123ecfbb1db3d0380bf75080`.

Copyright (c) 2026 alanbulan. The source is licensed under MIT. The complete permission and warranty notice is retained in [`backend/internal/pkg/prismbridge/LICENSE`](../backend/internal/pkg/prismbridge/LICENSE), with provenance in its [`NOTICE.md`](../backend/internal/pkg/prismbridge/NOTICE.md). The bridge subdirectory also retains the MIT license.

| Imported/adapted source | Location in this repository |
| --- | --- |
| `internal/creds`, `internal/httpc`, `internal/prism`, `internal/upstream`, `internal/sentinel`, including associated regression tests and embedded browser profile/script | Corresponding packages under `backend/internal/pkg/prismbridge/` |
| Protocol types/defaults from `internal/config/config.go` | `backend/internal/pkg/prismbridge/config/config.go`; standalone configuration loader/server settings were not imported |
| Facade input/tool-bridge and native project/sandbox execution semantics | `backend/internal/pkg/prismbridge/bridge/`; adapted to Sub2API gateway, storage and admission boundaries |
| Cookie import, PKCE authorization and account catalog behavior | `backend/internal/service/prism_account*.go`; Sub2API administrator authorization, transactions and account storage replace the standalone server/account store |

Local adaptations include import paths, English outbound locale/profile defaults, safe credential error formatting, strict access-token session verification, account-specific catalog validation, Responses/Chat adapters, complete-history tool continuation and existing Redis-backed state. There is no external Prism sidecar requirement. Compact and silent history truncation are unsupported. The Codex tool bridge is an adapter capability, not a claim that Prism natively exposes those tools.

The MIT notices apply to incorporated source; the surrounding Sub2API fork remains under its existing [LGPL license](../LICENSE). Identical license and provenance copies under `backend/resources/licenses/prism/` are included in release archives and both container build paths. Existing third-party notices elsewhere in the repository remain applicable. Go dependencies retain their own upstream licenses and are resolved through `backend/go.mod` and `backend/go.sum`; this document does not replace those licenses.

`ranxi2001/sub2api` commit `a9060260e2755fe650d0de6b6d7c99d1362c4950` was inspected for reliability comparisons. No source from that reference was incorporated by the comparison pass; its root LGPL-3.0 notice is not being relabeled as MIT.
