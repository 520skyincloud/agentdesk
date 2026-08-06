# 企微员工号回复速度链路

> 更新时间：2026-08-06

本文记录客户消息从企微员工号进入系统，到 AI 回复发回客户微信会话的当前生产链路、等待
上限和排障口径。真实实现以 `weibao/main` 代码和 `reply-runtime-engine.md` 为准。

## 1. 入站回调

1. 协议平台回调 `POST /api/third/wxwork-protocol/callback`。
2. `internal/handlers/third/wxwork_protocol_handler.go` 读取原始 body，并调用 `WxWorkProtocolService.HandleCallback`。
3. `HandleCallback` 按 `notify_type` 分发：
   - 登录 / 登出：同步员工号状态。
   - 单条消息：`handleMessage`。
   - 批量消息：逐条调用 `handleChatMessage`。
4. `handleChatMessage` 做去重、自发 echo 过滤、会话映射、路由状态维护，并调用
   `MessageService.SendCustomerMessage`。

回调请求内不等待大模型。客户 Message 与 `AIReplyJob` 在同一数据库事务内提交，任一写入失败
时同时回滚并让协议上游重试；重复 ClientMsgID 幂等补齐缺失任务。

## 2. 客户消息落库后触发

`MessageService.SendCustomerMessage` 完成事务后：

1. 推送 WebSocket：会话页先看到客户消息。
2. 更新客户-门店关系、路由状态、未读数。
3. 如果是定位消息，绑定到员工号门店定位。
4. 文本、HTML、图片、语音和附件生成持久任务；历史、AI、人工、System、撤回和失败消息不生成。
5. 新任务最早执行时间为入队后 `250ms`；`cronx` 每秒扫描到期任务，单进程最多并发 4 个。
6. Worker CAS Claim 90 秒租约，每 30 秒续租；丢失租约会取消 Runtime Context。

图片、语音和附件理解由持久任务同步协调，不存在裸 goroutine 的第二条 AI 回复入口。最近
15 分钟的补偿扫描只补缺失任务，不会回放更早历史消息。

## 3. AI 回复前等待策略

代码位置：`internal/ai/runtime/reply_trigger_service.go`。

当前策略：

- 文本 debounce：`120ms`。只用于等极短时间内的连续文本，避免用户连发两句时抢答第一句。
- 媒体 settle：最多 `900ms`，且只在当前文本明显是在追问媒体时等待，例如“帮我看下这张图片”“这个文件什么意思”“听下语音”。客户先发图片/语音/文件后，马上补“这个多少钱”“能用吗”“这是什么”等短问题，也视为媒体追问并短等理解结果。普通文本、FAQ、寒暄、定位、小程序和服务动作不等待最近媒体理解，避免每条消息都被图片/语音解析拖慢。
- 媒体未完成时不抢答：如果 `900ms` 内媒体仍未理解完成，当前文本回复会延后，不先输出“看不到/无法确认”这类低质量回复。媒体理解完成后，`MediaUnderstandingService` 会查找同一会话 8 秒内最新的客户文本追问，优先触发那条文本，而不是触发旧图片消息。这样“先发图片再问这个多少钱”会合并成一次完整回答。
- 连发合并窗口：`8s`。最终回复前会把最近连续客户消息按顺序合并，标注文本、图片、语音、文件、定位、小程序等类型，并保留媒体理解结果，要求模型统一理解当前真正的问题，不只回复最后一句。
- 媒体上下文窗口：`6s`。只等待最近几秒内的媒体理解，避免老媒体拖慢新问题。

上述等待发生在任务 Claim 之后。纯文本固定 settle 通常约 `120ms`；只有明确追问近期媒体时
才可能增加最多约 `900ms`。8 秒是回看范围，不是固定等待 8 秒。

## 4. AI 执行链路

1. Worker 每次执行都重新读取并校验 Conversation、源 Message、Session、Route、Binding、当前
   企微实例、AI 开关和接待状态。
2. `aiReplyService.TriggerReply` 执行 settle、新鲜度检查和待确认 Interrupt 恢复。
3. 同 Session 最近 8 秒客户消息按顺序合并；默认上下文继续包含 15 条近期消息、压缩记忆和
   同门店客户长期记忆。
4. Runtime 依次执行 IntentDetect、条件 FastGPT 知识探测/检索、ReplyPlan、Generate、Validate。
5. 每个模型阶段读取当前九槽 `MaxRetryCount`；默认 `2`，所以超时、5xx 或空模型结果最多是
   初次调用加两次重试。DeepSeek V4 请求同时关闭 `thinking` 和 `enable_thinking`。
6. FastGPT 明确区分无命中和基础设施失败；`interaction/clarify` 条件探测命中后直接复用结果，
   不再做第二次正式检索。
7. `replyCommitService.CommitAIReplyBatch` 将同轮多段文本和定位/小程序等结构化动作一次原子写入
   Message、Outbox、会话计数、AI 轮次和事件，事务后发布 WebSocket。
8. Runtime 返回 `completed` 后，Job 再从数据库验证 Message 或 Interrupt 持久证据才终结。

## 5. 出站协议发送

1. `ChannelMessageOutboxService.EnqueueWxWorkProtocolMessage` 创建 outbox。
2. `WxWorkProtocolService.DispatchPendingOutbox` 只读取 `next_retry_at` 为空或已经到期的 pending/
   failed outbox，CAS Claim 也再次检查到期条件。
3. `dispatchOutbox` 校验会话、渠道、当前实例、协议 `conversation_id`。
4. 文本调用 `/msg/send_text`，富媒体先校验/上传 CDN，再调用对应协议发送接口。
5. 成功后 outbox 标记 `sent`；失败按 Outbox 自身计划重试，不重新调用模型或重新提交消息。
6. AI 发送成功后异步调用 `/msg/report_unread` 标记企微会话已读。

## 6. 慢点定位

AI 运行日志 `AgentRunLog.TraceData` 会记录：

- `settleMs`：回复前等待用户连发 / 媒体理解的耗时。
- `runtimeLatencyMs`：知识库、工具、模型执行耗时。
- `commitMs`：写入 AI 回复和创建 outbox 的耗时。
- runtime trace 里的 `retriever.embeddingMs / vectorSearchMs / hydrateMs`：知识库检索分段耗时。

任务与协议侧还要同时看：

- Message `created_at` 到 AIReplyJob `started_at`：入队初始 250ms、每秒调度和并发排队耗时。
- `AgentRunLog.latency_ms`：settle、Runtime 和 Commit 的总耗时。
- `AIReplyJob.attempt_count / result_code / last_error_class`：基础设施 Claim 次数和受控终态；
  `attempt_count` 不是模型调用次数。
- `ChannelMessageOutbox.next_retry_at / send_status`：消息已提交后等待协议发送或重试的耗时。
- Usage/Gateway Call：按槽统计真实 provider 调用次数；默认模型失败应看到恰好 3 次调用。

排查顺序：

1. `settleMs` 高：用户刚发过图片/语音/文件，系统在等媒体理解；或连续发文本导致旧回复被跳过。
2. `runtimeLatencyMs` 高：模型接口慢、知识库检索慢、工具链调用慢。
3. `commitMs` 高：数据库写入或 outbox 创建慢。
4. Message 到 Job `started_at` 高：worker 并发已满、任务尚未到期或调度器未运行。
5. outbox pending 很久：先看 `next_retry_at` 是否到期，再查协议接口、媒体上传和 CDN 配置。

## 7. 失败与恢复时间

- 模型阶段：默认最多 3 次调用，槽内重试等待约 `100ms`、`200ms`，但主要耗时由每次槽
  `TimeoutMS` 决定。耗尽后立即重新检查会话并进入现有人工任务池。
- FastGPT：基础设施失败默认初次请求加两次网关重试；无命中不会按故障重试。
- 人工派单失败：1 分钟后只重试派单，不重跑 Intent、知识或 Generate。
- 非受控基础设施失败：AIReplyJob 最多 4 次 Claim，退避 `15s / 1min / 3min`。
- 任务满 15 分钟仍是最新问题且无人回复：停止模型调用并幂等转人工。
- Commit 后进程退出：稳定 Message/Interrupt 证据使恢复任务直接完成；Outbox 独立续投。

`AgentRunLog` 只用于诊断，明确记录 `committed`、`policy_skipped`、`interrupted` 或 `failed`；
没有错误文本的 RunLog 不能证明消息已经提交。

## 8. 不可破坏的速度边界

- 不能为了缩短耗时绕过 Tenant/Store/Binding/实例或九槽校验。
- 不能把 8 秒回看改成固定等待，也不能恢复裸异步 Hook 或媒体二次触发。
- 不能用 RunLog 代替 Message/Interrupt 完成证据。
- 不能让 Outbox 重试重跑 Runtime。
- 不能让 System 欢迎资源 supersede 客户任务；人工回复和更新客户消息必须按状态机停止旧任务。
- 调整超时或重试只能通过九槽 revision 发布并应用，必须保持 Usage 和 Binding 计费归因。
