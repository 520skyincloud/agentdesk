# 回复 Runtime 引擎设计

> 状态：当前统一项目权威设计
>
> 更新时间：2026-08-12
>
> 适用分支：`weibao/main`
>
> AI 行为来源：`origin/codex/ai-billing@4db799363040a4478a5585e101d119de11a26f8e`

当前部署状态：Runtime V2 核心实现提交 `6a9ad46d2595cf212a90ec431e8307122852c227` 已集成到两个远端
`main`，测试2服务器的 `/opt/agentdesk/current` 指向与仓库 HEAD 对齐的不可变发布目录。完整
ContextCompiler、Intent、Reply、Validator 和 authoritative ActionLedger 仅对 Binding `1`
（合肥南七）启用。FastGPT 与 NewAPI 是外部依赖，不属于测试2服务器的本次迁移范围。该灰度状态
不代表其他 Binding 已启用，也不代表真实客户消息矩阵已经全部验收。

本文只描述当前生产运行链。旧 AIConfig、独立 Agent 模型绑定、本地知识 ID、历史测试
会话和旧 fallback 可从 Git 历史追溯，但不能作为当前接口、Schema 或行为依据。

## 1. 目标与边界

回复引擎负责把客户连续消息转换为可验证的 AI 回复、资源动作或人工交接：

- 准确：只根据当前 Tenant/Store 行业、知识和会话事实回答。
- 完整：连续多问题逐项覆盖。
- 自然：表达短、直接，符合企微客服语境。
- 安全：不虚构门店事实、执行结果、赔偿或承诺。
- 可恢复：Interrupt/Checkpoint/Resume 不重复调用、发消息或建人工任务。
- 可归因：每次模型调用、提交、Outbox 和 Usage 都能追溯 Tenant/Store/revision/RequestID。

模型不决定客服选人；规则派单是独立确定性领域。

## 2. 可信范围

生产入口在 `internal/ai/runtime/reply_trigger_service.go`。运行时不接受前端拼出的 Tenant、
Store、行业或模型范围，而从已提交业务事实逐层恢复：

```text
AIReplyJob + AIReplyTurn -> Conversation + Message
  -> Conversation.TenantID / CustomerID
  -> Message.SessionNo / RequestID
  -> Tenant-scoped ConversationRouteState
  -> StoreID / StoreStaffBindingID / WxWorkInstanceID
  -> Tenant.IntentProfileID
  -> Store active Model Profile Assignment
  -> Store active Credential revision
  -> Store KnowledgeBase / StoreCustomerRelation
```

同步调用、异步 worker、重试和 Resume 都必须重新验证父链。Tenant、Store、WxWork、
Customer、KnowledgeBase、Profile revision 或 Credential revision 任一冲突都显式失败。

Conversation、Route、当前有效企微实例的 TenantID、StoreID、StoreStaffBindingID 必须完全
一致，且 Route 必须指向该 Binding 唯一的当前有效实例。草稿、停用、删除、已被替换或历史
实例不能解析模型；范围冲突时不得向 NewAPI 或 FastGPT 发起请求。

## 3. 唯一模型解析

所有 Reply、IntentDetect、MemorySummary、CustomerTag、Vision、ASR、Embedding、Rerank
和 DocumentParser 调用统一经过 `ModelCallResolverService`：

```text
conversationId
  -> Tenant + Store
  -> ready StoreModelProfileAssignment
  -> 精确 active ModelProfileTemplate revision
  -> 九槽发布校验（ASR 可显式停用，其余八槽强制启用）
  -> 指定 ModelProfileSlot
  -> active StoreModelCredential
  -> 运行边界内 AES-GCM 解密
  -> 不可持久化 ModelCallConfig
```

不存在：

- AIConfig 或 AIConfigID fallback。
- Tenant 默认模型、Tenant 授权池。
- 企微或员工模型覆盖。
- 平台共享 API Key。
- 缺槽自动替代。
- 其他 Store Credential 回退。

候选测试、FastGPT 同步或 CAS 激活失败时继续使用旧 active revision；首次配置失败时保持
AI 未就绪，不伪造回复。

`AIAgent` 只保留内部接待策略身份，承载名称、基础指令、技能、接待和交接模式。它不选择
Provider、BaseURL、模型或 Key，也没有独立模型配置页面。

## 4. 行业、Intent 与 Persona

行业唯一来自 `Tenant.IntentProfileID`。当前模型与 fresh Schema 不存在 Company，也没有
Store、知识库或企微行业覆盖。

IntentDetect 只读取：

- 已发布行业 `ReplyIntentProfile` 的 Prompt。
- 严格 JSON Schema。
- 该 Profile 下启用的 `ReplyIntentConfig`。

`WxWorkProtocolInstance.PersonaPrompt` 只在 Generate 阶段影响表达语气。它不能进入
IntentDetect，也不能改变行业、意图分类、九槽、Credential、知识、客户标签或人工派单。

IntentDetect 正常只调用一次模型；首轮严格 JSON 解析失败时最多追加一次修复调用。调用
次数和消息顺序由 `TestRuntimeIntentDetectGoldenCallCountAndMessageOrder` 固定。

IntentDetect 的单次调用超时必须读取当前意图模型槽的 `TimeoutMS`；槽未配置超时时使用
60 秒默认值。一次逻辑模型调用的 Context 必须覆盖 `MaxRetryCount + 1` 次单次超时和退避时间，
不能把单次 `TimeoutMS` 同时当作整组重试的总时限，否则第一次超时会取消剩余重试。整条回复仍受
AI Agent 总回复时限、任务租约和会话新鲜度约束；上游 Context 取消、任务租约丢失和会话状态
变化仍可提前取消调用。

DeepSeek V4 的 Chat Completions 调用必须同时显式携带 `thinking.type=disabled` 和
`enable_thinking=false`。该契约只按模型名识别，不能依赖 BaseURL 是否为 DeepSeek 官方域名，
因此统一 NewAPI 网关、Runtime 主链、辅助 LLM 调用和九槽连通性验证必须保持一致。生产验收
还需以成功用量记录中的 `reasoning_tokens=0` 确认上游真实执行结果。

Runtime V2 的 IntentDetect 与 Generate 使用 NewAPI Responses API 的原生结构化输出，不再只靠
Prompt 要求模型返回 JSON：Intent 调用附加 `text.format=json_schema`、`name=intent_tasks_v2`、
`strict=true` 和嵌入的 `intent_tasks.v2` Schema；Generate 调用对应使用 `reply_output_v2` 和
`reply_output.v2` Schema。结构化输出属于单次调用配置，不能写回九槽或全局套在 `reply_llm` 上，
否则人工摘要、普通文本测试和其他复用槽会被错误约束。模型响应仍必须经过本地 `strictjson`、
TaskCoverage、EvidenceReference、ActionReference、Safety 和 CommitInvariant 校验；上游 Schema
不是替代本地验证的成功凭据。

DeepSeek Responses 当前只允许精确模型名 `deepseek-v4-flash`。请求显式携带
`reasoning.effort=none`。发送到 Responses 的 Schema 必须为 `const`、`enum` 等原本可推断的
原始类型补齐显式 `type`，以满足 NewAPI/DeepSeek 对每个 Schema 节点必须声明 `type`、`anyOf`
或 `$ref` 的要求；该处理只能等价显式化类型，不能删除约束或替代本地完整 Schema 校验。
九槽真实测试必须让 `intent_detect_llm` 和 `reply_llm` 分别执行真实的 `intent_tasks.v2` 与
`reply_output.v2` Schema，并验证输出通过对应本地 Schema，不能使用简化的连接测试 Schema，
也不能只以 HTTP 200 或非空 `output` 判定通过。Responses 工具调用同时必须完整传递 `tools`、
`tool_choice=auto`、模型返回的 `function_call.call_id` 以及后续 `function_call_output.call_id`，确保
天气、工单和其他现有工具链在最终严格 JSON 输出前仍可正常执行。

Responses 的确定性 HTTP 400（尤其结构化 Schema 被拒绝）必须记录为受控错误分类，例如
`structured_output_schema_rejected`，并立即失败，不能用槽内网络重试重复发送相同非法请求。
超时、网络错误、429 和 5xx 仍按九槽 `MaxRetryCount` 执行初次调用加槽内重试。日志、Usage 和
RunLog 只保存受控分类、状态码关联证据和请求 ID，不保存 Prompt、原始上游响应或客户正文。

当前统一模型网关为 `http://36.138.68.47:6081/v1`。OpenAI 兼容接口必须从 `/v1` 开始，
`/chat/completions`、`/responses`、`/embeddings`、`/rerank` 和 `/audio/transcriptions` 均在该
BaseURL 后拼接；网关根路径返回的是管理前端，不能作为模型 BaseURL。Migration `75` 会幂等
更新所有 `ModelProfileTemplate` revision 的 `GatewayBaseURL`，新建 Profile、默认种子和真实
模型评测脚本也使用同一地址。Credential/API Key 不随地址迁移写入源码、日志或 migration，
且不存在旧网关回退、镜像连接或双写。

## 5. 阶段编排

```text
Trigger / Batch
  -> Normalize
  -> IntentDetect
  -> IntentPromptSelect
  -> ContextBuild
  -> FastGPT Retrieve / Rerank / Answerability
  -> ReplyPlan / Tool / Resource / HumanRoute
  -> Generate
  -> Validate
  -> Commit
  -> Analytics / Evolution / Outbox
  -> WebSocket
```

- Normalize：合并当前批次并保留消息类型、媒体理解和 RequestID。
- IntentDetect：基于行业 Prompt/Schema 识别主意图、次意图和所需能力。
- PromptSelect：只装载当前意图需要的规则。
- ContextBuild：当前问题优先，其次近期原文、媒体理解、压缩记忆、Store 事实和标签。
- Knowledge：逐问题检索，不用一个命中覆盖其他问题。
- ReplyPlan：确定目标、依据、禁止事项和允许动作。
- Generate：只自然表达计划，不能重新决定真实动作。
- Validate：验证依据、动作账本、敏感承诺和输出结构。
- Commit：同轮全部文本段和结构化动作使用稳定 ClientMsgID，在一个事务内原子提交 Message、
  Outbox、会话计数、AI 轮次和事件；事务后再发布 WebSocket。

IntentDetect、Knowledge、Generate、Validate 和 Commit 的运行错误只能向任务层返回受控分类：
`intent_detect_failed`、`generation_failed`、`knowledge_unavailable`、`empty_output`、
`resource_invariant_broken`、`commit_failed`。模型事件报错必须立即中止；普通客户文本没有回复
文本、结构化动作或持久 Interrupt 时属于 `empty_output`，不能按完成或策略跳过处理。

Runtime 的 `completed` 只表示返回了内部持久化证据：本轮已提交 AI Message ID 列表或已持久化
Interrupt ID。Job 收到后还要按 Tenant、Conversation、源 Message、RequestID、Session 和稳定
ClientMsgID 重新查询数据库；证据不匹配按 `commit_failed` 进入人工兜底。AgentRunLog 仅记录
`committed`、`policy_skipped`、`interrupted`、`failed` 等诊断状态，空错误 RunLog 不是成功凭据。

`provide_location` 是回复计划中的定位资源动作：只有 Store 已配置导航名、地址和有效经纬度
时，才通过企微协议定位消息接口提交 `location`。新联系人欢迎小程序和到店绑定票据只属于
首联资源编排，不得作为定位动作的 fallback，也不得因为客户询问位置而发送小程序。

Tenant 适配只增加可信范围、唯一 Resolver、Store FastGPT 和现有人工任务池端口，不改写
固定 AI 来源中的 Normalize/Intent/Plan/Generate/Validate 行为。

### 5.1 持久触发与恢复

客户文本、HTML、图片、语音和附件与 `AIReplyJob` 在同一 Message 事务内提交。任务写入
失败时消息事务回滚，由企微入站幂等重试；重复 `ClientMsgID` 只补齐缺失任务。历史导入、
AI、人工、系统、撤回和失败消息不创建任务。

`AIReplyJob` 只保存 Tenant、Conversation、Message、Session、Store、Binding、TurnID、
TurnVersion、RequestID、触发类型和受控状态，不保存正文、Prompt、模型输出、密钥、完整指纹
或上游错误原文。唯一键为 `TenantID + ConversationID + MessageID`，状态为：

```text
pending -> processing -> completed | skipped | superseded | expired | failed
                     \-> retry -> processing
```

- `cronx` 每秒 Claim，`ProcessDue` 只负责非阻塞派发，不等待整批模型调用完成；单进程最多并发 4 个任务。
- 新客户消息提交后立即取消同一会话中更早的活动 Context；旧任务重新检查最新消息并直接收敛为 `superseded`，不得占用重试次数。
- CAS 租约为 90 秒，每 30 秒续租；租约丢失立即取消 Context，Commit 前再次校验租约和业务范围。
- 每个模型阶段由当前九槽 `MaxRetryCount` 控制调用重试，默认 `2`，即初次调用加两次重试；
  Intent 严格 JSON 修复属于协议修复，不计作网络重试。Responses 结构化 Schema 被上游以确定性
  HTTP 400 拒绝时，归类为 `structured_output_schema_rejected`，同一非法请求不重复发送。
- Job 最多 4 次 Claim 用于进程崩溃、租约、数据库和可重试 Runtime 失败的恢复，退避为 15 秒、
  1 分钟、3 分钟。阶段重试耗尽后的技术错误仍先进入 Job 的受控重试预算；Job 重试只重新执行
  尚未提交的 Runtime，不重建已经提交的 Message、Outbox、Usage 或人工任务。只有 Job 重试耗尽、
  任务账本达到任务重试上限，或错误明确不可恢复时，才使用稳定键
  `ai_reply_job_handoff_<jobID>` 进入现有人工任务池。
- 人工派单失败时记录 `human_dispatch_retry`，后续 Claim 只重试派单，不再调用模型。
- 知识任务失败会释放任务占用并写入 `next_retry_at`，不把整轮立即改成 `handoff_pending`；同轮已
  成功的任务继续保留，只有该任务重试耗尽后才转人工。
- 任务创建 15 分钟后仍是最新消息且无人回复时，不再调用模型，使用稳定请求键进入现有人工任务池。
- 最近 15 分钟补偿扫描只补非历史、未撤回、可触发且缺任务的客户消息，不扫描旧历史。
- 同 Session 更新客户消息使旧任务 `superseded`；更新人工消息按人工接管处理；System、欢迎语、
  欢迎图片、小程序或绑定卡不能覆盖客户任务。Session 变化或已有回复按现有状态机收敛，关闭、
  撤回、AI 关闭或人工接待使任务 `skipped`；范围损坏使任务 `failed` 且不得派单。
- 已存在与源消息和 RequestID 匹配的稳定 AI Message 或持久 Interrupt 时直接收敛；Outbox 失败
  只重试投递，不重跑模型。

禁止恢复 `TriggerAIReplyAsyncHook`、媒体理解完成后的第二条异步触发路径或任何裸 goroutine
回复入口。Runtime 对 worker 返回 `completed`、`skipped`、`superseded`、`deferred` 四种
结构化结果；媒体等待只能返回 `deferred`，`completed` 必须携带上述持久证据。

### 5.2 持久对话轮次与迟到消息

企微员工号入站必须把协议 `sendtime` 规范化后写入 `Message.SentAt`；`Message.CreatedAt` 只表示
平台实际持久化时间。`CreatedAt - SentAt` 作为 `inbound_lag_ms` 记录，时间缺失、早于 2000 年、
晚于平台 24 小时或形成负延迟时按平台接收时间/零延迟处理。轮次归属使用 `SentAt`，不能使用
网络到达顺序猜测客户是否看过上一条回复。

`AIReplyTurn` 是内部持久协调记录，只保存 Tenant、Conversation、Session、Store、Binding、版本、
首末客户 Message ID、发送时间和 Commit/Delivery 证据，不保存客户正文、Prompt、模型输出或原始
上游响应。状态为：

```text
open -> running -> committed -> delivered
                  \-> interrupted | closed | failed
```

`AIReplyTurnTask` 是轮次内的逐题账本。每个独立问题、资源动作或人工动作使用
`SourceMessageID + 顺序 + TaskType` 生成稳定 TaskKey，只保存范围、意图标签、确定性问题指纹、
阶段、结果码和提交证据，不保存问题正文、知识正文或模型输出。状态为：

```text
pending -> running -> ready -> committed -> delivered
                   \-> covered | handoff | skipped | superseded
```

同一 Turn 通过租约保证只有一个 AI Job 执行；每批最多领取 6 个未完成 Task，余量由同一持久 Job
自动续批。正常多题链路保持一次 Intent、知识 Task 最多 4 路并行检索、一次 Generate；Generate
必须按 taskKey 覆盖所有成功文本 Task，最多拆成三条文本消息。知识 `no_hit` 明确禁止猜测，单项
`failed` 只将对应 Task 转人工，不能清空其他成功结果或重跑完整模型链。

同批知识 Task 各自独立检索。两个及以上 Task 的排名第一命中同时指向相同
`KnowledgeBaseID + SourceRecordID` 时，Runtime 生成内部 `AnswerGroup`，Generate 必须只输出一个
`replyPart`，并在 `taskKeys[]` 中完整列出该组 TaskKey；Commit 只创建一条 AI Message，同时把该
Message 作为组内全部 Task 的提交证据。不同首条命中保持独立回答；该规则不使用关键词或模糊语义
判断，也不跨 Turn 复用旧知识答案。

客户消息事务内完成 Message、Turn Version 和 AIReplyJob 的写入。Turn Version 是当前执行所有权：
同一 Turn 每增加一条客户消息都递增 Version，释放旧 Job 的 Task 领取和 Turn 租约，将旧版本 Job
标记为 `superseded`，再由最新版本 Job 从持久 Task 账本接管未完成工作：

```text
customer Message(sendtime)
  -> lock Conversation + current AIReplyTurn
  -> same Session and sent before prior AI delivery (+1s precision tolerance): increment Version
  -> sent after prior AI delivery: close old Turn and create new Turn
  -> persist Message.TurnID/TurnVersion + Job.TurnID/TurnVersion
```

- System、欢迎语、欢迎图片、小程序、绑定卡和其他自动化消息不关闭 Turn。
- 人工回复、人工接管、撤回、会话关闭、会话继承、Session 变化和 AI 关闭使当前 Turn 失效。
- 只有 `Job.TurnVersion == Turn.Version` 的 Job 才能 Claim、进入 Runtime 和 Commit。新版本提交后，
  进程内旧 Worker 立即取消；即使取消信号存在竞态，Runtime Scope 和 Commit CAS 仍会拒绝旧版本。
- 模型只生成内存中的 PreparedReplyBatch；Commit 事务先 CAS 校验精确 Turn Version、当前单 Job
  租约、TaskKey 领取归属、Session、Route、Store、Binding、当前实例、AI 开关和人工状态。无 Task
  证据、已被覆盖或范围失效的旧批次不得创建 Message 或 Outbox。
- 已在新客户消息事务之前原子 Commit 的 Message 仍可按其 committed/delivered Task 证据投递，避免
  丢失已经完成的不同问题答案；这不代表旧 Worker 可以继续运行。迟到的精确重复问题复用该
  Message/Outbox，不创建第二条回复。
- 精确规范化后相同的迟到问题复用本轮既有 Message/Outbox：pending、sending 直接复用，failed
  提前原任务重试，sent 视为已覆盖，不再调用模型。
- 不同迟到问题从 `LastDeliveredVersion` 后开始构建输入，只回答新增问题。若最终批次与上一答案
  完全相同，允许一次带“只回答新增问题”生成约束的受控 Runtime 重跑；相关模型和知识调用继续
  按本轮 RequestID 正常记录 Usage，仍相同则按 `generation_failed` 转人工。
- 文本只做 Unicode NFKC、大小写、空格和结尾标点标准化后哈希；图片、定位、小程序沿用资源
  指纹。禁止模糊语义去重，避免吞掉真实不同问题。

灰度开关默认 fail closed：`AI_REPLY_TURN_COORDINATOR_ENABLED` 未显式为 `true` 时不启用；启用后
可用 `AI_REPLY_TURN_COORDINATOR_BINDING_IDS` 限定 StoreStaffBinding ID，空白名单表示全量启用。
表不存在时自动回退旧链，不伪造 Turn 证据。

### 5.3 紧邻回答上下文

Generate 在不改变 Intent Schema 和模型调用次数的前提下，可读取当前 Session 内最近 10 分钟的
紧邻上一组客户问题和 AI 回复批次。AI 批次按相同 RequestID 聚合文本、图片、定位和小程序；
System/欢迎消息既不进入回答内容，也不切断承接关系。出现新的客户消息、人工客服消息、Session
变化或超过窗口时，不启用该提示。

该上下文只用于控制表达：重复事实应简短承接，增加条件时只回答新增差异，纠正答案时按本轮知识
重新回答，新主题完全忽略旧答案。旧 FastGPT 答案不得直接复用，本轮仍执行正常知识检索。

### 5.4 Runtime V2 严格契约与 ContextCompiler

Runtime V2 的内部交换数据只允许使用 `internal/ai/runtime/contracts/` 中嵌入的 Draft 2020-12
Schema。进程启动时必须编译并验证全部 Schema；任一 Schema 无法加载时启动失败，不能在运行中
降级为宽松 JSON。当前契约为：

```text
message_analysis.v1
dialogue_state_snapshot.v1
intent_tasks.v2
reply_plan.v2
action_ledger.v1
evidence_bundle.v1
runtime_context_snapshot.v1
reply_output.v2
validation_result.v1
reply_tag_context.v1
runtime_trace.v2
```

所有模型 JSON 使用 `strictjson.DecodeObject`：只接受唯一 UTF-8 JSON Object，拒绝 Markdown
代码块、前后解释、未知字段、重复键和超过预算的 payload。Intent 和 Generate 分别只允许一次
协议修复；修复调用只能调整 JSON 结构和任务覆盖，不能新增、删除或改写业务任务。修复上下文
必须保持相同 Context fingerprint，否则按协议失败处理。

`ContextCompiler` 负责 Intent 和 Generate 的唯一模型输入构建，并按硬 Token 预算裁剪：当前任务
和强约束优先，其次 Evidence、当前 Session 事实、紧邻完整轮次和压缩记忆。裁剪以完整消息或完整
轮次为单位，不能截断 JSON、资源引用或把客户与回答拆开。编译结果包含 fingerprint；协议修复、
Resume 和审计必须复用同一事实范围，不得重新扫描更晚消息或旧知识答案。

稳定规则和基础 Runtime Snapshot 属于强制上下文。各分类百分比只用于可选历史、记忆和 Evidence 的
裁剪目标，不能单独作为强制上下文的失败门槛；只有完整强制输入超过真实 `AvailableInput` 时才返回
`context_mandatory_overflow`。编译失败必须记录为 `context_build`，且未发起 Generate 时不得伪造
`reply_generate` 用量事件。

V2 迁移使用五个内部模式：`ContextCompiler`、`IntentContract`、`ReplyContract`、`Validator`、
`ActionLedger`。默认保持 legacy；只有 Tenant/Store/Binding 灰度范围命中，且环境变量组合满足依赖
约束时才启用 V2。`reply_output.v2` 必须配合 V2 ContextCompiler；完整 Validator 必须配合 V2
ReplyContract；authoritative ActionLedger 也必须配合 V2 ReplyContract。非法组合启动执行前直接
失败，不能部分开启。

当 Runtime V2 命中灰度范围时，`intent_detect_llm` 和 `reply_llm` 对应 Profile 槽必须发布为
`apiMode=responses`、`modelName=deepseek-v4-flash`。同一 Profile 的其他槽继续按各自用途选择
Chat Completions 或 Responses；发布新 revision 前必须使用当前门店 active Credential 逐个真实
测试全部启用槽，再通过 Assignment/Activation 原子切换，禁止直接覆盖 active revision。

### 5.5 MessageAnalysis、DialogueState 与确定性 Validator

`MessageAnalysis` 是按 `TenantID + MessageID + SourceRevision` 保存的派生证据，包含内容
fingerprint、分析器身份、状态和严格 `message_analysis.v1`，不保存额外客户正文副本。相同 revision
只有完全相同证据可幂等完成；同 revision 不同 JSON、MessageID、fingerprint 或状态必须拒绝，
避免恢复任务覆盖已完成分析。

`ConversationDialogueState` 按 `TenantID + ConversationID + SessionNo` 保存严格
`dialogue_state_snapshot.v1`。Reducer 只接受显式事件并使用 CAS revision：客户消息只推进
`BasedOnMessageID`，不能自行推断意图、主题或任务；Intent/Task/AI 提交事件才推进 TurnVersion 和
语义状态；旧 MessageID 或旧 TurnVersion 事件直接忽略且不增加 revision。人工消息、纯路由转人工、
人工接待、等待 AI 恢复和恢复 AI 分别写入 `human_serving`、`human_pending`、`human_serving`、
`resume_pending`、`ai_serving`，并与路由修改处于同一事务。Snapshot JSON 与行内 BasedOn 字段
必须一致，Reducer 重放必须确定性。

Generate 只看到当前未完成 Task、对应 Evidence、允许动作和必要相邻上下文，输出
`reply_output.v2.parts[]`。六级本地 Validator 顺序固定为 Schema、TaskCoverage、EvidenceReference、
ActionReference、Safety、CommitInvariant。缺 taskKey 的多题输出仅属于一次可修复协议错误；未知
Evidence/Action、内部标识泄露、无证据的“已发送/已安排/已转人工”、TurnVersion 不一致和空分段
直接拒绝，不增加第二个模型评分或事实核验调用。

## 6. 批次与媒体

- 短 debounce 合并极短连发。
- 同一窗口内客户连续消息形成一个批次。
- 保留文本、图片、语音、文件、定位、小程序和表情类型。
- 文本追问刚才媒体时可短等媒体理解，再唤醒最新追问。
- 只有普通媒体且没有明确诉求时不主动回复。
- 媒体含报错、求助、投诉、退款等明确诉求时进入意图阶段。
- 后续文本改问 WiFi、发票、定位等业务问题时，以文本业务意图为主。

媒体理解使用当前 Store 精确用途槽，不得调用平台默认模型。

企微首联资源由统一编排入口发送，并统一以 `System` 消息和 Conversation + 资源类型构成的
稳定 ClientMsgID 提交。存在有效静态到店连接时，到店绑定票据替代普通欢迎小程序；首条客户
消息和联系人同步发生竞争时，只有本次真实创建会话的一方可以发送
`arrival_bind_ticket_<ticketID>`。已有会话的联系人变更不自动发卡，票据过期也不恢复资格；
后台显式发卡和真实再次扫码继续走各自独立链路。欢迎资源不能覆盖客户 AIReplyJob，也不能
改变客户消息的 IntentDetect 与定位动作。

## 7. FastGPT 知识

知识只走托管 FastGPT：

- KnowledgeBase 必须属于当前 Tenant + Store。
- Dataset/Profile/Credential applied revision 必须与当前 Assignment 一致。
- 本地 KnowledgeDocument、KnowledgeFAQ、KnowledgeChunk、Qdrant 和本地向量 fallback
  均不存在。

FastGPT Data ID 映射后的 `SourceRecordID` 是唯一稳定知识身份。检索上下文、引用、命中
审计、资源列表和前端展示都复用它；当前 DTO/model 不保留旧 DocumentID、FAQID、
ChunkID 或本地知识兼容字段，也不通过标题猜测身份。

知识规则：

- 多问题分别检索后合并，不能只检索整段。
- IntentDetect 明确识别寒暄、感谢、表情等社交意图时不检索；`interaction/clarify` 且未声明
  知识需求时允许一次条件 FastGPT 探测，达到当前阈值后补成 `hotel_info/store_knowledge`
  任务，探测结果直接复用于正式回答，禁止重复检索。
- 包含明确对象的澄清问题可直接探测；“这个呢”“还有吗”“它有吗”等纯指代问题只有在紧邻
  上下文能唯一解析一个对象时才允许探测。无法唯一解析时保留澄清，不能因任意知识命中强制
  改成知识意图。
- 未命中时不编造，也不注入固定“已记录/同事跟进”话术。
- FastGPT 无命中与基础设施失败必须分开：无命中可追问一个关键点；基础设施失败在网关初次
  调用加两次重试后返回 `knowledge_unavailable` 并进入人工。
- 低风险 FAQ 优先回答或追问关键字段，不默认转人工。
- 真实服务动作只能由当前工具/接待路由决定，模型不能虚构已执行。
- 检索失败时仍可基于非知识上下文继续 ReplyPlan，但必须带“不编造门店事实”约束。

## 8. 客户标签上下文

回复标签只读取当前 StoreCustomerRelation 中：

- 已提交。
- 属于当前 Tenant 行业。
- 当前 Tenant 已启用。
- `ReplyEnabled` 为真。

标签不进入 IntentDetect、检索 query、工具或人工路由，不新增模型调用。标签读取失败
fail open，原 Generate messages 不变。客户标签演化和回复标签上下文是两个独立开关，
fresh Store 默认都关闭。

## 9. 人工交接

AI 只输出是否需要人工、原因和客户等待文案。唯一任务入口是
`ConversationHumanDispatchService`：

- 进入现有人工任务池。
- 客服组、小组、排班、Presence、容量、公平债务、SLA、恢复和转派继续走 manual/rule。
- 模型不得给客服打分或选人。
- 同一 RequestID 重试不得重复建任务或重复发送等待文案。

明确人工、退款/赔偿/投诉升级、安全、隐私、严重订单异常和价格争议可进入人工路由。
普通 FAQ、用品、电视、入住、小程序、定位、轻互动和普通文件咨询不能因为关键词误转。

模型槽与 Job 重试都耗尽、FastGPT 基础设施失败达到任务上限、空输出、结构化资源不变量损坏
或 Commit 失败才进入同一任务池；确定性 Schema/配置错误可直接进入该池，因为继续发送同一非法
请求没有恢复价值。该兜底只负责保住客户问题，不改变客服选择、排班和容量算法。范围损坏时继续
fail closed，不使用不可信 Store/Binding 创建人工任务。

## 10. 提交、Outbox 与 WebSocket

成功回复提交顺序：

```text
Validate
  -> AIReplyTurn lease + Task ownership CAS
  -> authoritative AIReplyTurnAction ledger
  -> stable ClientMsgID
  -> Message batch + Outbox + Conversation cursor/counters + AIReplyTurn evidence + EventLog transaction
  -> ServiceAnalyticsCapture
  -> ObserveCommittedMessage
  -> WebSocket refresh/resync
```

- 外部客服/AI消息在 Message 事务内写入 `OutboundChannelType` 持久投递意图。
- 同轮多段文本和结构化动作全部提交成功后才形成可见结果；任一 Message、Outbox、计数或事件
  写入失败时整个事务回滚。
- `AIReplyTurnAction` 以稳定 ActionKey 保存资源或人工动作证据，状态为
  `requested -> prepared -> committed -> delivered`，投递失败进入 `delivery_failed` 后只能由原
  Outbox 重试；资源构建失败进入 `failed`，旧 Turn 进入 `superseded`。新 Turn version 可以把同一
  superseded 动作重新置为 requested，但不能复用旧 payload、MessageID 或 OutboxID。
- authoritative 模式下 Commit 只能消费 ActionLedger 中 status=`prepared` 且 PreparedRevision
  完整的 `PreparedAction`。Trace 中的旧 `resourceActions`、知识资源或文本描述只能用于诊断，
  绝不能反推动作或补建资源消息。
- 事务后仍按 `(channel_type, message_id)` 幂等补偿 Outbox。
- 相同 ClientMsgID 重试只补建 Outbox，不重复模型、运营事实或标签演化。
- AIReplyJob 在模型执行前和 Commit 前重新读取 Session、Route、Binding、实例、AI 开关和接待状态。
- Outbox Claim 前再次读取关联 AI Message 的 TurnID/TurnVersion 和已提交 Task。带 Task 证据的消息
  只有在 Task 仍为 committed/delivered 时才可发送；已被覆盖、人工接管或范围失效时进入
  `cancelled`。仅对没有 Task 证据的兼容旧消息，更新版本完成 Commit 后才按
  `cancelled_stale_turn` 取消；这样既不重复发送，也不会丢弃已经完成的独立问题答案。
- 后台补偿只扫描明确有持久投递意图且缺 Outbox 的新消息。
- Outbox 待投递查询和 CAS Claim 都要求 `next_retry_at IS NULL OR next_retry_at <= now`；未到期
  不能抢占，到期后只重试协议投递。
- 普通 AI 回复在批量 Commit 前，对同 Tenant、Conversation、Session、紧邻 AI 批次且不超过
  10 分钟的知识图片、定位和小程序做资源指纹比较。图片使用 AssetID，定位使用门店、标准化
  经纬度和地址，小程序使用 AppID、标准化 PagePath 和门店/业务资源身份。
- 已成功发送的相同资源默认只保留本轮文本；原 Outbox 为 pending、sending 或 failed 时复用原
  投递任务，pending/failed 可提前到期，不创建第二条资源消息。客户明确指出资源类型并要求
  重发时允许新提交；只说“再发一下”且上一批存在多个资源时必须追问具体资源。
- 资源过滤后若本轮没有文本，Commit 必须提交确定性的“仍在上面/正在重新发送”文本，继续使用
  本轮 RequestID 和源消息稳定 ClientMsgID，不能静默完成。System 欢迎资源、首次绑定卡和真实
  再次扫码仍使用各自频控，不参与 AI 资源去重。
- 历史空意图行和企微员工人工自回显不会被误发。
- Outbox 或 WebSocket 失败不能重跑模型。

## 11. Usage、Trace 与秘密

每次 provider 调用记录：

- TenantID、StoreID。
- Profile ID/revision、用途槽。
- Credential revision。
- RequestID、上游 receipt、token 和费用归因。

API Key、密文、nonce、完整 fingerprint、客户正文、Prompt 和完整上游响应不得进入
Usage、Trace、日志或 API。Trace 只保存受控结构化阶段证据和脱敏错误分类。
ActionLedger 可在内部 TraceData 的 `suppressedActions` 记录资源去重、原投递复用、显式重发和
歧义重发澄清；该字段不进入公开 DTO 或 WebSocket。

## 12. 失败关闭

- Tenant 行业缺失：阻止 AI。
- Store Assignment 或必需模型槽不完整：阻止 AI；ASR 停用时仅语音转写能力不可用。
- Credential 未激活、revision 不一致或解密失败：阻止 AI。
- FastGPT 未就绪：知识路径失败关闭，不读取本地 fallback。
- 任一父链跨 Tenant/Store：拒绝执行。
- IntentDetect、Generate、Knowledge、Validate、资源构建或 Commit 受控失败：先按错误元数据判断
  是否可重试；可重试错误进入 Job 或逐题任务的退避队列，重新检查会话新鲜度后继续处理。确定性
  Schema/配置错误，或重试预算耗尽后，进入现有人工任务池，不把 Job 标成 `completed`。
- Commit 已成功但外部发送失败：只重试 Outbox，不重跑模型。
- 需要人工且 AI 不可用：仍可进入现有人工池。

Schema 继续通过 AutoMigrate 同时兼容 SQLite/MySQL。Runtime V2 新表和 nullable 关联字段不需要
历史正文 backfill；DML runner 只执行明确、幂等的数据同步。Migration `75` 是本次唯一数据更新，
只把现有 Model Profile revision 的网关地址切到统一 NewAPI `/v1`，不修改 Credential、Assignment、
Binding、知识库或会话数据。旧 AIConfig、本地知识和兼容 Resolver 仍不得恢复。

## 13. 关键回归

- AI 来源阶段顺序、Prompt/Schema 和模型调用次数不漂移。
- Persona 不进入 IntentDetect。
- 所有模型用途只能经 Store Resolver。
- FastGPT 命中以 SourceRecordID 贯穿检索、引用和审计。
- 标签开关关闭时 Generate 上下文不增加标签。
- need_human 只进现有任务池，规则派单不调用模型。
- ClientMsgID 重试不重复模型、消息、任务或运营事实。
- Message 与 AIReplyJob 原子提交，进程重启、租约回收和补偿扫描不丢回复任务。
- 企微入站 `sendtime` 固化到 SentAt，CreatedAt 保持平台接收时间；1、2、3、14 秒迟到的相同问题
  必须加入原 Turn，最终只发送一次回复。
- 两个 Worker 同时竞争同一 Turn 时只有精确匹配最新 Turn Version 且持有租约的 Job 可执行。新客户
  消息升级 Version 后，旧 Job 必须被取消并收敛为 `stale_turn_version`；最新 Job 依据稳定 TaskKey
  接管全部未完成任务。Outbox 以已原子提交的 Task 终态作为最终门禁，未提交旧批次不能继续发送。
- 两个相关知识问题命中同一排名第一知识记录时只提交一条 Message，并把同一 Message ID 写入全部
  对应 Task；命中不同知识记录的问题仍逐题回答。
- 每槽 `MaxRetryCount=2` 时超时、5xx 或空模型结果恰好执行三次 provider 调用，耗尽后只创建
  一个稳定人工任务；派单重试不重跑 Runtime。
- Runtime `completed` 缺少有效 Message/Interrupt 证据时按 `commit_failed` 处理；空错误 RunLog
  不得收敛任务。
- 多段文本与文本加定位原子提交，任一写入失败不留下半段消息或孤立 Outbox。
- System 欢迎消息不 supersede 客户任务，人工回复停止 AI，更新客户消息 supersede 旧任务。
- 知识问题不产生门店资源动作；定位只提交 location；小程序只由明确资源任务或独立首联/扫码链触发。
- 紧邻重复事实自然承接且不重复知识图片；明确重发可发送，多个资源的模糊重发只追问资源类型。
- 相同定位、小程序和知识图片按各自指纹去重；不同 Tenant、Conversation、Session 或超过 10 分钟
  不互相抑制，System 欢迎资源不参与比较。
- 纯指代澄清只有对象唯一时才做条件知识探测，且 IntentDetect、FastGPT、Generate 调用次数不增加。
- Outbox 在 WebSocket 前形成可靠投递事实。
- SQLite/MySQL fresh Schema 均不包含旧 AIConfig/Grant/StoreSetting/ConversationTag/
  本地知识表与专属列。

验证命令：

```bash
go test -tags dev ./internal/ai/... -count=1
go test ./internal/migration -run 'UnifiedModelProfile|SwitchUnifiedNewAPI' -count=1
go test -tags dev ./internal/ai/runtime/... -run 'RecentAnswered|ClarifyKnowledge|DuplicateResource|ExplicitResend' -count=1
go test -tags dev ./internal/services -run 'AIReplyTurn|TaskLedger|AIReplyJob|Runtime|Reply|Intent|FastGPT|HumanDispatch|MessageBatch|Outbox|WxWorkProtocol' -count=1
go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply|Turn|Task|Runtime|Intent|HumanDispatch|Knowledge|Outbox' -count=1
go test -tags dev ./... -count=1
go vet -tags dev ./...
# 生产静态资源生成后，再验证非 dev 嵌入构建：
cd web && pnpm build:sdk && pnpm typecheck && pnpm build
cd .. && go test ./... -count=1 && go vet ./...
```

## 14. 核心保护区

以下路径共同构成稳定回复协议，普通客服、审计、派单或页面需求不得直接改变其阶段顺序、
Intent JSON、动作字段、幂等键或模型归因：

- `internal/pkg/replyintent/defaults.go`
- `internal/ai/runtime/executor/`
- `internal/ai/runtime/contracts/` 与 `internal/pkg/strictjson/`
- `internal/ai/runtime/contextcompiler/`
- `internal/ai/runtime/reply_trigger_service.go`
- `internal/ai/runtime/runtime_reply_executor.go`
- `internal/ai/runtime/reply_commit_service.go`
- `internal/services/ai_reply_hook.go` 中的内部结果和受控错误契约
- `internal/ai/runtime/conversation_memory_service.go`
- `internal/ai/rag/`
- `internal/services/model_call_resolver_service.go`
- `internal/services/ai_reply_job_service.go` 中的 Job 状态机、完成证据、租约和人工兜底
- `internal/services/ai_reply_turn_service.go` 中的轮次归属、版本 CAS、迟到覆盖和 Outbox 门禁
- `internal/repositories/ai_reply_turn_repository.go`
- `internal/services/ai_reply_turn_task_service.go` 与 `internal/ai/runtime/executor/task_ledger.go` 中的逐题任务状态机
- `internal/repositories/ai_reply_turn_task_repository.go`
- `internal/services/ai_reply_turn_action_service.go` 与 `internal/repositories/ai_reply_turn_action_repository.go`
- `internal/services/message_analysis_service.go` 与 `internal/repositories/message_analysis_repository.go`
- `internal/services/conversation_dialogue_state_service.go`、Reducer 与对应 repository
- `internal/models/models.go` 中 `AIReplyTurn`、`AIReplyTurnTask` 及 Message/Job/Conversation 的内部关联字段
- `internal/services/message_service.go` 中的入站消息与任务原子提交边界

定位、小程序、电话和知识图片的资源动作字段及严格 Builder 也属于保护协议。普通业务需求不得
把一种资源失败降级成另一种资源，或改变欢迎资源与 AI 资源动作的独立触发边界。

确需变更时，必须先说明字段兼容、模型调用次数、Usage/计费、知识范围、动作提交、人工任务和
Outbox 的影响，并运行本文第 13 节回归；外围业务优先通过适配器和向后兼容字段扩展。
