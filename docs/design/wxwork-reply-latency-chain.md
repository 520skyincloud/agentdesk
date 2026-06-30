# 企微员工号回复速度链路

本文记录客户消息从企微员工号进入系统，到 AI 回复发回客户微信会话的完整链路，以及当前速度优化点。

## 1. 入站回调

1. 协议平台回调 `POST /api/third/wxwork-protocol/callback`。
2. `internal/handlers/third/wxwork_protocol_handler.go` 读取原始 body，并调用 `WxWorkProtocolService.HandleCallback`。
3. `HandleCallback` 按 `notify_type` 分发：
   - 登录 / 登出：同步员工号状态。
   - 单条消息：`handleMessage`。
   - 批量消息：逐条调用 `handleChatMessage`。
4. `handleChatMessage` 做去重、自发 echo 过滤、会话映射、路由状态维护、客户消息落库。

回调请求内不直接等待大模型。文本消息落库后立即返回，AI 回复由异步 hook 触发。

## 2. 客户消息落库后触发

`MessageService.SendCustomerMessage` 完成事务后：

1. 推送 WebSocket：会话页先看到客户消息。
2. 更新客户-门店关系、路由状态、未读数。
3. 如果是定位消息，绑定到员工号门店定位。
4. 如果当前路由是人工接待、总部接待、AI 已关闭，则不触发 AI。
5. 图片、语音、文件进入 `MediaUnderstandingService.UnderstandInboundMessageAsync`。
6. 普通文本 / HTML / GIF 进入 `TriggerAIReplyAsyncHook`。

## 3. AI 回复前等待策略

代码位置：`internal/ai/runtime/reply_trigger_service.go`。

当前策略：

- 文本 debounce：`120ms`。只用于等极短时间内的连续文本，避免用户连发两句时抢答第一句。
- 媒体 settle：最多 `900ms`，且只在当前文本明显是在追问媒体时等待，例如“帮我看下这张图片”“这个文件什么意思”“听下语音”。客户先发图片/语音/文件后，马上补“这个多少钱”“能用吗”“这是什么”等短问题，也视为媒体追问并短等理解结果。普通文本、FAQ、寒暄、定位、小程序和服务动作不等待最近媒体理解，避免每条消息都被图片/语音解析拖慢。
- 连发合并窗口：`8s`。最终回复前会把最近连续客户消息合并为“请一起理解，不要只回复最后一句”。
- 媒体上下文窗口：`6s`。只等待最近几秒内的媒体理解，避免老媒体拖慢新问题。

这几个值的目标是：比过去更快，同时保留“用户快速连发”和“先发图片再问问题”的正确语义。

## 4. AI 执行链路

1. `aiReplyService.TriggerReply` 判断是否仍是最新客户消息。
2. 处理中断确认，例如转人工确认。
3. 合并短时间内连续客户消息。
4. `runtimeReplyExecutor.Run` 调用 `internal/ai/application/runtime`。
5. runtime 构建历史上下文、知识库检索、工具定义、模型请求。
6. 如果有回复文本，`replyCommitService.CommitAIReply` 写入 AI 消息。
7. AI 消息入 outbox 后，`MessageService` 立即异步触发 `WxWorkProtocolService.DispatchPendingOutbox(10)`。

## 5. 出站协议发送

1. `ChannelMessageOutboxService.EnqueueWxWorkProtocolMessage` 创建 outbox。
2. `WxWorkProtocolService.DispatchPendingOutbox` 读取 pending outbox。
3. `dispatchOutbox` 校验会话、渠道、实例、协议 `conversation_id`。
4. 文本调用 `/msg/send_text`，富媒体先校验/上传 CDN，再调用对应协议发送接口。
5. 成功后 outbox 标记 `sent`；失败记录真实错误，不假成功。
6. AI 发送成功后异步调用 `/msg/report_unread` 标记企微会话已读。

## 6. 慢点定位

AI 运行日志 `AgentRunLog.TraceData` 会记录：

- `settleMs`：回复前等待用户连发 / 媒体理解的耗时。
- `runtimeLatencyMs`：知识库、工具、模型执行耗时。
- `commitMs`：写入 AI 回复和创建 outbox 的耗时。
- runtime trace 里的 `retriever.embeddingMs / vectorSearchMs / hydrateMs`：知识库检索分段耗时。

排查顺序：

1. `settleMs` 高：用户刚发过图片/语音/文件，系统在等媒体理解；或连续发文本导致旧回复被跳过。
2. `runtimeLatencyMs` 高：模型接口慢、知识库检索慢、工具链调用慢。
3. `commitMs` 高：数据库写入或 outbox 创建慢。
4. outbox pending 很久：协议发送慢、协议接口失败、媒体上传慢、CDN 参数缺失。

## 7. 后续可继续优化

- 对纯寒暄、确认、感谢等低风险短消息走轻量模型或本地快速回复，但不能绕过人格和门店配置。
- 对知识库检索做缓存，例如同一会话短时间内相同问题复用检索结果。
- 媒体理解可先回“我看下”，理解完成后再补充回复，但要避免打扰式多次回复。
- 出站协议发送可改为专用 worker 池，减少 cron 兜底依赖。
