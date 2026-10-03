export default {
  modelIntegrity: {
    common: {
      save: '保存更改',
      saving: '保存中…',
      saved: '已保存',
      refresh: '刷新',
      reload: '重新载入',
      unsaved: '有未保存的更改',
      saveFailed: '保存失败',
      loadFailed: '配置加载失败，请检查网络后重试。',
      conflict: '该配置已在其他页面或由其他管理员修改。请重新载入后再保存，当前未保存的修改需要重新填写。',
      defaultEffort: '默认强度',
      allEfforts: '所有推理强度',
      cancel: '取消',
      close: '关闭',
      remove: '移除',
      confirm: '确定',
      retry: '重试',
      leaveConfirm: '有未保存的更改，确定离开此页面？'
    },
    status: {
      pass: '正常',
      consistent: '正常',
      warning: '异常',
      different: '与参考不同',
      suspected_normal: '疑似正常',
      suspected_luna: '疑似 Luna',
      attributed: '已归因',
      insufficient: '证据不足',
      uncertain: '证据不足',
      error: '失败',
      running: '进行中',
      healthy: '正常',
      degraded: '异常',
      inconclusive: '证据不足'
    },
    reason: {
      stateProbe: {
        healthy: '两次请求使用同一线路。',
        degraded: '两次请求之间线路发生切换。本次结果仅作提醒，不改变 BPS 状态；账号的 BPS 自动切换由「调度策略」中该账号的独立探测决定。',
        inconclusive: '本次无法判断线路是否切换。'
      },
      running: '测试进行中，请稍后刷新查看结果。',
      noDetail: '本次结果无附加说明。',
      unknown: '本次未得到有效结论，请查看详情。',
      correct_answer: '糖果题回答正确。',
      all_public_candy_variants_passed: '{count} 次回答均正确。',
      single_public_item_failed: '本次回答错误。题目公开，单次错误不能说明模型降级，建议复测。',
      one_or_more_public_candy_variants_failed: '{count} 次中至少 1 次未答出 {answer}。仅作提醒，建议稍后复测。',
      insufficient_valid_samples: '有效回答 {valid}/{required}，不足以得出结论。上游可能不稳定，请稍后重试。',
      insufficient_cells: '有效采样不足，本次不作判定。',
      no_versioned_baseline: '该模型暂无参考样本，无法比对。',
      behavior_distribution_consistent_with_versioned_reference: '回答行为与该模型的参考样本一致。',
      behavior_distribution_differs_from_reference: '回答行为与参考样本差异明显，建议复测确认。',
      fingerprint_is_identity_evidence_not_capability: '仅表示回答行为存在差异，不代表能力下降。',
      modeltrace_behavioral_attribution: '归因结果基于回答行为推断，不能作为实际模型的证明。',
      modeltrace_insufficient_outputs: '有效输出不足，无法完成归因，本次不作判定。',
      non_luna_behavioral_attribution: '行为归因结果为非 Luna 模型，判定为疑似正常。该结果为行为推断，不代表实际路由已核实。',
      suspected_luna_attribution: '行为归因结果最接近 Luna。该结果为行为推断，不能证明实际路由，也不代表已确认降智，建议复测。',
      unresolved_behavioral_attribution: '未能确定最接近的参考模型，本次不作判定。',
      timeout: '上游超时，测试未完成，请稍后重试。',
      context_deadline_exceeded: '测试超出时间限制，未完成。',
      rate_limit: '上游限流，测试未完成，请稍后重试。',
      http_429: '上游返回 429（请求过于频繁），测试未完成。',
      http_401: '上游认证失败，请检查账号凭据。',
      http_403: '上游拒绝访问，请检查账号权限或额度。',
      http_5xx: '上游服务错误，测试未完成。',
      invalid_api_key: '账号凭据无效，请检查账号设置。',
      model_not_found: '上游不支持该模型，请检查模型映射。',
      response_incomplete: '上游响应中断，本次不作判定。',
      response_failed: '上游响应失败，本次不作判定。',
      previous_response_not_found: '上游未找到上一条响应，本次不作判定。',
      upstream_error: '上游返回未知错误，本次不作判定。'
    },
    tests: {
      title: '降智测试',
      headerDescription: '检测账号是否提供所请求的模型，结果仅作提醒。',
      description: '使用固定题目与采样，定期检测账号是否提供所请求的模型，并检测请求线路是否稳定。测试结果仅作提醒，不参与账号排序，也不改变 BPS 状态；BPS 自动切换由「调度策略」中各账号的独立探测决定。',
      budget: '自动测试预计每天发出约 {requests} 次上游请求（{plans} 个自动计划）。',
      budgetNone: '未开启自动测试，仅手动测试会产生请求。',
      budgetHint: '测试请求与正常请求同样计费。',
      targets: '测试对象',
      targetsHint: '每个测试对象为「账号 + 模型 + 推理强度」组合，独立测试。',
      search: '搜索账号或模型',
      addTargets: '添加测试对象',
      noTargets: '暂无测试对象',
      noTargetsHint: '选择要检测的账号和模型后，可手动或定期测试。',
      noMatch: '没有匹配的测试对象',
      autoCount: '{count} 项自动',
      manualOnly: '仅手动',
      removeTarget: '移除测试对象',
      removeConfirmTitle: '移除该测试对象？',
      removeConfirm: '{target} 的自动测试将停止，历史记录保留。保存后生效。',
      selectTarget: '请在左侧选择测试对象。',
      types: {
        candy: {
          name: '糖果题',
          what: '对一道有标准答案的公开题目提问 {count} 次，检查是否均回答正确。回答错误仅提示需要复测。'
        },
        fingerprint: {
          name: '行为指纹',
          what: '多次采样回答行为，与参考样本比对并确定最接近的模型。归因为非 Luna 模型时判定为疑似正常。'
        },
        modeltrace: {
          name: 'ModelTrace 归因',
          what: '发送 3 次请求，根据回答行为推断最接近的模型。归因为非 Luna 模型时判定为疑似正常，仅供参考。'
        },
        state_probe: {
          name: '状态探针',
          what: '连续发送两次关联请求，检测线路是否在中途切换。结果仅作提醒，不改变 BPS 状态；BPS 自动切换由「调度策略」中的账号探测决定。'
        }
      },
      alertOnly: '仅作提醒',
      runNow: '立即测试',
      running: '测试中…',
      progress: {
        starting: '正在启动测试',
        samples: '正在采样 {done}/{total}'
      },
      runDone: '测试完成',
      runFailed: '测试启动失败',
      runAllTitle: '运行全部测试项',
      auto: '自动运行',
      every: '间隔',
      interval: {
        m5: '5 分钟',
        m10: '10 分钟',
        m30: '30 分钟',
        m15: '15 分钟',
        h1: '1 小时',
        h6: '6 小时',
        h12: '12 小时',
        h24: '24 小时',
        d3: '3 天',
        d7: '7 天',
        custom: '自定义（{value}）',
        customOption: '自定义',
        customMinutes: '自定义间隔（分钟，存储上限 {max}）'
      },
      candySamples: '每次请求数',
      candySamplesHint: '每次糖果题的请求次数，范围 1–10，默认 1。',
      jitter: '随机延迟（分钟）',
      jitterHint: '在测试间隔后随机延迟 0–{max} 分钟，以错开多个账号的测试时间。',
      jitterUnavailable: '当前间隔不支持随机延迟。',
      sampleMode: '采样量',
      sampleModes: {
        quick: '快速',
        standard: '标准',
        strict: '严格',
        custom: '当前：{mode}'
      },
      perRun: '每次 {count} 次请求',
      perDay: '每天约 {count} 次请求',
      perDayOff: '未开启自动运行',
      nextRun: '下次 {time}',
      lastRun: '上次 {time}',
      neverRun: '尚未测试',
      onlyDirectOAuth: '只支持直连 OpenAI OAuth 账号的默认推理强度。',
      goScheduling: '在调度策略页配置 BPS',
      manualSampleTitle: '选择本次采样量',
      manualSampleHint: '仅影响本次手动测试，不修改自动计划。采样越多结论越可靠，请求量也越大。',
      manualSampleOption: '{mode}：{count} 次请求',
      history: '测试记录',
      historyHint: '仅保留最近的记录，不保存题目原文和账号凭据。',
      historyScopeAll: '全部对象',
      historyScopeTarget: '仅当前对象',
      filterType: '全部测试',
      filterResult: '全部结果',
      filterAttention: '需关注',
      filterOk: '正常',
      filterLikely: '疑似正常',
      filterNeutral: '无法判定',
      noHistory: '暂无测试记录。',
      noHistoryMatch: '没有符合筛选条件的记录。',
      columns: {
        time: '时间',
        target: '测试对象',
        test: '测试',
        result: '结果',
        samples: '有效样本',
        cost: '估算费用',
        trigger: '触发方式'
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
        upstreamModel: '上游返回模型',
        baseline: '参考样本版本',
        technical: '技术细节',
        candidates: '候选模型',
        fingerprintMetric: '差异度 {jsd}（越小越接近），显著性 p = {p}',
        modeltraceMetric: '归因模型 {model}，概率 {probability}。',
        fingerprintNearest: '最接近的参考模型为 {model}。',
        nearestModel: '最接近的参考模型',
        attributionNote: '归因结果仅作提醒，不参与账号调度。',
        stateProbeMetric: '两次请求状态码 {mint} / {cont}，{ticket}',
        newTicket: '线路已切换',
        sameTicket: '线路未变'
      },
      add: {
        title: '添加测试对象',
        accounts: '账号',
        accountsHint: '支持多选，仅列出 OpenAI 账号。',
        searchAccounts: '搜索账号名称或 ID',
        noAccounts: '未找到 OpenAI 账号。',
        model: '请求模型',
        modelPlaceholder: '选择模型',
        effort: '推理强度',
        selected: '已选 {count} 个账号',
        submit: '添加 {count} 个',
        added: '已添加 {count} 个测试对象，保存后生效。',
        skipped: '{count} 个组合已存在，已跳过。'
      },
      baselineNote: '行为指纹参考样本版本 {version}。'
    },
    scheduling: {
      title: '调度策略',
      headerDescription: '配置账号排序策略与 BPS 备用线路。',
      description: '配置账号排序策略、模型规则与 BPS 备用线路，并查看最近的调度决策。',
      policy: {
        title: '排序策略',
        hint: '仅决定多个账号均满足调度条件时的优先顺序；账号能否参与调度由调度条件决定。',
        defaultLabel: '默认策略',
        factors: {
          price: '价格',
          errors: '错误率',
          speed: '首包延迟'
        },
        levelAria: '{factor}：{level} / 4',
        options: {
          legacy: {
            name: '系统默认',
            effect: '沿用系统设置中的调度权重，与启用本功能前一致。'
          },
          cost_first: {
            name: '优先低价',
            effect: '优先调度计费倍率较低的账号；错误率与首包延迟权重相应降低。'
          },
          stability_first: {
            name: '优先稳定',
            effect: '优先调度错误率低、首包延迟短的账号；价格权重沿用系统设置，不参考降智测试结果。'
          },
          avoid_degradation: {
            name: '避免降智',
            effect: '大幅降低价格权重，优先调度错误率低、响应稳定的账号。'
          },
          custom_balance: {
            name: '自定义平衡',
            effect: '按自定义的价格、稳定性、错误率、首包延迟与负载权重排序。'
          },
        },
        custom: {
          title: '自定义权重',
          hint: '按百分比填写，保存时自动归一化；权重越高，对排序影响越大。',
          cost: '价格',
          stability: '稳定性',
          error_rate: '错误率',
          ttft: '首包延迟',
          load: '并发负载',
          zeroTotal: '权重合计须大于 0，请至少为一项设置权重。'
        },
        avoidNote: '降智测试结果目前仅作提醒，不参与账号排序，因此「避免降智」与「优先稳定」的区别主要在于价格权重更低。',
        sharedNote: '账号优先级权重在各策略下保持不变；负载权重仅在「自定义平衡」下按设置调整，其余策略沿用系统设置。'
      },
      rules: {
        title: '模型规则',
        hint: '为指定模型设置独立策略，规则优先于默认策略；指定推理强度的规则优先于未指定的规则。',
        empty: '未配置规则，所有模型使用默认策略。',
        model: '模型',
        effort: '推理强度',
        policy: '策略',
        add: '添加规则',
        remove: '删除规则',
        duplicate: '该模型与推理强度的规则已存在。',
        pickModel: '请选择模型',
        weights: '规则权重',
        weightsSeparator: '，',
        editWeights: '编辑权重',
        doneWeights: '收起',
        weightsFor: '{model}（{effort}）自定义权重',
        weightsHint: '仅作用于本规则，与默认策略的权重相互独立。按百分比填写，保存时自动归一化。'
      },
      gates: {
        title: '调度条件',
        hint: '与排序策略无关。不满足任一条件的账号不参与本次调度。',
        session: '续写上一条响应时须使用原账号；普通会话优先沿用上次的账号，原账号不可用时再切换。',
        model: '账号支持所请求的模型，且模型映射包含该模型。',
        status: '账号未暂停调度，且不处于限流或冷却状态。',
        features: '账号支持本次请求所需的功能与连接方式。',
        privacy: '分组要求开启隐私设置时，账号须已开启。',
        capacity: '并发与排队仍有余量。'
      },
      bps: {
        title: 'BPS 备用线路',
        hint: '按 OAuth 账号配置。原线路连续探测异常时，整个账号切换至 BPS 线路，恢复后切回。探测模型仅用于线路检测，不限制切换范围。',
        master: '启用 BPS 自动切换',
        masterHint: '关闭后，「自动切换」模式的账号均使用原线路；「始终使用 BPS」不受影响，各账号配置保留。',
        add: '添加账号',
        edit: '设置',
        summary: {
          total: '共 {count} 个账号',
          bps: '{count} 个使用 BPS',
          native: '{count} 个使用原线路',
          locked: '{count} 个因 403 已停用',
          inactive: '{count} 个未启用'
        },
        columns: {
          account: '账号',
          mode: '模式',
          state: '当前线路',
          counters: '切换计数',
          probe: '探测模型与间隔',
          actions: '操作'
        },
        modes: {
          auto: '自动切换',
          force_on: '始终使用 BPS',
          force_off: '不使用 BPS'
        },
        modeHints: {
          auto: '按切换阈值在原线路与 BPS 之间切换，受全局开关控制。',
          force_on: '始终使用 BPS，不受探测结果和全局开关影响。',
          force_off: '始终使用原线路，探测结果不触发切换。'
        },
        state: {
          bps: '使用 BPS',
          native: '原线路',
          locked: 'BPS 已停用',
          inactive: '未启用'
        },
        counters: '连续异常 {degraded}/{failure} · 连续正常 {healthy}/{recovery}',
        lastProbe: '上次探测：{time}',
        nextProbe: '下次探测：{time}',
        ruleFor: '连续 {failure} 次探测异常后切换至 BPS；切换后连续 {recovery} 次正常则切回原线路。',
        updatedAt: '更新于 {time}',
        every: {
          minutes: '每 {n} 分钟',
          hours: '每 {n} 小时',
          days: '每 {n} 天'
        },
        interval: {
          customOption: '自定义',
          customMinutes: '自定义间隔（分钟，存储上限 {max}）'
        },
        disabled: {
          upstream_403: 'BPS 线路返回 403，已停用并锁定。锁定不会自动解除，确认账号状态正常后点击「恢复」。',
          other: 'BPS 已停用（{reason}）。'
        },
        warnings: {
          masterOff: '全局开关已关闭，自动切换暂不生效；如需始终使用 BPS，请将模式设为「始终使用 BPS」。',
          masterOffShort: '全局开关已关闭，暂不切换'
        },
        dialog: {
          addTitle: '添加 BPS 账号',
          editTitle: 'BPS 设置 · {account}',
          account: 'OAuth 账号',
          pickAccount: '选择账号',
          noCandidates: '所有 OpenAI OAuth 账号均已添加。',
          mode: '模式',
          probeModel: '探测模型',
          pickModel: '选择模型',
          defaultModel: '默认探测模型（gpt-5.4）',
          probeModelHint: '仅用于发送探测请求。切换作用于整个账号，与降智测试的测试对象相互独立。',
          thresholds: '切换阈值',
          failure: '连续异常次数（切换至 BPS）',
          recovery: '连续正常次数（切回原线路）',
          interval: '探测间隔',
          thresholdsManual: '切换阈值仅在「自动切换」模式下生效，当前配置将保留。',
          remove: '移除该账号',
          apply: '应用',
          applyHint: '应用后需点击页面上的「保存更改」才会生效。'
        },
        history: {
          title: '最近记录',
          empty: '暂无该账号的探测或操作记录。',
          probe: '探测：{status}',
          reset: '管理员已恢复 BPS 状态'
        },
        reset: '恢复',
        resetTitle: '恢复该账号的 BPS 状态？',
        resetBody: '将清空 {account} 的 BPS 状态、403 锁定与计数，之后由探测重新判定。此操作立即生效，无需保存。',
        resetDone: 'BPS 状态已恢复',
        resetFailed: '恢复失败',
        removeTitle: '移除该账号的 BPS 配置？',
        removeBody: '{account} 将从列表中移除，保存后使用原线路。降智测试中该账号的测试对象不受影响。',
        empty: '尚未添加账号。BPS 仅支持直连 OpenAI OAuth 账号，点击「添加账号」开始配置。',
        emptyNoOAuth: '暂无可用的 OpenAI OAuth 账号。BPS 仅支持直连 OpenAI OAuth 账号。',
        legacyRoutes: '{count} 个测试对象仍保留升级前的模型级 BPS 配置，本页面不会修改。'
      },
      decisions: {
        title: '最近调度记录',
        hint: '记录每次请求的账号选择过程，仅保留最近 {limit} 条，服务重启后清空。',
        empty: '暂无调度记录。产生请求后将显示选中的账号及原因。',
        noMatch: '没有符合筛选条件的记录。',
        filterModel: '全部模型',
        onlyProblems: '仅显示未选中账号的记录',
        policyUsed: '策略：{policy}',
        chosen: '选中 {account}',
        noneChosen: '未选中账号',
        counts: '{eligible} 个可用，{excluded} 个已排除',
        showCandidates: '查看候选账号',
        hideCandidates: '收起',
        affinityOnly: '本次沿用会话绑定的账号，未对其他账号评分。',
        truncated: '账号数量较多，仅显示其中 64 个。',
        scoreHint: '分数仅在可用账号之间比较，分数越高越优先。',
        migration: '从 {from}x 倍率账号切换至 {to}x',
        errorLabel: '技术细节',
        columns: {
          account: '账号',
          verdict: '结果',
          why: '原因',
          rate: '倍率',
          errors: '错误率',
          ttft: '首包延迟',
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
        load_balance_selection: '按当前策略对可用账号评分，选中排名靠前的账号。',
        session_sticky: '同一会话沿用上次的账号。',
        previous_response_sticky: '续写上一条响应，须使用原账号。',
        guardian_parent_sticky: '跟随关联的主账号。',
        sticky_escape: '原账号暂不可用，已切换至其他账号。',
        rate_ladder_same_rate: '切换账号时优先选择相同倍率的账号。',
        rate_ladder_upgrade: '无相同倍率的可用账号，已切换至更高倍率的账号。',
        rate_ladder_lower_rate_fallback: '已切换至更低倍率的账号。',
        no_selection: '无可用账号，请求未发出。',
        selection_error: '账号选择出错。',
        unknown: '其他原因（{code}）。'
      },
      exclusion: {
        excluded: '本次请求中已尝试且失败。',
        not_schedulable: '账号已暂停调度或状态异常。',
        platform_mismatch: '账号不属于该平台。',
        runtime_blocked: '账号处于限流或冷却状态。',
        privacy_not_set: '分组要求开启隐私设置，该账号未开启。',
        transport_incompatible: '连接方式与本次请求不兼容。',
        model_not_supported: '账号不支持该模型。',
        account_model_not_owned: '账号的模型映射不包含该模型。',
        capability_mismatch: '账号不支持本次请求所需的功能。',
        channel_upstream_restricted: '渠道设置不允许使用该上游。',
        exec_capability_cooldown: '近期工具调用出错，处于冷却中。',
        shadow_parent_unhealthy: '关联的主账号状态异常。',
        evaluation_hard_failure: '该模型的测试连续出现明确失败，暂停调度该模型。',
        proxy_stream_quarantined: '代理连接近期频繁中断，已临时隔离。',
        compact_support_unknown: '尚未确认是否支持上下文压缩。',
        concurrency_full: '并发已满。',
        conn_queue_full: '排队已满。',
        model_rate_limited: '该模型被上游限流。',
        upstream_rate_limited: '账号被上游限流。',
        quota_auto_pause: '额度即将用尽，已自动暂停。',
        platform_quota: '受平台额度限制。',
        account_nil: '账号信息缺失。',
        unknown: '其他限制（{code}）。'
      },
      candidateReason: {
        score_top_k_candidate: '分数排名靠前，进入候选列表。',
        ranked_below_top_k: '可用，但分数排名未进入候选列表。',
        same_rate_candidate: '倍率与原账号相同。',
        higher_rate_candidate: '倍率高于原账号。',
        below_migration_rate: '倍率低于原账号，本次切换不考虑。'
      }
    }
  }
}
