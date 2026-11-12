/**
 * OpenAI OAuth login client profiles.
 *
 * - `codex`: the ordinary Codex CLI login (default; existing behavior).
 * - `excel`: the official Excel add-in login used for BPS. Accounts stay
 *   ordinary `openai/oauth` accounts; the saved `credentials.client_id` is
 *   the only thing that marks them as BPS-only.
 *
 * The server owns the OAuth state. The client never derives or reformats it;
 * it only compares the callback's `state` with the one embedded in the
 * authorization URL the server generated for the current session.
 */

export type OpenAIOAuthClient = 'codex' | 'excel'

/** Official Excel add-in OAuth client. Saved accounts with this client_id are BPS-only. */
export const OPENAI_EXCEL_OAUTH_CLIENT_ID = 'app_fnr0pYvVwwFDocDumLG3H2Bp'

/** Fixed official redirect for the Excel client. */
export const OPENAI_EXCEL_CALLBACK_ORIGIN = 'https://bps.openai.com'
export const OPENAI_EXCEL_CALLBACK_PATH =
  '/basispoints/extension/360590d7-f8f9-4d88-bf75-0edfe0a4b9f3/auth/callback'

export function isOpenAIExcelClientId(clientId: unknown): boolean {
  return typeof clientId === 'string' && clientId.trim() === OPENAI_EXCEL_OAUTH_CLIENT_ID
}

/** Picks the login profile from saved account credentials. Anything other than the Excel client is Codex. */
export function resolveOpenAIOAuthClient(
  credentials: Record<string, unknown> | null | undefined
): OpenAIOAuthClient {
  return isOpenAIExcelClientId(credentials?.client_id) ? 'excel' : 'codex'
}

/** True for an ordinary OpenAI OAuth account whose saved client is the Excel (BPS) client. */
export function isOpenAIBPSAccount(
  account: { platform?: string; type?: string; credentials?: Record<string, unknown> | null } | null | undefined
): boolean {
  return (
    !!account &&
    account.platform === 'openai' &&
    account.type === 'oauth' &&
    isOpenAIExcelClientId(account.credentials?.client_id)
  )
}

/**
 * Whether a token response belongs to the login client the session was generated for.
 * An Excel session must return the Excel client_id; a Codex session must never return it.
 */
export function tokenMatchesOpenAIOAuthClient(
  tokenClientId: unknown,
  expected: OpenAIOAuthClient
): boolean {
  if (expected === 'excel') return isOpenAIExcelClientId(tokenClientId)
  return !isOpenAIExcelClientId(tokenClientId)
}

/** Reads the server-generated `state` from an authorization URL. Returns '' when absent. */
export function readOAuthStateFromAuthUrl(authUrl: string | null | undefined): string {
  if (!authUrl) return ''
  try {
    return new URL(authUrl).searchParams.get('state') || ''
  } catch {
    return ''
  }
}

export type OpenAIExcelCallbackError =
  | 'empty'
  | 'no_session'
  | 'not_url'
  | 'wrong_origin'
  | 'wrong_path'
  | 'oauth_error'
  | 'missing_code'
  | 'missing_state'
  | 'state_mismatch'

export type OpenAIExcelCallbackResult =
  | { ok: true; code: string; state: string }
  | { ok: false; error: OpenAIExcelCallbackError; detail?: string }

/**
 * Validates a pasted Excel login callback. Only the full official URL is accepted:
 * https://bps.openai.com/basispoints/extension/<id>/auth/callback?code=...&state=...
 * The state must equal the one in the authorization URL generated for this session.
 */
export function parseOpenAIExcelCallback(
  input: string | null | undefined,
  expectedState: string | null | undefined
): OpenAIExcelCallbackResult {
  const raw = (input || '').trim()
  if (!raw) return { ok: false, error: 'empty' }

  const expected = (expectedState || '').trim()
  if (!expected) return { ok: false, error: 'no_session' }

  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return { ok: false, error: 'not_url' }
  }

  if (url.origin !== OPENAI_EXCEL_CALLBACK_ORIGIN || url.username || url.password) {
    return { ok: false, error: 'wrong_origin' }
  }
  if (url.pathname !== OPENAI_EXCEL_CALLBACK_PATH) {
    return { ok: false, error: 'wrong_path' }
  }

  const oauthError = url.searchParams.get('error')
  if (oauthError) {
    return { ok: false, error: 'oauth_error', detail: oauthError.slice(0, 80) }
  }

  const codes = url.searchParams.getAll('code')
  const code = codes.length === 1 ? codes[0].trim() : ''
  if (!code) return { ok: false, error: 'missing_code' }

  const states = url.searchParams.getAll('state')
  const state = states.length === 1 ? states[0].trim() : ''
  if (!state) return { ok: false, error: 'missing_state' }

  if (state !== expected) return { ok: false, error: 'state_mismatch' }

  return { ok: true, code, state }
}

/** i18n key for a callback validation failure. */
export function openAIExcelCallbackErrorKey(error: OpenAIExcelCallbackError): string {
  const keys: Record<OpenAIExcelCallbackError, string> = {
    empty: 'admin.accounts.oauth.openai.bps.callbackErrors.empty',
    no_session: 'admin.accounts.oauth.openai.bps.callbackErrors.noSession',
    not_url: 'admin.accounts.oauth.openai.bps.callbackErrors.notUrl',
    wrong_origin: 'admin.accounts.oauth.openai.bps.callbackErrors.wrongOrigin',
    wrong_path: 'admin.accounts.oauth.openai.bps.callbackErrors.wrongPath',
    oauth_error: 'admin.accounts.oauth.openai.bps.callbackErrors.oauthError',
    missing_code: 'admin.accounts.oauth.openai.bps.callbackErrors.missingCode',
    missing_state: 'admin.accounts.oauth.openai.bps.callbackErrors.missingState',
    state_mismatch: 'admin.accounts.oauth.openai.bps.callbackErrors.stateMismatch'
  }
  return keys[error]
}
