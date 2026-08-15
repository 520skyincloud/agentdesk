# 多模态契约实施记录（codex/deepseek-responses-schema）

执行依据：`docs/development/ai-reply-runtime-v2-multimodal-dispatch-reliability-plan.md` +
`docs/development/ai-reply-runtime-v2-multimodal-code-json-contract.md`。

## 已落地提交（截至 2026-08-14）

| 提交 | 内容 |
|---|---|
| a57c0d8 | 30 个运行时契约 schema（observation/message_analysis.v2/turn_input_envelope/intent_tasks.v3/question_unit/task_source_bindings/task_normalization/capability_decision/reply_plan.v4/reply_output.v3/handoff_decision.v2/handoff_pending_action.v2 等）+ MessageAnalysis 权威接线（EnsurePending/CompleteReady、worker ledger 字段、media_understanding RecordMediaReady） |
| 9ef954c | ObservationPolicyProjector（最小权限 allowed/forbidden 投影）+ TurnInputEnvelope（U*/O* 引用、pending media 空占位、语音缺转写检测） |
| e5e2f07 | intent_tasks.v3 rune span 校验 + QuestionUnit 规范化（同源去重、canonical hash、degraded_single_task 降级） |
| c9d14e2 | CapabilityDecisionV1 服务端路由（信息问答绝不转人工；办理请求缺字段立即澄清；business_handoff 仅在发布配置允许时） |
| af42140 | AIReplyTurnTask 持久字段（SourceBindings/CanonicalQuestionHash/Capability/AnswerGroupKey 等 §3.2 全量）、StableTaskKey 去 OccurrenceIndex、findCanonicalDuplicate 不再跳过同源、MarkTechnicalFailureDB / AttachKnowledgeCheckpointDB / CancelHandoffDecisionDB、BuildFinalAnswerGroups 确定性分组（§22.12 规则 1-7） |
| 8377795 | contracts Go 结构（ReplyPlanV4/ReplyOutputV3）+ BuildReplyPlanV4（planFingerprint canonical SHA-256）+ ResolveReplyPart（服务端解析 GroundingEvidenceRefs/ResolvedActionRefs，消除 missing_task_evidence） |
| 0d61e29 | ValidatorV3（§19.1 顺序：schema→group/task coverage→server-resolved refs→duplicate content 两层检测（rune bigram Jaccard≥0.85 + 包含率）→fact source→knowledge quality→action claims→safety→commit invariants→classifyRecovery） |
| f5fd08f | AIReplyJob 阶段恢复（ResumeStage/StageAttemptCount/CheckpointFingerprint/ProgressNoticeMessageID + CASAdvanceStage + DecideRecovery 失败分类；技术失败绝不 handoff） |
| f112d7b | HandoffDecisionV2 确定性政策（§20.2：safety→dispatch；technical/knowledge_gap→mode=none；capability route→confirm）+ HandoffPendingActionV2 持久负载校验 |

## 验证

- `go build ./...`、`go vet ./internal/...` 通过。
- `go test ./internal/... -count=1` 全部通过（services 49s 全量含 turn task dedup 回放）。
- 关键不变量测试：技术失败不转人工（DecideRecovery + DecideHandoff 双侧）、地址伪造拒绝、组覆盖缺失可修复、重复内容跨组 retryable、高相似仅告警。

## 已知边界（后续接线）

- 生产 Generate/Commit 链仍走 V2；契约 §22.14 要求 PlanV4+OutputV3+ValidatorV3 成组灰度切换（AI_REPLY_PROMPT_LAYER_V2 同款 env 开关模式），尚未接线到 `executeClaimed` 主链路。
- task_knowledge.go 的批量 metadata join（§22.12 BuildEvidenceForTaskBatch）与 AnswerGroup Reconcile 尚未接线。
- Migration：新增字段均带 default，AutoMigrate 兼容 SQLite/MySQL；与并行分支无共享契约冲突（models.go AIReply* 为本分支独有域）。

## 2026-08-15 第二轮：按计划 §19 逐 Phase 落地生产主链修复（58079b0 已部署 test-2）

| 提交 | Phase | 生产根因修复 |
|---|---|---|
| e133854 | Phase 1 MessageAnalysis | RecordMediaReady 改写 message_analysis.v2（analyzer.kind=asr/vision 合法），CompleteReadyV2/ReadyForMessage 双读；生产 Analysis 行不再停在 pending |
| c47e92c | Phase 2 Knowledge Query | 知识 Query 剥离 [语音]/文件名运输包装；多题按问号/句号/感叹号真实停顿拆分子句（含逗号二级兜底与 ASR 口语重复去重）；MaxContextItems 2→5、TopK 4→5 且 task_knowledge/answerability_gate 统一预算（剃须刀排第 4 必须进入 Generate） |
| 89e9d38 | Phase 3 Echo 对账 | 出站回显先对账：同会话 5 分钟窗内与平台 AI 消息精确匹配 → ai_outbox_echo 补齐送达证据，不创建 Agent Message、不打断 Turn、不切人工（1403/1404-1406 场景） |
| 58079b0 | Phase 4 Validator | Evidence/Action 引用改服务端 deterministic_autofix 派生；模型漏回显不再 rejected + 15s/1m/3m 整链重试；未知引用仍拒绝 |

验证：全仓 build/vet/test 绿；回归测试覆盖 1392 四题语音、1362 口语重复、1399 排名第 4 证据、1403 自回显、missing_task_evidence 五个生产回放。

## 2026-08-15 第三轮：Phase 5-7（1cb0a31 已部署 test-2）

| 提交 | Phase | 修复 |
|---|---|---|
| d67b701 | §14.5/§22.16 | 技术失败（protocol/network/db/content/knowledge/scope/safety）耗尽预算后进入确定性终态 `technical_failure_no_handoff`，三处 dispatchHuman 入口全部按 failure class 门禁；Task 层走 MarkTechnicalFailureDB 终态 failed。测试改按新契约：技术失败 dispatchCalls=0 |
| 592b74e | §3.9.11 | 媒体分析独立于路由：入站即异步触发 UnderstandInboundMessage（幂等），人工接待/知识未配置下 ASR/OCR/文件解析仍完成（1404-1406 场景） |
| 1cb0a31 | §16.3 | 取消转人工改为事务闭合整个 Handoff：V2 payload taskKeys、同轮 handoff_pending/handoff → skipped，派生 pending/running → superseded，delivered 历史不改写；新增 CancelHandoffTransactionDB + 回归测试 |

验证：全仓 build/vet/test 绿（含新契约测试与 handoff cancel 事务回放）。

## 2026-08-15 第四轮：计划 §4/§14/§3.3 剩余条款（bea9294 已部署 test-2）

| 提交 | 条款 | 修复 |
|---|---|---|
| 6d26efb | §4.13/§11.1 | AnswerGroup 改为排序后完整合格命中集合 fingerprint 分组；首条 hit/相同 subIntent/相同全文 Query 不再足以合并（1392 假覆盖场景） |
| 6d26efb | §4.17/§3.3.1 | Task↔RetrieveLog 持久审计链：KnowledgeRetrieveLog 新增 TurnID/TaskID/TaskKey/QueryFingerprint；Task 新增 KnowledgeQueryFingerprint；retriever 回传 RetrieveLogID 并写入 Task（生产 knowledge_retrieve_log_id=0 修复） |
| 5b56b66 | §4.6/§4.15/§3.9.8 | 历史 AI/客服回复进入上下文时带 [NON_AUTHORITATIVE_ASSISTANT_HISTORY] 标签：仅允许指代/语气/避免重复，禁止作为门店事实、知识答案、推荐来源 |
| aef7924 | §14.3/§14.2 | 协议/验证类失败（intent_detect_failed/generation_failed/empty_output 等）改用 800ms/1500ms 短预算，耗尽即确定性终态；15s/1m/3m 长退避只保留给网络/知识不可用类 |
| bea9294 | §3.3 | 企微语音翻译主路径（apply_voice_id/query_voice_text）接入 Channel Breaker：按租户连续失败 5 次熔断 60s，直接使用已配置 ASR，不再每条语音重复 -5103017 失败 |

验证：全仓 build/vet/test 绿（含 AnswerGroup 证据集合、协议短预算、非权威历史标签回归）。

## 2026-08-15 第五轮：热修复 + §10.2 串线修复 + §17 元数据回填（deb8c63 已部署 test-2）

| 提交 | 内容 |
|---|---|
| bc8bf3f | **生产事故热修复**：补 `AIReplyTurnTask.KnowledgeQueryFingerprint` model 字段（前次脚本替换因 gofmt 对齐静默失败，service 写列但 AutoMigrate 未建列 → MySQL 1054 → 知识阶段全失败 → Job 终态不回复）；同时把 MessageAnalysis EnsurePending 的 MarkStale 移出插入事务，消除并发死锁 1213。恢复 Job 639/640 重排队后正常回复 |
| f6757eb | §17.3.1 知识质量元数据离线回填：`BackfillFromRetrieveHits` 从历史检索命中按 (tenant,kb,sourceRecordID) 幂等回填（不覆盖人工审核），cron @every 5m；**生产已回填 200 行，18 条元问题标记 claimType=meta，17.2 运行时门禁（meta/blocked 拦截）从此有数据支撑** |
| deb8c63 | §10.2/4.14 串线修复：来源绑定改「全文相等 → 归一化包含（返回 rune span）→ sequence 兜底」，包含式优先于 sequence，U1 正文不再可能配 U2 意图（1399/1400 场景）；Task 持久化 SourceSpan |

验证：全仓 build/vet/test 绿；生产 deb8c63 active/http200；metadata 回填 cron 生效（count=200）。
