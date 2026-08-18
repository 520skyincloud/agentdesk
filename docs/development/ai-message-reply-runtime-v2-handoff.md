# AI Message Reply Runtime V2 开发交接

> 状态：代码实现、本地门禁和测试2服务器 Binding 1 灰度部署完成
>
> 日期：2026-08-12
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

核心实现提交 `6a9ad46d2595cf212a90ec431e8307122852c227` 已集成到两个远端 `main`，并通过不可变发布目录
部署到测试2服务器。部署只迁移并运行 AgentDesk 与其 MySQL；FastGPT 和 NewAPI 没有由本次发布
安装或迁移，AgentDesk 继续调用已经存在的 `http://36.138.68.47:6080` 和
`http://36.138.68.47:6081/v1`。API Key、Credential revision、Assignment、Binding 和模型名均未改写。

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

### 7.1 2026-08-12 模型方案 revision 自动跟随

- `StoreModelProfileAssignment` 仍是门店级唯一模型方案绑定；同一门店的所有有效员工绑定继续复用各自
  active NewAPI 凭据，不新增员工级模型选择或重复配置入口。
- 平台发布同一个 Profile code 的新 revision 后，系统自动把仍使用该 code 旧 revision 的 ready 门店
  标记为待升级。门店已人工选择其他 pending 方案时视为显式覆盖，不被自动改写。
- 自动协调器复用现有 `ActivatePendingProfile` 核心流程：逐个有效员工凭据执行九槽真实验证，必要时同步
  FastGPT，再通过 CAS 原子切换 Assignment。任一阶段失败时旧 active revision 继续服务，记录失败并按
  固定间隔重试。
- 发布请求只持久化待升级状态；外部验证和 FastGPT 同步在后台执行，因此不会增加客户消息回复链路延迟。
- 服务启动后的周期协调会补齐已经发布但尚未应用的同 code revision，因此部署前已创建的候选方案也能
  自动收敛。
- 本次没有数据库迁移、公开 API、前端 DTO、WebSocket payload、计费口径或员工号协议变化。
- 按用户要求，本次只执行 `go build ./cmd/server` 和 `git diff --check`，未运行测试套件；部署后由用户先做
  真实模型与会话验收。

## 8. 部署与回滚

### 8.0 2026-08-12 DeepSeek Responses 严格结构化输出

- 基线：`weibao/main@3ab3809`，独立 worktree 分支 `codex/deepseek-responses-schema`。
- Runtime V2 Intent 调用按请求附加 `intent_tasks.v2` JSON Schema，Generate 调用按请求附加
  `reply_output.v2` JSON Schema；两者均使用 Responses `text.format.type=json_schema` 和
  `strict=true`。该配置为内存态且 `json:"-"`，不进入 DTO、日志或持久化配置。
- DeepSeek Responses 只允许精确模型名 `deepseek-v4-flash`，请求使用
  `reasoning.effort=none`。`deepseek-v4-pro` 和带非标准后缀的模型名不能发布为 Responses Profile。
- Responses 适配器完整支持函数工具：请求发送 `tools` 与 `tool_choice=auto`，响应
  `function_call` 转换为 Eino ToolCall，工具结果以相同 `call_id` 的 `function_call_output` 回传。
  普通文本 Responses 调用不携带 Runtime Schema，Interrupt Resume 也不被误套全新回复协议。
- Profile 九槽测试对 Intent/Reply 使用最小真实 Schema，并要求解析到 `{"ok":true}`；普通槽继续
  只验证自身协议。测试通过后才能发布不可变新 revision 并由 Assignment/Activation 切换。
- 已使用服务器现有 active 门店 Credential 在内存中完成真实受控验证，未输出或落盘 Key：
  NewAPI `http://36.138.68.47:6081/v1/responses`、模型 `deepseek-v4-flash`、`strict=true` 返回 HTTP 200，
  输出通过最小 Schema，Usage 中 `reasoning_tokens=0`。该验证只证明网关/模型协议可用，不替代
  Profile 九槽发布测试和真实客户消息验收。
- 本次不增加数据库表、migration、公开 API、DTO、WebSocket 字段、Token 统计或 Binding 计费口径。
  回滚顺序为切回上一 Profile revision，再切回上一二进制发布目录；Credential 不需要改变。

### 8.1 已完成部署

- 服务器：测试2 `36.138.68.47:2301`。
- 服务：`agentdesk.service` 和本机 MySQL 均为 `active`，AgentDesk 监听 `127.0.0.1:8083`。
- 当前发布由 `/opt/agentdesk/current` 指向
  `/opt/agentdesk/releases/20260812-runtime-v2-<git-short-sha>`；目录内 `REVISION` 必须等于两个远端
  `main` 的完整提交号，上一不可变发布目录继续保留作回滚。
- 原域名 `https://weibao.omnireva.com` 继续由旧入口 Nginx 反向代理到测试2服务器；首页、登录页、
  `/api/auth/options` 和企微验证文件均返回 HTTP 200。
- AutoMigrate 后共 124 张表，`t_ai_reply_turn`、`t_ai_reply_turn_task`、
  `t_ai_reply_turn_action` 已存在，DML migration 75 成功。
- 最终同步基线：338 条消息、3 个会话、3 个客户、12 个资源、2 个知识库、87 个 AI Reply Job。
- 历史失效 Outbox `id=1` 已标记 `cancelled` 且清空 `next_retry_at`，不会在新 Worker 启动后误发。
- 完整 V2 仅对 Binding `1` 启用；数据库确认该 Binding 属于 Tenant `2`、Store `1`“合肥南七”。
- 合肥南七 FastGPT 真实检索通过：“咖啡”命中 8 条，Provider 902ms；“停车场”命中 12 条，
  Provider 3670ms。
- 当前模型方案 `3@revision 3` 的九槽 NewAPI 真实测试通过，Provider 8747ms，无失败槽、无验证问题。
- 上述测试均通过微宝本机受控 API 发起，不发送企微客户消息，也没有部署或修改 FastGPT/NewAPI。

生产进程已读取以下配置：

```text
AI_REPLY_TURN_COORDINATOR_ENABLED=true
AI_REPLY_TURN_COORDINATOR_BINDING_IDS=1
AI_RUNTIME_CONTEXT_COMPILER=v2
AI_RUNTIME_INTENT_CONTRACT=v2
AI_RUNTIME_REPLY_CONTRACT=v2
AI_RUNTIME_VALIDATOR=v2
AI_RUNTIME_ACTION_LEDGER=authoritative
AI_RUNTIME_V2_BINDING_IDS=1
```

切换前环境备份：

```text
/opt/agentdesk/backups/runtime-production.env.before-full-v2-20260812-043834
```

### 8.2 仍需真实消息验收

代码门禁、进程环境、数据库结构、域名和外部依赖真实调用已经验证。客户可见行为仍需在 Binding `1`
重放知识多题、资源动作、人工接管、Resume 和 Outbox 失败重试，并观察至少 30 分钟的阶段延迟、
协议修复数、Validator 拒绝、Action 状态和 Outbox 取消。没有真实发生的客户消息，不得仅凭环境开关
描述为业务验收通过。

### 8.3 回滚

优先将上述五个 `AI_RUNTIME_*` 模式恢复为 legacy/shadow，保留 Binding 范围并重启
`agentdesk.service`。若需要回滚二进制，再把 `/opt/agentdesk/current` 切回上一发布目录。新增表和
nullable 字段不会阻止旧代码运行；若同时回滚 NewAPI 地址，需要显式恢复上一 Profile 网关，
Credential/API Key 无需改变。

### 8.4 2026-08-12 回复成功后误转人工热修

- 生产消息 `360` 已由 `gpt-5.6-luna` 成功生成并提交消息 `361`，但 AI Reply Job `99` 随后被错误标记为
  `commit_failed_human_dispatch`。根因是逐题任务使用 48 位小写 SHA-256 截断值作为稳定
  `ClientMsgID`，而 Job 完成证据校验仍只接受历史 `ai_reply_` 等前缀。
- 最小修复仅扩展 `isStableRuntimeAIClientMsgID`：继续接受历史前缀，同时严格接受长度恰好 48、字符仅为
  `0-9a-f` 的新版轮次哈希。Tenant、Conversation、Session、RequestID、消息方向、撤回和发送状态校验
  全部保留。
- 回归测试覆盖合法轮次哈希、长度错误、大写和非十六进制值，以及 RequestID 不一致和发送失败仍必须
  转入 `commit_failed`。
- 热修提交为 `1d73c49b12705bdf85e91ee43d29d32bf83e3785`，发布目录为
  `/opt/agentdesk/releases/20260812-ai-evidence-1d73c49`。服务重启后 `agentdesk.service` 为 active、
  `NRestarts=0`，本机 `/api/auth/options` 返回成功。
- 回滚只需将 `/opt/agentdesk/current` 切回
  `/opt/agentdesk/releases/20260812-role-navigation-491e5b6` 并重启服务；无数据库、API、前端或配置变更。

### 8.5 2026-08-12 上下文预算与模型重试热修

- 生产消息 `367`“我想喝咖啡”已完成 Intent 和 FastGPT 检索：4 条命中、2 条进入 Context、检索
  1034ms。实际失败发生在 Generate Prompt 编译，错误为
  `context_mandatory_overflow: stable policy=1074 cap=1063`；模型 Generate 请求并未发出。
- 根因是 ContextCompiler 把 stable policy 的 15% 分类配额误当独立硬上限。修复后分类比例只用于
  可选上下文裁剪；稳定规则和基础 Runtime Snapshot 只受完整 `AvailableInput` 硬预算约束。
- 同时，生产消息 `368`“酒店有拖鞋吗”在 Intent 阶段约 30 秒超时。九槽配置为
  `TimeoutMS=30000`、`MaxRetryCount=2`，但旧代码用同一个 30 秒 Context 包住整组重试，导致首次
  超时后剩余两次调用直接取消。修复后单次超时仍为 30 秒，一次逻辑调用的 Context 覆盖初次调用、
  两次重试和退避，同时继续受 180 秒整链总时限约束。
- 编译错误现在记录为 `context_build`；未真正调用 Reply 模型时不再创建伪造的
  `reply_generate/model_call_failed` 用量。Intent、Generate、协议修复和人工兜底契约保持不变。
- 无数据库迁移、公开 API、WebSocket、前端、Intent Schema、FastGPT 或 Profile 配置变化。回滚只需
  恢复上一二进制。

### 8.6 2026-08-12 DeepSeek Schema 拒绝与人工路由恢复

- 合肥南七 Binding `1` 的生产消息 `376`“你好”和 `378`“我想办理入住”均在 IntentDetect 失败，
  FastGPT 和 Generate 调用次数为 0。使用同一门店 active Credential 复现得到 NewAPI HTTP 400：
  Runtime Schema 中仅含 `const/enum` 的节点缺少显式 `type`，被 DeepSeek Responses 严格校验拒绝；
  请求未进入模型推理，Token 为 0。
- Intent 和 Reply 的嵌入 Schema 已补齐显式类型，并在发送 Responses 前执行等价标准化。Profile 测试
  改为真实执行 `intent_tasks.v2` 和 `reply_output.v2`，不再用简单 `{"ok":true}` 代替 Runtime 协议。
  Schema 400 归类为 `structured_output_schema_rejected` 且不做无意义网络重试；超时、429 和 5xx 仍按
  初次调用加两次重试。
- 真实 NewAPI `http://36.138.68.47:6081/v1/responses`、模型 `deepseek-v4-flash` 已分别通过普通调用、
  Intent Runtime Schema 和 Reply Runtime Schema 三条受控验收，均返回 HTTP 200。未修改或部署
  NewAPI/FastGPT，也未记录 Key、Prompt 或上游原始响应。
- 旧失败按既有 `intent_detect_failed` 策略把 Conversation `3` 持久化为
  `HQ_AGENTDESK_PENDING`。该状态会在消息入站和 Job 执行两处阻断 AI，因此修复二进制后继续发送消息
  仍会表现为转人工，且模型、知识库、Generate 均不会运行。
- 新增内部 `ConversationAIRecoveryService`，在一个事务中结束有效人工 Assignment、恢复 Conversation
  为 `AI_SERVING`、清空人工所有权和转人工元数据、恢复 Route、清理 pending 字段、取消残留
  `AIManualResumeTask`、同步 DialogueState 并记录审计事件。客户取消人工等待和普通人工超时恢复复用
  此服务，避免 Conversation 与 Route 半恢复。
- 新增仅服务器管理员可运行的 `cmd/conversation_ai_recovery`，不增加 HTTP API、前端入口、DTO、
  WebSocket payload、模型调用或数据库结构。命令必须提供会话 ID 和审计原因，并使用生产配置及环境
  密钥调用上述业务服务，禁止直接 SQL 修复会话状态。

### 8.7 2026-08-13 自动转人工真实根因与本次修复

诊断文档 `customer-service-auto-handoff-diagnosis-playbook.md` 对测试 2 的数据库记录和运行日志的结论为：

- 入住、咖啡、停车等问题并不是因为连续消息并发把知识库打崩；真实失败记录集中在
  `t_ai_usage_event.stage=reply_generate`、`error_class=model_call_failed`，旧代码一次模型失败后直接把
  Job 送入人工池。
- 知识检索成功的任务会留下命中证据；部分知识任务失败时，旧任务账本会把失败扩大成整轮人工，成功任务也
  不能独立收敛。
- 电话资源缺失属于门店配置不完整，不是客户问题不可处理；旧资源构建错误被当作不可恢复错误，导致整条回复转人工。
- 入住小程序动作原先主要依赖模型是否临时输出资源动作；现在在 Runtime 归一化层和协议修复后的本地恢复层
  统一补齐 `hotel_info/checkin_process` 知识任务及 `provide_mini_program` 资源任务，模型漏动作不会改变提交契约。

本次代码修复的结构化边界：

- `AIReplyExecutionError` 携带错误分类和可重试元数据。Intent、Generate、Knowledge、空输出、资源不变量和
  Commit 的技术失败先使用已有 Job/逐题任务退避预算；只有不可恢复错误或预算耗尽才进入人工池。
- FastGPT/模型每次网络调用的重试仍由各自网关或九槽客户端负责；Job Claim 只负责恢复，不重复放大已经耗尽的
  单阶段调用。人工派单失败只重试派单，不重新运行模型。
- 知识任务失败会释放 Claim、写入 `next_retry_at`，成功任务保持 delivered/committed；当 Runtime 在生成任务键前失败时，
  `MarkUnfinishedHandoffPendingDB` 从持久任务账本收敛所有未完成任务，避免账本悬空或静默完成。
- Responses Schema HTTP 400 被识别为确定性 `structured_output_schema_rejected`，同一非法请求不重复发送；
  超时、网络、429、5xx 和上游临时失败仍按既有初次调用加两次重试。
- 缺少门店电话时 Runtime 返回“当前门店暂未配置联系电话，请联系门店获取。”，不再因为缺配置把普通客户问题直接转人工；
  readiness 检查仍会报告缺配置，避免把配置问题隐藏。

### 8.8 服务器版本状态

此前测试 2 当前运行的是 `/opt/agentdesk/releases/20260813-takeover-65802f4`，对应旧提交
`65802f40ceff3fd5947a651a60c944ce2e315b11`。因此在本次提交部署前，服务器不会包含本节新增的错误状态机、
入住确定性规则和任务账本修复；此前在服务器上看到的旧行为不能作为本次修复的验收结果。

本次提交完成后必须使用提交完整 SHA 构建新 release，保留旧 release 作为回滚点，原子切换
`/opt/agentdesk/current`，重启 `agentdesk.service`，并核对 `current/REVISION`、二进制 SHA-256、服务状态、
`NRestarts`、本机健康接口和关键错误日志。未完成这些步骤前，不宣称服务器已更新。

### 8.9 2026-08-13 测试 2 实际发布结果

- 修复提交：`2c06d10f32c113fb0e3d3a33bf6d7dde5a2aa8f0`。
- 新 release：`/opt/agentdesk/releases/20260813-runtime-retry-2c06d10`。
- `current/REVISION`：`2c06d10f32c113fb0e3d3a33bf6d7dde5a2aa8f0`。
- 运行二进制 SHA-256：`57f7c3ec6ff3571d1caa396a1c17b3fa74973d27eb53c70fb141e5cdf661a9ba`，与本地构建产物一致。
- 发布前旧 release `/opt/agentdesk/releases/20260813-takeover-65802f4` 保留；本次成功备份目录为
  `/opt/agentdesk/backups/pre-runtime-retry-2c06d10-20260813-053404`，包含运行配置和 MySQL 压缩快照。
- `agentdesk.service` 为 `active`，`NRestarts=0`；本机 `8083` 根路径和 `/api/auth/options` 均返回 HTTP 200，
  公网 `http://36.138.68.47:2303`、`https://36.138.68.47:2303` 和 `https://weibao.omnireva.com` 均返回 HTTP 200。
- 关键任务表 `t_ai_reply_turn`、`t_ai_reply_turn_task` 均存在。发布后未执行客户消息重放，因此业务表现仍需用户用真实消息验收，
  不能把健康检查当作 AI 回复验收。
- 本次只替换 AgentDesk 应用，没有部署、重启或修改 FastGPT、NewAPI、企微回调、Nginx、systemd 或运行环境配置。
- 发布期间第一次备份因 MySQL 账号缺少 `PROCESS` 权限被 `mysqldump` 的 tablespace 导出拒绝，未发生切换；随后使用
  `--no-tablespaces` 重新备份并成功发布。该权限问题不影响应用运行。
- 发布后日志仍周期性出现既有 `FastGPT usage sync failed`（知识库用量同步告警）；未发现 API Key、Authorization Bearer、
  Prompt、原始上游响应或客户敏感字段泄漏。

回滚时将 `/opt/agentdesk/current` 原子切回旧 release 并重启 `agentdesk.service`；数据库无需回滚，若需要恢复数据可使用上述
发布前 MySQL 快照。

### 8.10 2026-08-18 连续语音漏答与入住流程修复

生产会话 `t_conversation.id=3` 的真实记录确认，本轮问题不是 FastGPT 并发崩溃：长语音的三个知识查询均正常并发完成，
无 5xx 或超时。故障来自四个串联断点：改写后的入住 Task 按 `sequence` 错绑到上一条“？”消息；口语“可以玩的”没有归一到
“附近游玩”；“咖啡+草稿纸”被合成一个 Task 后只要咖啡命中就把整项视为已回答；知识无命中时 Generate 又追问会话中
已经确定的门店。

本次只修改 V2 真实运行链路：

- Task 来源无文本证据时固定绑定当前触发消息，`sequence` 只表示任务顺序，不再猜测 Turn 内消息序号；同一消息拆出的多个 Task
  继续允许共享同一来源消息。
- 知识任务在持久化入账前按可靠问句边界拆成原子 Task，每题分别拥有检索结果、终态和回复覆盖；无标点的并列对象仅在两侧属于
  不同业务主题时拆分，避免把“早餐时间以及地点”误拆。
- `surrounding_facilities` 根据当前题目追加“附近游玩 / 附近餐饮 / 周边设施”中文主题锚点，保留客户原句且仍只执行一次检索，
  不增加模型调用。
- no-hit 计划增加 `do_not_ask_known_store_scope` 约束；Validator 对已知 `Conversation.StoreID` 的会话拒绝“哪家店/哪个门店”类
  重复追问，协议修复仍只使用既有的一次预算。
- Intent Prompt 只补充“并列对象逐项拆分”的语义规则，正确性仍由服务端原子 Task 和 Validator 保证，不依赖提示词单独兜底。

影响边界：无 model/migration、DTO/enum、HTTP API、WebSocket、企微协议、Token/Usage 或计费口径变化；与当前客服接管并行提交
无同文件修改。定向回放与 `internal/ai/runtime/executor` 全包测试均通过。生产 FastGPT Dataset
`6a5b172f8a2e8f826f7507e6` 已提交一条只含历史验证事实的完整自助入住流程文件，必须等集合 `dataAmount > 0` 且
`trainingAmount == 0`，并用“给我办入住 / 怎么入住 / 办理入住流程”真实检索通过后才可视为知识数据生效。

#### 8.10.1 生产复核补充

继续核对 `t_conversation.id=3` 后确认：语音 `t_message.id=2080` 的 ASR 已完整产出“附近游玩、咖啡、草稿纸、停车充电”四个
问题，`message_analysis.v2` 状态为 `ready`，因此漏答不发生在语音识别层。旧运行版本把“咖啡+草稿纸”合成一个知识 Task；
咖啡命中后整项被视为成功，草稿纸没有独立结果。原子 Task 修复用于解决这一整类连续消息和并列对象问题。

另确认两个检索层根因：

- 当前生产 Dataset 的 `seed-faq` 只有约 748 条数据，正常入住流程缺失；“办理入住”只能召回“两间房失败、手机不能用”等异常 FAQ，
  旧版本看似正确的入口、电梯、小程序和刷脸说明曾受到历史对话与零散知识共同影响，不能视为稳定证据。
- `supplies_self_help` 会把“草稿纸有没有”改写为“草稿纸有没有 客用品”，FastGPT 前五条因此变成驱蚊用品、消毒用品等宽泛结果；
  直接检索“草稿纸”才能命中现有的准确问答。具体用品查询现已保留原对象，不再追加“客用品”大类词。

本次追加修改：

- 不同业务主题用“和 / 跟 / 或者 / 以及 / 还有 / 顿号 / 句末标点”连接时，可在模型未拆分的情况下确定性拆为独立 Task；
  只有两侧主题可明确区分时才拆，仍不会拆开“早餐时间以及地点”等同一对象的多个维度。
- no-hit 回复若先说“资料没写”后又追加“通常会有、可以去翻找”等无证据酒店常识，Validator 将其判为可修复的事实越界；
  纯边界说明或一个真实缺失字段的澄清问题继续放行。
- 具体用品问题在检索前去掉“有没有 / 给我拿点 / 什么的”等口语包装，只保留实际对象；即使意图模型给拆出的子句保留了
  `service_facility` 这类宽泛 subIntent，“草稿纸有没有”也会稳定检索“草稿纸”，不会再被“客用品”宽词带偏。
- 正常 `checkin_process` 使用一条稳定程序型 Query，同时召回“酒店入口和电梯路线”与“登记后刷脸开门”；手机不可用、小程序打不开、
  另一间房无法办理等异常入住仍保留客户原问法，不会被正常流程覆盖。
- 当前门店的 `DefaultMiniProgramPayload` 能构建真实小程序消息时，为正常入住文字 Task 注入系统权威 `store_fact`：无人值守、无传统
  常驻前台和房卡、通过已配置小程序登记、登记后刷脸开门。这样文字流程先形成，结构化小程序 Task 随后独立发送；FastGPT 新文件
  是否仍在训练不再决定基础入住流程能否回答。
- Evidence Judge 允许“酒店入口 + 电梯”和“入住登记 + 刷脸 + 开门”两类确定性程序证据归入正常入住 Task，仍拒绝退房、异常入住和
  其他主题候选。永久 pending 的 FastGPT 文件集合不作为本次运行正确性的依赖，应由知识库治理单独清理。

影响边界仍为 V2 executor；没有数据库结构、公开 API、WebSocket、企微协议、模型供应商、Token/Usage 或计费变化。与
`codex/customer-audit`、`codex/ai-billing` 当前改动无同文件冲突。针对性回归和 `go test -tags dev ./internal/ai/runtime/executor -count=1`
均通过。
