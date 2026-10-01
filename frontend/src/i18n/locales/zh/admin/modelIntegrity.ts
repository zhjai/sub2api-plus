export default {
  modelIntegrity: {
    nav: {
      aria: '降智调度页面切换',
      tests: '降智测试',
      scheduling: '降智调度'
    },
    common: {
      save: '保存更改',
      saving: '保存中…',
      saved: '已保存',
      refresh: '刷新',
      reload: '重新载入',
      unsaved: '有未保存的更改',
      saveFailed: '保存失败',
      loadFailed: '设置没能加载，请检查网络后重试。',
      conflict: '这份设置刚在别处被改过（可能是另一个页面或其他管理员）。重新载入后再保存；这里还没保存的修改需要重做。',
      defaultEffort: '默认强度',
      allEfforts: '所有推理强度',
      cancel: '取消',
      close: '关闭',
      remove: '移除',
      confirm: '确定',
      retry: '重试',
      leaveConfirm: '有未保存的更改，确定离开这个页面吗？'
    },
    status: {
      pass: '正常',
      consistent: '和参考一致',
      warning: '有答错',
      different: '和参考不同',
      insufficient: '样本不够',
      uncertain: '无法判断',
      error: '没测完',
      running: '测试中',
      healthy: '线路正常',
      degraded: '线路异常',
      inconclusive: '无法判断'
    },
    reason: {
      stateProbe: {
        healthy: '两次请求走的是同一条线路。',
        degraded: '两次请求之间线路被换过。连续 3 次这样，已开启自动切换的账号会改走 BPS。',
        inconclusive: '这次没能判断线路有没有被换。'
      },
      running: '还在测试，稍后刷新看结果。',
      noDetail: '这次没有附带说明。',
      unknown: '这次没能得出结论（代码 {code}）。',
      correct_answer: '答对了糖果题。',
      all_public_candy_variants_passed: '5 次都答对了。',
      single_public_item_failed: '这次没答对。题目是公开的，单次答错不能说明模型被降级，建议复测。',
      one_or_more_public_candy_variants_failed: '5 次里至少有 1 次没答出 {answer}。这只是提醒，建议过一会儿复测。',
      insufficient_valid_samples: '有效回答只有 {valid}/{required} 个，不够下结论。可能是上游不稳定，稍后再试。',
      insufficient_cells: '有效采样太少，这次不下结论。',
      no_versioned_baseline: '这个模型还没有参考样本，暂时没法比对。',
      behavior_distribution_consistent_with_versioned_reference: '回答习惯和这个模型的参考样本一致。',
      behavior_distribution_differs_from_reference: '回答习惯和参考样本差别明显，可能换了模型，建议复测确认。',
      fingerprint_is_identity_evidence_not_capability: '只能说明回答习惯有差别，不能说明能力变差。',
      modeltrace_behavioral_attribution: '只是根据回答习惯的推测，不能证明真实模型。',
      modeltrace_insufficient_outputs: '有效回答太少，没法推测。',
      timeout: '上游超时，没测完。稍后重试。',
      context_deadline_exceeded: '测试超过了时间限制，没测完。',
      rate_limit: '上游限流，没测完。稍后重试。',
      http_429: '上游返回 429（请求太频繁），没测完。',
      http_401: '上游认证失败，请检查账号凭据。',
      http_403: '上游拒绝访问，请检查账号权限或额度。',
      http_5xx: '上游服务出错，没测完。',
      invalid_api_key: '账号凭据无效，请检查账号设置。',
      model_not_found: '上游不认识这个模型，请检查模型映射。',
      response_incomplete: '上游回答中途断了，这次不下结论。',
      response_failed: '上游回答失败，这次不下结论。',
      previous_response_not_found: '上游找不到上一条回答，这次不下结论。',
      upstream_error: '上游返回了未知错误，这次不下结论。'
    },
    tests: {
      title: '降智测试',
      description: '定期用固定题目和采样检查账号给你的是不是请求的那个模型。测试结果只作提醒，不会自动改变选账号。',
      budget: '自动测试每天大约会发出 {requests} 次上游请求（{plans} 个自动计划）。',
      budgetNone: '还没有开启自动测试，目前只有手动测试会产生请求。',
      budgetHint: '这些请求和正常请求一样计费。',
      targets: '测试对象',
      targetsHint: '每个对象是「账号 + 模型 + 推理强度」的一个组合，各自独立测试。',
      search: '搜索账号或模型',
      addTargets: '添加测试对象',
      noTargets: '还没有测试对象',
      noTargetsHint: '选择要检查的账号和模型，就可以手动或定期测试。',
      noMatch: '没有匹配的测试对象',
      autoCount: '{count} 项自动',
      manualOnly: '仅手动',
      removeTarget: '移除测试对象',
      removeConfirmTitle: '移除这个测试对象？',
      removeConfirm: '{target} 的自动测试会停止，历史记录会保留。保存后生效。',
      selectTarget: '从左侧选一个测试对象查看和设置。',
      types: {
        candy: {
          name: '糖果题',
          what: '同一道有标准答案的题问 5 次，看是否都答对。题目公开，答错只代表需要复测。'
        },
        fingerprint: {
          name: '行为指纹',
          what: '多次采样回答习惯，和这个模型的参考样本对比，看像不像同一个模型。'
        },
        modeltrace: {
          name: 'ModelTrace 归因',
          what: '用 3 次请求推测回答最像哪个模型，只作参考。'
        },
        state_probe: {
          name: '状态探针',
          what: '连续发两次请求，检查线路有没有被中途换掉。BPS 自动切换依据这个结果。'
        }
      },
      alertOnly: '只作提醒',
      runNow: '立即测试',
      running: '测试中…',
      runDone: '测试完成',
      runFailed: '测试没能开始',
      runAllTitle: '一次跑完所有测试项',
      auto: '自动运行',
      every: '间隔',
      interval: {
        m15: '15 分钟',
        h1: '1 小时',
        h6: '6 小时',
        h24: '1 天',
        d3: '3 天',
        d7: '7 天',
        custom: '自定义（{value}）'
      },
      jitter: '随机延后（分钟）',
      jitterHint: '最多 {max} 分钟，让多个账号错开时间。',
      jitterUnavailable: '间隔已经是最短，不能再随机延后。',
      sampleMode: '采样量',
      sampleModes: {
        quick: '快速',
        standard: '标准',
        strict: '严格',
        custom: '当前：{mode}'
      },
      perRun: '每次 {count} 次请求',
      perDay: '每天约 {count} 次请求',
      perDayOff: '不自动运行',
      nextRun: '下次 {time}',
      lastRun: '上次 {time}',
      neverRun: '还没测过',
      onlyDirectOAuth: '只支持直连 OpenAI OAuth 账号的默认推理强度。',
      goScheduling: '在降智调度页查看 BPS',
      manualSampleTitle: '这次采样多少？',
      manualSampleHint: '只影响这一次手动测试，不改自动计划。采样越多结论越可靠，花的请求也越多。',
      manualSampleOption: '{mode}：{count} 次请求',
      history: '测试记录',
      historyHint: '只保留最近的记录，不保存题目原文和账号凭据。',
      historyScopeAll: '全部对象',
      historyScopeTarget: '只看当前对象',
      filterType: '全部测试',
      filterResult: '全部结果',
      filterAttention: '需要留意',
      filterOk: '正常',
      filterNeutral: '无法判断',
      noHistory: '还没有测试记录。',
      noHistoryMatch: '没有符合筛选的记录。',
      columns: {
        time: '时间',
        target: '测试对象',
        test: '测试',
        result: '结果',
        samples: '有效样本',
        cost: '估算费用',
        trigger: '方式'
      },
      trigger: {
        manual: '手动',
        scheduled: '自动'
      },
      costUnknown: '暂无',
      detail: {
        title: '测试详情',
        samples: '有效样本',
        requests: '请求数',
        tokens: '输入 / 输出 Token',
        duration: '耗时',
        cost: '估算费用',
        upstreamModel: '上游返回的模型',
        baseline: '参考样本版本',
        technical: '技术细节',
        candidates: '最像的模型',
        fingerprintMetric: '差异度 {jsd}（越小越像），显著性 p = {p}',
        modeltraceMetric: '最像 {model}，概率 {probability}',
        stateProbeMetric: '两次请求状态码 {mint} / {cont}，{ticket}',
        newTicket: '线路被换过',
        sameTicket: '线路没变'
      },
      add: {
        title: '添加测试对象',
        accounts: '账号',
        accountsHint: '可以多选，只列出 OpenAI 账号。',
        searchAccounts: '搜索账号名称或 ID',
        noAccounts: '没有找到 OpenAI 账号。',
        model: '请求模型',
        modelPlaceholder: '选择模型',
        effort: '推理强度',
        selected: '已选 {count} 个账号',
        submit: '添加 {count} 个',
        added: '已添加 {count} 个测试对象，保存后生效。',
        skipped: '{count} 个组合已经存在，已跳过。'
      },
      baselineNote: '行为指纹参考样本版本 {version}。'
    },
    scheduling: {
      title: '降智调度',
      description: '决定请求来了先用哪个账号，以及原线路出问题时怎么切换。',
      policy: {
        title: '选账号的方式',
        hint: '只决定「好几个账号都能用」时谁排在前面。账号能不能用，由右边的检查先决定。',
        defaultLabel: '全站默认',
        factors: {
          price: '看价格',
          errors: '看出错率',
          speed: '看首字速度'
        },
        levelAria: '{factor}：{level} / 4',
        options: {
          legacy: {
            name: '保持原样',
            effect: '沿用系统设置里的权重，和升级前的选法一样。'
          },
          cost_first: {
            name: '优先低价',
            effect: '倍率低的账号排前面。出错率和速度仍会参考，但分量变轻。'
          },
          stability_first: {
            name: '优先稳定',
            effect: '最近出错少、首字快的账号排前面，价格照系统设置参考。'
          },
          avoid_degradation: {
            name: '避免降智',
            effect: '几乎不因为便宜去选账号，优先最近出错少、响应稳定的账号。'
          }
        },
        avoidNote: '测试结果目前只作提醒，不会直接把账号排除。所以现阶段「避免降智」和「优先稳定」的差别只在于更少考虑价格。',
        sharedNote: '负载、并发和账号优先级在四种方式下作用相同。'
      },
      rules: {
        title: '按模型单独设置',
        hint: '某个模型要用不同的选法时在这里加。填了推理强度的规则比不填的优先。',
        empty: '所有模型都按上面的全站默认。',
        model: '模型',
        effort: '推理强度',
        policy: '选法',
        add: '添加规则',
        remove: '删除规则',
        duplicate: '这个模型和推理强度已经有规则了。',
        pickModel: '先选模型'
      },
      gates: {
        title: '先检查，再排序',
        hint: '这些条件和选法无关，任何一条不满足，账号这次就不会被选。',
        session: '续写上一条回答时必须回到原账号；普通会话尽量沿用上次的账号，原账号不可用时才换。',
        model: '账号支持这个模型，模型映射里包含它。',
        status: '账号没有被暂停，也不在限流或冷却中。',
        features: '账号支持这次请求用到的功能和连接方式。',
        privacy: '分组要求开启隐私设置时，账号必须已开启。',
        capacity: '并发和排队还有空位。'
      },
      bps: {
        title: 'BPS 备用线路',
        hint: '直连 OAuth 账号的原线路连续出问题时，可以自动改走 BPS 线路，线路恢复后自动切回。只对默认推理强度生效。',
        master: '允许自动切到 BPS',
        masterHint: '关掉后所有账号都走原线路，下面每个账号的设置会保留。',
        rule: '状态探针连续 3 次发现线路异常就切到 BPS；之后连续 2 次正常就切回原线路。',
        columns: {
          target: '账号和模型',
          mode: '设置',
          state: '现在走哪条线路',
          probe: '最近的状态探针'
        },
        modes: {
          auto: '自动切换',
          force_off: '不使用 BPS',
          force_on: '手动开启（忽略总开关）'
        },
        state: {
          bps: '正在走 BPS',
          native: '原线路',
          locked: 'BPS 已停用',
          inactive: '未启用'
        },
        streak: '连续异常 {degraded} 次，连续正常 {healthy} 次',
        disabled: {
          upstream_403: 'BPS 线路返回 403（拒绝访问），已自动停用。确认账号没问题后可以手动恢复。',
          other: 'BPS 已停用（{reason}）。'
        },
        warnings: {
          masterOff: '自动模式受总开关控制；手动开启会直接走 BPS。',
          noProbe: '没开自动状态探针，只有手动探测时才可能切换。'
        },
        reset: '恢复',
        resetTitle: '恢复这条 BPS 线路？',
        resetBody: '会清空 {target} 的 BPS 状态和计数，之后重新由状态探针判断。这一步立即生效，不需要保存。',
        resetDone: 'BPS 状态已恢复',
        resetFailed: '恢复失败',
        empty: '还没有可以使用 BPS 的对象。BPS 只支持直连 OpenAI OAuth 账号的默认推理强度，先在降智测试里添加这样的账号和模型。',
        goTests: '去添加测试对象',
        ineligible: '另有 {count} 个测试对象不支持 BPS（不是直连 OAuth 账号，或指定了推理强度）。'
      },
      decisions: {
        title: '最近的调度记录',
        hint: '每次请求选账号的过程。只保留最近 {limit} 次，服务重启后清空。',
        empty: '还没有调度记录。有请求经过后，这里会显示选了哪个账号、为什么。',
        noMatch: '没有符合筛选的记录。',
        filterModel: '全部模型',
        onlyProblems: '只看没选到账号的',
        policyUsed: '选法：{policy}',
        chosen: '选中 {account}',
        noneChosen: '没有选中账号',
        counts: '{eligible} 个可用，{excluded} 个被排除',
        showCandidates: '看候选账号',
        hideCandidates: '收起',
        affinityOnly: '这次直接沿用了会话绑定的账号，没有给其他账号打分。',
        truncated: '账号太多，只显示其中 64 个。',
        scoreHint: '分数只在可用账号之间比较，越高越靠前。',
        migration: '从 {from}x 倍率的账号换出，换到 {to}x',
        errorLabel: '技术细节',
        columns: {
          account: '账号',
          verdict: '结果',
          why: '原因',
          rate: '倍率',
          errors: '出错率',
          ttft: '首字耗时',
          load: '负载',
          score: '分数'
        },
        verdict: {
          selected: '选中',
          topK: '候选',
          eligible: '可用',
          excluded: '排除'
        },
        effort: '推理强度 {effort}'
      },
      decision: {
        load_balance_selection: '按选法给可用账号打分，选了靠前的一个。',
        session_sticky: '同一会话沿用了上次的账号。',
        previous_response_sticky: '在续写上一条回答，必须回到原账号。',
        guardian_parent_sticky: '跟随关联的主账号。',
        sticky_escape: '原来的账号暂时不能用，换到了别的账号。',
        rate_ladder_same_rate: '换账号时优先找同样倍率的账号。',
        rate_ladder_upgrade: '同倍率没有可用账号，换到了倍率更高的账号。',
        rate_ladder_lower_rate_fallback: '换到了倍率更低的账号。',
        no_selection: '没有能用的账号，请求没能发出。',
        selection_error: '选账号时出错。',
        unknown: '其他原因（{code}）。'
      },
      exclusion: {
        excluded: '这次请求里已经试过它并失败了。',
        not_schedulable: '账号已暂停调度或状态异常。',
        platform_mismatch: '不是这个平台的账号。',
        runtime_blocked: '正在限流或冷却中。',
        privacy_not_set: '分组要求开启隐私设置，这个账号没开。',
        transport_incompatible: '连接方式不符合这次请求。',
        model_not_supported: '账号不支持这个模型。',
        account_model_not_owned: '账号的模型映射里没有这个模型。',
        capability_mismatch: '不支持这次请求用到的功能。',
        channel_upstream_restricted: '渠道设置不允许用这个上游。',
        exec_capability_cooldown: '最近工具调用出错，冷却中。',
        shadow_parent_unhealthy: '关联的主账号状态异常。',
        evaluation_hard_failure: '测试连续明确失败，暂时不接这个模型。',
        proxy_stream_quarantined: '代理连接最近频繁中断，暂时隔离。',
        compact_support_unknown: '还不确定它是否支持上下文压缩。',
        concurrency_full: '并发已满。',
        conn_queue_full: '排队已满。',
        model_rate_limited: '这个模型被上游限流。',
        upstream_rate_limited: '被上游限流。',
        quota_auto_pause: '额度快用完，已自动暂停。',
        platform_quota: '平台额度限制。',
        account_nil: '账号信息缺失。',
        unknown: '其他限制（{code}）。'
      },
      candidateReason: {
        score_top_k_candidate: '分数靠前，进了候选名单。',
        ranked_below_top_k: '能用，但分数不够靠前。',
        same_rate_candidate: '和原账号倍率相同。',
        higher_rate_candidate: '倍率比原账号高。',
        below_migration_rate: '倍率低于原账号，这次换号不考虑。'
      }
    }
  }
}
