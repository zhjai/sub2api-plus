# ModelTrace

Source: https://github.com/xqy2006/ModelTrace

Algorithm revision: `df3a0f9d3e054c0dc02d6d586686db8daf8fa7c8`

The active `unified_bank_97623969.json` is the unmodified 17-model bank from
haowang02/cpa-plugin-codex-candy-eval commit
`976239690451849223c2174ffdd08681019753d1`, including the measured
`gpt-6.1-sol` profile, feature transforms and 1/2/3-output calibration.
SHA-256: `a4e256c00444179b76f3855578660e66f30659df8c0122f1768dd05a8d705630`.
`unified_bank.json` remains the historical 16-model bank and is not embedded.
`modeltrace-golden_97623969.json` is the exact matching upstream fixture;
tests verify its probabilities, scores, similarities and rankings unchanged.
`LICENSE_97623969` is the exact license from that commit; `LICENSE` remains
the embedded algorithm license. Full provenance is in `../CPA_DATA_97623969.md`.
The scoring algorithm and Chinese challenge generator in
`../../modeltrace_algorithm.go` and `../../openai_modeltrace.go` are Go ports of
upstream `static/fingerprint-core.js` and `static/challenge-browser.js`.
The upstream MIT license is included in this directory and embedded in the
plugin. No upstream runtime or external service is required.

Probabilities describe only candidates in this bank. They do not prove model
identity and may be misleading for models absent from the bank.
