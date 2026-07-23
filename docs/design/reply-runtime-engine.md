# 回复 Runtime 引擎设计

> 当前权威状态：本文顶部“当前统一运行时契约”描述
> `codex/tenant-ai-unified-integration` 的生产链路，AI 行为来源固定为
> `origin/codex/ai-billing@4db799363040a4478a5585e101d119de11a26f8e`。
> 文末 2026-06 至 2026-07-15 的评测与演进记录只用于追溯，其中出现的
> `AIConfig`、旧独立 Agent 模型绑定、旧知识 ID 和历史测试会话均已退出当前模型解析，
> 不得作为恢复旧接口、旧表或旧 fallback 的依据。

## 0. 当前统一运行时契约（2026-07-23）

### 0.1 入口与可信范围

生产回复入口仍由 `internal/ai/runtime/reply_trigger_service.go` 负责批次、并发、
Interrupt/Resume 和最终提交。运行时不接受前端传入的 Tenant、Store、行业或模型范围，
而是从已经提交的业务事实逐层重建：

```text
Conversation + Message
  -> Conversation.TenantID / CustomerID
  -> Message.SessionNo / RequestID
  -> Tenant-scoped ConversationRouteState
  -> StoreID / WxWorkInstanceID
  -> Tenant.IntentProfileID
  -> Store active Model Profile Assignment
  -> Store active Credential revision
  -> Store KnowledgeBase / StoreCustomerRelation
```

同步请求、异步 worker、重试和 Resume 都必须重新验证父链。任何 Tenant、Store、
WxWork、Customer、KnowledgeBase、Profile revision 或 Credential revision 冲突都要
显式失败，不能信任全局主键唯一、`ActiveTenant` 前端状态或调用方拼出的 scope。

### 0.2 唯一模型解析

所有 Reply、IntentDetect、MemorySummary、CustomerTag、Vision、ASR、Embedding、
Rerank 和 DocumentParser 调用统一通过
`internal/services/model_call_resolver_service.go`：

```text
conversationId
  -> Tenant + Store
  -> ready StoreModelProfileAssignment
  -> exact active ModelProfileTemplate revision
  -> 9-slot publication validation
  -> requested ModelProfileSlot
  -> active StoreModelCredential
  -> AES-GCM decrypt inside runtime boundary
  -> non-persistent ModelCallConfig
```

不存在 Tenant 默认模型、企微覆盖、员工覆盖、平台默认 Key、旧 `AIConfigID` 或缺槽
fallback。新 candidate 测试、FastGPT 同步或 CAS 激活失败时继续使用旧 active revision；
首次配置失败时不伪造 AI 回复。

`AIAgent` 仍是会话使用的内部接待策略身份，承载名称、基础指令、技能和接待模式。
它不再选择 Provider、BaseURL、模型或 API Key，也没有独立的租户可配置模型页面。

### 0.3 行业、Intent 与回复阶段

行业唯一来自 `Tenant.IntentProfileID`。IntentDetect 只读取该已发布行业 Profile 的
Prompt、JSON Schema 和启用的 `ReplyIntentConfig`；Company、Store、知识库和企微实例
上的历史行业字段固定为零且不参与解析。

`WxWorkProtocolInstance.PersonaPrompt` 是冻结 AI 来源保留的接待表达层配置，不是行业
Prompt 或模型 Profile 配置。它只经运行时 `AIAgent.SystemPrompt` 进入 Generate 阶段的
`Agent 规则`，用于调整当前企微实例的回复语气；IntentDetect 不读取它，且它不能改变
行业 Prompt/Schema、意图类别、九槽、Provider、BaseURL、Credential、知识检索、标签或
人工派单。租户可见的“接待人设”因此不等于租户可见模型 Prompt；平台行业和模型内部
Prompt/Schema 仍不可向租户返回。

当前阶段顺序为：

```text
Trigger/Batch
  -> Normalize
  -> IntentDetect
  -> IntentPromptSelect
  -> ContextBuild
  -> FastGPT Retrieve / Rerank / Answerability
  -> ReplyPlan / Tool / Resource / HumanRoute
  -> Generate
  -> Validate
  -> Commit
  -> Outbox / WebSocket
```

Prompt、Schema、IntentTasks、ReplyPlan、Answerability、Generate、Validate、
Interrupt、Checkpoint、Resume 和 Trace 保持固定 AI 来源行为。Tenant 适配只增加可信
范围、唯一模型 resolver、Store FastGPT 和现有人工任务池端口，不在合并时改写模型行为。

2026-07-23 对固定来源 SHA 的逐文件复核确认：

- `intent_pipeline.go`、`generated_reply_validator.go` 和 `knowledge_guard.go`
  与来源 blob 完全一致；
- IntentDetect 的系统 Prompt、用户 Prompt、严格 JSON 解析和修复提示未改写，正常响应
  只调用一次模型，首轮 JSON 非法时只追加一次修复调用；
- golden 同时注入仅供 Generate 使用的企微 Persona 标记，并断言 IntentDetect 的系统
  和用户消息均不包含该标记；
- executor 主流程仅把旧 `AIConfig` 载体替换为唯一 Resolver 生成的瞬态
  `ModelConfig`，阶段顺序和生成/校验/提交行为未改变；
- 行业解析、知识读取、历史消息和提交资源的差异只用于 Tenant/Store 强隔离以及
  FastGPT-only 事实源，不恢复旧 fallback。

上述调用次数和消息顺序由
`TestRuntimeIntentDetectGoldenCallCountAndMessageOrder` 固定，不能在后续合并中静默漂移。

### 0.4 FastGPT 与客户标签

知识检索只走托管 FastGPT。KnowledgeBase 必须属于当前 Tenant + Store，且其 Dataset、
Profile revision、Credential revision 和 readiness 与当前 Assignment 一致。本地
`KnowledgeDocument`、`KnowledgeFAQ`、`KnowledgeChunk`、Qdrant 及本地向量 fallback
已经退出运行链。

回复标签上下文只读取当前 StoreCustomerRelation 已提交、当前行业、已启用且
`ReplyEnabled` 的固定行业标签。它不进入 IntentDetect、检索 query、工具或人工路由，
不新增模型调用；读取失败时 fail open，原 Generate messages 保持不变。
`CustomerTagEvolutionEnabled` 与 `ReplyTagContextEnabled` 均默认关闭并分别灰度。

### 0.5 人工交接、提交与计量

AI 只决定是否需要人工、原因和客户等待文案。实际任务唯一通过
`ConversationHumanDispatchService` 进入既有人工池，客服组、小组、排班、Presence、
容量、公平债务、SLA、恢复、转派和释放继续由 manual/rule 派单处理。模型不得选人，
同一 RequestID 重试不得重复建任务或重复发送等待文案。

成功回复按以下边界提交：

```text
Validate
  -> stable ClientMsgID
  -> Message + Conversation cursor + EventLog transaction
  -> ServiceAnalyticsCapture
  -> ObserveCommittedMessage
  -> idempotent ChannelMessageOutbox ensure
  -> WebSocket refresh/resync
```

外部渠道的客服/AI消息在 Message 事务内写入内部 `OutboundChannelType` 投递意图，
提交后按 `(channel_type, message_id)` 幂等确保 Outbox，再发布 WebSocket。相同
`ClientMsgID` 重试只补建 Outbox，不重复运营事实、演化游标或模型调用；后台补偿也只
扫描该字段非空的新消息。历史行默认空值，企微员工号人工自回显明确不写该字段，因此
不会被当成待发送消息。

Outbox 或 WebSocket 失败不能重跑模型。每次真实 provider 调用记录 Tenant、Store、
Profile revision、Usage slot、Credential revision、RequestID 和 NewAPI receipt；
Prompt、Response、客户正文、API Key、nonce、密文和完整 fingerprint 不进入 Usage、
Trace、日志或 API。

### 0.6 失败与发布边界

- Tenant 行业、Store Assignment、九槽、Credential 或 FastGPT readiness 任一缺失时，
  不回退旧模型系统；需要人工的客户会话进入现有任务池。
- 当前代码和隔离测试完成不代表生产发布完成。“丽斯文旅 / 高铁南站店”真实
  NewAPI、FastGPT、
  回复、转人工、规则派单、标签、账单及备份恢复证据以
  `docs/development/tenant-ai-unified-integration-plan.md` 的 B13/B14 门禁为准。
- 旧 `AIConfig`、Grant、StoreSetting、ConversationTag 和本地知识链只允许出现在历史
  DML migration、404 回归测试与受控 Schema Cleanup 中。

目标：企微员工号回复必须同时满足“准、快、聪明、人味”。提示词只是表达层，不能承担全部决策。回复链路必须先经过 runtime 引擎做批次、意图、风险和覆盖率判断，再交给模型自然表达。

## 1. 回复质量公式

每次 AI 回复计算一个内部质量分：

```text
Score = 0.30 * Accuracy + 0.20 * Coverage + 0.15 * Speed + 0.15 * HumanTone + 0.10 * Safety + 0.10 * ActionFit - Penalty
```

各项含义：

- Accuracy：是否基于当前门店知识、账号配置、媒体理解和会话上下文回答；不编造 WiFi、价格、政策、承诺。
- Coverage：客户连续发多条时是否逐项覆盖。比如“能开专票不 + WiFi 是哪个”必须同时回答发票和 WiFi。
- Speed：普通文本不等待媒体；媒体追问最多短等，媒体完成后触发最新追问；避免两次模型串行重复回答。
- HumanTone：短、自然、像前台微信；不说“根据知识库/系统显示/我帮你确认下”来逃避。
- Safety：退款、赔偿、投诉升级、安全、订单异常等不乱承诺。
- ActionFit：需要发小程序/定位/追问/转人工时走正确动作；不能把 FAQ 误转人工。
- Penalty：重复回复、只答最后一句、固定兜底、莫名转人工、空口承诺、暴露技术过程都会扣分。

## 2. Runtime 分层

### 2.1 Batch Layer

位置：`internal/ai/runtime/reply_trigger_service.go`。

- 120ms debounce 过滤极短连发。
- 8 秒内客户连续消息合并成一个批次。
- 批次内保留消息类型：文本、图片、语音、文件、定位、小程序、表情。
- 媒体理解结果作为上下文保留，不改变网页端原消息展示。
- 如果文本明显追问刚才媒体，而媒体 900ms 内没理解完成，则先不抢答；媒体理解完成后唤醒最新文本追问。

### 2.2 Reply Engine Core

位置：`internal/ai/replyengine`。

这是所有 AI 回答和 AI 动作的统一规则入口。它不访问数据库、不发消息，只输出判定结果，因此可被 executor、默认资源动作、graph 工具和发送前 guard 共同调用，避免规则散落。

核心输出：

- `Questions`：当前批次问题清单。
- `Intent`：每个问题的意图标签。
- `Instruction`：给模型的本轮硬规则。
- `AllowHandoffTool`：本轮是否允许转人工工具。
- `NeedsMiniProgram / NeedsLocation / NeedsServiceTask`：是否需要小程序、定位、服务动作。

### 2.3 Preflight Layer

位置：`internal/ai/runtime/executor/reply_preflight.go`，规则来源为 `internal/ai/replyengine`。

模型前先生成“客户问题清单”和“意图标签”：

- `FAQ_WIFI`：WiFi、无线网、密码。
- `FAQ_INVOICE`：发票、专票、普票、抬头。
- `FAQ_SUPPLIES`：剃须刀、拖鞋、牙刷、矿泉水、纸巾等。
- `CHECKIN_MINIPROGRAM`：入住、小程序、安心宿。
- `LOCATION_NAVIGATION`：地址、定位、怎么去、导航。
- `HANDOFF_REQUEST`：明确找人工、转人工、真人客服。
- `HUMAN_DECISION`：退款、赔偿、投诉升级、安全、隐私、价格争议。
- `GENERAL_CHAT`：其他普通对话。

Preflight 只给模型规则，不直接硬编码最终话术。它要求模型：多问题逐项覆盖，FAQ 不直接转人工，不重复兜底，不用含糊确认类空话逃避常见问题。反面坏句不写进模型上下文，避免模型学走。

### 2.3.1 Intent-Staged Prompt Runtime

位置：`internal/ai/runtime/executor/intent_pipeline.go`，trace 写入 `AgentRunLog.trace_data.pipeline`。

当前回复引擎从“一个大提示词同时做所有事”改为意图分阶段执行：

```text
Normalize -> IntentDetect -> IntentPromptSelect -> ContextBuild -> Tool/Knowledge -> ReplyPlan -> Generate -> Validate -> Commit
```

关键约束：

- 上下文不缩减：近期原始消息、压缩记忆、媒体理解、门店/客户关系继续携带。
- 渐进式披露只作用于提示词和工具策略：先识别意图，再注入当前意图 prompt pack。
- `Generate` 只负责自然表达 `ReplyPlan`，不能重新决定资源发送、人工路由或真实动作。
- `Validate` 记录发送前校验规则；如果后续要重跑，应按失败阶段回退，而不是在最后粗暴改写坏回复。

已落地 trace 字段：

- `pipeline.normalize`：当前文本、消息类型、历史数量、压缩记忆来源、是否有媒体上下文。
- `pipeline.intent`：`primaryIntent / secondaryIntents / shouldReply / needsKnowledge / needsResource / needsHumanRoute / reason`。
- `pipeline.promptSelect`：当前意图加载的 prompt pack 名称和专项规则。
- `pipeline.contextBuild`：完整上下文分层和优先级：当前问题 > 最近原始上下文 > 媒体理解 > 压缩记忆 > 当前意图资源/知识库。
- `pipeline.toolKnowledge`：本意图是否需要知识、工具、门店变量或人工路由。
- `pipeline.replyPlan`：目标、依据、禁止事项、语气长度。
- `pipeline.generate`：模型生成阶段状态；no-reply/fallback/模型生成会分别记录原因。
- `pipeline.validate`：发送前校验状态和规则。

当前意图集合：

- `hotel_faq`：WiFi、发票、停车、早餐、用品、洗衣、投屏等门店知识问题。
- `checkin_miniprogram`：自助入住、小程序入口。
- `location`：位置、导航、地址。
- `phone`：电话披露。
- `media_question`：图片/语音/文件理解后的追问，结合完整上下文回答，不复述 OCR。
- `service_request`：用品、维修、行李等服务请求，优先知识库自助引导，不空口承诺执行。
- `handoff / complaint`：明确人工、投诉、退款、赔偿、价格争议、安全隐私等，按托管模式和排班策略路由。
- `thanks / confirm / social`：轻互动、确认、感谢，短而自然，不只回“哈哈”。
- `no_reply_media_only`：普通图片/语音/文件只有理解上下文、无明确诉求时不主动回复。
- `unknown`：不确定时短答或追问一个关键点，不兜底套话。

媒体规则：

- 普通自拍、食物图、环境图：媒体理解写入上下文，不主动回复。
- 图片/截图里含“打不开、报错、怎么处理、求助、投诉、退款、连不上”等明确诉求：进入意图阶段并回复。
- 图片后续文本追问：以文本问题为主意图；若是“这是啥菜 / 我吃得怎么样 / 你看一下”这类追问，标记为 `media_question`，仍携带完整近期上下文和压缩记忆。
- 图片后换话题问 WiFi/发票/定位/电话：业务意图优先，不被图片带偏。

### 2.4 Knowledge Layer

知识库只回答有依据的门店事实。未命中时不能直接套“记录一下”，也不能把固定 fallback 文案作为示例塞进提示词；要看意图：

- 连续多条消息必须按问题拆开分别检索，再合并上下文。不能把“能开专票不 + WiFi 是哪个”整段只检索一次，否则发票命中会盖掉 WiFi 知识。
- FAQ 信息不足：追问一个关键字段。
- WiFi 没配置：问房号或提示查看房间内 WiFi 牌，不能编密码。
- 发票流程有知识：直接短答；没有知识：说明可开电子票并引导小程序/追问订单信息，不能乱承诺开具结果。
- 送水、拖鞋、牙刷、纸巾、剃须刀、矿泉水等低风险用品请求，优先走知识库回答自取位置、前台领取方式或门店规则；不要默认说门店帮忙送/拿。
- 如果知识库没有用品处理规则，不由 AI 自行决定“给电话”或“转人工”，而是进入现有接待路由决策：全托管走总部网页端客服，半托管按当前时间段在总部网页端客服和门店群通知之间切换，非托管只通知门店群；只有路由策略要求门店自行处理且存在门店联系方式时，才可回复电话信息。
- 超出酒店客服范围：自然说明处理不了该类内容，不自动转人工。
- 知识检索失败或检索器不可用：仍继续走 Reply Runtime Engine，注入“不可编造、可追问一个关键字段、可调用小程序/定位工具”的策略，不允许提前短路成固定话术。

### 2.5 Risk Layer

转人工必须收紧：

- 允许转人工：明确要求人工；退款/赔偿/投诉升级/安全风险/严重订单异常/隐私授权/价格争议。
- 不允许转人工：普通 FAQ、WiFi、发票流程、用品、电视、入住、小程序、定位、轻互动、普通文件咨询。
- 低风险用品和门店规则问题不诱导转人工；先查知识库，知识库无解时交给既有托管模式/时间段路由，不在 Reply Engine 里新增分类或硬编码电话策略。
- 非酒店范围文档或问题：先说明“这个不是酒店服务范围，我这边处理不了”，不要莫名触发非服务时间转人工提示。

## 3. 当前已落地

- 连续消息合并保留媒体理解结果。
- 媒体理解完成后唤醒最新文本追问，解决“先图后问”乱答。
- Reply Engine 已抽成 `internal/ai/replyengine`，executor、默认资源动作、graph 转人工风险共用同一套意图规则。
- Preflight 生成问题清单和意图标签，要求多问题逐项覆盖。
- 转人工图分析收紧，不再因为普通“客服”字样或文档类问题自动转人工。
- 知识层已删除默认固定 fallback 文案来源，不再把“确认/记录/同事跟进”类句式注入模型上下文。
- 发送前文本改写 guard 已移除：回复正确性必须由 Reply Runtime Engine、知识检索和接待路由在模型生成前约束；模型生成后的链路只负责提交真实回复，不负责“拦截后改句子”。
- 轻互动提示词改为结合上下文回复，不只回“哈哈/好的/嗯嗯”。

## 4. 2026-06 历史待办（非当前统一集成完成判定）

以下条目记录当时的产品优化方向，不表示当前统一分支仍缺少同名基础设施。
是否继续实施必须先核对当前代码、统一集成方案和真实运行证据，不能据此恢复旧模型或
旧知识链。

- 账号/门店配置里补 WiFi 名称、密码、发票默认流程、用品领取点等结构化字段。
- RunLog 中记录 Score 各项分值，用于后续 10 轮 × 20 场景自动压测。
- 增加重复回复检测：同一会话同一批次相似回复只允许发一次。
- 用真实历史对话集跑回放，输出错例、扣分项和下一轮规则修正。

## 5. 历史演进记录（2026-06 至 2026-07-15，仅追溯）

### 2026-06-30 模型接口模式、缓存与上下文窗口

模型配置新增 `apiMode`，用于区分上游调用协议：

- `chat_completions`：默认模式，走 OpenAI-compatible `/chat/completions`。千问兼容模式、DeepSeek、大多数 OpenAI-compatible 服务默认使用这个模式。
- `responses`：为 OpenAI/火山方舟 Responses API 预留。火山方舟同时提供 ChatCompletions 与 Responses 能力，Responses 可通过 `previous_response_id` 承接上下文；但当前 runtime 仍基于 Eino ChatModelAgent，尚未接入 `/responses` adapter。若模型配置选择 responses，后端必须明确报错，不允许静默降级成 chat 假装已命中缓存。
- 火山方舟另有上下文缓存对话接口 `/api/v3/context/chat/completions`，其缓存命中应通过 provider 返回的 `usage.prompt_tokens_details.cached_tokens` 验证。后续可扩展第三种 `ark_context_chat`，不要和普通 `response_format` 混为一谈。

`response_format` 只约束输出结构，不等于缓存命中。缓存命中依赖 provider 对稳定 prompt 前缀的缓存策略，或 Responses/Ark Context Chat 这类上下文续接能力返回的真实 usage。运行日志后续必须记录 `promptTokens / completionTokens / cachedTokens`，不能只用字符数估算。

上下文窗口调整为“最近约 15 条消息 + 压缩记忆”：

- 默认 `ContextMaxMessages` 从 30 调整为 15，减少每轮输入 token。
- 15 条之外的本 session 旧消息不丢弃，原文仍完整保存在 `t_message`。
- runtime 构建模型输入时，优先读取 `ConversationSessionSummary` 作为系统记忆；没有正式摘要时，用窗口外最近旧消息生成短 digest，避免突然丢前情。
- 压缩记忆只作为承接上下文的事实参考，不能把未完成动作描述成“已安排/已通知/已处理”。
- 超过窗口后应由 `ConversationContextCompressionWorker` 异步生成更稳定摘要，摘要字段包括稳定事实、未解决事项、客人偏好和媒体理解结果。

### 2026-06-30 真实模型评估后的规则修正

- WiFi/网络连不上不是“AI 帮忙排查”的承诺场景。若知识库有 WiFi/网络处理规则，按知识答；若缺少房号或门店配置，只追问房号或提示查看房间 WiFi 牌。运行时不提供“我帮你排查/处理网络”这个允许动作，从源头避免假承诺。
- 行李协助首问按门店规则回答，例如智能酒店不提供行李搬运时可直接说明；若客户持续追问、明确找人工或出现投诉/强需求，再进入接待路由。不要首问就承诺帮拿，也不要把所有行李问题一律转人工。
- 优化方式分三层：Reply Runtime 先做意图与动作边界；知识库提供事实；模型只负责自然表达。禁止靠一整段长提示词或生成后拦截替代规则引擎。

### 2026-06-30 优化方式说明：不是堆提示词

本轮真实豆包评估后明确：单靠模型提示词无法稳定阻止“我帮你排查网络问题”这类假动作。因此优化必须分层：

1. Reply Runtime Engine 做结构化判断：先判定 WiFi/网络、行李首问、行李持续追问、人工风险等意图。
2. 知识库提供事实：例如南七店不提供早餐、剃须刀位置、入口路线、停车路线。
3. 模型只负责自然表达：模型不决定是否真的派人、是否真的排查网络。
4. 出站前不再改写模型文本：若 WiFi/网络问题仍出现“我帮你排查/处理网络”，视为上游 Reply Runtime 决策或知识检索约束缺陷，必须回到 preflight/knowledge/action route 修复。
5. 行李策略：首问按门店规则答，不默认转人工；客户持续追问、明确找人工或升级时，再进入接待路由。

这意味着提示词只用于表达边界，不能替代规则引擎；模型不能决定真实世界动作是否发生。

### 2026-06-30 测试口径纠偏

此前 `docs/generated/real-llm-5x30-hotel-eval.*` 的 150 轮记录只证明“真实调用了大模型”，不是生产链路验收。它使用 Excel 本地材料和独立脚本检索，不等于当前会话 `来一杯生椰拿铁` 绑定的账号 Agent、豆包模型配置和 FastGPT 云端知识库。

后续凡标记为“真实链路测试”，必须满足：

1. 以目标会话或员工号账号为入口，读取其绑定的 `aiAgentId`、`aiConfigId`、`knowledgeIds`。
2. 调用应用内 runtime/debug 接口或 service，而不是外置脚本直接拼 prompt 调模型。
3. 验证 `t_knowledge_retrieve_log` / trace 中确实记录了当前 Agent 绑定的云端知识库检索。
4. 报告里必须写明会话 ID、Agent、模型配置、知识库 ID、检索命中数和耗时，不能用其他门店材料替代。

### 2026-06-30 豆包-2.0-lite 真实 runtime 复测结论

测试入口固定为当前生产链路：`conversationId=7`、`AIAgent=3（吴朝伟 - 独立配置）`、`AIConfig=6（豆包-2.0-lite）`，通过 `applicationruntime.NewService().Run(...)` 读取数据库绑定关系后执行，不再使用独立 prompt 脚本模拟。

权限打开后第一轮 30 条真实 runtime 结果：

- 总消耗：`154352 total tokens`，其中 `116600 cached tokens`，缓存命中约 `75.5%`。
- 上下文窗口仍是旧实例配置时 `history=27`，后已把测试实例调整为 `contextMaxMessages=15 / contextMaxTokens=6000`。
- 调整后 30 条复测：总消耗降到 `109358 total tokens`，其中 `79288 cached tokens`，缓存命中约 `72.5%`；输入历史降为 `history=16 + message_digest/11`。
- 定位、小程序、电话、明确转人工、人类判断、感谢/问候等确定性路径已不再调用模型，耗时通常 2-6ms，token 为 0。
- 第二次复测第 29 条后火山方舟返回 `429 Too Many Requests`，提示账号 `2127985032` 的 `doubao-seed-2-0-mini` 达到推理限额并进入 Safe Experience Mode 暂停。随后所有需要 ChatModel 的问题都会失败，但确定性 runtime 路径仍可正常回复。这属于上游模型账号限制，不是 AgentDesk runtime 逻辑错误。

本轮发现并修正的根因：

- 账号 Agent 旧系统提示词包含“我确认一下再回复你”“我帮你看下/反馈一下”“通知处理”等诱导模型假承诺的表达，已在测试账号上改为短提示：模型不得承诺真实动作，电话/定位/小程序只用账号绑定资料，未配置则明确缺资料。
- `CONTACT_PHONE` 独立成意图，不再混在 `FAQ_HOTEL` 里让模型编“无法提供公开电话”。门店电话必须来自账号/门店配置；当前测试实例未配置电话，所以确定性回复“这家门店还没配置联系电话，我这边不能乱给号码”。
- `LOCATION_NAVIGATION` 和 `CHECKIN_MINIPROGRAM` 在知识检索前走账号资源。当前测试实例已有经纬度和安心宿小程序 payload，因此可确定性回复，不再出现“我确认下定位”。后续接协议发送动作时，必须以工具结果确认“已发送”，不能只用文字冒充。
- `HANDOFF_REQUEST / HUMAN_DECISION` 不再放给模型空回复，直接进入“回复确认后转人工”的确定性确认话术。
- `GENERAL_CHAT` 中的问候/感谢直接用短句处理，不再耗费大模型，也不再出现 emoji、波浪号或固定“哈哈”。
- 叫醒、电视/投屏、空调/维修等真实动作类事项不能让模型说“已设置/已安排/联系管家”。当前策略为：需要同事接一下时，让客人回复“确认”后进入接待路由。

仍需继续治理的点：

- WiFi、停车、早餐、用品等知识类问题仍坚持“知识库优先”，不硬编码成 runtime 固定答案。若知识库命中错域（例如回复公司品牌/加盟范围），应清理该账号绑定知识库或在检索层增加门店域过滤，而不是把所有 FAQ 前置写死。
- 用品自取、停车路线、发票信息等应来自门店知识库；runtime 只负责禁止假动作和确定性资源路由。
- 火山方舟豆包模型需要关闭/调整 Safe Experience Mode 或提高推理限额，否则任何需要模型表达的知识类问题都会 429。

### 2026-06-30 Responses API 夜间评估与优化策略

已验证 `GPT-5.5 多模态模型` 的 OpenAI-compatible `/responses` 接口可用，HTTP 200，且返回 `usage.input_tokens_details.cached_tokens`。因此模型配置 `apiMode=responses` 不再只是 UI 字段，runtime 已新增 Responses ChatModel 适配器：

- 位置：`internal/ai/runtime/internal/impl/factory/responses_chat_model.go`。
- 适配方式：对 Eino runtime 暴露 `ToolCallingChatModel`，底层调用 `{baseUrl}/responses`。
- 输入映射：system 消息合并为 `instructions`，user/assistant/tool 历史压成 `input`。
- usage 映射：`input_tokens -> promptTokens`，`output_tokens -> completionTokens`，`input_tokens_details.cached_tokens -> cachedPromptTokens`。
- 当前限制：第一版不接工具调用和 stream；动作类、资源类、转人工类已由 Reply Runtime Engine 前置处理。后续要吃到更稳定的连续上下文缓存，需要把 provider 返回的 `response.id` 作为 `previous_response_id` 持久化到会话/运行日志中。

本轮真实生椰拿铁链路 Responses 3×30 评估：

- 会话：`7 / 来一杯生椰拿铁`。
- Agent：`3 / 吴朝伟 - 独立配置`。
- 模型配置：`3 / GPT-5.5 多模态模型 / apiMode=responses`。
- 总轮数：90。
- 错误数：0。
- 平均耗时：约 4.63s；P90 约 11.0s；最大 30.3s。
- Token：prompt 247405，completion 5771，total 253176，cached 56576，prompt 缓存命中约 22.9%。
- 结论：Responses 能返回缓存命中，但仅切接口不能保证高命中；必须进一步接 `previous_response_id` 或 provider 上下文会话能力。

本轮直接优化：

- `电视投屏 / 投屏 / 空调显示 / 空调故障 / E3 / 故障` 优先归类为 `SERVICE_TASK`，避免先落入 `FAQ_HOTEL` 后进模型长思考。
- `确认确认 / 嗯可以 / 确认 / 确定` 作为确认模糊词，由 runtime 秒级回复，不再调用模型。
- `表情理解 / 不耐烦 / 咋还没回复` 由 runtime 轻互动处理，不再进模型慢跑。

夜间自动任务已创建：每小时在本线程唤醒一次，直到 2026-07-01 07:30 前，每次执行 3×30 Responses 真实 runtime 评估，记录报告并根据问题自主优化 Reply Runtime Engine。优化优先级：

1. 动作/资源/确认类尽量前置 runtime，减少模型调用。
2. 高频固定 FAQ（WiFi、用品、洗衣、退房、停车、早餐）逐步做结构化直答或知识命中缓存。
3. Responses adapter 下一步接 `previous_response_id`，提升连续测试缓存命中。
4. 对低分/慢回复，只回到规则、检索、上下文和资源路由修；不做生成后坏句拦截。

#### 2026-06-30 16:03-16:20 心跳评估结果

本次按夜间自动任务要求，继续走真实内部链路，不向企微真实发送消息：

- 会话：`conversationId=7`，客户 `来一杯生椰拿铁`。
- Agent：`AIAgent=3`，知识库 `1,2`。
- 模型：`AIConfig=3`，`gpt-5.5`，`apiMode=responses`。
- 报告文件：
  - `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-160312.md`
  - `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-161125.md`
  - `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-162000.md`

三次 3×30 评估对比：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `160312` | 87/90 | 0 | 2406ms | 6335ms | 18195ms | 158007 | 68096 | 44.0% |
| `161125` | 90/90 | 0 | 2564ms | 7127ms | 22294ms | 172739 | 123136 | 72.7% |
| `162000` | 90/90 | 0 | 2100ms | 7031ms | 14107ms | 132716 | 93696 | 72.2% |

本次修复的两个根因：

1. **混合多问不能被单个确定性 fast path 抢答。** 示例：`wifi和停车都发我一下 + 房间没纸巾`。之前用品 fast path 直接返回“纸巾在1020对面的洗衣房，可以自取”，导致 WiFi/停车漏答；现在只要同一批次存在 `FAQ_WIFI / FAQ_HOTEL / FAQ_INVOICE` 这类知识问题，就不允许用品 fast path 独占回复，必须拆问题分别检索并合并上下文，再由模型一次性自然表达。修复后混合多问准确性恢复 100 分，但仍属于模型路径，12-14s 的尾延迟留作下一步优化。
2. **媒体追问没有理解结果时不应进模型慢跑。** 示例：`刚才发的语音你听懂了吗`、`刚发的图片里是什么`。如果历史窗口/摘要里没有 `图片内容是 / 语音内容是 / 媒体理解` 等可用结果，runtime 直接短句要求补发或打字，不调用知识库和模型；如果已有媒体摘要，则继续把摘要交给模型结合上下文回答。修复后语音/图片追问从 8-22s、约 4k token 降到 2ms、0 token。

已验证：

- `go test ./internal/ai/replyengine ./internal/ai/runtime/executor -count=1` 通过。
- `pnpm --dir web typecheck` 通过。
- `docker compose build agent-desk && docker compose up -d agent-desk` 已执行，服务健康，未清空数据卷。

下一步继续优化方向：

- 多 FAQ 合成路径仍需要模型，尾延迟高。后续可把高置信知识命中转为结构化短答合成：WiFi、停车、发票、纸巾这类事实都命中时，由 runtime 直接拼成一条自然短回复，避免 12s 以上尾延迟。
- Responses adapter 仍未持久化 `response.id` 并复用 `previous_response_id`，缓存命中主要来自 provider 前缀缓存；要进一步降低 token 和延迟，需要接续会话级 response id。

#### 2026-06-30 16:48-17:03 心跳评估结果

本次继续按夜间任务走真实内部链路，仍不向企微真实发送消息：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-164840.md`
- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-170315.md`

本次评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `164840` | 90/90 | 0 | 1966ms | 7249ms | 14714ms | 132648 | 83200 | 64.1% |
| `170315` | 90/90 | 0 | 2380ms | 6385ms | 19420ms | 133150 | 93952 | 72.4% |

本轮尝试并保留的优化：

- 新增高置信多 FAQ 合成器：当同一批次拆出多个 FAQ，且每个子问题都能从知识检索结果中找到可用片段时，runtime 可直接合成短回复，避免再调用模型。例如“发票 + WiFi”可在命中充分时走 0 token 路径。
- 合成器必须跳过错域/转人工/处理类片段，例如“公司介绍模式”“主要回答公司、品牌、展厅、加盟和AI方案”“联系前台/管家/工作人员协助处理”。这些片段不能被当作门店 WiFi/停车/用品事实。
- 如果任一子问题缺少安全命中，必须回退到模型+知识上下文路径，保证准确覆盖优先于速度。

本轮发现的知识治理问题：

- `混合多问：wifi和停车都发我一下 + 房间没纸巾` 的检索结果里出现了明显错域片段：`公司介绍模式/品牌/展厅/加盟/AI方案`，它不属于生椰拿铁门店客服知识，却会在高置信合成前排到停车相关片段前面。
- 为避免“快但错”，runtime 已过滤这些错域片段；过滤后若没有稳定 WiFi+停车+用品三项命中，就回退给模型。最终准确性保持通过，但混合多问仍有 15-19s 尾延迟。
- 后续需要在知识库侧治理：把公司介绍/招商/品牌展厅类知识从该门店客服 Agent 的知识库中拆出，或在检索层增加门店域过滤，确保门店服务问题只命中门店服务资料。

本轮结论：

- 媒体追问、资源动作、确认、用品、人工确认等确定性路径继续保持 0 token 毫秒级。
- 多 FAQ 合成可以降 token 和延迟，但不能牺牲覆盖率。现阶段先保留安全合成器和错域过滤；缺命中时宁可回模型，不输出半截答案。

#### 2026-06-30 17:48 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-174827.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `174827` | 90/90 | 0 | 2042ms | 6557ms | 19914ms | 132584 | 81920 | 63.1% |

本轮结论：

- 确定性路径稳定：用品、电话、定位、小程序、明确人工、确认模糊词、语音/图片无理解结果追问继续保持 0 token 毫秒级。
- `混合多问` 三次均准确覆盖 WiFi、停车、纸巾，但耗时分别约 18.2s、19.9s、9.5s。原因是安全合成器没有拿到足够稳定的安全片段组合，按“准优先”策略回退模型。
- 这不是本轮应继续硬编码 runtime 的问题。若为了速度强行合成，容易重新引入上一轮发现的错域片段，如“公司介绍模式/品牌/展厅/加盟/AI方案”。

仍需知识治理：

- 将公司介绍、招商、品牌展厅、AI 方案等资料从生椰拿铁门店客服知识库中拆出，或给检索层加门店服务域过滤。
- 将 WiFi、停车、发票、纸巾等高频门店事实整理成结构化 FAQ，确保每个子问题能稳定命中一条可合成的门店服务片段。

#### 2026-06-30 18:50 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-185003.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `185003` | 90/90 | 0 | 2128ms | 6716ms | 22531ms | 133162 | 89088 | 68.6% |

本轮观察：

- `混合多问` 三次均准确覆盖 WiFi、停车、纸巾，但都因为模型路径尾延迟被扣分。回复内容没有假动作，也没有漏项。
- `发票` 和 `能力边界` 偶发慢，属于单模型调用尾延迟；当前没有发现规则误判或假承诺。
- 确定性路径继续稳定：用品、定位、小程序、电话、人工确认、语音/图片缺理解结果追问仍是毫秒级 0 token。

本轮未继续修改 runtime，原因：

- 高置信合成器已经具备安全过滤。如果强行绕过知识命中，把 WiFi/停车等内容硬编码到 runtime，会破坏“门店知识必须来自知识库/账号配置”的边界。
- 当前剩余慢点更适合通过知识治理和 Responses `previous_response_id` 续接优化解决，而不是继续堆规则。

下一步优先级保持：

1. 清理门店知识库错域内容，尤其是公司介绍、招商、品牌展厅、AI 方案等非门店服务资料。
2. 将高频 FAQ 做成结构化门店事实，供高置信合成器稳定命中。
3. Responses adapter 持久化并复用 `response.id / previous_response_id`，降低模型路径尾延迟和 token。

#### 2026-06-30 19:50 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-194957.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `194957` | 90/90 | 0 | 2278ms | 6163ms | 25880ms | 132830 | 53760 | 41.4% |

本轮观察：

- 准确性稳定：90 条全部通过，没有空回复、假动作承诺或无端转人工。
- 慢点集中在模型路径：`发票` 最大 25.9s，`混合多问` 15-20s，`能力边界` 一次 8s+。
- `混合多问` 内容已覆盖 WiFi、停车、纸巾；慢是因为安全合成器没有稳定拿到可合成的门店片段，继续回退模型。
- 本轮缓存命中率降到 41.4%，说明仅依赖 provider 前缀缓存不稳定；要进一步降 token/延迟，需要实现 Responses `previous_response_id` 续接。

本轮未修改 runtime，原因：

- 当前剩余问题不是规则误判，而是知识命中质量和模型路径尾延迟。
- 继续增加硬编码规则会让门店知识边界变脆，容易把错域资料或固定答案带进回复。

#### 2026-06-30 22:51 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-225113.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `225113` | 86/90 | 4 | 3603ms | 8498ms | 61418ms | 114941 | 59904 | 53.6% |

本轮异常：

- 出现 4 个上游 Responses 调用错误，均来自 `http://43.128.146.66:8085/v1/responses`：`context deadline exceeded` 或 `EOF`。
- 错误场景为连续多问、早餐、停车等需要模型表达的路径；确定性路径不受影响。
- 成功返回的模型回复没有发现新的规则误判：WiFi/网络不假承诺排查，混合多问覆盖 WiFi、停车、纸巾，人工/动作类路径仍安全。

本轮结论：

- 这不是 Reply Runtime Engine 规则错误，而是 GPT-5.5 Responses 上游可用性波动。
- 后续应增加模型调用层 retry/backoff、错误降级策略，以及 Responses `previous_response_id` 续接；否则高峰时段 timeout/EOF 会直接拉低通过率。
- 业务规则本轮不应继续硬编码；确定性路径已经在保护动作安全和低延迟。

#### 2026-06-30 20:50 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-205020.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `205020` | 90/90 | 0 | 2136ms | 6713ms | 19427ms | 133083 | 57600 | 44.4% |

本轮观察：

- 准确性继续稳定：90 条全部通过，无空回复、无假动作、无无端转人工。
- 慢点仍集中在模型路径：`混合多问` 11.7-19.4s，`连续多问` 一次 12.8s，`网络故障` 一次 9.8s。
- `混合多问` 和 `连续多问` 的内容均覆盖目标意图，说明 runtime 的覆盖规则有效；扣分来自延迟。
- 缓存命中率仍波动，本轮为 44.4%。这再次证明仅靠 provider 前缀缓存不稳定，不能作为长期性能方案。

本轮未修改 runtime，原因：

- 剩余慢点不是规则错误。若继续把 WiFi、停车、发票等内容写死到 runtime，会偏离“门店事实来自知识库/账号配置”的原则。
- 下一步应做 Responses `previous_response_id` 续接，或做结构化门店 FAQ 后再启用更强的高置信合成。

#### 2026-06-30 21:49 心跳评估结果

本次继续走真实内部链路：`conversationId=7`，`AIAgent=3`，知识库 `1,2`，`AIConfig=3`，`gpt-5.5`，`apiMode=responses`，不向企微真实发送消息。

报告文件：

- `docs/generated/internal-runtime-shengye-gpt-5.5-responses-3x30-20260630-214928.md`

评估摘要：

| 时间戳 | 通过 | 错误 | 平均耗时 | P90 | 最大耗时 | total token | cached token | 缓存命中 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `214928` | 90/90 | 0 | 2169ms | 6251ms | 24423ms | 132970 | 68608 | 52.8% |

本轮观察：

- 准确性继续稳定：90 条全部通过，无空回复、无假动作、无无端转人工。
- 慢点仍是模型路径尾延迟：`混合多问` 13-24s，`退房` 一次 17.5s，`网络故障` 两次 8-10s。
- 慢回复内容都符合策略：WiFi 不承诺排查，只给 WiFi 信息或追问房号；混合多问覆盖 WiFi、停车、纸巾；退房使用门店电话信息。
- 缓存命中率本轮为 52.8%，仍然波动。

本轮未修改 runtime，原因：

- 没有出现新的规则误判；继续硬编码会牺牲门店知识边界。
- 剩余提速应优先依赖知识治理、结构化 FAQ 和 Responses `previous_response_id`。

### 2026-07-15 多租户运行时边界

AI/计费分支合入租户主线后，回复 Runtime 不再只依赖全局主键唯一性保证数据正确，以下链路已统一使用会话或任务持久化的 TenantID：

- Intent scope 和门店变量：按 `conversation_id + tenant_id` 读取 RouteState，再按 `instance/store/company id + tenant_id` 读取企微员工号、门店和公司。
- 历史与记忆：历史 Message、当前 session、RouteState 和 ConversationSessionSummary 全部带 TenantID；新摘要写入 TenantID，更新使用 `id + tenant_id`。空闲扫描按 tenant/conversation/session 分组并同租户关联摘要。
- 知识检索：KnowledgeRetriever 在调用 RAG 前按 `AIAgent.TenantID` 过滤 KnowledgeIDs；检索策略和 AnswerMode 只读取同租户 KnowledgeBase。合法配置的召回和排序语义不变。
- 知识图片：资源组、资源项和 Asset 必须与会话企微员工号属于同一 TenantID；最终 Commit 再次按会话租户读取实例和 Asset，脏 trace 不能跨租户发送图片。
- 异步任务：FastGPT 数据集任务和人工超时 AI 恢复任务从自身记录读取 TenantID，所有父对象读取和状态更新都使用该租户。媒体理解先从全局唯一 Message ID 解析持久化租户，之后更新、会话读取和 AI 触发均带租户。
- 用量证据：AIUsageEvent 从显式 TenantID、Conversation、Message、KnowledgeBase、企微员工号、Store 和 Company 合并租户证据；任一证据冲突则拒绝落库。FastGPT 独立 token 的平台聚合记录是唯一允许 TenantID=0 的用量类型。

这些改动只收紧数据读取和持久化边界，不改变意图判断、模型供应商、token 统计、计费口径、回复文案或转人工状态机。对应回归覆盖错误租户知识图片、Agent 跨租户 KnowledgeIDs、摘要跨租户消息和原有 Runtime 全包。
