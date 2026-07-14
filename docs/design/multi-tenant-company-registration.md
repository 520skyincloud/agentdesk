# 多租户公司、账号权限与邀请注册设计

> 状态：已确认，作为后续实现基线
> 确认日期：2026-07-13
> 适用范围：租户公司、账号、角色权限、注册邀请、公司切换、数据隔离、后台导航
> 不涉及：AI 回复策略、模型调用、token 统计、计费口径

## 1. 文档权威边界

本文件是后续多租户公司与账号体系的设计依据。实现时仍以真实代码为最终事实；若代码与本文冲突，必须先查清真实调用链，再修正代码或更新本文。

本文在以下领域替代旧文档中的历史表述：

- 平台公司、客户企业和门店的层级关系。
- 超级管理员、管理员、公司主管、客服组长、客服和门店员工的角色层级。
- 页面导航按角色写死的历史方案。
- 账号级例外权限的历史方案。
- 公司创建、邀请码和普通注册流程。
- 多租户数据隔离和公司切换方式。

`docs/design/customer-service-full-scope-implementation.md` 中与“按角色写死菜单”相关的内容不再作为导航实现依据。AI 回复引擎仍以代码和 `docs/design/reply-runtime-engine.md` 为准。

## 2. 目标与非目标

### 2.1 目标

系统升级为可服务多家客户公司的多租户客服平台：

```text
平台
├── 超级管理员 / 管理员
└── 接入公司 Tenant
    ├── 公司主管
    ├── 客服组长 / 客服 / 门店员工
    ├── 综合客服组 / 客服小组 / 排班
    ├── 门店 / 企微员工号 / 渠道
    ├── 客户企业 / 客户 / 会话 / 派单 / 工单
    ├── 知识库 / 快捷回复 / 标签 / AI Agent
    └── 公司级配置与审计
```

必须达到：

- 平台管理员可以创建、启停和进入接入公司。
- 创建公司时同时创建首个公司主管账号和唯一邀请码。
- 邀请码注册的账号自动绑定对应租户，但不自动获得业务角色。
- 公司主管管理本公司账号并为其分配下级角色。
- 所有业务数据按租户强制隔离，不能依赖前端筛选保证安全。
- 所有业务能力都在权限管理中有可见权限点。
- 账号只绑定角色，角色绑定权限，不保留账号级隐藏权限。
- 页面和必要信息保守显示，操作选项按权限隐藏；后端继续逐接口鉴权。

### 2.2 非目标

本轮不设计：

- 自助申请租户和自动开通套餐。
- 跨租户共享客服账号。
- 租户自定义任意权限点。
- 邀请链接直接授予客服、组长或门店员工角色。
- 对工商信息真实性的自动第三方核验；第一版采用格式校验、唯一约束和平台人工确认。
- 修改模型供应商、模型调用、token 或计费语义。

## 3. 当前代码审计结论

### 3.1 `Company` 语义已经混用

现有 `Company` 同时用于：

- `Customer.CompanyID` 的客户所属企业。
- `Store.CompanyID` 的门店上级公司。
- 企微员工号和公司级模型设置范围。

因此不能直接把现有 `Company` 改名为租户。必须新增独立 `Tenant`，并把原 `Company` 收敛为租户下的“客户企业/客户组织”。历史门店公司数据通过显式迁移映射为 `Tenant`。

### 3.2 当前登录没有租户上下文

`AuthPrincipal` 目前只有用户、角色和权限，没有：

- 用户固定所属租户。
- 平台管理员当前进入的租户。
- 是否允许跨租户管理。

现有多数列表和详情接口只校验 `*.view` 后查询全表，不能形成数据隔离。

### 3.3 当前权限体系存在缺口

- `UserPermission` 支持账号级临时允许或拒绝权限，认证服务会合并这些记录，违反“账号只绑定角色”。
- 前端导航通过角色名称写死允许 URL，形成权限管理之外的隐藏页面规则。
- 部分 service 通过 `admin`、`cs_team_leader` 等角色名直接放行业务动作。
- `RolePostUpdate_sort` 只有登录校验，没有 `role.update` 权限校验。
- `permission.sync` 有权限常量但没有路由、Handler、页面或真实调用，是无效空权限。
- 账号角色分配只检查角色是否启用，没有校验操作者等级、租户归属和目标角色层级。
- 角色和用户页面的部分按钮未按动作权限隐藏。

### 3.4 当前没有普通注册与邀请注册

现有认证只有登录、会话和后台创建账号，没有公开注册路由、邀请码模型、注册审核状态和邀请使用日志。

### 3.5 `Channel` 不能随旧页面删除

当前 `Channel` 是 Web、企微、会话、消息投递、回调和 Outbox 的真实运行时入口。旧“接入渠道”页面可以重做，但底层 `Channel` 必须保留并改为租户数据，管理入口迁移到当前公司的设置中。

## 4. 已确认的产品原则

以下内容已经确认，实施时不再采用平行方案：

1. 新增独立 `Tenant`，页面名称为“接入公司”。
2. 公司主管位于管理员之下、客服组长之上。
3. 公司主管拥有本租户全部业务管理能力，但没有平台管理能力。
4. 账号只能分配角色，不能直接分配权限。
5. 管理员及以上才能为下级角色分配权限。
6. 所有新能力先建立权限点并同步到权限管理，再开发页面和接口。
7. 页面和必要信息保守显示；模块使用 `view` 权限控制进入，具体操作按动作权限隐藏。
8. 导航不再使用角色名称硬编码允许 URL。
9. 邀请码只决定租户归属，不携带角色。
10. 邀请注册账号默认待审核、待分配角色，不能直接访问业务数据。
11. 数据隔离由后端租户上下文和查询约束保证，前端公司切换器不是安全边界。
12. `Channel` 运行时保留，并迁移为公司级渠道设置。

## 5. 核心领域模型

### 5.1 Tenant 接入公司

建议字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `ID` | `int64` | 租户主键 |
| `TenantCode` | `varchar(64)` | 系统生成、全局唯一、创建后不可修改 |
| `LegalName` | `varchar(200)` | 公司法定名称 |
| `ShortName` | `varchar(100)` | 后台显示简称 |
| `RegistrationType` | `varchar(30)` | 证件类型，如统一社会信用代码 |
| `RegistrationNo` | `varchar(64)` | 法定识别号，全局唯一 |
| `ContactName` | `varchar(100)` | 联系人 |
| `ContactMobile` | `varchar(32)` | 联系电话 |
| `ContactEmail` | `varchar(100)` | 联系邮箱 |
| `Address` | `varchar(500)` | 公司地址 |
| `VerificationStatus` | `varchar(30)` | 待核验、已核验、已驳回 |
| `VerifiedAt` | `datetime` | 核验时间 |
| `VerifiedBy` | `bigint` | 核验人 |
| `Status` | `int` | 启用、禁用、删除 |
| `Remark` | `text` | 平台备注 |
| `AuditFields` | - | 创建与更新审计 |

唯一性：

- `TenantCode` 全局唯一。
- `RegistrationType + RegistrationNo` 全局唯一。
- 法定名称不作为唯一事实；同名公司由法定识别号区分。

### 5.2 TenantInvitation 公司邀请码

每家公司同时只保留一个有效邀请码，但允许重置：

| 字段 | 说明 |
| --- | --- |
| `TenantID` | 所属租户 |
| `CodeHash` | 邀请码哈希，用于查询和比较 |
| `CodeCiphertext` | 邀请码密文，用于公司主管查看和复制 |
| `CodeLast4` | 列表脱敏展示 |
| `Version` | 重置版本 |
| `UsedCount` | 成功注册次数 |
| `LastUsedAt` | 最近使用时间 |
| `RotatedAt` | 最近重置时间 |
| `Status` | 有效、失效 |
| `AuditFields` | 创建和重置审计 |

邀请码要求：

- 使用密码学安全随机数生成，不能使用数据库 ID、社会信用代码或顺序编号。
- 全局唯一，不能暴露租户 ID。
- 公司停用时自动不可用。
- 重置后旧邀请码立即失效。
- 日志不得明文记录邀请码。

### 5.3 User 账号扩展

建议新增：

| 字段 | 说明 |
| --- | --- |
| `TenantID` | `0` 表示平台账号，正数表示固定所属租户 |
| `RegistrationSource` | 平台创建、公司创建、邀请码注册 |
| `ApprovalStatus` | 待审核、已通过、已拒绝 |
| `ApprovedAt` | 审核时间 |
| `ApprovedBy` | 审核人 |
| `ApprovalRemark` | 审核或驳回说明 |
| `MustChangePassword` | 平台创建初始账号首次登录必须改密 |

第一版继续保持用户名、手机号和邮箱全局唯一，避免同时改变登录标识。后续确有同名账号需求时，再单独设计“公司识别码 + 用户名”登录。

平台账号 `TenantID=0`；租户账号必须 `TenantID>0`。普通注册请求不能提交或覆盖 `TenantID`。

### 5.4 Role 角色扩展

现有 `SortNo` 只负责页面排序，不能作为授权等级。建议新增：

| 字段 | 说明 |
| --- | --- |
| `Scope` | `platform` 或 `tenant` |
| `AuthorityLevel` | 固定授权等级，不随拖动排序变化 |
| `IsSystem` | 系统角色保护标记 |

默认等级：

| 角色 | Code | Scope | Level |
| --- | --- | --- | ---: |
| 超级管理员 | `super_admin` | platform | 100 |
| 管理员 | `admin` | platform | 80 |
| 公司主管 | `tenant_admin` | tenant | 60 |
| 客服组长 | `cs_team_leader` | tenant | 40 |
| 客服 | `cs_user` | tenant | 20 |
| 门店员工 | `store_staff` | tenant | 20 |

### 5.5 Permission 权限作用域

权限定义新增 `Scope`：

| 值 | 说明 |
| --- | --- |
| `platform` | 仅平台角色可持有，例如接入公司管理、登录会话管理、模型供应商配置和 MCP 调试 |
| `tenant` | 可分配给租户角色，但实际数据仍受租户、客服组和门店范围约束 |

未显式声明的现有业务权限按 `tenant` 处理。租户角色保存权限时，前端过滤平台权限，后端再次拒绝平台权限，不能只依赖页面过滤。

### 5.6 TenantRegistrationLog 注册日志

公开注册必须记录安全审计，但不能保存明文密码或邀请码：

- `TenantID`
- `InvitationID`
- `Action`，区分邀请码校验、注册、审核等安全动作
- `InviteHash`，只保存规范化邀请码的哈希，不保存明文
- `UserID`，失败时为 0
- `Principal`，脱敏用户名/手机号
- `Success`
- `Reason`
- `ClientIP`
- `UserAgent`
- `CreatedAt`

## 6. 平台数据与租户数据边界

### 6.1 平台级数据

默认保持平台级：

- Permission 权限定义。
- Role 系统角色模板。
- 平台管理员账号。
- 全局系统配置。
- 企微协议设备池。
- 模型供应商和计费基础配置，具体边界与 `codex/ai-billing` 确认。
- Migration 和系统运行日志。

### 6.2 租户级数据

至少以下根数据必须增加 `TenantID`：

- User、UserIdentity。
- 原 Company（客户企业）、Customer、CustomerIdentity、CustomerContact。
- Store、StoreStaffBinding、WxWorkProtocolInstance。
- AgentProfile、AgentTeam、AgentTeamSquad、AgentTeamSchedule。
- Channel、AIAgent、QuickReply、Tag。
- Conversation、Message、ConversationAssignment、ConversationRouteState。
- Ticket、TicketProgress、TicketView。
- KnowledgeBase、KnowledgeDocument、KnowledgeFAQ、KnowledgeChunk、KnowledgeCandidate。
- Asset、Notification。

会被后台任务、回调、WebSocket、Outbox 或独立查询直接访问的子表也必须带 `TenantID`，不能只靠父表间接推断。包括消息同步、渠道映射、Outbox、检索日志和会话事件等。

关联关系必须满足同租户约束。例如：

- 不能把租户 A 的客服分配到租户 B 的客服组。
- 不能把租户 A 的知识库绑定到租户 B 的企微员工号。
- 不能把租户 A 的客户关联到租户 B 的会话或工单。
- 不能通过猜测 ID 读取、修改或删除其他租户数据。

## 7. 角色、权限与数据范围

### 7.1 三层判断

任何后台操作按三层判断：

```text
登录身份是否合法
  ↓
是否拥有业务权限
  ↓
目标数据是否位于可管理的租户/客服组/门店范围
```

权限回答“能做什么”，数据范围回答“能操作谁的数据”。两者不能互相替代。

### 7.2 页面保守显示原则

页面和必要信息优先保留，避免角色因动作权限不足而失去正常工作上下文：

- 一级模块由对应 `*.view` 权限决定是否显示和进入。
- 复合页面只要拥有任一子模块查看权限，就显示页面框架和允许查看的内容。
- 创建、编辑、删除、分配、导出、重置等按钮分别按动作权限隐藏。
- 表格基础字段正常显示；敏感字段另设专门查看权限时才脱敏或隐藏。
- 没有动作权限时不展示误导性按钮，不依赖点击后报 403。
- 直接访问无查看权限页面时返回明确的无权限状态。
- 后端始终校验权限；前端隐藏只负责使用体验。

示例：客服需要查看今日个人审计时，为客服角色配置 `audit.today.view`。审计页可见，但完整审计、导出和配置分别要求 `audit.view`、`audit.export`、`audit.config`。

### 7.3 新增租户相关权限

所有权限必须在 `internal/pkg/constants/auth.go` 定义，并通过幂等 DML migration 同步到权限表：

| 权限 Code | 含义 | 默认角色 |
| --- | --- | --- |
| `tenant.view` | 查看接入公司 | 超管、管理员 |
| `tenant.create` | 创建接入公司 | 超管 |
| `tenant.update` | 编辑接入公司 | 超管、管理员 |
| `tenant.updateStatus` | 启停接入公司 | 超管、管理员 |
| `tenant.switch` | 进入和切换公司 | 超管、管理员 |
| `tenantInvite.view` | 查看邀请码和邀请链接 | 超管、管理员、公司主管 |
| `tenantInvite.rotate` | 重置邀请码 | 超管、管理员、公司主管 |
| `tenantRegistration.view` | 查看邀请注册账号 | 超管、管理员、公司主管 |
| `tenantRegistration.review` | 审核邀请注册账号 | 超管、管理员、公司主管 |

公司成员管理继续复用：

- `user.view`
- `user.create`
- `user.update`
- `user.delete`
- `user.assignRole`

这些权限在租户账号上只对本租户生效。

### 7.4 公司主管默认权限

公司主管默认拥有本租户业务管理权限：

- 用户查看、创建、更新、停用和分配下级角色。
- 客服组、小组、客服档案和排班管理。
- 门店、企微员工号和租户渠道管理。
- 客户企业、客户、会话、派单和工单管理。
- 知识库、快捷回复、标签、AI Agent 等租户业务配置。
- 邀请码查看、重置和注册账号审核。
- 租户内通知和审计查看。

公司主管默认不拥有：

- `role.create`、`role.update`、`role.delete`、`role.assignPermission`。
- 平台租户创建和跨租户切换。
- 超级管理员和管理员账号管理。
- 平台模型供应商、计费规则、设备池和全局存储配置。

公司主管可以查看角色和权限说明，但只能把平台预先配置好的租户角色分配给本公司账号。

### 7.5 账号角色分配规则

分配角色必须同时满足：

1. 操作者拥有 `user.assignRole`。
2. 操作者和目标账号属于同一租户；平台管理员进入目标租户后例外。
3. 目标角色为启用状态。
4. 目标角色 `AuthorityLevel` 低于操作者最高角色等级。
5. 租户账号只能分配 `Scope=tenant` 的角色。
6. 不能修改自己的角色。
7. 公司主管不能分配 `tenant_admin`、`admin`、`super_admin`。
8. 管理员不能分配或修改 `super_admin`。
9. 角色变化后撤销目标账号现有登录会话，要求重新登录。
10. 记录原角色、新角色、操作者、租户和时间。

新增或替换公司主管账号由平台管理员执行，不由现任公司主管自行提权。

### 7.6 角色权限配置规则

- 必须拥有 `role.assignPermission`。
- 超级管理员可以配置管理员及以下角色，但超级管理员自身角色保持系统保护。
- 管理员只能配置比自己低级的角色，不能修改管理员或超级管理员角色。
- 公司主管默认没有角色权限配置能力。
- 角色管理页面必须显示角色作用域和等级，不能把拖动排序误认为权限层级。

### 7.7 清理隐藏权限

实施时按以下顺序废止 `UserPermission`：

1. 查询现有账号级例外记录并形成审计清单。
2. 把仍有业务价值的例外转换成正式角色或细粒度权限。
3. 清空账号级例外记录。
4. 删除认证服务对 `t_user_permission` 的合并读取。
5. 删除无调用的 service、repository 和 model。
6. 确认无历史代码引用后再移除数据表。

## 8. 创建接入公司流程

### 8.1 页面表单

“接入公司”新建流程分三段：

1. 公司身份：法定名称、简称、证件类型、统一社会信用代码、联系方式和地址。
2. 公司主管：用户名、姓名、手机号、邮箱。
3. 确认创建：核对公司、主管账号和默认资源。

### 8.2 后端事务

必须在一个事务内完成：

```text
校验法定识别号和账号唯一性
→ 创建 Tenant
→ 生成 TenantCode
→ 创建并加密保存邀请码
→ 创建公司主管 User
→ 绑定 TenantID
→ 分配 tenant_admin 角色
→ 创建默认综合客服组
→ 写入审计记录
```

任一步失败，全部回滚。接口只返回一次公司主管初始密码，账号首次登录必须改密。

### 8.3 创建成功结果

页面展示：

- 公司名称与 `TenantCode`。
- 公司主管用户名和一次性初始密码。
- 公司邀请码。
- 带邀请码的注册链接。
- 进入该公司的操作。

## 9. 邀请注册流程

### 9.1 普通注册链接

公开页面：

```text
/register
/register?invite=<invite-code>
```

基本信息：

- 用户名。
- 姓名或昵称。
- 手机号。
- 邮箱。
- 密码和确认密码。
- 公司邀请码，必填。

从邀请链接进入时，邀请码自动填充并锁定。页面在服务端验证后只显示公司名称和简称，不暴露公司内部数据。

### 9.2 注册事务

```text
校验邀请码格式
→ 查找有效邀请并校验租户状态
→ 校验用户名、手机号和邮箱唯一性
→ 创建绑定 TenantID 的用户
→ RegistrationSource=invite
→ ApprovalStatus=pending
→ 不分配业务角色
→ 增加邀请使用次数
→ 写注册安全日志
```

邀请码只绑定租户，不允许前端提交 `tenantId`、角色 ID、客服组 ID 或门店 ID。

### 9.3 审核与启用

注册成功后账号处于“待审核/待分配角色”：

- 不能登录业务后台，或登录后只能看到等待审核状态。
- 公司主管在本租户账号管理中查看申请。
- 审核时选择客服组长、客服或门店员工等下级角色。
- 门店员工后续再绑定门店和客服组。
- 审核通过后账号启用；驳回时记录原因。

### 9.4 邀请注册浮窗

当前公司账号管理页增加“邀请注册”：

- 显示当前公司。
- 显示完整邀请码。
- 显示带邀请码的注册链接。
- 复制邀请码。
- 复制注册链接。
- 重置邀请码。
- 显示成功注册次数和最近使用时间。

邀请码查看由 `tenantInvite.view` 控制，重置操作由 `tenantInvite.rotate` 控制。

### 9.5 安全限制

- 邀请码验证和注册接口按 IP、邀请码和账号标识限流。
- 注册提交使用幂等键，避免重复创建账号。
- 密码按现有 bcrypt 规则保存，不记录明文。
- 注册失败统一返回适度模糊的错误，防止枚举有效邀请码和账号。
- 邀请码重置、审核、驳回和角色分配全部写审计。
- 第一版即使邀请码泄露，也只能产生待审核账号，不能直接获得业务权限。

## 10. 租户上下文与公司切换

### 10.1 AuthPrincipal

认证上下文至少增加：

- `TenantID`：账号固定租户，平台账号为 0。
- `ActiveTenantID`：平台管理员当前进入的租户。
- `CanSwitchTenant`：是否具有 `tenant.switch`。
- `IsPlatformAccount`：是否为平台账号。

### 10.2 请求规则

平台管理员进入公司业务页面后，由统一请求客户端携带 `X-Tenant-ID`：

- 平台账号：后端验证 `tenant.switch` 和目标租户状态，再设置 `ActiveTenantID`。
- 租户账号：始终使用账号自身 `TenantID`；传入其他租户 ID 时拒绝。
- 平台页面不要求当前租户；公司业务页面必须存在有效租户上下文。

`X-Tenant-ID` 只是平台管理员选择了哪家公司，不是授权凭证。

### 10.3 查询和写入规则

- 所有租户 service 方法显式接收租户上下文。
- Repository 查询统一附加 `tenant_id = ?`。
- 详情、更新、删除必须使用 `id + tenant_id` 查找，不能先按 ID 读取再相信前端。
- 新建数据的 `TenantID` 从认证上下文写入，禁止采用请求体传入值。
- 跨实体关联在 service 中校验双方 `TenantID` 一致。
- 禁止在 handler 直接调用 repository。

### 10.4 非 HTTP 链路

以下链路同样必须隔离：

- WebSocket 连接和订阅主题包含 `TenantID`。
- 第三方回调通过 Channel 或企微实例反查租户，不相信回调 payload 的租户字段。
- Outbox、异步任务和定时任务保存并恢复租户上下文。
- 文件存储使用租户目录前缀，Asset 记录带 `TenantID`。
- 缓存 key 包含 `TenantID`。
- 搜索、统计、导出和报表强制带租户条件。
- Qdrant payload 和检索 filter 增加 `tenant_id`，不能只依赖 `knowledge_base_id`。

## 11. API 设计

遵循现有 Gin 显式路由，不增加 `/api/v1`。

### 11.1 平台接入公司

```text
GET  /api/dashboard/tenant/list
GET  /api/dashboard/tenant/:id
POST /api/dashboard/tenant/create
POST /api/dashboard/tenant/update
POST /api/dashboard/tenant/update_status
```

公司切换不写服务器全局状态，不增加“切换公司”写接口；前端选择公司后由请求头携带上下文，避免不同浏览器标签互相覆盖。

### 11.2 邀请码管理

```text
GET  /api/dashboard/tenant-invitation/current
POST /api/dashboard/tenant-invitation/rotate
```

### 11.3 注册审核

```text
GET  /api/dashboard/tenant-registration/list
POST /api/dashboard/tenant-registration/review
```

### 11.4 公开注册

```text
POST /api/auth/register/validate_invite
POST /api/auth/register
```

公开注册 Handler 只做参数解析和响应；邀请码验证、用户创建、事务和日志全部在 service。

## 12. 页面与导航设计

### 12.1 侧边栏头部

品牌下方固定显示当前公司：

- 超级管理员、管理员：可搜索和切换公司，并进入“接入公司”。
- 公司主管及普通租户账号：只显示所属公司，不提供切换。
- 平台账号未选择公司时，只显示平台管理模块。

### 12.2 平台管理导航

仅拥有相应平台查看权限的账号显示：

- 平台总览。
- 接入公司。
- 平台管理员。
- 角色管理、权限管理。
- 模型与计费。
- 设备池、存储和平台审计。

### 12.3 当前公司导航

按 `view` 权限保守展示：

- 工作台：公司总览、会话、派单、工单、客户。
- 客服组织：公司账号、客服组与小组、排班、门店员工。
- 服务能力：知识库、知识候选、快捷回复、标签、AI Agent。
- 公司设置：渠道接入、公司资料、邀请注册。

原 `/dashboard/companies` 的“公司”改称“客户企业”，位于客户管理中。原公司详情里的门店员工号开户注册迁移到门店员工管理，模型配置与 `codex/ai-billing` 协调后归入公司设置。

### 12.4 接入公司页面

采用适合运营管理的紧凑列表，不使用大面积营销卡片：

- 公司法定名称和简称。
- `TenantCode`。
- 法定识别号。
- 核验状态、启用状态。
- 公司主管。
- 客服数、门店数、客服组数。
- 最近活跃时间。
- 进入、编辑、启停等操作。

页面操作按 `tenant.*` 权限隐藏。创建公司使用分段抽屉或对话框，不跳转复杂向导。

### 12.5 账号管理页面

保留当前账号列表和基础信息，增加：

- 当前公司固定上下文。
- 注册来源。
- 审核状态。
- 角色和客服组/门店归属。
- “新增账号”和“邀请注册”两个清晰入口。
- 待审核筛选和审核操作。

用户页面只允许分配角色，不展示权限勾选器。

### 12.6 角色与权限页面

- 角色页展示作用域、授权等级和权限摘要。
- `role.view` 允许查看角色和权限说明。
- `role.create` 控制新增角色。
- `role.update` 控制编辑、状态和拖动排序。
- `role.assignPermission` 控制权限配置入口。
- `role.delete` 控制删除。
- 权限页展示所有当前有效权限及来源，不提供账号级权限入口。

## 13. 旧渠道页面处置

前端“接入渠道”改为“接入公司”时：

1. 新导航指向 `/dashboard/tenants`。
2. 旧 `/dashboard/channels` 在迁移期重定向到新入口，避免书签直接失效。
3. 删除旧渠道列表和编辑页面的展示逻辑。
4. 保留 Channel model、service、handler 和运行时调用。
5. 在当前公司“公司设置 > 渠道接入”重新提供租户范围内的渠道配置。
6. Channel 增加 `TenantID`，所有读取和回调按租户校验。

只有确认旧前端组件无引用后才能删除文件，不能因为替换页面而删除渠道运行能力。

> 阶段 7A 实施说明（2026-07-14）：当前先直接复用 `/dashboard/channels` 作为唯一“接入公司”入口，删除该路由下的旧渠道列表与编辑逻辑，不同时新增 `/dashboard/tenants` 平行入口或重定向。待平台管理与当前公司导航完成拆分时，再统一决定是否迁移 URL；迁移前后都必须保持单一产品入口。Channel 后端、消息路由和 Outbox 运行时不受此临时路径选择影响。

## 14. 历史数据迁移

### 14.1 迁移原则

- DDL 通过 AutoMigrate。
- DML 回填通过新版本幂等 migration。
- 创建 migration 前同时核对 `origin/main`、`codex/customer-audit` 和 `codex/ai-billing` 版本。
- 多租户正式启用前必须完成回填和一致性审计。

### 14.2 现有 Company 拆分

不能把所有旧 Company 自动解释成租户：

- 被 Store、WxWorkProtocolInstance 或公司级模型设置引用的记录生成 Tenant 候选。
- 只被 Customer 引用的记录保留为客户企业。
- 同时被门店和客户引用的记录拆成 Tenant 与客户企业两条记录。
- 无法判断归属的数据进入“历史默认租户”，由平台管理员核对。

迁移必须输出统计，不向 `docs/generated/` 提交临时报告。

### 14.3 账号回填

- 超级管理员和管理员回填 `TenantID=0`。
- 可通过门店员工绑定、客服档案或客服组明确归属的账号回填对应租户。
- 无法确定的普通账号保持禁用并进入人工确认清单。
- 禁止默认把所有普通账号开放到所有租户。

### 14.4 权限数据清理

- 新增租户权限时使用新 DML migration 同步权限表和默认角色关系。
- `permission.sync` 没有真实调用，随权限基础阶段删除常量、默认角色绑定和历史权限记录。
- `RolePostUpdate_sort` 补 `role.update` 校验。
- `UserPermission` 按第 7.7 节审计后废止。

## 15. 实施阶段与提交边界

任何阶段开始前都必须 `git fetch origin` 并重新检查并行分支同文件修改。

### 阶段 0：设计与契约确认

- 本文作为设计基线。
- 确认字段命名、权限 code、角色层级和接口路径。
- 不修改 AI runtime 和计费语义。

验证：文档审查、共享文件清单、migration 版本核对。

### 阶段 1：权限基础清理

- 新增公司主管角色定义。
- 新增租户、邀请码和注册审核权限。
- 同步权限与默认角色。
- 修复角色排序鉴权。
- 删除无效 `permission.sync`。
- 移除前端角色 URL 白名单，改为查看权限和动作权限。
- 审计 `UserPermission` 数据，但先不直接删除有数据的表。

迁移安全规则：migration 34 若发现 `t_user_permission` 存在历史记录，必须中止启动并保留原数据，待人工转换为正式角色后重试；记录数为 0 时才停用运行时读取并保留空物理表。禁止静默删除账号级例外权限。

高风险共享文件：`internal/pkg/constants/auth.go`、权限 migration、`web/lib/navigation.tsx`、多语言资源。

### 阶段 2：Tenant 与认证上下文

- 新增 Tenant、TenantInvitation、TenantRegistrationLog。
- 扩展 User、Role、AuthPrincipal 和登录响应。
- 实现平台账号与租户账号上下文规则。
- 统一请求客户端支持 `X-Tenant-ID`。

迁移安全规则：migration 35 创建历史默认租户并回填现有账号。若账号同时持有启用中的平台角色和租户角色，迁移必须中止并保留原数据；重复执行不得覆盖后续创建的邀请注册账号或其他租户账号。

高风险共享文件：`internal/models/models.go`、认证 DTO/service、`web/lib/api/client.ts`。

### 阶段 3A：公司创建与邀请码管理

- 实现接入公司 CRUD。
- 实现公司与主管账号原子创建。
- 实现邀请码查看和重置。
- 为默认综合客服组增加 `TenantID` 和 `IsDefault`，并通过 migration 36 将历史客服组归入 `legacy-default`。
- 邀请码使用 AES-256-GCM 加密保存可受控展示密文，查询使用 SHA-256 哈希；密钥通过 `AGENT_DESK_INVITATION_ENCRYPTION_KEY` 注入。
- 统一社会信用代码执行 18 位合法字符格式校验，数据库组合唯一约束保证同证件类型下唯一；这不替代第三方工商核验。
- 创建公司、主管账号、`tenant_admin` 角色关系、默认综合客服组和首个邀请码必须处于同一事务。
- 重置邀请码时禁用该租户所有旧有效版本，并从历史最高版本递增；包含初始密码或邀请码明文的响应使用 `Cache-Control: no-store`。

阶段 3A 只开放平台接入公司和当前公司邀请码管理 API，不代表客服组及其他业务数据已经完成租户隔离，也不启用公开注册页面。

### 阶段 3B：公开邀请注册与审核

- 实现公开注册、审核和角色分配。
- 增加注册安全日志、限流和幂等。
- 邀请码只绑定租户；注册账号默认待审核且不带角色。

当前实现状态（2026-07-14）：

- 后端已实现邀请码公开校验、注册提交、租户内待审核列表和审核接口。
- 公开注册由 `tenantRegistration.enabled` 控制并默认关闭；启用时必须配置有效的独立邀请码加密密钥，否则服务拒绝启动。
- 注册请求必须携带调用方生成的 `X-Request-Id`。安全日志保存 HMAC 请求指纹，不保存密码或邀请码明文；同一请求标识修改密码或其他请求内容会被拒绝。
- 注册按可信客户端 IP、邀请码和账号标识限流。Gin 默认不信任转发头，只有 `server.trustedProxies` 明确配置的反向代理才能提供真实客户端 IP。
- 邀请注册账号直接绑定邀请码对应租户，初始为禁用、待审核且无角色；审核通过必须同时具有 `tenantRegistration.review` 和 `user.assignRole`，并且只能分配操作者权限等级以下的本租户角色。
- 审核通过、拒绝和幂等重放均保留注册安全日志；审核后撤销该账号已有登录会话。
- `/register` 页面、账号页“邀请注册”浮窗和待审核列表仍属于阶段 7，当前不能因为后端路由存在就开启公开注册。
- 客户、门店、会话、派单、工单、知识库、WebSocket、回调、Outbox、文件和向量检索尚未全部完成租户隔离，因此生产和共享测试环境必须保持注册开关关闭。

### 阶段 4：租户字段与历史回填

- 按领域分批增加 TenantID。
- 建立旧 Company 拆分和账号归属回填。
- 建立组合唯一索引。
- 生成一致性检查命令，不把临时结果提交到 `docs/generated/`。

首批实现状态（2026-07-14）：

- 已完成客服组织批次：`AgentProfile`、`AgentTeamSquad`、`AgentTeamSquadMember`、`AgentTeamSchedule` 增加 TenantID，并从 `AgentTeam`/`User` 确定性写入和回填。
- 客服组创建必须从当前 `ActiveTenantID` 写入租户；公司主管可以创建和管理本公司客服组，不能管理其他租户客服组。
- 历史数据出现账号与客服组租户冲突、跨租户小组成员或排班小组错配时 migration 中止，不自动改归属。
- 客服分支旧 migration 25/26 已避让 AI 分支并重编号为 37/38，客服组织租户回填使用 39；migration runner 会拒绝同版本不同 remark 的历史记录。
- 阶段 4A 当时只完成字段与回填；客服组织运行时查询和最终写入条件已在阶段 5 首批补齐。其他业务模型、组合唯一索引和一致性检查命令仍按后续阶段分批推进。

第二批实现状态（2026-07-14）：

- `QuickReply` 增加 `TenantID`，创建时只接受认证上下文中的 `ActiveTenantID`；平台管理员未进入公司时不能创建，租户账号不能提交或覆盖租户字段。
- 快捷回复分页、全量可用列表、详情、更新和删除均强制使用当前租户条件；跨租户 ID 对调用方表现为不存在，最终更新和删除 SQL 同样携带 `tenant_id`，不只依赖前置读取。
- migration 40 将历史零租户快捷回复确定性归入 `legacy-default`，保留指向现存租户的显式值；发现缺失租户引用时整笔事务回滚，重复执行不改变已确认归属。
- 快捷回复测试数据固定写入 `legacy-default`，若固定 ID 已属于其他租户则停止，不覆盖其他租户数据。
- 本批没有修改前端 DTO 或页面。统一请求客户端已经发送 `X-Tenant-ID`，现有快捷回复入口直接获得隔离能力。
- `Tag` 暂不单独增加租户字段。标签当前同时关联会话与工单，而这两个父业务域尚无 `TenantID`；提前租户化会制造无法可靠回填和校验的半隔离关系，必须与客户、会话、工单批次一起处理。

第三批实现状态（2026-07-14）：

- `Notification` 增加 `TenantID`，创建时从接收账号确定归属，不接受调用方指定租户。接收账号不存在或已删除时不再创建无法投递的孤儿通知。
- 通知列表、未读统计、单条已读和全部已读同时使用 `recipient_user_id + tenant_id`；租户账号使用固定 `User.TenantID`，平台账号使用 `0`，平台通知不会因切换当前公司而消失。
- migration 41 按接收账号回填历史通知。缺少接收账号或显式租户与账号冲突时整笔回滚，不根据工单/会话 `BizID` 猜测归属。
- WebSocket 仍以已认证用户主题推送，response DTO 和事件 payload 不增加租户字段；通知中的 `ActionURL` 只负责导航，目标工单/会话接口仍必须独立执行租户鉴权。
- 本批没有处理工单、会话本体的租户归属，也不代表通知关联的业务对象已经完成隔离。

第四批实现状态（2026-07-14）：

- 明确区分 `Tenant` 与历史 `Company`：`Tenant` 仍表示接入本系统的租户公司，`Company` 表示租户内部维护的客户企业档案；本批为 `Company` 和运行时接入配置 `Channel` 增加 `TenantID`。
- Company/Channel 后台列表、详情、创建、更新、启停和删除均要求有效 `ActiveTenantID`；跨租户 ID 对调用方表现为不存在，最终更新 SQL 同样包含 `id + tenant_id`。渠道用户密钥重置也执行相同边界。
- Company 的模型设置入口先确认目标客户企业属于当前租户，再调用原有设置 service；没有新增平行页面、权限、DTO、enum、Gin 路由或 WebSocket payload。
- migration 42 将历史零租户 Company/Channel 确定性归入 `legacy-default`，保留指向现存租户的显式值；缺少默认租户或引用不存在租户时整笔事务回滚，重复执行不改变已确认归属。
- `Channel.ChannelID` 继续保持全局唯一，因为公开接入和回调仍按该稳定标识全局反查渠道；`Company.Name` 暂时也保留历史全局唯一索引，后续改为租户组合唯一前必须先审计历史重名并兼容 SQLite/MySQL 索引迁移。
- 本批只完成 Company/Channel 归属和后台管理链路隔离。Customer、Store、WxWorkProtocolInstance、Conversation、Message、回调、Outbox 和 WebSocket 尚未沿 Channel tenant 形成完整链路，不能由本批推定完成。
- AIAgent 尚未租户化，Channel 当前只能验证绑定 Agent 存在且启用，不能验证同租户；历史企业微信客服账号枚举仍依赖平台全局配置，租户级第三方凭据隔离需在渠道设置重构时单独完成。

第五批实现状态（2026-07-14）：

- `Customer`、`CustomerIdentity`、`CustomerContact` 和 `StoreCustomerRelation` 增加 `TenantID`。migration 43 优先从客户企业 Company 和历史 Conversation→Channel 确定 Customer 归属，无来源的孤立历史客户归入 `legacy-default`，三个子表严格继承父客户。
- 同一客户若关联的 Company 与历史会话 Channel 属于不同租户，或子表是孤儿/显式租户与父客户冲突，migration 43 整笔事务回滚；重复执行保留已确认归属。
- 客户与联系方式后台列表、详情、门店关系展示、创建、保存、更新、启停和删除全部要求 `ActiveTenantID`。列表 join 同时约束 Customer、Contact 和 Company 的 tenant，最终写 SQL 使用 `id + tenant_id`。
- 手工创建/保存客户只能绑定当前租户的客户企业；联系方式新建和全量保存只继承父客户租户，不接受 request 指定租户。
- 外部客户创建从真实 Channel 继承租户；同一 `ExternalSource + ExternalID` 可以在不同租户分别形成客户，但同租户内继续复用原客户。客户会话 token 校验、会话所有权检查和企微联系人资料回写均校验 Channel/Customer 同租户。
- `ConversationService.Create*` 不再允许缺失、停用或 `TenantID=0` 的渠道创建无归属客户。小程序无 Channel 的旧独立 Agent 兜底会收到明确错误，必须先配置真实接入渠道；本批没有修改 AI Agent 选择、回复引擎或模型调用。
- Store、WxWorkProtocolInstance、Conversation、Message、Ticket 本体尚未增加 TenantID。门店关系已继承 Customer tenant，但门店/企微展示对象和工单关联仍需在父域租户化后继续校验。

第六批实现状态（2026-07-14）：

- `Store`、`StoreStaffBinding` 和 `WxWorkProtocolInstance` 增加 `TenantID`，先建立门店来源域的共享归属契约；本批不改变企微协议字段、模型调用或回复运行时。
- migration 44 以显式 Tenant、客户企业 Company、接入 Channel、后台 User、客服组 AgentTeam、Customer/StoreCustomerRelation 等已有归属作为交叉证据。所有非零证据必须指向同一 Tenant，不能用来源优先级覆盖冲突。
- 门店先汇总门店员工绑定、企微实例和客户门店关系的归属证据，再回填门店员工绑定，最后回填企微实例；缺失 Store/Company/Channel/User/AgentTeam/Customer 等父对象时整笔事务回滚。
- 没有任何来源证据的历史门店、绑定或未绑定企微实例才归入 `legacy-default`；已有显式 Tenant 必须真实存在，重复执行不改变已确认归属。
- DDL 继续由 AutoMigrate 增加 `bigint not null default 0` 索引字段。migration 44 不新增 DTO、enum、接口、权限或 WebSocket 字段。
- 本批只是 Store/WxWork 的字段和历史回填契约，尚未完成后台 Handler/service/repository、协议回调、设备池、门店知识库和企微运行时的租户强制条件。公开注册仍必须关闭。
- `codex/ai-billing` 同时修改 WxWork 实例模型、repository、handler 和 service。本批有意不触碰后三类运行时代码；合并时必须先保留三处 `TenantID`，再让双方基于共同归属字段继续各自运行时隔离和 AI 能力。

### 阶段 5：业务服务强制隔离

- 用户、角色分配和客服组织。
- 客户、客户企业、门店和企微员工号。
- 会话、消息、派单、工单。
- 知识库、快捷回复、标签和 AI Agent。
- 详情、更新、删除和跨实体关联全部校验租户。

不能只完成列表过滤就认为隔离完成。

首批实现状态（2026-07-14）：

- 客服组、客服档案、客服小组/成员和排班的后台列表、全量选项、详情与排班日历均强制要求当前公司，并在 repository 查询中附加 `tenant_id`。
- 更新、删除和小组成员替换先按 `id + tenant_id` 读取，最终写 SQL 同样使用 `id + tenant_id`；直接提交其他公司 ID 对调用方表现为对象不存在，不能触达目标记录。
- 平台管理员未选择公司时不能读取或管理客服组织；公司主管、客服组长和客服继续在当前租户内叠加原有角色/客服组范围，不以租户过滤替代权限判断。
- 客服档案输出 builder 已恢复为纯映射。关联用户、客服组、客服负载和排班展示名称只从当前租户批量或定向加载，损坏的跨租户关联不会补出另一公司的名称。
- 门店员工无论分配到客服组还是解除分配，都先校验账号与原/目标客服组属于当前租户；客服组范围同步的最终写入也携带租户条件。
- 双租户测试覆盖列表、详情、日历、客服负载、跨租户 ID 更新/删除、小组成员污染、门店员工解除归属和 repository 最终写入条件。
- 本批没有给 Conversation/Message/Ticket/StoreStaffBinding 等其他业务表增加 TenantID。派单任务列表、全局待回复统计、会话状态机和非 HTTP 链路仍必须在对应批次独立验收，不能由客服组织隔离推定完成。
- `AgentProfile.AgentCode` 仍使用历史全局唯一索引；这不构成越权，但会限制不同租户复用相同客服工号。组合唯一索引调整需要单独评估历史数据和 SQLite/MySQL 索引迁移，未夹带到本批运行时修复。

第二批实现状态（2026-07-14）：

- Company/Channel 后台 Handler 在原动作权限后增加当前租户要求，平台管理员未进入公司时不能读取或管理客户企业与渠道配置。
- Company/Channel service 提供当前租户分页和详情，并在创建时只继承 `AuthPrincipal.ActiveTenantID`；request/response 不开放可伪造的租户字段。
- Company 更新/启停/删除以及 Channel 更新/启停/删除/密钥重置的最终 repository SQL 均携带 `tenant_id`，不是只做列表过滤或前置读取。
- 双租户测试覆盖创建继承、列表/详情隔离、跨租户 ID 篡改、无当前租户拒绝、最终写条件和 migration 回滚。
- Customer service 仍可通过历史全局 Company 读取建立关联，Channel 之后的会话/消息链路也尚未租户化；这些跨域入口必须在父子归属可确定后继续收口。

第三批实现状态（2026-07-14）：

- 客户和联系方式后台 Handler 在原权限后要求当前公司；平台管理员未进入公司时不能读取客户或联系方式。
- Customer service 的分页、详情、客户企业绑定、保存、更新、启停和删除使用当前租户，CustomerContact service 的列表、创建、复活软删行、更新、主联系方式切换和删除使用父客户租户。
- 外部会话创建按 Channel tenant 查找/创建 CustomerIdentity；客户 session 验证和会话所有权不能用另一租户的同名外部身份通过。
- 双租户测试覆盖同外部 ID 分租户、后台 CRUD、跨租户客户企业、联系方式 ID 注入、门店关系租户继承、repository 最终写条件和无当前租户拒绝。
- Ticket 与派单/会话后台本体尚未租户化，它们对 Customer 的全局运行时读取仍需在对应批次改为父业务对象的 tenant 条件；不能由客户页隔离推定工单或会话隔离完成。

第四批实现状态（2026-07-14）：

- 用户管理列表、全量选项和详情现在要求当前公司；门店员工客服组筛选子查询和门店员工归属展示同时使用 `StoreStaffBinding.TenantID`，平台管理员未进入公司时直接拒绝。
- 客服组新增/编辑时，无论使用门店员工账号 ID 还是兼容旧企微实例 ID，都只解析当前租户的 User/StoreStaffBinding/WxWorkProtocolInstance；分配、转组、解绑和客服组范围同步的最终 Binding/WxWork 更新 SQL 同时携带 tenant。
- 公司主管 `tenant_admin` 在 `ManagedDataScope` 中是“当前租户内不受客服组范围限制”，不再被误当成无任何数据；超级管理员和平台管理员的 unrestricted 语义也先叠加当前 Tenant 过滤，不代表全平台可见。
- Store、StoreStaffBinding、WxWorkProtocolInstance 的展示关联只从同租户 Company/AgentTeam/Store/Instance 读取，损坏的跨租户绑定不会补出另一公司的名称或企微账号。
- 企微员工号列表使用现有 `ApplyWxWorkInstanceFilter` 时先按 Tenant 过滤；会话范围在客服组过滤前先按 Conversation.Channel→Channel.TenantID 限定当前公司，详情可见性执行同一前置条件。
- migration 37/38 发生在 migration 44 之前，仍保留 nil-operator 的历史全局回填路径；运行时 operator 非空时才强制 tenant，专项测试验证零租户历史数据仍可按原顺序升级。
- 本批没有修改企微实例创建/编辑/登录 handler/service、协议回调、设备池、AI 回复或计费。WxWork 全动作与非 HTTP 链路仍需下一批，Conversation/Message/派单本体也仍需独立 TenantID 和双租户测试。

### 阶段 6：非 HTTP 链路隔离

- WebSocket。
- 第三方回调。
- Outbox、异步任务和定时任务。
- 文件和缓存。
- Qdrant payload/filter。

AI 和向量检索部分先与 `codex/ai-billing` 确认共享契约和合并顺序。

### 阶段 7：页面与导航重构

- 接入公司页面和公司选择器。
- 平台管理与当前公司导航。
- 账号邀请注册浮窗和审核列表。
- 角色、权限页面按动作权限隐藏选项。
- 客户企业重命名和渠道设置迁移。

### 阶段 8：灰度启用

- 默认关闭多租户入口。
- 完成历史回填、双租户安全测试和数据一致性检查。
- 再启用公司切换和公开注册。
- 观察越权拒绝、注册失败、回调和 Outbox 指标。

## 16. 测试方案

### 16.1 权限测试

- 账号不能直接获得 `UserPermission` 例外权限。
- 公司主管能分配下级租户角色，不能分配同级或平台角色。
- 管理员不能修改超级管理员角色。
- 无 `role.update` 时不能调用角色排序接口。
- 无动作权限时前端不显示操作，手工请求仍被后端拒绝。
- 拥有 `view` 权限但没有编辑权限时，页面和必要信息仍正常显示。

### 16.2 租户隔离测试

建立租户 A、B，并使用相同名称的客户、客服组和知识库验证：

- A 的账号列表、详情、更新和删除不能触达 B。
- A 不能给 B 的客服分配会话、工单、门店或客服组。
- A 不能绑定 B 的知识库、渠道和企微员工号。
- 直接修改 URL、body ID、query ID 和请求头都不能越权。
- 超管切换 A/B 后只看到当前租户数据。
- 两个浏览器标签选择不同公司时互不覆盖。

### 16.3 注册测试

- 有效邀请码创建待审核账号并绑定正确租户。
- 无效、过期、已重置和停用公司邀请码不能注册。
- 注册请求中的 tenantId、roleIds 被拒绝或忽略。
- 重复提交只创建一个账号。
- 未审核账号不能访问业务后台。
- 审核和角色分配后账号权限正确生效。

### 16.4 运行链路测试

- WebSocket 不向其他租户推送事件。
- 第三方回调从 Channel/企微实例正确反查租户。
- Outbox worker 不跨租户读取或投递。
- 知识检索同时按 tenantId 和 knowledgeBaseId 过滤。
- 文件 URL 和缓存 key 不跨租户复用。

### 16.5 工程验证

每个阶段至少执行：

```bash
gofmt -w <changed-go-files>
go test ./internal/services ./internal/handlers/dashboard ./internal/migration
pnpm --dir web typecheck
pnpm --dir web lint
```

涉及页面时使用浏览器验证桌面和移动端；涉及 SQLite/MySQL 约束时分别运行数据库测试。

## 17. 验收标准

只有以下条件全部满足，才允许宣布多租户功能完成：

- 接入公司可创建、编辑、启停并生成公司主管账号。
- 公司主管角色在角色管理和权限管理中完整可见。
- 公司主管可管理本租户业务和账号，但不能跨租户或配置平台权限。
- 邀请码可查看、复制、重置，邀请链接可完成注册。
- 邀请注册账号自动归属正确租户，默认无业务角色并等待审核。
- 账号页面只能分配角色，不能直接分配权限。
- 所有业务权限在权限管理可见，没有角色 URL 白名单或账号例外权限。
- 页面和必要信息按查看权限保守显示，操作选项按动作权限隐藏。
- 列表、详情、写操作、导出、WebSocket、回调、任务、向量和文件均通过双租户隔离测试。
- 旧渠道页面被替换，但 Channel 真实消息链路保持可用。
- 历史数据完成租户映射，没有未确认账号被开放到多个租户。
- SQLite、MySQL、Go 测试、前端类型检查和关键浏览器流程通过。

## 18. 回滚边界

- 租户化采用新增表、字段和双写/回填方式，完成验收前不删除旧字段。
- 公司切换和公开注册受功能开关控制，可单独关闭。
- 邀请注册关闭不影响平台管理员后台创建账号。
- 旧 Channel 运行时始终保留，前端迁移可独立回滚。
- `UserPermission` 只有在现有数据完成迁移并验证后才能删除。
- Qdrant 新 payload 上线前保留重建索引能力，避免旧向量缺少 tenantId。

## 19. 并行分支协同

与 `codex/ai-billing` 的高风险共同文件包括：

```text
internal/models/models.go
internal/bootstrap/routes.go
internal/bootstrap/server.go
internal/services/company_service.go
internal/pkg/dto/request/company_request.go
internal/pkg/dto/response/company_response.go
web/lib/api/admin.ts
web/lib/navigation.tsx
web/messages/zh-CN.json
web/messages/en-US.json
```

知识库向量 payload、AIConfig 和公司模型设置还涉及 AI 分支真实逻辑。建议合并顺序：

1. 先提交独立的租户/权限共享契约。
2. 两个活跃分支 rebase 到该契约。
3. 客服分支实现账号、权限、租户业务隔离。
4. AI 分支实现模型、计费和向量检索的租户维度。
5. 最后合并页面导航和公司设置。

禁止整文件覆盖另一分支；每个可提交步骤都要更新 `docs/development/customer-audit-merge-handoff.md`，记录同文件修改、字段语义、验证和建议合并顺序。

## 20. 当前实施检查点：企微员工号后台动作隔离（2026-07-14）

本检查点承接阶段 4F 的 Store/WxWork 归属字段和阶段 5D 的客服组范围，完成企微员工号后台真实操作入口的租户边界，不改变企业微信协议、AI 回复或计费语义。

### 已完成边界

- 企微员工号列表先按 `ActiveTenantID` 限定，再叠加客服组可见范围；详情同时校验实例租户和 `AgentTeamScopeService` 数据范围。
- 手工创建、扫码登录、远程开户链接创建均从认证上下文继承租户。Channel、Company、Store 只能引用当前租户对象，GUID 继续保持协议设备级全局唯一。
- 未识别登录回调只能生成 `tenant_id=0 + pending_binding` 隔离记录；后台认领使用 `id + tenant_id=0` 原子条件，不能覆盖已被其他租户认领的实例。
- 更新、AI 开关、AI 设置和删除先读取当前租户实例，最终 Instance/Store/StoreStaffBinding 写入 SQL 继续携带 tenant 条件。
- 登录二维码、登录校验、恢复、停止、退出、资料同步、企业信息、代理、好友请求、群列表/详情/成员、群同步和邀请成员等全部协议动作，在调用协议 service 前统一校验当前租户和客服组实例范围。
- 门店模型设置入口先验证请求中的 Company、Store、WxWorkInstance 均属于当前租户且彼此不冲突，再进入原模型设置 service；本步骤不修改模型配置、模型调用或计费实现。
- 远程配置 token 作为无登录 bearer capability 使用，但 token 对应实例必须已有非零 TenantID；自动创建或更新的 Store 继承 token 实例租户。

### 仍未完成边界

- `KnowledgeBase` 尚无 TenantID。企微实例的知识库名称和绑定校验仍使用全局 ID；必须在知识库、文件、向量检索共同租户化后才能关闭此缺口。
- `WxWorkProtocolDevicePoolInstance` 仍是平台全局设备池。GUID 全局唯一是当前协议资源约束，但设备池哪些操作仅平台管理员可见、哪些可授权租户认领仍需单独确定权限与数据策略。
- 第三方协议回调不能依赖浏览器租户头，当前按全局 GUID 找实例；未知 GUID 已隔离，但已识别回调之后的 Conversation、Message、RouteState、Assignment、Outbox 和 WebSocket 仍需端到端租户审计。
- `StoreAIModelSetting` 和 AIConfig 仍由 `codex/ai-billing` 负责。本检查点只保护现有 dashboard 入口，不宣称底层模型设置表已完成租户隔离。
- 企微统计仍通过实例 ID 聚合尚无 TenantID 的 Conversation/RouteState；实例访问入口已保护，但会话域完成租户化前不能视为完整防线。
- 公开邀请注册继续保持关闭；上述剩余业务域完成双租户验证前不得启用共享生产租户入口。

### 兼容与合并要求

- 本检查点不增加 model、migration、DTO、enum、Gin 路由、权限点、WebSocket payload 或前端字段。
- `codex/ai-billing` 同时修改企微实例 repository/service/handler。合并必须逐方法保留其欢迎语、意图、FastGPT 和模型设置语义，同时保留本检查点的 TenantID 继承、关联对象校验、最终写入条件和协议动作前置保护。
- migration 20 的历史 CompanyID 回填继续使用全局旧数据路径，不能误改为要求 ActiveTenantID；migration 44 才负责 Store/WxWork 的确定性租户归属。

## 21. 当前实施检查点：会话与工单域归属契约（2026-07-14）

本检查点先建立会话、消息、派单、工单和共享标签的租户字段及历史回填契约，不提前改动 AI 回复、派单状态机、Outbox 消费或工单页面。运行时隔离必须在后续步骤基于该契约完成，不能把字段存在等同于功能已隔离。

### 字段与回填范围

- `Conversation` 以显式 Tenant、Channel、Customer、当前客服组和非平台客服账号作为一致性证据；无任何证据的历史会话才归入 `legacy-default`。
- `Message`、RouteState、SessionSummary、MessageSyncLog、Participant、ReadState、WxWorkKF 映射、ChannelMessageOutbox、Assignment、EventLog 和 ConversationInterrupt 继承父 Conversation，并验证 Message、Store、WxWorkInstance、Channel、Customer、Squad 等非零引用同租户。
- 尚未映射到会话的原始协议 MessageSyncLog，以及尚未绑定会话的纯 AI checkpoint ConversationInterrupt，保留 `tenant_id=0` 作为平台隔离记录；它们不能进入任何租户业务查询。门店群通知 Outbox 使用负数合成 message_id，只有结构化 payload 明确为 `store_room_handoff_notice` 时才允许按父 Conversation 回填。
- `Ticket` 以显式 Tenant、Customer、Conversation 和非平台负责人作为一致性证据；TicketProgress 继承 Ticket。TicketView 从租户账号继承，历史平台账号视图无法还原当时 ActiveTenant 时确定性归入 `legacy-default`。
- `Tag` 同时服务 Conversation 与 Ticket，不能拆成两套平行标签。migration 45 将 ParentID 连通的整棵标签组件作为一个归属单元，汇总显式 Tenant、ConversationTag 和 TicketTag 证据；同一标签树被不同租户使用时中止迁移，要求人工拆分数据，不静默复制或覆盖。
- `StoreCustomerRelation.LastConversationID` 在 Conversation 归属确定后执行同租户校验，防止客户门店关系指向其他租户会话。
- migration 45 对任何缺失父对象、非法显式 Tenant、跨租户父子引用、引用消息或共享标签冲突整笔回滚；重复执行保留已确认归属。

### 有意保留和后置范围

- `TicketNoSequence` 继续作为平台全局日序号分配器，`Ticket.TicketNo` 继续全局唯一；它不承载租户业务内容，因此本批不改组合唯一索引和并发分配算法。
- `WxWorkKFSyncState` 只有 OpenKfID、没有可靠 ChannelID，不能凭字符串猜租户，留到第三方渠道凭据和非 HTTP 同步批次。
- KnowledgeCandidate/KnowledgeRetrieveLog 属于知识域，SkillRunLog/AgentRunLog 属于 AI 审计域，必须在各自负责人确认写入与查询语义后继承 Conversation tenant。
- `codex/ai-billing` 新增的 `AIManualResumeTask` 当前不在本分支模型中。合并时必须新增 TenantID 并安排独立可重跑回填；已经执行过 migration 45 的数据库不能依赖重新执行 45 补这张后引入的表。
- Asset、文件、向量 payload、WebSocket topic、回调、Outbox worker 和定时任务仍按阶段 6 分批完成。

### 合并与运行要求

- 本检查点增加 19 个 model 字段和 migration 45，没有 request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端变化。
- `codex/ai-billing` 同时修改 `models.go` 及 ConversationRouteState 相关运行时。合并必须逐结构保留 TenantID、AIManualResumeTask、欢迎语/意图/FastGPT 字段，禁止整文件选边。
- 下一运行时批次必须让每个创建入口从父 Conversation/Ticket 或 ActiveTenant 写 TenantID，并让列表、详情、更新、状态流转、标签替换、派单、工单号使用方及最终 SQL 同时携带 tenant；迁移通过不能替代这些测试。

## 22. 当前实施检查点：会话、消息与派单运行时隔离（2026-07-14）

本检查点在阶段 4G 的归属字段和 migration 45 之上，完成会话、消息、派单及企微协议投递的主要运行时租户边界。它不改变 AI 回复、检索、模型供应商、token 或计费语义，也不把字段回填等同于后续工单、标签和实时通道已经隔离。

### 已完成运行时边界

- Conversation 创建从 Channel 继承 TenantID；未关闭会话复用同时按 CustomerID 和 TenantID 限定，不能复用其他租户会话。
- Dashboard 会话列表、详情、消息列表以及客服发送、撤回、分配、转派、释放、关闭均要求认证上下文中的 ActiveTenantID，并在 service 和最终 repository 写入条件中校验 tenant。
- ConversationParticipant、Message、ConversationRouteState、ConversationReadState、ConversationAssignment、ConversationEventLog、ConversationInterrupt、MessageSyncLog、WxWorkKF 映射及 ChannelMessageOutbox 从父 Conversation 继承 TenantID。
- 派单排班、小组、客服档案、启用用户、实时负载和任务计数均限定当前租户；派单返回的用户、客服组、门店和企微实例展示信息也只从同一租户补充。
- 企微协议的 Conversation、Message、Channel、Mapping、Instance 和 Outbox 关系执行同租户校验；Outbox 领取、成功、失败和媒体状态更新均携带 tenant 条件。门店群负数合成消息任务继续按结构化 `store_room_handoff_notice` 语义工作。
- 尚未关联 Conversation 的原始 MessageSyncLog，以及只有 checkpoint、尚未绑定 Conversation 的 ConversationInterrupt，继续以 `tenant_id=0` 隔离；checkpoint 转成待处理 Interrupt 时继承 Conversation tenant，跨租户复用 checkpoint ID 会被拒绝。

### 兼容边界与后续缺口

- `ConversationRouteService.GetByConversationID` 和 `MessageService.FindLatestByConversationID` 有意保留内部全局 ID 兼容语义。AI runtime 和部分纯单元测试会构造缺少完整 Conversation 行的局部 fixture；租户敏感的 HTTP、派单和写入路径必须继续显式调用 tenant-aware 方法，不能在本分支全局改写这两个 helper。
- Ticket、TicketProgress、TicketView、Tag、ConversationTag 和 TicketTag 只有归属字段及历史回填，运行时列表、详情、状态流转、标签替换和最终写入隔离仍是下一批次。
- WebSocket topic、连接订阅和广播 payload 尚未完成租户审计；不能仅依赖会话详情接口阻止跨租户实时事件泄露。
- KnowledgeCandidate、KnowledgeRetrieveLog、SkillRunLog、AgentRunLog、文件/Asset 和向量 payload 仍由各自领域后续处理。
- `internal/services/media_understanding_service.go` 仍按 message ID 更新结果；该文件属于 AI 分支改动范围，合并后必须由 Message 解析 TenantID 并执行 tenant-qualified 更新。
- AI 分支必须让 ConversationSessionSummary 创建继承 TenantID，并为 AIManualResumeTask 增加 TenantID；后者应使用 migration 45 之后的新 migration 回填，不能修改已经发布的 migration 45。
- 公开邀请注册继续关闭。Ticket/Tag、WebSocket、知识库/向量和 AI 异步任务完成双租户验证前，不能宣称端到端生产隔离完成。

### 验证与合并要求

```text
go test ./internal/services -count=1
go test -race ./internal/services -run 'TestConversation(Runtime|DispatchAndFinalWrites|InterruptRejects)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- 新增 `internal/services/conversation_runtime_tenant_test.go`，覆盖跨租户读写拒绝、子记录 TenantID 继承、派单候选隔离、最终 SQL 条件、独立同步日志隔离及 checkpoint 升级/冲突。
- 本检查点没有 model、migration、request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端变化。
- `codex/ai-billing@f2d2da4` 与本检查点存在 Conversation builder/handler、RouteState、Message、人工派单、超时和企微协议等同文件修改。合并必须逐方法保留 AI 分支的欢迎语、意图、FastGPT、人工恢复和媒体理解语义，同时保留本检查点的 ActiveTenant 校验、TenantID 继承及最终 tenant 写入条件。

## 23. 当前实施检查点：工单与共享标签运行时隔离（2026-07-14）

本检查点复用现有工单、标签和会话标签页面及权限，不新增平行工单或派单模型。工单继续表示需要持续跟进的服务事项，派单继续表示当前客户会话由谁回复；两者只通过 Conversation/Customer 关联，不合并状态机。

### 已完成运行时边界

- 工单列表、汇总、详情、进展和个人保存视图要求 ActiveTenantID；列表聚合补充的 Tag、User、Customer 也只读取当前租户。
- 手工创建工单从 ActiveTenantID 继承 TenantID，并验证 Customer、Conversation、Assignee 和 Tag 均属于同一租户。AI Graph 和企微默认资源通过已有 Conversation 创建工单时，系统身份从父 Conversation 继承 tenant，不需要伪造浏览器租户头。
- 更新、关联客户、指派、状态变更和新增进展先读取当前租户 Ticket；Ticket、TicketProgress、TicketTag 和 TicketView 的最终写入/删除携带 tenant 条件。
- TicketProgress、TicketTag 和 TicketView 创建写入父 Ticket 或 ActiveTenant 的 TenantID；共享 Tag 不拆成工单标签和会话标签两套模型。
- Tag 列表、树、详情、父标签、同级重名、排序、状态和删除均限定当前租户。ConversationTag 新增/删除同时校验 Conversation、Tag 和 relation tenant。
- 工单与会话标签筛选子查询同时携带 tenant。会话筛选原来引用不存在的历史表名 `conversation_tag_rels`，现已改为当前 GORM 表 `t_conversation_tag`。

### 权限、兼容与后续缺口

- `/api/dashboard/tag/update_sort` 原先未做权限校验，现复用权限管理中已有的 `tag.update` 权限；没有新增隐藏权限或角色内硬编码授权。
- TicketNoSequence 和 TicketNo 继续平台全局唯一，符合阶段 4G 决策；本步骤不改变工单号格式或并发分配算法。
- TicketService 的全局 ID 只读 helper 继续供内部 TicketCreated/TicketAssigned 事件消费。所有用户输入可达的 Dashboard 读写路径均使用 tenant-aware 方法。
- 工单创建/指派的站内通知会从接收账号继承 tenant；企微通知的 `defaultToUsers` 仍来自平台全局配置，需在通知域租户化时确定是平台运维告警还是租户主管通知，当前不擅自改变收件语义。
- WebSocket notification topic、连接订阅和 ActionURL 打开后的二次鉴权仍属于实时通道审计；本步骤依靠工单详情接口阻止跨租户 URL 读取，但不宣称 WebSocket 已端到端隔离。

### 契约与验证

- 没有 model、migration、request/response DTO、enum、Gin 路由、权限常量、WebSocket payload 或前端文件变化；只收紧现有 handler/service/repository 调用。
- 新增 `internal/services/ticket_tag_tenant_test.go`，覆盖双租户列表/聚合、详情、进展、创建引用、更新、指派、状态、视图、标签树、会话标签和最终写入，并覆盖 AI/企微系统建单继承父会话 tenant。

```text
go test ./internal/services ./internal/handlers/dashboard -count=1
go test -race ./internal/services -run 'Test(TicketAndTagRuntimeTenantIsolation|SystemTicketCreationInheritsConversationTenant)' -count=1
go test ./... -run '^$' -count=1
go vet ./...
git diff --check
```

- `codex/ai-billing@f2d2da4` 没有修改 Ticket/Tag 文件；双方在 `internal/handlers/dashboard/conversation_handler.go` 有同文件改动。合并时保留 AI 分支的回复入口变化和本步骤的标签筛选 tenant/表名修复，禁止整文件覆盖。

## 24. 当前实施检查点：WebSocket 与公司切换实时隔离（2026-07-14）

本检查点复用现有 Dashboard、通知和开放 IM WebSocket，不新增平行实时通道或隐藏权限。

### 已完成边界

- Dashboard 会话 WebSocket 要求已有 `conversation.view` 权限和正数 `ActiveTenantID`；浏览器连接通过 `tenantId` query 传当前公司，后端只在 `/api/ws/*` 读取该 query，普通 HTTP 请求仍只接受租户 Header。
- `ClientSession` 固化连接建立时的 TenantID；原全局 `admin:all` 改为 `admin:tenant:{tenantId}`，未分配会话只向所属公司广播。
- Dashboard 手工订阅 `conversation:{id}` 时复用 `AgentTeamScopeService.CanViewConversation`，同时校验连接 Tenant 与当前认证 Tenant；访客手工订阅还必须满足连接 Tenant、Conversation Tenant 和外部身份所有权三者一致。
- 访客默认 topic 改为 `guest:{tenantId}:{externalId}`，相同外部 ID 在不同公司不会共享在线状态或实时消息。
- Conversation 实时路由展示按 Conversation Tenant 读取 RouteState、Store 和 WxWorkInstance；客户在线状态也按 tenant + externalId 判定。
- 会话页切换公司时清空 Zustand 会话、消息、筛选和企微员工号缓存，并通过请求序号阻止旧公司异步响应回写；会话和通知 WebSocket 随当前公司变化断开重连。

### 权限、兼容与后续缺口

- 导航继续按权限点过滤；本批没有角色 URL 白名单。概览页保留为所有已登录角色可见的必要信息页，具体业务入口仍由 `*.view` 权限控制。
- 站内通知 topic 仍按接收 UserID；User 固定归属单一 Tenant，通知记录从接收账号继承 Tenant，因此不会形成跨租户订阅。平台账号通知属于 tenant 0 平台域。
- 客户会话本地缓存虽然使用 `guest:{externalId}` identity key，但缓存复用同时校验全局唯一 ChannelID；服务端 CustomerIdentity 查询仍按 Channel 所属 Tenant 限定，因此不新增重复 tenant key。
- Asset/文件仍未租户化；企业微信客服号 KF Outbox 仍存在全局 helper；Knowledge/向量和 AI 日志继续由后续共享契约处理。公开邀请注册保持关闭。

### 契约与合并要求

- 本批没有 model、migration、request/response DTO、enum、Gin 路由或新权限点；WebSocket 事件 payload 结构不变，只改变服务端 topic 组成和订阅授权。
- `codex/ai-billing@f2d2da4` 同时修改 `internal/services/ws_service.go`、`internal/builders/conversation_builder.go` 和 `web/lib/api/admin.ts`。合并必须保留 AI 分支的自动转人工/恢复展示及新增 AI API，同时保留本批 tenant topic、订阅校验和 WebSocket URL tenant 参数，禁止整文件覆盖。

## 25. 当前实施检查点：Asset 归属与消息附件隔离（2026-07-14）

本检查点将现有 Asset 作为租户文件主体继续复用，不新增会话附件表或平行文件入口。

### 归属与回填

- `Asset` 增加 TenantID，DDL 由 AutoMigrate 创建；migration 46 只做 DML 回填、一致性检查和冲突回滚。
- 历史归属证据只接受 Asset 显式 Tenant、非平台 CreateUser、已租户化媒体 Message payload 中的 `assetId`，以及 HTML 消息的 `data-asset-id`。文件名、URL、StorageKey 和目录名不能作为租户事实。
- 同一 Asset 被不同 Tenant 的 Message 引用、显式 Tenant 与上传账号/Message 冲突、Asset 引用不存在的创建账号、Message 引用不存在 Asset 或引用消息缺少 Tenant 时，migration 整笔回滚；存在但 TenantID 为 0 的平台账号不作为归属证据，无任何证据的孤立文件归 `legacy-default`，重复执行幂等。
- 新租户文件的对象路径增加 `tenants/{tenantId}` 前缀；平台兼容资源使用 `platform` 前缀。全局 OSS 前缀继续位于最外层，不改变现有存储配置语义。

### 运行时隔离

- Dashboard Asset 列表、详情、上传和删除要求 ActiveTenantID；读取与最终更新 SQL 均携带 tenant。
- 客户侧和客服侧上传从已鉴权 Conversation/ActiveTenant 继承 tenant；企微员工号协议和企业微信客服号入站媒体从 Instance/Conversation 继承 tenant。
- 普通图片、语音、视频、附件和 GIF 在写入 Message 前必须按 Conversation Tenant 读取 Asset；HTML 图片同时校验 tenant、AssetID、Provider 和 StorageKey。
- 企微协议/KF 出站只读取 Message Tenant 下的 Asset。媒体理解异步任务按 Message Tenant 读取 Asset、RouteState、WxWorkInstance、最新追问和 Conversation，并用 `message_id + tenant_id` 更新 payload。
- Message 响应只有在同租户下才可通过 AssetID 补齐缺失元数据；已通过会话鉴权的 payload 仍可生成现有本地/OSS 展示 URL。

### 尚未完成边界

- `/api/asset/file/{assetId}` 以及本地存储静态 URL 继续承担客户和企微下载兼容，当前 AssetID 是不可预测 bearer 标识，但不是完整访问授权。后续必须统一设计短期签名、下载用途与本地/OSS 两种 provider 的一致校验，完成前不能宣称“文件 URL 已端到端隔离”。
- KnowledgeBase/KnowledgeDocument、向量 payload、KnowledgeCandidate/RetrieveLog 仍未租户化；知识文档中的文件引用需在知识域共享契约中复用 Asset Tenant，不能建立第二套文件归属。
- AI 分支新增的欢迎图 Asset 字段、AIManualResumeTask 和 usage 日志不在本分支模型中。合并时欢迎图必须验证 Store/WxWorkInstance 与 Asset 同租户，AIManualResumeTask/日志必须继承 Conversation tenant。

### 合并要求

- 本批增加 `Asset.TenantID` 和 migration 46；没有 request/response DTO、enum、Gin 路由、权限点或前端字段变化。
- `codex/ai-billing@f2d2da4` 同时修改 `models.go`、Message、media understanding、企微协议和 Conversation handler。合并必须保留 AI 分支模型调用/usage/计费逻辑，并保留本批的 TenantID、tenant-qualified Asset/Message/Route/Instance 查询与最终更新；禁止整文件覆盖。
