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

- 新增内部 `ConversationTakeoverRequest`，状态为 `pending/authorized/approved/rejected/cancelled`。`authorized` 表示主管已批准、等待申请人二次确认；nullable `ActiveKey` 保证同一 Tenant、Conversation、Session 只有一个活动申请。
- 新增 dashboard 动作接口：
  - `POST /api/dashboard/conversation/takeover/request`
  - `POST /api/dashboard/conversation/takeover/direct`
  - `POST /api/dashboard/conversation/takeover/review`
  - `POST /api/dashboard/conversation/takeover/activate`
  - `POST /api/dashboard/conversation/resume_ai`
- 会话详情兼容新增 `takeoverState`，包含可回复、可申请、可直接接管、可审核、可二次确认激活、可交还 AI 以及当前申请展示状态；不修改 WebSocket 公共 payload。
- 普通申请只写申请和审计事件，不改变指派关系。重复申请幂等；已分配会话拒绝申请并要求走转派。
- 同时持有客服和门店员工角色时，申请资格按任一合法范围通过处理；客服档案不匹配不会覆盖有效的门店员工绑定资格。
- 审批通过时重新校验申请人的账号、角色、权限、客服组/门店员工绑定和会话范围，先写入 `authorized`；申请人二次确认后才原子写入 Assignment、Conversation、RouteState、申请终态和事件。
- 直接接管只允许 `HQ_AGENTDESK_PENDING + NeedHumanFollowUp + 未分配`，组长只能处理本人负责组，管理员只能处理当前租户。
- 当前接待人才能交还 AI；其他组长或管理员不能结束他人的人工服务。`HumanOnly` 或员工号 AI 回复关闭时拒绝交还。
- 网页发送在事务外初检、事务内锁定 Conversation/Route 后复检；企微员工号真实回复使用 `externalAgentReply`，不受网页审批门禁影响。
- 会话页群邀请在打开弹窗、提交和后端执行前均复用当前接待权限；后端同时核对会话、企微实例、门店员工绑定和群 ID，防止跨会话或跨群绕过。
- 会话关闭、新 Session、派单、自动派单、自动重派、转派、接管和交还 AI 会在同一事务内取消旧待审批申请。
- 所有相关写事务统一锁顺序：`Conversation -> ConversationRouteState -> ConversationTakeoverRequest`。
- 旧的组长“我来接管”入口已经复用 `ConversationTakeoverService.DirectTakeover`，不再维护第二套领取事务。

### 前端

- 会话底部不再用大按钮替换回复框；所有未关闭会话都显示原编辑器。
- 会话页恢复原回复框右下角 `AI回复` Switch 的位置和交互外观；Switch 只表示当前会话模式，不再调用员工号级全局 AI 开关。普通客服第一次点击提交申请，审批后申请人再次点击并确认才正式接管；组长、租户管理员、平台管理员和超级管理员在有效租户及权限范围内可直接确认接管。
- 点击发送时若尚无资格，会打开相应确认或提示，不会发送消息；文本发送失败或未获权限时不清空草稿。
- 交还 AI 只作用于当前会话，不再通过会话页切换员工号全局 AI 开关。
- 实时会话状态变化会刷新当前会话详情，使审批、派单和交还 AI 后的按钮状态及时更新。

## 权限与边界

- 申请人：`conversation.view` + `conversation.send`，且为负责组客服或当前门店员工绑定用户。
- 混合角色账号：客服或门店员工任一身份满足当前会话范围即可申请，不按角色顺序短路拒绝。
- 审核/直接接管：`conversation.view` + `conversation.assign`；直接接管额外要求 `conversation.send`。普通申请审批通过后进入内部 `authorized` 状态，仍不改变会话指派；申请人再次确认后才变为 `approved`。
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

## 2026-08-13 主线合并与测试 2 发布

### 主线合并

- 实施提交 `4a1ad9c8f2ae94c251d9b2380e1fa24b640ce8d8` 基于旧主线
  `3ab3809` 完成。发布前两个远端主线已前进到 `6e528e5`，因此没有覆盖推送，
  而是从最新主线创建集成分支并移植功能提交。
- 唯一冲突位于 `conversation_human_dispatch_service_test.go` 的测试 AutoMigrate
  列表：新主线新增 `ConversationDialogueState`，本功能新增
  `ConversationTakeoverRequest`。合并结果同时保留两张表，没有选择任一侧覆盖另一侧。
- 合并后的运行代码提交为
  `65802f40ceff3fd5947a651a60c944ce2e315b11`，已快进推送到
  `origin/main`（agentdesk）和 `weibao/main`。
- 合并后重新执行并通过：聚焦会话服务测试、bootstrap 路由/AutoMigrate 测试、
  完整 `go test ./internal/services -count=1`、`pnpm typecheck`、13 个会话前端
  Node 测试和 `git diff --check`。

### 新服务器发布

- 仅发布微宝应用到测试 2 服务器 `test-2`（公网 `36.138.68.47`）；没有连接或修改
  旧服务器。
- 服务器真实运行形态是 `agentdesk.service`，Nginx 在公网端口 `2303` 反向代理
  本机 `127.0.0.1:8083`，不是 Docker Compose 常驻容器。
- 上一 release：`/opt/agentdesk/releases/20260812-ai-recovery-6e528e5`。
- 当前 release：`/opt/agentdesk/releases/20260813-takeover-65802f4`。
- `/opt/agentdesk/current/REVISION` 为
  `65802f40ceff3fd5947a651a60c944ce2e315b11`。
- 当前 `agent-desk` SHA-256：
  `56472b77f69b34a1e93f4b2da77b5a5a412e42b3cbbdce40d758a6b17c30f979`。
- 发布包总 SHA-256：
  `a4f13844ee22bb07591bc16070bfe6f73463773234fda333739e8bb2d863e2a5`；
  包内 658 条逐文件校验全部通过，其中包含 656 个同版本前端文件。
- 发布前回滚点：
  `/opt/agentdesk/backups/pre-takeover-65802f4-20260813-023600`。
- 发布前 MySQL 一致性快照 SHA-256：
  `929cd83024b934ab975d9608e561624f54e43b2e4a4499d729a27291773d3b4e`。
- `current` 使用原子符号链接切换；重启后 `agentdesk.service` 为
  `active/running`，`NRestarts=0`。切换脚本包含 90 秒健康门禁和自动回滚。

### 生产验收

- AutoMigrate 已真实创建 `t_conversation_takeover_request`，共 23 列；唯一索引
  `uk_conversation_takeover_active` 已存在。
- 四条接管接口均已注册，不再是 404：
  `/takeover/request`、`/takeover/direct`、`/takeover/review`、`/takeover/activate`。
- 本机 `/api/auth/options`、`/dashboard/login/`、`/dashboard/conversations/`
  均返回 HTTP 200。
- 公网 `http://36.138.68.47:2303`、`https://36.138.68.47:2303` 和
  `https://weibao.omnireva.com` 登录页均返回 HTTP 200。
- 发布前后逐字节比对确认以下文件未变化：
  `runtime-production.env`、`agent-desk.yaml`、Nginx 2303 配置和
  `agentdesk.service`。
- FastGPT 仍为 `http://36.138.68.47:6080`，NewAPI 模型网关仍为
  `http://36.138.68.47:6081/v1`；两者公网入口均返回 HTTP 200。本次没有部署、
  重启或修改 FastGPT/NewAPI，也没有输出其密钥。
- 日志中的 `FastGPT usage sync failed` 在发布前旧进程与发布后新进程均持续出现，
  属于既有外部用量同步告警，不是本次接管审批发布引入；本次按范围未修改该链路。

### 发布回滚

- 应用回滚只需将 `/opt/agentdesk/current` 原子切回
  `/opt/agentdesk/releases/20260812-ai-recovery-6e528e5` 并重启
  `agentdesk.service`。
- 新增表可保留，不阻止旧版本启动；如需要恢复数据，使用上述发布前 MySQL 快照。
- 回滚不需要修改 Nginx、ESA、FastGPT、NewAPI、生产环境文件或企微回调配置。

## 2026-08-15 AI 开关可见性与主动接管修复

- 生产复核确认超管账号具备 `super_admin`、`conversation.view`、
  `conversation.send` 和 `conversation.assign`；不能接管不是账号权限问题。
- 原接管状态只允许 `HQ_AGENTDESK_PENDING + NeedHumanFollowUp` 直接接管，导致
  未分配的 `AI_SERVING`、`AI_FALLBACK` 会话对组长及管理员返回
  `canDirectTakeover=false`。这与“通过原 AI 回复开关主动接管”的产品语义冲突。
- 修复后，超管、管理员、租户管理员和负责当前组的客服组长可确认接管未分配的
  `AI_SERVING`、`AI_FALLBACK` 或有效 `HQ_AGENTDESK_PENDING` 会话；普通客服和
  门店员工继续走申请审批，已经分配给其他人的会话继续使用转派流程。
- 会话编辑器左侧工具按钮原本不可收缩，实际工作台宽度不足时会把右侧 AI 开关
  挤出 `overflow-hidden` 容器。修复后左侧工具区可横向滚动，右侧 AI 开关与发送键
  固定为不可收缩区域，开关在所有开放会话中保持可见。
- 生产会话 `来一杯生椰拿铁` 与 `其风` 的真实组合为
  `Conversation.Status=AI_SERVING`、`RouteStatus=STORE_WECOM_MANUAL`、
  `NeedHumanFollowUp=true`、`CurrentAssigneeID=0`。接管门禁现按“未分配且明确待人工”
  判断，允许超管、管理员、租户管理员和负责组长直接接管；`NeedHumanFollowUp=false`
  时仍拒绝，避免接管已经清除人工关注的会话。
- 服务器 release 的前端由 `web/out` 嵌入 Go 二进制。发布时必须先执行全新的
  `pnpm build`，再构建 Go 二进制；禁止只更新提交标识或复用旧 `web/out`，否则源码
  已恢复 Switch 但线上仍会显示旧接管图标。
- 本修复不修改数据库、公开 DTO、WebSocket、AI Runtime、知识库、企微协议、
  FastGPT、NewAPI、Token/Usage 或计费归因。
