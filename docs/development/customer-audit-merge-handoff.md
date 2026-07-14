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
