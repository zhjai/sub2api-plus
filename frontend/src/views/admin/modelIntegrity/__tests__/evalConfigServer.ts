import type { OpenAIEvalAccountPriorityRule, OpenAIEvalConfig } from '@/api/admin/accounts'

/** Account plus its sorted, lower-cased model list: the handler's base identity for a rule. */
function baseKey(rule: OpenAIEvalAccountPriorityRule): string {
  const models = (rule.requested_models ?? []).map(model => model.trim().toLowerCase()).sort()
  return `${rule.account_id}|${models.join(',')}`
}

/**
 * A stored config behind getOpenAIEvalConfig / saveOpenAIEvalConfig that
 * merges account priority rules the way openai_eval_handler.go does. The
 * payload goes through JSON first, so an undefined key is truly absent. A
 * missing `condition` keeps the stored condition of the rule with the same
 * base identity (clients older than conditions); an explicit null clears it.
 * A missing `enabled` likewise keeps the stored switch.
 */
export function evalConfigServer(initial: OpenAIEvalConfig) {
  let stored: OpenAIEvalConfig = JSON.parse(JSON.stringify(initial))
  const read = async () => JSON.parse(JSON.stringify(stored)) as OpenAIEvalConfig
  const save = async (payload: OpenAIEvalConfig) => {
    const wire = JSON.parse(JSON.stringify(payload)) as OpenAIEvalConfig
    const previous = new Map((stored.account_priority_rules ?? []).map(rule => [baseKey(rule), rule]))
    if (wire.account_priority_rules) {
      wire.account_priority_rules = wire.account_priority_rules.map(rule => {
        const old = previous.get(baseKey(rule))
        const merged = { ...rule }
        if (!('condition' in rule) && old?.condition) merged.condition = { ...old.condition }
        if (!('enabled' in rule) && old && 'enabled' in old) merged.enabled = old.enabled
        // The server stores no condition for an explicit null and omits it on read.
        if (merged.condition === null) delete merged.condition
        return merged
      })
    } else {
      wire.account_priority_rules = stored.account_priority_rules
    }
    stored = { ...stored, ...wire, revision: (stored.revision ?? 0) + 1 }
    return read()
  }
  return { read, save, stored: () => stored }
}
