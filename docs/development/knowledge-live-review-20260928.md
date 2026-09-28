# 2026-09-28 知识服务真实回合复核与修复交接

## 范围

- 基于 test-2 release `d112692` 的真实运行 trace，只复核 `arrival_knowledge` 和 `services_product`。
- 本次新增修改仅涉及知识证据合并与提示，不调用 PMS 写接口，不修改数据库、DTO、权限、企微协议或 Outbox。
- 不把隔离运行中的 `commitMessages.status=sent` 等同于企微收件端实际收到或卡片可打开；本批 `recipientReceipt`、`cardOpening` 均为 `not_verified`。

## 已核实结果

| 回合 / runLogId | 实际结果 | 结论 |
| --- | --- | --- |
| arrival 1 / 8749 | 分别回答免费停车、昭潭路入口及咖啡自取地点 | 内容通过，耗时 22.148 秒 |
| arrival 2 / 8750 | “那个在哪拿”正确承接咖啡，回答 1313 对面洗衣房 | 内容通过，耗时 12.671 秒 |
| arrival 3 / 8751 | 回答门店知识中的 Wi-Fi 名称及密码 | 内容通过，耗时 13.744 秒 |
| arrival 4 / 8752 | 提交入住 `mini_program` 卡片 | 资源类型通过，收件端及打开未验证 |
| services 1 / 8770 | “帮我点外卖”只有固定的无法代办句，没有检索或可行路径 | 体验未通过 |
| services 2 / 8771 | 有洗衣房自取事实，却添加“无人值守，暂时无法送房” | 未通过：未知配送能力变成否定 |
| services 3 / 8772 | 自取毛巾追问正确回答洗衣房地点 | 内容通过，耗时 16.906 秒 |
| services 4、5 / 8773、8774 | 空调故障及尝试无效的追问均固定回复“暂时没法准确回答” | 未通过：没有接住休息需求或下一步 |
| services 6、7 / 8775、8776 | 两种购买表达均提交枕头 `shop_product`，不误发入住卡片 | 资源类型及推荐内容通过，收件端及打开未验证 |
| services 8 / 8777 | 自然感谢收尾，未复活旧服务问题 | 内容通过 |

全部上述回合保持 AI 路由；人工分配、工单、补救记录、PMS 操作均未新增。

## 已确认根因

1. `intent_customer_scenarios.go` 把外部代办任务强制 `NeedsKnowledge=false`；现有 Generate 固定能力边界路径直接提交，导致可供自助的门店信息没有机会参与。本项由主任务处理，知识审查未改该文件。
2. 毛巾回合 Judge 正确保留自取事实及 `missingAspects=["是否可以送到房间"]`，错误发生在后续 Generate。原提示虽然禁止未知转肯定或否定，但没有清楚说明角色背景和历史 AI 回复不是补全业务政策的证据。
3. 空调回合 Judge 未选出事实，仅在各层保留故障解决办法的缺口；`applyKnowledgeEvidenceJudgeOutcome` 和 `applyKnowledgeEvidenceJudgeTraceToReplyPlan` 又丢失无选中层的缺口，后续 `service.go` 提前结束 Generate。无事实不应被当作无客户目标。
4. Judge 的“排除被客户拒绝的方案”原表述同时覆盖了自助方案和人工流程来源，混淆“证据是否适用”与“客户是否授权执行”。

## 本次修改

- `answerability_gate.go`：无知识答案时仍保留经 Judge 确认的具体缺口，贯穿 trace、question、disposition 和原 Task；合并时保留 PMS 事实，清除无当前证据支持的旧知识，不授予人工权限。
- 协议异常隔离提示不再引用 `runtime_safe_fallback` 伪装的固定事实；异常任务只能说明真实缺口或作必要追问，其他任务按各自已验证来源继续，不因协议错误新增人工路由。
- `knowledge_guard.go`：统一 Strict/Assist 的来源边界，明确自取事实不证明送房能力，门店背景及历史 AI 文案不得补出政策；有可用办法时直接说明，不将所有缺口机械展示给客户。
- `knowledge_evidence_judge.go`：明确排除的是已失败或被拒绝的自助办法；适用人工流程来源仍可记录，执行许可由现有程序控制。
- `knowledge_source_merge_test.go`：覆盖两类故障、稳定与旧 Task ID、PMS 和缺口合并、两种知识模式与人工许可职责分离。

## 验证与待办

- 定向回归已通过，耗时 1.183 秒。
- 首次 executor 全包回归只发现原有提示顺序字符串断言，已恢复兼容表述。
- 随后完整回归曾临时被并行 `jev_intent_goal_slots.go` 引用未补齐的 `jevIntentContext.DateValue` 阻塞；未改其他负责人的上下文文件。待该字段补齐后再次运行通过：executor 10.803 秒，instruction 0.911 秒。
- `gofmt`、`git diff --check` 已通过。
- 主任务须在最终并行编辑合并后回归，部署后复测毛巾、故障追问和外卖；未复测前不得把对应体验标为通过。

## 耗时依据

知识 Judge 使用 `gpt-5.6-luna`，本批没有超时重试；arrival 1/2/3 分别消耗 18.299/10.569/11.612 秒，services 2/3 为 15.253/14.839 秒，占这些回合总耗时约 75%-88%。向量检索约 0.7-1.1 秒，毛巾额外 Generate 为 2.897 秒。性能重点应在同一 Judge 的输入/输出及适用模型延迟，和避免对已足够的自助答复再次生成；不能用固定拒绝、删除证据或缩小有效候选来换取表面速度。

只读查询运行配置确认：模板 ID 1、revision 23，Judge slot ID 19，`provider=openai`、`model=gpt-5.6-luna`、`apiMode=chat_completions`、`maxOutputTokens=2048`、`timeoutMs=45000`、`maxRetryCount=0`。真正配置来自 `ModelProfileTemplateService.ResolveSlot`，不是旧 `t_store_ai_model_setting` 或 `t_ai_config`。

| 客户消息 ID | 输入 token | 输出 token | Judge 毫秒 |
| --- | ---: | ---: | ---: |
| 20063 | 4376 | 915 | 18298 |
| 20066 | 3952 | 483 | 10569 |
| 20068 | 3932 | 546 | 11611 |
| 20114 | 3986 | 765 | 15252 |
| 20116 | 4108 | 705 | 14837 |
| 20118 | 3903 | 340 | 7505 |
| 20120 | 3995 | 362 | 11310 |

用量来源为 `t_ai_usage_event` 的 `knowledge_evidence_judge`、`metricSource=upstream_actual`。所有 `reasoning_tokens=0` 不能证明关闭思考：`internal/ai/llm.go` 的 `ChatCompletionResult` 仅保留输入/输出 token，没有解析推理 token 明细；该模型调用也未传 `reasoning_effort`。当前数据模型没有思考强度字段，不能猜上游网关别名是否支持该参数。降低 2048 输出上限也不会自动缩短现有不足 1024 的回复，过度压低反而可能截断 JSON。

可立即沿用现有直接答复路径：无 PMS/工具/资源/人工动作、纯知识自助任务，Judge 已提供非空 `answerText`、有效层与支持事实时，`partial` 不应仅因存在不阻断该办法的 `missingAspects` 就再让 Generate 扩写。毛巾回合可因此省去 2.897 秒及无证据的配送否定。无事实、已拒绝或失败的办法不进入此路径。

## 协同与回滚

- 本次无共享数据契约、迁移或前端变更；与主任务 `service.go` 的受控缺口 Generate 调整配套。
- 同文件知识合并与提示修改应一并合入；并行上下文和 PMS 文件未覆盖。
- 可仅回退上述代码，不恢复数据库，不改变“薇薇”及“薇薇2”备份；本次未部署或修改服务器服务。
