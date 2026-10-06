-- Prism uses the existing account/group, quota and Composite contracts.
-- Extend, rather than replace, all previously introduced platforms.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                       'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe', 'prism'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                              'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe', 'prism'));
