export default {
  modelIntegrity: {
    nav: {
      aria: 'Model integrity pages',
      tests: 'Integrity tests',
      scheduling: 'Integrity scheduling'
    },
    common: {
      save: 'Save changes',
      saving: 'Saving…',
      saved: 'Saved',
      refresh: 'Refresh',
      reload: 'Reload',
      unsaved: 'Unsaved changes',
      saveFailed: 'Could not save',
      loadFailed: 'Settings did not load. Check the connection and try again.',
      conflict: 'These settings were just changed elsewhere (another page or admin). Reload before saving; unsaved edits here will need to be redone.',
      defaultEffort: 'Default effort',
      allEfforts: 'Any reasoning effort',
      cancel: 'Cancel',
      close: 'Close',
      remove: 'Remove',
      confirm: 'Confirm',
      retry: 'Retry',
      leaveConfirm: 'You have unsaved changes. Leave this page?'
    },
    status: {
      pass: 'Normal',
      consistent: 'Matches reference',
      warning: 'Some wrong answers',
      different: 'Differs from reference',
      insufficient: 'Not enough samples',
      uncertain: 'Inconclusive',
      error: 'Did not finish',
      running: 'Running',
      healthy: 'Route healthy',
      degraded: 'Route unhealthy',
      inconclusive: 'Inconclusive'
    },
    reason: {
      stateProbe: {
        healthy: 'Both requests used the same route.',
        degraded: 'The route changed between the two requests. Three in a row switch accounts with automatic switching to BPS.',
        inconclusive: 'Could not tell whether the route changed this time.'
      },
      running: 'Still running. Refresh in a moment.',
      noDetail: 'No explanation was attached.',
      unknown: 'No conclusion this time (code {code}).',
      correct_answer: 'Answered the candy question correctly.',
      all_public_candy_variants_passed: 'All 5 answers were correct.',
      single_public_item_failed: 'Wrong answer this time. The question is public, so one miss does not prove a downgrade. Run it again.',
      one_or_more_public_candy_variants_failed: 'At least 1 of 5 answers was not {answer}. Treat it as a reminder and retest later.',
      insufficient_valid_samples: 'Only {valid}/{required} usable answers, not enough to conclude. The upstream may be unstable; try later.',
      insufficient_cells: 'Too few usable samples to conclude.',
      no_versioned_baseline: 'No reference samples exist for this model yet.',
      behavior_distribution_consistent_with_versioned_reference: 'Answer habits match this model’s reference samples.',
      behavior_distribution_differs_from_reference: 'Answer habits differ clearly from the reference. The model may have been swapped; retest to confirm.',
      fingerprint_is_identity_evidence_not_capability: 'Shows a difference in answer habits only, not lower capability.',
      modeltrace_behavioral_attribution: 'A guess from answer habits; it cannot prove the real model.',
      modeltrace_insufficient_outputs: 'Too few usable answers to make a guess.',
      timeout: 'Upstream timed out before the test finished. Try again later.',
      context_deadline_exceeded: 'The test ran past its time limit.',
      rate_limit: 'Upstream rate-limited the test. Try again later.',
      http_429: 'Upstream returned 429 (too many requests).',
      http_401: 'Upstream rejected the credentials. Check the account.',
      http_403: 'Upstream denied access. Check the account’s permissions or quota.',
      http_5xx: 'Upstream server error.',
      invalid_api_key: 'Account credentials are invalid.',
      model_not_found: 'Upstream does not know this model. Check the model mapping.',
      response_incomplete: 'The upstream answer was cut off; no conclusion.',
      response_failed: 'The upstream answer failed; no conclusion.',
      previous_response_not_found: 'Upstream could not find the previous answer; no conclusion.',
      upstream_error: 'Upstream returned an unknown error; no conclusion.'
    },
    tests: {
      title: 'Integrity tests',
      description: 'Check on a schedule, with fixed questions and sampling, that accounts serve the model you asked for. Results are reminders only and never change account selection.',
      budget: 'Automatic tests send about {requests} upstream requests per day ({plans} automatic plans).',
      budgetNone: 'No automatic tests yet. Only manual tests send requests.',
      budgetHint: 'These requests are billed like normal traffic.',
      targets: 'Test targets',
      targetsHint: 'Each target is one account + model + reasoning effort, tested independently.',
      search: 'Search accounts or models',
      addTargets: 'Add test targets',
      noTargets: 'No test targets yet',
      noTargetsHint: 'Pick the accounts and model to check, then test manually or on a schedule.',
      noMatch: 'No matching targets',
      autoCount: '{count} automatic',
      manualOnly: 'Manual only',
      removeTarget: 'Remove target',
      removeConfirmTitle: 'Remove this test target?',
      removeConfirm: 'Automatic tests for {target} will stop; history is kept. Takes effect after saving.',
      selectTarget: 'Select a target on the left to see and edit it.',
      types: {
        candy: {
          name: 'Candy question',
          what: 'Asks one question with a known answer 5 times. The question is public, so a miss only means “retest”.'
        },
        fingerprint: {
          name: 'Behavior fingerprint',
          what: 'Samples answer habits and compares them with this model’s reference samples.'
        },
        modeltrace: {
          name: 'ModelTrace attribution',
          what: 'Uses 3 requests to guess which model the answers resemble. Reference only.'
        },
        state_probe: {
          name: 'State probe',
          what: 'Sends two linked requests to check whether the route was switched mid-way. Drives automatic BPS switching.'
        }
      },
      alertOnly: 'Reminder only',
      runNow: 'Test now',
      running: 'Testing…',
      runDone: 'Test finished',
      runFailed: 'Test could not start',
      runAllTitle: 'Run every test type once',
      auto: 'Run automatically',
      every: 'Every',
      interval: {
        m15: '15 minutes',
        h1: '1 hour',
        h6: '6 hours',
        h24: '1 day',
        d3: '3 days',
        d7: '7 days',
        custom: 'Custom ({value})'
      },
      jitter: 'Random delay (minutes)',
      jitterHint: 'Up to {max} minutes, so accounts do not all fire at once.',
      jitterUnavailable: 'The interval is already the minimum; no random delay possible.',
      sampleMode: 'Sample size',
      sampleModes: {
        quick: 'Quick',
        standard: 'Standard',
        strict: 'Strict',
        custom: 'Current: {mode}'
      },
      perRun: '{count} requests per run',
      perDay: '≈ {count} requests / day',
      perDayOff: 'Not automatic',
      nextRun: 'Next {time}',
      lastRun: 'Last {time}',
      neverRun: 'Never tested',
      onlyDirectOAuth: 'Only for direct OpenAI OAuth accounts on the default reasoning effort.',
      goScheduling: 'See BPS on the scheduling page',
      manualSampleTitle: 'How many samples this time?',
      manualSampleHint: 'Only this manual run is affected, not the schedule. More samples give a firmer answer and cost more requests.',
      manualSampleOption: '{mode}: {count} requests',
      history: 'Test history',
      historyHint: 'Only recent runs are kept. Prompts and credentials are never stored.',
      historyScopeAll: 'All targets',
      historyScopeTarget: 'This target only',
      filterType: 'All tests',
      filterResult: 'All results',
      filterAttention: 'Needs attention',
      filterOk: 'Normal',
      filterNeutral: 'Inconclusive',
      noHistory: 'No test runs yet.',
      noHistoryMatch: 'No runs match these filters.',
      columns: {
        time: 'Time',
        target: 'Target',
        test: 'Test',
        result: 'Result',
        samples: 'Usable samples',
        cost: 'Est. cost',
        trigger: 'Started by'
      },
      trigger: {
        manual: 'Manual',
        scheduled: 'Schedule'
      },
      costUnknown: 'n/a',
      detail: {
        title: 'Test details',
        samples: 'Usable samples',
        requests: 'Requests',
        tokens: 'Input / output tokens',
        duration: 'Duration',
        cost: 'Est. cost',
        upstreamModel: 'Model reported upstream',
        baseline: 'Reference version',
        technical: 'Technical details',
        candidates: 'Closest models',
        fingerprintMetric: 'Difference {jsd} (lower is closer), significance p = {p}',
        modeltraceMetric: 'Closest to {model}, probability {probability}',
        stateProbeMetric: 'Status codes {mint} / {cont}, {ticket}',
        newTicket: 'route was switched',
        sameTicket: 'route unchanged'
      },
      add: {
        title: 'Add test targets',
        accounts: 'Accounts',
        accountsHint: 'Select one or more. Only OpenAI accounts are listed.',
        searchAccounts: 'Search by account name or ID',
        noAccounts: 'No OpenAI accounts found.',
        model: 'Requested model',
        modelPlaceholder: 'Choose a model',
        effort: 'Reasoning effort',
        selected: '{count} accounts selected',
        submit: 'Add {count}',
        added: 'Added {count} test targets. Save to apply.',
        skipped: '{count} combinations already existed and were skipped.'
      },
      baselineNote: 'Fingerprint reference version {version}.'
    },
    scheduling: {
      title: 'Integrity scheduling',
      description: 'Decide which account serves a request first, and what happens when the normal route misbehaves.',
      policy: {
        title: 'How accounts are picked',
        hint: 'Only decides the order when several accounts can serve. Whether an account can serve at all is decided first by the checks alongside.',
        defaultLabel: 'Site default',
        factors: {
          price: 'Price',
          errors: 'Error rate',
          speed: 'First-token speed'
        },
        levelAria: '{factor}: {level} of 4',
        options: {
          legacy: {
            name: 'Keep as is',
            effect: 'Uses the weights from system settings, same as before the upgrade.'
          },
          cost_first: {
            name: 'Cheapest first',
            effect: 'Lower-multiplier accounts go first. Errors and speed still count, but less.'
          },
          stability_first: {
            name: 'Most stable first',
            effect: 'Accounts with fewer recent errors and faster first tokens go first; price follows system settings.'
          },
          avoid_degradation: {
            name: 'Avoid degraded models',
            effect: 'Almost never picks an account for being cheap; prefers accounts with few recent errors and steady responses.'
          }
        },
        avoidNote: 'Test results are reminders for now and never exclude an account, so today “Avoid degraded models” differs from “Most stable first” only by caring less about price.',
        sharedNote: 'Load, concurrency and account priority work the same under all four options.'
      },
      rules: {
        title: 'Per-model overrides',
        hint: 'Add one when a model needs a different option. Rules with a reasoning effort win over rules without.',
        empty: 'Every model uses the site default above.',
        model: 'Model',
        effort: 'Reasoning effort',
        policy: 'Option',
        add: 'Add override',
        remove: 'Delete override',
        duplicate: 'That model and effort already have an override.',
        pickModel: 'Choose a model first'
      },
      gates: {
        title: 'Checked before ordering',
        hint: 'These do not depend on the option. If any fails, the account is skipped for that request.',
        session: 'Continuing a previous answer must return to its account; a normal conversation keeps its account unless that account is unavailable.',
        model: 'The account supports the model and its mapping includes it.',
        status: 'The account is not paused, rate-limited or cooling down.',
        features: 'The account supports the features and connection type the request needs.',
        privacy: 'If the group requires privacy mode, the account has it on.',
        capacity: 'There is room in concurrency and the queue.'
      },
      bps: {
        title: 'BPS backup route',
        hint: 'When a direct OAuth account’s normal route keeps failing, traffic can switch to the BPS route and switch back once it recovers. Default reasoning effort only.',
        master: 'Allow automatic switching to BPS',
        masterHint: 'When off, every account uses its normal route. Per-account settings below are kept.',
        rule: 'Three unhealthy state probes in a row switch to BPS; two healthy ones in a row switch back.',
        columns: {
          target: 'Account and model',
          mode: 'Setting',
          state: 'Current route',
          probe: 'Recent state probes'
        },
        modes: {
          auto: 'Switch automatically',
          force_off: 'Do not use BPS',
          force_on: 'Forced on (ignores the main switch)'
        },
        state: {
          bps: 'Using BPS',
          native: 'Normal route',
          locked: 'BPS disabled',
          inactive: 'Not enabled'
        },
        streak: '{degraded} unhealthy in a row, {healthy} healthy in a row',
        disabled: {
          upstream_403: 'The BPS route returned 403 (access denied) and was disabled automatically. Restore it once the account is fine.',
          other: 'BPS disabled ({reason}).'
        },
        warnings: {
          masterOff: 'Automatic mode follows the main switch; forced on uses BPS directly.',
          noProbe: 'Automatic state probes are off; switching can only follow manual probes.'
        },
        reset: 'Restore',
        resetTitle: 'Restore this BPS route?',
        resetBody: 'Clears the BPS state and counters for {target}; state probes decide again from scratch. Applies immediately, no save needed.',
        resetDone: 'BPS state restored',
        resetFailed: 'Could not restore',
        empty: 'Nothing can use BPS yet. BPS works only for direct OpenAI OAuth accounts on the default reasoning effort; add one on the tests page first.',
        goTests: 'Add test targets',
        ineligible: '{count} other test targets cannot use BPS (not direct OAuth, or a specific reasoning effort).'
      },
      decisions: {
        title: 'Recent scheduling decisions',
        hint: 'How each request picked an account. Only the latest {limit} are kept and they reset on restart.',
        empty: 'No decisions yet. Once requests arrive, this shows which account was picked and why.',
        noMatch: 'No decisions match these filters.',
        filterModel: 'All models',
        onlyProblems: 'Only requests with no account',
        policyUsed: 'Option: {policy}',
        chosen: 'Picked {account}',
        noneChosen: 'No account picked',
        counts: '{eligible} could serve, {excluded} excluded',
        showCandidates: 'Show candidates',
        hideCandidates: 'Hide',
        affinityOnly: 'The conversation’s bound account was reused directly; other accounts were not scored.',
        truncated: 'Too many accounts; showing 64 of them.',
        scoreHint: 'Scores only compare accounts that could serve; higher goes first.',
        migration: 'Moved off a {from}x account to {to}x',
        errorLabel: 'Technical details',
        columns: {
          account: 'Account',
          verdict: 'Result',
          why: 'Why',
          rate: 'Multiplier',
          errors: 'Error rate',
          ttft: 'First token',
          load: 'Load',
          score: 'Score'
        },
        verdict: {
          selected: 'Picked',
          topK: 'Shortlisted',
          eligible: 'Could serve',
          excluded: 'Excluded'
        },
        effort: 'effort {effort}'
      },
      decision: {
        load_balance_selection: 'Scored the available accounts with the current option and picked a top one.',
        session_sticky: 'The conversation kept its previous account.',
        previous_response_sticky: 'Continuing a previous answer, so it had to return to the same account.',
        guardian_parent_sticky: 'Followed the linked parent account.',
        sticky_escape: 'The previous account was unavailable, so it moved to another one.',
        rate_ladder_same_rate: 'While switching, looked for an account with the same multiplier first.',
        rate_ladder_upgrade: 'No account at the same multiplier, so it moved to a higher one.',
        rate_ladder_lower_rate_fallback: 'Moved to a lower-multiplier account.',
        no_selection: 'No account could serve; the request was not sent.',
        selection_error: 'Picking an account failed.',
        unknown: 'Other reason ({code}).'
      },
      exclusion: {
        excluded: 'Already tried and failed during this request.',
        not_schedulable: 'Paused or in an abnormal state.',
        platform_mismatch: 'Belongs to another platform.',
        runtime_blocked: 'Rate-limited or cooling down.',
        privacy_not_set: 'The group requires privacy mode and this account lacks it.',
        transport_incompatible: 'Connection type does not fit this request.',
        model_not_supported: 'Does not support this model.',
        account_model_not_owned: 'Its model mapping does not include this model.',
        capability_mismatch: 'Lacks a feature this request needs.',
        channel_upstream_restricted: 'The channel does not allow this upstream.',
        exec_capability_cooldown: 'Recent tool-call errors; cooling down.',
        shadow_parent_unhealthy: 'Its linked parent account is unhealthy.',
        evaluation_hard_failure: 'Tests failed clearly several times; paused for this model.',
        proxy_stream_quarantined: 'Its proxy dropped connections often; isolated for now.',
        compact_support_unknown: 'Not yet known whether it supports context compaction.',
        concurrency_full: 'Concurrency is full.',
        conn_queue_full: 'Queue is full.',
        model_rate_limited: 'Upstream rate-limited this model.',
        upstream_rate_limited: 'Rate-limited upstream.',
        quota_auto_pause: 'Quota almost used up; paused automatically.',
        platform_quota: 'Platform quota limit.',
        account_nil: 'Account data missing.',
        unknown: 'Other restriction ({code}).'
      },
      candidateReason: {
        score_top_k_candidate: 'Scored high enough to be shortlisted.',
        ranked_below_top_k: 'Could serve, but scored lower.',
        same_rate_candidate: 'Same multiplier as the previous account.',
        higher_rate_candidate: 'Higher multiplier than the previous account.',
        below_migration_rate: 'Lower multiplier than the previous account; skipped while switching.'
      }
    }
  }
}
