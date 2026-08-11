# AI Message Reply Runtime V2 开发交接

> 状态：代码实现与本地验证完成，尚未部署服务器
>
> 日期：2026-08-11
>
> 分支：`codex/ai-message-reply-runtime-v2`
>
> 基线：`weibao/main@0568270`

## 1. 目标与边界

本次按 `ai-message-reply-runtime-v2.md` 重构现有回复引擎的内部契约，不重写公开产品接口。
目标是把连续消息、逐题知识、上下文、动作、提交、恢复和发送资格变为可验证的持久证据，避免：

- 模型宽松 JSON 或遗漏 taskKey 后仍提交。
- Trace 推断资源动作。
- 旧 Turn/旧 Worker 覆盖新消息或人工状态。
- 资源已外发但账本 CAS 失败导致重复发送。
- Resume 重入 Intent、Knowledge、Tag 或重新扫描上下文。
- 模型 Profile 仍连接旧 NewAPI 地址。

本分支只更新仓库。没有 SSH、服务器部署、生产数据库修改、生产 Profile 切换或 API Key 变更。

## 2. 实现结构

### 2.1 严格契约

新增 `internal/ai/runtime/contracts/`，嵌入并启动校验 11 个 Draft 2020-12 Schema：

- `message_analysis.v1`
- `dialogue_state_snapshot.v1`
- `intent_tasks.v2`
- `reply_plan.v2`
- `action_ledger.v1`
- `evidence_bundle.v1`
- `runtime_context_snapshot.v1`
- `reply_output.v2`
- `validation_result.v1`
- `reply_tag_context.v1`
- `runtime_trace.v2`

新增 `internal/pkg/strictjson/`，拒绝 Markdown、额外文本、未知字段、重复键、非对象和超限 payload。

### 2.2 ContextCompiler

`internal/ai/runtime/contextcompiler/` 统一构建 Intent 与 Generate 输入：

- 当前 Task 和硬约束优先。
- Evidence、Session facts、紧邻完整轮次、压缩记忆按预算降级。
- 最多保留四个完整近期轮次，不能截断一半轮次或 JSON。
- 编译结果带 fingerprint，协议修复必须保持同一 fingerprint。
- Intent 使用 `intent_tasks.v2`；Generate 使用 `reply_output.v2`。

### 2.3 MessageAnalysis 与 DialogueState

新增：

- `MessageAnalysis`
- `ConversationDialogueState`
- 对应 repository/service/reducer

`MessageAnalysis` 同 revision 只允许完全相同证据幂等完成。`ReadyForMessage` 会复核行内 MessageID、
revision、fingerprint、status 与严格 JSON。

DialogueState 通过 CAS reducer 接收客户、Intent/Task、AI 提交、人工消息、路由和 Resume 事件。
客户消息只推进 Message 证据，不伪造语义；旧 MessageID/TurnVersion 事件忽略且不增加 revision。
纯路由的 `human_pending`、`human_serving`、`resume_pending`、`ai_serving` 与路由修改原子提交。

### 2.4 Intent、知识和 ReplyPlan

- Intent V2 只接受 `intent_tasks.v2`，能力由后端已发布 Intent 配置派生。
- Intent 非法 JSON 最多一次协议修复，不计九槽网络重试。
- 每个知识 Task 独立检索，并发上限 4；成功、no-hit、failed 分开保存。
- 条件知识探测结果直接复用，禁止重复 FastGPT 查询。
- Evidence 以 `EvidenceBundleV1` 传递，Generate 只能引用有效 EvidenceRef。
- `ReplyPlanV2` 为每个 Task 固定目标、知识状态、EvidenceRef、ActionRef 和禁止事项。

### 2.5 Generate 与 Validator

- 单次 Generate 输出 `reply_output.v2.parts[]`，每个文本 Task 必须且只能覆盖一次。
- 协议错误只允许一次定向修复；修复后仍非法则 `generation_failed`。
- Validator 固定检查 Schema、TaskCoverage、EvidenceReference、ActionReference、Safety、CommitInvariant。
- V2 禁止暴露 task/evidence/action 内部标识，禁止在 Action Commit 前声称“已发送/已安排”。
- 纯闲聊 Generate 不携带旧酒店知识；知识任务只看到自己的 Evidence 和必要相邻上下文。

### 2.6 ActionLedger、Commit 和 Outbox

新增 `AIReplyTurnAction` 及 repository/service，状态覆盖：

```text
requested -> prepared -> committed -> delivered
                         -> delivery_failed -> delivered
requested/prepared -> failed | superseded
```

- 资源 Builder 成功后产生内存 `PreparedActionV1`，payload 不进入 Prompt、Trace 或公开 DTO。
- authoritative 模式禁止从 Trace fallback 构造资源。
- Message、Outbox、Task、Action、Turn、会话计数在同一事务提交。
- 稳定 ClientMsgID 使用 Tenant/Conversation/Turn/Version/Task 或 Action key 计算。
- 旧 Outbox 在 Claim 前发现 Turn/Task 失效时取消，并同步把 Action 标为 superseded。
- 外部发送已发生后，即使 Action CAS miss，也不能回滚 Outbox 成功并触发重复发送。
- 资源准备不变量失败持久化 Action failed 证据并返回 `resource_invariant_broken`。

### 2.7 灰度模式

Runtime V2 默认不改变 legacy 行为。内部模式：

- `AI_RUNTIME_CONTEXT_COMPILER=legacy|shadow|v2`
- `AI_RUNTIME_INTENT_CONTRACT=v1|v2`
- `AI_RUNTIME_REPLY_CONTRACT=legacy|v2`
- `AI_RUNTIME_VALIDATOR=legacy|v2`
- `AI_RUNTIME_ACTION_LEDGER=shadow|authoritative`

范围可按 `AI_RUNTIME_V2_TENANT_IDS`、`AI_RUNTIME_V2_STORE_IDS`、
`AI_RUNTIME_V2_BINDING_IDS` 限定。非法组合直接失败；部署时必须逐阶段开启，不能只开后置门禁。

## 3. 数据变化

AutoMigrate 新增/扩展内部结构：

- `MessageAnalysis`
- `ConversationDialogueState`
- `AIReplyTurnAction`
- `AIReplyTurn`、`AIReplyTurnTask`、`Message`、`AIReplyJob`、Outbox 的内部关联/状态字段

没有公开 DTO、HTTP 路由、WebSocket payload 或前端页面变化。所有新增关联均 Tenant-scoped，
并加入租户完整性审计与 SQLite/MySQL Schema 测试。

新增 DML migration `75`：

- 统一地址：`http://36.138.68.47:6081/v1`
- 更新所有 `ModelProfileTemplate.GatewayBaseURL`
- 幂等执行
- 不修改 API Key、Credential revision、Assignment、Binding 或模型名
- 不保留旧地址回退、双写或镜像连接

新建 Profile 后端兜底、前端表单默认值、migration 69 fresh seed 和
`scripts/real_llm_5x30_hotel_eval.py` 默认值也使用该地址。

## 4. 主要文件

- `internal/ai/runtime/contracts/`
- `internal/pkg/strictjson/`
- `internal/ai/runtime/contextcompiler/`
- `internal/ai/runtime/executor/`
- `internal/ai/runtime/reply_commit_service.go`
- `internal/services/message_analysis_service.go`
- `internal/services/conversation_dialogue_state_service.go`
- `internal/services/conversation_dialogue_state_reducer.go`
- `internal/services/ai_reply_turn_action_service.go`
- `internal/services/message_service.go`
- `internal/services/channel_message_outbox_service.go`
- `internal/models/message_analysis.go`
- `internal/models/conversation_dialogue_state.go`
- `internal/models/ai_reply_turn_action.go`
- `internal/bootstrap/init.go`
- `internal/migration/000075_switch_unified_newapi_gateway.go`
- `web/app/dashboard/model-profiles/_components/edit.tsx`

## 5. 兼容性和风险

- 不改变 Intent 公开 Schema、九槽字段、Usage/计费归因或公开接口。
- 默认 feature mode 为 legacy；代码合入本身不会自动全量启用 V2。
- V2 的额外开销是本地 Schema/映射和带索引 CAS，正常链路不新增模型或 FastGPT 调用。
- Route 与 DialogueState 新增同事务写入；本地数据库阶段的目标 p95 仍应低于 50ms。
- Migration 75 直接更新现有不可变 Profile revision 的 BaseURL，这是用户明确要求的全面切换；
  回滚镜像前必须把 Profile 地址恢复到上一网关，或创建并切回上一可用 revision。
- 新地址为 HTTP；部署环境必须明确允许到该内网/受控地址的非 TLS 访问，且不能把该规则扩展到
  FastGPT 或其他外部服务。

## 6. 并行分支影响

共享高风险文件包括：

- `internal/models/models.go`
- `internal/bootstrap/init.go`
- `internal/services/message_service.go`
- `internal/services/channel_message_outbox_service.go`
- `internal/services/conversation_route_service.go`
- `go.mod` / `go.sum`

开始和提交前均需 `git fetch origin`、`git fetch weibao`，检查 `main`、
`codex/customer-audit`、`codex/ai-billing` 的同文件变化。建议先合本 Runtime V2 契约，再让其他
分支 rebase；禁止在客服审计分支单独改模型调用、ActionLedger 或 Usage 语义。

## 7. 验证状态

已通过：

```bash
go test -tags dev ./internal/services -run 'DialogueState|MessageAnalysisReady|OutboxAndActionCancel|AIReplyTurnAction' -count=1
go test ./internal/migration ./internal/services -run 'UnifiedModelProfile|SwitchUnifiedNewAPI|ModelProfile' -count=1
go test -tags dev ./internal/services -run 'ConversationDialogueStateTracks|ConversationRoute|ManualSessionTimeout|AIManualResume' -count=1
go test -tags dev ./internal/ai/runtime/... -run 'ReplyOutputV2|ReplyValidatorV2|AuthoritativeActionLedger|StableTurnClientMsgID' -count=1
go test -tags dev ./internal/ai/runtime/... ./internal/ai/application/runtime ./internal/services -count=1
cd web && pnpm build
```

完整门禁已通过：

```bash
go test -tags dev ./internal/ai/runtime/... ./internal/ai/application/runtime ./internal/services ./internal/migration -count=1
go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply|Runtime|Turn|Action|Outbox|DialogueState' -count=1
go test -tags dev ./... -count=1
go test ./... -count=1
go vet -tags dev ./...
cd web && pnpm typecheck
git diff --check
```

补充检查已通过：`go mod tidy -diff` 无输出，所有改动 Go 文件均通过 `gofmt -l` 检查。

## 8. 部署与回滚

本分支尚未部署。后续部署必须：

1. 先备份当前数据库并记录当前 Profile revision、Assignment 和 Credential revision。
2. 先运行 Schema/迁移预检，再启动新镜像。
3. 首批只为目标 Tenant/Store/Binding 开启 ContextCompiler/Intent/Reply/Validator/ActionLedger。
4. 验证知识多题、资源动作、人工接管、Resume、Outbox 失败重试和 DeepSeek reasoning_tokens=0。
5. 观察至少 30 分钟的阶段延迟、协议修复数、Validator 拒绝、Action 状态和 Outbox 取消。

回滚时关闭 V2 模式并恢复上一镜像。新增表和 nullable 字段不会阻止旧代码运行；若同时回滚
NewAPI 地址，需要显式恢复上一 Profile 网关，Credential/API Key 无需改变。
