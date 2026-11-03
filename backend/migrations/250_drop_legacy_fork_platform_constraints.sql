-- The released fork migration 245 restored these whitelists after upstream 242.
-- Keep that migration immutable and reconcile both upgrade and fresh-install order.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
