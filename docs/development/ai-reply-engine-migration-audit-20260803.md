# AgentDesk AI 回复引擎迁移审计（2026-08-03）

## 1. 基线与审计方法

- 当前统一项目基线：`codex/tenant-ai-unified-integration@7c8ae19`。
- 成熟 AI 来源：`origin/codex/ai-billing@4db799363040a4478a5585e101d119de11a26f8e`。
- 开始前执行 `git fetch origin --prune`，未整体 merge 或 cherry-pick 来源分支。
- 审计从企微客户消息入站开始，逐层追踪 Message、Session、Memory、Intent、Knowledge、
  Plan、Generate、Validate、Commit、Outbox、企微发送、人工接待和运行日志。
- 对 `internal/ai` 的来源树逐文件比较内容，而不是只比较文件名或函数名。

来源树共 168 个文件：80 个与当前工作树逐字节一致，49 个保留成熟行为并做 Tenant、Store、
Binding、统一模型或 FastGPT 适配，39 个未保留。只统计非测试代码时为 126 个来源文件：
58 个逐字节一致、35 个适配、33 个未保留。

39 个未保留文件均有明确退役原因：旧 AIConfig/ModelUsage 适配、本地 RAG chunk/index、
本地 rerank/Qdrant、旧 skills 子系统。当前项目用九槽 ModelCallResolver、统一 Usage、托管
FastGPT 和 Runtime 内置 Tool/Graph 替代这些能力；恢复它们会产生第二套模型、知识或技能
入口，不属于“完整迁移”。`ai_config_adapter.go` 缺失也是正确退役，不是漏文件。

## 2. 成熟来源的真实运行链

```text
MessageService 提交客户消息
  -> AIReplyService 触发与短防抖
  -> 会话可回复性 / 人工状态 / Interrupt 检查
  -> History + ConversationMemory
  -> Normalize
  -> IntentDetect（行业 Prompt + 严格 JSON）
  -> IntentPromptSelect
  -> ContextBuild（当前消息、近期原文、媒体、记忆、门店事实、标签）
  -> Knowledge Retrieve / Rerank / Answerability
  -> ReplyPlan / Tool / Resource / HumanRoute
  -> Generate
  -> Validate（依据、动作账本、承诺和输出结构）
  -> Commit（稳定 ClientMsgID）
  -> Message + Event + Outbox
  -> 企微协议发送 / 回执 / Outbox 重试
  -> AgentRunLog / Usage / Evolution / WebSocket
```

多轮上下文、Intent JSON、五类顶层意图、多任务拆分、知识注入、工具/Graph、Interrupt、
生成校验、结构化动作账本和稳定 Commit 均已存在于当前项目。大量核心文件保持逐字节一致，
包括 `intent_pipeline.go`、`generated_reply_validator.go`、`event_consumer.go`、
`knowledge_guard.go`、Graph、Instruction、Tool Factory、Checkpoint 和 Agent 主体。

## 3. 修复前当前项目的真实差异

当前项目并非缺少整套 Runtime；成熟主链已被吸收。确认的实际缺口是：

1. 客户 Message 提交后依赖裸 goroutine 触发，进程退出会丢回复任务。
2. 媒体理解完成后还有第二条异步 AI 入口，可能与文本任务竞争或重复执行。
3. Conversation 与 Route 虽能解析模型，但没有强制 StoreStaffBinding 和当前实例完全一致。
4. Reply 与 IntentDetect 的 Usage 归因没有统一写入 StoreStaffBindingID。
5. 部分企微入站消息 RequestID 为空，降低幂等、日志和调用归因的可追踪性。
6. FastGPT Usage SQL 使用未转义的 `cursor`，与 MySQL 8.4 保留字冲突。

此前线上表现“机械、迟钝”不能据此推导为 Intent/Plan/Generate 主体缺失。已确认的具体原因
包括 IntentDetect 曾被固定 12 秒上限提前取消、瞬时触发可能丢失或竞争、FastGPT 就绪状态
失败关闭，以及首联/联系人资源链在 AI 之外独立发卡。重复小程序的生产证据来自
`wx_contact_welcome` / `arrival_bind_ticket_*`，不是 AI Commit 选择了小程序。

## 4. 当前适配与来源行为的边界

### 正确保留

- Normalize、IntentDetect、PromptSelect、ContextBuild、ReplyPlan、Generate、Validate 阶段顺序。
- 五类 Intent、intentTasks、resourceActions 和多任务输出语义。
- 会话记忆、媒体上下文、Interrupt/Resume、Tool/Graph 和人工路由协作。
- 稳定 AI ClientMsgID、Message/Outbox、运行日志及重复提交防护。

### 正确适配

- AIConfig 改为 `ModelCallResolverService`，按 Tenant + Store + Binding + 九槽 + Credential revision 解析。
- 本地 FAQ/Qdrant 改为当前 Store 的托管 FastGPT Dataset，保留检索、重排、依据与命中审计。
- 知识身份改为 FastGPT `SourceRecordID`，不再保留旧 Document/FAQ/Chunk 身份。
- 人工交接接入现有 `ConversationHumanDispatchService`，不新建第二套人工任务。
- Usage 统一记录 Tenant、Store、Binding、用途槽、Profile/Credential revision 和网关回执。

### 明确不恢复

- AIConfig、Grant、StoreSetting、ConversationTag 和平台共享 Key fallback。
- 本地 KnowledgeDocument/FAQ/Chunk/Qdrant 及旧 rerank 实现。
- 旧 skills 目录、旧 hook bridge、七鱼、独立 Agent 模型配置或第二套 FastGPT 入口。

## 5. 本次实现

### 范围与模型解析

- Conversation、Route 的 TenantID、StoreID、StoreStaffBindingID 必须一致。
- Route 必须指向该 Binding 唯一的当前有效企微实例；停用、删除、替换草稿和历史实例拒绝。
- 任一范围冲突均在 NewAPI/FastGPT 调用前失败。
- Reply、IntentDetect 统一使用 `ModelCallUsageScope()`，补齐 Binding 归因。

### 持久 AIReplyJob

- 新增内部 `AIReplyJob` model、enum、repository 和 service。
- 客户文本、HTML、图片、语音、附件与任务在同一 Message 事务内提交。
- 唯一键：`TenantID + ConversationID + MessageID`；重复消息幂等补任务。
- 状态：`pending、processing、retry、completed、skipped、superseded、expired、failed`。
- 每秒调度、最多 4 并发、90 秒租约、30 秒续租。
- 最多 4 次执行，退避为 15 秒、1 分钟、3 分钟。
- 15 分钟仍无人回复时进入现有人工任务池；范围损坏不基于不可信数据派单。
- 任务不保存正文、Prompt、模型输出、密钥、完整指纹或上游错误原文。

### Runtime 与媒体

- 删除 `TriggerAIReplyAsyncHook` 和媒体理解完成后的第二条 AI 旁路。
- 同步 Hook 返回 `completed、skipped、superseded、deferred` 和受控原因码。
- 模型前及 Commit 前重新检查租约、Conversation、Message、Session、Route、Binding、实例、
  AI 开关和人工状态。
- 媒体理解由任务同步协调；普通无诉求媒体跳过，后续文本追问由文本任务处理。
- 语音转写失败提示使用 `voice_transcription_failed_<messageID>`，重试不重复发送。
- `reply-runtime-eval` 改为执行真实持久任务，并在清理时删除对应任务。

### Schema 与契约

- 新增一个内部表和内部 enum，通过 AutoMigrate 支持 SQLite/MySQL。
- 未新增 DML migration，未注册 generator。
- 未修改公开 DTO、HTTP API、WebSocket payload、Intent JSON 或结构化动作字段。
- 未增加模型调用；只修正 Reply/Intent 的 Binding 用量和计费归因。

## 6. 结构化动作边界

- 普通知识问题：`hotel_info + needsKnowledge`，资源动作列表必须为空，不发送小程序。
- 当前酒店位置：`hotel_variable + provide_location`，Commit 只构造 `location`。
- 入住小程序：只有明确 `provide_mini_program` 任务才由 AI Commit 构造小程序。
- 首联资源：只在真实创建 Conversation 时进入独立资源编排；已有联系人同步变化不重发。
- 静态到店连接：首次绑定票据替代普通欢迎小程序，使用 `arrival_bind_ticket_<ticketID>` 幂等。
- 真实再次扫码：继续走 `ArrivalScanEvent`、`arrival_scan_<eventID>` 和既有冷却/频控，不经过 AI Intent。
- 定位、小程序、欢迎资源、绑定票据和再次扫码互不作为失败 fallback。

## 7. 测试证据

新增或强化测试覆盖：

- 消息与任务事务回滚、唯一键、重复消息补任务、最近 15 分钟补偿。
- 双 Worker CAS Claim、租约续期、过期回收、三段退避、第四次转人工。
- 进程退出后任务仍在、已有 Commit/RunLog 收敛、Outbox 失败不重跑模型。
- 新消息覆盖、Session 变化、关闭、撤回、AI 停用、人工接管和 15 分钟过期。
- 媒体单一路径、普通媒体跳过、语音失败提示幂等。
- Route/Conversation/实例跨租户、跨门店、错 Binding、停用或替换时上游调用为零。
- Reply、Intent、媒体及辅助模型的 Usage Event/Gateway Call 带正确 Binding。
- WiFi/设备知识问题不请求门店资源；位置问题只准备 location，即使实例已配置小程序。
- 首次绑定卡只发一次，旧联系人变化不重发，真实再次扫码仍可投递。
- SQLite AIReplyJob AutoMigrate、MySQL 契约（配置 `TEST_MYSQL_DSN` 时执行）和 Tenant Integrity Audit。

## 8. 当前交付与生产验证（2026-08-07）

- 本文第 5 节所述持久任务、受控错误、槽内重试、人工兜底和原子提交已经进入
  `weibao/main`，生产环境当前运行镜像为 `mlogclub/agent-desk:0d1f0eb`。其中
  `4648558` 固化九槽显式重试值，`0d1f0eb` 修正 Store 自有运行资源的 readiness 归属。
- 本次补强没有再修改生产 Runtime；新增的代码变更仅为 HTTP 级重试次数测试，以及
  文本加定位消息的 Message/Outbox 原子回滚测试。测试确认 `MaxRetryCount=2` 时，NewAPI 5xx
  与空输出均恰好执行首次调用加两次重试。
- 本地验证已通过定向测试、AI/Service 范围 race、`go test ./... -count=1`、`go vet ./...`
  和 `pnpm typecheck`。生产 Tenant Integrity Audit 覆盖 98 个注册模型、114 张必需表和
  287 个关系，结果为 0 violation。
- 高铁南站店和合肥南七均已生效九槽模板 r3。合肥南七先于 2026-08-07 12:12:32 完成一次
  r3 九槽真实测试，耗时 15,692 ms；随后通过受保护的凭据激活服务完成最终切换，连接验证于
  13:35:59 通过，FastGPT 于 13:36:02 就绪，本次启用槽测试耗时 15,237 ms。
- 生产数据库只读复核确认两店均为 `template_id=3 / template_revision=3`，
  `pending_template_id=0 / pending_template_revision=0`，Assignment 为 `ready/ready`；两份凭据
  均为 `active`、模型测试 `passed`、FastGPT `ready`。r3 九槽的 `MaxRetryCount` 全部为 2，
  ASR 仍按本轮边界停用。
- 最近 24 小时没有新的 `AIReplyJob` 或 `AgentRunLog`，因此尚不能声称本次生产配置已经通过
  真实客户入站、FastGPT、DeepSeek、结构化动作和企微发送的完整端到端验收。
- 高铁南站店 readiness 当前为 15/17，定位资源通过；缺少权威电话和小程序 payload。现有
  Store 与实例历史中没有可安全复用的值，不复制其他门店配置，也不虚构业务资源。
- 没有 `TEST_MYSQL_DSN` 时 MySQL 实例级 Schema 测试会跳过；SQLite 和模型契约仍执行。
  ASR 渠道可用性不属于本轮，语音槽停用时继续失败关闭。

## 9. 核心保护区

保护路径以 `docs/design/reply-runtime-engine.md` 第 14 节为准。普通业务开发不得修改
Intent 名称、JSON 字段、resourceAction、阶段顺序、稳定 ClientMsgID、任务状态或 Usage 口径。
需要扩展时优先在协议接入、业务 service 或向后兼容适配层实现，并先运行完整 Runtime 回归。
