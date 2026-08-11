# AI 回复 Turn Coordinator 实施交接

## 状态与目标

- 初始实施基线：`weibao/main@53f9375`；2026-08-11 增量修复基线：`weibao/main@f25fd78`。
- 开发分支：`codex/ai-reply-task-ledger`，独立 worktree 实施。
- 状态：Turn/Task 基础结构已在 `f25fd78` 灰度运行；最新版本所有权与知识答案分组增量正在完成
  回归、提交和灰度发布，未完成生产证据验证前不得宣称已上线。
- 目标：处理企微消息 1 至 14 秒迟到导致的连续消息重复回复；不改变 Intent、FastGPT、九槽、
  Token/计费和人工派单协议。

## 实现边界

- 协议 `sendtime` 写入 `Message.SentAt`，`CreatedAt` 保留平台接收时间，并记录脱敏
  `inbound_lag_ms`。
- 新增内部 `AIReplyTurn` 和 `AIReplyTurnTask`。客户 Message、Turn Version 和 AIReplyJob 在同一事务内
  形成稳定关系，每个独立问题、资源动作或人工动作使用稳定 TaskKey 持久化处理状态。
- 同一 Turn 只允许一个持有租约的 Job 执行；每批最多处理 6 个 Task，知识 Task 最多 4 路并行，
  剩余 Task 由同一持久 Job 自动续批。
- 多题生成必须按 `taskKey` 覆盖本批次全部文本任务，最多提交三条文本消息；知识失败只影响对应
  Task，已成功答案不回滚。
- Runtime 只有在 `Job.TurnVersion == Turn.Version` 时才能 Claim、运行和 Commit。新客户消息升级
  Version 时释放旧 Job 的 Task 领取与 Turn 租约、将旧版本 Job 标记为 `superseded`，并取消进程内
  Worker；最新 Job 从账本接管全部未完成 Task。Message、Outbox、会话计数和 Turn 证据仍在同一事务提交。
- Outbox Claim 前再次检查 Turn 和已提交 Task。带 Task 证据的消息按 Task 终态决定发送；已覆盖、
  人工接管或范围失效时取消。只有没有 Task 证据的兼容旧消息才用 Version 作为兜底门禁。
- 相同迟到问题使用确定性文本哈希复用既有回复；不同问题只处理新增内容。生成仍重复旧答案时只
  重试一次 Generate，仍失败进入现有人工池。
- System/欢迎资源不结束轮次；人工回复、人工接管、撤回、关闭、会话继承和 Session 变化结束轮次。
- 同批知识 Task 的排名第一命中若指向相同 `KnowledgeBaseID + SourceRecordID`，使用内部
  `AnswerGroup` 合并为一个 `replyPart`。该 Part 的 `taskKeys[]` 覆盖组内全部 Task，Commit 创建一条
  Message 并为每个 Task 写入相同提交证据；不同首条命中不合并。

## 数据与接口影响

- 新表：`AIReplyTurn`、`AIReplyTurnTask`，通过 AutoMigrate 创建。
- 兼容字段：`Conversation.CurrentAIReplyTurnID`、`Message.AIReplyTurnID/AIReplyTurnVersion`、
  `AIReplyJob.TurnID/TurnVersion/CoveredByMessageID/CoveredByTaskID`。
- Outbox 新增内部终态 `cancelled`，不修改公开 DTO。
- DML migration：无。
- HTTP API、WebSocket payload、Intent Schema、九槽字段和前端页面：无变化。
- Usage、Token 和计费：归因口径不变；提前识别为完全重复的问题不调用模型，因此不会重复计费。
- 隐私：Turn 只保存范围、ID、版本、时间和提交/投递证据，不保存正文、Prompt、模型输出或原始响应。

## 灰度与回滚

- 默认关闭：`AI_REPLY_TURN_COORDINATOR_ENABLED=false`。
- 首批显式设置 `true`，并在 `AI_REPLY_TURN_COORDINATOR_BINDING_IDS` 只填写“合肥南七店”当前启用
  员工号的真实 StoreStaffBinding ID；必须从生产数据库按 Tenant、Store 和 active 状态查询，禁止猜测。
- 表不存在或开关关闭时回退旧链；范围损坏继续 fail closed。
- 回滚先关闭开关并强制重建容器，再切回上一不可变镜像。新增表和零值兼容字段无需删除。

## 验证覆盖

- 1、2、3、14 秒迟到的相同问题加入原 Turn，最终只复用一条回复。
- 不同迟到问题不会被吞掉；重复生成旧答案触发受控重试/人工兜底。
- 多个知识问题逐题建立 Task，并行检索后由一次 Generate 按 taskKey 完整覆盖；单项失败只转该题人工。
- Outbox pending、sending、failed、sent 和 stale cancellation。
- 两个 Worker、旧 Turn Version Commit、客户撤回、人工路由、关闭和会话继承。
- SQLite/MySQL AutoMigrate、Tenant/Store/Binding 审计、模型注册和 Turn 隐私字段。
- DeepSeek、九槽、FastGPT、Usage 和 Binding 计费路径没有契约变化。

验证命令：

```bash
go test -tags dev ./internal/services -run 'AIReplyTurn|AIReplyJob|Outbox|WxWorkProtocol' -count=1
go test -tags dev ./internal/ai/runtime/... -run 'Turn|PreCommit|InflightTail|Duplicate' -count=1
go test -race -tags dev ./internal/ai/... ./internal/services -run 'AIReply|Turn|Task|Outbox|HumanDispatch|Knowledge' -count=1
go test -tags dev ./... -count=1
go vet -tags dev ./...
```

## 2026-08-10 灰度前复核补丁

- 复核发现同一批次同时出现“部分知识任务失败”和“Generate 失败”时，旧逻辑只把已标记的知识失败
  Task 转人工，其余已通过知识检索但未生成成功的 Task 会被 Job 续跑，可能放大模型调用和延迟。
- 修复后，凡本批次发生 `generation_failed`、`knowledge_unavailable`、`empty_output` 或
  `resource_invariant_broken`，都将错误影响范围与本批全部未提交 Task 合并后一次性进入现有人工池。
- 已经原子提交成功的 Task 保持 delivered/committed，不会被回滚或重复派单；人工派单失败仍只重试派单，
  不重新执行模型链。
- 回归测试 `TestAIReplyTaskLedgerGenerationFailureIncludesTasksThatPassedKnowledge` 断言模型链只运行一次、
  人工只派单一次、所有受影响 Task 均进入 handoff，终态 Job 不会再次运行。

## 2026-08-11 最新版本所有权与知识答案分组

- 生产复核发现 `f25fd78` 虽已具备 Turn/Task 表和单 Turn 租约，但同一 Turn 增加客户消息后，较早
  Version 的活动 Job 仍可能继续持有 Task 并进入 Runtime/Commit，造成连续消息分别生成和重复回复。
- 修复后，同 Turn 每条新客户消息都升级 Version，同时释放旧 Task Claim 和 Turn Lease、批量终结
  旧版本 Job，并通过活动 Context 取消旧 Worker。Claim、Runtime Scope、Freshness 和 Commit 均要求
  精确匹配最新 Version，取消竞态也不能绕过数据库门禁。
- 生产复核还发现“想喝咖啡了”和“咖啡在哪”等相关问题可能分别检索到同一 FastGPT 记录，但旧
  多任务协议仍要求每个 TaskKey 单独生成内容。修复后以排名第一的真实知识记录生成 AnswerGroup，
  模型按 `replyParts[].taskKeys[]` 一次回答，Commit 将一条 Message 绑定到组内全部 TaskKey。
- 该增量不修改 Model、公开 API、DTO、WebSocket、Intent Schema、九槽字段、Usage 或计费归因；
  不新增表、字段或 DML migration。新增检查均为已有 Turn/Task 行锁、内存分组和 Commit 映射。
- 已在新消息到达前原子 Commit 的不同问题答案仍可按 Task 证据发送，避免误删有效回答；精确重复的
  迟到问题继续复用既有 Message/Outbox。该边界必须与“旧 Worker 失去提交资格”区分。
- 新增回归覆盖：新 Version 取消活动 Worker并释放 Claim、旧 Job 收敛为 `stale_turn_version`、相同
  首条知识命中合并生成、协议禁止拆分 AnswerGroup、单 Message 对多个 TaskKey 写入提交证据。

## 并行分支与合并

- 共享高风险文件：`models.go`、`message_service.go`、`ai_reply_job_service.go`、
  `reply_commit_service.go`、Outbox service/repository、会话关闭/人工派单路径和租户完整性审计。
- 无 migration 版本冲突，但新增 AutoMigrate 表和关联字段必须与最新 `weibao/main` 同文件变化复核。
- 合并前必须 `git fetch origin`，检查 `weibao/main` 和引用分支 `origin/codex/ai-billing` 的同文件差异；
  禁止整体 merge/cherry-pick `ai-billing`。
- 建议作为一个可回滚的后端契约提交合入主线，不单独挑选 Model 或 Outbox 片段。

## 核心保护区

- `internal/services/ai_reply_turn_service.go`
- `internal/repositories/ai_reply_turn_repository.go`
- `internal/services/ai_reply_turn_task_service.go`
- `internal/repositories/ai_reply_turn_task_repository.go`
- `internal/ai/runtime/executor/task_ledger.go`
- `internal/ai/runtime/executor/task_knowledge.go`
- `internal/services/ai_reply_job_service.go`
- `internal/ai/runtime/reply_commit_service.go`
- `internal/services/channel_message_outbox_service.go`
- `internal/services/message_service.go` 的客户消息事务
- `internal/models/models.go` 的 Turn 关联字段

后续业务不得绕过 Turn Version Commit/Outbox 门禁，也不得把模糊语义去重加入该层。
