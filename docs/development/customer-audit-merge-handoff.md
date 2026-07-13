# 客服与对话审计分支合并交接

> 状态日期：2026-07-13
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

- `000025_backfill_wxwork_agent_team_bindings.go`
- `000026_backfill_store_staff_agent_team_bindings.go`

版本 `21-24` 已由 `codex/ai-billing` 使用，因此本分支不得恢复为 `21/22`。

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
- DDL 仅通过 AutoMigrate，无 migration 版本，因此不与 `ai-billing` 的 `21-24` 或本分支 `25-26` 冲突。

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

- DDL 由 `AutoMigrate` 增加 Role/Permission 字段；DML 使用 `000027_sync_tenant_auth_foundation.go` 同步内置角色、权限和默认关系。
- migration 27 先统计 `t_user_permission`。若存在历史记录则返回错误、保留全部记录并阻止服务带着权限语义变化启动；记录为空时不删除物理表，只移除运行时依赖。
- migration 27 删除 `permission.sync` 及其角色关系，迁移保持幂等。

### 主要文件

```text
internal/models/models.go
internal/pkg/constants/auth.go
internal/migration/000002_init_auth_data.go
internal/migration/000027_sync_tenant_auth_foundation.go
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

- 已在开始阶段执行 `git fetch origin`；`codex/ai-billing` 当前使用 migration 21-24，本分支使用 25-27，无版本重复。
- 高风险同文件为 `models.go`、`web/lib/api/admin.ts`、导航和双语资源；AI 分支当前最新提交未修改本阶段字段语义，但合并前仍需再次 fetch 和逐文件核对。
- 本阶段不修改 AI runtime、供应商配置、token 统计、计费口径、会话/消息状态或 WebSocket payload。
- 建议先合并本阶段权限共享契约，再由客服和 AI 分支 rebase；阶段 2 才增加 Tenant 和认证上下文。
- 回滚边界：可以整体回滚阶段 1 代码和 migration 27；空的 `t_user_permission` 物理表保留，不做破坏性 DDL。
