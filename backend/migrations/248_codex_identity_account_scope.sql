-- Preserve-client identity is configured on the OAuth account, while request
-- continuations remain bound to the API key and user that created them.
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_codex_identity_config_check;

UPDATE accounts
SET extra = (COALESCE(extra, '{}'::jsonb) - 'codex_identity_api_key_id')
    || jsonb_build_object('codex_identity_revision', gen_random_uuid()::text)
WHERE extra ->> 'codex_identity_mode' = 'preserve_client';

ALTER TABLE accounts ADD CONSTRAINT accounts_codex_identity_config_check CHECK (
  extra ->> 'codex_identity_mode' IS DISTINCT FROM 'preserve_client'
  OR (platform = 'openai' AND type IN ('oauth', 'setup-token') AND parent_account_id IS NULL
      AND COALESCE(lower(btrim(credentials ->> 'auth_mode')), '') <> 'agentidentity'
      AND COALESCE(extra ->> 'codex_fingerprint_mode', 'off') = 'off'
      AND COALESCE(extra ->> 'codex_identity_namespace', '') <> ''
      AND COALESCE(extra ->> 'codex_identity_revision', '') <> '')
);
