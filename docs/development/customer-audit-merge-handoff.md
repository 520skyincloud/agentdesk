# 客服与对话审计分支合并交接

> 状态日期：2026-07-14
> 工作分支：`codex/customer-audit`
> Draft PR：<https://github.com/520skyincloud/agentdesk/pull/1>
> 并行分支：`codex/ai-billing`

## 1. 文档使用边界

- 本文只记录客服、派单、会话范围与仿真数据开发，不定义 AI 回复、token 或计费语义。
- 当前回复引擎以真实代码和 `docs/design/reply-runtime-engine.md` 为准。
- `docs/development-handoff.md`、`docs/wecom-hook-bridge.md` 和 `docs/generated/` 不能作为本分支实现依据。
- 文件清单和冲突情况必须在提交或合并前重新通过 Git 核对，不能只依赖本文快照。

## 2. 已提交范围

| Commit | 内容 | 主要边界 |
| --- | --- | --- |
| `0711997` | 客服/门店/客户测试主数据 Seed | 独立开发命令，不调用真实企微和 AI runtime |
| `277d288` | 会话派单工作台 | 复用 Conversation、Assignment、RouteState 和事件，不新增平行任务表 |
| `0053d5d` | 客服组企微账号范围管理 | 客服组配置页承载服务范围 |
| `27d8334` | 客服档案账号范围编辑 | 后续由统一客服组归属方案替代部分语义 |
| `c8c6879` | 移除客服个人账号范围 | 门店员工号属于客服组，不固定属于单个客服 |
| `514bcf7` | 客服组管理范围 | 管理员、组长、客服按既有权限和数据范围工作 |
| `8389ebb` | 权限接入原全局权限页 | 不建立派单专用平行权限体系 |
| `9ca4b2b` | 客服会话范围与待回复工作流 | 会话页负责实际回复，派单页负责任务编排 |

## 3. 拆分提交内容

### 门店员工客服组归属共享契约

本次共享契约提交包含：

- `StoreStaffBinding.AgentTeamID` 作为客服组归属事实源。
- `WxWorkProtocolInstance.AgentTeamID` 作为派单查询缓存，由服务层事务同步。
- 用户管理单个绑定 API 与客服组批量绑定 DTO。
- 客服组范围缓存根据门店员工归属重新生成。
- 历史客服组范围数据通过 migration `25/26` 幂等回填。
- 旧调用方只传 `wxWorkInstanceScopeIds` 时转换到真实门店员工；两个范围字段都未传时保留原归属，避免静默清空。
- 无法映射到真实门店员工的旧企微实例明确返回错误，不创建虚假归属。

### 双向绑定前端

双向绑定前端提交包含：

- 用户管理页展示门店、企微员工号和客服组归属，并支持单个门店员工选择客服组或暂不分配。
- 客服组编辑页使用双列选择器批量纳管门店员工。
- 左侧支持关键字、归属范围、公司和门店绑定状态筛选，右侧展示本组已选员工。
- 两列独立滚动，适配大量门店员工，不删除客服组侧批量入口。
- 两个入口调用同一后端事实源，保存后重新读取同步信息。

### 会话来源账号导航

会话导航提交只修改现有会话页：

- 在“全部账号”下选择客户会话时继续保留全部会话列表，不切换到账号过滤视图。
- 左侧高亮当前会话来源的企微员工号，并在需要时滚动到可见区域。
- 用户主动点击具体企微账号时，仍按原有逻辑筛选该账号下的客户会话。
- “我的待回复”统计按当前列表计算，不误用全部账号汇总数据。
- 不新增 API、DTO、数据库字段、权限或 WebSocket payload。

### 仿真会话与派单数据

仿真 Seed 提交扩展现有 `cmd/customer_audit_seed`：

- 生成 36 条客户会话、135 条消息和 21 条历史/当前 Assignment。
- 状态分布为 6 条 AI 接待、9 条待派发、18 条已派发处理中、3 条已关闭。
- 27 条会话最后一条为客户消息并标记需要人工回复，12 名客服都有当前任务。
- 消息参与者是客户、AI 和客服，不把门店员工模拟成客户聊天对象。
- AI 文案是带 `simulation=true` payload 的固定测试消息，不调用真实模型、不生成 token、不写计费、不投递企微 outbox，也不发布 WebSocket。
- Seed、report 和 cleanup 使用同一 batch 标识；cleanup 按依赖顺序删除仿真会话及关联数据，不删除非测试会话。

## 4. 数据与迁移

### 新增字段

| Model | 字段 | 类型与默认值 | 语义 |
| --- | --- | --- | --- |
| `StoreStaffBinding` | `AgentTeamID` | `bigint not null default 0`，索引 | 门店员工所属客服组；`0` 表示暂未分配，作为统一事实源 |
| `WxWorkProtocolInstance` | `AgentTeamID` | `bigint not null default 0`，索引 | 从门店员工绑定同步的查询缓存，不作为独立事实源 |

DDL 由 AutoMigrate 执行，兼容 SQLite 和 MySQL。DML 回填使用：

- `000037_backfill_wxwork_agent_team_bindings.go`
- `000038_backfill_store_staff_agent_team_bindings.go`

这两个迁移最初在客服分支使用 25/26；`codex/ai-billing` 后续占用 21-33 后，已在阶段 4 重编号为 37/38。执行过旧 25/26 的分支开发库不能直接与 AI 分支合并使用，必须重建或逐条核验后人工 remap，禁止直接删除生产迁移记录。

## 5. 接口与页面

### 已提交派单接口

- 任务列表与状态统计
- 客服实时负载
- 自动分配
- 手动派发
- 转派
- 释放回客服组待派发池

### 新增或扩展接口

- `POST /api/dashboard/user/bind_agent_team`：在用户管理中为门店员工设置客服组。
- 客服组创建/更新 DTO 增加 `storeStaffUserIds`，用于客服组侧批量绑定。

页面职责保持如下：

- 用户管理：单个门店员工反向选择客服组。
- 客服组档案：批量纳管门店员工，显示人员和轻量任务负载。
- 派单工作台：查看、自动派发、手动派发、转派和释放人工会话。
- 会话工作台：客服处理已经分配给自己的客户会话。

## 6. 与 `codex/ai-billing` 的重叠

截至 2026-07-13，双方提交或工作树涉及的共同文件包括：

```text
internal/bootstrap/routes.go
internal/bootstrap/server.go
internal/builders/conversation_builder.go
internal/models/models.go
web/app/dashboard/conversations/_components/conversation-info-panel.tsx
web/lib/api/admin.ts
web/lib/navigation.tsx
web/messages/en-US.json
web/messages/zh-CN.json
```

合并要求：

1. 不覆盖 `ai-billing` 对 AI runtime、计费 DTO 和模型字段的修改。
2. `models.go` 只保留本分支新增的两个客服组归属字段，并人工合并另一分支字段。
3. `routes.go`、`admin.ts`、导航和多语言文件按业务块合并，禁止整文件取一侧。
4. `conversation_builder.go` 与会话信息面板需逐字段核对 AI 审计字段和客服范围字段。
5. 合并前重新执行 `git fetch origin` 和同文件清单检查。

## 7. 旧文档与脚本审计

| 文件/目录 | 当前判断 | 当前引用 | 处置 |
| --- | --- | --- | --- |
| `docs/development-handoff.md` | 2026-06-30 的迁移与会话恢复说明，不是当前架构文档 | `scripts/restore_codex_session_backup.sh` 提示人工阅读 | 已增加历史资料警告，保留恢复内容 |
| `docs/wecom-hook-bridge.md` | 旧 Hook Bridge 接入说明，不能代表当前企微协议链路 | 多个 `start-wecom-hook-bridge*` 脚本仍调用 bridge | 已增加历史测试接入警告；部署引用未确认前不删除 |
| `docs/generated/` | 历史评测与临时产物 | 多个 reply runtime 评测脚本仍向该目录输出 | 已增加 README 和 Git 忽略规则，不作为产品依据，不默认提交报告 |
| `docs/design/reply-runtime-engine.md` | 当前分支的回复引擎设计说明 | 文档自身记录真实 runtime 验证 | 代码优先；`ai-billing` 当前删除该文件，合并前需由该负责人说明原因 |

代码中存在 FAQ 数据模型和知识库实现，不代表允许从旧文档恢复旧 FAQ 路径。任何调整必须先核对当前路由、RAG 和 runtime 调用关系。

## 8. 验证状态

已提交阶段曾通过定向 Go 测试、`pnpm typecheck`、目标 ESLint、Docker 构建和浏览器验证。

2026-07-13 对当前工作树重新执行并通过：

```bash
go test ./internal/services -run 'TestAgentTeam|TestStoreStaff|TestConversationDispatch' -count=1
go test -tags dev ./cmd/customer_audit_seed -count=1
pnpm --dir web typecheck
```

同时通过 `go test ./internal/migration` 编译检查，并确认当前分支 migration 注册版本无重复。合并 `codex/ai-billing` 后仍需再次执行 migration 编译和全链路测试。

共享契约还通过了旧 `wxWorkInstanceScopeIds` 兼容测试，覆盖旧字段转换、新字段双向同步、跨客服组移动和解除分配。

双向绑定前端已通过 `pnpm typecheck` 和目标 ESLint；页面视觉验证记录保留在本地 `.codex/audits/agent-team-binding/`，截图不进入 PR。

在 Compose 网络内对 `customer-audit-v1` 重新执行 Seed 和 report 后，真实测试库返回：36 条仿真会话、135 条消息、21 条 Assignment、27 条待人工、18 条当前任务覆盖 12 名客服，`expectedSimulationComplete=true` 且 `simulationBaselineIntact=true`。

综合客服组小组编排已在 1440x900 和 390x844 下验证。桌面端批量操作按钮完整可见；移动端无横向溢出，资源池和小组容器均可滚动到达。真实页面还验证了鼠标拖拽加入、批量加入、撤销、单人移除和排班弹窗预选，拖拽落库后页面无控制台错误。

拖拽交互使用页面级 `DragOverlay`，原客服条保留半透明占位，关闭拖拽自动滚动，并按鼠标指针判定目标小组。真实页面验证指定空小组从 0 变为 1 名成员，拖拽前后页面及滚动容器位置保持不变；临时验收小组随后已清理。

验收期间发现空小组响应把 `memberProfileIds` 序列化为 `null`，前端会在映射成员时崩溃。当前 builder 已固定返回空数组，前端保留防御性兼容，并新增 `TestBuildAgentTeamSquadListUsesEmptyMemberArray`。临时验收小组及成员关系已从测试库清理。

本功能相关 service、builder、handler、bootstrap 定向测试与编译均通过。全仓 `go test ./internal/...` 仍存在既有测试隔离失败：`TestBuildLightweightTicket` 未初始化全局 DB，异步 AI 回复测试会在其他测试关闭全局 DB 后继续访问；该问题不属于客服小组改动，且本分支不得越界修改 AI runtime。

## 9. 当前未完成能力

- 综合客服组下客服小组与排班联动已按 `docs/design/agent-team-squad-scheduling.md` 实现并完成桌面、移动端定向验收。
- 大模型统筹派发尚未接入；当前自动派发仍是确定性规则。
- 模型推荐理由、置信度、长期记忆和组长覆盖分析尚未实现。
- 通知和审计已有事件基础，但尚未形成完整派单审计报表。
- 客服组归属、双向绑定、会话导航和仿真数据均已按职责拆分；本地 `.codex/audits/` 仅是页面验证截图，不进入 PR。

## 10. 提交与回滚边界

- 协同规则、归属共享契约、双向绑定 UI、会话导航和仿真数据保持独立提交，便于 review 和 cherry-pick。
- 任何提交前逐文件暂存，禁止 `git add .`。
- 回滚双向绑定时不得删除已有门店员工、企微实例、客服组或会话数据；只能撤销新增字段使用和幂等回填逻辑。

## 11. 综合客服组与客服小组（2026-07-13）

### 数据与接口

- 新增 `AgentTeamSquad` 与 `AgentTeamSquadMember`，客服可在所属综合组内加入多个小组。
- `AgentTeamSchedule.SquadID=0` 表示全组值班，正数表示指定小组值班。
- `ConversationAssignment.SquadID` 保存派发小组快照；Conversation 仍只通过 `CurrentTeamID` 归属综合客服组。
- 小组 CRUD 与成员替换接口位于 `/api/dashboard/agent-team/squad/*`，复用既有 `agentTeam.*` 全局权限，不增加平行权限。
- DDL 仅通过 AutoMigrate；相关历史绑定 DML 最终使用 37/38，不与 `ai-billing` 的 21-33 冲突。

### 运行语义

- 当前班次为全组值班时保持原派单候选逻辑。
- 当前班次指定小组时，只允许该小组有效成员进入自动派单候选。
- 指定小组无候选人时任务留在综合组待派发池，不回退全组。
- 停用小组不参与派单；有当前或未来排班的小组不能停用、删除或迁移综合客服组。
- 主管手动派发可覆盖值班小组；会话已有综合组归属或已有客服可反推归属时，不能跨综合客服组。
- 已派发会话不随换班迁移；新任务和待派发任务使用新班次。

### 页面

- 客服档案页新增“客服成员 / 小组编排 / 服务范围”，原成员表和 CRUD 保持不变。
- 小组编排支持拖拽加入、复选批量加入、移除、撤销、新建/编辑/删除和排班跳转。
- 排班页新增可选小组，保持 `0=全组值班` 的兼容入口；列表与日历显示小组。
- 空小组响应统一为 `memberProfileIds: []`；桌面批量操作不再被下拉框挤压，移动端保留可滚动的非拖拽批量入口。

### 并行分支与合并顺序

- 同文件风险：`internal/models/models.go`、`internal/bootstrap/routes.go`、`web/lib/api/admin.ts`、`web/messages/zh-CN.json`、`web/messages/en-US.json`。
- 不涉及 AI runtime、供应商、token 或计费。
- 建议先合并小组模型/API，再合并排班与派单，最后合并两个前端步骤；禁止整文件覆盖 `ai-billing` 修改。

## 12. 多租户公司与邀请注册设计（2026-07-13）

### 设计依据

- 新增 `docs/design/multi-tenant-company-registration.md`，作为租户公司、账号、角色权限、邀请注册、公司切换、数据隔离和后台导航的后续实现基线。
- 本步骤只形成设计文档，没有修改 model、migration、DTO、接口、权限数据、页面或运行时。
- 现有 `Company` 同时承担客户企业、门店公司和公司模型范围，后续不得直接改名为租户；设计采用独立 `Tenant`，原 `Company` 收敛为租户内客户企业。
- 页面采用保守显示策略：模块和必要信息按查看权限保留，创建、编辑、删除、分配、导出、重置等选项按动作权限隐藏；后端仍逐接口鉴权。

### 已确认权限边界

- 账号只绑定角色，角色绑定权限；后续审计并废止 `UserPermission` 账号级例外权限。
- 新增公司主管 `tenant_admin`，位于 `admin` 与 `cs_team_leader` 之间，只管理本租户。
- 管理员及以上可以为低级角色配置权限；公司主管只能为本公司账号分配下级角色。
- 前端角色 URL 白名单属于隐藏权限，后续改为查看权限和动作权限。
- `RolePostUpdate_sort` 需补 `role.update`；无真实调用的 `permission.sync` 随权限基础阶段删除。
- 新增租户、邀请码和注册审核权限时，通过独立幂等 DML migration 同步到全局权限管理。

### 后续共享风险与合并顺序

- 高风险文件：`internal/models/models.go`、认证 DTO/service、`internal/bootstrap/routes.go`、`internal/bootstrap/server.go`、`web/lib/api/client.ts`、`web/lib/api/admin.ts`、`web/lib/navigation.tsx` 和多语言资源。
- Qdrant payload/filter、AIConfig、公司模型设置和计费边界必须与 `codex/ai-billing` 负责人共同确认，客服分支不得单独修改 AI runtime 或计费语义。
- 建议先合并独立租户/权限共享契约，再让两个分支 rebase，之后分别实现客服业务隔离与 AI/计费租户维度。
- 正式启用公司切换和公开注册前，必须完成列表、详情、写操作、WebSocket、回调、Outbox、向量和文件的双租户隔离验收。

## 13. 多租户阶段 1：权限基础清理（2026-07-13）

### 本步骤目标与结果

- 新增 `tenant_admin` 公司主管角色，固定层级为 `super_admin=100`、`admin=80`、`tenant_admin=60`、`cs_team_leader=40`、成员角色 `=20`。
- Role 新增 `scope`、`authority_level`，Permission 新增 `scope`；排序字段 `sort_no` 继续只承担页面顺序。
- 新增接入公司、邀请码和注册审核权限，所有权限继续由全局权限管理页展示。
- 账号仍只分配角色；认证不再合并 `t_user_permission` 账号级允许/拒绝记录。
- 删除无真实路由和调用的 `permission.sync`，角色排序接口补 `role.update`。
- 前端导航取消客服角色 URL 白名单，统一按 `*.view` 权限；用户与角色页面按动作权限和目标层级隐藏操作。

### 账号与角色安全边界

- 操作者只能分配和管理低于自身最高等级的角色，平台角色只能由超级管理员分配。
- 不能通过用户管理修改自己的角色、密码、状态或删除自己；自己的基础资料仍可编辑。
- 创建账号时若同时携带角色，除 `user.create` 外还必须具有 `user.assignRole`。
- 更新、停用、删除、重置密码和角色分配都会拒绝包含同级或更高等级角色的目标账号。
- 租户角色不能绑定 `scope=platform` 的权限，前后端均做限制。

### 数据与迁移

- DDL 由 `AutoMigrate` 增加 Role/Permission 字段；DML 使用 `000034_sync_tenant_auth_foundation.go` 同步内置角色、权限和默认关系。
- migration 34 先统计 `t_user_permission`。若存在历史记录则返回错误、保留全部记录并阻止服务带着权限语义变化启动；记录为空时不删除物理表，只移除运行时依赖。
- migration 34 删除 `permission.sync` 及其角色关系，迁移保持幂等。

### 主要文件

```text
internal/models/models.go
internal/pkg/constants/auth.go
internal/migration/000002_init_auth_data.go
internal/migration/000034_sync_tenant_auth_foundation.go
internal/services/auth_service.go
internal/services/role_service.go
internal/services/user_service.go
internal/handlers/dashboard/role_handler.go
internal/handlers/dashboard/user_handler.go
internal/builders/user_builder.go
internal/pkg/dto/request/admin_request.go
internal/pkg/dto/response/admin_response.go
web/lib/navigation.tsx
web/components/app-sidebar.tsx
web/lib/api/admin.ts
web/app/dashboard/roles/*
web/app/dashboard/permissions/page.tsx
web/app/dashboard/users/*
web/messages/zh-CN.json
web/messages/en-US.json
```

删除运行时文件：

```text
internal/repositories/user_permission_repository.go
internal/services/user_permission_service.go
```

### 验证证据

```text
go test ./internal/handlers/dashboard ./internal/migration -count=1
go test ./internal/services -run 'Test(RoleAuthorityAssignmentMatrix|UserServiceAssignRolesEnforcesAuthority|UserServicePrivilegedMutationsEnforceAuthority|RoleServiceTenantRoleRejectsPlatformPermission|TenantAdminCreatesAccountWithLowerRoleOnly|AuthServiceIgnoresLegacyAccountPermissionOverrides)$' -count=1
cd web && pnpm typecheck
cd web && pnpm eslint <本阶段目标文件>
git diff --check
```

- Handler、migration 和阶段 1 service 测试通过。
- 前端类型检查通过；目标 lint 只有 `app-sidebar.tsx` 既有 `<img>` 性能警告，无错误。
- 全量 `go test ./internal/services` 仍会被既有异步 AI 回复测试在测试结束后访问已清空的全局 `sqls.DB()` 触发 nil panic；栈位于 `TriggerReplyAsync -> BuildRuntimeAIAgentForConversation`，与本阶段权限代码无关，不能作为阶段 1 通过证据，也不得隐瞒。

### 并行分支影响与合并顺序

- 阶段 1 最初提交时使用 migration 27；并行分支后续占用 25-28、30-33 后，本分支在阶段 2 将权限迁移重编号为 34，避免最终树出现重复版本。
- 高风险同文件为 `models.go`、`web/lib/api/admin.ts`、导航和双语资源；AI 分支当前最新提交未修改本阶段字段语义，但合并前仍需再次 fetch 和逐文件核对。
- 本阶段不修改 AI runtime、供应商配置、token 统计、计费口径、会话/消息状态或 WebSocket payload。
- 建议先合并本阶段权限共享契约，再由客服和 AI 分支 rebase；阶段 2 才增加 Tenant 和认证上下文。
- 回滚边界：可以整体回滚阶段 1 代码和 migration 34；空的 `t_user_permission` 物理表保留，不做破坏性 DDL。

## 14. 多租户阶段 2：Tenant 与认证上下文（2026-07-13）

### 本步骤目标与结果

- 新增独立 `Tenant`、`TenantInvitation`、`TenantRegistrationLog` 模型及基础 repository/service，为后续公司创建与邀请注册提供契约；注册安全日志只提供创建和查询，邀请码历史不提供物理删除。本阶段没有开放相关 CRUD 或公开注册路由。
- `User` 增加 `TenantID`、注册来源、审核状态、审核信息和首次改密标记。
- 登录上下文增加 `TenantID`、`ActiveTenantID`、`CanSwitchTenant`、`IsPlatformAccount`；平台账号只有持有 `tenant.switch` 才能使用 `X-Tenant-ID` 进入公司上下文，租户账号始终固定在自身租户。
- 前端统一请求客户端自动携带经本标签页校验的 `X-Tenant-ID`；活动公司使用 `sessionStorage` 保存，避免两个浏览器标签相互覆盖公司上下文。
- 用户列表、全量下拉、详情、更新、停用、删除、重置密码和角色分配均先按租户范围定位目标账号；跨租户 ID 按不存在处理，不能通过错误差异确认其他公司账号。
- 账号角色作用域形成双保险：迁移拒绝历史混合角色，登录拒绝运行时混合角色，角色分配拒绝平台账号绑定租户角色或租户账号绑定平台角色。

### 数据与迁移

- DDL 继续由 `AutoMigrate(models.Models...)` 创建 Tenant 表并扩展 User。
- migration 35 创建 `legacy-default` 历史默认公司：平台角色账号回填为 `TenantID=0`，其余历史账号回填到默认公司。
- migration 35 遇到同时持有启用平台角色和启用租户角色的账号时中止，不猜测归属；重复执行只处理仍为历史默认字段的账号，不覆盖之后创建的邀请账号或其他租户账号。
- 阶段 1 migration 从 27 重编号为 34，阶段 2 使用 35。原因是 `origin/codex/ai-billing@a936a18` 已使用 25-28、30-33。
- 曾在本分支旧提交上实际执行 migration 27 的开发数据库带有分支专属版本记录，不能直接作为最终合并数据库使用；合并前应备份并重建开发库，或逐条核对 `t_migration` 后人工修复，禁止直接删除生产迁移记录。

### 兼容边界

- 本阶段只完成认证和账号管理范围，不代表客户、门店、企微员工号、会话、消息、派单、工单、知识库、文件、WebSocket、Outbox 或 Qdrant 已完成租户隔离。
- OIDC 与企业微信登录为保持现有自动建号链路，暂时把新账号归入 `legacy-default` 并直接审核通过；阶段 3 在开放邀请注册前必须改为通过可信租户映射建号，禁止 SaaS 新租户继续落入历史默认公司。
- `X-Tenant-ID` 只是平台管理员当前选择，不是授权凭证；后端每次请求都会重新验证切换权限、租户状态和账号作用域。
- 旧前端本地登录会话缺少租户字段时，首次 profile 刷新不携带公司头，后端返回完整上下文后覆盖旧会话格式。

### 主要文件

```text
internal/models/models.go
internal/pkg/enums/tenant.go
internal/pkg/dto/dto.go
internal/pkg/dto/response/auth_response.go
internal/pkg/dto/response/admin_response.go
internal/migration/000034_sync_tenant_auth_foundation.go
internal/migration/000035_backfill_tenant_auth_context.go
internal/repositories/tenant*_repository.go
internal/services/tenant*_service.go
internal/services/auth_service.go
internal/services/user_service.go
internal/services/oidc_login_service.go
internal/services/wxwork_login_service.go
internal/handlers/dashboard/user_handler.go
internal/middleware/auth_middleware.go
web/lib/auth.ts
web/lib/api/client.ts
web/components/auth-provider.tsx
web/lib/api/admin.ts
web/lib/generated/enums.ts
```

### 验证范围

```text
go test ./internal/services -run '<认证上下文、账号租户范围、角色作用域、OIDC 兼容测试>' -count=1
go test ./internal/migration ./internal/middleware -run '<migration 34/35 与租户错误保留测试>' -count=1
go test ./internal/services ./internal/migration ./internal/handlers/dashboard ./internal/middleware -run '^$' -count=1
cd web && pnpm typecheck
cd web && pnpm eslint components/auth-provider.tsx lib/api/client.ts lib/auth.ts lib/api/admin.ts lib/generated/enums.ts
git diff --check
```

- migration、middleware、dashboard handler 和阶段 2 聚焦 service 测试通过。
- `go test ./... -run '^$' -count=1` 全仓编译级验证通过。
- `pnpm typecheck` 与阶段目标文件 eslint 通过；`make enums` 后生成文件稳定。
- 全量前端 `*.test.mjs` 为 42/43：唯一失败是既有 `components/nav-main.test.mjs` 仍精确匹配无属性的 `<SidebarMenuButton />`，而当前已提交组件带 `className="rounded-xl"`；该文件不在阶段 2 差异中，本阶段未混入无关修复。

### 并行分支影响与合并顺序

- `origin/codex/ai-billing@a936a18` 与本阶段共同修改 `internal/models/models.go`、`internal/pkg/dto/response/auth_response.go`、`internal/services/wxwork_login_service.go` 和 `web/lib/api/admin.ts`，均为不同字段的相邻新增，合并时必须逐段保留双方内容，不能整文件选边。
- AI 分支给 `User` 增加 `EmailVerifiedAt`，给认证选项增加邮箱验证码字段，并重构企微邮箱绑定；租户分支增加租户字段并为企微新账号选择租户。最终合并后的企微建号必须同时保留邮箱验证和租户归属。
- 本阶段不修改模型调用、AI 回复链路、模型供应商、token 统计、FastGPT 或计费口径。
- 由于阶段 2 同时承担阶段 1 migration 重编号，不应单独 cherry-pick `12d77b9`；应合并阶段 1 与阶段 2 的最终树，再让并行分支 rebase。
- 回滚阶段 2 时可移除 Tenant 认证上下文代码，但已生成的 Tenant/User 新列由 AutoMigrate 保留；不得通过破坏性 DDL 回滚。

## 15. 多租户阶段 3A：接入公司与邀请码管理后端（2026-07-13）

### 本步骤目标与结果

- 新增接入公司列表、详情、创建、更新和启停接口；接口分别使用 `tenant.view/create/update/updateStatus`。
- 接入公司列表和详情除 `tenant.view` 外还硬性要求平台账号，防止权限误配或历史脏关系让租户账号读取平台公司目录。
- 创建接入公司在单个事务内创建 Tenant、已审核公司主管账号、`tenant_admin` 角色关系、默认“综合客服组”和首个邀请码，任一步失败全部回滚。
- 公司主管初始密码只在创建成功响应返回一次并要求首次改密；创建和邀请码查看/重置响应设置 `Cache-Control: no-store`。
- 邀请码使用密码学安全随机数生成，SHA-256 哈希用于后续匹配，AES-256-GCM 密文用于受权查看；密文只接受唯一规范的 URL-safe Base64 表示。
- 邀请密钥支持 YAML 配置和 `AGENT_DESK_INVITATION_ENCRYPTION_KEY` 环境变量覆盖，生产环境不得复用登录或客服会话密钥。
- 当前公司邀请码使用 `tenantInvite.view/rotate`；重置时一次禁用该租户全部旧有效邀请码并按历史最高版本递增，旧记录保留审计字段，不提供物理删除。
- 统一社会信用代码执行 18 位合法字符格式校验，`registration_type + registration_no` 由数据库组合唯一索引兜底；这里只证明格式和系统内唯一，不声称完成第三方工商核验。

### 数据、接口与迁移

- `AgentTeam` 新增 `TenantID` 和 `IsDefault`；新公司默认综合客服组写入明确 TenantID。
- `User` 增加 `ApprovalRemark`；`TenantRegistrationLog` 增加 `Action` 和 `InviteHash`，供阶段 3B 记录注册安全动作。
- migration 36 只把 `tenant_id=0` 的历史客服组回填到 `legacy-default`，保留已有显式租户，重复执行幂等；缺少历史默认租户时中止。
- 新增路由：
  - `GET /api/dashboard/tenant/list`
  - `GET /api/dashboard/tenant/:id`
  - `POST /api/dashboard/tenant/create`
  - `POST /api/dashboard/tenant/update`
  - `POST /api/dashboard/tenant/update_status`
  - `GET /api/dashboard/tenant-invitation/current`
  - `POST /api/dashboard/tenant-invitation/rotate`
- CORS 允许并暴露 `X-Request-Id` 和 `X-Tenant-ID`，供后续公开注册幂等和平台公司上下文使用。

### 主要文件

```text
config/config.example.yaml
internal/models/models.go
internal/pkg/config/config.go
internal/pkg/dto/request/tenant_request.go
internal/pkg/dto/response/tenant_response.go
internal/repositories/tenant_repository.go
internal/repositories/tenant_invitation_repository.go
internal/repositories/user_repository.go
internal/services/user_service.go
internal/services/tenant_management_service.go
internal/services/tenant_invitation_business_service.go
internal/services/tenant_invitation_crypto.go
internal/builders/tenant_builder.go
internal/handlers/dashboard/tenant_handler.go
internal/handlers/dashboard/tenant_invitation_handler.go
internal/bootstrap/routes.go
internal/bootstrap/server.go
internal/migration/000036_backfill_agent_team_tenants.go
```

### 验证证据

```text
go test ./internal/pkg/config -count=1
go test ./internal/services -run 'TestTenant(Service|Invitation|Management)' -count=1
go test -race ./internal/services -run 'TestTenant(Service|Invitation|Management)' -count=1
go test ./internal/migration -run 'Test(BackfillAgentTeamTenants|BackfillTenantAuthContext|SyncTenantAuthFoundation)' -count=1
go test ./internal/handlers/dashboard -run 'Test(Tenant|RoleUpdateSort|UserCreateWithRoles)' -count=1
go test ./internal/bootstrap ./internal/handlers/dashboard -count=1
go test ./cmd/server ./cmd/migration -count=1
git diff --check
```

- 上述阶段测试、竞态检测和编译检查通过。
- 全量 `go test ./internal/... -count=1` 仍在既有 `TestBuildLightweightTicket` 失败：测试未初始化全局 DB，`BuildCustomer -> ListStoreRelations` 发生 nil pointer。
- 单独执行 `go test ./internal/services -count=1` 还会稳定失败于 `TestStoreManualAgentReplyStartsIdleTimeout`、`TestManualSessionTimeoutRestoresStoreManualAfterAgentReplyWithCustomerNotice` 和 `TestConversationHumanDispatchStoreManualAllowsWebReplyWithoutClaim`；现象是共享测试 DB/会话 fixture 未形成预期人工分配或门店范围。本步骤未修改 Ticket、Customer builder、人工会话回复或 AI runtime，不能把这些失败算作阶段 3A 通过，也不越界夹带修复。

### 未完成边界

- 阶段 3A 没有开放 `/register`、邀请码公开校验、注册提交、待审核列表或审核接口；限流、请求幂等和注册安全日志写入属于阶段 3B。
- `AgentTeam.TenantID` 只是默认资源和后续隔离契约。客服组列表/详情/写操作，以及小组、排班、门店员工、派单等完整租户隔离必须在独立步骤逐链路完成，不能以 migration 36 代替运行时隔离。
- 正式启用公司切换和公开注册前，仍需完成客户、门店、企微、会话、消息、工单、知识库、文件、WebSocket、回调、Outbox 与向量检索隔离验收。

### 并行分支影响与合并顺序

- 本步骤开始时已 `git fetch origin`；`origin/codex/ai-billing@a936a18` 的 migration 最高为 33，本步骤使用 36，不重复。
- 同文件为 `config/config.example.yaml`、`internal/pkg/config/config.go`、`internal/models/models.go`、`internal/bootstrap/routes.go`、`internal/bootstrap/server.go` 和 `internal/bootstrap/server_route_test.go`。
- AI 分支在配置中新增 Email/FastGPT/NewAPIUsage，在模型和路由中新增邮箱验证、FastGPT 和回复运行时能力；最终合并必须逐段保留双方字段与路由，不得整文件选边。
- 本步骤不修改 AI runtime、模型供应商、FastGPT 运行语义、token 统计、计费、会话消息状态或 WebSocket payload。
- 建议先合并阶段 1-3A 的租户共享契约，再让并行分支 rebase；阶段 3B 继续依赖当前 Tenant/User/Invitation 契约。
- 回滚边界：可回滚阶段 3A 路由、service 和 DTO；AutoMigrate 新列及已创建 Tenant 数据不得用破坏性 DDL 删除，migration 36 已回填的历史客服组需按备份和明确映射处理。

## 16. 多租户阶段 3B：公开邀请注册与租户内审核后端（2026-07-14）

### 本步骤目标与结果

- 新增公开邀请码校验和注册接口，以及租户内待审核账号列表、通过/拒绝审核接口；公开 handler 只解析请求，注册事务、限流、幂等、角色边界和安全日志均在 service。
- 公开注册由 `tenantRegistration.enabled` 和 `AGENT_DESK_TENANT_REGISTRATION_ENABLED` 控制，默认关闭。启用时会校验阶段 3A 的独立邀请码加密密钥，无有效密钥时服务拒绝启动。
- Gin 默认关闭代理信任，`server.trustedProxies` 只接受显式 IP/CIDR；这保证登录、验证码和邀请注册不能通过伪造 `X-Forwarded-For` 绕过 IP 限流。
- 注册请求必须携带调用方生成的 `X-Request-Id`。安全日志新增 HMAC 请求指纹、审核操作者和客户端 IP 索引；密码、邀请码和账号明文不进入日志。同一请求标识修改请求内容会被拒绝。
- 注册事务原子创建绑定邀请码租户的禁用待审核账号、增加邀请码使用次数并写成功日志。账号初始无角色，登录被拒绝；精确重放返回原账号，不重复增加使用次数。
- 注册按客户端 IP、邀请码和账号标识三个维度限流；数据库查询错误只写服务端结构化日志，对外返回统一业务错误。
- 审核限制在操作者 `ActiveTenantID` 内。通过审核必须同时拥有 `tenantRegistration.review` 与 `user.assignRole`，只能分配本租户且低于操作者授权等级的角色；拒绝必须填写原因且不能带角色。审核完成后撤销账号已有会话。
- 注册动作和审核决定使用后端枚举，并通过 `make enums` 生成到前端共享枚举文件。

### 数据、接口与配置

- `TenantRegistrationLog` 新增 `RequestFingerprint`、`OperatorID`、`OperatorName`，`Action` 改为后端枚举类型；DDL 由 `AutoMigrate` 处理，本步骤没有 DML migration。
- 新增路由：
  - `POST /api/auth/register/validate_invite`
  - `POST /api/auth/register`
  - `GET /api/dashboard/tenant-registration/list`
  - `POST /api/dashboard/tenant-registration/review`
- 公开注册路由只在开关启用时挂载；后台审核路由始终挂载并继续由认证、租户上下文和权限控制。
- 新增配置：`tenantRegistration.enabled`、`server.trustedProxies`。公开注册响应和邀请码校验响应均设置 `Cache-Control: no-store`。

### 主要文件

```text
config/config.example.yaml
internal/bootstrap/routes.go
internal/bootstrap/server.go
internal/models/models.go
internal/pkg/config/config.go
internal/pkg/enums/tenant.go
internal/pkg/dto/request/tenant_registration_request.go
internal/pkg/dto/response/tenant_registration_response.go
internal/repositories/login_session_repository.go
internal/repositories/tenant_invitation_repository.go
internal/repositories/tenant_registration_log_repository.go
internal/repositories/user_repository.go
internal/services/tenant_invitation_crypto.go
internal/services/tenant_registration_business_service.go
internal/builders/tenant_registration_builder.go
internal/handlers/api/tenant_registration_handler.go
internal/handlers/dashboard/tenant_registration_handler.go
web/lib/generated/enums.ts
```

### 验证范围

```text
go test ./internal/pkg/config ./internal/bootstrap -run 'Test(Load|NewServer|ConfigureTrustedProxies)' -count=1
go test ./internal/handlers/api ./internal/handlers/dashboard -run 'TestTenantRegistration' -count=1
go test ./internal/services -run '^TestTenantRegistration' -count=1
go test ./internal/services -run '^TestTenantRegistrationConcurrentReplayCreatesOneAccount$' -count=20
go test -race ./internal/services -run '^TestTenantRegistration' -count=1
make enums
```

- 已覆盖默认关闭、缺少密钥拒绝启动、可信代理、敏感响应不缓存、显式幂等键、邀请码生命周期、三维限流、并发重放、待审核登录拒绝、租户边界、角色授权等级和审核权限组合。
- 共享租户测试 helper 改为每次创建唯一 SQLite 内存库并在测试结束时关闭连接，消除 `-count` 重复运行造成的角色 fixture 污染。
- `go vet ./...`、`go test ./... -run '^$' -count=1`、`cd web && pnpm typecheck` 和生成枚举文件 eslint 通过。
- 全量 `go test ./... -count=1` 仍失败于阶段 3A 已记录的两个既有测试问题：`TestBuildLightweightTicket` 未初始化全局 DB；部分消息测试启动异步 AI 回复后过早清空全局 DB，后台 goroutine 在 `BuildRuntimeAIAgentForConversation` 访问 nil DB。阶段 3B 未修改 Ticket builder、AI runtime 或消息触发链路，不能把这两项记录成通过，也不越界夹带修复。

### 未完成边界

- 本步骤只完成阶段 3B 后端；`/register` 页面、账号页邀请浮窗和待审核 UI 仍在阶段 7，不提供半成品入口。
- 公开注册开关必须保持关闭，直到阶段 4-6 完成业务数据及非 HTTP 链路隔离，并通过双租户验收。
- OIDC 与企业微信自动建号仍按阶段 2 的兼容逻辑进入历史默认租户；在 SaaS 开放前必须改为可信租户映射，不能把邀请码注册误当成第三方登录隔离已完成。

### 并行分支影响、合并顺序与回滚

- 本步骤开始时已 `git fetch origin`；`origin/codex/ai-billing@f2d2da4` 与本步骤共同修改 `config/config.example.yaml`、`internal/pkg/config/config.go`、`internal/models/models.go`、`internal/bootstrap/routes.go`、`internal/bootstrap/server.go` 和 `internal/bootstrap/server_route_test.go`。
- 合并时必须同时保留 AI 分支的 `User.EmailVerifiedAt`、邮箱验证码/FastGPT/NewAPI 配置、AI 模型与回复路由，以及本分支的租户注册字段、公开邀请路由和可信代理配置；不得整文件选边，也不得恢复已移除的账号级 `UserPermission`。
- 邀请注册创建的账号必须保持 `EmailVerifiedAt=nil`，后续邮箱验证语义由 AI/计费分支负责人共同确认后再调整。
- 本步骤不修改 AI runtime、模型调用、供应商、FastGPT、token 统计、计费、消息状态或 WebSocket payload。建议先合并阶段 1-3B 的租户认证共享契约，再让并行分支 rebase，之后继续阶段 4 的业务模型租户字段。
- 回滚时可关闭注册开关并回滚路由/service；AutoMigrate 已增加的安全日志列保留，不执行破坏性 DDL。后台审核路由可独立保留，不影响平台管理员后台创建账号。

## 17. 多租户阶段 4A：客服组织租户字段与历史回填（2026-07-14）

### 本步骤目标与结果

- 为 `AgentProfile`、`AgentTeamSquad`、`AgentTeamSquadMember`、`AgentTeamSchedule` 增加 `TenantID`；`AgentTeam` 已在阶段 3A 增加该字段，继续作为客服组织的租户父级。
- `AgentTeam` 页面创建现在必须存在 `ActiveTenantID`，并从认证上下文写入租户；公司主管被纳入客服组织管理员，但只能管理本租户客服组。平台管理员管理非历史客服组时也必须先进入对应公司。
- 客服档案创建/更新同时校验账号与客服组属于同一租户，并从客服组写入 TenantID。
- 客服小组、成员关系、单条排班和批量排班均从客服组继承 TenantID；跨租户账号、客服组、小组、成员或排班关系在 service 层拒绝。
- 通用排班 `Create/Update` 同样补齐父级校验，避免绕过后台业务方法创建零租户排班。
- `cmd/customer_audit_seed` 与 `cmd/testdata/agentteam` 明确使用 `legacy-default`，写入用户、客服组和客服档案租户字段；测试数据不再把平台超级管理员当作租户客服组长。

### Migration 与数据安全

- 原客服分支 migration 25/26 与 `codex/ai-billing` 的 25/26 语义冲突，最终重编号为：
  - 37：企微员工号客服组绑定回填。
  - 38：门店员工客服组绑定回填。
  - 39：客服档案、小组、成员和排班租户回填。
- migration 39 先由账号/客服组确定历史记录租户，再验证全部显式非零值和父子关系；客服组长账号错租户、客服账号与客服组冲突、跨租户小组成员、排班引用其他客服组小组或父记录缺失时整笔事务回滚，不猜测归属。
- migration runner 新增“数据库已执行记录的 version + remark 必须匹配当前代码定义”校验，防止旧分支开发库把同版本的另一迁移静默跳过；同时修正迁移记录写入失败日志引用错误变量的问题。
- DDL 仍由 AutoMigrate 增加兼容 SQLite/MySQL 的 `bigint not null default 0` 索引字段；本步骤不删除旧列或索引。

### 主要文件

```text
internal/models/models.go
internal/services/agent_profile_service.go
internal/services/agent_team_service.go
internal/services/agent_team_scope_service.go
internal/services/agent_team_squad_service.go
internal/services/agent_team_schedule_service.go
internal/migration/migration.go
internal/migration/000037_backfill_wxwork_agent_team_bindings.go
internal/migration/000038_backfill_store_staff_agent_team_bindings.go
internal/migration/000039_backfill_agent_organization_tenants.go
cmd/customer_audit_seed/main.go
cmd/testdata/agentteam/init.go
cmd/testdata/README_AGENTTEAM.md
```

### 验证与未完成边界

```text
go test ./internal/migration -run 'Test(BackfillAgentOrganizationTenants|ValidateMigrationDefinition)' -count=1
go test ./internal/services -run 'Test(AgentTeamScope|TenantAdminCreates|BindStoreStaff|UpdateAgentTeam|AgentTeamSquad|BuildAgentProfileModel|AgentTeamScheduleService)' -count=1
go test ./cmd/customer_audit_seed ./cmd/testdata ./cmd/testdata/agentteam -run '^$' -count=1
```

- 已覆盖回填幂等、显式租户保留、账号/客服组冲突回滚、跨租户成员回滚、排班小组冲突回滚，公司主管当前租户创建/管理边界，以及客服档案、小组成员、单条和批量排班的新写入租户继承。
- `go test -race` 聚焦用例、`go vet ./...`、`go test ./... -run '^$' -count=1` 和 `cd web && pnpm typecheck` 通过。完整 `internal/services` 中本步骤相关测试全部通过，包最终仍因阶段 3B 已记录的异步 AI 回复测试清库竞态而失败，本步骤未改 AI runtime。
- 本步骤是阶段 4 的首个低冲突批次，只完成客服组织数据契约和写入一致性。列表、详情、统计、派单和排班查询的强制 `tenant_id` 过滤属于阶段 5，当前不能据此宣布客服组织运行时隔离完成。
- 客户、客户企业、门店、企微员工号、会话、消息、派单、工单、知识库、文件、WebSocket、回调、Outbox 和向量检索仍待后续批次。

### 并行分支与回滚

- 开始和 migration 重编号前均已核对 `origin/codex/ai-billing@f2d2da4`；该分支 migration 最高为 33，本分支最终从 34 连续使用至 39。
- 本步骤与 AI 分支仅在 `internal/models/models.go` 同文件重叠，字段位于客服组织模型；不修改 `EmailVerifiedAt`、AI 模型、回复引擎、FastGPT、token 或计费语义。合并时必须保留双方字段。
- 含旧客服分支 migration 25/26 成功记录的数据库，在合并 AI 分支后会被定义校验明确阻止启动；开发环境优先备份后重建，其他环境必须逐条核对 remark 和真实数据效果后制定 remap，不自动修复。
- 回滚代码时可移除新写入校验，但 AutoMigrate 新列和已回填 TenantID 保留；不得用破坏性 DDL 清列或删除迁移历史。

## 18. 多租户阶段 4B：快捷回复租户隔离（2026-07-14）

### 本步骤目标与结果

- 为 `QuickReply` 增加 `TenantID`，并完成快捷回复后台真实运行链路的租户隔离。
- 创建从 `AuthPrincipal.ActiveTenantID` 写入租户；没有当前公司上下文时拒绝创建，不接收前端租户字段。
- 分页列表、可用快捷回复列表、详情、更新和删除均按当前租户查询。跨租户 ID 表现为不存在，最终更新/删除 SQL 也带 `tenant_id` 条件。
- 沿用现有快捷回复页面、权限和 API，不新增平行入口，不改变 response DTO；前端统一 API client 已携带 `X-Tenant-ID`，无需页面改动。

### Migration 与数据安全

- migration 40 将历史 `tenant_id=0` 的快捷回复回填至 `legacy-default`，保留引用现存租户的显式归属。
- 缺少历史默认租户或存在指向不存在租户的记录时，迁移失败并整体回滚；迁移可重复执行。
- `cmd/testdata/quickreply` 固定写入 `legacy-default`。若测试数据固定 ID 已归属其他租户则报错停止，禁止覆盖。
- DDL 由 AutoMigrate 增加 `bigint not null default 0` 索引字段；不删除旧列，不改变前端字段。

### 主要文件

```text
internal/models/models.go
internal/repositories/quick_reply_repository.go
internal/services/quick_reply_service.go
internal/handlers/dashboard/quick_reply_handler.go
internal/migration/000040_backfill_quick_reply_tenants.go
cmd/testdata/quickreply/init.go
```

### 验证与未完成边界

```text
go test ./internal/migration -run 'TestBackfillQuickReplyTenants' -count=1
go test ./internal/services -run '^TestQuickReplyService' -count=1
go test ./internal/handlers/dashboard ./cmd/testdata ./cmd/testdata/quickreply -run '^$' -count=1
```

- 双租户测试覆盖创建归属、列表/详情隔离、跨租户更新/删除拒绝、同租户更新/删除和无租户上下文拒绝；迁移测试覆盖幂等、显式归属保留和异常引用整笔回滚。
- 已扫描运行时引用并删除快捷回复 service 中无人使用的全局 CRUD 包装，后台 handler 只能调用显式租户方法；repository 的通用 CRUD 仅供迁移和测试数据等内部链路使用。
- `go test ./internal/migration -count=1`、聚焦 `go test -race`、`go vet ./...`、`go test ./... -run '^$' -count=1` 和 `cd web && pnpm typecheck` 通过。`go test ./... -count=1` 仍被既有 `TestBuildLightweightTicket` 未初始化全局 DB，以及三个门店人工回复测试夹具缺少当前角色/客服组服务范围阻断；均不在快捷回复运行链路，后者应作为独立测试修复提交处理。
- `Tag` 没有在本批单独租户化。它同时参与 `ConversationTag` 和 `TicketTag`，必须等会话、工单具备租户所有权后一起回填和校验，避免形成半隔离关系。
- 本步骤不代表客户、门店、企微实例、会话、消息、派单、工单、知识库、文件、WebSocket、回调、Outbox 或向量检索已经隔离；公开邀请注册继续保持关闭。

### 并行分支与回滚

- 开始本步骤时已核对 `origin/codex/ai-billing@f2d2da4`。该分支没有修改快捷回复 handler/repository/service/testdata，但同样修改 `internal/models/models.go`；合并时必须保留双方新增字段，不能整文件选边。
- migration 40 高于并行分支当前最高版本 33；提交前仍需再次 fetch 并核对版本和同文件变化。
- 本步骤不修改 AI runtime、模型调用、FastGPT、token 统计、计费、消息状态或 WebSocket payload。建议先合并租户认证与组织契约，再合并本批快捷回复字段和迁移。
- 回滚运行时代码时保留已添加列和历史回填结果；不得删除 migration 40 记录或使用破坏性 DDL。旧版本运行时会忽略该字段，但重新开放多租户前必须恢复租户过滤。

## 19. 门店人工回复测试基线修复（2026-07-14）

- 三个门店人工回复测试仍使用 `TeamID=0`、空角色的历史夹具，与当前“客服必须属于综合客服组，客服组纳管门店员工号”的真实权限模型冲突。
- 测试现在创建纳管来源门店 `88` 和企微员工号实例 `77` 的综合客服组，将客服档案加入该组，并给操作者 `cs_user` 角色；不修改生产 service，不放宽会话可见或消息发送权限。
- 三个原失败用例、整组人工派单/超时测试和对应 `go test -race` 均通过；`go test ./... -run '^$' -count=1` 通过。完整 services 包仅剩既有异步 AI 回复 goroutine 在测试清库后访问全局 DB 的失败，本步骤不修改 AI runtime。
- 影响文件仅为 `internal/services/conversation_human_dispatch_service_test.go` 和本文档；不涉及 model/migration、DTO/enum、API、WebSocket 或 AI 回复链路。
- 回滚边界仅为测试夹具。并行 `codex/ai-billing@f2d2da4` 同样扩展了该测试文件，新增 AI 恢复、门店通知和相关模型夹具；两侧改动语义互补但存在同文件冲突，合并时必须保留 AI 分支新增用例，并将本步骤的 `createHumanDispatchStoreAgent` 组织夹具应用到对应三个回复用例，不能整文件选边。

## 20. 多租户阶段 4C：站内通知租户归属（2026-07-14）

### 本步骤目标与结果

- 为 `Notification` 增加 `TenantID`，归属唯一继承自 `RecipientUserID` 对应账号；调用方不能提交或覆盖通知租户。
- 创建通知前校验接收账号存在且未删除，避免新增无法投递和无法确定租户的孤儿通知。
- 通知分页、未读统计、单条已读和全部已读均使用 `recipient_user_id + tenant_id`。平台账号使用账号固定租户 `0`，不会因 `ActiveTenantID` 切换而隐藏平台通知。
- 删除 notification service/repository 的全局单条读取路径，更新动作只能通过接收账号与租户组合定位。
- API 路径、request/response DTO 和 WebSocket payload 不变，前端无需改动。

### Migration 与数据安全

- migration 41 按历史通知的接收账号回填 `TenantID`；租户账号写入其固定租户，平台账号保持 `0`。
- 通知引用缺失账号，或已有显式非零租户与接收账号冲突时迁移失败并整笔回滚；重复执行不改变已确认归属。
- 通知 `BizType/BizID/ActionURL` 不作为租户来源。它们只是业务关联和导航信息，目标工单/会话仍需在各自批次强制隔离。

### 主要文件

```text
internal/models/models.go
internal/repositories/notification_repository.go
internal/services/notification_service.go
internal/handlers/dashboard/notification_handler.go
internal/migration/000041_backfill_notification_tenants.go
internal/services/notification_service_test.go
internal/services/event_handlers/notification_event_handler_test.go
```

### 验证、并行影响与回滚

```text
go test ./internal/migration -run 'TestBackfillNotificationTenants' -count=1
go test ./internal/services -run '^TestNotificationService' -count=1
go test -race ./internal/services -run '^TestNotificationService' -count=1
go test ./internal/services/event_handlers -count=1
go test ./internal/services -run 'Test(ConversationHumanDispatch|StoreManualAgentReply|ManualSessionTimeout|Handoff)' -count=1
```

- 覆盖租户继承、平台账号零租户、迁移幂等、冲突/孤儿回滚、跨账号与伪造租户已读拒绝、批量已读隔离，以及工单/会话事件通知继承接收账号租户。
- `go test ./internal/migration -count=1`、`go vet ./...`、`go test ./... -run '^$' -count=1` 和 `cd web && pnpm typecheck` 通过；完整 `go test ./... -count=1` 中 services 与通知事件通过，仅剩既有 `TestBuildLightweightTicket` 因 builder 内部访问未初始化全局 DB 失败，作为独立分层修复处理。
- 并行 `codex/ai-billing@f2d2da4` 不修改通知 handler/repository/service；仅在 `internal/models/models.go` 同文件重叠，合并时保留双方字段。
- migration 41 高于并行分支当前最高版本 33；提交前仍需再次 fetch 核对。
- 本步骤不修改 AI runtime、模型调用、计费、消息状态或通知 WebSocket 事件。回滚运行时代码时保留已添加列和回填结果，不删除迁移历史。

## 21. 客户响应构建分层与批量聚合修复（2026-07-14）

### 本步骤目标与结果

- `BuildCustomer` 和门店关系 builder 不再调用 Company/Customer/Store/WxWork service，builders 恢复为纯 `model + context -> response DTO` 映射。
- Customer service 新增展示数据批量聚合：按当前客户集合一次加载客户企业、一次加载门店关系，再分别批量加载门店和企微员工号实例。
- 客户列表、详情、创建/保存响应和门店关系接口显式传入构建上下文，保留原有 `company`、`storeRelations`、门店名和员工号名字段；API 路径与 DTO 不变。
- 客户列表不再为每个客户逐条查询企业、门店关系、门店和企微实例，移除原有 N+1；工单 builder 使用基础客户映射时也不再隐式访问全局 DB。

### 分层、验证与并行影响

- 数据访问只在 Customer service 通过 repository 完成，handler 只负责调用 service、传递聚合数据和返回 DTO，builder 不依赖 service。
- 新增纯 builder context 测试和 Customer service 聚合测试；原 `TestBuildLightweightTicket` 未初始化全局 DB 的稳定崩溃已修复。
- `go test ./internal/builders -count=1`、客户聚焦 service 测试、`go vet ./...`、`go test ./... -run '^$' -count=1` 与 `cd web && pnpm typecheck` 通过。完整测试只剩既有异步 AI 回复 goroutine 在测试清库后访问全局 DB，本步骤不修改 AI runtime。
- 不涉及 model/migration、DTO/enum、路由、WebSocket 或前端文件。并行 `codex/ai-billing@f2d2da4` 不修改本步骤文件，无特殊合并顺序；回滚边界是 customer builder、handler 展示聚合和对应测试。

## 22. 多租户阶段 7A：接入公司管理页（2026-07-14）

### 本步骤目标与结果

- `/dashboard/channels` 已从旧 Channel CRUD 替换为平台“接入公司”管理页；页面不再引用 `fetchChannels/createChannel/updateChannel/deleteChannel`，但 Channel model、service、handler、回调、消息路由和 Outbox 运行时全部保留。
- 新增独立 `web/lib/api/tenant.ts`，只封装既有 `/api/dashboard/tenant/*` 契约；列表支持公司法定名称、公司编码、法定识别号、核验状态和启用状态筛选。
- 列表展示公司法定名称/简称、`TenantCode`、统一社会信用代码、公司主管、业务联系人、核验状态和启用状态；旧 `/dashboard/companies` 继续承担客户企业管理，不与 Tenant 混用。
- 创建表单分为公司法定资料、业务联系人、首个公司主管和平台备注。创建成功后单独显示主管用户名、一次性临时密码、邀请码、邀请链接和默认综合客服组 ID；敏感结果不写入列表或日志。
- 编辑只更新公司资料，不重复创建或修改公司主管。启停当前正在进入的公司时会先清除前端当前公司上下文，再刷新登录资料。
- “进入公司”调用既有 `setActiveTenantId` 和 `refreshProfile`，随后进入公司总览；回到列表可看到“当前公司”标记。

### 权限、导航与共享组件

- 导航名称改为“接入公司”，入口权限从 `channel.view` 改为 `tenant.view`。
- `tenant.create` 控制新建按钮，`tenant.update` 控制编辑按钮，`tenant.updateStatus` 控制状态开关，`tenant.switch` 与 `canSwitchTenant` 共同控制进入公司动作；没有动作权限时不显示对应操作。
- `DashboardCrudPage` 新增向后兼容的 `showEdit` 和 `showActionsColumn`，默认值保持原页面行为；用于真正隐藏无权操作，而不是保留不可用按钮。
- 修复通用 CRUD 表头和 `ProjectDialog` 头尾在暗色模式下的对比度。新建表单和一次性结果弹窗均验证了独立滚动与移动端单列布局。
- 中英文资源均新增 `tenant.*` 文案。公开 `/register` 页面仍不存在，`tenantRegistration.enabled` 继续保持关闭；结果中的邀请链接只作为已生成凭据展示，不代表注册入口已经开放。

### 验证与未完成边界

```text
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/channels/page.tsx app/dashboard/channels/_components/edit.tsx app/dashboard/channels/_components/creation-result.tsx lib/api/tenant.ts components/dashboard/crud/dashboard-crud-page.tsx components/project-dialog.tsx lib/navigation.tsx
cd web && node --test app/dashboard/channels/tenant-page.test.mjs components/dashboard/crud/dashboard-crud-utils.test.mjs
cd web && pnpm build
```

- 使用当前源码、独立端口和临时 SQLite 数据库完成浏览器验收：桌面/390px 移动端列表、创建表单滚动、暗色模式、真实原子创建、一次性结果和进入公司状态同步均通过；测试数据与 `8083` Docker 数据完全隔离，数据库文件不进入 Git。
- 本阶段不伪造客服数、门店数、客服组数和最近活跃时间；Tenant 列表 DTO 尚无这些统计，且相关业务表未全部完成租户化，应在事实源隔离后由后端聚合补充。
- 平台管理/当前公司双层导航、侧边栏公司选择器、当前公司渠道设置和公开邀请注册继续延后。全域隔离完成前不得开放注册，也不得把旧 Channel 管理入口恢复到本页面。
- 不涉及 model/migration、后端 DTO/enum、Gin 路由、WebSocket、AI runtime、FastGPT、模型调用、token 或计费。

### 并行影响、合并顺序与回滚

- 开始时核对 `origin/codex/ai-billing@f2d2da4`：双方共同修改 `web/lib/navigation.tsx` 和中英文资源。合并时必须同时保留 AI 分支的 `replyIntentProfiles` 导航/文案与本步骤的 `tenant.view` 接入公司入口/`tenant.*` 文案，不能整文件选边。
- 建议在租户认证、权限和 Tenant 后端契约之后合并本步骤；不依赖新的 migration。合并后重新执行前端类型检查、导航契约测试和静态构建。
- 回滚边界仅为接入公司前端、租户 API 封装、两个通用组件的可见性/暗色样式和导航文案。回滚不得删除 Channel 后端，也不得重新开放公开注册。

## 23. 多租户阶段 5A：客服组织运行时隔离（2026-07-14）

### 本步骤目标与结果

- 衔接阶段 4A 已有 `TenantID`，补齐客服组、客服档案、客服小组/成员和排班的后台真实读取与写入隔离，不新增平行页面、状态或权限。
- `agent-team`、`agent`、`agent-team/squad` 和 `agent-team-schedule` Handler 在原动作权限校验后统一要求有效 `ActiveTenantID`；平台管理员未进入公司时不能读取公司组织数据。
- 列表、全量选择、详情和排班日历使用当前租户查询；提交其他租户的 path/query/body ID 不能读取、更新、删除或替换成员。
- Repository 新增租户内 `Get/Updates/Delete/FindByIds` 方法。客服组、客服档案、小组、成员和排班的业务更新/删除最终 SQL 均包含 `tenant_id`，避免只依赖前置校验。
- 门店员工分配与解除客服组均先按当前租户读取账号和客服组；从原组迁移、范围同步和客服组最终更新不能跨公司。
- 客服档案 builder 移除 User/AgentTeam service 依赖，改为纯映射；Handler 只传入 service 已按租户加载的用户和客服组数据。
- 客服档案页使用的派单客服负载列表按当前租户过滤，关联用户名和客服组名也只从当前租户读取；未改会话状态机、派单算法、AI 推荐或模型调用。

### 主要文件与契约影响

```text
internal/repositories/agent_team_repository.go
internal/repositories/agent_profile_repository.go
internal/repositories/agent_team_squad_repository.go
internal/repositories/agent_team_squad_member_repository.go
internal/repositories/agent_team_schedule_repository.go
internal/repositories/user_repository.go
internal/services/agent_team_scope_service.go
internal/services/agent_team_service.go
internal/services/agent_profile_service.go
internal/services/agent_team_squad_service.go
internal/services/agent_team_schedule_service.go
internal/services/conversation_dispatch_workbench_service.go
internal/services/user_service.go
internal/builders/agent_profile_builder.go
internal/handlers/dashboard/agent_handler.go
internal/handlers/dashboard/agent_team_handler.go
internal/handlers/dashboard/agent_team_squad_handler.go
internal/handlers/dashboard/agent_team_schedule_handler.go
internal/services/agent_organization_tenant_service_test.go
```

- 无 model、AutoMigrate 字段、DML migration、DTO、enum、Gin 路由或 WebSocket payload 变化。
- Repository/service 方法均为向后兼容新增；现有无租户通用方法保留给明确的运行时和回填调用，后台 HTTP 链路改用租户方法。
- 修正历史测试夹具，使客服组、客服、门店员工和排班显式属于 `TenantID=101`；没有放宽生产权限。

### 验证、已知边界与回滚

```text
go test ./internal/handlers/dashboard ./internal/services -run 'TestAgentOrganization|TestAgentTeamScopeCanManageTeam|TestTenantAdminCreatesAndManagesTeamsOnlyInActiveTenant|TestAgentTeamSquad|TestAgentTeamSchedule' -count=1
go test -race ./internal/services ./internal/handlers/dashboard -run 'TestAgentOrganization|TestAgentTeamScopeCanManageTeam|TestTenantAdminCreatesAndManagesTeamsOnlyInActiveTenant|TestAgentTeamSquad|TestAgentTeamSchedule' -count=1
go vet ./...
go test ./... -run '^$' -count=1
```

- 双租户测试覆盖客服组/档案/小组/排班列表与详情、排班日历、客服负载、跨租户 body ID 更新/删除、成员关系污染、门店员工解除归属和最终 repository 写条件；无当前租户的组织列表 Handler 返回 forbidden。
- 聚焦测试、race、vet 和全仓编译通过。全量 `go test -p 1 ./... -count=1` 仍失败于既有消息测试启动异步 AI 回复后关闭全局测试 DB，后台 goroutine 在 `BuildRuntimeAIAgentForConversation` 访问 nil DB；该问题已在阶段 3B/21 记录，本步骤不修改 AI runtime 或消息触发链路。
- Conversation、Message、ConversationAssignment、ConversationRouteState 和 Ticket 尚未完成租户字段/运行时隔离，因此派单任务列表、全局待回复统计和会话动作不能随本步骤宣布完成；公开邀请注册继续保持关闭。
- `AgentProfile.AgentCode` 的历史全局唯一索引未改为租户组合唯一。后续调整必须先审计重复值，再设计兼容 SQLite/MySQL 的索引迁移。
- 回滚可撤销 Handler/service 的租户方法调用和新增 repository 方法；没有数据库结构可回滚。回滚不得恢复后台全局组织读取后再开放多租户入口。

### 并行分支与合并顺序

- 本步骤开始前已 `git fetch origin`，共同基点 `e67e207` 后 `origin/codex/ai-billing@f2d2da4` 未修改本步骤客服组织、派单工作台或 agent profile builder 文件，无同文件冲突。
- 本步骤依赖阶段 1-4A 的 Tenant/AuthPrincipal/客服组织字段，应在这些契约之后合并；随后再继续客户/门店/企微和会话/派单域隔离。
- 不修改 AI 回复引擎、模型供应商、FastGPT、token、计费或向量语义。AI/计费分支不需要因本步骤 rebase 后调整字段语义，但合并前仍需再次 fetch 核对最新同文件变化。

## 24. 多租户阶段 4D/5B：客户企业与接入渠道归属及后台隔离（2026-07-14）

### 本步骤目标与结果

- 先审计 Customer、Store、WxWork、Channel 与 Company 的真实调用链后，将本步骤收紧为两个可独立确定的根归属：租户内部客户企业 `Company` 和租户拥有的接入配置 `Channel`。
- `Company` 和 `Channel` 增加 `TenantID`。创建只从 `AuthPrincipal.ActiveTenantID` 继承，不增加前端可提交租户字段。
- Company/Channel 后台列表和详情按当前租户查询；创建、更新、启停、删除以及 Channel 用户密钥重置均要求有效当前租户。平台管理员未进入公司时即使持有原查看权限也返回 forbidden。
- Company 模型设置读取/更新在调用原有 `StoreAIModelSettingService` 前，先确认目标 Company 属于当前租户。
- Repository 的最终更新条件使用 `id + tenant_id`，跨租户 ID 篡改不能依赖前置读取绕过。公开接入、第三方回调和内部消息运行时仍保留按全局稳定 Channel ID 的读取方法，没有把 HTTP 管理权限规则错误套到非 HTTP 入口。
- 客户审计仿真 seed 创建/复用 Company 和 Channel 时显式使用其 legacy tenant，不覆盖其他租户同类记录。

### Migration、契约与主要文件

- migration 42 将历史 `tenant_id=0` 的 Company/Channel 归入 `legacy-default`；已有非零值必须引用真实 Tenant，否则事务失败并回滚。重复执行保持现有归属。
- DDL 由 AutoMigrate 增加兼容 SQLite/MySQL 的 `bigint not null default 0` 索引字段；本步骤不删除旧字段或索引。
- 无 request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端文件变化。原 `company.*`、`channel.*` 权限继续使用，不新增重复权限。

```text
cmd/customer_audit_seed/main.go
internal/models/models.go
internal/repositories/company_repository.go
internal/repositories/channel_repository.go
internal/services/company_service.go
internal/services/channel_service.go
internal/handlers/dashboard/company_handler.go
internal/handlers/dashboard/channel_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/migration/000042_backfill_company_channel_tenants.go
internal/migration/000042_backfill_company_channel_tenants_test.go
internal/services/company_channel_tenant_service_test.go
```

### 验证证据与已知边界

```text
go test ./internal/migration -run 'TestBackfillCompanyAndChannelTenants' -count=1
go test ./internal/services -run 'Test(CompanyServiceEnforcesTenantContextAcrossCRUD|ChannelServiceEnforcesTenantContextAcrossCRUD|CompanyAndChannelRepositoriesKeepTenantInFinalWritePredicate|CompanyAndChannelServicesRequireActiveTenant)$' -count=1
go test ./internal/handlers/dashboard -run 'TestCompanyAndChannelListHandlersRequireActiveTenant$' -count=1
go test -race ./internal/migration ./internal/services ./internal/handlers/dashboard -run 'Test(BackfillCompanyAndChannelTenants|CompanyServiceEnforcesTenantContextAcrossCRUD|ChannelServiceEnforcesTenantContextAcrossCRUD|CompanyAndChannelRepositoriesKeepTenantInFinalWritePredicate|CompanyAndChannelServicesRequireActiveTenant|CompanyAndChannelListHandlersRequireActiveTenant)' -count=1
go vet ./...
go test ./... -run '^$' -count=1
```

- 上述聚焦、race、vet 和全仓编译全部通过。完整 `go test -p 1 ./... -count=1` 仍失败于既有消息测试的异步 AI 回复协程：测试清理将全局 DB 置空后，后台 goroutine 在 `BuildRuntimeAIAgentForConversation` 继续读取 `ConversationRouteState` 并 panic。本步骤没有修改 AI runtime、消息触发或测试生命周期。
- migration 测试覆盖旧值回填、显式值保留、重复执行幂等，以及缺失租户引用时已发生的前序更新一并回滚。双租户 service 测试覆盖创建继承、分页/详情、跨租户更新/启停/删除/密钥重置和 repository 最终条件。
- 本步骤不能被解释为客户域或消息链路已经完成隔离。Customer/CustomerIdentity、Store/StoreStaffBinding、WxWorkProtocolInstance、Conversation/Message、派单、Ticket、回调、Outbox、WebSocket、文件与向量检索仍待后续批次。
- `Company.Name` 仍是历史全局唯一，限制不同租户使用相同客户企业名称；`Channel.ChannelID` 有意保持全局唯一，以支持公开入口和回调反查。
- AIAgent 尚无 TenantID，Channel-to-AIAgent 暂时只能验证存在且启用。企业微信客服账号枚举仍来自平台全局配置，不代表租户级外部凭据已隔离。
- Store/WxWork 本步骤有意延期：`codex/ai-billing` 正在修改企微远程接入、欢迎内容、意图配置和相关模型/service；未取得共同归属契约前不在客服分支抢改。

### 并行分支、合并顺序与回滚

- 任务开始时已 `git fetch origin`，核对 `origin/codex/ai-billing@f2d2da4`，其 migration 最高仍为 33，migration 42 当前未冲突。
- 双方共同修改 `internal/models/models.go` 的 Company 区域：AI 分支增加 `Company.IntentProfileID`，本步骤增加 `Company.TenantID`。双方也共同修改 `internal/services/company_service.go`：AI 分支增加意图行业配置校验/写入，本步骤增加当前租户读取和最终 `id + tenant_id` 更新。合并必须逐字段、逐方法保留两组语义，不能整段或整文件选边；Channel 的本步骤字段/service 当前无同文件业务冲突。
- AI 分支还配套修改 Company request/response、builder 和前端页面；本步骤没有修改这些文件。建议先合并 Tenant/AuthPrincipal 基础契约，再合并本步骤 Company/Channel 归属，随后 AI 分支 rebase，并让 Company 创建/更新同时写入 `TenantID + IntentProfileID`、让更新 SQL 同时保留 `intent_profile_id` 和 `tenant_id` 条件；Store/WxWork 应在双方确认 Tenant 归属和迁移顺序后继续。
- 本步骤不修改模型调用、回复引擎、FastGPT、token 统计、计费或向量语义。AI/计费负责人无需改变这些语义。
- 回滚运行时代码时保留 AutoMigrate 已增加的列、migration 42 记录和已回填 TenantID；不得删除迁移历史或把已归属数据批量清零。公开注册继续保持关闭。

## 25. 多租户阶段 4E/5C：客户主档、身份与联系方式隔离（2026-07-14）

### 本步骤目标与结果

- 为 `Customer`、`CustomerIdentity`、`CustomerContact`、`StoreCustomerRelation` 增加 `TenantID`，将客户域直接子表与父客户归属保持一致。
- 客户列表 SQL 在 `t_customer` 主条件和 Contact/Company join 中同时使用 tenant；详情、门店关系展示、创建、更新、启停、删除与单事务档案保存都要求有效 `ActiveTenantID`。
- 手工客户只能关联当前租户 Company。联系方式列表/创建/更新/删除、软删复活、主联系方式切换和批量替换都先验证父客户，并在最终 Customer/Contact 更新 SQL 使用 `id + tenant_id`。
- 外部客户创建改为显式接收 Channel tenant；CustomerIdentity 按 `tenant_id + external_source + external_id` 查找，同一个外部 ID 在不同租户不会复用同一 Customer。
- Conversation 创建先验证 Channel 存在、启用且已有租户归属，再创建/复用客户；CustomerSession 验证、客户会话所有权、外部身份回查和企微联系人资料异步回写都校验 Channel/Customer tenant。
- StoreCustomerRelation 从父 Customer 继承 tenant，创建/更新最终条件携带 tenant；客户审计仿真 seed 的 Customer/Identity/Contact/Relation 也固定写入其 legacy tenant。

### Migration、契约与主要文件

- migration 43 的 Customer 归属来源优先级不是猜测覆盖：显式有效 Tenant、Company tenant、历史 Conversation→Channel tenant 必须互相一致；没有任何来源时才归入 `legacy-default`。
- Company 缺失、Conversation 引用缺失 Channel、父来源跨租户、Identity/Contact/StoreRelation 孤儿或子表显式租户与父 Customer 冲突时，migration 失败并整体回滚；重复执行幂等。
- DDL 继续由 AutoMigrate 增加 `bigint not null default 0` 索引字段；没有修改 request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端文件。

```text
cmd/customer_audit_seed/main.go
internal/models/models.go
internal/repositories/customer_repository.go
internal/repositories/customer_identity_repository.go
internal/repositories/customer_contact_repository.go
internal/repositories/store_customer_relation_repository.go
internal/services/customer_service.go
internal/services/customer_contact_service.go
internal/services/customer_session_service.go
internal/services/conversation_service.go
internal/services/wxwork_protocol_service.go
internal/handlers/dashboard/customer_handler.go
internal/handlers/dashboard/customer_contact_handler.go
internal/handlers/dashboard/company_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/migration/000043_backfill_customer_domain_tenants.go
internal/migration/000043_backfill_customer_domain_tenants_test.go
internal/services/customer_tenant_service_test.go
internal/services/customer_service_test.go
internal/services/message_service_test.go
internal/services/conversation_human_dispatch_service_test.go
internal/services/wxwork_protocol_service_test.go
```

### 验证证据与已知边界

```text
go test ./internal/migration -run 'TestBackfillCustomerDomainTenants' -count=1
go test ./internal/services -run 'Test(CustomerServiceEnforcesTenantContextAcrossCRUD|ExternalCustomerIdentityIsSeparatedByChannelTenant|ConversationCreationDerivesCustomerTenantFromChannel|CustomerContactServiceRejectsCrossTenantIDs|CustomerRepositoriesKeepTenantInFinalWritePredicate|CustomerServicesRequireActiveTenant)$' -count=1
go test ./internal/handlers/dashboard -run 'TestCustomerListHandlersRequireActiveTenant$' -count=1
go test -race ./internal/migration ./internal/services ./internal/handlers/dashboard -run 'Test(BackfillCustomerDomainTenants|CustomerServiceEnforcesTenantContextAcrossCRUD|ExternalCustomerIdentityIsSeparatedByChannelTenant|ConversationCreationDerivesCustomerTenantFromChannel|CustomerContactServiceRejectsCrossTenantIDs|CustomerRepositoriesKeepTenantInFinalWritePredicate|CustomerServicesRequireActiveTenant|CustomerListHandlersRequireActiveTenant|EnsureExternalCustomer|LoadCustomerPresentationData|ConversationHumanDispatchHumanOnly|WxWorkProtocolReferencedRecall|WxWorkProtocolEmployeeOutgoingEcho|ConversationCreate|CreateExternalAgentMessageWithoutOutbox)' -count=1
go test ./internal/migration ./internal/handlers/dashboard -count=1
go vet ./...
go test ./... -run '^$' -count=1
go test -p 1 ./... -count=1
```

- 上述 focused、race、全 migration/Handler、vet、全仓编译和完整串行测试均通过。本次完整串行测试等待异步回复协程结束后通过；此前已记录的测试清理竞态仍属于残余风险，当前步骤没有修改 AI runtime 生命周期。
- migration 测试覆盖 Company 来源、Conversation→Channel 来源、无来源 legacy 兜底、显式值保留、重复执行、跨租户父来源冲突和孤儿子表整笔回滚。
- 双租户测试覆盖后台 CRUD、同外部 ID 隔离、Conversation 从 Channel 继承、跨租户 Company/Customer/Contact ID 注入、档案批量保存、StoreRelation 继承和最终 repository 条件。
- `Conversation`、`Message`、`Ticket`、`Store`、`StoreStaffBinding`、`WxWorkProtocolInstance` 尚无 TenantID。Conversation 创建已经保证新 Customer 来源明确，但会话列表/派单/工单、Store/WxWork 展示对象、回调、Outbox 和 WebSocket 仍需自己的租户字段与双租户测试。
- 小程序若没有匹配到真实 Channel，旧独立 AI Agent fallback 现在会在 Conversation 创建时因无法确定租户而失败；没有伪造 tenant 或恢复旧独立 Agent。后续 UI/接口应明确提示先配置渠道。
- `CustomerIdentityService` 的历史通用 CRUD 包仍存在但当前没有 handler、路由或调用方；真实运行链路均改用 tenant-aware repository/service。若后续重新开放身份管理，必须先提供租户化 API，不能直接复用全局 CRUD。
- AI 分支新增 `WxWorkCustomerHandoffSetting`，它引用 Customer 和 WxWorkInstance，但当前 AI 分支模型没有 TenantID。合并后必须从 Customer 继承并校验实例租户，不能把本步骤视为该新增表已隔离。

### 并行分支、合并顺序与回滚

- 开始本步骤前 `origin/codex/ai-billing@f2d2da4` 的 migration 最高为 33，migration 43 当前未冲突；提交前仍需 fetch 复核。
- 同文件重叠包括 `internal/models/models.go`、`internal/services/wxwork_protocol_service.go`、`internal/services/message_service_test.go`、`internal/services/conversation_human_dispatch_service_test.go` 和 `internal/services/wxwork_protocol_service_test.go`。生产 service 的 tenant 校验与 AI 分支的回复/企微能力语义互补，测试夹具也必须同时保留双方新增表和 Channel tenant，禁止整文件选边。
- `internal/services/customer_service.go`、Customer/Contact handlers/repositories、migration 43 当前不与 AI 分支同文件冲突，适合先独立合并；随后 AI 分支 rebase，解决 models/企微 service/测试夹具冲突并补 `WxWorkCustomerHandoffSetting.TenantID`。
- 本步骤没有改变 AI 回复触发、模型供应商、FastGPT、token、计费或向量语义。`wxwork_protocol_service.go` 仅在联系人资料回写前验证实例 Channel 与 Customer tenant，并为最终 Customer 更新增加 tenant 条件。
- 回滚运行时代码时保留新增列、migration 43 记录和已回填 TenantID；不得清零客户归属。若临时回滚严格 Channel 校验，也不得重新开放公开注册或允许跨租户后台客户访问。

## 26. 多租户阶段 4F：门店与企微员工号归属契约（2026-07-14）

### 本步骤目标与结果

- 为 `Store`、`StoreStaffBinding`、`WxWorkProtocolInstance` 增加 `TenantID`，使门店、门店账号绑定和企微员工号实例具备可由后续后台、回调、会话与派单链路共同使用的租户根。
- 本步骤按共享契约单独提交，不修改企微实例 service/handler/repository、AI runtime、模型供应商、token、计费或向量检索语义，降低与 `codex/ai-billing` 当前企微重构的同文件冲突。
- 没有增加前端可提交的 tenant 字段，也没有改变企微协议请求/响应字段；公开注册和灰度多租户入口继续保持关闭。

### Migration、契约与主要文件

- migration 44 的 Store 归属会同时汇总：Store 自身显式 Tenant/Company，StoreStaffBinding 的显式 Tenant/User/AgentTeam/Company，WxWorkProtocolInstance 的显式 Tenant/Channel/AgentTeam/Company，以及 StoreCustomerRelation 对应 Customer tenant。
- Store 回填完成后，StoreStaffBinding 必须与 Store/User/AgentTeam/Company 同租户；WxWorkProtocolInstance 必须与 Channel/Store/StoreStaffBinding/AgentTeam/Company 同租户。
- 任一非零父 ID 缺失、父对象尚无 tenant、显式 tenant 不存在或多条证据跨租户时，migration 整笔回滚；只有完全没有归属证据的历史行才进入 `legacy-default`。重复执行幂等。
- DDL 由 AutoMigrate 增加三个 `bigint not null default 0 index` 字段，兼容 SQLite/MySQL；migration 44 只做 DML 回填。

```text
internal/models/models.go
internal/migration/000044_backfill_store_wxwork_tenants.go
internal/migration/000044_backfill_store_wxwork_tenants_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

### 验证与边界

```text
go test ./internal/migration -run 'TestBackfillStoreAndWxWorkTenants' -count=1
go test -race ./internal/migration -run 'TestBackfillStoreAndWxWorkTenants' -count=1
go test ./internal/migration -count=1
go vet ./...
go test ./... -run '^$' -count=1
go test -p 1 ./... -count=1
git diff --check
```

- migration 测试覆盖 Company、Channel、User、AgentTeam、CustomerRelation 来源、显式值、legacy 兜底、重复执行、跨租户冲突、孤儿 Store 引用和无效显式 Tenant，并验证失败时前序更新一并回滚。
- 聚焦测试、migration 全包、race、`go vet ./...` 和全仓编译通过。完整 `go test -p 1 ./... -count=1` 再次触发既有异步 AI 测试清理竞态：测试关闭全局 DB 后，`TriggerReplyAsync` 后台协程在 `BuildRuntimeAIAgentForConversation` 读取 `ConversationRouteState` 时因 nil DB panic；本步骤未修改该运行时或消息触发链路，不能把完整串行回归记录为通过。
- 本步骤不等于 Store/WxWork 运行时隔离完成。后台列表/详情/写操作仍需按 `ActiveTenantID` 收紧；协议回调应先按全局稳定 GUID 找实例，再以实例 Tenant 校验后续资源，不能要求第三方回调携带浏览器租户头。
- `WxWorkProtocolDevicePoolInstance`、`StoreAIModelSetting`、KnowledgeBase、Conversation/Message/派单、回调、Outbox、WebSocket、文件和向量仍需后续独立租户批次与双租户测试。
- StoreCode、WxWork GUID 等历史全局唯一索引本步骤不调整；组合唯一范围要在运行时隔离完成后单独审计历史重复与 SQLite/MySQL 索引迁移。

### 并行分支、合并顺序与回滚

- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 的 migration 最高为 33，migration 44 当前未冲突。
- 同文件冲突限定在 `internal/models/models.go`。AI 分支为 Store/WxWork 增加欢迎内容、行业意图等字段，本步骤只增加三处 `TenantID`；合并必须逐字段保留，不能整段选边。
- AI 分支还修改 `wx_work_protocol_instance_repository.go`、instance handler/service；本步骤有意不修改这些文件。建议先合并 Tenant/AuthPrincipal、Company/Channel、Customer 和本步骤 Store/WxWork 字段契约，再由 AI 分支 rebase，并在其新建/更新实例及门店流程中写入和校验 TenantID。
- 客服分支后续再基于共同模型契约补后台管理、客服组范围、会话/派单和非 HTTP 链路隔离。回滚运行时代码时不得删除新增列、migration 44 记录或清空已回填归属。

## 27. 多租户阶段 5D：用户管理与门店员工客服组归属隔离（2026-07-14）

### 本步骤目标与结果

- 保留现有“用户管理可给门店员工分配客服组、客服组编辑可反向选择门店员工”的双向入口，不新增平行页面；两侧列表、筛选、详情展示、分配、转组和解绑统一使用当前 `ActiveTenantID`。
- 用户列表/全量选项/详情要求有效当前公司。`agentTeamId` 筛选中的 StoreStaffBinding 子查询携带 tenant，门店员工归属聚合只加载同租户 Store、Company、AgentTeam 和 WxWorkProtocolInstance。
- 客服组创建/编辑兼容 `StoreStaffUserIDs` 和旧 `WxWorkInstanceScopeIDs` 两种 request 字段，但所有解析都限定当前租户；其他租户 ID 对调用方表现为不存在。
- Binding 更新和按 Binding 批量同步 WxWorkInstance 的最终 SQL 使用 `tenant_id`；客服组范围回算只聚合当前租户 Binding/Instance，不能由污染关联扩入其他公司。
- `tenant_admin`、super admin 和 admin 的 `Unrestricted` 重新定义为“当前公司内不受客服组范围限制”。企微实例列表先 tenant 后 team scope；会话列表和详情在原客服组范围前先通过 Conversation.Channel→Channel tenant 校验当前公司。
- `EnsureForInstance` 要求实例、门店和 Binding 同租户；新建 Binding 与回写 Instance 处于同一事务，跨租户操作者不能创建或复用绑定。

### 主要文件与契约

```text
internal/repositories/store_repository.go
internal/repositories/store_staff_binding_repository.go
internal/repositories/wx_work_protocol_instance_repository.go
internal/services/store_service.go
internal/services/store_staff_binding_service.go
internal/services/agent_team_service.go
internal/services/agent_team_scope_service.go
internal/handlers/dashboard/user_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/services/agent_team_scope_service_test.go
internal/services/agent_organization_tenant_service_test.go
internal/services/store_staff_tenant_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 没有 model、migration、request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端文件变化；依赖上一提交 migration 44 已建立的三个 TenantID。
- WxWork repository 只新增 `GetInTenant`、`UpdatesInTenant` 和 `UpdatesByStoreStaffBindingIDsInTenant`，旧全局方法继续供 migration 和协议按稳定标识读取，不改变其语义。
- migration 37/38 调用的 `BackfillWxWorkInstanceBindings`、`BackfillStoreStaffAgentTeamBindings` 保留 nil-operator 全局路径；运行时客服组操作传入 operator 后才强制 tenant，避免历史升级顺序被破坏。

### 验证与已知边界

```text
go test ./internal/services ./internal/handlers/dashboard -run 'Test(StoreStaff|StoreDomain|EnsureStoreStaff|LegacyStoreStaff|AgentTeamScope|BindStoreStaff|UpdateAgentTeam|AgentOrganization|AgentOrganizationListHandlersRequireActiveTenant)' -count=1
go test -race ./internal/services ./internal/handlers/dashboard -run 'Test(StoreStaff|StoreDomain|EnsureStoreStaff|LegacyStoreStaff|AgentTeamScope|BindStoreStaff|UpdateAgentTeam|AgentOrganization|AgentOrganizationListHandlersRequireActiveTenant)' -count=1
go test ./internal/migration -count=1
go vet ./...
go test ./... -run '^$' -count=1
go test -p 1 ./... -count=1
git diff --check
```

- 双租户测试包含故意污染的跨租户 User→Binding 关系，确认当前公司归属展示和企微实例范围不会读出污染行；Store/Binding/Instance 的错误租户最终更新条件均不改变目标记录。
- 专项测试覆盖公司主管在当前租户的 unrestricted 范围、另一租户企微实例不可见、无当前租户不返回归属、跨租户 EnsureForInstance 被拒绝，以及 migration 37/38 对 `tenant_id=0` 历史数据仍可运行。
- 上述聚焦、race、migration 全包、`go vet ./...` 和全仓编译通过。完整 `go test -p 1 ./... -count=1` 再次失败于既有 AI 异步测试清理竞态：全局 DB 清理后 `TriggerReplyAsync` 仍在 `BuildRuntimeAIAgentForConversation` 读取 RouteState 并 nil pointer panic；本步骤未修改该 goroutine 生命周期，不能记录完整串行回归通过。
- 本步骤只完成用户管理、客服组归属和相关范围读取。WxWork 实例后台全部动作仍有全局 service 方法，协议回调/设备池/Outbox/WebSocket 尚未隔离；Conversation 只增加基于 Channel 的前置过滤，本体和 Message/RouteState/Assignment 尚无 TenantID。
- `FindStoreStaffUserIDs`、`deriveScopeFromWxWorkInstances` 等无当前生产调用的历史全局 helper 暂保留给旧测试/迁移兼容；真实 Handler 已使用 `FindStoreStaffUserIDsInTenant`。若后续重新开放调用，必须传 tenant，不能直接接回页面。

### 并行分支、合并顺序与回滚

- 开始时 `origin/codex/ai-billing@f2d2da4`；本步骤不新增 migration。与 AI 分支同文件仅 `wx_work_protocol_instance_repository.go`，新增 tenant-aware 方法应与其远程接入/意图查询方法逐方法合并。
- 不修改 AI 回复、欢迎语、模型供应商、FastGPT、token、计费或向量语义。建议先合并 migration 44 契约，再合并本步骤运行时过滤；AI 分支 rebase 后在其实例创建/更新链路调用相同 tenant-aware repository 方法。
- 回滚本步骤时可撤销 Handler/service/repository 调用变化，但不得删除上一提交的 TenantID、migration 44 或已回填数据；在 WxWork 全动作和会话本体隔离完成前继续关闭公开注册。
