import { describe, expect, it } from 'vitest'
import {
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
  OPENAI_EXCEL_CALLBACK_ORIGIN,
  OPENAI_EXCEL_CALLBACK_PATH,
  OPENAI_EXCEL_OAUTH_CLIENT_ID,
  hasAnyOpenAIBPSGrant,
  hasIndependentOpenAIBPSGrant,
  isNativeOpenAIOAuthEligibleForBPSGrant,
  isOpenAIBPSAccount,
  parseOpenAIExcelCallback,
  resolveOpenAIOAuthClient,
} from '../openaiOAuthClient'

const callback = `${OPENAI_EXCEL_CALLBACK_ORIGIN}${OPENAI_EXCEL_CALLBACK_PATH}`

describe('Excel OAuth callback validation', () => {
  it('accepts the official callback and compares the actual opaque session state', () => {
    expect(parseOpenAIExcelCallback(`${callback}?code=one-time&state=opaque-state`, 'opaque-state'))
      .toEqual({ ok: true, code: 'one-time', state: 'opaque-state' })
  })

  it.each([
    ['one-time', 'not_url'],
    [`https://bps.openai.com.evil.invalid${OPENAI_EXCEL_CALLBACK_PATH}?code=c&state=s`, 'wrong_origin'],
    [`http://bps.openai.com${OPENAI_EXCEL_CALLBACK_PATH}?code=c&state=s`, 'wrong_origin'],
    [`https://user@bps.openai.com${OPENAI_EXCEL_CALLBACK_PATH}?code=c&state=s`, 'wrong_origin'],
    [`${OPENAI_EXCEL_CALLBACK_ORIGIN}/other/callback?code=c&state=s`, 'wrong_path'],
    [`${callback}?code=c&state=other`, 'state_mismatch'],
    [`${callback}?code=c&code=d&state=s`, 'missing_code'],
    [`${callback}?code=c&state=s&state=s`, 'missing_state'],
    [`${callback}?code=c`, 'missing_state'],
    [`${callback}?state=s`, 'missing_code'],
    [`${callback}?error=access_denied&state=s`, 'oauth_error'],
  ])('rejects an invalid callback (%s)', (input, error) => {
    expect(parseOpenAIExcelCallback(input, 's')).toMatchObject({ ok: false, error })
  })

  it('requires an active authorization session', () => {
    expect(parseOpenAIExcelCallback(`${callback}?code=c&state=s`, ''))
      .toEqual({ ok: false, error: 'no_session' })
  })

  it('identifies BPS only by OpenAI OAuth grant type', () => {
    const credentials = { client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID }
    expect(resolveOpenAIOAuthClient(credentials)).toBe('excel')
    expect(isOpenAIBPSAccount({ platform: 'openai', type: 'oauth', credentials })).toBe(true)
    expect(isOpenAIBPSAccount({ platform: 'openai', type: 'apikey', credentials })).toBe(false)
    expect(resolveOpenAIOAuthClient({ client_id: 'codex-client' })).toBe('codex')
  })

  it('distinguishes primary BPS-only accounts from native OAuth accounts with an independent nested BPS grant', () => {
    const dualAccount = {
      platform: 'openai',
      type: 'oauth',
      credentials: {
        client_id: 'codex-client',
        [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: {
          client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
          access_token: 'nested-excel-at'
        }
      }
    }
    expect(isOpenAIBPSAccount(dualAccount)).toBe(false)
    expect(isNativeOpenAIOAuthEligibleForBPSGrant(dualAccount)).toBe(true)
    expect(hasIndependentOpenAIBPSGrant(dualAccount)).toBe(true)
    expect(hasAnyOpenAIBPSGrant(dualAccount)).toBe(true)

    const redactedDual = {
      platform: 'openai',
      type: 'oauth',
      credentials: {},
      credentials_status: { has_excel_bps_credentials: true }
    }
    expect(hasIndependentOpenAIBPSGrant(redactedDual)).toBe(true)

    expect(
      isNativeOpenAIOAuthEligibleForBPSGrant({
        platform: 'openai',
        type: 'oauth',
        parent_account_id: 7,
        credentials: {}
      })
    ).toBe(false)
    expect(
      isNativeOpenAIOAuthEligibleForBPSGrant({
        platform: 'openai',
        type: 'oauth',
        credentials: { auth_mode: 'AgentIdentity' }
      })
    ).toBe(false)
  })
})
