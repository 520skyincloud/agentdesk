# 派单排班与主管接管优化交接

> 日期：2026-08-12
>
> 分支：`codex/dispatch-schedule-takeover`
>
> 基线：`origin/main@b00c215`

## 目标

- 自动派单以有效排班表示接单责任，不再受在线、离线、忙碌、休息或 Presence 过期影响。
- 人工指定客服同样不检查 Presence。
- 客服组长、租户管理员和平台管理员可直接接管符合条件的待总部人工会话，且主管本人无需客服档案。
- 保持租户、客服组、账号、权限、服务范围、排班和容量等既有安全边界。

## 实现

- `conversation_dispatch_service.go`：候选计算和事务复核移除 Presence 门禁，Presence 继续用于工作台展示。
- `conversation_dispatch_recovery_service.go`：离线、忙碌和休息不再触发已派会话自动恢复或重派。
- `service_analytics_capture_service.go`：Presence 变化不再启动派单或回收 Assignment。
- `conversation_dispatch_workbench_service.go`：可派状态不读取 Presence，人工指派继续复用既有账号、权限、组范围和服务范围校验。
- `conversation_service.go`：通用指派复用派单工作台的人工派发事务；主管自接管校验三项会话权限、当前租户、唯一客服组、待人工路由和账号状态，并以条件更新保证并发单赢家。
- `agent_team_scope_service.go`：当前指派人始终保留会话可见范围；门店员工号 OR 条件增加完整括号，避免 SQL 优先级扩大租户范围。
- 会话页：只有具备 `conversation.assign` 的账号展示接管按钮。

## 接管契约

主管自接管必须满足：

1. 操作者具备 `conversation.view`、`conversation.send`、`conversation.assign`。
2. 会话属于当前 `ActiveTenantID`，状态为待接入且未分配。
3. 路由为 `HQ_AGENTDESK_PENDING`，并且 `NeedHumanFollowUp=true`。
4. 路由只能解析出一个启用的综合客服组。
5. 客服组长只能接管自己负责的组；租户或平台管理员可接管当前租户内任意组。
6. 操作者账号启用；租户账号必须属于当前租户，平台账号必须已切入当前租户。

成功事务同时写入 Conversation、ConversationAssignment、ConversationEventLog 和 ConversationRouteState，提交后才发布既有 WebSocket 和 `ConversationAssignedEvent`。不修改公开 HTTP API、DTO、枚举或 WebSocket payload。

## 数据与迁移

- 不新增或修改 model。
- 不新增 AutoMigrate 字段或 DML migration。
- SQLite 和 MySQL 继续使用现有事务及条件更新语义。

## 权限

- Handler 继续要求 `conversation.assign`。
- 自接管 service 额外要求 `conversation.view` 和 `conversation.send`。
- 客服组长范围由 `AgentTeam.LeaderUserID` 限制；管理员按当前租户管理。
- 普通客服没有客服档案时不能领取待人工会话。

## 验证

新增或更新测试覆盖：

- 排班客服离线、忙碌或休息仍进入规则候选，事务提交不因 Presence 变化失败。
- Presence 变化不触发已派会话恢复。
- 人工指定离线或休息客服成功。
- 组长无客服档案接管本组成功，接管后列表可见且可回复。
- 平台管理员必须先切入有效租户，切入后可无客服档案接管。
- 跨组接管、普通客服无档案、AI/门店人工等错误路由被拒绝。
- 租户管理员接管成功；并发接管只有一个赢家和一条 Assignment。
- 派单工作台提供“我来接管”，原因必填，成功后直接进入会话工作台。

提交前执行：

```bash
go test -tags dev ./internal/services -count=1
go test ./... -count=1
go test -race ./internal/services -run 'ConversationDispatch|SupervisorTakeover|HumanDispatch' -count=1
go vet ./...
cd web && pnpm typecheck
git diff --check
```

实际结果：

- `go test -tags dev ./internal/services -count=1`：通过。
- `go test ./... -count=1`：通过。
- `go test -race ./internal/services -run 'ConversationDispatch|SupervisorTakeover|HumanDispatch' -count=1`：通过。
- `go vet ./...`：通过。
- `cd web && pnpm typecheck`：通过。
- `cd web && pnpm build`：通过，49 个静态页面生成成功。
- `cd web && pnpm lint`：0 个错误；32 条为仓库既有警告，本次修改文件的定向 lint 通过。
- `git diff --check`：通过。

## 并行分支与合并

- 共享高风险文件为 `conversation_service.go`、`agent_team_scope_service.go` 和会话页组件。
- `origin/main@b00c215` 仅修改侧边栏导航，与本分支无同文件冲突。
- 本变更不影响 AI 回复链、模型、计费、FastGPT、NewAPI 或企业微信协议。
- 建议本提交直接合入 `main`；其他会话权限或派单分支应在其后 rebase，保留“指派人始终可见”和“主管接管单赢家”语义。

## 回滚

- 恢复上一应用镜像或回退本提交即可。
- 无数据库迁移，无需回滚数据结构。
- 已产生的 Assignment 和事件属于真实人工接管审计记录，不做破坏性回填。
