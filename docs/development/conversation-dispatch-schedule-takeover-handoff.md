# 派单排班与人工接管交接

> 日期：2026-08-12
>
> 分支：`codex/dispatch-schedule-takeover`
>
> 合入目标：`main`

## 本次范围

- 有效排班表示接单责任，自动和人工派单均不以在线、离线、忙碌、休息或 Presence 过期作为资格门禁。
- 普通客服或门店员工主动接管时先申请，客服组长审核后才能网页回复。
- 客服组长及以上可直接领取自己管理范围内、尚未分配的待总部人工会话。
- 当前接待人主动交还 AI 时二次确认；仅人工模式和 AI 回复关闭时禁止交还。
- 所有未关闭会话继续显示正常回复框，发送时再核验权限；无权限或申请未通过时不写 Message/Outbox，文本草稿保留。

明确不在本次范围：自动转人工原因、触发条件、模型失败转人工、人工池决策和 AI Handoff 规则。

## 真实实现

### 后端

- 新增内部 `ConversationTakeoverRequest`，状态为 `pending/approved/rejected/cancelled`。nullable `ActiveKey` 保证同一 Tenant、Conversation、Session 只有一个待审批申请。
- 新增 dashboard 动作接口：
  - `POST /api/dashboard/conversation/takeover/request`
  - `POST /api/dashboard/conversation/takeover/direct`
  - `POST /api/dashboard/conversation/takeover/review`
  - `POST /api/dashboard/conversation/resume_ai`
- 会话详情兼容新增 `takeoverState`，包含可回复、可申请、可直接接管、可审核、可交还 AI 以及当前申请展示状态；不修改 WebSocket 公共 payload。
- 普通申请只写申请和审计事件，不改变指派关系。重复申请幂等；已分配会话拒绝申请并要求走转派。
- 同时持有客服和门店员工角色时，申请资格按任一合法范围通过处理；客服档案不匹配不会覆盖有效的门店员工绑定资格。
- 审批通过时重新校验申请人的账号、角色、权限、客服组/门店员工绑定和会话范围，再原子写入 Assignment、Conversation、RouteState、申请终态和事件。
- 直接接管只允许 `HQ_AGENTDESK_PENDING + NeedHumanFollowUp + 未分配`，组长只能处理本人负责组，管理员只能处理当前租户。
- 当前接待人才能交还 AI；其他组长或管理员不能结束他人的人工服务。`HumanOnly` 或员工号 AI 回复关闭时拒绝交还。
- 网页发送在事务外初检、事务内锁定 Conversation/Route 后复检；企微员工号真实回复使用 `externalAgentReply`，不受网页审批门禁影响。
- 会话页群邀请在打开弹窗、提交和后端执行前均复用当前接待权限；后端同时核对会话、企微实例、门店员工绑定和群 ID，防止跨会话或跨群绕过。
- 会话关闭、新 Session、派单、自动派单、自动重派、转派、接管和交还 AI 会在同一事务内取消旧待审批申请。
- 所有相关写事务统一锁顺序：`Conversation -> ConversationRouteState -> ConversationTakeoverRequest`。
- 旧的组长“我来接管”入口已经复用 `ConversationTakeoverService.DirectTakeover`，不再维护第二套领取事务。

### 前端

- 会话底部不再用大按钮替换回复框；所有未关闭会话都显示原编辑器。
- 右下角小按钮根据服务端 `takeoverState` 展示申请接管、直接接管、审核申请或交还 AI，并使用 Dialog 二次确认。
- 点击发送时若尚无资格，会打开相应确认或提示，不会发送消息；文本发送失败或未获权限时不清空草稿。
- 交还 AI 只作用于当前会话，不再通过会话页切换员工号全局 AI 开关。
- 实时会话状态变化会刷新当前会话详情，使审批、派单和交还 AI 后的按钮状态及时更新。

## 权限与边界

- 申请人：`conversation.view` + `conversation.send`，且为负责组客服或当前门店员工绑定用户。
- 混合角色账号：客服或门店员工任一身份满足当前会话范围即可申请，不按角色顺序短路拒绝。
- 审核/直接接管：`conversation.view` + `conversation.assign`；直接接管额外要求 `conversation.send`。
- 网页回复：`conversation.send` + 当前指派人 + `HQ_AGENTDESK_SERVING`。
- 会话页群邀请：网页回复全部条件 + 当前会话绑定的企微实例和群 ID；无会话上下文的实例级调用额外要求 `channel.update`。
- 交还 AI：当前指派人 + `conversation.view` + `conversation.send`。
- 已分配给其他客服的会话不能通过接管申请或直接接管抢占，只能使用原转派流程。
- 所有查询和写入带 Tenant、Conversation、Session；不同租户、门店、员工号和客服组不得串线。

## 数据与兼容性

- DDL 通过 AutoMigrate 创建 `ConversationTakeoverRequest`，兼容 SQLite 和 MySQL。
- 不新增 DML migration，不修改 Intent、AI Runtime、模型、知识库、计费或企业微信协议字段。
- 新接口属于 dashboard 内部动作接口；会话详情只做向后兼容字段新增。
- 新表和终态记录可在回滚旧镜像后保留，旧版本不会读取它们。

## 测试覆盖

- 申请不改变会话指派、Message 或 Outbox；同一申请人重复提交幂等。
- 审批通过、拒绝、跨组拒绝和 Session 变化自动取消。
- 主管直接接管并发只允许一个赢家；旧入口和新接口使用同一事务。
- 已分配会话必须走转派；普通派单和转派后旧申请立即取消。
- 只有当前接待人可交还 AI；`HumanOnly` 不可交还。
- 审批前网页发送被拒绝且不产生 Message/Outbox；审批后可正常回复。
- 企微员工号外部回复继续正常。
- 手工指定离线/休息客服成功；Presence 只用于展示和运营分析。
- 混合角色通过有效门店绑定申请；群邀请正确会话成功，错员工号、错群和非当前接待人均拒绝。

验证命令：

```bash
go test ./internal/bootstrap ./internal/services -run 'ConversationTakeover|ConversationSupervisorTakeover|ConversationAssignmentAndTransfer|ManualSessionTimeout|StoreManualAgentReply|AgentTeamScope|ConversationDispatch' -count=1
go test ./... -count=1
go test -race ./internal/services -run 'ConversationTakeover|ConversationSupervisorTakeover|ManualSessionTimeout|ConversationDispatch' -count=1
go vet ./...
cd web && pnpm typecheck
cd web && node --test app/dashboard/conversations/wxwork-account-permissions.test.mjs
git diff --check
```

实际验证结果：

- 接管、主管接管、派单、超时、门店员工回复、范围与路由聚焦测试通过。
- `go test ./internal/services -count=1` 通过。
- 排除 `internal/ai/runtime/internal/impl/factory` 后的全仓测试通过。
- `go test -race ./internal/services -run 'ConversationTakeover|ConversationSupervisorTakeover|ManualSessionTimeout|ConversationDispatch' -count=1` 通过。
- `go vet ./...`、`pnpm typecheck`、会话前端 7 个 Node 契约测试和 `git diff --check` 通过。
- SQLite AutoMigrate、幂等迁移、活动申请唯一索引、模型注册和租户完整性审计通过。
- 当前环境未配置 `TEST_MYSQL_DSN`，MySQL AutoMigrate 契约测试按仓库惯例跳过。
- `TestChatModelFactoryRetriesAfterPerAttemptTimeout` 会因 `httptest.Server.Close` 等待活动连接而在 45 秒超时；相同问题已在干净 `origin/main@3ab3809` 复现，属于主线既有 AI factory 测试问题，本分支不修改该包。

## 并行分支与合并

- 共享高风险文件：`internal/models/models.go`、`conversation_service.go`、`conversation_route_service.go`、`message_service.go`、会话 DTO/API 和会话页组件。
- 本分支不修改 AI 回复引擎、模型供应商、FastGPT、NewAPI、Token/Usage 或计费归因。
- 合入后，其他修改 Conversation/Route/Message 事务的分支必须保留统一锁顺序和事务内发送复检。
- 建议先合入本分支，再让并行的会话权限或派单分支 rebase；AI Profile 等无同文件变更可独立合入。

## 回滚

- 回退应用提交或恢复上一镜像即可，不需要数据库回填。
- 新表和已完成申请记录保留为审计历史；nullable 字段/独立表不影响旧版本。
- 回滚后恢复旧会话页时，网页回复将回到旧权限语义，因此应同时回滚前后端，不能只回滚一侧。
