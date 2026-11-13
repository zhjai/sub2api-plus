/**
 * BPS (official Excel login) account settings that are specific to the BPS
 * transport. Native Codex options (WebSocket, compact, passthrough, quota
 * reset, namespace flattening) never apply to these accounts and are not
 * modelled here.
 */

/** Suggested model IDs for a new BPS account. Upstream decides availability. */
export const OPENAI_BPS_RECOMMENDED_MODELS: readonly string[] = ['gpt-6-astra', 'gpt-6.1-sol']

export const OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY = 'openai_bps_skip_historical_encrypted'
export const OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY = 'openai_bps_hosted_tools_policy'

export type OpenAIBPSHostedToolsPolicy = 'omit' | 'reject'

export interface OpenAIBPSOptions {
  skipHistoricalEncrypted: boolean
  hostedToolsPolicy: OpenAIBPSHostedToolsPolicy
}

export const DEFAULT_OPENAI_BPS_OPTIONS: Readonly<OpenAIBPSOptions> = Object.freeze({
  skipHistoricalEncrypted: false,
  hostedToolsPolicy: 'omit'
})

type ExtraLike = Record<string, unknown> | null | undefined

export function readOpenAIBPSOptions(extra: ExtraLike): OpenAIBPSOptions {
  return {
    skipHistoricalEncrypted: extra?.[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] === true,
    hostedToolsPolicy: extra?.[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] === 'reject' ? 'reject' : 'omit'
  }
}

/**
 * Writes BPS options into a mutable extra map.
 *
 * - `options === null` (not a BPS account): both keys are removed.
 * - Defaults are not stored unless the key already existed, in which case the
 *   default is written explicitly so a stored non-default value is cleared even
 *   when the server merges instead of replacing.
 */
export function applyOpenAIBPSOptions(
  extra: Record<string, unknown>,
  options: OpenAIBPSOptions | null,
  previous?: ExtraLike
): void {
  if (!options) {
    delete extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]
    delete extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]
    return
  }
  const had = (key: string) => previous != null && Object.prototype.hasOwnProperty.call(previous, key)

  if (options.skipHistoricalEncrypted) {
    extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] = true
  } else if (had(OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY)) {
    extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] = false
  } else {
    delete extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]
  }

  if (options.hostedToolsPolicy === 'reject') {
    extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] = 'reject'
  } else if (had(OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY)) {
    extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] = 'omit'
  } else {
    delete extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]
  }
}

export interface ModelMappingRow {
  from: string
  to: string
}

/** True when the account names at least one concrete model of its own. */
export function hasExplicitOpenAIBPSModels(allowedModels: readonly string[], mappings: readonly ModelMappingRow[]): boolean {
  const concrete = (value: string) => {
    const trimmed = value.trim()
    return trimmed !== '' && !trimmed.includes('*')
  }
  return allowedModels.some(concrete) || mappings.some((row) => concrete(row.from) && row.to.trim() !== '')
}

/** Adds the recommended models to a whitelist without duplicating existing entries. */
export function mergeRecommendedBPSWhitelist(current: readonly string[]): { models: string[]; added: number } {
  const models = [...current]
  let added = 0
  for (const model of OPENAI_BPS_RECOMMENDED_MODELS) {
    if (!models.includes(model)) {
      models.push(model)
      added++
    }
  }
  return { models, added }
}

/** Adds identity mappings for the recommended models without touching existing rows. */
export function mergeRecommendedBPSMappings(current: readonly ModelMappingRow[]): { rows: ModelMappingRow[]; added: number } {
  const rows = current.map((row) => ({ ...row }))
  const existing = new Set(rows.map((row) => row.from.trim()))
  let added = 0
  for (const model of OPENAI_BPS_RECOMMENDED_MODELS) {
    if (!existing.has(model)) {
      rows.push({ from: model, to: model })
      existing.add(model)
      added++
    }
  }
  return { rows, added }
}
