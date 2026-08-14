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
