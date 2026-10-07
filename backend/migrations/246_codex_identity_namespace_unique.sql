-- The application derives this opaque namespace from the actual credential.
-- Enforce uniqueness across instances, including inactive duplicate imports.
CREATE UNIQUE INDEX IF NOT EXISTS accounts_codex_identity_namespace_unique
ON accounts ((extra ->> 'codex_identity_namespace'))
WHERE deleted_at IS NULL
  AND extra ->> 'codex_identity_mode' = 'preserve_client';

-- A background credential refresh can bypass the full account save service.
-- Revoke atomically when the stable namespace inputs change. Token rotation
-- under a stable ChatGPT account/user leaves the authorization unchanged.
CREATE OR REPLACE FUNCTION revoke_codex_identity_on_credential_change()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  namespace_changed boolean;
BEGIN
  namespace_changed := OLD.platform IS DISTINCT FROM NEW.platform
       OR OLD.type IS DISTINCT FROM NEW.type
       OR OLD.parent_account_id IS DISTINCT FROM NEW.parent_account_id
       OR COALESCE(NULLIF(lower(btrim(OLD.credentials ->> 'auth_mode')), ''), 'oauth') IS DISTINCT FROM COALESCE(NULLIF(lower(btrim(NEW.credentials ->> 'auth_mode')), ''), 'oauth')
       OR NULLIF(btrim(OLD.credentials ->> 'chatgpt_account_id'), '') IS DISTINCT FROM NULLIF(btrim(NEW.credentials ->> 'chatgpt_account_id'), '')
       OR NULLIF(btrim(OLD.credentials ->> 'chatgpt_user_id'), '') IS DISTINCT FROM NULLIF(btrim(NEW.credentials ->> 'chatgpt_user_id'), '')
       OR (OLD.type = 'setup-token' AND NULLIF(btrim(OLD.credentials ->> 'chatgpt_account_id'), '') IS NULL
           AND NULLIF(btrim(OLD.credentials ->> 'access_token'), '') IS DISTINCT FROM NULLIF(btrim(NEW.credentials ->> 'access_token'), ''));
  IF OLD.extra ->> 'codex_identity_mode' = 'preserve_client' AND namespace_changed THEN
    NEW.extra := (COALESCE(NEW.extra, '{}'::jsonb) - 'codex_identity_api_key_id') || jsonb_build_object(
      'codex_identity_mode', 'isolated',
      'codex_identity_namespace', '',
      'codex_identity_revision', gen_random_uuid()::text,
      'codex_identity_diagnostic', 'namespace_changed: experiment revoked by credential refresh; verify credentials and explicitly enable again');
  ELSIF OLD.extra ? 'codex_identity_revision' THEN
    -- Retain history after disabling: subsequent credential changes must not
    -- retain an old revision. Default accounts without history are untouched.
    IF NOT (COALESCE(NEW.extra, '{}'::jsonb) ? 'codex_identity_revision')
       OR COALESCE(NEW.extra ->> 'codex_identity_revision', '') = '' THEN
      NEW.extra := (COALESCE(NEW.extra, '{}'::jsonb) - 'codex_identity_api_key_id') || jsonb_build_object(
        'codex_identity_mode', 'isolated',
        'codex_identity_namespace', '',
        'codex_identity_revision', gen_random_uuid()::text);
    ELSIF namespace_changed
       OR OLD.extra ->> 'codex_identity_mode' IS DISTINCT FROM NEW.extra ->> 'codex_identity_mode'
       OR OLD.extra ->> 'codex_identity_api_key_id' IS DISTINCT FROM NEW.extra ->> 'codex_identity_api_key_id'
       OR OLD.extra ->> 'codex_fingerprint_seed' IS DISTINCT FROM NEW.extra ->> 'codex_fingerprint_seed'
       OR COALESCE(OLD.extra ->> 'codex_fingerprint_mode', 'off') IS DISTINCT FROM COALESCE(NEW.extra ->> 'codex_fingerprint_mode', 'off') THEN
      NEW.extra := NEW.extra || jsonb_build_object('codex_identity_revision', gen_random_uuid()::text);
      IF namespace_changed AND NEW.extra ->> 'codex_identity_mode' IS DISTINCT FROM 'preserve_client' THEN
        NEW.extra := NEW.extra || jsonb_build_object('codex_identity_namespace', '');
      END IF;
    END IF;
  END IF;
  IF OLD.extra ? 'codex_identity_diagnostic'
     AND NEW.extra ->> 'codex_identity_mode' IS DISTINCT FROM 'preserve_client'
     AND NOT (NEW.extra ? 'codex_identity_diagnostic') THEN
    NEW.extra := NEW.extra || jsonb_build_object('codex_identity_diagnostic', OLD.extra ->> 'codex_identity_diagnostic');
  END IF;
  IF OLD.extra -> 'codex_identity_experiment_history' = 'true'::jsonb
     OR OLD.extra ->> 'codex_identity_mode' = 'preserve_client'
     OR NEW.extra ->> 'codex_identity_mode' = 'preserve_client' THEN
    NEW.extra := NEW.extra || '{"codex_identity_experiment_history":true}'::jsonb;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS accounts_codex_identity_credential_change ON accounts;
CREATE TRIGGER accounts_codex_identity_credential_change
BEFORE UPDATE OF credentials, extra, platform, type, parent_account_id ON accounts
FOR EACH ROW EXECUTE FUNCTION revoke_codex_identity_on_credential_change();

-- Also guard patch/bulk paths and concurrent fingerprint edits at the database.
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'accounts_codex_identity_config_check' AND conrelid = 'accounts'::regclass) THEN
ALTER TABLE accounts ADD CONSTRAINT accounts_codex_identity_config_check CHECK (
  extra ->> 'codex_identity_mode' IS DISTINCT FROM 'preserve_client'
  OR (platform = 'openai' AND type IN ('oauth', 'setup-token') AND parent_account_id IS NULL
      AND COALESCE(lower(btrim(credentials ->> 'auth_mode')), '') <> 'agentidentity'
      AND COALESCE(extra ->> 'codex_fingerprint_mode', 'off') = 'off'
      AND COALESCE(extra ->> 'codex_identity_namespace', '') <> ''
      AND COALESCE(extra ->> 'codex_identity_revision', '') <> ''
      AND COALESCE(extra ->> 'codex_identity_api_key_id', '') ~ '^[1-9][0-9]*$')
);
END IF;
END $$;
