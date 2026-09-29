-- Keep the CPA bridge key on the existing shared `lucen` OpenAI pool instead
-- of a disabled single-rate group. Upstream billing is authoritative for this
-- bridge, so the Sub2API group multiplier is intentionally not preserved.
--
UPDATE api_keys AS k
SET group_id = route.id,
    updated_at = NOW()
FROM groups AS old_group,
     groups AS route
WHERE k.name = 'lucen-0.06'
  AND k.group_id = old_group.id
  AND old_group.name = 'lucen-0.06'
  AND route.name = 'lucen'
  AND route.platform = 'openai'
  AND k.deleted_at IS NULL;
