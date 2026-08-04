# 门店员工会话工作台优化交接

## 目标

门店员工账号登录后直接进入会话工作台，只能查看和回复本人活动 `StoreStaffBindingID` 下的会话。门店内不同员工号互相隔离；企微实例更换后，会话历史仍归属于稳定 Binding，不随实例切换丢失。

## 归属规则

- `Store.ID`：稳定门店身份，承载门店知识库及门店级业务配置。
- `StoreStaffBinding.ID`：稳定门店员工号身份，是门店员工会话的权限归属主键。
- `WxWorkProtocolInstance.ID`：一次企微登录实例，可替换，不作为门店员工历史会话的权限主键。
- `Conversation.StoreStaffBindingID` 与 `ConversationRouteState.StoreStaffBindingID` 必须一致。
- 会话和 Route 的 `StoreID` 必须一致；企微实例的 Binding 与 Store 也必须同时匹配。
- 活动门店员工范围只接受 `user_id = active_user_id = 当前用户` 且 `status = ok` 的 Binding；异常数据一律 fail closed。

## 实现

### 权限和后端范围

- `store_staff` 内置角色增加 `conversation.view`、`conversation.send`。
- DML migration `000074` 幂等补齐既有门店员工角色的两项权限。
- `AgentTeamScopeService` 增加 Binding 级范围，并用于会话、企微实例、详情访问和 WebSocket 会话订阅。
- 纯门店员工账号按 Binding 限制；管理员、客服组长或客服兼有 `store_staff` 角色时保留其原有高层范围，避免新增角色导致权限收窄。
- 门店员工查看活动及关闭会话不依赖 `CurrentAssigneeID`，因此企微实例替换后仍可读取同一 Binding 的历史。
- `STORE_WECOM_MANUAL` 且未被总部客服占用时，门店员工可以从平台回复本人 Binding 会话；其他 Binding 继续拒绝。

### WebSocket

- 新增内部 topic：`admin:store-staff-binding:<bindingId>`。
- 纯门店员工的 dashboard WebSocket 默认只订阅本人活动 Binding topic，不订阅租户级或其他员工号 topic。
- 消息、会话和客户标签变化会发布到经 Conversation/Route 双重一致性验证后的 Binding topic。
- 会话变化 payload 以向后兼容方式新增 `storeStaffBindingId`，未改变现有字段语义。

### 前端

- 门店员工拥有会话权限后，默认导航优先进入 `/dashboard/conversations`。
- 会话页复用现有 `/api/dashboard/store-workbench/current` 获取本人账号、门店、企微健康状态和 AI 状态，不加载租户企微账号池。
- 桌面展示本人账号侧栏，移动端展示紧凑身份条；会话列表仍复用现有工作台组件。
- 门店员工不能切换实例级 AI 配置；AI 开关只展示当前工作台状态。
- 管理员、客服组长和客服即使兼有 `store_staff` 角色，仍使用原管理/客服会话界面。

## 数据与接口影响

- Model / DDL：无。
- DML migration：新增版本 `74`，只补内置角色权限关系，事务内幂等执行。
- HTTP API：无新增、无路径或 DTO 破坏；复用会话及门店工作台接口。
- WebSocket：内部新增 Binding topic；payload 仅新增可选 `storeStaffBindingId`。
- 前后端枚举：无变化。

## 验证覆盖

- 同门店两个 Binding 的会话、实例和 WebSocket 事件互不可见。
- 伪造其他实例筛选不泄漏数据。
- Binding/Store 不一致的污染实例被拒绝。
- 本人活动及关闭历史会话可见。
- 本人门店企微人工会话可回复，其他 Binding 不可回复。
- 管理员、客服组长、客服的原有范围不因兼有门店员工角色而缩小。
- 权限 migration 连续执行两次只生成一条关系。
- 前端导航、权限门禁、门店身份展示及 AI 开关只读行为有契约测试。

验证命令：

```bash
go test ./internal/services -count=1
go test ./internal/migration ./internal/bootstrap/... -count=1
go test ./... -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm lint
```

## 并行分支与合并

- 共享高风险文件：`agent_team_scope_service.go`、`conversation_service.go`、`message_service.go`、WebSocket payload/service、角色权限及 migration 注册表。
- 合并前必须重新 `git fetch origin`，核对远端 migration 版本及上述同文件修改。本次开发时 GitHub TLS 连接曾失败，不能以当时的远端引用证明没有新冲突。
- 建议先合并权限 migration 和 Binding 范围，再合并 WebSocket 与前端；不要只挑前端提交，否则门店员工会缺少后端权限和隔离保障。

## 回滚边界

- 回滚前端不会改变数据，但门店员工会失去专属身份视图。
- 回滚 Binding topic 会恢复租户级实时事件订阅，存在同租户其他员工号事件泄漏风险，不建议单独回滚。
- migration 已写入的权限关系在代码回滚后无结构影响；若业务明确要求撤销，需通过新的幂等 DML migration 删除指定角色与两项权限关系，禁止手工改生产数据库。
