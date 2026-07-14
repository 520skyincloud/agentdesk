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

- 本步骤当时只完成阶段 3B 后端；`/register` 页面、账号页邀请浮窗和待审核 UI 后续已由第 38 节完成，正式配置仍保持关闭。
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
- 中英文资源均新增 `tenant.*` 文案。该步骤实施时公开 `/register` 尚未完成，后续已由第 38 节补齐；`tenantRegistration.enabled` 继续保持关闭。

### 验证与未完成边界

```text
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/channels/page.tsx app/dashboard/channels/_components/edit.tsx app/dashboard/channels/_components/creation-result.tsx lib/api/tenant.ts components/dashboard/crud/dashboard-crud-page.tsx components/project-dialog.tsx lib/navigation.tsx
cd web && node --test app/dashboard/channels/tenant-page.test.mjs components/dashboard/crud/dashboard-crud-utils.test.mjs
cd web && pnpm build
```

- 使用当前源码、独立端口和临时 SQLite 数据库完成浏览器验收：桌面/390px 移动端列表、创建表单滚动、暗色模式、真实原子创建、一次性结果和进入公司状态同步均通过；测试数据与 `8083` Docker 数据完全隔离，数据库文件不进入 Git。
- 本阶段不伪造客服数、门店数、客服组数和最近活跃时间；Tenant 列表 DTO 尚无这些统计，且相关业务表未全部完成租户化，应在事实源隔离后由后端聚合补充。
- 该步骤实施时平台/公司导航、公司选择器、渠道设置和公开邀请注册仍延后；注册与导航后续分别由第 38、39 节补齐。当前公司渠道设置仍未迁移，不得把旧 Channel 管理入口恢复到本页面。
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
- `AgentProfile.AgentCode` 在本步骤当时仍是历史全局唯一；第 59 批已完成重复审计、SQLite/MySQL 兼容升级并改为租户组合唯一。
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
- 本步骤当时保留的 `Company.Name` 全局唯一已在第 59 批改为租户组合唯一；`Channel.ChannelID` 继续有意保持全局唯一，以支持公开入口和回调反查。
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
- 本步骤当时未调整 StoreCode、WxWork GUID。第 59 批已完成 StoreCode 租户组合唯一；WxWork GUID 继续作为协议设备级全局身份。

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

## 28. 多租户阶段 5E：企微员工号后台全动作隔离（2026-07-14）

### 本步骤目标与结果

- 承接 migration 44 的 `Store/StoreStaffBinding/WxWorkProtocolInstance.TenantID`，收紧企微员工号 dashboard 的列表、详情、创建、更新、删除、远程配置和全部协议动作。
- 列表使用“当前租户 + 客服组范围”组合过滤；详情和动作统一通过当前租户实例读取及 `CanViewWxWorkInstance` 校验。跨租户 ID 对调用方表现为实例不存在，同租户但不在客服范围内表现为无权访问。
- 创建、扫码登录和远程开户链接从 `ActiveTenantID` 继承归属；Channel、Company、Store 引用必须属于当前租户。GUID 保持协议设备级全局唯一，跨租户复用被拒绝。
- 未知登录回调继续创建 `tenant_id=0 + pending_binding` 隔离记录。后台登录认领使用 `id + tenant_id=0` 原子条件；清理未归属登录占用使用 `tenant_id IN (0, 当前租户)` 最终条件，认领竞态下不会释放其他租户的设备绑定。
- 远程配置 token 对应实例必须已有租户；远程提交自动创建/更新的 Store 和 StoreStaffBinding 继承该租户。复用已有 GUID 前先校验 Company，不能先认领后发现跨租户引用。
- Instance 更新、AI 开关、AI 设置和删除使用 tenant-qualified repository 读写。response 中 Channel、Store、Company 名称也只从当前租户读取，避免污染关联泄露其他公司展示信息。
- 门店模型设置两个现有入口增加 Company/Store/WxWorkInstance 租户及相互归属校验，只保护 dashboard 边界，不修改 AI 分支负责的模型设置、模型调用、token 或计费语义。

### 主要文件与契约

```text
internal/repositories/wx_work_protocol_instance_repository.go
internal/services/wx_work_protocol_instance_service.go
internal/services/wx_work_protocol_instance_company_test.go
internal/services/wx_work_protocol_instance_tenant_test.go
internal/handlers/dashboard/wxwork_protocol_instance_handler.go
internal/handlers/dashboard/wxwork_protocol_instance_tenant_handler_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- repository 新增 `ClaimTenant`、`ReleaseLoginBinding`、`DeleteInTenant` 等最终条件方法；旧全局 `Get/Take/Find/Updates` 继续服务协议回调、全局 GUID 唯一检查和历史 migration，不把运行时 dashboard 接回全局方法。
- 没有 model、migration、request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端文件变化。
- 没有修改企业微信协议字段或接口。消息发送仍以 `wework.apifox.cn` 定义的 `conversation_id` 为准，本步骤没有恢复 CLI、微信客服 API、旧 hook bridge 或旧企微字段。

### 验证与已知边界

```text
go test ./internal/services ./internal/handlers/dashboard -run 'WxWorkProtocol|AgentTeamScope|StoreStaffTenant' -count=1
go test -race ./internal/services ./internal/handlers/dashboard -run 'WxWorkProtocol|AgentTeamScope|StoreStaffTenant' -count=1
go test ./internal/migration -count=1
go vet ./...
go test ./... -run '^$' -count=1
go test -p 1 ./... -count=1
git diff --check
```

- 聚焦测试已覆盖双租户列表/详情读取、创建继承、跨租户 Channel/Company/Store/GUID 拒绝、更新/删除最终条件、未归属回调隔离、原子认领、远程配置 Store 继承，以及所有已注册协议动作在外部调用前拒绝跨租户实例。
- 上述聚焦测试及其 race 版本、migration 全包、`go vet ./...`、全仓编译均通过。完整 `go test -p 1 ./... -count=1` 再次失败于既有 `TriggerReplyAsync` 测试清理竞态：全局 DB 清理后后台协程在 `BuildRuntimeAIAgentForConversation` 读取 RouteState 时 nil pointer；堆栈与阶段 4F/5D 记录一致，本步骤没有修改 AI runtime 生命周期，不能把完整串行回归记录为通过。
- KnowledgeBase 尚无 TenantID，实例知识库展示与绑定校验仍是全局读取；设备池仍是平台全局资源；StoreAIModelSetting/AIConfig 底层隔离仍由 AI/计费分支负责。
- 协议回调按全局稳定 GUID 识别实例是必要入口语义，但回调后的 Conversation/Message/RouteState/Assignment、Outbox、WebSocket 尚未端到端租户化。Conversation/Message 本体、派单和 Ticket 也仍是后续批次。
- `CountStats` 仍通过实例 ID 聚合尚无 TenantID 的会话数据；handler 已保护实例访问，但不能替代会话域租户字段。
- 公开注册继续关闭。只有知识库/向量、会话消息、回调投递和实时通道完成双租户验证后，才可重新评估启用。

### 并行分支、合并顺序与回滚

- 开始时已 fetch，`origin/codex/ai-billing@f2d2da4`。双方同文件包括企微实例 repository、service、handler 和 company test；必须逐方法合并，禁止整文件选边。
- AI 分支需要保留欢迎内容、意图识别、FastGPT、模型设置和回复行为；本步骤需要保留 TenantID 继承、Channel/Company/Store 校验、tenant-qualified 最终写入、未知回调隔离和协议动作前置保护。建议先合并 Tenant/Store/WxWork 共享契约和本步骤隔离，再由 AI 分支 rebase 解决方法级冲突。
- 本步骤没有新增 migration，无版本号冲突；没有修改模型供应商、回复 runtime、token 统计、计费或向量语义。
- 可回滚本步骤的 handler/service/repository 调用变化和新增测试，但不得删除 migration 44 的字段/记录或清空已回填 TenantID。回滚期间必须继续关闭公开注册，不能恢复全局 dashboard 访问作为替代。

## 29. 多租户阶段 4G：会话、派单、工单与共享标签归属契约（2026-07-14）

### 本步骤目标与结果

- 为会话、消息、派单和工单真实运行链路建立共同 TenantID 契约，避免后续各页面分别通过 Channel/Customer/Team 临时推断归属。
- `Conversation`、`Message`、ConversationRouteState、ConversationSessionSummary、MessageSyncLog、ConversationParticipant、ConversationReadState、WxWorkKFConversation、WxWorkKFMessageRef、ChannelMessageOutbox、ConversationAssignment、ConversationEventLog 和 ConversationInterrupt 增加 TenantID。
- `Ticket`、TicketProgress、TicketView、`Tag`、ConversationTag 和 TicketTag 增加 TenantID。Conversation/Ticket 共用 Tag 主体和父子树，不新增平行标签模型。
- migration 45 先从已租户化 Channel、Customer、AgentTeam/User 确定 Conversation，再回填 Message 和全部会话子表；随后从 Customer/Conversation 确定 Ticket，最后按标签父子连通组件汇总 ConversationTag/TicketTag 证据。
- Conversation.LastMessageID、Message.QuotedMessageID、ReadState.LastReadMessageID、SessionSummary/SyncLog/WxWorkRef/Outbox/Interrupt 的消息引用必须与父会话同租户；RouteState 的 Store/WxWorkInstance、WxWorkKFConversation 的 Channel、Assignment 的 Squad 也执行同一校验。
- StoreCustomerRelation.LastConversationID 现在必须与 Relation.TenantID 一致；缺失引用、非法显式 Tenant、跨租户引用或同一标签树跨租户复用会使整笔 migration 回滚。
- 无任何可靠归属证据的独立历史 Conversation、Ticket、Tag 归入 `legacy-default`；租户账号 TicketView 从 User 继承，历史平台账号视图归入 legacy。migration 重复执行幂等，不改写已确认归属。

### 主要文件与契约

```text
internal/models/models.go
internal/migration/000045_backfill_conversation_ticket_tenants.go
internal/migration/000045_backfill_conversation_ticket_tenants_test.go
internal/services/conversation_human_dispatch_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- DDL 继续由 AutoMigrate 增加 `bigint not null default 0 index`，migration 45 只做 DML、一致性验证和冲突回滚。
- 本步骤没有修改 repository/service/handler、AI 回复、派单状态机、Ticket 编号算法、Outbox worker、WebSocket、request/response DTO、enum、Gin 路由、权限或前端。
- `TicketNoSequence` 和 TicketNo 保持平台全局唯一分配语义；`WxWorkKFSyncState` 因缺少 ChannelID 留到渠道凭据批次，禁止按 OpenKfID 字符串猜租户。

### 验证与已知边界

```text
go test ./internal/migration -run 'TestBackfillConversationAndTicketDomainTenants' -count=1
go test -race ./internal/migration -run 'TestBackfillConversationAndTicketDomainTenants' -count=1
go test ./internal/migration -count=1
go vet ./...
go test ./... -run '^$' -count=1
go test -p 1 ./... -count=1
git diff --check
```

- 测试覆盖 19 个新增 TenantID 模型、所有直接会话子表、有效显式 Tenant 保留、legacy 兜底、平台账号历史 TicketView、重复执行、Channel/Customer 冲突、Ticket/StoreRelation 与 Conversation 的客户不一致、跨租户引用消息、Conversation/Ticket 共享标签冲突、孤儿 Message、非法显式 Tenant 和失败前写入整体回滚。
- 推送后复查真实调用点发现两类合法的会话前置记录：协议通知会先写 `conversation_id=0` 的 MessageSyncLog，AI checkpoint 也可能先写 `conversation_id=0` 的 ConversationInterrupt；门店群通知 Outbox 则使用负数合成 message_id。migration 45 在合入主干和正式执行前修正为：前两类无业务引用记录保留 tenant 0 隔离，门店群任务只接受 JSON `kind=store_room_handoff_notice` 并从 Conversation 继承；未知负数任务、无会话却带消息/租户的记录继续中止迁移。测试已覆盖三类合法记录和伪造合成任务回滚。
- 门店人工派发的既有测试夹具此前仍创建 `tenant_id=0` 的 Store、StoreStaffBinding、WxWorkProtocolInstance、AgentTeam、AgentProfile 和 User，租户化后的真实范围校验会把该夹具路由到总部或拒绝客服回复。本步骤只把夹具统一归入测试租户 101，并补建测试所需 MessageSyncLog 表；7 个门店人工派发、回复和超时聚焦测试恢复通过，未修改派发 service 或状态机。
- migration 聚焦及 race、migration 全包、`go vet ./...`、全仓编译和 `git diff --check` 通过。完整 `go test ./internal/services -count=1` 仍会触发既有异步 AI 测试清理竞态：测试关闭或切换全局 DB 后，`TriggerReplyAsync` 后台协程继续从 `BuildRuntimeAIAgentForConversation` 读取 RouteState 并 nil pointer panic；本步骤不修改 AI runtime 生命周期，不能把完整 service 回归记录为通过。
- 本步骤只是共享字段和历史回填契约。运行时创建目前仍可能写入 `tenant_id=0`，Conversation/Message/Ticket/Tag 的列表、详情、最终写入、派单和工单操作尚未收紧；公开注册继续关闭。
- KnowledgeCandidate/KnowledgeRetrieveLog、SkillRunLog/AgentRunLog 尚未租户化；文件/Asset、回调、Outbox 消费、WebSocket topic 和异步任务也仍待阶段 6。
- AI 分支新增 `AIManualResumeTask`，引用 Conversation/WxWorkInstance/Message，但本分支没有该模型。合并时必须给它增加 TenantID，并使用新的后续 migration 回填已在 migration 45 之后创建的数据，不能修改已发布 migration 45 的定义。

### 并行分支、合并顺序与回滚

- 开始时已 fetch，`origin/codex/ai-billing@f2d2da4`，其 migration 最高为 33；migration 45 当前无版本冲突。
- 双方同文件为 `internal/models/models.go`。AI 分支还修改 ConversationRouteState repository/service、Message service 和 Conversation handler，但本步骤有意不触碰这些运行时文件，适合作为独立共享契约先合并。
- 建议先合并 migration 45 契约，再由 AI 分支 rebase，逐结构保留其 AIManualResumeTask、企微替换、欢迎语/意图/FastGPT 字段和本步骤 19 个 TenantID；之后双方分别基于同一 Tenant 字段实现 AI/计费日志与客服运行时隔离。
- 回滚运行时代码时不得删除已执行的列、migration 45 记录或清零归属。若 migration 因历史跨租户 Tag/Message/父对象冲突中止，应先修复或拆分明确数据，再重试，禁止把冲突数据批量归 legacy 绕过。

## 30. 多租户阶段 5F/6A：会话、消息、派单与企微协议投递隔离（2026-07-14）

### 目标与完成结果

- Conversation 创建继承 Channel tenant，未关闭会话复用按 tenant 限定；Dashboard 列表、详情和消息读取要求 ActiveTenantID。
- 客服发送、撤回、分配、转派、释放、关闭及其最终写入拒绝跨租户 ID；Conversation 的参与者、消息、路由、已读、分配、事件、Interrupt、同步日志、企微映射和 Outbox 从父会话继承 tenant。
- 派单排班、小组、客服档案、启用用户、实时负载、任务计数及返回展示字段都限定当前租户。
- 企微协议 Outbox 的领取、投递状态和媒体状态更新使用 tenant-qualified 条件；Conversation/Message/Channel/Mapping/Instance 关系执行同租户校验。
- detached MessageSyncLog 与 checkpoint-only ConversationInterrupt 保持 tenant 0 隔离；checkpoint 进入待处理会话时升级为父 Conversation tenant，并拒绝跨租户 checkpoint ID 复用。

### 主要文件与共享契约

```text
internal/builders/conversation_builder.go
internal/handlers/api/conversation_handler.go
internal/handlers/dashboard/agent_team_handler.go
internal/handlers/dashboard/conversation_handler.go
internal/repositories/conversation_repository.go
internal/repositories/message_repository.go
internal/repositories/conversation_route_state_repository.go
internal/repositories/conversation_read_state_repository.go
internal/repositories/conversation_interrupt_repository.go
internal/repositories/channel_message_outbox_repository.go
internal/repositories/wx_work_kf_conversation_repository.go
internal/services/conversation_tenant_guard.go
internal/services/conversation_service.go
internal/services/message_service.go
internal/services/conversation_dispatch_service.go
internal/services/conversation_dispatch_workbench_service.go
internal/services/conversation_human_dispatch_service.go
internal/services/conversation_interrupt_service.go
internal/services/channel_message_outbox_service.go
internal/services/wxwork_protocol_service.go
internal/services/conversation_runtime_tenant_test.go
```

- 本步骤没有 model、migration、request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端变化。
- `ConversationRouteService.GetByConversationID` 与 `MessageService.FindLatestByConversationID` 保留全局 ID 兼容行为；租户敏感路径使用新增 tenant-aware 方法。原因是 AI runtime 和纯单测存在无完整 Conversation 行的局部对象，不能由客服分支改变其基础语义。

### 验证、风险与后续

```text
go test ./internal/services -count=1
go test -race ./internal/services -run 'TestConversation(Runtime|DispatchAndFinalWrites|InterruptRejects)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 上述检查已通过。完整 service 测试本次也通过；历史上存在的 AI 异步回复测试清理竞态未复现，但本步骤没有修改 AI goroutine 生命周期。
- 下一独立批次为 Ticket、TicketProgress、TicketView、Tag、ConversationTag 和 TicketTag 运行时隔离；随后审计 WebSocket、权限遗漏、公司上下文和前端入口隐藏。
- `internal/services/media_understanding_service.go` 的 Message 更新仍缺 tenant 条件，必须由 AI 分支从 Message 继承 TenantID 后修复。本分支不修改该 AI-owned 文件。
- AI 分支还必须保证 ConversationSessionSummary 创建写入 TenantID，并为 AIManualResumeTask 增加 TenantID 和 migration 45 之后的新回填版本。

### 并行分支、合并顺序与回滚

- 本步骤开始及提交前均需 fetch。当前 `origin/codex/ai-billing@f2d2da4`；同文件包括 Conversation builder/handler、RouteState repository/service、Message service、人工派单/超时和企微协议 service。
- 建议先合并阶段 4G 的 Tenant 字段/migration 45，再合并本步骤运行时隔离，之后由 AI 分支 rebase 并逐方法保留双方语义；禁止整文件覆盖。
- 本步骤不需要新的 migration，也不需要 rebase 当前 `origin/codex/customer-audit`。回滚可撤销本步骤 handler/service/repository 调用和新增测试，但不能删除已执行的 TenantID 字段、migration 45 记录或恢复跨租户全局 Dashboard 访问。

## 31. 多租户阶段 5G/6B：工单、标签与个人视图运行时隔离（2026-07-14）

### 目标与完成结果

- 复用现有 Ticket、Tag、ConversationTag 页面和 API，不把工单与会话派单合并，也不新增平行标签模型。
- Ticket 列表、汇总、详情、进展、个人视图、创建、更新、关联客户、指派和状态流转全部按 ActiveTenantID 限定。
- Customer、Conversation、Assignee、Tag 引用在写入前验证同租户；TicketProgress、TicketTag、TicketView 和 ConversationTag 创建继承 tenant，最终更新/删除继续携带 tenant 条件。
- AI Graph 与企微默认资源以系统身份从已有 Conversation 创建 Ticket 时，从父会话继承 TenantID，保持 `codex/ai-billing` 和企微自动服务工单兼容。
- Tag 树的父子关系、同级名称、排序、状态、删除和关联检查均限定当前租户。会话标签筛选从错误的历史表名 `conversation_tag_rels` 修正为当前 `t_conversation_tag`，工单/会话标签子查询均增加 tenant 条件。
- `TagPostUpdate_sort` 补上已有 `tag.update` 权限校验。没有新增权限常量，权限仍由权限管理和角色绑定统一分配。

### 主要文件与契约

```text
internal/handlers/dashboard/ticket_handler.go
internal/handlers/dashboard/tag_handler.go
internal/handlers/dashboard/conversation_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/repositories/ticket_repository.go
internal/repositories/ticket_progress_repository.go
internal/repositories/ticket_view_repository.go
internal/repositories/ticket_tag_repository.go
internal/repositories/tag_repository.go
internal/repositories/conversation_tag_repository.go
internal/services/tenant_operation_guard.go
internal/services/ticket_service.go
internal/services/ticket_view_service.go
internal/services/ticket_tag_service.go
internal/services/tag_service.go
internal/services/conversation_tag_service.go
internal/services/ticket_service_test.go
internal/services/ticket_tag_tenant_test.go
```

- 没有 model、migration、request/response DTO、enum、Gin 路由、权限常量、WebSocket payload 或前端文件变化。
- 现有全局 Ticket/Tag CRUD helper 为内部事件和历史兼容保留；Dashboard handler 不再调用这些 helper。写操作统一通过 service 校验父对象与 tenant，再调用 tenant-qualified repository 方法。
- TicketNoSequence 保持平台全局日序列；本步骤没有 migration 版本，也不改变 TicketNo 唯一索引。

### 验证、风险与后续

```text
go test ./internal/services ./internal/handlers/dashboard -count=1
go test -race ./internal/services -run 'Test(TicketAndTagRuntimeTenantIsolation|SystemTicketCreationInheritsConversationTenant)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 上述检查全部通过。双租户测试覆盖列表/聚合补充字段、详情、进展、创建引用、更新、关联、指派、状态、个人视图、标签树、会话标签和系统建单继承。
- 站内通知从接收账号继承 tenant，但 TicketCreated 的企微 `defaultToUsers` 仍是平台全局配置；后续通知域审计需要明确平台告警与租户主管提醒边界。
- WebSocket topic/订阅、权限与导航遗漏、公司切换后的前端缓存刷新仍在最终阶段审计；公开邀请注册继续关闭。

### 并行分支、合并顺序与回滚

- 开始时已 fetch，`origin/codex/ai-billing@f2d2da4`。AI 分支没有修改 Ticket/Tag 文件；双方同文件仅包含 `internal/handlers/dashboard/conversation_handler.go`，且本步骤只修改标签筛选和移除标签调用。
- 建议在阶段 4G 字段/migration 45 和阶段 5F/6A 会话运行时隔离之后合并本步骤。合并 Conversation handler 时逐块保留 AI 分支回复入口和本步骤 `t_conversation_tag + tenant_id` 条件。
- 本步骤不需要 rebase 当前远端分支。可回滚本步骤 handler/service/repository 调用、权限补漏和新增测试；不得删除已执行的 TenantID 字段、migration 45 数据或恢复跨租户工单/标签查询。

## 32. 多租户阶段 6C：WebSocket 与前端公司上下文隔离（2026-07-14）

### 目标与完成结果

- Dashboard 会话 WebSocket 从当前公司建立连接，要求 `conversation.view + ActiveTenantID`；租户 query 仅对 `/api/ws/*` 生效，不扩大普通 HTTP 的租户来源。
- `admin:all` 替换为 `admin:tenant:{tenantId}`；Dashboard Conversation topic 订阅复用客服组数据范围，访客订阅同时校验连接 tenant、Conversation tenant 和外部身份所有权。
- 访客 topic 和在线状态使用 `tenantId + externalId`，解决不同公司相同企微/访客 ID 的实时碰撞。
- 实时 RouteState、Store、WxWorkInstance 展示读取携带 Conversation Tenant；会话页公司切换会清空旧会话、消息、筛选和员工号缓存，并使会话/通知 WebSocket 重连。
- 员工号列表和会话请求使用序号失效机制，旧公司慢响应不能覆盖新公司页面状态。

### 主要文件与共享契约

```text
internal/builders/conversation_builder.go
internal/services/auth_service.go
internal/services/ws_realtime_types.go
internal/services/ws_service.go
internal/services/ws_service_test.go
internal/services/tenant_auth_context_test.go
internal/services/conversation_human_dispatch_realtime_test.go
web/lib/api/websocket.ts
web/lib/api/websocket.test.mjs
web/lib/api/admin.ts
web/lib/api/notification.ts
web/lib/stores/agent-conversations.ts
web/hooks/use-agent-conversation-realtime.ts
web/components/notification-provider.tsx
web/app/dashboard/conversations/page.tsx
```

- 没有 model、migration、DTO、enum、Gin 路由、新权限点或 WebSocket payload 字段变化。
- `tenantId` query 是 WebSocket 握手的浏览器兼容传输方式；后端认证仅在请求路径以 `/api/ws/` 开头且 Tenant Header 为空时读取，防止普通 API 通过 query 绕过既有 Header 约定。

### 验证、风险与后续

```text
go test ./internal/services ./internal/builders -count=1
go test -race ./internal/services -run 'Test(Ws|AuthenticateUsesPerRequestTenantHeader|AIHandoff)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm exec eslint <本批改动文件>
cd web && node --test lib/api/websocket.test.mjs lib/agent-conversation-realtime.test.mjs app/dashboard/channels/tenant-page.test.mjs
git diff --check
```

- 上述检查通过；改动文件 eslint 只有会话页既有 `<img>` warning，无 error。
- 全量 `pnpm lint` 仍失败于本批外的 content-editor、palette、i18n provider 等既有 React lint 错误；全量 MJS 测试仍失败于 `nav-main.test.mjs` 对已带 `className` 的 SidebarMenuButton 使用过时正则。两项均未由本批引入，不能记录为全量通过。
- 审计确认通知记录从接收账号继承固定 Tenant；客户 session 缓存还会校验唯一 ChannelID，因此当前无需新增平行通知 topic 或重复客户 identity key。
- 下一独立批次为 Asset/文件归属、Dashboard 文件列表和消息附件引用隔离；Knowledge/向量及 AIManualResumeTask/AI 日志需与 AI 分支共同契约后处理。公开注册继续关闭。

### 并行分支、合并顺序与回滚

- 提交前已 fetch，`origin/codex/ai-billing@f2d2da4`。同文件为 `internal/services/ws_service.go`、`internal/builders/conversation_builder.go`、`web/lib/api/admin.ts`。
- 合并时逐方法保留 AI 分支的自动转人工状态、恢复展示、FastGPT/模型 API，以及本批的 tenant topic、订阅校验、实时 tenant 读取和 WebSocket URL 参数；不得整文件选边。
- 本批可独立回滚 WebSocket topic、订阅校验和前端缓存重置，不涉及数据库回滚；回滚会重新暴露跨租户实时风险，因此在替代方案上线前必须继续关闭公开注册和多租户生产入口。

## 33. 多租户阶段 4H/6D：Asset 归属、附件与媒体异步隔离（2026-07-14）

### 目标与完成结果

- 复用现有 Asset 主体，为文件增加 TenantID；不新增平行附件模型、不改变 AssetID、Provider、StorageKey 或前端 AssetResponse 契约。
- migration 46 从显式 Tenant、非平台上传账号、媒体 Message `assetId` 和 HTML `data-asset-id` 确定历史归属；冲突、缺失创建账号/Asset 引用和无 Tenant 的引用消息会整笔回滚，存在但 TenantID 为 0 的平台账号不作为归属证据，无证据 Asset 归 legacy，重复执行幂等。
- Dashboard Asset 列表/详情/上传/删除按 ActiveTenantID；客户和客服上传从 Conversation/ActiveTenant 继承；企微员工号协议和 KF 入站媒体从 Instance/Conversation 继承。
- 图片、语音、视频、附件、GIF 和 HTML 图片在 Message 规范化阶段验证 Asset 与 Conversation 同租户；GIF 补齐原先遗漏的 Asset 存在性与状态校验。
- 企微协议/KF 出站、消息响应元数据补齐和 AI 媒体理解均按 Message Tenant 读取 Asset。媒体理解的 Message、Conversation、RouteState、WxWorkInstance、最新追问和最终 payload 更新也携带 tenant。
- 新文件 StorageKey 采用 `tenants/{tenantId}/...`，平台兼容路径为 `platform/...`，并继续叠加现有 OSS 全局前缀。

### 主要文件与共享契约

```text
internal/models/models.go
internal/migration/000046_backfill_asset_tenants.go
internal/migration/000046_backfill_asset_tenants_test.go
internal/repositories/asset_repository.go
internal/services/asset_service.go
internal/services/asset_tenant_test.go
internal/handlers/api/message_handler.go
internal/handlers/dashboard/asset_handler.go
internal/handlers/dashboard/conversation_handler.go
internal/pkg/utils/message.go
internal/services/im_message_asset.go
internal/services/message_service.go
internal/services/message_service_test.go
internal/services/media_understanding_service.go
internal/services/wxwork_protocol_service.go
internal/services/wxwork_kf_inbound_service.go
internal/services/wxwork_kf_outbound_service.go
```

- 新增共享字段 `Asset.TenantID` 和 DML migration 46；没有 request/response DTO、enum、路由、WebSocket payload、新权限点或前端变化。
- migration 46 创建前已核对 `origin/main` 最高 20、`origin/codex/ai-billing` 最高 33、本分支最高 45，无版本冲突。DDL 继续由 AutoMigrate 先执行。

### 验证与已知边界

```text
go test ./internal/migration -run 'TestBackfillAssetTenants' -count=1
go test -race ./internal/migration -run 'TestBackfillAssetTenants' -count=1
go test -race ./internal/services -run 'TestAsset(RuntimeTenantIsolation|StoragePrefixIncludesTenant)' -count=1
go test ./internal/services ./internal/handlers/api ./internal/handlers/dashboard ./internal/pkg/utils ./internal/migration -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 上述检查全部通过。migration 测试覆盖上传账号、平台账号、结构化附件、HTML、显式 Tenant、legacy、幂等、跨租户共享冲突、缺失创建账号和事务回滚；运行时测试覆盖列表/读取/删除、图片/GIF/HTML 引用和媒体异步最终更新。
- `/api/asset/file/{assetId}` 和本地静态 URL 继续是客户/企微展示使用的 bearer URL；AssetID 不可预测但不等于授权。统一短期签名必须同时覆盖 local 与 OSS provider，未完成前公开注册和多租户生产入口继续关闭。
- `internal/services/store_ai_model_setting_service.go` 仍有 AI-owned 的全局 RouteState 读取；Knowledge/向量与 AI 新增日志/AIManualResumeTask 也未完成 Tenant 契约，不在本批冒充完成。

### 并行分支、合并顺序与回滚

- `origin/codex/ai-billing@f2d2da4` 同时修改 `models.go`、Message service、media understanding、企微协议 service 和 Conversation handler。建议先合并 Asset 字段/migration 46，再逐方法合并运行时隔离，最后重放 AI 分支 usage/计费和 FastGPT 变化。
- media understanding 合并时，AI 分支的 usage capture、模型测试、自动触发增强必须保留；本批的 `message.TenantID` 参数、Asset/Message/Conversation/Route/Instance tenant 查询和最终 tenant 更新也必须保留。
- 回滚运行时代码不得删除已执行的 Asset TenantID、migration 46 记录或清零归属。若 migration 因共享 Asset 冲突中止，应拆分或修复明确文件引用后重试，禁止把冲突文件直接归 legacy。

## 34. 多租户阶段 4I/6E：KF 同步游标、Outbox 与 CLI bridge 隔离（2026-07-14）

### 目标与完成结果

- 复用现有 `WxWorkKFSyncState`、KF mapping/message ref 和统一 ChannelMessageOutbox，不新增渠道状态表或平行投递模型。
- `WxWorkKFSyncState` 增加 TenantID；migration 47 根据相同 openKfId 的 KF Channel 回填，跨租户重复、显式冲突、无效 Tenant 和孤立游标整笔回滚，重复执行幂等。
- KF callback 从 Channel 固化本批 Tenant，游标、每条消息、mapping、message ref 和失败回调均在该租户内处理。
- KF worker 按每条 Outbox Tenant 读取 Message、Conversation、Mapping、Channel 和 Asset，并以 tenant 条件 claim/更新；Outbox 创建验证父会话和消息归属。
- 员工号协议共用的 message ref 查询、撤回、回声修复和语音 ref 恢复按 instance/message tenant；没有改变员工号协议字段或 AI 回复语义。
- CLI bridge 由 bridge token 命中的 Channel Tenant 限定入站幂等、会话复用、outbox poll 和 sent/failed 回写；双租户测试验证 A 公司凭据看不到也不能完成 B 公司任务。
- `setupMessageWelcomeTestDB` 默认关闭真实异步 AI hook，避免测试 fixture 关闭 DB 后后台 goroutine 继续访问全局 DB；需要验证 hook 的测试仍显式安装 stub，生产代码不变。

### 主要文件与共享契约

```text
internal/models/models.go
internal/migration/000047_backfill_wxwork_kf_sync_state_tenants.go
internal/migration/000047_backfill_wxwork_kf_sync_state_tenants_test.go
internal/repositories/wx_work_kf_sync_state_repository.go
internal/repositories/wx_work_kf_conversation_repository.go
internal/repositories/wx_work_kf_message_ref_repository.go
internal/repositories/channel_message_outbox_repository.go
internal/services/wx_work_kf_sync_state_service.go
internal/services/wx_work_kf_conversation_service.go
internal/services/wx_work_kf_message_ref_service.go
internal/services/channel_message_outbox_service.go
internal/services/wxwork_kf_inbound_service.go
internal/services/wxwork_kf_outbound_service.go
internal/services/wxwork_cli_bridge_service.go
internal/services/wxwork_protocol_service.go
internal/services/media_understanding_service.go
internal/services/wxwork_kf_tenant_test.go
internal/services/wxwork_protocol_service_test.go
internal/services/message_service_test.go
```

- 新增共享字段 `WxWorkKFSyncState.TenantID` 和 DML migration 47；没有 DTO、enum、路由、WebSocket payload、权限点或前端变化。
- migration 47 创建前已核对 `origin/main` 最高 20、`origin/codex/ai-billing` 最高 33、本分支最高 46，无版本冲突。DDL 继续由 AutoMigrate 先执行。

### 验证与运行边界

```text
go test ./internal/migration -run '^TestBackfillWxWorkKFSyncStateTenants' -count=1
go test -race ./internal/migration -run '^TestBackfillWxWorkKFSyncStateTenants' -count=1
go test -race ./internal/services -run 'TestWxWork(KFRuntimeTenantIsolation|CLIBridgePollsOnlyChannelTenant)|TestWxWorkProtocolReferencedRecallMarksOriginalMessageRecalled|TestWxWorkProtocolEmployeeOutgoingEchoRepairsLegacyRef' -count=1
go test ./internal/services ./internal/handlers/third ./internal/bootstrap -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 上述专项 migration、runtime/race、协议测试、完整 services/migration 测试、全仓编译和 vet 均已通过。
- KF `DispatchPendingOutbox` 当前未注册 cron，本批只隔离代码，不启用旧能力；员工号协议 cron 不变。
- CLI、企业微信客服号和员工号协议仍是三种独立入口，仅复用统一消息账本/mapping/outbox 数据结构，不互相借用协议字段。

### 并行分支、合并顺序与回滚

- `origin/codex/ai-billing@f2d2da4` 同文件为 `models.go`、`media_understanding_service.go`、`message_service_test.go`、`wxwork_protocol_service.go` 和 `wxwork_protocol_service_test.go`。
- 建议先合并 SyncState 字段/migration 47 与 repository/service tenant 原语，再逐方法合并协议 ref/voice 调用，最后重放 AI 分支 usage capture 和回复增强测试。不得整文件选边。
- 可回滚 KF/CLI 运行时调用和新增测试，但不得删除已执行的 TenantID 字段、migration 47 记录或把冲突/孤立游标归 legacy。回滚隔离会重新暴露跨租户 outbox 风险，替代方案上线前公开注册保持关闭。

## 35. 多租户阶段 4J/6F：知识域、首页指标与本地向量隔离（2026-07-14）

### 目标与完成结果

- 8 个现有知识实体增加 TenantID；migration 48 依据 Store、WxWorkProtocolInstance、ConversationRouteState、Conversation、KnowledgeBase 和非平台 User 的真实引用回填，冲突/缺失引用整笔回滚，无证据旧知识库归 legacy，重复执行幂等。
- Dashboard 知识库、文档、FAQ、候选和检索日志按 ActiveTenantID 与客服组范围共同隔离；详情、写动作、排序、索引、调试、审核、导出和员工号绑定不再接受其他公司 ID。
- Candidate 自动生成、RetrieveLog/Hit 写入、Chunk 生成/替换/删除和索引状态写回均继承并校验 Tenant。
- Qdrant point payload 增加 `tenant_id`，查询强制 `tenant_id + knowledge_base_id`，缺少 Tenant 时 fail closed；关系库 hydrate 再按 Tenant 读取知识对象，混合租户检索报错。
- 首页总览原先为全平台统计，本批同步修正为当前公司；AI Agent 数暂按当前公司 Channel 引用，Skill 失败数按关联 Conversation Tenant。
- migration 将 Document/FAQ 标为 pending，提醒历史向量必须重建，避免旧无 Tenant payload 继续参与召回。

### 主要文件与共享契约

```text
internal/models/models.go
internal/migration/000048_backfill_knowledge_domain_tenants.go
internal/migration/000048_backfill_knowledge_domain_tenants_test.go
internal/repositories/knowledge_*_repository.go
internal/repositories/dashboard_repository.go
internal/services/knowledge_*_service.go
internal/services/knowledge_tenant_service_test.go
internal/services/agent_team_scope_service.go
internal/services/dashboard_service.go
internal/services/wx_work_protocol_instance_service.go
internal/handlers/dashboard/knowledge_*_handler.go
internal/handlers/dashboard/dashboard_handler.go
internal/handlers/dashboard/wxwork_protocol_instance_handler.go
internal/ai/rag/index*.go
internal/ai/rag/retrieve*.go
internal/ai/rag/vectordb/payload.go
internal/ai/rag/vectordb/qdrant.go
internal/ai/rag/vectordb/qdrant_test.go
```

- 新增共享字段：8 个知识实体的 TenantID；新增 DML migration 48；Qdrant payload/filter 增加内部 tenant 字段。
- 没有前端 JSON DTO、enum、Gin 路由、WebSocket payload、新权限点或页面入口变化；首页 API 返回结构不变，仅统计口径改为当前公司。

### 验证与部署边界

```text
go test ./internal/ai/rag/... ./internal/repositories ./internal/services ./internal/handlers/dashboard ./internal/migration -count=1
go test -race ./internal/migration -run '^TestBackfillKnowledgeDomainTenants' -count=1
go test -race ./internal/services -run 'Test(KnowledgeRuntimeTenantIsolation|DashboardOverviewUsesActiveTenant)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 专项覆盖：完整父子回填与幂等、共享知识库冲突回滚、子实体显式冲突回滚、后台读写隔离、Candidate 三方冲突、RetrieveLog 跨租户拒绝、首页指标隔离、Qdrant Tenant filter/fail-closed 和混合租户检索拒绝。
- migration 48 后必须触发每个有效知识库的 rebuild；重建前本地向量召回为空是预期行为，禁止移除 Tenant filter 兼容旧 point。
- FastGPTDatasetJob、KnowledgeResourceGroup/Item、AI usage/run log、AIManualResumeTask 和 AIAgent/AIConfig Tenant 仍需与 AI 分支合并后完成；文件 URL 签名仍未完成，因此公开注册保持关闭。

### 并行分支、合并顺序与回滚

- 开始、提交前及 push 后均已 fetch，`origin/codex/ai-billing@f2d2da4`。AI 分支同文件集中于 `models.go`、KnowledgeBase service/builder、KnowledgeRetrieveLog repository、RAG retrieve/log/answer 和前端 knowledge API；vectordb provider 当前无同文件修改。合并 RetrieveLog repository 时保留 AI 分支 `FindRecentQuestions` 与本批 tenant-aware list/detail 方法，并在其进入运行链前把 TenantID 作为必填查询边界。
- 建议先合并本批字段/migration/repository 和 vectordb 契约，再逐方法合并 AI 分支 FastGPT/intent/usage 逻辑，最后补新增 Resource/Job/usage 模型 Tenant 并执行双租户 FastGPT 测试。
- 可回滚后台 handler/service 和 Qdrant运行时代码，但不得删除已执行的 TenantID 字段、migration 48 历史或把冲突数据强行归 legacy。回滚 Qdrant Tenant filter 会重新开放跨租户向量风险，不得在多租户环境执行。

## 36. 多租户阶段 6G：Asset 短期签名、本地静态绕行与头像兼容（2026-07-14）

### 目标与完成内容

- 复用现有 `/api/asset/file/{assetId}`，将裸 AssetID bearer 下载改为 HMAC 短期签名。签名绑定版本、AssetID、TenantID、expires 和 purpose；支持 `inline`、`wxwork_cdn`，无签名/篡改为 403，过期为 410。
- handler 验签后只用 `asset_id + tenant_id` 读取成功状态 Asset；local、OSS 和外部协议媒体都先经过同一应用授权入口。外部 URL 仅在成功验签后重定向。
- 删除 Go `StaticFS` 本地目录挂载和 Next dev `/storage/*` 代理，阻止知道 StorageKey 后绕开签名路由。
- AssetResponse 保留结构但不再返回 StorageKey；上传 URL、普通媒体消息、HTML 图片、REST 消息、WebSocket 消息和企微 CDN 拉取都生成应用签名 URL。前端删除裸 AssetID URL fallback。
- 消息编辑器只提交 `data-asset-id`。服务端仍兼容历史 provider/StorageKey 三字段输入并校验，但标准化入库和响应均去除 StorageKey；历史 Message payload 无需重写。
- `AgentProfile.Avatar` 继续保存 URL，不增加模型字段。客服档案、消息 builder 和 WebSocket 会将旧本地/OSS URL或已过期应用 URL按 Profile/Message Tenant 重新签发；外部头像原样返回。
- 新增 `storage.assetURLSigningSecret`、`storage.assetURLTTLSeconds` 和 `AGENT_DESK_ASSET_URL_SIGNING_SECRET`。关闭公开注册时允许从 customer session secret 做领域隔离派生；开启注册时启动校验强制独立密钥。

### 主要文件与共享契约

```text
config/config.example.yaml
internal/pkg/config/config.go
internal/pkg/config/config_test.go
internal/pkg/assetaccess/asset_access.go
internal/pkg/assetaccess/asset_access_test.go
internal/handlers/api/asset_handler.go
internal/handlers/api/asset_handler_test.go
internal/services/asset_service.go
internal/services/asset_tenant_test.go
internal/services/im_message_asset.go
internal/pkg/utils/message.go
internal/pkg/utils/message_test.go
internal/builders/asset_builder.go
internal/builders/asset_builder_test.go
internal/builders/agent_profile_builder.go
internal/builders/conversation_builder.go
internal/services/ws_service.go
internal/services/wxwork_protocol_service.go
internal/bootstrap/server.go
internal/bootstrap/server_route_test.go
web/components/chat/shared-message-editor.tsx
web/lib/im-editor-image.ts
web/lib/im-message.ts
web/next.config.ts
```

- 没有 model、migration、request/response DTO 字段、enum、Gin 路由、权限常量或 WebSocket payload 变化。现有 AssetResponse 的 `storageKey` 字段为兼容保留但返回空字符串，`url` 字段语义由 provider URL 变为应用签名 URL。
- 当前本分支 migration 最高 48；本步骤不占用 49。权限继续复用既有 Asset/Conversation 权限和客户会话鉴权，没有角色内隐藏授权。

### 验证

```text
go test ./... -count=1
go test -race ./internal/pkg/assetaccess ./internal/handlers/api ./internal/services -run 'TestAsset' -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm exec eslint lib/im-editor-image.ts lib/im-message.ts components/chat/shared-message-editor.tsx next.config.ts
git diff --check
```

- 单元测试覆盖有效、过期、篡改 asset/tenant/purpose、跨租户有效签名但无归属、无签名、外部重定向、local 真实文件读取、静态目录绕行、AssetResponse 去 StorageKey、旧头像 URL 刷新及 HTML assetId-only 编辑。
- 全量 Go 测试、专项 race、`go vet ./...`、前端 typecheck、改动文件 ESLint 和 `git diff --check` 均通过。

### 风险、回滚与并行分支

- 签名 URL 在有效期内仍可被持有者转交；当前不绑定具体账号或浏览器。下载权限的来源仍是生成该 URL 的会话、文件或企微业务接口。
- 公开 OSS bucket 的 ACL 不受应用控制；多租户生产必须使用私有 bucket。本批停止对外发送 StorageKey，但不能把应用签名误解为替代 OSS ACL。
- 可回滚签名构建和前端 assetId-only 编辑，但不能只恢复 `StaticFS` 或无签名 Asset handler，否则会重新暴露跨租户文件。回滚应恢复为受鉴权代理下载或另一套完整授权方案。
- 开始时已 fetch，`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`。AI 分支同文件包括 config、server、conversation builder、message utils、Asset handler/service、企微协议和测试。合并按方法保留 AI 分支 Email/FastGPT/NewAPI/usage/回复逻辑及本批签名/tenant 边界，禁止整文件覆盖。
- 建议合并顺序：Asset Tenant/migration 46 -> 本批 assetaccess/下载路由/响应收口 -> AI 分支欢迎图、媒体理解、FastGPT 与 usage 重放。AI 新增资源只保存 AssetID 并复用签名入口。

## 37. 多租户阶段 6H：平台存储与企微设备池权限收口（2026-07-14）

### 目标与完成内容

- 审计系统设置导航后确认：平台全局存储配置原复用 `asset.view/create`，平台全局企微设备池原复用 `channel.view/update`，导致公司主管可直接调用接口读取或修改平台级配置。
- 新增 `storageSetting.view/update` 与 `wxworkDevicePool.view/update/sync` 五项 platform scope 权限；权限会显示在权限管理页，不在角色或 handler 中隐藏授权。
- 超级管理员和平台管理员获得新权限；tenant_admin 及以下不获得。租户 Asset/Channel 权限继续保留原业务能力，不再进入平台设置 handler。
- 存储 handler 和设备池 handler 在权限校验后再次要求 `IsPlatformAccount`。设备池列表/设置、配置修改和同步分别使用 view/update/sync，避免动作权限混用。
- 导航复用原页面，只将“存储设置”和“企微设备池”可见条件切换为新权限；没有新增平行页面。
- migration 49 幂等调用现有权限/内置角色同步函数。没有 model、AutoMigrate、request/response DTO、enum、Gin 路由或 WebSocket payload 变化。

### 主要文件

```text
internal/pkg/constants/auth.go
internal/migration/000049_sync_platform_system_permissions.go
internal/migration/000049_sync_platform_system_permissions_test.go
internal/handlers/dashboard/storage_setting_handler.go
internal/handlers/dashboard/storage_setting_handler_test.go
internal/handlers/dashboard/wxwork_protocol_device_pool_handler.go
internal/handlers/dashboard/authz_handler_test.go
web/lib/navigation.tsx
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

### 验证与风险

```text
go test ./internal/migration ./internal/handlers/dashboard ./internal/services -count=1
go test -race ./internal/migration ./internal/handlers/dashboard -run 'Test(SyncPlatformSystemPermissions|StorageSetting|WxWorkDevicePool)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm exec eslint lib/navigation.tsx
git diff --check
```

- 专项覆盖 migration 幂等、权限 scope/APIPath、平台管理员与公司主管默认关系、租户账号即使持有平台权限仍拒绝、平台账号仅持有旧 Asset/Channel 权限仍拒绝、平台正常读写存储和设备池设置。
- 全量 Go 测试、专项 race、`go vet ./...`、前端 typecheck、导航 ESLint 和 `git diff --check` 均通过；本地真实库已执行 migration 49，五项权限均为 platform scope，只有 `super_admin/admin` 获得默认绑定，两个原页面均返回 200。
- 本批只改变后台授权，不改变任何企微员工号协议字段或远程请求；无需查询协议文档字段。设备池仍是平台全局库存，租户通过自己的 WxWorkProtocolInstance 使用已分配实例。
- 回复意图配置属于 AI 分支真实回复引擎，当前不改。合并后需按代码确认平台模板/租户配置语义，再决定权限 scope，禁止依据旧回复意图文档恢复旧逻辑。
- migration 49 创建前已核对 `origin/main@e67e207` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支最高 48。共享文件为 `auth.go`、migration 目录和 `navigation.tsx`；合并时逐权限/导航项保留双方变化。
- 可回滚 handler 和导航，但不能只回滚平台账号校验而保留旧 Asset/Channel 权限，否则重新开放越权。若必须回滚，应同时禁用页面和路由直到替代授权上线；已执行 migration 49 的权限记录可保留，不影响租户业务。

## 38. 多租户阶段 7B：邀请注册、邀请码浮窗与账号审核前端（2026-07-14）

### 目标与完成内容

- 复用现有用户管理页完成邀请注册闭环，没有创建第二个账号管理入口。账号页新增“账号 / 注册审核”标签，原后台创建、门店员工归组、角色分配、启停和密码操作保持原语义。
- 账号表增加注册来源/审核状态；“邀请注册”浮窗展示当前公司邀请码、绝对注册链接、使用次数和生命周期信息。重置前明确告知旧邀请码及旧链接立即失效。
- 注册审核列表按 pending/approved/rejected 查询。通过审核只显示 `assignable` 且启用的角色，并要求 `tenantRegistration.review + user.assignRole + role.view`；拒绝必须填写原因且不提交角色。
- 新增 `/register` 页面。页面读取公开 AuthOptions 开关，自动校验 URL 邀请码并显示公司身份；提交相同内容重试时复用 `X-Request-Id`，成功后只显示等待公司主管审核，不自动登录。
- 登录页只有在 `tenantRegistrationEnabled=true` 时展示注册链接。仓库正式配置未开启公开注册；邀请浮窗会在开关关闭时明确标注链接暂不可用。
- 修复前后端分端口时公共请求的 CORS 预检：允许请求客户端已经使用的 `Accept-Language` 和 `X-Locale`。没有扩大 allowed origin，也没有开放 credentials 或绕过鉴权。

### 主要文件与契约

```text
internal/bootstrap/server.go
internal/bootstrap/server_route_test.go
internal/handlers/api/auth_handler.go
internal/pkg/dto/response/auth_response.go
web/app/register/page.tsx
web/app/register/_components/registration-form.tsx
web/app/register/tenant-registration-ui.test.mjs
web/app/dashboard/users/page.tsx
web/app/dashboard/users/_components/invitation-dialog.tsx
web/app/dashboard/users/_components/registration-review.tsx
web/components/login-form.tsx
web/components/ui/alert.tsx
web/lib/api/auth.ts
web/lib/api/tenant-registration.ts
web/messages/zh-CN.json
web/messages/en-US.json
```

- AuthOptions response 增加 `tenantRegistrationEnabled`。这是向后兼容新增字段；旧前端忽略，新前端缺失时按 false 处理。
- 没有 model、AutoMigrate、DML migration、enum、Gin 路由、WebSocket payload 或新权限点。继续使用阶段 3A/3B 已进入权限管理的 `tenantInvite.*`、`tenantRegistration.*`、`user.assignRole` 和 `role.view`。
- 新增 shadcn alert 基础组件只用于注册不可用、公司身份和错误状态，没有修改其他基础组件。

### 验证与环境发现

```text
go test ./internal/bootstrap -run 'TestNewServerExposesPublicAuthOptions|TestNewServer' -count=1
go test ./... -count=1
go test -race ./internal/bootstrap ./internal/services -run 'Test(NewServer|TenantRegistration)' -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm build
cd web && node --test app/register/tenant-registration-ui.test.mjs
cd web && pnpm exec eslint <本批前端文件>
git diff --check
```

- 全量 Go 测试、注册/Server 专项 race、`go vet`、Next 生产构建、typecheck、本批 ESLint 和 5 项注册 UI 契约测试均通过。
- 全量前端 Node 历史测试为 52/53 通过；唯一失败是 `components/nav-main.test.mjs` 仍用 `/render={<SidebarMenuButton \/>}/` 匹配源码，而现有 `nav-main.tsx` 已是 `render={<SidebarMenuButton className="rounded-xl" />}`。本批未修改导航组件或该历史测试，不能将该源码正则漂移计为注册功能失败，也不在本批夹带修复。
- 浏览器使用当前源码后端和独立临时 SQLite 验证：账号/审核标签、权限入口、邀请信息失败态、注册开关关闭态、完整注册表单、自动邀请码校验、桌面与 390x844 窄屏均正常；账号页和注册页无页面级横向溢出。
- 使用旧 Docker MySQL 数据启动当前源码时，migration 39 因 `agent team 1 leader tenant 0 conflicts with team tenant 1` 主动失败。该环境存在历史平台账号担任租户客服组长的脏关系；不得跳过校验或猜测归属，部署前需由业务确认正确租户/组长后修复数据，再重跑 migration。
- Docker 镜像构建尝试因拉取 `docker/dockerfile:1.7` 元数据超时中止，未形成代码编译失败证据；最终构建结果以本批提交前的 Go/Next 全量验证为准。

### 并行分支、合并顺序与回滚

- 开始时已 fetch，`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`。AI 分支同文件为 `server.go`、`server_route_test.go`、AuthOptions handler/DTO、登录页、auth API 和中英文消息。
- 建议先合并租户认证/权限/阶段 3B 注册后端，再逐段合并 AuthOptions 与 CORS，最后合并注册页和用户页。必须保留 AI 分支邮箱验证/FastGPT/NewAPI/usage 选项和本批 tenant registration 开关、语言头及权限显隐。
- 本批不需要 rebase 才能独立提交，但最终集成禁止整文件选边。没有修改 AI runtime、模型供应商、FastGPT、token、usage 或计费语义。
- 可回滚前端注册入口、审核标签和 AuthOptions 新字段；后台阶段 3B 路由可继续保持关闭。CORS 语言头是现有请求客户端的必要兼容，不建议随 UI 回滚。邀请码及已审核账号数据不应通过删除表或破坏性 DDL 回滚。

## 39. 多租户阶段 7C：公司上下文与导航分层（2026-07-14）

### 目标与完成内容

- 品牌下新增唯一公司上下文入口。租户账号显示所属公司；平台账号可搜索启用公司、进入公司、返回平台管理或前往“接入公司”管理页。
- 认证 `LoginResponse`/资料响应增加 `activeTenantName`，名称由已验证 Tenant 产生。普通租户账号不需要平台 `tenant.view` 权限即可显示公司身份。
- 导航从旧“接待中心/客服配置/AI能力/系统管理”混排改为“当前公司/客服组织/服务能力/账号与权限/平台管理”。每个入口同时受现有查看权限和平台/Active Tenant 上下文约束。
- 平台账号未选择公司时只显示角色权限与平台入口；进入公司后同时显示公司业务和平台入口。访问公司专属 URL 时若缺少 Active Tenant，布局自动回到 `/dashboard/channels`，不会先请求租户业务接口。
- 恢复已有但漏掉导航的 `/dashboard/ai-agents` 与 `/dashboard/wxwork-protocol-instances`，分别复用 `aiAgent.view` 与 `channel.view`；原租户内“公司管理”文案改为“客户企业”。
- 修复 `nav-main.test.mjs` 对现有 `SidebarMenuButton className` 的过时源码正则，未改变 NavMain 运行逻辑。

### 主要文件与共享契约

```text
internal/pkg/dto/dto.go
internal/pkg/dto/response/auth_response.go
internal/services/auth_service.go
internal/services/tenant_auth_context_test.go
web/app/dashboard/layout.tsx
web/app/dashboard/channels/page.tsx
web/app/dashboard/channels/tenant-page.test.mjs
web/components/app-sidebar.tsx
web/components/auth-provider.tsx
web/components/tenant-context-switcher.tsx
web/components/tenant-context-switcher.test.mjs
web/components/nav-main.test.mjs
web/lib/auth.ts
web/lib/auth.test.mjs
web/lib/navigation.tsx
web/lib/navigation.test.mjs
web/messages/zh-CN.json
web/messages/en-US.json
```

- 共享 JSON 只向后兼容增加 `activeTenantName`；没有 model、migration、enum、Gin 路由、WebSocket payload 或权限点变化。
- 账号与角色/权限关系不变：账号仍只分配角色，角色绑定权限；上下文过滤不是角色名白名单，也不替代后端鉴权。
- 用户主动切换公司时，资料刷新使用“保留会话并抛错”模式；失败后恢复原 Tenant。普通认证过期仍清理登录态并返回登录页，二者不混用。
- Active Tenant ID 和名称均按标签页存储；测试覆盖共享 `localStorage` 被另一标签写入后，当前标签仍保留自己的公司 ID 与名称。

### 验证、风险与后续

- 专项 Go 测试覆盖平台切换和租户账号的 Active Tenant 名称；前端测试覆盖平台无公司、租户账号、平台进入公司、权限过滤、URL 上下文守卫和切换失败恢复。
- 全量 Go、专项 race、`go vet`、前端 62 项 Node 测试、typecheck 与 Next 生产构建通过。
- 浏览器使用当前源码临时 SQLite 后端验证平台模式、进入默认公司、返回平台、完整导航和公司总览；桌面与 `390x844` 移动侧栏无页面级横向溢出。
- 当前公司渠道设置页面仍待后续迁移；AI Config/回复意图最终作用域须合并 AI 分支后按真实代码确认。公开注册继续关闭。
- 旧 Docker MySQL 的 migration 39 仍因平台账号担任租户客服组长而中止，必须由业务确认正确组长后修复数据，不能在本批绕过。

### 并行合并与回滚

- `codex/ai-billing@f2d2da4` 与本批重叠 `auth_response.go`、`navigation.tsx` 和双语资源，并在认证链路有不同实现。合并时保留 AI 分支邮箱/FastGPT/NewAPI/计费入口，同时保留 `activeTenantName`、上下文导航和权限过滤。
- 可独立回滚前端导航和 `activeTenantName` 展示字段；回滚不能恢复角色 URL 白名单，也不能让平台未选公司时访问租户页面。无数据库回滚边界。

## 40. 多租户阶段 7D：接入公司运营资源摘要（2026-07-14）

### 目标与完成内容

- 审计“接入公司”列表后确认，设计要求的客服数、门店数、客服组数和最后活跃时间尚未接入真实数据；此前相关主体尚无 TenantID，现有模型已经具备可靠租户归属，因此本批补齐该安全缺口。
- `TenantRepository.FindOperationalStats` 对当前分页 TenantID 批量聚合 `AgentProfile`、`Store`、`AgentTeam`、`Conversation` 和 `User`，没有 N+1 查询。删除资源排除，停用资源保留在存量计数中。
- `TenantService.FindOperationalStats` 负责比较最新会话活跃与最新未删除账号登录；builder 将聚合结果映射到 response DTO，handler 只编排 service 和 builder。
- `/dashboard/channels` 保持唯一“接入公司”入口，只增加紧凑的资源/活跃列；旧 Channel 管理、企微员工号配置和 AI Agent 绑定没有回填到该页面。

### 主要文件与契约

```text
internal/repositories/tenant_repository.go
internal/services/tenant_service.go
internal/pkg/dto/tenant.go
internal/pkg/dto/response/tenant_response.go
internal/builders/tenant_builder.go
internal/handlers/dashboard/tenant_handler.go
internal/services/tenant_management_service_test.go
web/lib/api/tenant.ts
web/app/dashboard/channels/page.tsx
web/app/dashboard/channels/tenant-page.test.mjs
web/messages/zh-CN.json
web/messages/en-US.json
```

- `TenantResponse` 新增 `agentCount`、`storeCount`、`agentTeamCount`、`lastActiveAt`，属于向后兼容的只读字段。无 request DTO、model、AutoMigrate、DML migration、enum、Gin 路由、权限点或 WebSocket 变化。
- SQLite 的 datetime 聚合值和 MySQL `parseTime=True` 返回形态不同，repository 的扫描器兼容 string、`[]byte` 和 `time.Time`，不使用数据库私有日期函数。
- `tenant_repository_test.go` 覆盖 MySQL 常见 `time.Time` 与 SQLite string/`[]byte` 扫描形态。真实 MySQL 验证仍受既有 migration 39 `agent team 1 leader tenant 0 conflicts with team tenant 1` 阻断，未绕过该业务冲突。
- 新增双租户 service 测试，覆盖跨租户隔离、删除/停用资源口径、删除账号排除，以及登录和会话活跃时间比较。前端契约测试覆盖四个字段的真实展示。
- 浏览器用全新 SQLite 执行既有 `customer_audit_seed` 时发现旧脚本遗漏 TenantID：Store、StoreStaffBinding、WxWorkProtocolInstance 和模拟 Conversation 全部落在 tenant 0，导致列表门店数为 0 且派单池无法读取仿真会话。本批同步补齐这些主体及 RouteState、Participant、Message、Assignment、EventLog 的租户继承。
- 历史零租户数据只在 Remark 带 `TEST_SEED:` 时允许修复；异租户或无标记记录拒绝重绑。`simulation_test.go` 新增资源创建/修复和会话全子记录租户测试，避免脚本再次绕过运行时隔离。

### 并行分支、合并与回滚

- 开始前已 fetch；`origin/codex/ai-billing@f2d2da4` 只与本批重叠双语资源文件，并新增与本批不同的 `nav.replyIntentProfiles` key。合并时逐 key 保留，无需整文件选边。
- 本批自身不触碰 AIAgent/AIConfig、FastGPT、模型供应商、回复 runtime、token、usage 或计费；后续第 33 批已完成 AIAgent 租户契约。当前公司 Channel 管理入口仍需单独恢复，不能把公司统计或 Agent 隔离完成误认为页面职责已经迁移。
- 可独立回滚聚合方法、response 字段和前端列，无数据回滚边界；回滚不应影响阶段 7C 的公司上下文和导航。
- `cmd/customer_audit_seed` 与 AI 分支无同文件修改。脚本修复后的测试数据已具备正数 TenantID；回滚脚本不应把数据库记录改回 tenant 0，清理继续按 `TEST_SEED:<batch>` marker 执行。

### 验证结果

```text
go test ./... -count=1
go test -race ./cmd/customer_audit_seed ./internal/services ./internal/repositories -run 'Test(SeedResourceUpsertsInheritTenantID|SimulationRecordsInheritTenantID|TenantOperationalStats)' -count=1
go vet ./...
cd web && node --test $(rg --files . | rg '\.test\.mjs$' | sort)
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- Go 全量、专项 race、vet、67 项前端测试、typecheck、生产构建和 diff 检查通过。
- 当前源码 + 全新 SQLite + 修复后仿真脚本的浏览器验证显示：客服 12、门店 100、客服组 3，最近活跃取模拟会话时间。桌面列表使用内部横向滚动；`390x844` 下 document width 保持 390，统计单元格宽/scrollWidth 均为 245，无页面级横向溢出或文本溢出。

## 第 33 批：AIAgent 租户隔离与运行时引用收口（2026-07-14）

### 目标与复用判断

- 复用现有 `/dashboard/ai-agents`、`aiAgent.*` 权限、Channel/Conversation 模型和 AI runtime，不新增平行页面、权限、状态或独立 Agent 模型。
- 修复 AIAgent 全局列表/详情/写入，以及 Channel 只校验 Agent 存在未校验同租户的问题，使当前公司 AI 客服、渠道、会话和派单形成同一租户边界。
- AIConfig 按现有权限和阶段 6.1 继续作为平台模型配置；本批不修改模型供应商、FastGPT、回复策略、Token、usage 或计费。

### 文件与契约变化

- `internal/models/models.go`：AIAgent 新增 `TenantID`；客户端请求不能指定。
- `internal/migration/000050_backfill_ai_agent_tenants.go`：从显式 Tenant、Channel、Conversation、TeamIDs、KnowledgeIDs 和非平台审计账号解析历史归属；冲突或缺失引用事务回滚，无证据归 `legacy-default`。
- `internal/repositories/ai_agent_repository.go`、`internal/services/ai_agent_service.go`：增加 tenant-aware 读写、公司内名称唯一、同租户 Team/Knowledge 校验，最终更新/排序带 `id + tenant_id`。WithTx 校验读取使用同一事务连接。
- AI Agent dashboard handler 按 Active Tenant 列表和详情；响应中的 Team/Knowledge 名称也按 `item.TenantID` 读取。沿用已有权限，不增加隐藏权限。
- Channel、Conversation、AI reply、派单、转人工、企微 KF 入站、默认资源、WebSocket、消息 builder 和首页统计均使用已知 Tenant 读取 Agent；小程序移除无 Channel 的全局 Agent 扫描。
- Skill 调试在调用现有 runtime Hook 前校验 Agent、Conversation、CheckPoint 属于当前公司。企微员工号继续使用当前动态 runtime Agent，并继承 Instance Tenant；未恢复旧 `ai_agent_id`。
- `cmd/testdata` 的 AI Agent 与 Channel seed 显式归入 `legacy-default`，upsert 查询带 Tenant，避免 migration 后重新制造 tenant 0 数据。
- 没有 request/response DTO、enum、Gin 路由、WebSocket payload 或新权限变化。

### 测试与结果

```text
go test ./... -count=1
go test -race ./internal/services -run 'Test(AIAgent|ConversationRuntime|ConversationHumanDispatch|ChannelServiceEnforcesTenantContext)' -count=1
go test -race ./internal/migration -run 'TestBackfillAIAgentTenants' -count=1
go vet ./...
cd web && node --test $(rg --files . | rg '\.test\.mjs$' | sort)
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- migration 覆盖多证据一致、幂等、两租户共享冲突回滚、缺失 Team 和缺失 Agent 引用。
- service 覆盖列表/详情隔离、创建继承、跨租户同名、跨租户更新/启停/排序/删除拒绝、Team/Knowledge 引用拒绝、Channel/Conversation 绑定拒绝和 repository 最终写条件。
- 全量 Go、专项 race、vet、67 项前端测试、typecheck 和生产构建通过。
- 当前源码 `8085` + 前端 `3000` 浏览器双租户验证通过：默认公司只显示 A Agent，切换测试公司 B 后只显示 B Agent；临时数据随后清理并切回默认公司。`390x844` 下 document width/scrollWidth 均为 390，无页面级横向溢出。

### 并行分支、合并顺序与回滚

- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`。migration 50 创建时本分支最高 49，AI 分支最高 33，无版本冲突。
- 当前 AI 分支未修改 `ai_agent_service.go` 和 AIAgent CRUD；高风险同文件是 `models.go`、`reply_trigger_service.go`、`miniprogram_chat_service.go`、会话 builder、转人工/企微运行时及测试。先合并 AIAgent Tenant 字段、migration 和 repository/service 原语，再逐方法合并：reply trigger 保留 AI 分支 route-aware 选择与本批 Tenant 查询，mini-program 保留 AI 分支人工状态判断与本批 Channel 必填边界；AIConfig 继续保留平台语义，禁止整文件覆盖。
- 本批没有改变 AI 分支负责的模型调用、计费和 usage 字段。需要 rebase 时重点检查 AI 分支是否新增 AIAgent 字段或 runtime 调用点，并给所有新 Agent 查询补已知 Tenant。
- 回滚边界为本批 tenant-aware 代码和入口校验；已回填 TenantID 不回写 0。保留字段与数据可以向后兼容，恢复全局 Agent 或删除 Tenant 字段会重新产生跨公司绑定风险。

## 第 34 批：租户公司接入设置与 Channel 密钥权限收口（2026-07-14）

### 目标与完成内容

- 保持 `/dashboard/channels` 为平台“接入公司”，将原占位 `/dashboard/settings` 改为当前公司 Channel 管理，复用现有 Channel API、类型和 `channel.*` 权限。
- 在“服务能力”增加租户上下文“接入设置”导航。平台账号未选择公司时不可见且 URL 守卫生效；租户账号按 `channel.view` 显示。
- 页面支持 Web、微信公众号和企微员工号协议渠道的筛选、创建、编辑、启停和删除。编辑弹窗加载当前租户已启用 AIAgent，Web/公众号展示直接访问链接和 JWT Secret，企微协议展示当前真实运行配置。
- 历史 `wxwork_kf`、`wxwork_cli` 只在已有记录中标记为历史，不恢复旧编辑表单、旧 bridge 或旧协议字段。
- 修复原 Channel 列表/详情泄露 `configJson` 的权限缺口：列表始终脱敏，详情从 `channel.view` 收紧为现有 `channel.update`。没有新增权限点，权限管理仍是唯一授权来源。

### 文件与契约

```text
internal/handlers/dashboard/channel_handler.go
internal/handlers/dashboard/channel_handler_test.go
internal/handlers/dashboard/authz_handler_test.go
web/app/dashboard/settings/page.tsx
web/app/dashboard/settings/_components/channel-edit.tsx
web/app/dashboard/settings/channel-settings-page.test.mjs
web/lib/navigation.tsx
web/lib/navigation.test.mjs
web/messages/zh-CN.json
web/messages/en-US.json
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 没有 model、migration、request/response DTO 字段、enum、Gin 路由或 WebSocket payload 变化。
- `GET /api/dashboard/channel/list` 和创建响应的 `configJson` 现在为空；`GET /api/dashboard/channel/{id}` 要求 `channel.update` 并继续返回编辑所需完整配置。创建、更新、启停、重置 Secret 和删除路径不变。
- 企微协议开发前已查 `wework.apifox.cn/llms.txt` 及回调、设置通知地址、发送文本接口；本批没有改变协议 API body、回调或 `conversation_id` 前缀规则。

### 权限、验证与风险

- 页面显隐：`channel.view`；新建：`channel.create`；当前渠道编辑/启停/读取敏感详情/重置 Secret：`channel.update`；删除：`channel.delete`。历史 KF/CLI 记录禁用编辑和启停；账号仍只绑定角色，角色仍在权限管理内绑定权限。
- 聚焦测试覆盖平台/租户页面职责、四权限显隐、租户导航、历史表单不恢复、列表敏感配置脱敏和详情更新权限。
- 浏览器验证桌面、`390x844` 列表和新建/编辑弹窗，无页面级横向溢出和控制台错误；移动表格保留自身滚动，不挤宽页面。
- 详情权限收紧可能影响绕过页面、仅持 `channel.view` 直接调用详情的历史客户端；仓库内当前唯一调用点是编辑弹窗，本批已按 `channel.update` 显示，因此正常 UI 无兼容损失。回滚该鉴权会重新暴露 App Secret，不建议回退。

验证命令：

```text
go test ./... -count=1
go test ./internal/handlers/dashboard -run 'Test(ChannelDetailRequiresUpdatePermission|ChannelListResponseRedactsSensitiveConfig)' -count=1
go vet ./...
cd web && node --test $(rg --files . | rg '\.test\.mjs$' | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/settings/page.tsx app/dashboard/settings/_components/channel-edit.tsx lib/navigation.tsx
git diff --check
```

- Go 全量与权限专项、`go vet`、71 项前端测试、typecheck、生产构建、目标 ESLint 和 diff 检查均通过。

### 并行合并与回滚

- `origin/codex/ai-billing@f2d2da4` 与本批重叠导航和双语资源，并修改 `admin.ts`；本批没有修改 `admin.ts`。合并时逐段保留 AI 分支 `replyIntentProfiles` 与本批 `channelSettings`，禁止整文件选边。
- 本批没有触碰 AIAgent 模型、AIConfig、FastGPT、回复 runtime、token、usage 或计费。无 migration 版本冲突，无数据回滚边界。
- 页面与导航可以独立回滚；Channel 列表脱敏和详情更新权限建议作为安全修复保留。若需要兼容只读详情，应新增不含密钥的只读 DTO，而不是恢复完整 `configJson` 暴露。

## 第 35 批：AI/Skill 运行日志租户隔离（2026-07-14）

### 目标与复用判断

- 审计发现 `/dashboard/agent-run-logs` 已按现有 `conversation.view` 向公司账号开放，但 `AgentRunLog`、`SkillRunLog` 及后台列表/详情仍是全局数据，存在跨公司回复诊断日志泄露。
- 复用现有日志表、Agent 运行日志页面、运行时写入服务和 Dashboard 指标，不新增页面、路由、DTO、enum、权限或 WebSocket payload。
- `conversation.view` 继续控制 Agent 运行日志，因为该页面用于诊断会话回复链路；本批没有建立与会话查看职责重复的新权限。

### 文件与契约变化

```text
internal/models/models.go
internal/migration/000051_backfill_ai_run_log_tenants.go
internal/migration/000051_backfill_ai_run_log_tenants_test.go
internal/repositories/agent_run_log_repository.go
internal/repositories/skill_run_log_repository.go
internal/services/agent_run_log_service.go
internal/services/skill_run_log_service.go
internal/services/dashboard_service.go
internal/services/ai_run_log_tenant_test.go
internal/handlers/dashboard/agent_run_log_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/ai/runtime/reply_runlog_service.go
internal/ai/runtime/reply_runlog_service_test.go
internal/ai/skills/runlog_service.go
internal/ai/skills/runlog_tenant_test.go
internal/ai/skills/log_test.go
internal/services/knowledge_tenant_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `AgentRunLog`、`SkillRunLog` 新增 `TenantID`；AutoMigrate 负责字段和索引，migration 51 从 Conversation、Message、AIAgent 及已有 Tenant 解析历史归属。
- migration 对缺失引用、跨租户证据和 Message/Conversation 不一致执行事务级失败回滚；无任何父记录证据的日志才归 `legacy-default`，重复执行幂等。
- Agent 日志创建要求正数 Tenant 和 Conversation，并校验可选 Message/AIAgent 同租户；回复 runtime 从 Conversation 写入 Tenant。Skill runtime 从 AIAgent 写入 Tenant，并在持久化前校验 Conversation/AIAgent 父记录。
- Agent 日志后台列表/详情使用 Active Tenant；首页 Skill 失败数直接按日志 Tenant 统计。JSON 字段、接口路径、筛选参数和响应结构均不变。

### 验证结果

```text
go test ./... -count=1
go test -race ./internal/migration ./internal/services ./internal/ai/runtime ./internal/ai/skills -run 'Test(BackfillAIRunLogTenants|AgentRunLogServiceEnforcesTenantReadsAndWrites|DashboardOverviewUsesActiveTenant|ReplyRunLogStoresRequestID|SkillRunLogWriteEnforcesTenantParents|BuildRunLog)' -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- Go 全量、专项 race、vet、71 项前端测试、typecheck、Next 生产构建和 diff 检查通过。
- 测试覆盖双租户列表/详情、运行时继承、跨租户写入拒绝、Message/Conversation 关系校验、首页指标隔离，以及 migration 的幂等、legacy 兜底和冲突回滚。
- `make generator` 已执行；生成器因既有 `TicketNoSequence` 注册产生了与手写并发序列 service 重名的无关未跟踪文件，该副产物已删除且未纳入提交，既有工单号实现未修改。

### 并行分支、合并顺序与回滚

- 开始和完成验证前均已 fetch。当前 `origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@3ea2678`；migration 51 与远端最高编号 20/33/50 不冲突。
- AI 分支与本批同文件修改为 `models.go` 和 `reply_runlog_service.go`。合并先保留两个日志 Tenant 字段、migration 51、repository/service/handler，再逐方法合并回复日志；AI 分支的 final action、资源、Graph 和 committed reply 逻辑必须与本批 Conversation Tenant 继承同时存在。
- 本批不改变模型调用、供应商、FastGPT、回复策略、token、usage 或计费口径。最终集成不要求本批先 rebase，但禁止对上述共享文件整文件选边。
- 可回滚 tenant-aware 查询和运行时校验代码，但已回填 TenantID 不回写 0。删除字段、撤销 Active Tenant 条件或恢复全局详情会重新开放跨公司日志访问，不属于安全回滚方案。

## 第 36 批：平台 Skill 定义写权限收口（2026-07-14）

### 目标与复用判断

- `SkillDefinition` 是平台共享定义，租户 AIAgent 只引用其 ID；现有 `skillDefinition.create/update/delete` 却是 tenant scope，并默认授予公司主管和客服组长，会让一个公司修改所有公司共用的技能模板。
- 复用现有 Skill 页面、模型、接口和四项权限，不新增租户 Skill 模型或平行配置页。`view/debug` 保持租户可用，模板写操作收口平台账号。
- 本批不触碰 Skill runtime、工具执行、模型调用、供应商、token、usage 或计费语义。

### 文件与契约

```text
internal/pkg/constants/auth.go
internal/migration/000052_restrict_skill_definition_writes.go
internal/migration/000052_restrict_skill_definition_writes_test.go
internal/handlers/dashboard/skill_definition_handler.go
internal/handlers/dashboard/authz_handler_test.go
web/components/dashboard/crud/dashboard-crud-page.tsx
web/app/dashboard/skill-definition/page.tsx
web/app/dashboard/skill-definition/platform-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 三项 Skill 写权限改为 platform scope；超级管理员和管理员保留，公司主管、客服组长及其他 tenant scope 角色不再持有。权限记录没有删除，在权限管理中继续可见。
- migration 52 先复用 `ensurePermissions/ensureRoles/ensureRolePermissions` 同步内置数据，再删除所有非 platform 角色上的三项历史关系；自定义平台角色已有绑定不受影响，重复执行幂等。
- 创建、更新、启停、删除和恢复 handler 增加平台账号校验，防止历史脏关系或旧 token 绕过 scope。列表、详情和 debug 路径不变。
- Skill 页面同时按平台账号和 create/update/delete 权限隐藏写操作。通用 CRUD 新增默认兼容的 `showCreate`，隐藏新增时刷新按钮仍保留。
- 没有 model、AutoMigrate、DTO、enum、Gin 路由、WebSocket payload 或 JSON 契约变化。

### 验证结果

```text
go test ./... -count=1
go test -race ./internal/migration ./internal/handlers/dashboard -run 'Test(RestrictSkillDefinitionWritesToPlatform|SkillDefinitionWritesRejectTenantAccountEvenWithPlatformPermission)' -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/skill-definition/page.tsx components/dashboard/crud/dashboard-crud-page.tsx
git diff --check
```

- Go 全量、专项 race、vet、73 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- migration 测试覆盖两类平台内置角色、两类租户内置角色、自定义平台/租户角色和幂等；handler 测试覆盖五个写入口的脏权限防线；前端测试覆盖平台身份与三项权限的组合显隐。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@f25811b`；migration 52 与远端最高编号 20/33/51 不冲突。
- AI 分支与本批预计文件没有同文件修改。本批不需要 rebase；最终合并如果 AI 分支新增 Skill 权限或页面动作，必须继续遵守“租户只读/调试、平台写模板”的边界。
- 页面和通用 `showCreate` 可独立回滚，但权限 scope、migration 清理和 handler 平台防线应作为同一安全边界保留。恢复租户写权限会重新产生跨公司共享模板修改风险。

## 第 37 批：平台 AIConfig 写操作显隐与防线（2026-07-14）

### 目标与复用判断

- AIConfig 是平台模型供应商配置，现有 create/update/delete 权限已经是 platform scope；租户角色持有 view 只是为了查看并选择可用模型。
- 审计发现租户只读页面仍展示新增、编辑、状态、排序和删除操作，五个写 handler 也没有防御异常携带平台权限的租户账号。
- 复用现有 AIConfig 页面、接口、权限和第 36 批 `showCreate`，不修改模型配置 service、请求结构或运行时。

### 文件与契约

```text
internal/handlers/dashboard/ai_config_handler.go
internal/handlers/dashboard/authz_handler_test.go
web/app/dashboard/ai-configs/page.tsx
web/app/dashboard/ai-configs/platform-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- create、update、update_status、update_sort、delete 在原动作权限后增加 `IsPlatformAccount` 校验；list、list_all 和 detail 保持只读开放。
- 页面按 `isPlatformAccount + aiConfig.create/update/delete` 控制新增、编辑、状态开关、拖拽排序、删除和操作列；只读列表、筛选和刷新不受影响。
- 权限常量、角色绑定、model、AutoMigrate、DML migration、DTO、enum、Gin 路由、WebSocket payload 和 JSON 契约均无变化。
- API Key 继续只返回 `hasApiKey` 脱敏状态；本批不改变供应商接入、模型调用、超时重试、token、usage 或计费。

### 验证结果

```text
go test ./... -count=1
go test -race ./internal/handlers/dashboard -run TestAIConfigWritesRejectTenantAccountEvenWithPlatformPermission -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/ai-configs/page.tsx
git diff --check
```

- Go 全量、专项 race、vet、74 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- 后端测试固定五个写入口的脏权限防线；前端测试固定平台身份、三项动作权限和所有写控件显隐。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@a4d975c`。AI 分支与本批预计文件无同文件修改，无 migration 版本影响，也不需要 rebase。
- 本批不改 AI/计费负责人维护的 AIConfig 字段、供应商、runtime、usage 或计费语义；最终合并无需重放模型配置代码。
- 前端显隐可独立回滚；handler 平台防线应保留。若未来实现租户模型配置，需另行迁移模型和计费契约，不能把当前全局配置直接开放写入。

## 第 38 批：租户角色平台权限清理与登录会话保护（2026-07-14）

### 目标与发现

- 权限审计发现 `session.view` 是 platform scope，却仍出现在 `cs_team_leader` 默认权限中；默认数据 migration 不调用 RoleService 的 scope 校验，因此客服组长实际获得了该权限。
- Session handler 只检查权限数组，会返回全平台 LoginSession，并补充全局 User 用户名，构成真实跨租户登录信息泄露。
- 本批保留 LoginSession 的平台审计定位，不增加租户 Session 平行模型或页面。

### 文件与契约

```text
internal/pkg/constants/auth.go
internal/pkg/constants/auth_test.go
internal/migration/000053_remove_tenant_role_platform_permissions.go
internal/migration/000053_remove_tenant_role_platform_permissions_test.go
internal/handlers/dashboard/session_handler.go
internal/handlers/dashboard/authz_handler_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 客服组长默认权限删除 `session.view`；permission 本身继续保留 platform scope 并在权限管理可见。
- migration 53 同步内置权限/角色后，统一删除所有 tenant scope 角色上的全部 platform permission 关系，覆盖内置角色、历史自定义角色和未来误配置数据；平台角色关系不删除，重复执行幂等。
- 常量单测固定内置 tenant role 不得包含 platform permission。Session list、revoke、revoke_by_user 额外要求 `IsPlatformAccount`，防御数据库脏关系和旧 token。
- 没有 model、AutoMigrate、DTO、enum、Gin 路由、WebSocket、页面、导航或 JSON 契约变化。

### 验证结果

```text
go test ./... -count=1
go test -race ./internal/pkg/constants ./internal/migration ./internal/handlers/dashboard -run 'Test(BuiltinTenantRolesDoNotReceivePlatformPermissions|RemoveTenantRolePlatformPermissions|SessionHandlersRejectTenantAccountEvenWithPlatformPermission)' -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- Go 全量、专项 race、vet、74 项前端回归、typecheck、Next 生产构建和 diff 检查通过。
- 测试覆盖客服组长历史关系、自定义 tenant/platform 角色、全平台权限交叉清理、迁移幂等、内置常量矩阵及三个 Session API 防线。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@2d86659`；migration 53 与远端最高编号 20/33/52 不冲突。AI 分支与本批预计文件没有同文件修改。
- ReplyIntentConfig 同样需要平台写显隐和 handler 脏权限防线，但 AI 分支同时重写其模型、DTO、service、handler、migration 和页面。本批不制造冲突；最终合并须把第 37 批 AIConfig 的平台写边界应用到合并后的 ReplyIntentConfig。
- migration 53、常量矩阵和 Session handler 防线应整体保留。回滚会重新暴露平台登录会话；若新增租户登录审计，应另建 tenant-qualified 查询契约。

## 第 39 批：角色与 MCP 平台防线、AI Agent 动作权限（2026-07-14）

### 目标与复用判断

- 审计剩余 platform permission 使用点后发现，角色写 handler 和 MCP 调试 handler 仍只校验权限数组，未防御租户旧 token 或异常权限数据。
- `/api/dashboard/mcp/catalog` 同时被租户 AI Agent 编辑器和平台 Skill 编辑器使用，它只返回工具元数据，却错误复用 platform scope 的 `mcp.view`；迁移 53 后租户编辑器必然收到 403。
- 复用现有角色、MCP、AI Agent 页面和权限体系：角色/MCP 调试增加平台身份防线，工具目录复用 `aiAgent.view`，AI Agent 页补齐既有动作权限显隐。不新增平行页面、权限点或业务模型。

### 文件与契约

```text
internal/handlers/dashboard/role_handler.go
internal/handlers/dashboard/mcp_handler.go
internal/handlers/dashboard/authz_handler_test.go
web/app/dashboard/roles/page.tsx
web/app/dashboard/roles/platform-permissions.test.mjs
web/app/dashboard/mcp/page.tsx
web/app/dashboard/mcp/platform-permissions.test.mjs
web/app/dashboard/ai-agents/page.tsx
web/app/dashboard/ai-agents/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 角色 create/update/delete/update_status/assign_permission/update_sort 统一要求对应 platform permission 和 `IsPlatformAccount`；list/list_all/detail 继续允许持有 `role.view` 的租户账号只读查看。
- MCP list_servers/test_connection/list_tools 要求 `mcp.view + IsPlatformAccount`，call_tool 要求 `mcp.call + IsPlatformAccount`。catalog 改用 `aiAgent.view`，继续只返回工具 code、名称、来源和 schema，不暴露服务器 endpoint/header。
- 角色页按平台身份和动作权限控制新增、拖拽、分配权限。MCP 页按平台身份和 `mcp.call` 隐藏真实调用编辑器。AI Agent 页按 create/update/delete 控制新增、编辑、状态、排序、删除和操作列。
- 没有 model、AutoMigrate、DML migration、request/response DTO、enum、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 结构变化。

### 验证结果

```text
go test ./... -count=1
go test -race ./internal/handlers/dashboard -run 'Test(RoleWritesRejectTenantAccountEvenWithPlatformPermission|MCPDebugHandlersRejectTenantAccountEvenWithPlatformPermission|MCPCatalogUsesAIAgentViewInsteadOfPlatformDebugPermission)' -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/roles/page.tsx app/dashboard/mcp/page.tsx app/dashboard/ai-agents/page.tsx
git diff --check
```

- 全量 Go、专项 race、vet、77 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- 浏览器以当前超管账号验证角色页仍显示新增/拖拽/分配权限，AI Agent 页仍显示管理动作，MCP 页面正常加载且控制台无 error/warning。测试环境没有 MCP Server，真实调用显隐由源码契约测试固定。
- 仓库没有安装可直接执行的 `prettier` 命令；未把该非项目脚本当作失败。目标 ESLint、typecheck 和 build 均通过。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@639b0a2`。AI 分支仅在检查范围内修改 `web/lib/navigation.tsx`，本批未修改该文件；预计文件无同文件冲突，无 migration 编号影响，不需要 rebase。
- 本批不修改 AI 分支维护的 ReplyIntentConfig、模型供应商、回复 runtime、FastGPT、token、usage 或计费。合并后仍须按第 38 批门槛收口 ReplyIntentConfig 平台写权限。
- 页面显隐可以独立回滚；Role/MCP handler 平台防线应保留。回滚 catalog 的 `aiAgent.view` 会再次破坏租户 AI Agent 工具选择，除非同步提供新的可见租户权限和完整角色迁移。

## 第 40 批：快捷回复与客户档案动作权限显隐（2026-07-14）

### 目标与复用判断

- 快捷回复、客户档案后端已经按现有 create/update/delete 权限和 Active Tenant 正确鉴权，不需要新增权限、接口或数据模型。
- 两个前端页面仍使用 `DashboardCrudPage` 默认写动作，导致只持有 view 的客服看到新增、编辑、启停和删除按钮；客户页同时有合法的只读详情动作，不能简单隐藏整个操作列。
- 复用现有 `showCreate/showEdit/showActionsColumn`、条件 `deleteItem` 和 row action 机制，只修前端表达，不增加平行页面或角色硬编码。

### 文件与契约

```text
web/app/dashboard/quick-replies/page.tsx
web/app/dashboard/quick-replies/action-permissions.test.mjs
web/app/dashboard/customers/page.tsx
web/app/dashboard/customers/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 快捷回复：`quickReply.create` 显示新增，`quickReply.update` 显示编辑与启停，`quickReply.delete` 显示删除；没有 update/delete 时不渲染操作列。
- 客户档案：`customer.create` 显示新增，`customer.update` 显示编辑与启停，`customer.delete` 显示删除；`customer.view` 下的详情、门店关系、会话跳转继续可用，所以操作列始终保留详情入口。
- 没有 Go、model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或响应结构变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/quick-replies/page.tsx app/dashboard/customers/page.tsx
git diff --check
```

- 全量 Go、vet、79 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- 两项新契约测试覆盖 create/update/delete 显隐；客户测试额外固定详情动作保留和状态动作只在 update 权限下出现。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/codex/ai-billing@f2d2da4` 与本批预计文件无同文件修改，无 migration 编号影响，不需要 rebase。
- 客户企业页 `web/app/dashboard/companies/page.tsx` 与 AI 分支同文件，本批主动避开；后续合并后再处理其 CRUD 与 AI 模型设置的双重权限显隐。
- 本批只改页面表达和测试，可独立回滚且无需数据回滚。回滚不会绕过后端鉴权，但会重新让只读账号看到必然失败的写按钮。

## 第 41 批：排班日历与标签树动作权限显隐（2026-07-14）

### 目标与复用判断

- 排班和标签后端已按现有 create/update/delete/batch 权限鉴权并完成 Tenant 隔离，不需要新权限或接口。
- 两页存在标准按钮之外的隐式写入口：排班空白日期点击、排班块点击/拖动/拉伸，以及标签状态 Switch 和 DnD 排序。只隐藏顶部按钮仍会让只读账号触发失败请求。
- 复用现有权限、页面和组件，通过能力 props 收紧交互，不新增平行工作台或角色硬编码。

### 文件与契约

```text
web/app/dashboard/agent-team-schedules/page.tsx
web/app/dashboard/agent-team-schedules/_components/calendar.tsx
web/app/dashboard/agent-team-schedules/action-permissions.test.mjs
web/app/dashboard/tags/page.tsx
web/app/dashboard/tags/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 排班 create 控制顶部/列表/日期格新建和 URL 自动打开；update 控制列表编辑、排班块编辑、移动和缩放；delete 控制删除；batchGenerate 控制批量编排。
- 日历组件显式接收 `canCreate/canUpdate`。无 create 的日期格去除按钮和键盘创建语义；无 update 的排班块去除按钮语义、拖动光标、pointer 写处理和缩放手柄。
- 标签 create 控制新增，update 控制编辑/启停/排序，delete 控制删除。无 update 时隐藏拖拽列和 Switch；操作列和空状态列数按权限动态收缩。
- 没有 Go、model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或响应结构变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/agent-team-schedules/page.tsx app/dashboard/agent-team-schedules/_components/calendar.tsx app/dashboard/tags/page.tsx
git diff --check
```

- 全量 Go、vet、82 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- 排班契约测试覆盖四项权限、列表动作和日历 create/update 交互；标签测试覆盖新增、编辑/状态/排序、删除、动态列和只读布局。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/codex/ai-billing@f2d2da4` 与本批预计文件无同文件修改，无 migration 编号影响，不需要 rebase。
- 本批不修改 AI 回复、模型供应商、计费、企微协议或派单状态机。页面和测试可独立回滚，无数据回滚；回滚不会绕过后端，但会恢复只读账号可触发的失败写交互。

## 第 42 批：知识候选审核动作权限显隐（2026-07-14）

### 目标与复用判断

- KnowledgeCandidate 后端列表已经使用 `knowledgeBase.view`，全部写动作已经统一使用 `knowledgeBase.update`，并叠加 Tenant/客服组范围；不需要新增候选专属权限。
- 页面此前无权限判断，持有 view 的只读角色仍可选择候选并看到编辑、质检、审核、导出和导入标记按钮，点击后统一得到 403。
- 复用现有知识库权限和当前页面，不增加平行审核页、角色判断或隐藏授权。

### 文件与契约

```text
web/app/dashboard/knowledge-candidates/page.tsx
web/app/dashboard/knowledge-candidates/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `knowledgeBase.update` 控制选择列、批量审核工具栏、质检、周导出、单条编辑/通过/驳回/标记导入和编辑弹窗；动作函数也增加相同守卫。
- `knowledgeBase.view` 继续提供候选内容、来源范围和会话跳转。会话入口不放入 `canManage`，避免只读审核人员失去核对上下文。
- 没有 Go、model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或响应结构变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/knowledge-candidates/page.tsx
git diff --check
```

- 全量 Go、vet、83 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- 新契约测试固定 update 权限、选择/审核显隐、编辑弹窗守卫和来源会话入口保留。

### 并行分支、合并与回滚

- 开始前已 fetch：`origin/codex/ai-billing@f2d2da4` 与本批预计文件无同文件修改，无 migration 编号影响，不需要 rebase。
- 本批不修改候选生成、AI 质检算法、知识导入、模型调用、token、usage 或计费。页面和测试可独立回滚，无数据回滚；回滚会恢复只读账号的失败写按钮。

## 第 43 批：工单职责权限与首次指派审计（2026-07-14）

### 目标与复用判断

- 工单已有 `create/update/assign/changeStatus/progress` 细粒度权限和专用指派接口，但普通 `UpdateTicketRequest` 仍带 `currentAssigneeId`，导致 `ticket.update` 可绕过 `ticket.assign`、指派原因、进展记录和通知事件。
- 手工创建、会话转工单也可在只有 `ticket.create` 时指定负责人；工单详情和会话菜单则未按现有动作权限显隐。
- 本批复用现有工单、客户关联、会话菜单和权限点，不新增工单/派单平行模型。即时会话派单与跨会话工单继续保持原职责分界。

### 文件与契约

```text
internal/handlers/dashboard/ticket_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/pkg/dto/request/ticket_request.go
internal/services/ticket_service.go
internal/services/ticket_service_test.go
internal/services/ticket_tag_tenant_test.go
web/lib/api/ticket.ts
web/app/dashboard/tickets/page.tsx
web/app/dashboard/tickets/_components/edit.tsx
web/app/dashboard/tickets/_components/ticket-detail-dialog.tsx
web/app/dashboard/tickets/_components/create-ticket-from-conversation-dialog.tsx
web/app/dashboard/tickets/action-permissions.test.mjs
web/components/customer-link-or-create-dialog.tsx
web/app/dashboard/conversations/page.tsx
web/app/dashboard/conversations/_components/conversation-info-panel.tsx
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `UpdateTicketRequest` 和前端 `UpdateTicketPayload` 删除 `currentAssigneeId`；`UpdateTicket` 不再校验或写负责人。旧请求的多余 JSON 字段被忽略，现有客户端不会因解析失败中断，但改派必须迁移到 `/ticket/assign`。
- 两个创建 handler 在非零初始负责人时额外要求 `ticket.assign`。服务层创建未指派主记录后，在同一事务内调用 `assignTicketTx`，记录初始指派进展；事务提交后同时发布创建和指派事件。
- 工单列表/详情按现有动作权限显示新建、编辑、指派、状态、进展和客户维护；Agent/标签辅助列表按其 view 权限按需加载。会话页按 `ticket.create/conversation.transfer/conversation.close` 收紧菜单和弹窗，客户关联弹窗按上下文关联权限叠加 `customer.view/create`。
- 没有 model、AutoMigrate、DML migration、enum、Gin 路由、WebSocket payload、权限常量、导航或统一响应变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/tickets/page.tsx app/dashboard/tickets/_components/ticket-detail-dialog.tsx app/dashboard/tickets/_components/edit.tsx app/dashboard/tickets/_components/create-ticket-from-conversation-dialog.tsx app/dashboard/conversations/page.tsx app/dashboard/conversations/_components/conversation-info-panel.tsx components/customer-link-or-create-dialog.tsx lib/api/ticket.ts
git diff --check
```

- 全量 Go、vet、87 项前端回归、typecheck、Next 生产构建和 diff 检查通过。
- 目标 ESLint 无 error，保留会话页原有 `@next/next/no-img-element` warning；本批未修改对应图片节点。
- 新测试覆盖创建时 `ticket.create + ticket.assign` 复合权限、普通编辑保留负责人、首次和再次指派进展、租户隔离，以及工单/会话/客户关联动作显隐。

### 并行分支、合并与回滚

- 开始前和提交前均已 fetch：`origin/codex/ai-billing@f2d2da4`。最终范围扩展后发现双方同改 `web/app/dashboard/conversations/_components/conversation-info-panel.tsx`；本批改前半段未关联客户权限，AI 分支改后半段自动转人工和公司意图字段，当前 `git merge-tree --write-tree HEAD origin/codex/ai-billing` 对该文件可自动合并。最终合并须保留双方区块并重新跑 typecheck/build；无 migration 冲突，不需要为本批单独 rebase。
- 本批不修改 AI 回复 runtime、模型供应商、token、usage、计费或 ReplyIntentConfig。DTO 变化仅限工单普通更新不再接受负责人；AI 内部系统建单仍可调用服务层创建并获得完整首次指派审计。
- 推荐整体合并后端、前端和测试。只回滚后端会恢复 `ticket.update` 静默改派漏洞；只回滚前端则旧创建表单会向无 `ticket.assign` 账号展示负责人并收到 403。
- 无表结构和存量数据回滚。新增的首次指派进展与通知属于正确审计事实，不应在代码回滚时删除。

## 第 44 批：派单与会话监控工作台动作权限（2026-07-14）

### 目标与复用判断

- 派单工作台后端已将列表/统计/客服负载放在 `conversation.view`，四个编排动作放在 `conversation.handover`；页面却只读取 `task.manageable`，导致只读账号仍看到全部编排按钮。
- 会话监控后端已有 `conversation.assign/transfer/close`，页面列表和详情却无权限判断；标签、客服、客服组筛选还无条件调用各自 view 接口。
- 第 43 批把客服列表辅助权限误写成不存在的 `agentProfile.view`。真实权限常量和 handler 均为 `agent.view`，本批立即更正，不新增别名或隐藏权限。

### 文件与契约

```text
web/app/dashboard/conversation-dispatch/page.tsx
web/app/dashboard/conversation-dispatch/action-permissions.test.mjs
web/app/dashboard/conversation-monitor/page.tsx
web/app/dashboard/conversation-monitor/_components/detail.tsx
web/app/dashboard/conversation-monitor/action-permissions.test.mjs
web/app/dashboard/tickets/page.tsx
web/app/dashboard/tickets/_components/ticket-detail-dialog.tsx
web/app/dashboard/tickets/action-permissions.test.mjs
web/app/dashboard/conversations/page.tsx
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 派单页新增 `conversation.handover` 能力守卫，动态移除操作列、修正空状态 `colSpan`，并保护自动派发/派发/转派/释放函数和弹窗。`agentTeam.view` 独立控制客服组筛选和接口加载。
- 会话监控页按 `conversation.assign/transfer/close` 拆分列表菜单、详情 footer 和弹窗；标记已读继续属于 view。`tag.view/agent.view/agentTeam.view` 控制三个辅助筛选及其数据请求。
- 工单与会话转工单的可选负责人统一改用真实 `agent.view`；需要同时具备业务动作权限和客服查看权限才显示人员选择器。
- 没有后端、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、权限常量、导航或响应结构变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/conversation-dispatch/page.tsx app/dashboard/conversation-monitor/page.tsx app/dashboard/conversation-monitor/_components/detail.tsx app/dashboard/tickets/page.tsx app/dashboard/tickets/_components/ticket-detail-dialog.tsx app/dashboard/conversations/page.tsx
git diff --check
```

- 全量 Go、vet、90 项前端回归、typecheck、Next 生产构建和 diff 检查通过。
- 目标 ESLint 无 error；保留原有 warning：监控详情 `messages` 依赖、监控页 `loadDetail` 依赖、会话页 `<img>`。本批没有修改这些逻辑节点。
- 新增契约测试固定派单 read/handover 分界、动态操作列和弹窗守卫，以及监控列表/详情各动作与辅助筛选权限。

### 并行分支、合并与回滚

- 开始本批前已 fetch。`origin/codex/ai-billing@f2d2da4` 不修改派单页、监控页、监控详情或工单页面；无 migration 影响，不需要 rebase。
- 本批不修改 AI 分支维护的聊天面板、自动转人工、回复 runtime、模型供应商、token、usage、计费或 ReplyIntentConfig。
- 页面与测试可独立回滚，无数据回滚；回滚会恢复误导性的主管写按钮及辅助接口 403。`agent.view` 更正不应单独回退，否则合法工单指派角色会失去人员选择入口。

## 第 45 批：平台存储与企微设备池只读边界（2026-07-14）

### 目标与复用判断

- `storageSetting.view/update` 与 `wxworkDevicePool.view/update/sync` 已是权限管理中可见的 platform scope 权限；两个 handler helper 还强制 `IsPlatformAccount`，后端边界完整。
- 前端两页此前没有读取 Session 权限：只读平台账号可编辑 OSS/设备池凭据并看到保存、同步按钮，点击后才收到 403。
- 本批只修页面表达和前端动作守卫，不新增权限、角色判断替代品、接口或配置模型。

### 文件与契约

```text
web/app/dashboard/storage-settings/page.tsx
web/app/dashboard/storage-settings/action-permissions.test.mjs
web/app/dashboard/wxwork-device-pool/page.tsx
web/app/dashboard/wxwork-device-pool/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 存储设置：`isPlatformAccount + storageSetting.update` 控制全部输入、Provider 下拉、私有 Bucket Switch 和保存按钮；保存函数增加同一守卫。
- 设备池：`isPlatformAccount + wxworkDevicePool.update` 控制后台地址/账号/密码与保存；`isPlatformAccount + wxworkDevicePool.sync` 独立控制同步实例；两个函数都增加能力守卫。
- 查看、刷新、配置状态、实例统计和实例列表不依赖写权限。没有后端、DTO、路由、权限常量、模型、迁移、导航或 JsonResult 变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/storage-settings/page.tsx app/dashboard/wxwork-device-pool/page.tsx
git diff --check
```

- 全量 Go、vet、92 项前端回归、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- Node 测试仅保留既有 typeless package warning；目标 ESLint 无 error/warning。
- 新增源码契约测试固定平台身份、存储 update、设备池 update/sync 和只读 disabled 表单。

### 并行分支、合并与回滚

- 开始前已 fetch。`origin/codex/ai-billing@f2d2da4` 不修改两个页面或测试，无 model/migration/共享 API 影响，不需要 rebase。
- 本批不修改设备同步协议、企微员工号运行时、AI 回复、模型、token、usage 或计费。
- 页面和测试可独立回滚，无数据回滚；后端平台防线必须保留。门店工作台仍是静态占位，知识库动作显隐待 AI 分支合并后处理。

## 第 46 批：通知只读流程与运行日志辅助筛选（2026-07-14）

### 目标与复用判断

- 通知中心已有 `notification.view/update`，但页面和全局 Provider 默认把打开通知与标记已读绑定，导致只有 view 的账号点击未读通知时因更新 403 而无法导航。
- Agent 运行日志主体属于会话审计视图，页面却无条件加载 AI Agent 列表；只有 `conversation.view` 而没有 `aiAgent.view` 的账号会因可选筛选接口 403 影响页面。
- 本批复用现有权限并拆清主页面与辅助能力，不新增通知跳转权限、运行日志专属 Agent 权限或角色硬编码。

### 文件与契约

```text
web/app/dashboard/notifications/page.tsx
web/app/dashboard/notifications/action-permissions.test.mjs
web/components/notification-provider.tsx
web/app/dashboard/agent-run-logs/page.tsx
web/app/dashboard/agent-run-logs/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 通知页面由 `notification.update` 控制单条/全部已读；通知业务跳转始终可用。Provider 在无 `notification.view` 时跳过未读 API 和 WebSocket，在无 update 时保留 Toast 跳转。
- 有 update 但标记已读失败时，Provider 仍在 `finally` 中执行跳转；列表页同样把导航从更新请求的异常路径中解耦。
- 运行日志只在 `aiAgent.view` 下请求并显示 AI Agent 筛选；主体日志列表与其他筛选保持可用。
- 没有后端、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket payload、权限常量、导航或统一响应变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/notifications/page.tsx components/notification-provider.tsx app/dashboard/agent-run-logs/page.tsx
git diff --check
```

- 全量 Go、vet、94 项前端回归、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- Node 测试仅保留既有 typeless package warning；目标 ESLint 无 error/warning。
- 新增源码契约测试固定通知 view/update 分界、更新失败不阻断跳转，以及运行日志 AI Agent 筛选的辅助权限。

### 并行分支、合并与回滚

- 开始前已 fetch。`origin/codex/ai-billing@f2d2da4` 不修改本批页面、Provider 或测试，无共享契约、同文件和 migration 冲突，不需要 rebase。
- 本批不修改 AI 回复 runtime、模型供应商、token、usage、计费或 ReplyIntentConfig。运行日志只改变可选筛选数据加载，不改变日志生成和查询语义。
- 页面、Provider 和测试可独立回滚，无数据回滚；回滚会恢复通知只读跳转失败和运行日志辅助接口 403。

## 第 47 批：运营总览显式权限（2026-07-14）

### 目标与复用判断

- `/api/dashboard/dashboard/overview` 原先只要求 ActiveTenant，侧边栏总览入口也没有 `requiredPermission`。这构成未进入权限管理的隐藏读取能力，与“所有权限可见、角色绑定权限、账号只绑定角色”的规则冲突。
- 总览是现有运营首页，不新增平行审计页面或第二套指标；本批只为它建立 `dashboard.view` 并复用现有角色、权限列表、导航过滤和 Tenant 上下文。
- 客服默认需要查看今日运营信息，因此内置客服角色获得该权限；门店员工不默认获得公司级汇总。

### 文件、权限与 migration

```text
internal/pkg/constants/auth.go
internal/handlers/dashboard/dashboard_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/migration/000054_sync_dashboard_overview_permission.go
internal/migration/000054_sync_dashboard_overview_permission_test.go
web/app/dashboard/_components/dashboard-home.tsx
web/app/dashboard/_components/dashboard-home-permission.test.mjs
web/lib/navigation.tsx
web/lib/navigation.test.mjs
web/lib/permission-i18n.ts
web/lib/permission-i18n.test.mjs
web/messages/zh-CN.json
web/messages/en-US.json
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `dashboard.view` 是 tenant scope、GET、`/api/dashboard/dashboard/overview` 的可分配权限。Handler 同时要求该权限和 ActiveTenant。
- migration 54 复用 `ensurePermissions/ensureRoles/ensureRolePermissions` 幂等同步。默认关系为超级管理员、管理员、公司主管、客服组长和客服有权，门店员工无权；不改自定义角色。
- 既有 `internal/pkg/constants/auth_test.go` 继续校验租户内置角色不能获得 platform scope 权限，本批定向和全量测试均包含该检查。
- 总览导航按权限隐藏。无权限访问 `/dashboard` 时，前端复用 `filterDashboardNavForSession` 找到第一个有权模块并替换路由，同时在跳转前不请求 overview。
- 没有 model、AutoMigrate、DTO、enum、业务 API 路径、指标查询、WebSocket、AI 回复、模型、token、usage 或计费变化。

### 验证结果

```text
go test ./... -count=1
go vet ./...
cd web && node --test $(find . -name '*.test.mjs' -not -path './node_modules/*' -print | sort)
cd web && pnpm typecheck
cd web && pnpm build
cd web && pnpm exec eslint app/dashboard/_components/dashboard-home.tsx lib/navigation.tsx lib/permission-i18n.ts
git diff --check
```

- 全量 Go、vet、96 项前端回归、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。
- Node 测试仅保留既有 typeless package warning；目标 ESLint 无 error/warning。
- 新测试固定无权限和无公司上下文均不可读、migration 可重复执行、门店员工不获得默认权限、总览入口按权限显隐及首页无权回退。

### 并行分支、合并与回滚

- 开始前已 fetch。`origin/main` 最高 migration 20，`origin/codex/ai-billing@f2d2da4` 最高 33，本分支此前最高 53，migration 54 无编号冲突。
- 同文件为 `web/lib/navigation.tsx`、`web/messages/zh-CN.json` 和 `web/messages/en-US.json`。AI 分支在导航后部增加意图行业入口并在 nav 文案区增加翻译；本批在导航首项增加 `dashboard.view` 并在 common 区增加无模块状态，本批区块和语义不重叠。提交后的 `git merge-tree --write-tree HEAD origin/codex/ai-billing` 显示双语资源可自动合并，但 `navigation.tsx` 因两条长期分支在同一数组累计的其他变化需要手工解决；不得整文件选边，必须保留本分支完整租户导航、`dashboard.view` 和 AI 分支 `replyIntentProfiles`，然后重跑导航测试与 build。
- 本批可独立回滚代码；数据库中已同步的额外权限和角色关系对旧代码无害，不使用破坏性 DML 回退。若未来取消权限，应通过新的幂等 migration 停用/解绑。

### 权限复扫与下一合并边界

- `rg` 复扫 dashboard handler 后，未再发现整个资源文件完全不含 `RequirePermission`、统一 permission helper 或 `HasPermission` 的后台入口；后续仍按函数和动作逐项核对，不能把文件级扫描当作最终安全证明。
- 当前明确待合并后处理的前端域为：`web/app/dashboard/companies`、`web/app/dashboard/knowledge`、`web/app/dashboard/reply-intent-configs`、`web/components/wxwork-protocol/wxwork-protocol-instance-manager.tsx` 及复用该 Manager 的公司详情。AI 分支正在修改这些页面/API/handler，禁止合并前整文件覆盖。
- 合并后的检查顺序：先确认最终后端动作权限，再处理主 CRUD 显隐和动作函数守卫，然后处理 Company/Channel/Knowledge/AIConfig 等辅助列表的按权限加载，最后跑双租户浏览器验收。门店工作台继续保持静态占位，不构造假运行链路。

## 第 48 批：Dashboard Handler 权限契约测试（2026-07-15）

### 目标与实现

- 人工 `rg` 文件级扫描只能证明某个文件出现过权限代码，不能证明同文件每个导出 handler 都鉴权。本批新增 Go AST 测试，逐函数建立本包调用图并递归追踪 `RequirePermission/HasPermission`。
- 直接校验和统一 helper 委托均被识别：AIConfig/Skill 平台写、工单进展和企微协议统一动作无需复制权限代码，也不会被误报。
- `UserPostChange_password` 是唯一 allowlist：它只能修改当前 principal 的密码，属于认证级自服务；测试同时要求该函数调用 `Authenticate`。管理员重置他人密码不在 allowlist，继续要求 `user.update`。

### 文件与契约

```text
internal/handlers/dashboard/permission_contract_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 没有生产代码、权限常量、默认角色、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket payload、前端或统一响应变化。
- 测试负责发现“新增 handler 完全没有权限契约”，不替代动作权限选择审计。知识库调试回答当前会调用模型并写检索日志但复用 `knowledgeDocument.view`；该计费/调试边界留给 AI 分支合并后的最终权限审计，不在本分支改变模型或用量语义。
- Skill 调试经代码和阶段 37 设计复核，继续允许持有 `skillDefinition.view` 的租户账号在本公司 Agent/Conversation/Checkpoint 范围内运行，不新增重复 call 权限。

### 验证结果

```text
go test ./internal/handlers/dashboard -run TestDashboardHandlersHaveExplicitPermissionContract -count=1
go test ./internal/handlers/dashboard -run 'Test(DashboardHandlersHaveExplicitPermissionContract|DashboardOverviewRequires|SkillDefinitionWritesReject|AIConfigWritesReject)' -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

- 全量 Go 和 vet 通过；权限契约测试成功追踪当前所有导出 dashboard handler。
- 没有前端文件变化，因此不重复声明新的前端构建证据；前端 96 项回归与生产构建仍以第 47 批结果为最近证据。

### 并行分支与回滚

- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 未修改新增测试或两份交接文档；无 migration、共享 DTO 或运行时同文件冲突。
- AI 分支合并后必须先运行本测试，再开展 knowledge/company/wxwork/reply-intent 页面动作显隐审计。若新增 handler 失败，先确认是缺失权限还是有意的认证级自服务，禁止为通过测试随意扩充 allowlist。
- 删除新增测试和本节文档即可回滚，不需要数据或配置回滚。

## 第 49 批：客服审计仿真全生命周期验证（2026-07-15）

### 目标与方法

- 旧仿真测试覆盖场景构造和 TenantID 继承，但没有在同一测试中证明“空库 migration -> seed -> 重复 seed -> report -> cleanup”的完整闭环。
- 手工验证使用 `/tmp` 下独立 SQLite 和临时最小配置，不连接当前开发服务数据库。首次与重复 seed 后分别读取报告，cleanup 后再读取报告并直接检查会话子表。
- 随后新增自动化生命周期测试，使用 `t.TempDir()` 创建数据库，不依赖外部 MySQL、Qdrant、企微服务或当前配置文件。

### 文件与精确基线

```text
cmd/customer_audit_seed/lifecycle_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 核心资源：Company 1、Channel 1、Store 100、客服组长 3、客服 12、门店员工 100、AgentTeam 3、AgentProfile 12、StoreStaffBinding 100、WxWorkInstance 100、Customer/Contact/Identity 各 500、StoreCustomerRelation 801。
- 仿真资源：Conversation 36、Message 135、历史 Assignment 21、当前已派发 18、实际覆盖客服 12、需人工回复 27；AI/Pending/Active/Closed 为 6/9/18/3。
- 重复 seed 后整个可比较 report 必须完全相等，不仅检查三个布尔标志。

### Cleanup 证明

- cleanup 后 report 除 batch/marker 外全部为零。
- 直接检查 `t_conversation`、`t_conversation_route_state`、`t_conversation_participant`、`t_message`、`t_conversation_assignment`、`t_conversation_event_log` 均无残留，防止因报告子查询失去 RouteState 而漏报孤儿数据。
- 新鲜数据库最终仅剩 bootstrap admin；六个内置角色、`dashboard.view` 和 migration 54 继续存在，确认 cleanup 只删除带测试 marker 的业务数据。
- 手工临时 YAML、SQLite、WAL 和 SHM 均已删除；不提交 `.codex/audits/` 或 `docs/generated/` 产物。

### 验证结果

```text
go test ./cmd/customer_audit_seed -run TestFreshDatabaseSeedLifecycle -count=1 -v
go test -race ./cmd/customer_audit_seed -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

- 上述验证全部通过；普通生命周期测试约 1.2 秒，race 包测试通过。
- 没有生产 Seed、model、AutoMigrate、DML migration、DTO、enum、API、权限、路由、WebSocket、前端或 AI/计费语义变化。

### 并行分支与回滚

- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 不修改新增测试或两份交接文档；无共享文件和 migration 编号冲突。
- 测试可独立删除，不影响现有 Seed CLI。测试失败时应先区分 migration 初始化、Seed 幂等、报告口径或 cleanup 孤儿，不允许为了恢复绿色而放宽精确基线。

## 第 50 批：会话详情辅助资源与动作权限收口（2026-07-15）

### 目标与原功能判断

- 会话详情原本同时承担会话状态、客户档案、客户企业、关联工单和会话标签展示；这些是同一侧栏中的关联信息，不应拆出平行页面，但也不能用 `conversation.view` 代替各资源已有权限。
- 审计确认后端已经分别使用 `customer.view/update`、`company.update`、`ticket.view`、`tag.view` 和 `conversation.tag`，缺口只在前端无条件加载辅助 API、无条件展示编辑控件。本批复用既有权限，不新增角色特判、隐藏权限或重复权限。

### 文件与行为

```text
web/app/dashboard/conversations/_components/conversation-info-panel.tsx
web/app/dashboard/conversations/conversation-info-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `customer.view` 控制客户和联系人加载；`customer.update` 与 `company.update` 分别控制客户、客户企业编辑，且客户档案不可见时两个动作都不出现。
- `ticket.view` 控制关联工单加载；`tag.view` 控制标签树加载；`tag.view + conversation.tag` 共同控制标签编辑器。
- 会话 DTO 内已有标签继续以名称只读展示，不因无法读取标签树而消失。基础会话状态、来源员工号和门店信息也不依赖客户档案权限。
- 契约测试固定六项权限映射、辅助读取短路、写弹窗显隐和现有标签只读保留。

### 验证、风险与合并顺序

```text
node --test app/dashboard/conversations/conversation-info-permissions.test.mjs app/dashboard/tickets/action-permissions.test.mjs
rg --files -g '*.test.mjs' | sort | xargs node --test
pnpm typecheck
pnpm exec eslint app/dashboard/conversations/_components/conversation-info-panel.tsx app/dashboard/conversations/conversation-info-permissions.test.mjs
pnpm build
go vet ./...
go test ./... -count=1
git diff --check
```

- 定向 7 项和全前端 99 项测试、TypeScript、目标 ESLint、Next 生产构建、vet、最终全仓 Go 单次测试及 diff 检查通过。
- 首次并行验证中的全仓 Go 测试曾在 `internal/services` 失败，单包与最终全仓复跑通过；额外 `go test ./internal/services -count=3` 暴露该包在同进程重复运行时复用全局 DB/配置的既有隔离问题，第二轮起会批量报表缺失/表缺失。该问题不由本批前端产生，且涉及共享测试基础设施和回复异步状态，留作独立协同任务，不能把 `-count=3` 记录成通过。
- AI 分支也修改 `conversation-info-panel.tsx`，加入自动转人工开关和 `intentProfileId` 保存兼容。合并顺序建议先保留本批 `ConversationInfoPermissions`，再重放 AI 开关；开关必须使用后端已经要求的 `conversation.handover` 做前端显隐和动作守卫，同时保留公司更新的 `intentProfileId`，禁止整文件二选一覆盖。
- 本批无 migration、共享 DTO、enum、API、路由、WebSocket、模型调用、token、usage 或计费变化。页面和测试可独立回滚，回滚不需要数据处理，但会恢复只读账号的 403 和误导入口。

## 第 51 批：企微员工号账号入口与 Manager 动作权限（2026-07-15）

### 原链路与设计选择

- 会话页中的企微账号列用于按员工号筛选客户并反映来源门店，属于会话导航；账号新增、编辑、删除、重新登录和远程开户链接属于渠道配置。两者原来共用一组无权限按钮，不能通过删除整个账号列解决，否则会破坏用户已确认的“全部账号/具体企微账号”工作流。
- 本批保留会话主入口，把账号读取、开户和管理拆回后端已有 `channel.view/create/update/delete`；复用 Manager 同时自带权限，避免企微独立页、公司详情和会话抽屉各写一套不一致判断。

### 文件与行为

```text
web/app/dashboard/conversations/page.tsx
web/components/wxwork-protocol/wxwork-protocol-instance-manager.tsx
web/app/dashboard/company-detail/page.tsx
web/app/dashboard/conversations/wxwork-account-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 会话页无 `channel.view` 时不调用实例列表，清空旧实例和筛选 ID，只保留“全部账号”及会话列表；统计改用当前会话，避免显示为 0。具备查看权限时原员工号搜索、门店来源和当前来源亮显保持不变。
- 现场扫码、占用清理、远程开户链接统一要求 `channel.view + channel.create`；扫码成功后的资料同步仅在另有 `channel.update` 时执行。账号管理抽屉由 `channel.update || channel.delete` 控制。
- Manager 按 create/update/delete 控制 CRUD，按 update 控制更换登录；按 `knowledgeBase.view/company.view` 条件加载辅助选项，按 `aiConfig.view/update` 控制模型读取与保存。所有写函数补同权限守卫。
- 客户企业详情按 `channel.view` 隐藏整个企微账号区域，按 `channel.view + channel.create` 显示公司专属开户链接；模型保存回调要求 `aiConfig.update`。

### 验证、并行与回滚

```text
node --test app/dashboard/conversations/wxwork-account-permissions.test.mjs
rg --files -g '*.test.mjs' | sort | xargs node --test
pnpm typecheck
pnpm exec eslint app/dashboard/company-detail/page.tsx app/dashboard/conversations/page.tsx app/dashboard/conversations/wxwork-account-permissions.test.mjs components/wxwork-protocol/wxwork-protocol-instance-manager.tsx
pnpm build
go test ./... -count=1
go vet ./...
git diff --check
```

- 定向 4 项、全前端 103 项、TypeScript、生产构建、Go 全量、vet 和 diff 检查通过；目标 ESLint 无 error，仅报告会话页既有二维码 `<img>` warning。
- AI 分支同文件 Manager 新增欢迎语编辑、意图行业列表、替换账号远程链接和模型测试。最终合并必须把欢迎语 action/保存与替换链接函数置于 `canUpdateChannels`，把 `fetchReplyIntentProfiles` 置于 `canViewStoreModelSettings`，把模型测试按钮与函数置于 `canUpdateStoreModelSettings`，并保留 AI 分支所有字段和新替换流程。
- AI 分支不修改会话主页面、公司详情或本批新测试，但修改 Manager 和 `web/lib/api/admin.ts`；本批没有 API 变更，因此建议先保留 AI 最终字段/API，再重放本批 Manager 权限层。禁止整文件采用任一分支版本。
- 本批无 migration、共享 DTO、enum、路由、WebSocket、AI 回复、token、usage 或计费变化。整体回滚无需数据处理，但会重新暴露只读账号 403 和写入口误导。

## 第 52 批：客户企业档案动作权限收口（2026-07-15）

### 原功能判断与实现

- `/dashboard/companies` 是当前租户内的客户企业档案 CRUD；平台租户接入与切换继续由 `/dashboard/channels` 承担，两者不合并、不新增第三个公司入口。
- 后端已经分别要求 `company.view/create/update/delete`，公司模型接口已经分别要求 `aiConfig.view/update`。本批未改变接口语义，只修复前端无条件暴露新增、编辑、删除、启停和账号明细入口的问题。
- 新增、编辑、删除和启停分别使用 `company.create/update/delete`；所有包装函数再次校验相同权限。`channel.view` 控制“账号列表”，`aiConfig.view/update` 分离模型读取与保存，相关导航、读取、保存函数均有守卫。
- 只读用户保留列表查询、筛选和刷新；没有账号、模型、状态、编辑或删除动作时，操作列整体隐藏。

### 文件与契约

```text
web/app/dashboard/companies/page.tsx
web/app/dashboard/companies/action-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 新测试固定公司 CRUD、状态切换、账号明细、模型读取和模型保存的六类边界，并要求 UI 显隐和动作函数双重守卫。
- 没有后端、model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket payload、权限常量、默认角色、导航或 JsonResult 变化；不涉及 AI 回复、模型调用、token、usage 或计费。

### 验证结果与已知测试风险

```text
cd web && node --test app/dashboard/companies/action-permissions.test.mjs
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/companies/page.tsx app/dashboard/companies/action-permissions.test.mjs
cd web && pnpm build
go vet ./...
go test ./internal/services -count=1
go test ./... -count=1 -p 1
git diff --check
```

- 定向 3 项和全前端 106 项测试、TypeScript、目标 ESLint、生产构建、vet、service 单包及串行全仓 Go 通过。
- 标准 `go test ./... -count=1` 两次在 `internal/services` 失败并出现 `t_conversation_read_state` 被替换/缺表日志；随后 service 单包和全仓 `-p 1` 均通过。这与第 50 批记录的全局测试 DB/配置隔离问题一致，本批没有 Go 改动。后续应独立修复测试基建，不能把 `-p 1` 写成标准并行命令已经通过。

### AI 分支合并与回滚

- `origin/codex/ai-billing@f2d2da4` 修改同一 `companies/page.tsx`，新增 `ReplyIntentProfile` 加载、意图行业列/表单和 `intentProfileId` payload，必须手工合并，禁止整文件选择任一分支。
- 合并后的意图列表请求必须受 `aiConfig.view` 控制；无该权限时不显示依赖选项源的字段。编辑既有公司时必须保留原 `intentProfileId`，不能因选项未加载提交 `0`；新建且无意图访问时可使用默认 `0`。同时保留本批 `company.*`、`channel.view`、`aiConfig.*` 的 UI 与函数守卫。
- 建议先保留 AI 分支最终 Company 类型、字段和 payload，再重放本批权限层并扩充本测试；随后重跑公司权限测试、typecheck、生产构建和双租户浏览器验收。
- 本批可按页面、测试和两份文档整体回滚，无数据回滚；回滚会恢复只读账号的写入口、状态菜单和跨资源访问误导。

## 第 53 批：回复意图平台写权限收口（2026-07-15）

### 真实链路与设计判断

- 通过 `rg` 追踪 model、service、handler 和 `internal/ai/runtime/executor` 后确认，`ReplyIntentConfig` 仍参与当前意图匹配、IntentDetect 和 prompt trace，不是旧文档中的废弃配置。
- 后端 list/detail 使用 `aiConfig.view`，create/update/delete 使用 platform scope 的 `aiConfig.create/update/delete`。因此本批复用现有权限管理，不新增 `replyIntent.*` 平行权限，也不修改回复 runtime 或权限常量。
- 页面现在要求“平台账号 + 对应动作权限”才能新增、编辑/启停或删除；每个包装函数再次检查同一能力。只读列表、筛选和刷新保持可用，无写能力时隐藏空操作列。

### 文件与验证

```text
web/app/dashboard/reply-intent-configs/page.tsx
web/app/dashboard/reply-intent-configs/platform-permissions.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
cd web && node --test app/dashboard/reply-intent-configs/platform-permissions.test.mjs
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/reply-intent-configs/page.tsx app/dashboard/reply-intent-configs/platform-permissions.test.mjs
cd web && pnpm build
go vet ./...
go test ./... -count=1 -p 1
git diff --check
```

- 定向 1 项、全前端 107 项、TypeScript、目标 ESLint、生产构建、vet 和串行全仓 Go 通过。
- 没有 Go 生产代码、model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket、权限常量、默认角色、导航、AI 调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 修改同一页面，增加 `ReplyIntentProfile` 获取、行业筛选/展示/表单和 `intentProfileId` payload。最终合并必须手工保留 AI 分支字段和本批 `useAuth`、平台身份、三项动作权限及函数守卫。
- AI 分支新增的 `fetchReplyIntentProfiles` 仍应只在 `aiConfig.view` 页面内调用；它提供选项数据，不应改变 create/update/delete 的 platform scope。合并后扩充本测试以断言 profile 字段存在，再重跑 typecheck、build 和回复 runtime 测试。
- 本批可按页面、测试和两份文档整体回滚，无需数据库处理；回滚不会绕过后端，但会恢复误导的租户写入口。

## 第 54 批：知识库分层动作权限收口（2026-07-15）

### 原页面与实现选择

- `/dashboard/knowledge` 原本就是“知识库侧栏 + 文档/FAQ 内容 + 检索日志 + 调试”的统一工作区，继续复用该信息架构，不新增平行配置页或第二套知识模型。
- 左侧档案使用 `knowledgeBase.create/update/delete`；排序和整库索引重建属于 update。右侧文档使用 `knowledgeDocument.view/create/update/delete`，文档索引重建属于 update。FAQ 使用 `knowledgeFAQ.view/create/update/delete`，批量导入属于 create，FAQ 索引重建属于 update。
- 检索日志、调试搜索和调试回答继续遵循当前 handler 的 `knowledgeDocument.view`。本批不改变调试回答的模型调用或 usage 语义；最终计费分支合并后仍须复核调试调用是否需要独立 call 权限。
- 页面、弹窗、拖拽、菜单和异步函数都执行相同边界。仅有 `knowledgeBase.view` 的门店员工仍能看知识库名称和归属，但内容区显示无权状态，不再请求无权接口。

### 文件与验证

```text
web/app/dashboard/knowledge/page.tsx
web/app/dashboard/knowledge/_components/knowledge-base-list.tsx
web/app/dashboard/knowledge/_components/document-list.tsx
web/app/dashboard/knowledge/_components/faq-list.tsx
web/app/dashboard/knowledge/action-permissions.test.mjs
web/messages/zh-CN.json
web/messages/en-US.json
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
cd web && node --test app/dashboard/knowledge/action-permissions.test.mjs
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/knowledge/page.tsx app/dashboard/knowledge/action-permissions.test.mjs app/dashboard/knowledge/_components/knowledge-base-list.tsx app/dashboard/knowledge/_components/document-list.tsx app/dashboard/knowledge/_components/faq-list.tsx
cd web && pnpm build
go vet ./...
go test ./... -count=1 -p 1
git diff --check
```

- 定向 4 项、全前端 111 项、TypeScript、目标 ESLint、生产构建、vet 和串行全仓 Go 通过。
- 在当前登录态的 3000 开发服务打开知识库页，超级管理员可见知识库、检索日志、调试和新增入口；截图未见重叠，知识库相关 console error 为 0。验证后恢复原会话页，不保留额外测试标签。
- 没有 Go 生产代码、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、权限常量、默认角色、AI runtime、token、usage 或计费变化。

### AI 分支合并阻断项

- `origin/codex/ai-billing@f2d2da4` 修改同一 `knowledge/page.tsx` 和 `knowledge-base-edit.tsx`，并新增 `FastGPTFilePanel`、`KnowledgeResourcePanel`。合并必须保留本批权限变量和现有四层内容判断，不能整文件选边。
- 图片资源 handler 已分别使用：list=`knowledgeBase.view`、sync=`knowledgeBase.update`、delete=`knowledgeBase.delete`；面板读取员工号还要求 `channel.view`。最终面板必须按辅助权限跳过员工号请求，并按 update/delete 隐藏和守卫动作。
- FastGPT 文件 handler 当前把 provision、upload、collections、search_test、delete_collection 全部放在 `knowledgeBase.view` 下，其中初始化、上传、删除是明确写操作。这与全局权限派发制冲突，合并上线前必须先确定并实施 create/update/delete 权限映射，补 handler 权限测试和前端动作守卫；不能仅凭 view 开放，也不能在本分支猜测 AI 负责人最终数据语义。
- `knowledge-base-edit.tsx` 新增 `fetchReplyIntentProfiles` 和 `intentProfileId`。最终选项请求依赖 `aiConfig.view`；自定义角色拥有 `knowledgeBase.update` 但没有 `aiConfig.view` 时，编辑必须保留既有行业绑定，禁止空列表提交清零。
- AI 分支也修改双语资源；手工合并时保留本批 `knowledge.contentViewDenied`，并继续保留 AI 分支新增文案，禁止整文件选边。
- 本批可按知识库页面、三个业务组件、测试、双语文案和两份文档整体回滚，不需要数据处理；回滚会恢复只读菜单、拖拽写入口和门店员工内容接口 403。

## 第 55 批：客服组织页面动作权限收口（2026-07-15）

### 目标与原页面职责

- 继续复用 `/dashboard/agents`，不增加第二套客服组织入口。页面左侧管理综合客服组，右侧保留客服档案、小组编排和服务范围；配置页不承担派单工作台职责。
- 综合客服组负责客服资源池、门店员工号服务范围和管理归属；客服小组是综合组内的调度/排班单元，客服可重复加入多个小组。该小组逻辑、双列拖拽和与排班/派单的关联均为用户确认保留的产品能力。
- 本批只收口前端动作权限，没有改变客服组、客服小组、门店员工号反向绑定、排班或派单的数据语义。

### 文件与权限边界

```text
web/app/dashboard/agents/page.tsx
web/app/dashboard/agents/action-permissions.test.mjs
web/app/dashboard/agents/_components/edit.tsx
web/app/dashboard/agents/_components/team-sidebar.tsx
web/app/dashboard/agents/_components/team-edit.tsx
web/app/dashboard/agents/_components/squad-arrangement.tsx
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 客服档案 CRUD 分别使用 `agent.create/update/delete`，并同时约束按钮、操作列和实际请求函数。只读账号仍可查看任务负载、服务规则和最近状态。
- 新建档案要求 `agentTeam.view + user.view` 提供合法组织和账号选项；缺少任一辅助权限时不调用对应 list API。编辑既有档案时禁用无权选择器并保留当前 ID，不以空选项覆盖。
- 综合组 CRUD 使用 `agentTeam.create/update/delete`。`tenant_admin` 加入可管理角色集合，使公司主管可按租户职责建立综合组及设置组长；动作仍必须拥有显式权限，不存在角色名直接放行。
- 组编辑器只有在 `user.view` 下加载组长和门店员工账号；无权时保留既有组长/员工 ID，隐藏门店员工双列选择入口。
- 小组创建、编辑/成员拖拽、删除分别使用 `agentTeam.create/update/delete`。成员拖拽、批量加入、移除和请求函数全部由 update 控制。
- 小组排班快捷入口要求 `agentTeamSchedule.view + agentTeamSchedule.create`；排班页面自身继续执行已有动作权限校验。

### 验证结果

```text
cd web && node --test app/dashboard/agents/action-permissions.test.mjs
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/agents/page.tsx app/dashboard/agents/action-permissions.test.mjs app/dashboard/agents/_components/team-sidebar.tsx app/dashboard/agents/_components/team-edit.tsx app/dashboard/agents/_components/edit.tsx app/dashboard/agents/_components/squad-arrangement.tsx
cd web && pnpm build
go vet ./...
go test ./... -count=1 -p 1
git diff --check
```

- 定向 5 项、全前端 116 项、TypeScript、目标 ESLint、生产构建、vet、串行全仓 Go 和 diff 检查通过。目标 ESLint 只有 `agents/page.tsx` 原有 `<img>` 性能 warning，无 error。
- 当前超级管理员登录态下实机检查 3000 开发页：三个综合组、客服档案任务负载、组操作菜单、成员/小组/服务范围 Tab 均正常；小组编排 1280x720 双列没有横向溢出，控制台 error/warning 为 0。
- 本批没有 model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket payload、权限常量、默认角色、导航、AI 调用、token、usage 或计费变化。

### 并行分支、合并顺序与回滚

- `origin/codex/ai-billing@f2d2da4` 修改同一 `agents/page.tsx`、`edit.tsx`、`team-edit.tsx`、`team-sidebar.tsx` 和 `web/lib/api/admin.ts`，不能自动整文件选边。
- AI 分支当前树中不存在 `squad-arrangement.tsx`、`squad-edit.tsx`，相对本分支还会删除 `agent_team_squad` handler、两个 repository、service、service tests、派单小组测试和 `conversation_dispatch_workbench_service.go`。这些不是可接受的清理：客服小组及其派单联动已经通过产品确认和测试，合并前必须列为阻断项。
- 建议合并顺序：先以本分支租户、客服组织和派单契约为基线，再逐文件叠加 AI/计费分支对客服档案响应、页面和 API 类型的新增字段；最后重跑小组 service、派单小组、handler 权限、116 项前端测试、typecheck、生产构建和双租户浏览器验收。禁止通过接受 AI 分支删除来解决冲突。
- 本批无需 rebase 当前远端；两分支同文件修改明确要求最终手工合并。字段、状态和权限语义必须以最终 handler/service 和权限常量复核，不能只按 TypeScript 编译结果判断。
- 本批可按上述六个前端文件、测试和两份文档整体回滚，无数据库回滚；回滚只撤销权限显隐与辅助请求保护，不得连带删除既有客服小组、排班或派单能力。

## 第 56 批：账号管理动作权限与安全删除（2026-07-15）

### 原页面与权限判断

- `/dashboard/users` 已同时承载账号列表、门店员工客服组反向绑定、邀请注册和注册审核，继续复用该页面，不新建平行账号中心。
- 代码和后端确认账号只保存 `UserRole`，页面只调用 `assignUserRoles`；`RolePermission` 仍只在角色管理中配置。没有恢复账号级 `UserPermission` 或新增隐藏权限赋予。
- 后端 `CanViewUser/CanManageUser/CanAssignRole` 已执行活动租户、同级/上级、平台/租户 scope 和自操作限制；前端显隐只改善体验，不替代这些最终边界。
- 审计发现现有 `user.delete` 已在权限管理、公司主管默认角色、handler 和 service 中存在，但页面没有入口；同时创建、更新、重置、启停、归组、邀请重置和注册审核多处只靠按钮路径，函数缺少同权限二次守卫。

### 文件与实现

```text
internal/services/user_service.go
internal/services/user_delete_dependency_test.go
internal/services/agent_profile_service.go
internal/services/conversation_service.go
internal/services/conversation_dispatch_workbench_service.go
internal/services/conversation_dispatch_squad_test.go
web/app/dashboard/users/page.tsx
web/app/dashboard/users/action-permissions.test.mjs
web/app/dashboard/users/_components/create.tsx
web/app/dashboard/users/_components/invitation-dialog.tsx
web/app/dashboard/users/_components/registration-review.tsx
web/lib/api/admin.ts
web/messages/zh-CN.json
web/messages/en-US.json
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- `user.create` 控制账号创建，`user.update` 控制资料编辑、启停和重置密码，`user.delete` 控制软删除，`user.assignRole + role.view` 控制角色选择与分配。创建者没有角色分配能力时，组件和页面双层保证 `roleIds=[]`。
- 页面新增 `deleteUser` API 包装和破坏性确认；提示明确“禁用”用于临时停用，“删除”会从账号管理移除并使会话失效。菜单只对后端标记 `manageable` 的账号展示，不允许自删或越级删除。
- 门店员工组筛选仅在 `agentTeam.view` 下携带 `agentTeamId`；归组函数要求 `agentTeam.view + agentTeam.update + bindingId`，保留原双向同步 service。
- 邀请弹窗 `open` 使用 `tenantInvite.view`，重置函数再次检查 `tenantInvite.rotate`。注册审核抽屉根据 `tenantRegistration.review` 和批准所需的 `user.assignRole + role.view` 生成 `canSubmit`，权限变化会关闭抽屉并阻止提交。
- 创建、编辑、重置、角色分配和邀请弹窗的 `open` 都与动作权限同步；函数重复检查权限、目标 `manageable` 和当前加载状态。

### 删除生命周期保护

- 原 `DeleteUser` 只写 `status=disabled + deleted_at` 并注销会话。自动派单会过滤禁用 User，但派单工作台的手动目标校验主要依赖 `AgentProfile.status`，直接开放旧删除接口可能留下可手动派单的孤立客服。
- 删除现在在事务中重新读取目标账号并检查五类依赖：未关闭且仍指派给该账号的会话、综合客服组组长、客服小组组长、未删除客服档案、未删除门店员工绑定。任一存在都返回明确业务错误，账号状态与 `deleted_at` 保持不变。
- 依赖全部清理后才执行原软删除并在事务提交后注销登录会话。该策略不级联删除会话、客服档案、员工号或历史记录，也不改变数据模型，符合审计和回滚边界。
- “禁用”继续作为可恢复的临时停用，不级联删除客服档案；但所有人工分配/转派目标现在复用 `AgentProfileService.GetEnabledForAssignment`，同时要求 User 未删除且状态启用、AgentProfile 状态启用。
- 派单工作台负载列表保留禁用客服用于主管理解历史负载和配置，但 `available=false`；旧会话分配/转接接口、工作台指派/转派和 `CanServeConversation` 都拒绝禁用账号，自动派单原有 User 状态过滤保持不变。

### 验证

```text
cd web && node --test app/dashboard/users/action-permissions.test.mjs
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/users/page.tsx app/dashboard/users/action-permissions.test.mjs app/dashboard/users/_components/create.tsx app/dashboard/users/_components/invitation-dialog.tsx app/dashboard/users/_components/registration-review.tsx lib/api/admin.ts
cd web && pnpm build
go test ./internal/services -run 'Test(UserServiceDelete|ConversationDispatchManualAssignment|ConversationDispatchCandidates)' -count=1
go vet ./...
go test ./... -count=1 -p 1
git diff --check
```

- 定向前端 5 项、全前端 121 项、删除依赖 5 个子场景、无依赖成功路径和禁用账号人工派单/负载可用性路径、TypeScript、目标 ESLint、生产构建、vet、串行全仓 Go 和 diff 检查均通过。
- 超级管理员在 3000 开发页实机看到邀请注册、注册审核、门店员工客服组归属和账号操作；菜单按当前权限显示分配角色、重置密码、禁用、删除。1280x720 无横向溢出，控制台 error/warning 为 0，未点击确认删除或修改测试数据。
- 本批没有 model、AutoMigrate、DML migration、request/response DTO、enum、Gin 路由、WebSocket、权限常量、默认角色、导航、AI runtime、token、usage 或计费变化。

### 并行合并与回滚

- `origin/codex/ai-billing@f2d2da4` 修改 `users/page.tsx`、创建/角色组件、`web/lib/api/admin.ts`、`user_handler.go`、`user_service.go`、`agent_profile_service.go`、`conversation_service.go` 和 admin request/response DTO；并删除 `conversation_dispatch_workbench_service.go`、`conversation_dispatch_squad_test.go`，其当前树中也不存在本分支邀请弹窗和注册审核组件。最终必须手工合并。
- 建议以本分支租户身份、邀请审核、角色 authority、安全删除和客服小组派单为基线，再叠加 AI 分支账号字段。`DeleteUser` 的事务依赖检查、`GetEnabledForAssignment` 及三条人工派单调用、页面所有权限函数守卫、`deleteUser` API 和双语风险提示必须保留；AI 分支不得通过删除邀请/审核/派单文件解决冲突。
- 合并后重跑 `role_user_authority_test`、`user_delete_dependency_test`、tenant registration business tests、dashboard handler 权限契约、121 项前端测试、typecheck、生产构建和双租户账号页验收。
- 本批前后端应一起回滚：只回滚 service 会暴露危险删除，只回滚页面仍会留下外部接口生命周期风险。无 migration 或数据回滚；已经成功删除的账号不会因代码回滚自动恢复。

## 第 57 批：门店员工自助工作台与默认角色最小权限（2026-07-15）

### 目标与现有功能取舍

- `/dashboard/store-workbench` 原为静态设计占位。本批在原入口内接入真实数据，不新建平行门店配置页，不把企微员工号 Manager、客服组织或会话工作台复制进来。
- 工作台只服务当前登录门店员工：读取与保存均使用认证主体 `UserID + ActiveTenantID`，请求不接收任意 `userId`、`bindingId`、`storeId` 或 `instanceId`。
- 门店员工可维护与本店人工接待直接相关的托管模式、服务时间、门店通知群、@ 成员、人工超时和门店位置；公司、门店、综合组、企微连接、AI 开关及知识库归属只读。

### 文件、接口与权限

```text
internal/bootstrap/routes.go
internal/bootstrap/server.go
internal/builders/store_workbench_builder.go
internal/handlers/dashboard/store_workbench_handler.go
internal/handlers/dashboard/authz_handler_test.go
internal/migration/000055_sync_store_workbench_permissions.go
internal/migration/000055_sync_store_workbench_permissions_test.go
internal/migration/000056_restrict_store_staff_to_workbench.go
internal/migration/000056_restrict_store_staff_to_workbench_test.go
internal/pkg/constants/auth.go
internal/pkg/dto/request/store_workbench_request.go
internal/pkg/dto/response/store_workbench_response.go
internal/services/store_workbench_service.go
internal/services/store_workbench_service_test.go
web/app/dashboard/layout.tsx
web/app/dashboard/layout-permissions.test.mjs
web/app/dashboard/notifications/action-permissions.test.mjs
web/app/dashboard/store-workbench/page.tsx
web/app/dashboard/store-workbench/_components/store-room-picker.tsx
web/app/dashboard/store-workbench/action-permissions.test.mjs
web/components/nav-user.tsx
web/lib/api/store-workbench.ts
web/lib/navigation.tsx
web/lib/navigation.test.mjs
web/lib/permission-i18n.ts
web/lib/permission-i18n.test.mjs
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

- 新增 `storeWorkbench.view/update`，权限在全局权限管理可见。内置 `store_staff` 默认只获得这两项；自定义角色仍由平台管理员在角色管理中显式配置。
- 新增 `GET /api/dashboard/store-workbench/current` 与三个 POST：`update`、`room_list`、`room_member_list`。所有 handler 先校验显式权限和活动公司，再由 service 解析当前主体范围。
- migration 55 同步两个权限，migration 56 只删除内置门店员工角色上除这两项外的历史关系；重复执行幂等，自定义角色权限测试确认不受影响。
- 工作台群/成员选择直接复用已核对的 `wework.apifox.cn` 员工号协议接口；群会话统一保存 `R:` 前缀，`@全员` 保存 `0`，无手填 ID 兜底。
- Dashboard Layout 新增统一直链权限守卫，来源仍是导航权限配置；不新增角色 URL 白名单。门店员工手工进入会话等无权路径会返回工作台。账号菜单的通知项同步受 `notification.view` 控制。

### 验证

```text
gofmt -w internal/pkg/constants/auth.go internal/migration/000056_restrict_store_staff_to_workbench.go internal/migration/000056_restrict_store_staff_to_workbench_test.go
go test ./internal/migration -run 'Test(SyncStoreWorkbenchPermissions|RestrictStoreStaffRoleToWorkbench)$' -count=1
go test ./internal/services -run '^TestStoreWorkbench' -count=1
go test ./internal/handlers/dashboard -run '^TestStoreWorkbench' -count=1
go vet ./...
go test ./... -count=1 -p 1
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm exec eslint app/dashboard/layout.tsx app/dashboard/store-workbench/page.tsx app/dashboard/store-workbench/_components/store-room-picker.tsx components/nav-user.tsx lib/api/store-workbench.ts lib/navigation.tsx
cd web && pnpm build
git diff --check
```

- 聚焦迁移/service/handler、全仓 Go、vet、127 项前端测试、TypeScript、目标 ESLint、Next 生产构建和 diff 检查通过。
- 测试库 migration 55/56 各成功一次；数据库中内置 `store_staff` 最终仅有 `storeWorkbench.view/update`。
- 浏览器使用 `test_customer_audit_store_staff_001` 验证：侧栏只有门店工作台，账号菜单只有修改密码/退出，无权会话直链回到工作台；1280x720 与 390x844 无横向溢出、重叠或控制台错误。未点击保存、定位授权或协议刷新，不修改业务数据。

### 并行合并与回滚

- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/bootstrap/routes.go`、`internal/bootstrap/server.go`、`web/lib/navigation.tsx`；最终合并必须逐段保留双方路由注册和导航项。AI 分支新增 `replyIntentProfiles`，本批新增工作台权限与通用直链守卫，不能整文件选边。
- AI 分支最高 migration 33，本批 migration 55/56 无编号冲突；本批不修改 AI 回复、FastGPT、模型、token、usage 或计费口径。
- 建议先保留本分支租户/权限/客服组织基础，再叠加 AI 分支路由和导航，最后重跑 dashboard handler 权限契约、导航/布局测试、全前端测试和生产构建。
- 回滚必须把工作台接口、页面、权限常量和默认角色语义视为整体。已执行 migration 不得改号或改 remark；若撤销，应新增幂等迁移处理角色关系，不能删除迁移历史或恢复门店员工历史宽权限。

## 第 58 批：租户数据一致性只读审计命令（2026-07-15）

### 目标、现有链路与文件

- 完成设计阶段 4 的部署前一致性检查，不新增 Dashboard 页面、API、权限或平行业务模型。审计直接读取 `models.Models`、现有表和当前角色 scope 契约，不使用历史 generated 报告作为产品依据。
- 命令只连接现有数据库，不运行 `bootstrap.Init`、`InitMigrations`、`AutoMigrate` 或 DML migration。SQLite 连接前拒绝缺失文件和内存库并强制 `mode=ro`，所有数据库在只读事务中执行。

```text
cmd/tenant_integrity_audit/main.go
cmd/tenant_integrity_audit/main_test.go
internal/repositories/tenant_integrity_audit_repository.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
Makefile
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

### 数据策略与输出契约

- 51 个注册 TenantID 模型全部显式登记零值策略；反射覆盖测试保证未来新增模型不登记即失败。User、TicketView、Notification、Asset 允许平台态 0，注册日志、未绑定企微实例、脱离会话同步日志和中断检查点使用受限零值条件，其余要求正租户。
- 64 张必需表和 125 条父子关系覆盖客户、门店/企微、会话/消息、渠道映射/Outbox、派单、工单、客服组织、知识库和 AI 运行日志。检查缺表/租户列、非法或未知 TenantID、必填引用、孤儿引用和父子租户不一致。
- 角色权限检查覆盖非法 scope、租户账号持有平台角色、平台账号持有租户角色、租户角色持有平台权限。业务归属字段要求 tenant 一致，Operator/Author/Reviewer 等操作人字段只要求引用存在，允许平台管理员在活动租户内执行有权动作。
- JSON 报告包含时间、驱动、样本上限、模型/策略/表/关系计数和违规数组。成功退出 0，数据违规退出 1，配置/连接/执行错误退出 2；每项违规包含稳定 code、entity、总数、有限 sampleIds 和说明。

### 验证

```text
go test ./internal/services ./cmd/tenant_integrity_audit -run 'TenantIntegrity|ReadOnly|ExecuteDoesNot' -count=1
go run ./cmd/tenant_integrity_audit --config /tmp/agentdesk-tenant-stats.yaml --sample-limit 5 --pretty
go test ./... -count=1 -p 1
go vet ./...
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- 聚焦测试、全仓 Go、vet、127 项前端测试、TypeScript 和生产构建通过。
- 实际测试库报告为 `passed`：51 个注册租户模型、51 个策略、64/64 表、125/125 关系、0 违规；审计前后 SQLite 大小与修改时间不变。
- 命令测试在只有 `audit_marker` 表的文件上得到缺表违规后，确认没有创建任何业务表且标记数据不变；缺失 SQLite 路径也不会因连接而创建空库。
- 本批没有 model、AutoMigrate、DML migration、request/response DTO、enum、API、Gin 路由、WebSocket、权限、前端或 AI/计费契约变化；没有写入 `docs/generated/`。

### 并行分支、合并顺序与回滚

- `origin/codex/ai-billing@f2d2da4` 当前不修改新增代码或 Makefile，无同文件业务冲突；两分支都会依赖 `internal/models/models.go`。最终模型合并后必须先跑策略覆盖测试，再跑真实库审计，任何新增 TenantID 模型都要显式补策略和关系。
- 本批不需要 migration 版本排序，也不要求 AI 分支先后合并。建议在最终模型契约确定后运行：覆盖测试 -> 全仓迁移测试 -> 只读一致性审计 -> 双租户浏览器/API 验收。
- 可独立删除命令、repository、service、测试和 Makefile 入口回滚，无数据库回滚。回滚会失去部署前自动阻断能力，不应删除本次已经确认的数据规则记录；命令返回违规时必须另做可审查修复，禁止把本命令改成自动修复器。

## 第 59 批：Company、Store、AgentProfile 租户组合唯一（2026-07-15）

### 目标、关联链路与文件

- 只调整已在设计中登记的三个租户业务标识：Company.Name、Store.StoreCode、AgentProfile.AgentCode。公开渠道、协议设备、登录标识、法定注册号和工单号继续全局唯一。
- 原 Company/AgentProfile service 的重复查询仍是全局条件；`customer_audit_seed` 的门店和客服 upsert 也会按全局编码命中。三处必须与数据库索引同时修改，否则页面仍拒绝合法跨租户同值，或仿真工具会改写其他租户。

```text
internal/models/models.go
internal/bootstrap/migration.go
internal/bootstrap/tenant_unique_indexes.go
internal/bootstrap/tenant_unique_indexes_test.go
internal/repositories/company_repository.go
internal/services/company_service.go
internal/services/agent_profile_service.go
internal/services/company_channel_tenant_service_test.go
internal/services/agent_organization_tenant_service_test.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
cmd/customer_audit_seed/main.go
cmd/customer_audit_seed/simulation_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

### 实现与升级顺序

- 新索引为 `uk_company_tenant_name(tenant_id,name)`、`uk_store_tenant_code(tenant_id,store_code)`、`uk_agent_profile_tenant_code(tenant_id,agent_code)`；字段 priority 显式固定列顺序。
- `InitMigrations` 先运行现有 AutoMigrate。其成功后，兼容器分别读取 SQLite PRAGMA 或 MySQL information_schema，校验新索引和旧索引的唯一性/字段顺序，再删除三个已知旧索引；最后才进入 DML migration runner。
- 新库没有旧索引时只验证新索引；升级库重复执行时幂等。若同租户已有重复数据，新索引创建失败并保留原数据；若旧索引名称被人工改成其他字段，兼容器拒绝删除并停止启动。
- Company 和 AgentProfile 在 service 预检中使用 tenant 条件，数据库竞态错误继续映射为“公司名称已存在”或“客服工号已存在”。已软删除 Company 仍保留名称，和数据库非部分索引语义一致。
- 仿真 upsert 先查目标 Tenant；仅当前 batch 标记的 tenant 0 历史行进入修复分支。Store/AgentProfile 的其他正租户同值行保持不变。租户审计增加三项重复业务键违规码。

### 验证

```text
go test -race ./internal/bootstrap ./internal/services ./cmd/customer_audit_seed -run 'Test(TenantScopedUniqueIndexes|RetireLegacyGlobalUniqueIndexes|CompanyNameIsUniqueWithinTenant|AgentCodeIsUniqueWithinTenant|TenantIntegrityAuditReportsDuplicateTenantBusinessKeys|SeedUpsertsDoNotReuseOtherTenantBusinessCodes)' -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
go run ./cmd/tenant_integrity_audit --config /tmp/agentdesk-tenant-stats.yaml --sample-limit 5
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- 聚焦 race、受影响全包、全仓 Go、vet、127 项前端测试、TypeScript 和生产构建通过；原测试库只读审计仍为 51/51 模型、64/64 表、125/125 关系、0 违规。
- SQLite 旧库副本升级后仅保留三个新组合唯一索引，业务行数保持 1/100/12；跨租户同值成功、同租户冲突、重复启动和升级后审计均通过。
- MySQL 8.4 临时库从新库创建和模拟旧索引两条路径完成升级；最终索引均为唯一且列顺序正确，跨租户同名成功，同租户冲突返回 1062。未修改当前 Docker 运行数据库，临时数据库已删除。
- 无 DML migration 版本、DTO、enum、API、路由、WebSocket、权限、前端或 AI runtime 变化。

### 并行分支与回滚

- AI 分支修改同一 `models.go`、Company service 和测试基础，且其模型仍缺本分支多项 TenantID。合并必须以本分支租户模型/索引和 service 条件为基线，逐字段叠加 AI 的 `IntentProfileID` 等变化；合并后重跑两个数据库的索引验证和 Company 页面/API 双租户用例。
- 本批 DDL 由 AutoMigrate 和启动兼容器完成，不参与 migration 编号排序。兼容器必须位于 AutoMigrate 之后、DML runner 之前，调整顺序会造成新索引尚未建立就删除旧保护。
- 代码可以回滚，数据库索引不能在已有跨租户同值后恢复为全局唯一。安全回滚保留组合索引，仅暂时恢复旧业务限制；任何删除合法数据以重建旧索引的方案都必须另行审批，不能作为自动 rollback。

## 第 60 批：migration 命令退出码与配置入口（2026-07-15）

### 问题与实现

- MySQL 第 59 批首次演练时索引校验按预期失败，但 `cmd/migration` 只 `slog.Error` 后 return，Docker/CI 看到的退出码仍为 0。这会破坏“迁移失败必须阻断发布”的验收前提。
- `main` 现在调用可测试的 `run(os.Args[1:])`；任意错误统一记录并 `os.Exit(1)`，成功返回 0。`run` 使用 `flag.NewFlagSet` 接收 `-config`，分阶段包装 load config、init DB、access connection、run migrations 错误并关闭连接池。
- Makefile 的 migration target 使用已有 `CONFIG ?= config/config.yaml`，执行 `go run ./cmd/migration --config "$(CONFIG)"`，不再要求通过切换工作目录选择配置。

### 文件与验证

```text
cmd/migration/main.go
cmd/migration/main_test.go
Makefile
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test ./cmd/migration -count=1
go test ./... -count=1 -p 1
go vet ./...
cd web && rg --files -g '*.test.mjs' | sort | xargs node --test
cd web && pnpm typecheck
cd web && pnpm build
git diff --check
```

- 失败子进程使用缺少组合唯一索引且含两条同租户重名 Company 的 SQLite，确认退出非零并且数据仍为两条；成功子进程使用显式 config 完成全量迁移，确认退出 0 且存在成功 migration 记录。
- 没有 model、AutoMigrate 契约、DML migration 版本、DTO、enum、API、路由、WebSocket、权限、页面或 AI runtime 变化。
- `origin/codex/ai-billing@f2d2da4` 不修改 `cmd/migration` 或 Makefile，无同文件冲突、不需要 migration 编号协调。可独立回滚；回滚不会修改数据库，但会重新让失败命令返回 0，因此不建议回滚。

## 第 61 批：客户展示补充数据逐关系租户校验（2026-07-15）

### 目标与实现

- 继续运行时租户边界审计。实际 Dashboard 链路为 Customer handler 按活动租户取 Customer，再由 `LoadPresentationData` 聚合 Company、StoreCustomerRelation、Store 和 WxWorkProtocolInstance，builder 将门店与员工号名称写入客户响应。
- 原聚合对 Store 和 WxWorkProtocolInstance 只有 ID 条件，脏外键可把其他租户名称带入当前租户响应；Company 的集合级租户条件也不能证明混合批次中每个 Customer 与 Company 同租户。
- 新实现记录每个 Customer/Company/Store/WxWorkProtocolInstance ID 的预期 TenantID：关系必须先与所属 Customer 同租户，父记录还必须与关系的预期租户一致。矛盾证据标记为不可补充，避免未来跨租户批量调用时因租户集合条件形成交叉匹配。
- 本租户 StoreCustomerRelation 不因父引用错误而被读取链路删除或隐藏，响应保留关系本身但不补外租户名称；后续清理通过 `tenant_integrity_audit` 和单独 DML 修复完成。

### 文件与验证

```text
internal/services/customer_service.go
internal/services/customer_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestLoadCustomerPresentationData' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 正常 tenant 101 Company/Store/Instance/Customer/Relation 聚合继续完整返回。
- 跨租户用例以 tenant 101 Customer/Relation 指向 tenant 202 Company/Store/Instance，确认三个外租户对象均不进入 presentation maps，而本地关系保留。聚焦 race、services、全仓 Go、vet 和 diff 检查通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不包含本批 `LoadPresentationData`，相对共同基线也未修改 `internal/services/customer_service.go`，当前无同文件冲突。最终合并后仍须确认本批实现、回归测试和之前 Customer TenantID 约束全部保留。
- 不需要 migration 编号协调；当前不要求 rebase。可独立回滚四个文件中的本批段落与代码，但回滚会重新暴露脏跨租户补充数据。数据清理必须另开可审查批次，不能把读取路径改成自动修复器。

## 第 62 批：异步业务通知租户边界（2026-07-15）

### 问题与实现

- ConversationAssignedEvent、TicketAssignedEvent 和 TicketCreatedEvent 均在提交后异步消费。原 handler 按业务 ID 读取实体，却调用按 RecipientUserID 推导 TenantID 的通知接口；错误事件可把租户 A 的业务内容写给租户 B 账号。企微通知在处理人没有身份时使用全局默认接收人，也缺少目标租户过滤。
- NotificationService 新增 `CreateInTenant/CreateAndPushInTenant`，内部以业务 TenantID 调用 UserRepository.GetInTenant；原通用入口保留，避免破坏非业务通知。会话/工单事件和 `notifyAgentDeskHandoff` 均传入已加载实体的 TenantID。
- WxWorkNotifyService 新增 `SendTextToAssigneeOrDefaultInTenant`。指定处理人为外租户账号时直接停止；同租户处理人缺少企微身份时，fallback 仅从目标租户与 tenant 0 平台账号中选择启用账号。Channel 和处理人文案补充读取同步按实体 TenantID。
- 不给内部 event 增加 TenantID，避免同时维护两份可能矛盾的租户证据；handler 每次重新读取 Conversation/Ticket 并以其 TenantID 为准。

### 文件与验证

```text
internal/services/channel_service.go
internal/services/conversation_human_dispatch_service.go
internal/services/event_handlers/conversation_assigned_event_handler.go
internal/services/event_handlers/notification_event_handler.go
internal/services/event_handlers/notification_event_handler_test.go
internal/services/event_handlers/ticket_assigned_event_handler.go
internal/services/event_handlers/ticket_create_event_handler.go
internal/services/notification_service.go
internal/services/notification_service_test.go
internal/services/wxwork_notify_service.go
internal/services/wxwork_notify_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run 'Test(NotificationServiceCreateInTenantRejectsForeignRecipient|WxWorkNotifyTenantScopedRecipients)$' -count=1 -p 1
go test -race ./internal/services/event_handlers -run 'Test(TicketAssignedInAppNotification|ConversationAssignedInAppNotification|AssignmentInAppNotificationsRejectCrossTenantRecipients)$' -count=1 -p 1
go test ./internal/services ./internal/services/event_handlers -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 错配的 Ticket/Conversation 事件不再生成 Notification；同租户 CreateInTenant 正常写入，外租户接收人被拒绝。企微 tenant 101 fallback 得到 tenant 101 与平台接收人，不包含 tenant 202。
- 聚焦 race、两个受影响包、全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、event schema、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支、剩余阻断与回滚

- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/services/conversation_human_dispatch_service.go` 的门店群转人工文案，本批在同文件将总部站内通知切到 `CreateAndPushInTenant`。最终必须逐段保留两者；AI 分支新增 `handoffNoticeCustomerName` 时使用全局 `CustomerService.Get`，合并时必须改为 `GetByTenantID(conversation.CustomerID, conversation.TenantID)`。其余本批文件当前无同文件冲突，不要求 rebase 或 migration 排序。
- 本轮同时审计 `media_understanding_service.go` 与 `store_ai_model_setting_service.go`，确认仍缺模型调用前 Message/Conversation 同租户校验、tenant-scoped route 解析和企微语音 Channel 同租户读取。AI 分支正修改这些文件的 usage/计费与二次触发，本分支不得越界改动；最终合并必须联合修复并增加双租户非 HTTP 测试，这仍是公开注册上线阻断项。
- 本批可独立回滚通知 service、调用点、测试和文档，不涉及数据库回滚；回滚会重新允许错误异步事件跨租户通知，并恢复全局企微 fallback，因此不建议回滚。

## 第 63 批：知识候选证据消息租户校验（2026-07-15）

### 问题与实现

- `KnowledgeCandidateService.UpsertCandidate` 已通过 Conversation/Store/KnowledgeBase 的真实模型合并 TenantID，但 `MessageIDs` 是逗号文本，不受数据库外键保护，原逻辑也未读取 Message 验证。错误调用可把其他租户或同租户其他会话的消息 ID 写成当前候选证据。
- 新增 `validateCandidateMessageIDs`：空证据保持兼容；非空值拒绝零数/负数，按首次出现顺序去重，再以 `tenant_id + id IN (...)` 批量读取。数量不一致即 fail closed；有 ConversationID 时逐条要求 ConversationID 相同。
- 校验发生在候选相似键查询之前，因此错误证据既不会创建新候选，也不会增加现有候选 frequency 或合并 evidence。人工分析和 `ExtractFromResolvedConversation` 无需各写一套规则。

### 文件与验证

```text
internal/services/knowledge_candidate_service.go
internal/services/knowledge_tenant_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestKnowledgeRuntimeTenantIsolation$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- tenant 101 同一会话的重复 MessageID 去重后保存一次；tenant 202 MessageID 被拒绝；tenant 101 另一 Conversation 的 MessageID 也被拒绝。正常父实体租户校验、知识候选创建和知识运行时测试继续通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 相对共同基线不修改本批 service 或测试，当前无同文件冲突，不要求 rebase 或 migration 排序。
- 可独立回滚四个文件中的本批变更，无数据库回滚；回滚会恢复跨租户/跨会话文本证据风险。若审计历史 MessageIDs，应新增只读解析规则和独立 DML 修复，不得在候选读取或审核时静默改写。

## 第 64 批：动态业务引用只读审计（2026-07-15）

### 目标与实现

- 现有 125 条 `tenantIntegrityRelations` 只覆盖固定外键。Notification 的 `biz_type + biz_id` 和 KnowledgeCandidate 的文本 `message_ids` 不能直接登记为普通关系，第 62/63 批虽已收紧运行时写入，历史数据仍是审计盲区。
- `auditNotificationBusinessReferences` 对 conversation/ticket 两种已知 BizType 分别执行结构化 LEFT JOIN，父级缺失或 TenantID 不同均报告 `DYNAMIC_TENANT_RELATION_MISMATCH`，不把动态检查计入普通关系数量。
- `auditKnowledgeCandidateMessageEvidence` 通过 repository 读取必要列；service 严格解析逗号 ID，非法/零/负值整条候选失败。所有唯一 MessageID 排序后按 500 条批量读取，候选级检查 TenantID，并在有 ConversationID 时检查会话归属。
- 违规按候选而不是按每个 MessageID 计数；样本按候选 ID 升序并服从统一 SampleLimit。命令仍只读，不运行迁移，不回填或删除数据。

### 文件与验证

```text
internal/repositories/tenant_integrity_audit_repository.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestTenantIntegrityAudit(PassesCleanTwoTenantFixture|ReportsDynamicReferenceViolations)$' -count=1 -p 1
go run ./cmd/tenant_integrity_audit --config /tmp/agentdesk-tenant-stats.yaml --sample-limit 5
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 测试得到 Notification conversation/ticket 各 1 条违规；KnowledgeCandidate 跨租户、跨会话、非法文本共 3 条违规，合法数据不误报，SampleLimit=1 时每类只返回 1 个样本。
- 实际 SQLite 报告为 passed：51/51 TenantID 模型策略、64/64 必需表、125/125 普通关系、0 违规。数据库执行前后修改时间均为 `1784055363`，大小均为 `4878336` 字节。
- 全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化；没有生成 `docs/generated/` 报告。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 相对共同基线不修改本批审计文件，当前无同文件冲突，不要求 rebase 或 migration 排序。AI 模型最终合并新增 TenantID 实体后仍需先跑策略覆盖测试，再运行包括本批动态规则的真实库审计。
- 可独立回滚 repository/service/test 和文档，无数据库回滚；回滚会失去历史动态引用检测。审计返回违规时必须另开幂等 DML 修复批次，禁止把本命令改为自动修复或删除器。

## 第 65 批：公司主管角色分配边界复核（2026-07-15）

### 审计结论

- 复核直接派角色、创建账号附带角色、邀请注册审核附带角色、全局角色写操作四条在线链路，现有实现符合已确认方案，因此本批不修改运行代码。
- `CreateUser/AssignRoles/Review` 最终复用 `replaceUserRolesDB` 的角色状态、角色等级和 platform/tenant scope 校验；目标账号另受当前租户 scope 和账号等级约束。公司主管可以给本租户低级账号分配 `cs_team_leader/cs_user/store_staff` 等低级租户角色，不能分配 `tenant_admin`、平台角色或管理其他租户账号。
- 全局 Role 是平台模板。创建、更新、删除、状态、排序和角色权限调整除明确权限码外还要求平台账号；租户账号即使因脏数据意外持有平台写权限也会被 handler 拒绝。平台管理员同样不能管理同级或更高角色账号。
- 账号管理前端只提交角色，不存在直接用户权限分配；创建和注册审核只显示可分配启用角色，调整抽屉将不可分配角色禁用并标识，避免把目标账号已有高等级角色静默隐藏或误导为可编辑。

### 文件与验证

```text
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^(TestRoleAuthorityAssignmentMatrix|TestUserServiceAssignRolesEnforcesAuthority|TestTenantAdminCreatesAccountWithLowerRoleOnly|TestUserTenantScopeAndCrossTenantManagement|TestTenantRegistrationReviewRejectsPeerRoleAndCrossTenantTarget)$' -count=1 -p 1
go test -race ./internal/handlers/dashboard -run '^(TestRoleWritesRejectTenantAccountEvenWithPlatformPermission|TestUserCreateWithRolesRequiresAssignRolePermission)$' -count=1 -p 1
node --test web/app/dashboard/users/action-permissions.test.mjs web/app/dashboard/roles/platform-permissions.test.mjs
git diff --check
```

- 三组聚焦测试均通过。本批无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、运行前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不修改本批两份文档对应的角色运行代码，当前无同文件运行冲突，不要求 rebase 或 migration 排序。最终合并必须保留现有 role/user/registration 权限测试，不能把租户角色误改为租户内可编辑实体。
- 可独立回滚两份文档中的本批段落，无代码或数据库回滚。后续新增角色/权限时仍须先在权限管理形成显式权限点，由平台管理员配置角色权限；公司主管只负责给本公司账号赋角色。

## 第 66 批：客服小组自动派单成员租户收口（2026-07-15）

### 问题与实现

- 小组配置写入已验证综合客服组、负责人和成员档案同租户，完整性审计也覆盖 Squad/Member/Profile 关系；但自动派单的 `ActiveMemberProfileSet` 原先读取启用小组和成员时没有 TenantID 条件，历史或手工脏关系可能被实时派单接受。
- `ActiveMemberProfileSet` 新增 tenant 参数，并在启用小组、启用成员两次查询中都增加 `tenant_id = ?`。`filterProfilesByActiveSquads` 从 `pickDispatchCandidates` 的权威会话租户透传该值。
- 小组负责人档案读取同步在 SQL 中限制综合客服组 TenantID，并继续检查 TeamID/TenantID，避免先全局读取再单靠内存比较。
- 没有改变客服可加入多个小组、拖拽交互、排班状态机、负载排序、最大并发、整组排班 `squadId=0` 或 Assignment 的 squad 快照语义。

### 文件与验证

```text
internal/services/agent_team_squad_service.go
internal/services/conversation_dispatch_service.go
internal/services/conversation_dispatch_squad_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestConversationDispatchCandidates(UseWholeTeamSchedule|FilterScheduledSquad|RejectCrossTenantSquadMembership|DoNotBroadenEmptyScheduledSquad|DoNotUseDisabledScheduledSquad)$' -count=1 -p 1
go test -race ./internal/services -run '^TestAgentTeamSquad(MembershipAndValidation|DeleteRequiresScheduleCleanup)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 新测试伪造 tenant 202 成员关系指向 tenant 101 的有效小组与客服档案；候选池保持为空。全部聚焦与全量验证通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 相对共同基线不修改本批 service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。最终合并保留 tenant 参数及脏关系回归测试即可。
- 可独立回滚两个 service、测试和两份文档，无数据库回滚；回滚会重新允许错误 TenantID 的小组成员关系影响自动派单。历史脏数据仍应由只读完整性审计发现并另做幂等修复，运行时不得自动改写关系。

## 第 67 批：排班小组与综合客服组运行时一致性（2026-07-15）

### 问题与实现

- 现有 `validateScheduleSquadDB` 已覆盖单条/批量排班写入，要求 Squad 启用、TeamID 匹配且 Squad/Team TenantID 一致，因此没有新增第二套写规则。
- 自动派单读取历史排班时只得到 `teamID -> squadID`，第 66 批虽按租户过滤小组成员，仍未验证该小组属于对应综合客服组。同租户脏排班配合同租户脏成员关系可能把其他综合组小组当成当前组排班。
- `ActiveMemberProfileSet` 现在一次返回成员集合和 `squadID -> teamID`；`filterProfilesByActiveSquads` 要求小组 TeamID 与客服档案 TeamID 相同后才检查成员身份。
- 第一次实现曾在排班选择阶段跳过错误/停用小组，使原测试的原因从 `no_matched_profile` 变成 `no_active_schedule_team`。为避免无必要契约漂移，最终实现改在成员筛选阶段校验，原报告语义保持不变。

### 文件与验证

```text
internal/services/agent_team_squad_service.go
internal/services/conversation_dispatch_service.go
internal/services/conversation_dispatch_squad_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestConversationDispatchCandidates(UseWholeTeamSchedule|FilterScheduledSquad|RejectCrossTenantSquadMembership|RejectMismatchedScheduledSquad|DoNotBroadenEmptyScheduledSquad|DoNotUseDisabledScheduledSquad)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 新测试构造 tenant 101 综合组 A 排班引用综合组 B 小组，并把 A 客服以脏关系放入 B 小组；候选池为空且原因保持 `no_matched_profile`。聚焦 race、单包、独立串行全仓、vet 和 diff 检查通过。
- 验证期间一次同时启动 services 与全仓测试，因仓库既有全局 DB/config 测试夹具争用出现临时表缺失；独立串行全仓随即通过。本批不修改测试基础设施，也不把并发运行失败记成业务通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不修改本批 service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。最终合并须同时保留第 66 批 tenant 过滤与本批 TeamID 匹配。
- 可独立回滚三个运行/测试文件和两份文档，无数据库回滚；回滚会恢复同租户跨综合组脏排班影响自动派单候选的风险。完整性审计当前只能发现 TenantID 错配，TeamID 语义修复应另做显式审计/修复批次，不能在派单时自动改写历史排班。

## 第 68 批：客服小组组织语义只读审计（2026-07-15）

### 目标与实现

- 现有普通关系审计只比较 TenantID，不能发现同一公司内“启用小组成员属于另一个综合组”或“排班引用另一个综合组的小组”。第 66/67 批已 fail closed，本批补历史数据可见性。
- `auditAgentOrganizationSemantics` 使用结构化 JOIN 增加两项独立检查：启用 `AgentTeamSquadMember` 的 Profile.TeamID 必须等于 Squad.TeamID；非零 `AgentTeamSchedule.squad_id` 的 Schedule.TeamID 必须等于 Squad.TeamID。
- 成员只检查 `StatusOk`，避免客服移组后已删除历史关系误报；排班检查保留全部非零小组引用，因为小组本身不支持变更所属综合组。父级缺失和 TenantID 错配继续由原 125 条关系处理。
- 两项检查复用统一 Count/sampleLimit，不增加普通关系计数，不写库、不修复数据。

### 文件与验证

```text
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestTenantIntegrityAudit(PassesCleanTwoTenantFixture|ReportsAgentOrganizationSemanticViolations)$' -count=1 -p 1
go run ./cmd/tenant_integrity_audit --config /tmp/agentdesk-tenant-stats.yaml --sample-limit 5
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 构造的串组成员和串组排班各报告 1 条，样本 ID 精确；合法成员/排班和干净双租户 fixture 不误报。
- 实际 SQLite 报告 passed：51/51 模型策略、64/64 表、125/125 普通关系、0 违规。执行前后 mtime `1784055363`、大小 `4878336` 字节均不变。
- 全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化；没有新增 generated 报告。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不修改本批审计 service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。最终合并新增 AI/FastGPT 租户模型后仍需先过模型策略覆盖，再运行包括本批语义检查的实际库审计。
- 可独立回滚 service/test 和两份文档，无数据库回滚；回滚会失去同租户串组历史问题的发现能力。任何修复必须根据报告另做幂等、可审阅 DML，不得让只读命令自动改组织关系。

## 第 69 批：职责角色移除前业务依赖保护（2026-07-15）

### 问题与实现

- User 删除已有 `ensureDeleteDependenciesCleared`，但 `AssignRoles` 原先直接删除并重建 UserRole。公司主管可移除 `cs_user/cs_team_leader/store_staff`，同时留下客服档案、组长指派或门店员工绑定，页面归属和账号职责随即不一致。
- `replaceUserRolesDB` 调整为先校验所有目标 Role，再比较原角色与目标角色；只有确实移除某项职责角色时才检查其依赖，避免新建账号或从未持有该职责的账号执行无关查询。
- `cs_user` 保护未关闭当前会话和未删除 AgentProfile；`cs_team_leader` 保护未删除 AgentTeam.LeaderUserID；`store_staff` 保护未删除 StoreStaffBinding。错误信息要求先转派/关闭、删除档案、更换组长或解除绑定。
- 依赖检查发生在删除 UserRole 之前并位于原事务内，失败保持原角色。没有自动清理、没有新增入口或隐藏权限。

### 文件与验证

```text
internal/services/user_service.go
internal/services/role_user_authority_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^(TestUserServiceAssignRolesPreservesDutyRoleDependencies|TestUserServiceAssignRolesEnforcesAuthority|TestTenantAdminCreatesAccountWithLowerRoleOnly)$' -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 测试依次验证未完成会话、客服档案、综合组组长和门店员工绑定阻止职责角色移除；每次失败后三个原角色均保留，清理依赖后空角色集合可成功保存。
- 全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不修改本批 User service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。最终合并保留本批前置校验顺序，避免恢复“先删后验”或静默级联。
- 可独立回滚 service/test 和两份文档，无数据库回滚；回滚会恢复职责角色与组织/会话归属悬空风险。未来离职编排必须作为独立、可预览、可审计事务设计。

## 第 70 批：职责对象与账号角色语义审计（2026-07-15）

### 目标与实现

- 第 69 批只阻止新错误，不能发现历史或手工数据中职责对象与角色不一致。本批继续扩展只读 `auditAgentOrganizationSemantics`。
- AgentProfile、AgentTeam.LeaderUserID、StoreStaffBinding 分别通过 `NOT EXISTS(UserRole JOIN Role)` 检查启用的 `cs_user/cs_team_leader/store_staff`。业务对象仅排除 `StatusDeleted`，角色必须 `StatusOk`。
- AgentTeamSquad.LeaderUserID 通过同 TenantID、同 TeamID、未删除 AgentProfile 检查本组客服身份，不错误要求小组负责人拥有综合组长角色。
- 复用统一 Count、ID 排序样本、sampleLimit 和缺列缓存；不增加普通外键关系计数，不写库或自动修复。

### 文件与验证

```text
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestTenantIntegrityAudit(PassesCleanTwoTenantFixture|ReportsAgentOrganizationSemanticViolations|ReportsDutyRoleAndSquadLeaderViolations)$' -count=1 -p 1
go run ./cmd/tenant_integrity_audit --config /tmp/agentdesk-tenant-stats.yaml --sample-limit 5
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 四类职责违规各 1 条且样本精确，合法职责记录不误报；前一批成员/排班语义和干净双租户 fixture 继续通过。
- 实际 SQLite 报告 passed：51/51 模型策略、64/64 表、125/125 普通关系、0 违规；前后 mtime `1784055363`、大小 `4878336` 字节不变。
- 全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端、模型调用、token、usage 或计费变化；没有新增 generated 报告。

### 并行分支与回滚

- `origin/codex/ai-billing@f2d2da4` 不修改本批审计 service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。合并后仍需以最终 Role/UserRole 与新增 AI 模型运行完整审计。
- 可独立回滚 service/test 和两份文档，无数据库回滚；回滚会失去历史职责对象缺角色和小组负责人缺本组档案的发现能力。修复必须人工确认恢复角色还是解除职责，再另做幂等 DML。

## 第 71A 批：账号角色变更追加式审计契约（2026-07-15）

### 目标与实现

- UserRole 角色替换会删除旧关系，AuditFields 无法保存角色集合前后快照；注册、会话和登录日志语义均不匹配。本批新增独立 UserRoleChangeLog，不复用旧日志，也暂不增加查看页面或权限。
- 日志保存 TenantID/UserID、排序后的前后角色 ID/code JSON、OperatorID/OperatorName 和 CreatedAt。TenantID=0 只用于平台账号日志；租户日志必须与目标 User 同租户。OperatorID 可为 0，正数只要求 User 引用存在，不要求同租户，以保留平台管理员在活动公司上下文中的合法操作。
- model 注册进 models.Models，由 AutoMigrate 创建表；repository 仅暴露 Create，保持追加式写入边界。没有 DML migration、DTO、enum、API、Gin 路由、权限、WebSocket 或前端变化。
- TenantIntegrityAudit 增加显式策略及两条关系，覆盖基线更新为 52/52 TenantID 模型、65 张必需表、127 条普通关系。测试验证平台零租户、租户日志、跨租户目标账号和不存在操作人。

### 文件与验证

```text
internal/models/user_role_change_log.go
internal/models/models.go
internal/repositories/user_role_change_log_repository.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestTenantIntegrityAudit(PassesCleanTwoTenantFixture|ReportsTenantRelationAndRoleViolations)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 全部验证通过；没有生成或提交 docs/generated 报告，也没有修改 `.codex/audits/`。

### 并行分支、合并与回滚

- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/models/models.go`。最终不得整文件选边，必须保留双方新增模型并以合并后的 models.Models 重跑策略覆盖与 AutoMigrate。其余本批文件当前无同文件修改，无 migration 排序要求。
- 合并顺序建议先 71A 数据契约，再合并 71B 在线写入；只合并 71B 而缺少 model/AutoMigrate 会在首次写日志时失败。
- 71A 尚未写入业务日志时可整体回滚；71B 投产后即使回滚写入逻辑，也应保留日志表和历史数据，不得把业务回滚做成数据删除。

## 第 71B 批：账号角色变更在线写入（2026-07-15）

### 真实入口与事务语义

- 手动角色分配、后台账号创建、接入公司主管创建和邀请注册审核均复用 `UserService.replaceUserRolesDB`。企业微信登录首次为账号补 `store_staff` 是额外运行时入口，本批也写同一 UserRoleChangeLog；migration 初始化和仿真种子不记录为在线人工操作。
- UserRoleRepository 通过 UserRole LEFT JOIN Role 返回可检查错误的当前快照。前后 ID/code 分别排序并编码为 JSON；输入重复 ID 或顺序变化不构成角色集合变化。
- 角色集合无变化时不删除重建关系、不写日志。发生变化时，职责依赖校验、删除旧 UserRole、创建新 UserRole 和追加日志都使用调用方同一事务；任何一步失败或外层事务回滚都不保留部分角色或日志。
- 普通账号/主管创建记录空集合到初始角色；注册审核通过记录审核角色，拒绝空集合和幂等重放不重复记录；企微补角色使用登录账号自身作为操作人并保留账号已有其他角色。
- 运行时引用扫描未发现其他直接写入口。通用 UserRoleService 的写方法当前无调用者，RoleService 只使用读取；后续不得从 handler 或新 service 绕过 UserService 角色事务。

### 文件与验证

```text
internal/repositories/user_role_repository.go
internal/services/user_service.go
internal/services/wxwork_login_service.go
internal/services/auth_service_test.go
internal/services/role_user_authority_test.go
internal/services/tenant_management_service_test.go
internal/services/tenant_registration_business_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^(TestUserServiceAssignRolesEnforcesAuthority|TestTenantAdminCreatesAccountWithLowerRoleOnly|TestUserServiceAssignRolesPreservesDutyRoleDependencies|TestUserRoleChangeLogRollsBackWithRoleReplacementTransaction|TestWxWorkDefaultStoreStaffRoleWritesOneAuditLog|TestTenantServiceCreateTenantBuildsAtomicCompanyFoundation|TestTenantRegistrationReviewApprovesRoleAndRevokesOldSessions|TestTenantRegistrationReviewRejectsWithoutRoles|TestTenantRegistrationReviewEnforcesTenantAndRoleAuthority)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 定向 race、完整 service、全仓 Go、vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端或 AI/计费变化；没有修改 `.codex/audits/`，也没有生成 docs/generated 报告。

### 并行分支、合并与回滚

- 以共同基线比较，`origin/codex/ai-billing@f2d2da4` 在本批文件中只同时修改 `internal/services/wxwork_login_service.go`。最终需逐段保留 AI 分支已验证邮箱绑定逻辑和本批企微角色日志事务，禁止整文件选边；`user_service.go`、repository 和四个测试文件当前无 AI 同文件修改。
- 合并顺序固定为 71A model/AutoMigrate -> 71B 在线写入 -> 合并后的全量 Go、模型策略覆盖与实际库审计。没有 DML migration 版本冲突，也不要求修改 AI runtime。
- 71B 可回滚 service/repository/test 接线，但保留 UserRoleChangeLog 表和已有数据。回滚后新变更不再可审计，且相同集合会恢复删除重建，因此只适合紧急停写，不应作为长期兼容方案。

## 第 72 批：最终遗漏扫描与 AI 分支合并阻断清单（2026-07-15）

### 本分支结论

- 重新追踪接入公司、邀请注册、角色/权限、账号、客服组/小组、排班、派单、会话、通知、文件和只读审计的页面到 repository 链路，未发现新的 `codex/customer-audit` 在线越权写入口。71A/71B 已补齐角色集合历史，正式配置的公开注册继续默认关闭。
- 客服小组和派单文件是本分支在共同基线后的新增能力；AI 分支没有这些文件，但也没有显式删除差异。标准 Git 合并会保留，只有整目录/整页面采用 AI 版本才会人为丢失，集成时禁止这种做法。
- 当前 TenantIntegrity 基线为 52/52 TenantID 模型、65 张必需表、127 条普通关系；合并 AI 新模型后必须提高基线，不能保留旧断言或把新模型排除在 models.Models 外。

### AI 新增模型必须处理

```text
AIManualResumeTask              -> TenantID 继承 Conversation
FastGPTDatasetJob               -> TenantID 继承 KnowledgeBase/Store
KnowledgeResourceGroup          -> TenantID 继承 KnowledgeBase/WxWorkInstance
KnowledgeResourceItem           -> TenantID 继承 KnowledgeResourceGroup
WxWorkCustomerHandoffSetting    -> TenantID 继承 Customer/WxWorkInstance
AIUsageEvent                    -> TenantID 继承 Conversation/Message/KnowledgeBase
AIUsageGatewayCall              -> TenantID 继承 AIUsageEvent/Conversation
```

- ReplyIntentProfile 保持平台全局行业模板；EmailVerificationCode 保持全局认证挑战记录，因为当前邮箱全局唯一且该表不进入租户业务管理。两者仍需平台权限/认证用途测试，不能被租户普通写权限修改。
- 上述业务模型必须补 AutoMigrate 字段、确定性历史回填、repository/service 租户条件、完整性 policy/relations 和双租户 worker 测试。CompanyID、StoreID 或不可见 UI 都不是租户隔离替代品。

### 已确认运行时/权限阻断

- AI 分支 `media_understanding_service.go` 仍按全局 Message/Conversation/WxWorkInstance/Channel 读取。最终实现必须保留本分支 Message/Conversation 同租户检查、StoreAIModelSetting `ResolveForMessage` 租户路由和企微语音按租户取 Channel，再合并 usage 事件。
- `fastgpt_dataset_handler.go` 的 provision/upload/delete collection 当前均使用 `knowledgeBase.view`。目标映射固定为 provision=create、upload=update、delete=delete，collections/search test=view；前后端必须共用权限管理中的现有权限码。
- FastGPTDatasetService、KnowledgeResourceService 和 AI usage service 的全局 Store/KnowledgeBase/Instance/Asset/route 读取必须改为父 TenantID 条件；异步 job 必须持久化 TenantID，不能在 worker 中依赖 HTTP operator。
- `models.Models` 合并必须同时保留 Tenant/UserRoleChangeLog/AgentTeamSquad/TenantID 字段和 AI 新模型；不得恢复 UserPermission、全局 StoreCode/AgentCode/CompanyName 唯一索引或 AI 分支旧无 Tenant 结构。

### 只读合并预演

- 共同基线为 `e67e207`，双方共有 53 个修改文件。`git merge-tree --write-tree HEAD origin/codex/ai-billing` 报告 24 个文本冲突：

```text
internal/ai/rag/retrieve.go
internal/bootstrap/server_route_test.go
internal/builders/conversation_builder.go
internal/handlers/api/auth_handler.go
internal/models/models.go
internal/pkg/config/config.go
internal/pkg/config/config_test.go
internal/pkg/dto/response/auth_response.go
internal/repositories/conversation_route_state_repository.go
internal/repositories/knowledge_retrieve_log_repository.go
internal/services/asset_service.go
internal/services/company_service.go
internal/services/im_message_asset.go
internal/services/knowledge_base_service.go
internal/services/media_understanding_service.go
internal/services/wx_work_protocol_instance_company_test.go
internal/services/wx_work_protocol_instance_service.go
internal/services/wxwork_login_service.go
web/app/dashboard/knowledge/page.tsx
web/app/dashboard/reply-intent-configs/page.tsx
web/components/login-form.tsx
web/components/wxwork-protocol/wxwork-protocol-instance-manager.tsx
web/lib/api/auth.ts
web/lib/navigation.tsx
```

- AI migration 为 21-33，本分支为 34-56，当前不重号。合并顺序必须是 71A 契约 -> AI 新模型 Tenant 化/回填 -> 71B 与 AI 登录/usage 逐段合并 -> handler 权限 -> 页面导航 -> 全量验证。
- 最终验收至少执行：migration version/remark 测试、模型策略覆盖、全仓 Go/race/vet、前端契约/typecheck/build、双租户媒体/FastGPT/usage/客服派单测试，以及真实数据库只读 `tenant-integrity-audit`。全部通过前保持 `tenantRegistration.enabled=false`。

### 本批文件与验证

```text
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
git fetch origin
base=$(git merge-base HEAD origin/codex/ai-billing)
comm -12 <(git diff --name-only "$base"..HEAD | sort) <(git diff --name-only "$base"..origin/codex/ai-billing | sort)
git merge-tree --write-tree HEAD origin/codex/ai-billing
git diff --check
```

- 本批是只读遗漏与合并预演，不修改运行代码、模型、migration、API、权限或前端；merge-tree 只创建临时 Git tree 对象，不改变工作树。可独立回滚两份文档，但会失去当前精确阻断清单。

## 第 73 批：角色写入审计旁路契约（2026-07-15）

### 问题与实现

- 第 71B 批完成当前在线入口日志，但 UserRoleService 仍暴露无人调用的 Create/Update/Updates/UpdateColumn/Delete。它们无法执行角色等级、租户 scope、职责依赖、事务快照和会话撤销，因此不能作为兼容入口保留。
- UserRoleService 收敛为只读查询。在线角色替换只允许 `UserService.replaceUserRolesDB`；企业微信新账号的默认门店员工角色只允许 `wxWorkLoginService.assignDefaultStoreStaffRole`，两者均已在 71B 同事务写 UserRoleChangeLog。
- 新增源码 AST 契约，扫描所有 `internal/services/*.go` 非测试文件，识别 repository/service 写调用、`models.UserRole` GORM 写链和针对 `t_user_role` 的 Exec。允许清单精确到文件和函数，不是整文件豁免。
- 独立检测器测试证明 Create/Update/Delete/链式 Model.Updates/原始 SQL 均会命中，Find 和其他 model 写入不会误报。migration 初始化和仿真 seed 不属于在线 service 扫描范围，保持原有确定性数据职责。

### 文件与验证

```text
internal/services/user_role_service.go
internal/services/user_role_write_contract_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^(TestUserRoleRuntimeWritesStayBehindAuditedServices|TestIsUserRoleMutationCall|TestUserServiceAssignRolesEnforcesAuthority|TestUserRoleChangeLogRollsBackWithRoleReplacementTransaction|TestWxWorkDefaultStoreStaffRoleWritesOneAuditLog)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 全部通过。没有 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端、AI runtime、模型调用、token、usage 或计费变化；没有修改 `.codex/audits/` 或生成 docs/generated 报告。

### 并行分支与回滚

- 以共同基线检查，`origin/codex/ai-billing@f2d2da4` 不修改本批 service/test 文件，当前无同文件冲突和 migration 排序要求。最终合并后必须保留 AST 契约并重跑，它会对 AI 分支新增 service 一并生效。
- 可独立回滚四个文件中的本批变更且无需数据库回滚；回滚会恢复无权限、无日志的通用角色写旁路，不建议回滚。

## 第 74 批：角色变更快照语义只读审计（2026-07-15）

### 规则与边界

- 第 71A 批只检查 UserRoleChangeLog 的 TenantID 与 User/Operator 关系，无法发现四个 JSON 快照列损坏。本批继续复用 TenantIntegrityAudit，不创建第二套审计命令。
- ID 数组必须可解析、非 null，元素为正数且严格升序；code 数组必须可解析、非 null，元素非空、无首尾空格且严格升序。严格升序同时保证去重。
- before/after 各自的 ID/code 数量必须一致，before/after ID 集合必须不同。违规码为 `USER_ROLE_CHANGE_LOG_PAYLOAD_INVALID`，按日志行计数并使用统一 sampleLimit。
- 不回查快照中的 Role 当前状态、ID 或 code。历史模板可能在变更后改名或删除，强制关联当前 Role 会破坏追加式证据语义；目标账号和操作人关系仍由普通完整性关系负责。

### 文件与验证

```text
internal/repositories/user_role_change_log_repository.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^TestTenantIntegrityAudit(PassesCleanTwoTenantFixture|ReportsTenantRelationAndRoleViolations|ReportsInvalidUserRoleChangePayloads)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 合法平台/租户记录不误报；非法 JSON、逆序、重复、数量不一致、无变化和空格 code 共 6 条违规，SampleLimit=2 时保留总数并只返回前两个 ID。全部验证通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端、AI runtime、token、usage 或计费变化；模型策略、必需表和普通关系计数保持 52/52、65、127。

### 并行分支与回滚

- 共同基线检查显示 `origin/codex/ai-billing@f2d2da4` 不修改本批运行文件，无同文件冲突、migration 排序或 rebase 要求。
- 可独立回滚五个文件中的本批变化，不涉及数据库回滚；回滚后损坏快照不会再由 preflight 发现，但不得删除或改写历史日志。

## 第 75 批：角色变更并发顺序与日志连续性（2026-07-15）

### 写入与审计语义

- UserRole 角色替换原先在事务中读取 User，但没有行锁；同账号并发请求可能读取相同 before 并各自产生日志。本批新增 UserRepository.GetForUpdate，`replaceUserRolesDB` 在读取角色快照前锁定目标账号，MySQL 通过 `FOR UPDATE` 串行化，SQLite 沿用单写事务。
- 行锁测试通过 GORM Query callback 检查 `FOR` clause，不依赖 SQL 日志字符串。所有现有角色写入口仍复用原事务，没有新增 mutex、分布式锁、状态字段或 API。
- UserRoleChangeLog 审计行增加 UserID，UserRoleRepository 一次批量读取所有有日志账号的当前 role IDs。相邻日志比较 `previous.after_ids == current.before_ids`，最新日志比较 `after_ids == current UserRole IDs`。
- 违规码为 `USER_ROLE_CHANGE_LOG_CHAIN_BROKEN`。相邻断链以后一条日志 ID 为样本；终态漂移以最后日志 ID 为样本，同一 ID 去重。无日志历史账号不参与，payload 已损坏的账号跳过连续性检查，只保留第 74 批原始违规。
- code 不参与连续性，因为 Role 模板可能在两次账号变更之间合法改名；ID 快照才是角色集合身份。历史第一条 before 不要求为空，以兼容上线前已有账号从当前集合开始记录。

### 文件与验证

```text
internal/repositories/user_repository.go
internal/repositories/user_role_repository.go
internal/repositories/user_role_change_log_repository.go
internal/services/user_service.go
internal/services/tenant_integrity_audit_service.go
internal/services/tenant_integrity_audit_service_test.go
internal/services/role_user_authority_test.go
docs/design/multi-tenant-company-registration.md
docs/development/customer-audit-merge-handoff.md
```

```text
go test -race ./internal/services -run '^(TestUserRepositoryGetForUpdateUsesRowLock|TestUserServiceAssignRolesEnforcesAuthority|TestTenantAdminCreatesAccountWithLowerRoleOnly|TestUserRoleChangeLogRollsBackWithRoleReplacementTransaction|TestTenantIntegrityAuditPassesCleanTwoTenantFixture|TestTenantIntegrityAuditReportsInvalidUserRoleChangePayloads|TestTenantIntegrityAuditReportsBrokenUserRoleChangeChain)$' -count=1 -p 1
go test ./internal/services -count=1 -p 1
go test ./... -count=1 -p 1
go vet ./...
git diff --check
```

- 合法双段链不误报；另一账号的第二条 before 断裂和平台账号最新 after 漂移各报告一条，样本精确。全部验证通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端、AI runtime、token、usage 或计费变化；没有修改 `.codex/audits/` 或 docs/generated。

### 并行分支与回滚

- 共同基线检查显示 `origin/codex/ai-billing@f2d2da4` 不修改本批运行文件，无同文件冲突、migration 排序或 rebase 要求。
- 可独立回滚九个文件中的本批变化，不需要数据库回滚；回滚会失去同账号并发序列化和日志连续性/终态检查，不建议回滚。
