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
- `/register` 页面、账号页“邀请注册”浮窗和待审核列表已在阶段 7B 完成；但正式配置继续关闭公开注册，页面存在不能替代全域隔离与上线验收。
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
- 阶段 4A 当时只完成字段与回填；客服组织运行时查询和最终写入条件已在阶段 5 首批补齐。其他业务模型已按后续批次推进，租户一致性只读审计命令在第 58 批完成；已确认的 Company.Name、Store.StoreCode、AgentProfile.AgentCode 三项租户组合唯一索引在第 59 批完成。

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
- `Channel.ChannelID` 继续保持全局唯一，因为公开接入和回调仍按该稳定标识全局反查渠道；本步骤当时保留的 `Company.Name` 历史全局唯一索引已在第 59 批完成双数据库审计并改为 `tenant_id + name` 组合唯一。
- 本批只完成 Company/Channel 归属和后台管理链路隔离。Customer、Store、WxWorkProtocolInstance、Conversation、Message、回调、Outbox 和 WebSocket 尚未沿 Channel tenant 形成完整链路，不能由本批推定完成。
- AIAgent 在阶段 33 已完成租户化，Channel 创建/更新会验证 Agent 与当前公司一致；租户级第三方凭据隔离仍需在渠道设置重构时单独完成。

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
- 本步骤当时保留的 `AgentProfile.AgentCode` 历史全局唯一索引已在第 59 批改为 `tenant_id + agent_code`；不同租户可复用工号，同租户重复继续由 service 和数据库双重拒绝。

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

## 26. 当前实施检查点：企业微信 KF/CLI 游标与投递隔离（2026-07-14）

本检查点只收紧现有企业微信客服号回调、外部渠道 Outbox 和 CLI bridge，不新增渠道入口，也不把企业微信客服号、CLI 与员工号协议合并成同一协议。

### 归属与回填

- `WxWorkKFSyncState` 增加 TenantID，继续保持 `openKfID` 平台全局唯一；DDL 由 AutoMigrate 创建，migration 47 只做 DML 回填。
- 同步游标的唯一归属证据是配置中 `openKfId` 相同的 `wxwork_kf` Channel Tenant。显式 Tenant 与 Channel 冲突、同一 openKfId 映射到不同 Tenant、Channel Tenant 无效或游标找不到 Channel 证据时，migration 整笔回滚；不把孤立游标猜测为 legacy。
- `WxWorkKFConversation`、`WxWorkKFMessageRef` 和 `ChannelMessageOutbox` 已在 migration 45 从父 Conversation/Message 回填，本检查点不新增重复 migration，只修运行时读取和最终更新条件。

### 运行时边界

- KF callback 先由全局唯一 openKfId 解析 Channel 和 Tenant，再用该 Tenant 读取/保存 SyncMsg 游标；每条同步消息创建会话前再次校验其 Channel Tenant 与回调批次一致。
- KF mapping、message ref、失败回调和 outbox 均按 Conversation/Outbox Tenant 读取；任务 claim 使用 `id + tenant_id + pending/failed` 原子条件，最终 sent/failed 更新也携带 tenant。
- KF 全局 worker 可以扫描多个租户的待投递任务，但每条任务只用自身 TenantID 读取 Message、Conversation、Mapping、Channel 和 Asset。缺失 Tenant 的任务不会被处理。
- 企微员工号协议仅对共用的 mapping/message ref 表增加 instance tenant 条件，协议字段和 `conversation_id` 语义不变。
- `/api/third/wxwork-cli/*` 继续使用独立 CLI bridge token。token 命中的 Channel Tenant 限制入站幂等、会话映射、outbox poll、sent/failed 回写；一个公司的 CLI poll 不会 claim 其他公司的任务。
- `ChannelMessageOutboxService.Create` 从父 Conversation 继承 Tenant，并校验正数 Message 与 Conversation 同租户；合成门店群通知继续允许负数 MessageID。

### 保留边界

- KF 下行 `DispatchPendingOutbox` 当前没有注册到 cron；本检查点没有擅自启用这项未运行能力。员工号协议 outbox cron 保持原状。
- 旧全局 CRUD helper 为生成代码和内部兼容保留，但真实 KF/CLI/员工号运行链不再调用它们完成业务动作。
- 本批没有 request/response DTO、enum、Gin 路由、权限点、WebSocket payload 或前端变化；现有 CLI/KF 路由及权限语义不变。
- 下一步仍是 Knowledge/向量与 AI 日志共享契约；公开邀请注册继续关闭。

### 合并要求

- 本批新增 `WxWorkKFSyncState.TenantID` 和 migration 47。`origin/codex/ai-billing@f2d2da4` 同时修改 `models.go`、media understanding、Message 测试和企微协议 service/test；合并必须逐方法保留 AI usage/回复逻辑与本批 tenant-qualified ref/voice 查询，禁止整文件覆盖。

## 27. 当前实施检查点：知识域、首页指标与本地向量隔离（2026-07-14）

本检查点复用现有知识库页面、权限和 RAG 结构，不新增平行知识入口，不改变模型选择、回答策略、FastGPT 请求或计费口径。

### 归属契约与回填

- `KnowledgeBase`、`KnowledgeDocument`、`KnowledgeFAQ`、`KnowledgeChunk`、`KnowledgeCandidate`、`KnowledgeRetrieveLog`、`KnowledgeRetrieveHit`、`KnowledgeFeedback` 增加 TenantID；DDL 仍由 AutoMigrate 创建，migration 48 只负责 DML 回填和一致性检查。
- KnowledgeBase 只接受显式 Tenant、Store、WxWorkProtocolInstance、ConversationRouteState 和非平台创建账号作为归属证据。同一知识库出现跨租户证据、引用缺失或显式 Tenant 冲突时整笔回滚；完全无引用的历史知识库才归 `legacy-default`。
- Document、FAQ 和 Chunk 必须继承 KnowledgeBase；Chunk 的 Document/FAQ 必须与其 KnowledgeBase 同租户且指向同一知识库。Candidate 交叉核对 KnowledgeBase、Store、Conversation 和非平台操作账号；RetrieveLog 交叉核对 KnowledgeBase 与 Conversation；Hit/Feedback 继承 RetrieveLog 并继续校验命中知识实体。
- migration 48 重复执行幂等。由于旧 Qdrant point 不含 `tenant_id`，迁移会把未删除 Document/FAQ 的索引状态重置为 pending；部署后必须按公司重建知识库索引。

### 后台和运行时边界

- 知识库、文档、FAQ、候选问答和检索日志的列表先按 ActiveTenantID，再叠加客服组服务范围；详情、创建、更新、删除、排序、索引重建、调试检索、批量审核、导出和企微员工号知识库绑定均校验当前公司。
- 管理员的 unrestricted 语义仅表示在当前公司内不受客服组范围限制，不再等于读取全平台知识数据。
- Candidate 自动生成从 Conversation、Store、KnowledgeBase 三方解析 Tenant，冲突时拒绝写入；相似候选合并、审核、导出状态更新和导出目录均携带 Tenant。
- RetrieveLog/Hit 从操作人、Conversation、KnowledgeBase 和命中结果解析同一 Tenant；后台检索日志详情只加载同租户 Hit。
- 文档/FAQ 索引切片继承 KnowledgeBase Tenant，切片替换、状态写回、索引删除和整库重建的关系库 SQL 均携带 Tenant。
- Qdrant payload 增加 `tenant_id`；本地向量检索强制使用 `tenant_id + knowledge_base_id`，未提供 Tenant 时构造不可命中的 fail-closed filter；关系库 hydrate 再按 Tenant 读取 Chunk、Document 和 FAQ，多租户知识库 ID 混合检索直接报错。
- 首页总览从当前公司统计 Conversation、AgentProfile、AgentTeam、Schedule、Channel 和 KnowledgeRetrieveLog；AI Agent 暂按当前公司 Channel 引用统计，SkillRunLog 暂按关联 Conversation Tenant 统计。

### 保留边界与上线要求

- 本批没有新增 request/response JSON 字段、enum、Gin 路由、WebSocket payload、页面入口或权限点；既有知识库权限继续由权限管理和角色绑定分配。
- `codex/ai-billing` 新增的 KnowledgeBase CompanyID/StoreID/FastGPT 字段必须与 TenantID 并存；FastGPTDatasetJob、KnowledgeResourceGroup/Item、AI usage/run log 和 AIManualResumeTask 尚未出现在本分支，合并后必须从 KnowledgeBase/Conversation/Asset 继承 Tenant，不能依靠 CompanyID 代替租户根。
- FastGPT 远端检索必须在 AI 分支中继续校验 KnowledgeBase Tenant 与会话 Tenant；本批没有猜测远端协议字段，也没有改变 FastGPT dataset 或模型/计费语义。
- AIAgent 已在阶段 33 成为 Tenant 主体；AIConfig 继续是平台级模型配置，SkillRunLog/AgentRunLog 等 AI 分支新增主体仍需按其真实运行链补齐 Tenant 契约。当前企微员工号运行时继续校验 Instance、KnowledgeBase 与会话同租户。
- 历史 Qdrant point 在重建前不会被新 Tenant filter 命中，这是预期的故障关闭。不得为恢复命中临时移除 Tenant filter；应执行知识库重建。
- `/api/asset/file/{assetId}` 的短期签名授权仍未完成。知识/向量和文件 URL 均完成、AI 分支剩余主体合并并通过双租户测试前，公开邀请注册继续关闭。

### 并行合并要求

- migration 48 创建前已核对：`origin/main` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支最高 47，无版本冲突。
- AI 分支同文件主要为 `models.go`、`knowledge_base_service.go`、`knowledge_builder.go`、`knowledge_retrieve_log_repository.go`、`rag/answer.go`、`rag/retrieve.go`、`rag/retrieve_log.go` 和知识页面 API。建议先合并 TenantID/migration 48/repository 原语，再合并 FastGPT 模型字段与资源模型，最后逐方法合并 retrieve/log/runtime；RetrieveLog repository 必须同时保留 AI 分支的近期问题查询和本批的 tenant-aware list/detail 原语，近期问题查询在投入租户业务前也必须增加租户参数。
- 合并时必须保留 AI 分支的 FastGPT、intent profile、usage/计费和回复语义，同时保留本批的 TenantID、同租户父子校验、Qdrant tenant payload/filter、后台 ActiveTenant 和首页统计条件；禁止整文件选边。

## 28. 当前实施检查点：文件短期签名与本地存储收口（2026-07-14）

本检查点继续复用 Asset、既有上传接口和 `/api/asset/file/{assetId}`，不建立第二套附件模型或公开文件入口。目标是补齐阶段 25 留下的下载授权缺口，同时保持客户聊天、客服工作台、历史消息和企微私有化 CDN 可用。

### 签名访问契约

- 应用层文件 URL 固定为 `/api/asset/file/{assetId}`，查询参数包含 `v`、`tenantId`、`expires`、`purpose` 和 `signature`。HMAC-SHA256 规范串绑定版本、AssetID、TenantID、过期时间和用途，签名使用 URL-safe Base64。
- 当前用途只允许 `inline` 和 `wxwork_cdn`。浏览器展示、上传响应和客服头像使用 `inline`；企微私有化 CDN 拉取使用 `wxwork_cdn`。未知用途、缺失字段、篡改签名返回 403，过期返回 410。
- 下载 handler 通过签名中的 TenantID 调用 `GetByAssetIDInTenant`，不再全局读取 Asset。删除、失败或跨租户不存在的 Asset 均不返回文件内容。
- 本地与 OSS 均由同一个应用下载入口先完成签名和租户校验，再由服务端读取 provider。外部协议媒体 URL 也必须先经过签名入口，验证后才 302 到原地址，并设置 `Referrer-Policy: no-referrer` 和 `Cache-Control: private, no-store`。
- 签名 URL 仍是有效期内可转交的 bearer capability，不宣称绑定具体浏览器或账号；它解决的是伪造、长期复用和跨租户替换，不替代消息/会话接口本身的查看权限。

### 配置与启用条件

- 新增 `storage.assetURLSigningSecret`、`storage.assetURLTTLSeconds` 和环境变量 `AGENT_DESK_ASSET_URL_SIGNING_SECRET`。默认有效期为 3600 秒。
- 公开注册关闭的兼容环境在未配置独立密钥时，可从 `customerSession.secret` 通过固定领域标签派生资产密钥，避免现有本地环境立刻中断；这不是生产多租户配置。
- 一旦 `tenantRegistration.enabled=true`，启动校验强制要求独立 `storage.assetURLSigningSecret`。不能以客服会话密钥派生兼容模式开启公开租户注册。

### 响应、历史数据与头像兼容

- AssetResponse 结构不变，但 `storageKey` 对外固定为空，`url` 改为应用签名 URL。媒体 Message 在库内仍可保存 provider/StorageKey 供内部投递，构建客户、客服和 WebSocket 响应时会重新按 `Message.TenantID + AssetID` 取 Asset、清除 StorageKey/旧 URL 并签发短期 URL。
- HTML 消息的新编辑器只提交 `data-asset-id`；服务端按 Conversation Tenant 验证并归一化，入库时去除 `src`、provider 和 StorageKey。历史三字段 HTML 继续接受并交叉校验，响应时只输出 `data-asset-id + signed src`。
- 前端不再从裸 AssetID 拼接无签名 URL。本地 `StaticFS` 和 Next.js `/storage/*` 开发代理已移除，配置目录不能绕过应用路由直接下载。
- 客服头像仍复用 `AgentProfile.Avatar` URL 字段，不新增 AvatarAssetID。客服档案、历史消息发送者和 WebSocket 输出会识别旧本地/OSS URL或已过期的应用 URL，按档案 Tenant 重新签发；外部企微/OIDC 头像 URL 保持原样。

### 权限、兼容与后续边界

- 没有新增 model、migration、request/response DTO 字段、enum、Gin 路由、权限点或 WebSocket payload。权限管理不需要新增隐藏权限；生成签名 URL 的前提仍是用户已通过原 Asset、Conversation 或企微运行链读取到同租户业务对象。
- OSS bucket 若配置为 provider 级公开，应用无法撤销通过 OSS 自身 URL 获得的访问；本批已停止向浏览器和消息响应暴露 StorageKey，正式多租户环境仍应使用私有 bucket。应用签名不能替代云存储 ACL。
- FastGPTDatasetJob、KnowledgeResourceGroup/Item、AI usage/run log 和 AIManualResumeTask 仍需与 AI 分支合并后完成租户边界。AIAgent 已在阶段 33 租户化，AIConfig 明确保留为平台级模型配置；文件 URL 已收口不代表 AI 分支新增主体已经隔离，公开注册继续关闭。

### 并行合并要求

- 本批没有 migration，当前本分支最高仍为 48；开始时 `origin/main` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33。
- AI 分支同文件包括 `config.go`、`config.example.yaml`、`server.go`、`conversation_builder.go`、`utils/message.go`、Asset handler/service、企微协议 service 及测试。合并必须保留 AI 分支 Email/FastGPT/NewAPI/usage/回复增强，同时保留本批签名配置、TenantID 取数、响应去 StorageKey、静态目录关闭和企微签名 URL；禁止整文件选边。
- 建议先合并 Asset Tenant/migration 46，再合并本批无 migration 的签名原语和下载路由，最后重放 AI 分支媒体理解、欢迎图、FastGPT 与 usage 测试。AI 分支新增欢迎图或资源表必须保存 AssetID 并在输出时复用本签名入口，不能重新保存 provider 直链。

## 29. 当前实施检查点：平台系统设置权限边界（2026-07-14）

本检查点修正“系统设置页面复用租户业务权限”的历史问题。存储设置和企微设备池继续是平台全局资源，不复制为每租户一套，也不改变 Asset、Channel 或企微员工号实例的租户业务职责。

### 权限与角色契约

- 权限管理新增五个可见平台权限：`storageSetting.view`、`storageSetting.update`、`wxworkDevicePool.view`、`wxworkDevicePool.update`、`wxworkDevicePool.sync`。全部标记为 `platform` scope，并记录真实 Dashboard API 路径。
- migration 49 复用既有 `ensurePermissions/ensureRoles/ensureRolePermissions` 幂等同步权限与内置角色。超级管理员通过完整权限集获得权限，平台管理员默认获得五项权限；公司主管、客服组长、客服和门店员工不获得。
- `asset.view/create/delete` 继续只负责当前租户 Asset；`channel.view/update` 继续只负责当前租户接入渠道和企微员工号。二者不再隐含平台存储密钥、全局目录或设备池管理权。
- 角色服务继续禁止把 platform scope 权限分配给 tenant scope 角色。handler 额外校验 `IsPlatformAccount`，即使异常 token 或历史脏数据把平台权限带给租户账号，也不能访问平台设置。

### 页面与接口职责

- 现有“存储设置”和“企微设备池”页面保留在系统设置导航，不新增重复入口。导航分别按 `storageSetting.view` 与 `wxworkDevicePool.view` 显示。
- 存储查看/修改接口分别要求对应独立权限；设备池列表和设置查看要求 view，修改远程后台配置要求 update，执行同步要求 sync。
- 本批不改变 SystemConfig 数据模型、存储设置 DTO、设备池 DTO、路由或响应；不修改员工号协议字段、登录/消息/`conversation_id` 语义，也不触碰设备池远程 API 请求格式。
- “回复意图配置”仍属于 AI/计费分支正在调整的回复引擎能力。本分支不凭旧文档改其 scope；合并 AI 分支后必须再确定它是平台模板还是租户配置，再赋予对应权限边界。

### 迁移、验证与合并边界

- migration 49 创建前核对 `origin/main` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支最高 48，无版本冲突。该 migration 只做权限和内置角色关系 DML，不涉及 model/AutoMigrate。
- 测试覆盖 migration 重复执行、五项权限均为 platform scope、平台管理员默认绑定、公司主管未绑定，以及租户账号带平台权限、平台账号仅带旧 Asset/Channel 权限均被 handler 拒绝。
- `auth.go`、migration 和 `navigation.tsx` 是并行共享文件。AI 分支合并时保留其新增 AI/计费权限和导航，同时保留本批五项平台权限、migration 49 与 handler 平台账号校验，禁止整文件覆盖。

## 30. 当前实施检查点：邀请注册与账号审核前端闭环（2026-07-14）

本检查点复用现有用户管理页和阶段 3A/3B 的邀请码、注册、审核接口，不新增平行账号后台，不把权限勾选器放回账号层。

### 公开注册入口

- `/api/auth/options` 增加 `tenantRegistrationEnabled`。登录页只有在开关开启时显示注册链接；`/register` 自身也先读取该开关，关闭时只显示不可用状态，不尝试调用未挂载的公开注册路由。
- 注册页从 `/register?invite=...` 读取邀请码，自动校验并只展示后端返回的公司法定名称/简称。表单只提交 username、nickname、mobile、email、password、confirmPassword、invitationCode，不允许提交 Tenant、角色、客服组或门店字段。
- 注册提交按规范化 payload 生成客户端请求指纹；相同内容重试复用同一个 `X-Request-Id`，内容改变后生成新请求标识，兼容后端精确重放和“同标识改内容拒绝”语义。
- 成功页明确账号仍为待审核、禁用且无角色；不自动登录，也不把邀请码、密码或请求指纹写入页面日志。
- 正式配置继续保持 `tenantRegistration.enabled=false`。页面完成不代表公开注册可以上线，仍需等待 AI 分支新增主体完成 Tenant 隔离并通过双租户验收。

### 用户管理页职责

- 现有 `/dashboard/users` 增加“账号 / 注册审核”标签，账号列表继续承载后台创建、资料修改、门店员工客服组归属、角色分配和密码管理；新增列只显示注册来源与审核状态。
- “邀请注册”动作要求现有 `tenantInvite.view`；重置邀请码额外要求 `tenantInvite.rotate`。浮窗展示当前公司、邀请码、绝对注册链接、使用次数和最近使用/重置时间。公开注册关闭时明确提示链接暂不可用，不伪装成可注册。
- “注册审核”标签要求 `tenantRegistration.view`；通过/拒绝动作要求 `tenantRegistration.review`。通过还必须同时具有 `user.assignRole` 与 `role.view`，角色列表只显示后端标记 `assignable=true` 且已启用的角色；拒绝必须填写原因且不提交角色。
- 审核页只分配角色，不直接分配权限。所有权限继续在权限管理中可见，由管理员及以上在角色管理中配置；账号操作者只能赋予可分配角色。
- 页面在桌面和 390px 窄屏下保持无页面级横向溢出；筛选项在窄屏堆叠，宽表格继续使用现有表格容器滚动。

### 共享请求与合并边界

- 公共请求客户端始终发送 `Accept-Language` 和 `X-Locale`，CORS allow headers 同步加入这两个值；否则前后端分端口部署会在预检阶段失败。同源部署行为不变。
- 本批没有 model、AutoMigrate、DML migration、enum、Gin 路由、WebSocket payload 或新权限点。共享 JSON 只增加 AuthOptions 的 `tenantRegistrationEnabled`，其余注册 DTO 沿用阶段 3B。
- `codex/ai-billing@f2d2da4` 同时修改 server、AuthOptions handler/DTO、登录页、auth API 和双语消息。合并必须同时保留 AI 分支邮箱验证/FastGPT/NewAPI 选项与本批注册开关、CORS 语言头和权限显隐，禁止整文件选边。

## 31. 当前实施检查点：公司上下文与平台/公司导航分层（2026-07-14）

本检查点完成阶段 7 的侧边栏公司上下文与导航职责拆分，继续复用 `/dashboard/channels` 作为唯一“接入公司”入口，不新增 `/dashboard/tenants` 平行页面。

### 公司上下文

- 登录与资料响应增加向后兼容的 `activeTenantName`。名称来自认证阶段已经校验为启用状态的 Active Tenant，普通租户账号无需获得 `tenant.view` 或调用平台公司列表，也能明确看到所属公司。
- 侧边栏品牌下固定显示工作上下文。租户账号只读显示所属公司；拥有 `tenant.view + tenant.switch` 的平台账号可以搜索启用公司、进入公司或返回平台管理。
- Active Tenant 的 ID 与名称都保存在标签页级 `sessionStorage`；共享登录会话被另一标签更新时，当前标签仍以自己的公司上下文为准。
- 平台账号未进入公司时，品牌首页指向“接入公司”；进入公司后指向公司总览。切换失败会恢复原公司上下文，不留下前端 ID 与认证资料不一致的状态。
- 平台账号未进入公司却访问公司总览、会话、派单、客户、用户等租户页面时，布局按同一导航上下文契约重定向到“接入公司”，避免先发出缺少 Tenant 的业务请求。

### 导航职责

- 当前公司下分为“当前公司、客服组织、服务能力、账号与权限”；平台能力单独归入“平台管理”。平台账号进入公司后同时保留公司和平台入口，返回平台后收起所有租户业务入口。
- 页面入口继续先按 `*.view` 权限过滤，再按平台/租户上下文过滤；没有恢复角色名称 URL 白名单，也没有新增隐藏权限。
- `AI Agent` 与“企微员工号”页面原本存在真实路由和权限但遗漏侧边栏入口，本批分别按 `aiAgent.view` 和 `channel.view` 恢复。原“公司管理”明确改称“客户企业”，避免与 Tenant 接入公司混淆。
- 角色和权限说明页保留在“账号与权限”，使公司主管拥有 `role.view`/`permission.view` 时仍可查看；用户管理只有存在 Active Tenant 时显示。

### 契约与剩余边界

- 本批没有 model、AutoMigrate、DML migration、enum、Gin 路由、WebSocket payload 或新权限。共享 JSON 只新增 `LoginResponse.activeTenantName`；旧前端忽略，新前端缺失时回退为通用当前公司标签。
- 当前公司“渠道接入”独立设置页仍未迁移，不能把平台“接入公司”页恢复成旧 Channel 管理页。AI Config、回复意图等最终平台/租户归属仍须在合并 AI 分支真实代码后确认。
- 正式公开注册继续等待 AI 分支新增主体 Tenant 化、真实历史数据 migration 39 冲突修复和双租户全链路验收。

## 32. 当前实施检查点：接入公司运营资源摘要（2026-07-14）

本检查点补齐平台管理员在“接入公司”列表判断公司实际使用状态所需的最小信息，不把公司管理页扩展成客服任务工作台，也不恢复旧 Channel 编辑器。

### 统计口径与页面职责

- 每个接入公司显示客服档案数、门店数、综合客服组数和最近活跃时间。三类资源均按 `TenantID` 聚合，排除 `StatusDeleted`；停用但尚未删除的资源仍属于公司存量并计入。
- 最近活跃时间取该公司 `Conversation.LastActiveAt` 最大值与未删除 `User.LastLoginAt` 最大值中的较晚者。已删除账号的历史登录时间不参与，避免删除账号继续抬高公司活跃度。
- 列表对当前分页公司统一执行分组查询，不按公司逐行查询。页面只增加一列紧凑的“资源与活跃”摘要，保留原公司法定身份、主管、联系人、核验、启停和工作上下文切换职责。
- 公司详情响应使用同一统计构建逻辑；新建公司结果在尚无资源活动时返回零值/空时间，不伪造活跃记录。

### 契约与边界

- `TenantResponse` 向后兼容增加 `agentCount`、`storeCount`、`agentTeamCount` 和可选 `lastActiveAt`。没有新增 model、AutoMigrate、DML migration、enum、Gin 路由、权限或 WebSocket payload。
- SQLite 对 `MAX(datetime)` 返回字符串，MySQL `parseTime=True` 通常返回 `time.Time`；repository 统一兼容两种驱动值后再由 service 比较时间，保持 SQLite/MySQL 同一业务口径。
- Repository 单测覆盖 `time.Time`、SQLite string/`[]byte`、空值和非法值；真实 MySQL 容器仍被已知 migration 39 历史组长归属冲突阻断，本批不跳过迁移伪造联调结果。
- 本检查点自身不修改 AI Agent、AI Config、模型调用、回复引擎、token、usage 或计费语义。后续阶段 33 已完成 AIAgent Tenant 契约并关闭跨公司绑定；AIConfig 继续是平台配置。当前公司 Channel 管理入口仍应作为独立信息架构步骤恢复，不能把平台“接入公司”页改回旧编辑器。
- 双租户测试覆盖资源计数不串租户、删除资源不计数、停用资源仍计数、删除账号登录不影响活跃时间，以及“账号登录/会话活跃两者取较晚值”。

### 仿真数据租户继承补漏

- 使用当前源码和全新 SQLite 执行 `cmd/customer_audit_seed` 时发现，脚本仍把 Store、StoreStaffBinding、WxWorkProtocolInstance 及模拟 Conversation 子记录写为 `tenant_id=0`。这些记录会被正确的租户查询隔离，导致公司列表显示门店 0、模拟会话也不能进入公司派单池。
- 脚本现从已加载的 `legacy-default` Tenant 显式写入门店、门店员工绑定、企微实例、Conversation、RouteState、Participant、Message、Assignment 和 EventLog。重复执行也会同步 TenantID。
- 历史 `tenant_id=0` 只在记录 Remark 明确包含 `TEST_SEED:` 时允许修复；非零异租户记录或没有仿真标记的平台记录会直接报错，不允许按全局唯一 StoreCode/GUID 静默改归属。
- 仿真脚本测试覆盖资源首次创建、历史零租户修复和全套模拟会话子记录继承。修复后现有报告口径恢复为 100 门店、100 门店绑定、100 企微实例、36 会话、135 消息、21 派发记录，27 条需人工回复任务仍可用于后续派单测试。

### 并行协作与回滚

- `codex/ai-billing@f2d2da4` 与本批仅重叠中英文资源文件，且修改不同 key；最终合并仍应逐 key 保留双方文案，不整文件覆盖。
- 本批可通过回滚 response 新字段、聚合方法和前端摘要列完整撤销，不涉及数据回滚。回滚统计不得顺带恢复旧 Channel 页面或改变当前公司上下文导航。
- 仿真脚本修复可独立回滚代码，但已修正为正数 TenantID 的测试记录不应回写 0；需要清理时继续使用脚本的 marker cleanup，不做破坏性全表处理。
- `auth_response.go`、`auth_service.go`、`navigation.tsx` 和双语资源与 `codex/ai-billing` 有同文件变化；合并必须逐字段、逐导航项保留双方能力，禁止整文件选边。

## 33. 当前实施检查点：AIAgent 租户根与渠道绑定收口（2026-07-14）

本检查点补齐当前公司导航已经开放、但后端仍按全局数据读取的 AI Agent。它只处理 AI 接待实例的租户归属和引用边界，不改变平台模型配置、模型调用或计费语义。

### 数据与服务契约

- `AIAgent.TenantID` 是正数租户根字段，只能从当前 `AuthPrincipal.ActiveTenantID` 继承；Create/Update request 不接收 TenantID。
- AI Agent 名称改为公司内唯一，不同公司允许同名。列表、全部选项、详情、更新、删除、启停和排序均按 Active Tenant 查询；最终写入条件同时包含 `id + tenant_id`。
- AI Agent 引用的 AgentTeam 与 KnowledgeBase 必须属于同一租户。SkillDefinition 和 AIConfig 本批继续是平台级定义；租户 AI Agent 可以引用已启用的全局 AIConfig。
- Channel 创建/更新必须绑定同租户 AI Agent；Conversation 创建按 Channel Tenant 读取 Agent，并再次验证 Profile Tenant。AI 回复、派单、转人工、企微客服入站、消息发送者展示和 Channel 响应均使用已知 Conversation、Message 或 Channel Tenant 查 Agent。
- 企微员工号当前真实运行链不再有独立 `ai_agent_id` 字段，而是按 Instance 动态构建运行时 Agent；本批只给该运行时对象继承 Instance Tenant，不恢复旧独立 Agent 或旧企微字段。
- 小程序会话必须提供真实 ChannelID。没有渠道时不再全局扫描“AI 店长”作为兜底，避免绕过租户和 Channel 入口。
- Skill 调试接口在执行全局 runtime Hook 前，先校验 Agent、Conversation 与 CheckPoint 都属于当前公司；Hook 本身的模型执行语义不变。

### Migration 50

- migration 50 在 AutoMigrate 增加字段后执行，证据来源为 AIAgent 显式 Tenant、Channel、Conversation、TeamIDs、KnowledgeIDs，以及非平台创建/更新账号。AIConfig 不提供租户证据。
- 所有非零证据必须一致；缺失 Agent/Team/Knowledge/User 引用、无租户父记录或跨租户共享 Agent 都会使事务失败并回滚。无任何证据的历史 Agent 归入 `legacy-default`。
- migration 可重复执行；已有正确 Tenant 不重写，已有 Tenant 与解析结果冲突时失败，不静默改归属。

### 权限、兼容与剩余边界

- 沿用权限管理中现有 `aiAgent.view/create/update/delete`，没有新增隐藏权限或角色特判。没有 request/response DTO、enum、Gin 路由或 WebSocket payload 变化。
- AIConfig 明确保留平台级配置：`aiConfig.create/update/delete` 继续是 platform scope，租户角色只按现有授权查看可选配置。本批没有修改 FastGPT、模型供应商、回复引擎、Token、usage 或计费口径。
- 当前公司 Channel 管理入口现在不再受跨租户 Agent 绑定阻塞，但仍应在“当前公司设置”中复用现有 Channel API 单独恢复，不能把平台“接入公司”页改回旧 Channel 编辑器。
- AI 分支新增的 FastGPTDatasetJob、KnowledgeResourceGroup/Item、AIManualResumeTask、usage/run log 等主体仍需独立审计；AIAgent 完成不代表公开邀请注册可以启用。

### 并行合并与回滚

- migration 50 创建前已 fetch 并核对：`origin/main@e67e207` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支此前最高 49，无版本冲突。
- 当前 `origin/codex/ai-billing@f2d2da4` 未修改 `ai_agent_service.go` 和 AIAgent CRUD，但同文件包括 `models.go`、`reply_trigger_service.go`、`miniprogram_chat_service.go`、会话 builder、转人工/企微运行时及相关测试。合并时先保留 `AIAgent.TenantID`、migration 50、tenant-aware repository/service 原语；`reply_trigger_service.go` 同时保留 AI 分支 route-aware runtime 选择和本批 `conversation.TenantID` Agent 查询，`miniprogram_chat_service.go` 同时保留 AI 分支人工状态判断和本批 Channel 必填边界；禁止整文件选边。
- 回滚代码可以撤销租户查询和运行时校验，但已回填的正数 TenantID 不应改回 0。若确需业务回滚，应保留字段和数据，只回退入口使用；删除字段或恢复全局 Agent 会重新引入跨公司绑定风险。

## 34. 当前实施检查点：当前公司接入设置（2026-07-14）

### 页面职责与复用判断

- `/dashboard/channels` 继续是平台管理员唯一的“接入公司”页面，不恢复旧 Channel 编辑器，也不混入租户渠道配置。
- 复用原占位的 `/dashboard/settings` 作为当前公司的“接入设置”，复用既有 Channel 模型、`/api/dashboard/channel/*` 接口、`web/lib/api/admin.ts` 方法和 `channel.view/create/update/delete` 权限，不增加平行模型、接口、状态或隐藏权限。
- “接入设置”位于租户“服务能力”导航，只有存在 Active Tenant 且拥有 `channel.view` 时显示；平台账号未进入公司时由既有公司上下文守卫返回“接入公司”。
- 页面管理 Web、微信公众号和当前真实企微员工号协议渠道。历史 `wxwork_kf`、`wxwork_cli` 记录可以在全部列表中识别和删除，但不进入编辑表单；没有恢复企业微信客服号、CLI、旧 hook bridge 或旧独立 Agent。
- 企微协议字段以 `https://wework.apifox.cn/llms.txt`、回调说明、`/client/set_notify_url`、`/msg/send_text` 协议页和当前 `WxWorkProtocolChannelConfig` 运行代码交叉核对；本批不改变 `conversation_id`、`S:`/`R:` 前缀、消息发送或实例回调语义。

### 权限与敏感配置边界

- `channel.view` 只负责列表和导航；`channel.create` 控制新建；`channel.update` 控制当前渠道的编辑、启停、含敏感配置的详情读取和 User JWT Secret 重置；`channel.delete` 控制删除。历史 KF/CLI 记录的编辑和启停均禁用。
- 审计发现历史 Channel 列表/详情均会返回完整 `configJson`，导致仅有 `channel.view` 的账号可读取 App Secret 和 Callback Token。本批将列表与创建响应的 `configJson` 固定清空，并把详情接口改为要求现有 `channel.update`；字段、URL 和 JsonResult 结构不变。
- 新建/编辑弹窗只加载当前公司已启用的 AIAgent；后端继续校验 Channel 与 AIAgent 同租户。Web/公众号保留窗口参数、访问链接和 User JWT Secret；企微协议保留当前服务代码实际消费的 App Key、App Secret、API、设备池、WECDN、公共素材和 Callback Token 配置。

### 数据、验证与回滚

- 没有 model、AutoMigrate、DML migration、request/response DTO、enum、Gin 路由或 WebSocket payload 变化。列表 `configJson` 清空和详情鉴权属于现有接口的最小权限收口。
- 前端契约测试固定平台公司页与租户 Channel 页不互换、四项权限分别生效、租户上下文导航和历史渠道表单不恢复；后端测试固定列表脱敏和仅查看账号不能读取详情。
- 浏览器使用当前源码前端和临时 SQLite 后端验证：平台模式仍显示“接入公司”，进入默认公司后“接入设置”读取本公司渠道；桌面无横向溢出，`390x844` 下 document width 为 390、表格仅内部滚动、弹窗宽/scrollWidth 均为 388，控制台无错误。
- 可独立回滚租户设置页、导航和列表脱敏；不需要数据回滚。若回滚详情鉴权会重新暴露敏感配置，不建议单独回滚该安全边界。

### 并行分支影响

- 开始前已 fetch：`origin/codex/ai-billing@f2d2da4` 与本批重叠 `web/lib/navigation.tsx`、中英文资源，另修改 `web/lib/api/admin.ts`；本批没有修改 `admin.ts`，导航和文案只增加 `nav.channelSettings` 及 Channel 文案。
- 合并 AI 分支时逐项保留其 `replyIntentProfiles` 导航/文案和本批 `channelSettings`，禁止整文件覆盖。本批未改模型调用、回复 runtime、FastGPT、模型供应商、token、usage 或计费语义，也不需要在合并前 rebase。

## 35. 当前实施检查点：AI/Skill 运行日志租户隔离（2026-07-14）

本检查点修复当前公司已经可访问 Agent 运行日志、但底层日志仍没有 Tenant 根字段的问题。复用现有运行日志、后台页面、Dashboard 指标和 `conversation.view` 权限，不新增平行审计页面、日志模型或隐藏权限。

### 数据归属与 Migration 51

- `AgentRunLog` 和 `SkillRunLog` 增加 `TenantID`，DDL 继续由 AutoMigrate 创建；migration 51 只负责历史数据归属回填和一致性检查。
- Skill 日志从已有显式 Tenant、Conversation 和 AIAgent 解析归属；Agent 日志额外交叉核对 Message，并要求 Message 指向日志记录的同一 Conversation。所有非零证据必须指向同一有效 Tenant。
- 引用缺失、Message/Conversation 不一致、跨租户 Conversation/AIAgent/Message 证据或已有 Tenant 与父记录冲突时，migration 整笔失败并回滚，不按名称、时间或日志内容猜测公司。
- 完全没有父记录证据的历史日志归入 `legacy-default`。migration 可重复执行；已有正确 Tenant 保持不变，错误 Tenant 不会被静默重绑。

### 运行时与读取边界

- AI 回复总链路日志从当前 Conversation 继承 Tenant；写入前校验 Conversation、Message 和可选 AIAgent 均属于该 Tenant，并要求 Message 与 Conversation 匹配。
- Skill 日志从运行时 AIAgent 继承 Tenant；写入前校验可选 Conversation 和 AIAgent 均属于该 Tenant。Skill 匹配、执行计划和错误记录语义不变。
- `/dashboard/agent-run-logs` 的列表和详情先要求 Active Tenant，再按 `tenant_id` 查询；跨公司 ID 对当前公司表现为不存在。页面继续使用 `conversation.view`，因为该日志是会话回复链路的诊断视图，不增加重复的日志查看权限。
- 首页“今日 Skill 失败”从日志自身 `tenant_id` 统计，不再依赖 Conversation 子查询，因此无 Conversation 的合法 Skill 运行也能归入正确公司，同时不会跨公司计数。

### 契约、验证与合并边界

- 没有 request/response DTO、enum、Gin 路由、WebSocket payload、页面入口或新权限点变化；账号仍只分配角色，角色仍在权限管理内绑定权限。
- 双租户测试覆盖列表/详情隔离、运行时 Tenant 继承、跨租户父记录拒绝、Message/Conversation 不匹配拒绝、Dashboard 失败数隔离，以及 migration 的幂等、无证据兜底、冲突和缺失引用回滚。
- migration 51 创建并再次提交前均需核对远端编号。当前 `origin/main@e67e207` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支此前最高 50，无版本冲突。
- AI 分支与本批重叠 `internal/models/models.go` 和 `internal/ai/runtime/reply_runlog_service.go`。合并必须同时保留 AI 分支的 final action、资源、Graph 和已提交回复定位逻辑，以及本批 `TenantID: input.Conversation.TenantID`；禁止整文件选边。
- 建议先合并 Tenant 字段、migration 51 和 tenant-aware repository/service，再逐方法重放 AI 分支运行日志增强。回滚代码时已回填的 TenantID 不应写回 0；恢复全局日志查询会重新产生跨公司审计泄露风险。

## 36. 当前实施检查点：平台 Skill 定义写权限收口（2026-07-14）

本检查点复核无 TenantID 的后台资源后确认：`SkillDefinition` 按阶段 33 契约是所有公司可选择的全局平台定义，不是每家公司独立维护的业务数据。原权限却把创建、更新和删除标记为 tenant scope，并默认授予公司主管和客服组长，导致任一公司都能改变其他公司 AIAgent 共用的 Skill 行为。

### 权限与页面职责

- `skillDefinition.view` 继续保持 tenant scope，租户账号可查看全局技能说明、在本公司 AIAgent 中选择技能，并沿现有同租户 Agent/Conversation/Checkpoint 校验执行调试。
- `skillDefinition.create/update/delete` 改为 platform scope，只允许平台账号持有。创建、编辑、启停、删除和恢复 handler 在权限校验后再次验证 `IsPlatformAccount`，历史脏角色或旧 token 即使携带权限也不能写入。
- 公司主管和客服组长默认角色只保留 `skillDefinition.view`；平台超级管理员和管理员继续保留三项写权限。账号仍只分配角色，权限管理仍展示全部四项技能权限及其真实 scope。
- Skill 页面按 `IsPlatformAccount + 动作权限` 隐藏新增、编辑、状态切换、删除和恢复；只读用户仍可刷新、筛选、查看和调试，不会看到点击后必然返回 403 的写按钮。
- 通用 `DashboardCrudPage` 只向后兼容增加默认 `true` 的 `showCreate`，使页面可以隐藏新增但保留刷新；既有页面行为不变。

### Migration 52、验证与边界

- migration 52 幂等同步三项权限的 platform scope，保留所有平台角色已有绑定，并删除所有非 platform 角色上遗留的三项关系，包括自定义租户角色。
- 本批没有 model、AutoMigrate、request/response DTO、enum、Gin 路由、WebSocket payload 或 Skill runtime 语义变化，不改变模型调用、工具执行、token、usage 或计费口径。
- 测试覆盖超级管理员/管理员保留权限、内置和自定义租户角色清理、自定义平台角色保留、重复迁移，以及租户账号携带脏平台权限仍被五个写 handler 拒绝。前端契约固定动作显隐和刷新保留。
- migration 52 创建前已 fetch：`origin/main@e67e207` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支最高 51，无版本冲突。AI 分支不修改本批权限常量、Skill handler、Skill 页面或通用 CRUD 文件；无需 rebase，最终合并仍需保留本批 scope 与角色关系清理。
- 回滚页面显隐不会改变服务端权限；回滚 migration 或 handler 防线会重新允许租户角色修改全局 Skill，不属于安全回滚。若未来要支持公司自定义 Skill，应新增明确 Tenant 归属和 AIAgent 同租户引用契约，而不是重新把当前全局写权限授予租户角色。

## 37. 当前实施检查点：平台 AIConfig 写操作显隐与防线（2026-07-14）

本检查点继续沿用已确认的 AIConfig 平台语义：模型供应商、API 接入和限额是平台配置，租户账号通过 `aiConfig.view` 只读查看可选模型，AIAgent 仍可引用启用的全局 AIConfig。审计发现服务端写权限已是 platform scope，但租户只读页面仍显示所有写控件，写 handler 也只依赖权限数组，没有像其他平台设置一样防御历史脏关系或旧 token。

### 权限与页面行为

- `aiConfig.view/create/update/delete` 的 code、scope 和默认角色关系不变，不新增权限或隐藏角色判断。租户角色继续只有 view，平台超级管理员和管理员按现有角色绑定持有写权限。
- 创建、更新、启停、排序和删除 handler 在原权限校验后增加 `IsPlatformAccount` 校验；租户账号即使异常携带平台权限也不能修改全局 AIConfig。
- AIConfig 页面同时按平台账号身份和 create/update/delete 权限展示新增、编辑、状态开关、拖拽排序、删除及操作列。租户只读用户仍能刷新、筛选和查看脱敏后的模型信息，不再看到必然失败的按钮。
- 页面复用第 36 批 `DashboardCrudPage.showCreate`，没有增加第二套 CRUD 或模型配置入口。

### 契约、验证与边界

- 本批没有 model、AutoMigrate、DML migration、DTO、enum、Gin 路由、WebSocket payload 或 AIConfig service 变化；不修改 API Key 保存、模型调用、供应商、超时、重试、token、usage 或计费口径。
- 测试覆盖租户账号异常持有 create/update/delete 时，五个写 handler 均返回禁止；前端契约覆盖平台身份、三项动作权限、状态开关、排序、删除和操作列显隐。
- 全量 Go、专项 race、vet、74 项前端测试、typecheck、生产构建、目标 ESLint 和 diff 检查通过。
- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 不修改本批 AIConfig handler、页面、权限测试或通用 CRUD 文件；无 migration 编号和同文件冲突。本批可以独立合并，不需要重放 AI 分支模型配置实现。
- 页面显隐可独立回滚，但 handler 平台账号校验应保留。未来若模型配置改为每租户独立，必须先设计 TenantID、密钥归属、计费和运行时解析，不能仅移除这次平台防线。

## 38. 当前实施检查点：租户角色平台权限清理与登录会话保护（2026-07-14）

本检查点从权限常量、默认角色和 handler 交叉审计发现：`session.view` 已声明为 platform scope，但仍被默认授予 `cs_team_leader`；`ensureRolePermissions` 只按常量建关系，不执行 RoleService 的 scope 校验，因此客服组长可直接调用 Session API 读取全平台账号的 IP、User-Agent 和登录状态。

### 权限不变量与 Migration 53

- 从客服组长默认权限中移除 `session.view`。LoginSession 继续是平台全局安全审计资源，不增加 TenantID，也不复制成每公司会话页。
- migration 53 幂等同步当前权限/角色后，删除所有非 platform 角色与任意 platform permission 的历史关系；不仅修复内置客服组长，也清理历史自定义租户角色上的平台权限。平台内置和自定义角色绑定保持不变。
- 新增常量矩阵测试：任何内置 tenant scope 角色一旦再次配置 platform permission，测试立即失败。运行时 `RoleService.AssignPermissions` 原有 scope 拒绝继续保留，形成常量、迁移和服务三层边界。
- Session 列表、按会话撤销和按用户撤销在原 `session.view/revoke` 权限后增加 `IsPlatformAccount` 校验；历史脏关系或旧 token 即使仍携带权限，也不能读取或操作全局登录会话。

### 契约、验证与剩余合并门槛

- 没有 model、AutoMigrate、DTO、enum、Gin 路由、WebSocket payload、页面或导航变化；权限点仍完整显示在权限管理中，账号仍只通过角色获得权限。
- 测试覆盖默认角色常量矩阵、内置客服组长历史关系、自定义租户/平台角色、所有平台权限交叉清理、迁移幂等，以及三个 Session handler 的脏权限防线。
- 全量 Go、专项 race、vet、74 项前端回归、typecheck、Next 生产构建和 diff 检查通过。
- migration 53 创建前已 fetch：`origin/main@e67e207` 最高 20、`origin/codex/ai-billing@f2d2da4` 最高 33、本分支最高 52，无版本冲突。AI 分支不修改本批权限常量、Session handler 或权限测试；最终合并必须保留 migration 53 和常量矩阵测试。
- ReplyIntentConfig 的页面和 handler 当前也缺少第 37 批同类平台写显隐/脏权限防线，但 `codex/ai-billing` 正在同时重写其模型、DTO、service、handler、migration 和整页。本分支不覆盖该运行语义；合并时必须让 create/update/delete 复用 AIConfig 平台账号防线，并按 create/update/delete 权限隐藏写操作。
- 回滚 migration 或 Session handler 防线会重新开放全平台登录数据，不属于安全回滚。若未来需要公司主管查看本公司账号登录状态，应设计租户级只读 DTO 和按 User.TenantID 的专用接口，不能复用当前平台全量 Session API。

## 39. 当前实施检查点：角色与 MCP 平台边界、AI Agent 动作显隐（2026-07-14）

本检查点继续横向核对所有 platform scope 权限的真实 handler。迁移 53 已清理数据库关系，但已签发 token 或异常认证数据仍可能携带旧平台权限；同时发现 MCP 的只读工具目录与平台调试错误共用 `mcp.view`，使租户 AI Agent 编辑器无法读取可选工具。

### 平台写操作与调试边界

- 角色列表、详情和权限说明继续由 tenant scope 的 `role.view` 提供，公司主管和客服可以查看平台预设角色；创建、更新、启停、删除、排序和分配权限在原 platform scope 动作权限后统一增加 `IsPlatformAccount` 校验。
- MCP Server 列表、连通性测试、远程工具枚举继续要求 `mcp.view + IsPlatformAccount`；真实工具调用要求 `mcp.call + IsPlatformAccount`。租户账号即使持有旧 token 中的 MCP 平台权限也不能读取服务器端点或触发远程工具。
- `/api/dashboard/mcp/catalog` 不是平台调试接口，而是 AI Agent/Skill 表单读取工具名称、代码和 schema 的只读选项源。它改为复用现有 tenant scope `aiAgent.view`，不返回 MCP endpoint 或 header，也不新增平行权限和页面。
- 本批没有改变 MCP 客户端、工具执行、AIAgent 工具白名单、Skill runtime、模型调用、token、usage 或计费语义。

### 页面、验证与合并边界

- 角色页只有平台账号同时持有 `role.create/update/assignPermission` 时才显示相应新增、拖拽和分配入口；租户账号保留只读角色上下文。
- MCP 页只有平台账号同时持有 `mcp.call` 时才显示 Arguments 编辑器和真实调用按钮；`mcp.view` 仍只提供连接与工具元数据调试。
- AI Agent 页补齐 `aiAgent.create/update/delete` 动作显隐：只读账号不再看到新增、编辑、启停、排序或删除，页面查看能力不变。
- 没有 model、AutoMigrate、DML migration、DTO、enum、Gin 路由、WebSocket payload、权限常量或导航变化。全量 Go、专项 race、vet、77 项前端测试、typecheck、Next 生产构建、目标 ESLint 和浏览器超管回归均通过。
- 开始前已 fetch：`origin/main@e67e207`、`origin/codex/ai-billing@f2d2da4`、`origin/codex/customer-audit@639b0a2`。AI 分支只在本批检查范围内修改了导航文件，本批不修改导航，因此无同文件冲突，也不需要 rebase。
- 回滚页面显隐不会改变服务端权限；角色与 MCP handler 的平台账号防线不应单独回滚。若未来允许租户自建角色或 MCP Server，必须先增加明确 Tenant 归属和数据范围，不能仅移除平台校验。

## 40. 当前实施检查点：快捷回复与客户档案动作权限显隐（2026-07-14）

本检查点继续核对 tenant scope 页面“查看入口”和“写动作”的职责。后端 QuickReply、Customer handler 已按 create/update/delete 正确鉴权并按 Active Tenant 隔离，但前端通用 CRUD 仍默认展示所有写操作，导致只持有 view 的客服看到点击后必然失败的按钮。

### 页面行为与权限契约

- 快捷回复页复用 `quickReply.create/update/delete`：create 控制新增，update 控制编辑和启停，delete 控制删除；只读账号仍可筛选、刷新和查看内容，且完全隐藏空操作列。
- 客户页复用 `customer.create/update/delete`：create 控制新建，update 控制编辑和启停，delete 控制删除。客户详情、门店关系和已有会话跳转属于 `customer.view` 的只读工作流，始终保留，因此只读账号仍看到详情操作而不是隐藏整列。
- 页面只改变动作可见性，不以此替代服务端鉴权；现有 handler 权限、Active Tenant 校验、最终 tenant-qualified 写入保持不变。
- 没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 变化。

### 验证、并行与后续

- 全量 Go、vet、79 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；新增契约测试固定两页的动作权限映射，并固定客户详情对只读账号可见。
- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 不修改两页或新增测试；无同文件和 migration 编号冲突，不需要 rebase。
- 客户企业页与 AI 分支同文件，本批不改；排班、标签、知识候选和工单仍需按各自现有动作权限独立核对，不能把本批两个标准 CRUD 页完成解释为全后台显隐审计结束。
- 本批可完整回滚前端页面与测试，无数据回滚；回滚会恢复误导性的只读写按钮，但不会改变后端权限和租户隔离。

## 41. 当前实施检查点：排班日历与标签树动作权限（2026-07-14）

本检查点处理非标准 CRUD 页面中的隐式写交互。排班和标签后端均已有细粒度动作权限，但前端除按钮外还通过日期格、排班块、拖拽和开关触发写接口，仅隐藏工具栏不足以形成一致权限体验。

### 排班交互边界

- `agentTeamSchedule.create` 控制顶部新建、列表新建和日历空白日期创建；URL 的 `action=create` 也必须具备 create 才能自动打开弹窗。
- `agentTeamSchedule.update` 控制列表编辑、日历排班块点击编辑、拖动换日和左右拉伸时段；只读账号看到排班内容，但排班块不再具有按钮语义、键盘编辑、拖动光标和缩放手柄。
- `agentTeamSchedule.delete` 控制列表删除和编辑弹窗删除；`agentTeamSchedule.batchGenerate` 独立控制批量编排入口。历史排班只读规则继续叠加在动作权限之上。
- 月/周/列表切换、日期导航、团队筛选、刷新和排班内容查看仍属于 view，不因缺少写权限消失。

### 标签交互边界

- `tag.create` 控制新增；`tag.update` 同时控制编辑、启停和同级拖拽排序；`tag.delete` 控制删除菜单。
- 无 update 时不渲染拖拽手柄及其空白列，也不渲染状态开关；状态 Badge、树展开/折叠、筛选和刷新继续只读可用。
- 操作列按 update/delete 动态出现；空状态 `colSpan` 同步按实际列数计算，避免只读页面保留空列或表格错位。

### 契约、验证与合并

- 没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket payload、权限常量或导航变化；页面显隐不替代既有后端权限和租户校验。
- 全量 Go、vet、82 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；测试固定排班日历鼠标/键盘交互与标签树各写动作的权限映射。
- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 不修改本批页面、日历组件或测试，无同文件与 migration 冲突，不需要 rebase。
- 本批可独立回滚前端和测试且无需数据回滚；回滚会恢复误导性写交互。客户企业、知识候选和工单动作显隐仍需后续独立核对。

## 42. 当前实施检查点：知识候选审核动作权限（2026-07-14）

本检查点核对知识候选页面与现有知识库权限的关系。当前后端没有隐藏的候选审核权限：列表使用 `knowledgeBase.view`，编辑、质检、审核、导出和导入标记统一使用 `knowledgeBase.update`，因此前端应准确复用这两个既有权限而不是新增重复权限。

### 页面职责与显隐

- `knowledgeBase.view` 保留候选列表、问题/答案、门店与知识库来源、状态、创建时间以及来源会话跳转。
- `knowledgeBase.update` 控制候选选择框、全选、质检、批量通过/驳回、周导出、单条编辑/通过/驳回/标记导入及编辑弹窗。
- 只读账号不再看到可选择但无法处理的 Checkbox 或空审核工具栏；来源会话按钮独立保留，不随 update 权限隐藏。
- 前端动作函数增加同一 `canManage` 守卫，避免未来 UI 组合变化重新触发写请求；服务端原权限和 Tenant/客服组范围校验仍是最终边界。

### 契约、验证与合并

- 没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket payload、权限常量或导航变化，也没有改知识候选生成、质检判断和知识导入语义。
- 全量 Go、vet、83 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；契约测试固定 `knowledgeBase.update` 与所有审核动作的关系，同时固定来源会话入口对只读账号可见。
- 开始前已 fetch，`origin/codex/ai-billing@f2d2da4` 不修改本批页面或测试，无同文件和 migration 冲突，不需要 rebase。
- 本批可独立回滚页面和测试且无需数据回滚；工单动作权限和 AI 分支中的客户企业页仍待后续独立收口。

## 43. 当前实施检查点：工单职责权限与首次指派审计（2026-07-14）

本检查点复用现有工单、会话和客户关联能力，不新增平行派单模型。工单仍负责跨会话事项闭环，会话派单仍负责即时消息回复；本批只收紧工单内部“内容编辑”和“负责人指派”的职责边界，并让页面准确表达已有权限。

### 后端契约与审计

- `ticket.update` 只修改标题、描述、分类、优先级、房间号和标签，不再接收或写入 `currentAssigneeId`；负责人只能通过 `/ticket/assign` 和 `ticket.assign` 变更，避免绕过指派原因、进展和通知事件。
- 手工创建和会话转工单仍可设置初始负责人，但非零负责人同时要求 `ticket.create + ticket.assign`。仅有创建权限时可以创建未指派工单，不能顺带派给客服。
- 创建时的初始负责人改为在同一事务内复用 `assignTicketTx`：先写“创建工单”，再写“指派处理人”进展，并在提交后发布 `TicketAssignedEvent`。因此首次指派与后续转派使用同一租户校验、人员状态校验和通知链路。
- 兼容旧客户端：旧版更新请求继续携带 `currentAssigneeId` 时，后端 DTO 会忽略该字段而不是报错，但负责人不会变化。创建请求字段保持不变。

### 页面权限与只读体验

- 工单列表新增入口使用 `ticket.create`；负责人和标签筛选仅在分别具备 `agent.view`、`tag.view` 时加载辅助数据，避免只读角色因无关列表接口 403 导致工单页失败。
- 工单详情的内容编辑、指派、状态、进展分别使用 `ticket.update`、`ticket.assign`、`ticket.changeStatus`、`ticket.progress`。无写权限时仍显示工单内容、当前状态、负责人、客户资料和历史进展。
- 已关联客户的档案编辑使用 `customer.update`；未关联工单的查找/新建客户同时要求 `ticket.update` 与对应的 `customer.view/customer.create`。共享客户关联弹窗也执行相同守卫。
- 会话工作台中的转工单、转接和关闭分别使用 `ticket.create`、`conversation.transfer`、`conversation.close`；无任何动作权限时不显示空菜单。会话关联客户继续使用 `conversation.linkCustomer`，并叠加客户查看或创建权限。

### 契约、验证与合并

- 本批修改 request DTO 和服务行为，但没有 model、AutoMigrate、DML migration、enum、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 变化；不涉及 AI 回复、模型、token、usage 或计费。
- 全量 Go、vet、87 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。新增 handler 测试固定创建/指派复合权限，service 测试固定编辑保留负责人及首次指派进展，前端契约测试固定工单和会话动作映射。
- 开始前和提交前均已 fetch。最终范围扩展到 `conversation-info-panel.tsx` 后确认 AI 分支也修改该文件：本批位于前半段未关联客户权限入口，AI 分支位于后半段自动转人工和公司意图配置，当前 `merge-tree` 可自动合并且语义不重叠；合并时必须同时保留双方逻辑。无 migration 编号影响，不需要为本批单独 rebase。
- 回滚后普通编辑会重新具备静默改派能力，首次指派也会丢失专用进展与通知，因此若只回滚前端会重新暴露后端职责漏洞；建议本批后端、前端和测试整体回滚。

## 44. 当前实施检查点：派单与会话监控工作台动作权限（2026-07-14）

本检查点继续复用现有会话权限，不新增“主管角色专属”隐藏能力。页面能力只由权限管理中可见、可分配的权限点决定；客服组/任务的 `manageable` 仍负责业务范围，不能代替操作权限。

### 派单工作台

- `conversation.view` 提供派单任务、状态统计、等待时间、推荐客服和客服实时负载的只读视图。
- `conversation.handover` 控制自动派发、手动派发、转派、释放以及对应操作列和弹窗。动作函数与弹窗 `open` 同时加守卫，不能只靠隐藏按钮。
- `task.manageable` 继续校验组长是否可管理该任务；有 `conversation.handover` 但任务不在其管理范围时，按钮仍禁用。
- 客服组筛选只在 `agentTeam.view` 下加载并显示；没有该权限不会调用客服组接口，也不会影响任务、统计和客服负载读取。

### 会话监控工作台

- 查看列表/详情、历史消息和标记已读继续属于 `conversation.view`；只读观察角色保留完整监控能力。
- 分配和重试自动调度使用 `conversation.assign`，转接使用 `conversation.transfer`，关闭使用 `conversation.close`。列表菜单、详情页底部动作、事件函数和三个弹窗均使用相同权限。
- 标签、客服、客服组筛选分别依赖 `tag.view`、`agent.view`、`agentTeam.view`；缺少任一辅助权限时只隐藏对应筛选并跳过接口，不影响基本监控页面。
- 第 43 批曾误写前端辅助权限码 `agentProfile.view`，本批已按真实常量和 `/agent/list_all` handler 更正为 `agent.view`，同时修正文档和契约测试。后端从未存在或授予错误权限码。

### 契约、验证与合并

- 没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 变化；不涉及 AI 回复、模型、token、usage 或计费。
- 全量 Go、vet、90 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过。目标 ESLint 仅保留监控详情/页面和会话页已有的三个 warning，无 error。
- 提交范围与 `origin/codex/ai-billing@f2d2da4` 无同业务页面修改；无需 rebase。最终合并后仍应重跑权限契约测试和生产构建。
- 本批可独立回滚前端与测试且无需数据回滚；回滚会重新让只读观察角色看到必然失败的主管动作，并让缺少辅助 view 权限的页面产生 403。

## 45. 当前实施检查点：平台存储与企微设备池只读边界（2026-07-14）

本检查点核对平台配置页面与既有 platform scope 权限。后端已同时检查显式权限和 `IsPlatformAccount`，无需新增权限或隐藏授权；遗漏仅在前端把只读平台账号当作可编辑账号。

### 页面职责与权限

- 存储设置页面在 `storageSetting.view` 下继续展示 OSS、本地存储和企微富媒体链路配置状态；只有平台账号且具备 `storageSetting.update` 才能编辑字段、切换私有 Bucket 并保存。
- 企微设备池在 `wxworkDevicePool.view` 下继续展示后台连接状态、实例统计、GUID、企微登录和本地绑定情况。
- 设备池凭据编辑/保存使用 `wxworkDevicePool.update`，同步实例使用独立的 `wxworkDevicePool.sync`。两项能力互不隐含，页面分别隐藏按钮并在处理函数中重复守卫。
- 只读页面保留刷新；凭据字段以禁用状态展示，不暴露密码明文。后端原有 platform account 防线仍是最终安全边界。

### 审计中明确不实施的内容

- 门店工作台当前是静态设计占位，没有加载、保存或通知跳转接口。其按钮不是可运行功能，本批不增加看似可用的假交互；后续实现前必须先定义真实权限、接口和门店绑定范围。
- 知识库页面确有动作权限显隐遗漏，但 `codex/ai-billing` 正在修改知识库主页面和编辑组件；为避免覆盖 FastGPT/资源组逻辑，本批不改该域，待合并后按真实代码统一收口。

### 契约、验证与合并

- 没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、权限常量、导航或响应结构变化，也不修改存储保存与设备同步语义。
- 全量 Go、vet、92 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；新增测试固定平台身份与 update/sync 权限的组合边界。
- `origin/codex/ai-billing@f2d2da4` 不修改两个平台页面或新增测试，无同文件和 migration 冲突，不需要 rebase。
- 本批可独立回滚页面与测试且无需数据回滚；回滚不会绕过后端，但会重新让只读平台账号编辑表单并触发 403。

## 46. 当前实施检查点：通知只读流程与运行日志辅助筛选（2026-07-14）

本检查点继续按“页面主权限负责核心职责，辅助资源权限只控制可选信息”的规则核对通知中心和 Agent 运行日志。两个页面都复用权限管理中已有权限，不新增角色隐含能力或重复权限。

### 通知查看与已读边界

- `notification.view` 控制通知列表、未读数请求和通知 WebSocket。没有查看权限时，Provider 不再请求未读接口或建立实时连接，并将本地未读数归零。
- `notification.update` 只控制单条标记已读和全部标记已读；只读账号仍可点击带 `actionUrl` 的通知进入目标页面，导航不依赖更新权限。
- 实时通知 Toast 的跳转遵循相同边界：有 update 时先尝试标记已读，无 update 时直接导航；标记已读失败也不能阻断用户进入通知指向的业务页面。
- 通知列表保留筛选、刷新、内容查看和业务跳转；缺少 update 时仅隐藏“全部已读”，不会把通知中心误变成不可用页面。

### 运行日志辅助筛选

- Agent 运行日志的主体列表仍由 `conversation.view` 提供，不要求用户额外具备 AI Agent 管理权限。
- AI Agent 下拉筛选及其 `/ai-agent/list_all` 请求只在具备 `aiAgent.view` 时加载和显示；缺少该辅助权限时仍可按状态、动作、时间和关键词查看运行日志。
- 该筛选不改变日志接口的数据范围和服务端租户隔离，只避免页面因无权读取 AI Agent 列表而出现无关 403。

### 契约、验证与合并

- 没有后端、model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 变化；不修改 AI 回复、模型、token、usage 或计费语义。
- 全量 Go、vet、94 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；新增测试固定通知 view/update 分界、跳转独立性和运行日志的 `aiAgent.view` 可选筛选。
- `origin/codex/ai-billing@f2d2da4` 不修改本批页面、Provider 或测试，无同文件和 migration 冲突，不需要 rebase。
- 本批可独立回滚前端与测试且无需数据回滚；回滚会重新使只读通知无法可靠跳转，并让无 `aiAgent.view` 的运行日志页面调用无权接口。

## 47. 当前实施检查点：运营总览显式权限（2026-07-14）

本检查点补齐后台首页原先的隐式访问。运营总览包含全公司会话、工单、客服负载和 AI 运行汇总，不能只凭“账号已登录并选中公司”开放；它必须像其他模块一样进入权限管理、角色配置、导航显隐和后端鉴权链路。

### 权限与默认角色

- 新增租户级 `dashboard.view`，名称为“查看运营总览”，对应 `GET /api/dashboard/dashboard/overview`；权限在权限管理页面可见，也可由平台管理员通过角色管理赋予或收回。
- 超级管理员通过全权限集合获得该权限；管理员、公司主管、客服组长和客服的内置角色默认获得。客服是否能查看今日运营信息由该权限决定，不再由前端角色名特判。
- 门店员工不默认获得公司级总览，避免把全公司客服负载和运营指标隐式暴露给门店账号；需要时必须通过合规角色显式赋予。
- migration 54 幂等同步权限和内置角色关系，不覆盖自定义角色的选择，也不恢复账号级权限分配。

### 接口、导航与登录落点

- `DashboardGetOverview` 先校验 `dashboard.view`，再校验 `ActiveTenantID`；仅有权限但没有当前公司、或只有公司上下文但没有权限，均不能读取汇总。
- 侧边栏“后台总览”按 `dashboard.view` 显隐。无该权限的账号直接进入 `/dashboard` 时不会请求总览接口，而是按同一导航权限规则跳到第一个可访问模块。
- 若账号没有任何可访问后台模块，首页显示明确空权限状态，不制造循环跳转；该状态不代替后端鉴权。
- 权限管理英文显示补充 `Operations overview / View operations overview`，中文继续使用后端权限名称。

### 契约、验证与合并

- 本批新增权限常量和 DML migration 54；没有 model、AutoMigrate、request/response DTO、enum、业务数据字段、WebSocket payload 或 JsonResult 变化，也不修改总览指标计算口径。
- 全量 Go、vet、96 项前端测试、typecheck、Next 生产构建、目标 ESLint 和 diff 检查通过；测试覆盖后端权限/公司上下文双门槛、migration 幂等与默认角色范围、导航显隐和无权回退。
- `origin/codex/ai-billing@f2d2da4` 同时修改 `web/lib/navigation.tsx` 和双语资源：AI 分支新增意图行业入口及其文案，本批修改总览入口和 `common.noAccessibleModules`，本批区块和语义不重叠；但两条长期分支在导航数组还累计了其他变化，`git merge-tree --write-tree HEAD origin/codex/ai-billing` 已确认 `navigation.tsx` 需要手工合并。必须保留本分支完整租户导航与 `dashboard.view`，同时保留 AI 分支 `replyIntentProfiles`；双语资源本次可自动合并。AI 分支最高 migration 33，与 54 不冲突。
- 回滚代码后数据库中多出的内置权限及角色关系不会破坏旧版本，不应通过破坏性 SQL 删除；若产品决定撤销总览权限，应先停用权限和清理角色关系，再单独做幂等 DML。

### 本轮复扫后的剩余边界

- 继续以 `rg` 核对 dashboard handler 后，没有发现第二个像旧总览一样、整个后台资源文件完全缺少显式权限或统一权限 helper 的入口；这不替代逐动作测试，AI 分支合并后仍要重跑 handler 权限审计。
- 客户企业页、知识库页、回复意图页和企微员工号 Manager 仍有动作显隐/辅助接口加载需要统一收口；这些文件均被 `codex/ai-billing` 修改。公司详情还复用了同一个企微 Manager，不能只隐藏外层开户链接按钮就宣称完成。
- 上述四个域必须在 AI 分支合并并确认最终 handler 权限后整页处理，尤其保留 FastGPT、意图行业、员工号欢迎语/模型设置和本分支租户范围；合并前不做局部补丁，避免同一页面形成两套权限判断。
- 门店工作台继续是静态设计占位，不创建假接口或假按钮权限。其真实数据源、门店范围、通知动作和权限点确定前不纳入可运行功能验收。

## 48. 当前实施检查点：Dashboard Handler 权限契约测试（2026-07-15）

本检查点把第 47 批后的函数级权限复扫固化为 Go AST 测试，避免后续新增 dashboard handler 时只依赖人工记忆。它不新增业务权限，也不改变已有权限语义。

### 测试契约

- 测试解析 `internal/handlers/dashboard/*_handler.go`，识别所有导出的、接收 `*gin.Context` 的 handler。
- 每个 handler 必须直接调用 `AuthService.RequirePermission/HasPermission`，或通过本包函数调用链最终进入上述检查；因此 AIConfig/Skill 平台写 helper、工单进展 helper 和企微统一动作 helper 都能按真实委托关系验证。
- 唯一认证级例外是 `UserPostChange_password`：修改当前登录账号自己的密码不是可由角色收回的业务管理权限。测试要求该入口仍显式调用 `Authenticate`，而重置其他账号密码继续由 `user.update` 控制。
- 测试不把函数名或 HTTP Method 当作权限事实，也不把“文件中某处出现过权限检查”等同于每个 handler 已鉴权。

### 审计结论与边界

- Skill 调试继续按既有权威设计使用 `skillDefinition.view`：租户只查看平台共享 Skill，并且 service 强制校验本公司 AIAgent、Conversation 和 Checkpoint；Skill 定义写入仍是 platform scope。
- 会话已读、工单个人视图保存/删除、企微登录状态/群资料读取及公司模型设置读取虽使用 POST，但属于读取状态或当前账号偏好，不新增平行动作权限。
- 知识库调试回答会触发模型并写检索日志，目前使用 `knowledgeDocument.view`。其租户知识库 ID 和检索向量已 fail-closed，但“查看是否应包含付费调试调用”必须结合 AI/计费分支最终用量口径决定；本分支不单方面新增权限或改变模型调用。
- AST 契约只证明 handler 最终进入了权限检查，不证明某个动作选择的权限一定正确；动作语义仍需后端测试、前端显隐和角色职责三方核对。

### 变更、验证与合并

- 仅新增 `internal/handlers/dashboard/permission_contract_test.go` 和两份交接记录；没有生产 Go、权限常量、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、前端或 AI runtime 变化。
- `go test ./... -count=1`、`go vet ./...` 和 `git diff --check` 通过；定向测试同时覆盖权限调用图、总览、Skill 和 AIConfig 现有边界。
- `origin/codex/ai-billing@f2d2da4` 不修改本批新增测试或两份交接文档，无同文件和 migration 冲突；合并 AI 分支后必须立即重跑该测试，新增 handler 若无权限契约会直接失败。
- 本批可通过删除测试文件和本节文档独立回滚，无业务数据与运行行为回滚。

## 49. 当前实施检查点：客服审计仿真全生命周期验证（2026-07-15）

本检查点重新验证客服组、门店员工号、客户会话和派单仿真数据在当前 54 个 migration 契约下仍可从空库完整建立、重复执行和安全清理，并把手工验证升级为自动化测试。

### 新鲜数据库证据

- 使用独立临时 SQLite，从空库执行 AutoMigrate 和全部 DML migration 后再运行 `customer_audit_seed`，不读取或修改当前 8083 服务数据库。
- 首次 seed 得到：1 个测试客户企业、1 个协议渠道、100 个门店、3 名客服组长、12 名客服、100 名门店员工、3 个综合客服组、12 个客服档案、100 个门店绑定、100 个企微实例、500 个客户及联系人/身份、801 条客户门店关系。
- 仿真派单基线为 36 个会话、135 条消息、21 条历史 Assignment、18 条当前已派发任务覆盖 12 名客服、27 条需人工回复；状态分布为 AI 接待 6、待接入 9、处理中 18、已关闭 3。三个报告标志均为 `true`。
- 对同一 batch 再次 seed 后，完整 report 逐字段保持一致，证明 upsert 和模拟会话重建不会累加用户、关系、消息或 Assignment。

### Cleanup 与系统数据边界

- cleanup 后 batch report 的所有业务计数归零；额外直接检查 Conversation、RouteState、Participant、Message、Assignment 和 EventLog 六张表均为 0，避免 RouteState 子查询归零却遗留孤儿消息的假通过。
- cleanup 后数据库只保留 bootstrap admin，六个内置角色、`dashboard.view` 权限及成功的 migration 54 仍存在，证明测试清理没有越界删除系统认证数据。
- 手工验证使用的临时配置和 SQLite 文件已删除，不写入仓库，也不生成 `docs/generated/` 报告。

### 自动化测试与合并

- 新增 `cmd/customer_audit_seed/lifecycle_test.go`，自动执行新鲜库迁移、首次 seed、重复 seed、精确报告比较、cleanup、孤儿表检查和系统数据保留检查。
- `go test -race ./cmd/customer_audit_seed -count=1`、`go test ./... -count=1`、`go vet ./...` 和 `git diff --check` 通过；生命周期测试单次普通执行约 1.2 秒。
- 没有生产 Seed、model、AutoMigrate、DML migration、DTO、enum、API、权限、路由、WebSocket、前端或 AI runtime 变化。`origin/codex/ai-billing@f2d2da4` 不修改新增测试或两份交接文档，无同文件和 migration 冲突。
- 本批可通过删除测试和本节文档独立回滚，不需要清理业务数据库；临时验证数据库已在本批结束前删除。

## 50. 当前实施检查点：会话详情辅助资源与动作权限收口（2026-07-15）

本检查点继续执行“页面查看权限不隐含其他资源权限、动作权限不由角色名称替代”的既定设计。审计发现会话工作台只要求 `conversation.view`，但详情侧栏会无条件读取客户档案、联系人、关联工单和标签树，并始终显示编辑客户、编辑客户企业和修改会话标签入口；自定义只读角色因此会收到多个 403，页面职责与权限管理中已存在的权限点不一致。

### 复用权限与页面行为

- 客户档案和联系人只在持有 `customer.view` 时加载；缺少该权限时仍保留会话的智能回复状态、员工号、门店和已附标签，不把客户档案读取失败误报成会话加载失败。
- 客户编辑同时要求 `customer.view + customer.update`，客户企业编辑同时要求 `customer.view + company.update`。两个弹窗及保存回调都受相同能力控制，服务端既有权限与 Tenant 校验继续作为最终边界。
- 关联工单只在持有 `ticket.view` 时加载。标签树只在持有 `tag.view` 时加载，只有同时持有 `tag.view + conversation.tag` 才显示会话标签选择器。
- 会话响应已经携带的标签名称始终保留只读展示；没有 `tag.view` 时不请求完整标签树，因此不会泄露同租户其他标签，也不会把“不能管理标签”错误表达成“会话没有标签”。

### 契约、验证与并行合并

- 修改 `web/app/dashboard/conversations/_components/conversation-info-panel.tsx`，新增 `web/app/dashboard/conversations/conversation-info-permissions.test.mjs`。没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限常量或 AI runtime 变化。
- 全前端 99 项契约测试、`pnpm typecheck`、目标 ESLint、Next 生产构建、`go vet ./...`、最终 `go test ./... -count=1` 和 `git diff --check` 通过。验证期间一次并行全量 Go 运行在 `internal/services` 失败，单包与最终全量立即复跑通过；`-count=3` 会因该包复用全局 DB/配置状态导致第二轮批量失败，属于既有测试隔离限制，不能据此修改本批前端或宣称服务测试可重复进程内运行。
- `origin/codex/ai-billing@f2d2da4` 同时修改会话详情组件：新增自动转人工开关，并在公司更新 payload 保留 `intentProfileId`。最终合并必须同时保留这两项和本批权限能力；该开关后端明确要求 `conversation.handover`，合并后前端必须按此权限隐藏并守卫，不能仅凭 `conversation.view` 展示。
- 本批不改变客户、客户企业、工单和标签的后端语义，可独立回滚页面与契约测试；回滚会恢复越权辅助请求和误导性写入口。公开邀请注册仍受 AI 分支新增主体租户隔离与合并后页面权限复核门槛约束。

## 51. 当前实施检查点：企微员工号账号入口与 Manager 动作权限（2026-07-15）

本检查点审计会话工作台、企微员工号独立页和客户企业详情共同复用的 `WxWorkProtocolInstanceManager`。会话页只要求 `conversation.view`，此前却无条件读取企微实例，并向所有账号显示新增、账号设置和远程开户链接；Manager 自身也无条件提供创建、编辑、删除和更换登录，并无条件加载知识库与客户企业选项。这使自定义会话只读角色整页出现 403，也让默认客服看到后端明确拒绝的账号管理动作。

### 页面职责与复用权限

- 会话工作台继续保留“全部账号”与全部会话入口，不把 `channel.view` 变成会话查看的附加门槛。缺少渠道查看权限时，只隐藏具体员工号导航和员工号搜索，统计由当前已加载会话计算；已有“全部账号下选择客户时来源员工号亮显”的行为在有渠道查看权限时保持不变。
- `channel.view` 控制企微实例列表和账号导航；会话页开户流程需要 `channel.view + channel.create`，因为创建后还要轮询登录状态和刷新实例；账号设置入口需要 `channel.update` 或 `channel.delete`。
- Manager 自身独立执行同一边界：无 `channel.view` 时不加载、不渲染；create/update/delete 分别控制新增、编辑和删除；“更换登录员工号”使用 `channel.update`。异步函数也有相同守卫，不能依赖按钮隐藏代替接口鉴权。
- Manager 的知识库与客户企业辅助选项只在分别持有 `knowledgeBase.view`、`company.view` 时请求；没有客户企业查看权限时，编辑表单不渲染公司选择字段并保留原绑定，避免空选项把既有公司静默清零。
- 模型设置读取和保存继续使用 `aiConfig.view/update`。客户企业详情的企微账号区由 `channel.view` 控制，公司专属远程开户链接由 `channel.view + channel.create` 控制；公司模型保存回调补充 `aiConfig.update` 守卫。

### 契约、验证与并行合并

- 修改 `web/app/dashboard/conversations/page.tsx`、`web/components/wxwork-protocol/wxwork-protocol-instance-manager.tsx`、`web/app/dashboard/company-detail/page.tsx`，新增 `web/app/dashboard/conversations/wxwork-account-permissions.test.mjs`。没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload 或权限常量变化。
- 定向 4 项、全前端 103 项契约测试、`pnpm typecheck`、目标 ESLint、Next 生产构建、`go test ./... -count=1`、`go vet ./...` 和 `git diff --check` 通过。目标 ESLint 仅保留会话页原有二维码 `<img>` 性能 warning，无新增 error 或 hooks warning。
- `origin/codex/ai-billing@f2d2da4` 大幅修改同一 Manager，新增欢迎语、意图行业、保留旧号的替换链接和模型连通测试。合并时必须在最终 Manager 保留本批权限变量：欢迎语和替换链接使用 `channel.update`，意图行业选项读取使用 `aiConfig.view`，模型测试使用 `aiConfig.update`；其新增函数也必须有动作守卫。
- 本批可按三个页面/组件、测试和本节文档整体回滚，不需要数据回滚；回滚会恢复只读账号的跨资源请求和误导写入口。公开邀请注册仍需等待 AI 分支新增模型 Tenant 契约和合并后双租户验收。

## 52. 当前实施检查点：客户企业档案动作权限（2026-07-15）

本检查点继续遵循“现有页面职责不变、每个动作复用权限管理中的全局权限”。`/dashboard/companies` 仍是租户内客户企业档案页，不承担平台租户接入职责，也不新增平行公司管理入口。

### 页面动作与辅助入口

- `company.create`、`company.update`、`company.delete` 分别控制新增、编辑/启停和删除；页面按钮、状态菜单和实际调用函数使用同一权限，不能只依赖后端 403 或按钮隐藏。
- `channel.view` 只控制进入客户企业的企微账号明细，不成为客户企业列表的附加门槛；缺少该权限时不展示“账号列表”。
- `aiConfig.view` 控制公司模型设置读取和入口，`aiConfig.update` 独立控制字段编辑与保存。读取权限不再隐含写权限，保存函数也执行二次守卫。
- 只读账号继续使用查询、筛选和刷新；当没有任何行级动作时隐藏空操作列，避免制造可操作错觉。

### 契约、验证与并行合并

- 修改 `web/app/dashboard/companies/page.tsx`，新增 `web/app/dashboard/companies/action-permissions.test.mjs`。没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket payload、权限常量、导航或 JsonResult 变化。
- 定向 3 项、全前端 106 项契约测试、`pnpm typecheck`、目标 ESLint、Next 生产构建、`go vet ./...` 和 `go test ./... -count=1 -p 1` 通过。
- 标准包级并发 `go test ./... -count=1` 仍会间歇触发既有测试全局 DB/配置互相覆盖，`internal/services` 单包和全仓 `-p 1` 均通过；该风险已在第 50 批记录，本批没有 Go 或测试基建改动，不能把它误记成本批业务回归或虚报标准命令通过。
- `origin/codex/ai-billing@f2d2da4` 修改同一公司页面，增加意图行业列表、列、表单字段和 `intentProfileId` 提交。最终合并必须同时保留本批全部权限守卫和 AI 分支字段；意图列表只在 `aiConfig.view` 下读取/展示，编辑者无该权限时必须保留既有 `intentProfileId`，禁止以空选项静默清零，创建时才可使用默认 `0`。
- 本批页面、测试和本节文档可整体回滚，无需数据回滚；回滚会恢复只读角色的误导写入口和跨资源 403。

## 53. 当前实施检查点：回复意图平台写边界（2026-07-15）

`ReplyIntentConfig` 经代码调用链确认仍由当前回复 runtime 的意图匹配和模型检测读取，不是历史页面。其后端接口复用 `aiConfig.view/create/update/delete`，写权限均为 platform scope；本批不新增隐藏权限或重复的意图管理权限。

### 页面与权限

- 持有 `aiConfig.view` 的账号仍可按既有导航和后端范围查看回复意图；查看不隐含新增、编辑、启停或删除。
- 新增、编辑/启停、删除分别要求平台账号且具备 `aiConfig.create/update/delete`。按钮和操作列按此显隐，实际写函数也重复校验。
- 租户账号即使因错误角色关系出现平台权限码，也不能在前端获得平台写入口；后端 platform scope 校验继续作为最终边界。

### 验证与并行合并

- 修改 `web/app/dashboard/reply-intent-configs/page.tsx`，新增 `web/app/dashboard/reply-intent-configs/platform-permissions.test.mjs`；没有 model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、权限常量、角色或导航变化。
- 定向 1 项、全前端 107 项测试、typecheck、目标 ESLint、Next 生产构建、`go vet ./...` 和 `go test ./... -count=1 -p 1` 通过。
- `origin/codex/ai-billing@f2d2da4` 在同一页面增加意图行业数据、筛选、列、表单字段和 `intentProfileId`。最终页面必须保留这些字段，同时保留本批平台身份与三项写权限守卫；意图行业选项读取继续属于 `aiConfig.view`，不能借由选项加载放宽写能力。
- 本批可整体回滚且不需要数据回滚；回滚会重新向只读或租户账号展示必然失败的平台写动作。

## 54. 当前实施检查点：知识库分层动作权限（2026-07-15）

知识库页面保留现有一页式工作区，不拆分重复入口；左侧知识库档案、右侧文档/FAQ、检索日志与调试分别复用后端已经存在的权限组。

### 页面职责与权限

- 左侧知识库新增、编辑/排序/重建索引、删除分别使用 `knowledgeBase.create/update/delete`；只读账号仍可搜索、选择和刷新知识库。
- 普通知识文档的查看、新增、编辑/重建索引、删除分别使用 `knowledgeDocument.view/create/update/delete`。无写权限时不展示更多菜单，所有函数保留二次守卫。
- FAQ 的查看、新增/导入、编辑/重建索引、删除分别使用 `knowledgeFAQ.view/create/update/delete`；导入弹窗不会向无 create 权限账号挂载。
- 检索日志与调试搜索/回答按当前后端契约继续依赖 `knowledgeDocument.view`。只有 `knowledgeBase.view` 的门店员工可看知识库归属，但不会再请求文档、FAQ、日志或调试接口并产生 403。

### 验证与合并边界

- 修改知识库页面及知识库、文档、FAQ 三个组件，新增权限契约测试和双语无权状态；没有后端、model、migration、DTO、enum、API、路由、WebSocket、权限常量或角色变化。
- 定向 4 项、全前端 111 项、typecheck、目标 ESLint、Next 生产构建、`go vet ./...` 和 `go test ./... -count=1 -p 1` 通过；本地 3000 开发页完成超级管理员实页检查，无布局重叠或知识库相关控制台错误。
- AI 分支新增 FastGPT 文件和图片资源 Tab。图片资源 list/sync/delete 当前分别使用 `knowledgeBase.view/update/delete`，员工号辅助列表还需要 `channel.view`；最终前端必须按这些权限条件加载和显示。
- AI 分支 FastGPT 文件的初始化、上传和删除 handler 当前错误地只要求 `knowledgeBase.view`。这是合并前阻断项：必须先把读、创建/上传、删除映射到明确可分配权限并补 handler/前端测试，不能把只读权限用于远端写操作，也不能仅靠隐藏按钮补救。
- AI 分支知识库编辑器新增意图行业选项；该选项只在 `aiConfig.view` 下加载，无权限编辑既有知识库时必须保留 `intentProfileId`，不能以空选项清零。

## 55. 当前实施检查点：客服组织页面动作权限（2026-07-15）

客服档案页继续承担综合客服组、客服资源、小组编排和轻量服务范围配置，不新增平行组织页面。综合客服组负责服务范围与资源池，客服小组负责组内可重复成员编排和排班；客服可同时进入多个小组，这一已确认产品逻辑必须在后续合并中保留。

### 页面职责与权限

- `agent.view` 继续提供客服档案列表。档案新增、编辑、删除分别使用 `agent.create/update/delete`，按钮、操作列和异步函数使用同一权限守卫。
- 新建客服档案必须同时具备 `agentTeam.view` 和 `user.view`，因为表单需要选择可管理综合组和已存在账号；缺少辅助查看权限时跳过对应接口并禁用选择器，编辑既有记录时保留原 ID。
- 综合客服组与服务范围使用 `agentTeam.view/create/update/delete`。公司主管 `tenant_admin` 可以参与本租户组长和组织配置，但仍必须拥有权限管理页面赋予的对应动作权限，不引入角色隐藏授权。
- 客服小组沿用 `agentTeam.create/update/delete`：创建小组用 create，编辑及拖拽/批量调整成员用 update，删除用 delete。只读账号仍可查看小组、成员和当班状态，但不能拖拽或打开写弹窗。
- 小组排班入口同时要求 `agentTeamSchedule.view + agentTeamSchedule.create`。只拥有排班查看权限时不显示“创建排班”快捷动作，不把查看隐含成写入。

### 验证与合并边界

- 修改客服档案页、综合组侧栏、综合组编辑、客服档案编辑和小组编排组件，新增权限契约测试；没有 Go、model、AutoMigrate、DML migration、DTO、enum、API、路由、WebSocket、权限常量、默认角色、AI runtime、token、usage 或计费变化。
- 定向 5 项、全前端 116 项、typecheck、目标 ESLint、Next 生产构建、`go vet ./...`、`go test ./... -count=1 -p 1` 和 diff 检查通过。目标 ESLint 仅保留原有客服头像 `<img>` 性能 warning。1280x720 实页检查确认成员、小组和组操作可见，小组双列无横向溢出，控制台无 error/warning。
- `origin/codex/ai-billing@f2d2da4` 同时修改客服档案页、档案编辑、组编辑和组侧栏，并缺少当前小组页面；其分支状态还会删除小组 handler、repository、service、测试和派单工作台小组能力。客服小组是已确认业务能力，这些删除是合并阻断项，必须手工保留本分支完整小组与派单契约，再叠加 AI 分支字段，禁止整文件选边。
- 本批可按五个页面组件、权限测试和两份文档整体回滚，不需要数据库处理；回滚会恢复误导性写入口和辅助资源 403，但不应被用来删除既有客服小组业务。

## 56. 当前实施检查点：租户账号动作权限与安全删除（2026-07-15）

账号管理继续复用 `/dashboard/users`，并保持“账号只能绑定角色、角色才能绑定权限”的全局权限派发制。页面不存在账号级权限编辑入口；角色选项只展示后端标记为 `assignable` 且启用的角色，同级/上级角色、平台/租户 scope 和目标账号范围仍由 service 最终校验。

### 页面动作边界

- 创建、更新/启停/重置密码、删除和分配角色分别使用现有 `user.create/update/delete/assignRole`。角色选择还要求 `role.view`；缺少角色分配能力时创建账号会强制提交空 `roleIds`。
- 所有写按钮、抽屉 `open` 和异步函数使用同一权限守卫。编辑自己仅允许当前后端支持的基础资料更新；角色、状态、密码重置和删除不能作用于自己或不可管理的同级/上级账号。
- 门店员工客服组筛选要求 `agentTeam.view`，反向绑定要求 `agentTeam.view + agentTeam.update`，并继续同步门店员工绑定和企微员工号归属。
- 邀请查看/重置使用 `tenantInvite.view/rotate`；注册列表/审核使用 `tenantRegistration.view/review`。批准注册还要求 `user.assignRole + role.view`，拒绝不需要角色分配权限。
- `user.delete` 复用现有软删除语义并在页面明确区分临时“禁用”。删除会注销登录会话；若账号仍有未完成会话、综合组/小组组长职责、客服档案或门店员工绑定，则 service 拒绝删除，要求先转派、换组长、删除档案或解绑，避免孤立业务关系和已删除客服继续被手动派单。
- 禁用账号保留客服档案和历史任务，但不能再作为分配或转派目标；派单负载视图仍显示该客服并标记为不可用。自动派单、旧会话分配接口和派单工作台统一要求 User 与 AgentProfile 同时启用。

### 契约与验证

- 新增前端账号权限测试和 service 删除依赖测试；修改账号页、创建/邀请/注册审核组件、API client、双语提示、账号 service、客服档案可分配判断和两条人工派单链路。没有 model、AutoMigrate、DML migration、DTO、enum、Gin 路由、WebSocket payload、权限常量、默认角色、导航或 AI/计费变化。
- 定向前端 5 项、全前端 121 项、删除依赖 5 类阻断和无依赖成功路径、禁用账号人工派单阻断、typecheck、目标 ESLint、Next 生产构建、`go vet ./...`、`go test ./... -count=1 -p 1` 与 diff 检查通过。1280x720 实页确认账号表格无横向溢出，菜单包含分配角色、重置密码、禁用和删除，控制台无 error/warning；未执行真实删除。
- `origin/codex/ai-billing@f2d2da4` 修改同一账号页、创建/角色组件、API client、user handler/service/DTO、客服档案与会话 service，并删除派单工作台及小组测试；同时缺少邀请和注册审核组件。最终合并必须保留本批权限守卫、安全删除依赖、禁用账号派单阻断及完整租户邀请审核流程，再叠加 AI 分支字段；禁止整文件选边或接受组件删除。
- 回滚前端会重新隐藏 `user.delete` 并恢复写函数缺少二次守卫；回滚 service 会使外部直接调用删除接口重新产生悬空业务关系，因此两部分应作为同一安全边界回滚，不需要数据库迁移。

## 57. 当前实施检查点：门店员工自助工作台与角色收口（2026-07-15）

本检查点把原静态 `/dashboard/store-workbench` 替换为门店员工当前账号专属的真实工作台。页面继续承担门店接待配置和运行状态查看，不并入企微员工号管理页、客服组织配置页或会话工作台，也不向请求暴露任意账号、门店、绑定或实例 ID。

### 账号范围与真实运行链路

- 新增租户级 `storeWorkbench.view` 与 `storeWorkbench.update`，均进入权限管理和角色管理；前者控制工作台、通知群及群成员只读数据，后者控制当前门店员工配置保存。
- `GET /api/dashboard/store-workbench/current`、`POST /update`、`POST /room_list`、`POST /room_member_list` 全部由登录主体的 `UserID + ActiveTenantID` 反查唯一 `StoreStaffBinding`，再解析同租户门店、综合客服组、企微员工号和知识库。接口不接收可替换主体范围的 ID。
- 一个账号出现多个有效门店绑定、一个绑定解析出多个企微实例、门店不存在或跨租户引用时均 fail closed；未绑定账号返回明确空态，停用绑定只读且禁止保存。
- 保存同步更新门店员工绑定与其企微实例，事务内统一写入托管模式、服务时段、门店群、@ 成员、人工超时和总部兜底；门店地址、导航名称与经纬度只在已绑定实例上更新。
- 门店群和成员选择复用企业微信员工号协议 `/room/get_room_list`、`/room/batch_get_room_detail`、`/room/batch_get_member_detail`；通知群使用 `R:` conversation ID，`@全员` 使用协议定义的 `0`。没有协议结果时不提供手填 ID 假入口。

### 门店员工角色与界面边界

- 内置 `store_staff` 角色默认只保留 `storeWorkbench.view/update`。migration 55 幂等创建权限和角色关系，migration 56 只清理该内置角色的历史宽权限，不修改任何租户自定义角色权限。
- 侧栏工作台入口不再借用 `channel.view`；门店员工不再默认看到公司会话、企微账号管理、AI、知识库、客户、通知和其他后台模块。
- Dashboard Layout 复用导航配置的权限和平台/公司上下文做直链守卫。手工打开无权模块会跳到第一个可访问页面；客户企业详情和通知中心补充显式附属路由规则。没有任何可访问模块的账号仍可在 `/dashboard` 看到原有空权限状态，避免重定向循环。
- 账号下拉菜单的通知入口按 `notification.view` 隐藏；修改当前账号密码与退出登录继续保留，不把认证级自助动作误做成业务权限。

### 契约、验证与并行边界

- 新增 request/response DTO、builder、service、handler、四条 Gin 路由和 DML migration 55/56；没有 model、AutoMigrate、enum、WebSocket payload、AI runtime、模型供应商、token、usage 或计费变化。
- service 测试覆盖当前账号/当前租户隔离、绑定与实例同步、危险配置拒绝、重复绑定 fail closed 和停用绑定；migration 测试覆盖幂等、内置角色收口及自定义角色保留；handler 和前端测试覆盖显式权限、无任意主体 ID、协议选择器和直链守卫。
- 全量 Go、vet、127 项前端测试、TypeScript、目标 ESLint、Next 生产构建及 diff 检查通过。模拟门店员工在 1280x720 与 390x844 下只看到工作台，无横向溢出或控制台错误；无权会话直链自动返回工作台，验证期间未保存配置或修改业务数据。
- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/bootstrap/routes.go`、`internal/bootstrap/server.go` 和 `web/lib/navigation.tsx`。最终必须手工保留本批工作台路由、`storeWorkbench.view`、直链守卫与 AI 分支回复意图行业入口；AI 分支最高 migration 33，与 55/56 不冲突。
- 本批前后端、权限常量和两个 migration 构成同一边界。只回滚页面会留下无入口权限，只回滚 migration 会恢复门店员工历史宽权限；已执行 migration 不应改号或改 remark，撤销产品能力需另做幂等 DML。

## 58. 当前实施检查点：租户数据一致性只读审计（2026-07-15）

本检查点完成阶段 4 约定的部署前一致性检查命令。它是上线和合并后的独立 preflight，不进入 Dashboard 页面、不新增权限，也不以修复脚本替代业务确认。

### 审计范围与只读边界

- 新增 `cmd/tenant_integrity_audit`，参数为 `-config`、`-sample-limit` 和 `-pretty`；Makefile 提供 `tenant-integrity-audit` 入口。输出始终为结构化 JSON，通过返回 0，发现违规返回 1，配置、连接或查询错误返回 2。
- 命令只加载配置并调用 `bootstrap.InitDB`，不调用 `bootstrap.Init`、`InitMigrations`、`AutoMigrate` 或 DML migration。SQLite 在连接前确认数据库文件已经存在并强制 `mode=ro`；SQLite/MySQL 检查均运行在 `sql.TxOptions{ReadOnly:true}` 事务内。
- 当前 `models.Models` 中 51 个含 `TenantID` 的模型全部有显式策略。测试会反射注册模型并与策略表双向比较，未来新增、删除或移除 TenantID 却未同步策略时直接失败，不能静默漏审。
- 允许平台态零租户的 User、TicketView、Notification、Asset，以及注册失败日志、未绑定企微实例、脱离会话的消息同步日志和中断检查点均使用独立条件；其他租户模型要求 `tenant_id > 0`。所有正租户值还必须引用真实 Tenant。
- 审计覆盖 64 张当前必需表和 125 条关系：缺表/缺列、必填父级为空、正数外键孤儿、父子 TenantID 不一致均形成稳定违规码和受 `sample-limit` 限制的样本 ID。
- 权限体系额外检查角色和权限 scope 合法性、租户账号持有平台角色、平台账号持有租户角色、租户角色持有平台权限。操作人字段只检查引用存在，不强制与业务记录同租户，避免把平台管理员在活动公司内的合法操作误判为串租。

### 验证、上线使用与剩余边界

- service 测试覆盖双租户干净数据、缺表、零/负租户、未知租户、孤儿、跨租户关系、三类角色权限 scope 冲突和样本上限；命令测试以只有标记表的真实 SQLite 文件执行，确认运行前后表结构和数据完全不变。
- `/tmp/agentdesk-tenant-stats.db` 实际只读审计通过：51/51 模型策略、64/64 必需表、125/125 关系，违规为 0；文件修改时间未变化。没有生成或提交 `docs/generated/` 报告。
- 全仓串行 Go 测试、`go vet ./...`、127 项前端契约、TypeScript 和 Next 生产构建通过。没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、WebSocket、前端页面、AI runtime、token、usage 或计费变化。
- 正式部署应先在数据库备份和只读账号上运行本命令；返回 1 时按违规码人工确认归属并用单独、可审查的修复步骤处理，命令本身永不自动改数据。第 59 批又补充三项同租户业务标识重复检查并完成对应组合唯一 DDL；审计通过与索引结构仍是两类证据，均需验收。
- `origin/codex/ai-billing@f2d2da4` 不修改本批新增命令、repository、service 或 Makefile；其对 `internal/models/models.go` 的最终合并若新增 TenantID 模型，会由策略覆盖测试阻断并要求显式登记。本批不需要 migration 合并顺序，可独立回滚新增代码、测试、Makefile 入口和本节文档。

## 59. 当前实施检查点：租户业务标识组合唯一（2026-07-15）

本检查点处理多租户运行时隔离完成后仍存在的三个历史全局唯一约束。客户企业名称、门店编码和客服工号属于租户业务空间；继续全局唯一不会越权，但会让两个互相隔离的客户公司无法使用各自常见名称和内部编码。

### 数据契约与业务链路

- `Company` 使用唯一索引 `uk_company_tenant_name(tenant_id,name)`；`Store` 使用 `uk_store_tenant_code(tenant_id,store_code)`；`AgentProfile` 使用 `uk_agent_profile_tenant_code(tenant_id,agent_code)`。
- Company 创建/更新重复查询改为当前 `ActiveTenantID` 内查询，客服档案工号按目标综合客服组 TenantID 查询；并发竞态下的数据库唯一错误转换为原有明确业务提示，不透传底层 SQL。
- 同一租户仍禁止重复，包括已软删除 Company 占用的名称；不同租户允许相同名称、门店编码和客服工号。ChannelID、TenantCode、法定注册号、用户名/手机/邮箱、企微 GUID、OpenKfID 和 TicketNo 的全局唯一语义不变。
- 仿真工具的 StoreCode/AgentCode upsert 同步按租户匹配；只有 `tenant_id=0` 且带当前 batch 标记的历史测试行允许被修复。其他正租户同值记录不会被读取或改写，report 的 CompanyNameExists 也限定 `legacy-default`。
- 第 58 批只读审计新增 `DUPLICATE_TENANT_COMPANY_NAME`、`DUPLICATE_TENANT_STORE_CODE`、`DUPLICATE_TENANT_AGENT_CODE`，在 DDL 前即可给出总数和样本 ID。

### 旧库升级与双数据库边界

- GORM AutoMigrate 会创建新索引，但不会删除旧 `uk_company_name`、`idx_t_store_store_code`、`idx_t_agent_profile_agent_code`。启动顺序因此固定为：AutoMigrate 创建新索引 -> 校验新索引唯一性和列顺序 -> 校验旧索引仍是预期单列唯一 -> 幂等删除旧索引 -> 执行 DML migration。
- 索引检查通过 SQLite PRAGMA 和 MySQL `information_schema.statistics` 的只读结构化查询实现；不依赖字符串解析，也不使用数据库私有 DDL 拼接。任何索引缺失、列顺序错误、非唯一或旧索引形态漂移都会 fail closed。
- 该结构变化不占用 `internal/migration` 版本，因为该 runner 只允许 DML；新组合索引仍由 AutoMigrate 创建，兼容步骤只负责 GORM 不会自动执行的旧索引退役。
- SQLite 真实测试库副本从旧索引升级后，Company/Store/AgentProfile 行数保持 1/100/12；三项新索引列顺序正确，旧索引消失，重复启动无输出且无变化。事务内跨租户同名成功，同租户重复被约束拒绝，升级后完整租户审计为 0 违规。
- Docker MySQL 8.4 独立临时数据库完成新库启动、人工模拟旧索引、再次启动升级和索引查询；三项均得到 `NON_UNIQUE=0` 且列顺序正确。跨租户同名插入成功，同租户重复返回 1062。临时数据库、配置和二进制均已清理，运行库未修改。

### 验证、合并与回滚

- 测试覆盖新库索引、旧库升级、幂等清理、意外旧索引形态拒绝、同租户历史重复导致 AutoMigrate 失败且数据不变、Company/AgentCode 跨租户复用、仿真工具不命中异租户和审计重复样本上限。
- 聚焦 race、全仓串行 Go、`go vet ./...`、127 项前端契约、TypeScript、Next 生产构建和 diff 检查通过。没有 DTO、enum、API、Gin 路由、WebSocket、权限、页面或 AI/计费语义变化。
- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/models/models.go` 和 Company service，最终必须手工保留本分支 TenantID、三个组合索引、租户内重复校验及 AI 分支 Company `IntentProfileID` 等字段，不能整文件选边。
- 一旦不同租户已经使用相同值，旧全局唯一索引无法恢复。代码回滚必须保留三个组合索引；强行恢复旧索引会失败或要求删除合法租户数据。启动兼容代码可在确认所有环境旧索引均退役后另批移除，不能随业务代码回滚自动反向建索引。

## 60. 当前实施检查点：独立迁移命令失败语义（2026-07-15）

MySQL 索引演练发现 `cmd/migration` 在配置、连接或迁移失败时只记录日志并从 `main` 返回，操作系统退出码仍为 0；部署脚本因此可能把失败当作成功。本检查点只修复命令生命周期，不改变任何迁移内容或顺序。

- migration 命令新增与 server 一致的 `-config` 参数，默认仍为 `config/config.yaml`；`make migration CONFIG=/path/config.yaml` 会把指定路径传给命令。
- 配置加载、数据库初始化、连接获取和 `InitMigrations` 任一步失败都会包装阶段信息并由 `main` 执行 `os.Exit(1)`；完整成功才记录 `migrations completed` 并返回 0。
- 命令在退出前关闭连接池。该关闭只影响独立进程，不改变 server 启动时持有的数据库生命周期。
- 子进程测试制造同租户重复 Company 数据，证明 AutoMigrate 失败时进程返回非零且原数据不变；另一子进程通过显式 config 对新 SQLite 完整迁移，证明成功退出 0 且 migration 成功记录存在。
- 本批只修改 `cmd/migration`、Makefile、测试和文档；没有 model、DDL/DML migration、DTO、enum、API、权限、前端或 AI/计费变化。AI 分支没有同文件修改，可独立合并和回滚。

## 61. 当前实施检查点：客户展示补充数据逐关系租户校验（2026-07-15）

客户列表、详情和门店关系接口已经按活动租户读取 Customer 与 StoreCustomerRelation，但 `LoadPresentationData` 随后仅按关联 ID 全局批量读取 Store 和 WxWorkProtocolInstance。历史脏关系若指向其他租户，当前租户响应会带出对方门店名称和企微员工号名称；Company 虽有允许租户集合条件，混合租户批次仍缺少逐条匹配证据。

### 数据边界与现有链路复用

- 本批复用现有 Customer handler、service 聚合和 builder，不新增页面、接口、状态或平行模型。Customer 确定 Company 的预期租户，StoreCustomerRelation 必须与所属 Customer 同租户，关系再确定 Store 和 WxWorkProtocolInstance 的预期租户。
- Company、Store 和 WxWorkProtocolInstance 查询同时增加租户集合条件，返回后再逐 ID 校验预期 TenantID。同一 ID 出现矛盾租户证据时拒绝补充，避免未来跨租户批量调用时因 `IN (tenantIDs...)` 形成交叉匹配。
- 当前租户自己的关系记录继续返回，但跨租户 Company/Store/WxWorkProtocolInstance 不进入 builder 上下文，因此不显示外租户名称。读取接口不隐式删除或修复历史记录，数据问题继续由只读完整性审计报告并通过独立 DML 批次处理。

### 验证、合并与回滚

- 修改 `internal/services/customer_service.go` 和 `internal/services/customer_service_test.go`，并同步两份权威文档。没有 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、权限、WebSocket、前端或 AI runtime 变化。
- 双租户回归构造 tenant 101 Customer/Relation 指向 tenant 202 Company/Store/WxWorkProtocolInstance，确认关系仍可见而三个外租户展示对象均被拒绝；正常同租户聚合继续通过。聚焦 race、services 全包、全仓 Go、`go vet ./...` 和 diff 检查通过。
- `origin/codex/ai-billing@f2d2da4` 没有本批展示聚合实现，相对共同基线也未修改 `customer_service.go`，当前无同文件冲突。最终合并后仍须确认本分支租户版 Customer service 和本批回归测试被保留；本批不需要 migration 排序或前置 rebase。
- 可独立回滚 service 与测试，不涉及数据库回滚；回滚会恢复脏跨租户关系的展示信息泄漏。若需清理脏记录，应依据只读审计结果另做幂等、可审查的数据修复，不能在读取路径自动改写。

## 62. 当前实施检查点：异步业务通知租户边界（2026-07-15）

会话和工单分配事件由事务提交后异步处理，原 handler 只按业务 ID 全局读取 Conversation/Ticket，再由 NotificationService 按接收账号推导 TenantID。正常派单虽然已校验接收人，但错误事件、历史数据或后续调用点可把租户 A 的业务摘要写成租户 B 的通知；企微通知的全局默认接收人还可能在目标客服未绑定企微时收到其他租户业务内容。

### 复用现有通知体系

- NotificationService 保留通用 `Create/CreateAndPush`，新增 `CreateInTenant/CreateAndPushInTenant`。租户业务调用必须用业务实体 TenantID 读取接收账号，账号不存在于该租户时不创建通知、不推 WebSocket。
- 工单分配、会话分配和总部转人工站内通知均切换到租户入口；事件结构不增加重复 TenantID，Conversation/Ticket 仍是权威租户来源。
- WxWorkNotifyService 新增租户入口。指定处理人必须属于目标租户；没有企微身份时，只允许回退到配置中的同租户账号或平台账号，其他租户默认接收人被过滤。处理人、接入渠道等通知文案补充数据也按实体租户读取。
- 这是通知投递边界，不改变派单、工单、角色权限或通知查看语义；平台账号进入默认接收范围是显式运维例外，租户账号不能跨公司接收。

### 验证、并行边界与剩余阻断

- 双租户测试覆盖错配 Ticket/Conversation 事件不产生站内通知、`CreateInTenant` 拒绝外租户接收人、企微 fallback 只保留目标租户与平台账号；原同租户通知、未读数、转人工及 event handler 测试继续通过。
- 聚焦 race、services/event_handlers 全包、全仓 `go test ./... -count=1 -p 1`、`go vet ./...` 和 diff 检查通过。没有 model、AutoMigrate、DML migration、DTO、enum、事件结构、API、权限、WebSocket payload、前端或 AI/计费变化。
- `origin/codex/ai-billing@f2d2da4` 同时修改 `conversation_human_dispatch_service.go` 的门店群转人工文案，本批修改同文件的总部站内通知，最终需逐段合并：保留 `CreateAndPushInTenant`，同时保留新版门店群文案；其新增客户名回查必须改用 `GetByTenantID(conversation.CustomerID, conversation.TenantID)`。其余本批文件无同文件冲突，也无 migration 排序要求。
- 同轮审计确认媒体理解任务仍须在模型调用前验证 Message/Conversation 同租户，`ResolveForMessage` 须使用租户 route，企微语音须按租户读取 Channel。相关文件正在 AI 分支承载 usage/计费改动，本分支按协作边界未修改；最终合并必须由 AI 负责人一起落地并补非 HTTP 双租户回归，否则公开邀请注册不得启用。
- 本批可独立回滚通知 service、调用点和测试，不涉及数据库回滚；回滚会恢复错误事件跨租户通知与企微 fallback 串租风险，因此不建议回滚。

## 63. 当前实施检查点：知识候选证据消息租户校验（2026-07-15）

KnowledgeCandidate 的 Conversation、Store 和 KnowledgeBase 已在 Upsert 时合并校验 TenantID，但 `MessageIDs` 以逗号文本保存，原实现直接持久化调用方传值。它既不是数据库外键，也无法由普通父子关系审计逐项验证，因此外租户消息或同租户其他会话消息可能被记录为当前候选的证据。

- `UpsertCandidate` 在问题去重和写入前统一校验证据：空列表继续允许；非空 ID 必须为正数，去重后每条 Message 必须属于候选 TenantID；候选带 ConversationID 时，每条 Message 还必须属于该会话。
- 外租户、缺失或非法消息统一返回“消息证据不存在或归属不一致”，不暴露其他租户是否存在该 ID；同租户但来自其他会话的证据返回明确会话归属错误。验证失败不会创建候选，也不会增加已有候选频次。
- 人工会话分析、人工接待结束后的自动提取及未来 Upsert 调用点复用同一 service 边界；不改变候选生成条件、质量判断、审核、导出或知识库内容。
- 双租户测试覆盖同一会话重复证据去重保存、tenant 202 消息不能进入 tenant 101 候选、tenant 101 另一会话消息不能进入当前候选。聚焦 race、services 全包、全仓 Go、vet 和 diff 检查通过。
- 只修改 `knowledge_candidate_service.go`、既有 `knowledge_tenant_service_test.go` 与两份文档；无 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端或 AI/计费变化。AI 分支当前无同文件修改，不需要 rebase 或 migration 排序。
- 可独立回滚代码、测试与文档，不涉及数据库回滚；回滚会恢复不可审计的跨租户/跨会话文本证据风险。历史数据如需检查，应新增显式解析 MessageIDs 的只读审计项，不能在读取候选时自动删除。

## 64. 当前实施检查点：动态业务引用只读审计（2026-07-15）

第 58 批的 125 条普通关系可以审计固定外键列，但不能表达 Notification 的 `biz_type + biz_id` 多态引用，也不能解析 KnowledgeCandidate 的逗号文本 `message_ids`。第 62/63 批已阻止新错误写入，本批补历史数据的只读发现能力，不自动修复。

- Notification 对 `biz_type=conversation` 和 `biz_type=ticket` 分别 LEFT JOIN 对应父表；业务对象不存在或与通知 TenantID 不一致时报告 `DYNAMIC_TENANT_RELATION_MISMATCH`，entity 区分 `Notification.conversation` 与 `Notification.ticket`。
- KnowledgeCandidate 由 repository 只读返回 `id/tenant_id/conversation_id/message_ids`，service 严格解析正整数并去重，再按 500 条批量读取 Message。非法文本、缺失消息、消息跨租户或不属于候选会话时，按候选记录报告 `KNOWLEDGE_CANDIDATE_MESSAGE_EVIDENCE_MISMATCH`。
- 两项检查使用原 `sampleLimit`，总数按通知/候选记录计数，样本 ID 升序且有限；它们不伪装成普通外键，因此 `configuredRelations/checkedRelations` 继续保持 125/125。
- 测试同时放入有效和错误数据：两类动态通知各 1 条违规，三个知识候选分别覆盖跨租户、跨会话和非法 ID，另有有效候选不误报；`sampleLimit=1` 时总数保留、样本各 1 条。
- 实际 `/tmp/agentdesk-tenant-stats.db` 只读审计仍通过：51/51 模型策略、64/64 表、125/125 普通关系、0 违规。执行前后文件修改时间 `1784055363`、大小 `4878336` 字节均未变化。
- 聚焦 race、全仓 Go、vet 和 diff 检查通过。只修改审计 repository/service/test 与两份文档；无 model、migration、DTO、API、权限、WebSocket、页面或 AI/计费变化。查询只使用 SQLite/MySQL 共同支持的 JOIN、IN、排序和基础比较。
- AI 分支当前无同文件修改，不需要 rebase 或 migration 排序。可独立回滚本批代码、测试和文档，无数据库回滚；回滚只会失去历史动态引用检测，不能以删除违规业务数据代替本检查。

## 65. 当前实施检查点：公司主管角色分配边界复核（2026-07-15）

本批按已确认方案复核账号创建、账号角色调整、邀请注册审核和全局角色管理四条真实入口。结论是现有实现已形成完整边界，无需新增平行权限、租户角色副本或隐藏授权逻辑。

- Role 继续作为平台预设模板全局存在，不按租户复制。租户账号可在具备 `role.view` 时读取角色模板，但响应中的 `assignable` 由操作者最高角色等级、角色状态和 scope 共同计算；公司主管只能选择低于自身的 tenant 角色。
- `UserService.CreateUser` 和 `UserService.AssignRoles` 最终都复用 `replaceUserRolesDB`。目标账号先受当前租户 scope 与账号等级约束，待分配角色再受禁用状态、同级/更高等级和 platform/tenant scope 约束；事务失败会回滚账号或保留原角色。
- 邀请注册审核先按 `ActiveTenantID` 读取待审核账号，并要求 `tenant.registration.review` 与 `user.assignRole`；审核角色仍复用同一 service 校验。公司主管不能审核其他公司账号，也不能借审核分配 `tenant_admin` 或平台角色。
- 角色创建、更新、删除、状态、排序和角色权限调整均通过 `requireRolePlatformPermission`，同时要求对应权限码与 `IsPlatformAccount=true`。即使租户账号因错误数据持有平台角色写权限，也无法修改全局 Role。账号管理只提交 `roleIds`，没有用户级 `permissionIds` 或直接权限分配入口。
- 前端创建账号和注册审核只展示 `assignable && enabled` 的角色；角色调整抽屉可以展示目标账号已有但当前不可分配的角色以解释现状，但禁用选择并标记“不可分配”。角色管理写操作同时按平台账号和明确动作权限显隐。
- 聚焦 race 测试覆盖公司主管给本租户客服分配低级角色、拒绝同级公司主管和平台角色、拒绝跨租户账号、租户账号误持平台权限仍不能写全局角色、平台管理员不能管理同级/更高账号；前端权限契约测试确认账号页不直接分配权限。全部通过。
- 本批只更新两份文档，无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端运行代码、AI 回复、模型调用、token、usage 或计费变化。`origin/codex/ai-billing@f2d2da4` 无同文件运行代码冲突，不要求 rebase、migration 排序或特殊合并顺序。
- 可独立回滚本节文档，不影响运行时；不得把本结论误读为允许公司主管修改角色模板。未来新增角色仍必须由平台管理员在角色管理中建立并经权限管理分配权限，公司主管只把可分配角色赋给本公司低级账号。

## 66. 当前实施检查点：客服小组自动派单成员租户收口（2026-07-15）

客服小组创建、编辑和拖拽成员已校验综合客服组、客服档案与小组 TenantID，但自动派单使用的 `ActiveMemberProfileSet` 原先只按 squad/profile ID 读取。正常数据不会跨公司，历史脏关系却可能被当作当前排班小组成员，运行时边界不应只依赖事后完整性审计。

- 自动派单把会话 TenantID 传入小组成员筛选；启用小组和启用成员关系都必须同时命中该 TenantID。租户不合法、外租户小组或外租户成员关系均返回空成员集合，使派单保持待处理而不是扩大候选池。
- `filterProfilesByActiveSquads` 继续以已按租户筛出的客服档案和当前排班为入口，只补充同一租户参数；`squadId=0` 的整组排班兼容语义不变，指定小组仍只允许该小组成员进入候选池。
- 小组负责人档案查询在 SQL 条件中增加综合客服组 TenantID，再保留原有 TeamID/TenantID 二次校验。页面、拖拽和编辑接口不变，一个客服仍可同时加入同一综合客服组内多个小组。
- 回归测试直接向 tenant 101 小组插入 tenant 202 的启用成员关系；修复后该关系不能让 tenant 101 客服进入候选池，报告为 `no_matched_profile`。整组排班、正常指定小组、空小组和停用小组测试继续通过。
- 聚焦 race、services 全包、全仓 `go test ./... -count=1 -p 1`、`go vet ./...` 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、页面、AI 回复、模型调用、token、usage 或计费变化。
- `origin/codex/ai-billing@f2d2da4` 不修改本批 service/test，当前无同文件冲突，不要求 rebase 或 migration 排序。可独立回滚两个 service、测试与本节文档；回滚会恢复脏跨租户小组成员影响自动派单候选的风险。

## 67. 当前实施检查点：排班小组与综合客服组运行时一致性（2026-07-15）

第 66 批已按租户过滤自动派单的小组和成员关系，但同一租户内仍有第二层组织语义：排班记录的 SquadID 必须属于该排班的 TeamID。排班创建、更新、批量预览和通用 Create 已通过 `validateScheduleSquadDB` 校验 TeamID/TenantID，本批不重复修改写路径，只收紧历史数据的实时使用。

- `ActiveMemberProfileSet` 在返回启用成员集合的同时返回每个启用小组的 TeamID；两份数据来自同一次 tenant-scoped 小组查询，不增加全局回查或 N+1。
- 自动派单筛选指定小组时，同时要求 `teamBySquad[squadID] == profile.TeamID`。外租户、停用、缺失或属于其他综合客服组的小组均不能使客服进入候选池；整组排班 `squadId=0` 不受影响。
- 为保留既有调试报告语义，错误小组排班仍先被识别为该综合组有排班，再在成员筛选阶段得到 `no_matched_profile`；没有把停用小组从该原因改成 `no_active_schedule_team`。
- 新测试在 tenant 101 内构造综合组 A 的排班指向综合组 B 小组，并伪造 A 客服为 B 小组成员；修复后候选池为空。正常指定小组、整组、空小组、停用小组及第 66 批跨租户成员测试均通过。
- 聚焦 race、services 单包、独立串行全仓 Go、`go vet ./...` 和 diff 检查通过。一次把 services 与全仓测试并行运行的验证因仓库既有全局 `sqls.DB/config` 测试夹具争用出现临时表缺失；两个进程结束后独立 `go test ./... -count=1 -p 1` 完整通过，不把并发验证失败隐瞒或误记为产品回归。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、页面、AI 回复或计费变化。AI 分支无同文件冲突，不要求 rebase 或 migration 排序；可独立回滚 service/test/文档，回滚会恢复同租户跨综合组脏排班影响候选池的风险。

## 68. 当前实施检查点：客服小组组织语义只读审计（2026-07-15）

普通租户关系审计可以确认 Squad/Member/Profile/Schedule 的 TenantID 一致，却无法表达同一租户内的组织约束。第 66/67 批已让实时派单拒绝错误成员和串组排班，本批让这些历史问题也能在上线前被只读命令发现，不增加自动修复。

- 新增 `AGENT_TEAM_SQUAD_MEMBER_TEAM_MISMATCH`：仅检查 `status=OK` 的启用成员关系，要求所引 AgentProfile.TeamID 等于 AgentTeamSquad.TeamID。已移除的历史成员不因客服后来移组而误报。
- 新增 `AGENT_TEAM_SCHEDULE_SQUAD_TEAM_MISMATCH`：当 Schedule.SquadID 大于 0 且父小组存在时，要求 Schedule.TeamID 等于 Squad.TeamID。整组排班 `squadId=0` 不参与该检查。
- 两项均复用 `TenantIntegrityQuery` 的 Count、ID 升序样本和统一 sampleLimit；缺少语义检查所需列时报告 `MISSING_REQUIRED_COLUMN`。它们不伪装成普通外键，因此 `configuredRelations/checkedRelations` 继续保持 125/125。
- 测试在 tenant A 内建立两个综合客服组，分别插入一条合法与一条串组成员、一条合法与一条串组排班；两类违规各报告 1 条且样本精确命中错误记录，合法记录不误报，干净双租户 fixture 继续 passed。
- 实际 `/tmp/agentdesk-tenant-stats.db` 审计继续 passed：51/51 TenantID 模型策略、64/64 表、125/125 普通关系、0 违规。执行前后文件 mtime 均为 `1784055363`，大小均为 `4878336` 字节。
- 聚焦 race、独立串行全仓 Go、`go vet ./...` 和 diff 检查通过。无 model、migration、DTO、API、权限、WebSocket、页面或 AI/计费变化；AI 分支无同文件冲突，不要求 rebase 或 migration 排序。
- 可独立回滚审计 service/test 和本节文档，无数据库回滚；回滚只会失去历史串组发现能力。发现违规后必须另开幂等 DML 修复批次，禁止审计命令自动挪人、改排班或删除记录。

## 69. 当前实施检查点：职责角色移除前业务依赖保护（2026-07-15）

账号删除已要求先转派会话、更换组长、删除客服档案并解除门店员工绑定，但角色调整原先可以直接移除职责角色并保留这些业务对象，造成账号角色与客服组织页面互相矛盾。本批复用现有账号管理与角色分配入口，不增加平行“岗位解绑”页面，也不自动级联删除数据。

- `replaceUserRolesDB` 先去重并完整校验目标角色状态、等级和 scope，形成目标角色集合；再读取账号原 UserRole，只对“原来持有、此次不再保留”的职责角色检查依赖；通过后才删除旧关系并写入新关系。
- 移除 `cs_user` 前要求没有未关闭的当前会话且没有未删除 AgentProfile；移除 `cs_team_leader` 前要求不再是任何未删除综合客服组的 LeaderUserID；移除 `store_staff` 前要求没有未删除 StoreStaffBinding。
- 依赖存在时返回明确处理顺序，整个角色替换事务失败并保留原角色。清理对应会话/档案/组长/门店绑定后可正常移除；只新增角色、保留原职责角色或新建账号不触发无关检查。
- 不把客服小组负责人绑定到 `cs_team_leader`：小组负责人按现有产品设计是本综合组客服档案并自动成为小组成员，因此由 `cs_user + AgentProfile` 依赖保护；综合客服组长才由 `cs_team_leader + Team.LeaderUserID` 表达。
- 测试覆盖未完成会话、有效客服档案、综合组组长、门店员工绑定四个阻断点，确认每次失败后三个原职责角色都完整保留，并在清理依赖后允许移除。原角色等级、跨租户、平台 scope 和创建账号测试继续通过。
- 聚焦 race、独立串行全仓 Go、`go vet ./...` 与 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端或 AI/计费变化；AI 分支无同文件冲突，不要求 rebase 或 migration 排序。
- 可独立回滚 User service、测试与本节文档，无数据库回滚；回滚会重新允许职责角色与业务归属悬空。若未来要支持“一键离职/换岗”，应另建显式预览与事务编排，不得把本保护改成静默级联清理。

## 70. 当前实施检查点：职责对象与账号角色语义审计（2026-07-15）

第 69 批阻止在线角色调整制造新的职责悬空，但历史数据、迁移或手工写库仍可能存在“客服组织对象有效，账号却没有对应启用角色”。本批把相反方向纳入现有只读完整性审计，不自动补角色、不清负责人。

- `AGENT_PROFILE_MISSING_CS_USER_ROLE`：未删除 AgentProfile 的 UserID 必须通过 UserRole 关联启用的 `cs_user`。
- `AGENT_TEAM_LEADER_MISSING_ROLE`：未删除 AgentTeam 的非零 LeaderUserID 必须关联启用的 `cs_team_leader`。
- `STORE_STAFF_BINDING_MISSING_ROLE`：未删除 StoreStaffBinding 的 UserID 必须关联启用的 `store_staff`。
- `AGENT_TEAM_SQUAD_LEADER_PROFILE_MISMATCH`：未删除 AgentTeamSquad 的非零 LeaderUserID 必须存在同 TenantID、同 TeamID、未删除 AgentProfile。小组负责人继续是本综合组客服，不额外要求 `cs_team_leader`。
- 三类职责角色检查使用 `NOT EXISTS(UserRole JOIN Role)` 并要求 Role.Status=OK；被禁用角色不提供可用职责。已删除业务对象不要求继续保留角色，停用但未删除对象仍需保持职责完整，避免再次启用时恢复悬空状态。
- 测试同时建立合法和缺角色的客服档案、综合组组长、门店员工绑定，以及合法和缺本组档案的小组负责人；四类违规各精确报告 1 条，合法记录不误报。
- 实际 `/tmp/agentdesk-tenant-stats.db` 审计继续 passed：51/51 模型策略、64/64 表、125/125 普通关系、0 违规；前后 mtime `1784055363`、大小 `4878336` 字节不变。
- 聚焦 race、独立串行全仓 Go、vet 和 diff 检查通过。无 model、migration、DTO、API、权限、WebSocket、页面或 AI/计费变化；AI 分支无同文件冲突，不要求 rebase 或 migration 排序。
- 可独立回滚审计 service/test 与本节文档，无数据库回滚；回滚只会失去历史职责悬空发现能力。违规修复必须先判断应恢复角色还是解除业务职责，不能由审计命令替业务负责人做决定。

## 71A. 当前实施检查点：账号角色变更追加式审计契约（2026-07-15）

现有 UserRole 只保存当前关系，角色替换会删除旧记录；AuditFields 也只能说明单条关系的创建和更新，无法还原账号角色集合从什么变成什么。TenantRegistrationLog、ConversationEventLog 和登录日志分别属于注册、会话与认证链路，不能复用为角色历史。本批先建立独立、追加式数据契约，下一批再接入所有角色替换入口。

- 新增 UserRoleChangeLog，保存目标 TenantID/UserID、变更前后有序角色 ID JSON、变更前后有序角色 code JSON、操作人 ID/名称和创建时间。租户账号日志必须与目标账号同租户；平台账号日志允许 TenantID=0。OperatorID=0 表示无登录操作人的系统入口，正数操作人必须引用真实 User，但允许平台管理员操作租户账号。
- 模型由 models.Models 注册并通过 AutoMigrate 建表，不占用 DML migration 版本。新增 repository 目前只提供 Create，禁止更新或删除，保持追加式语义；本批没有 API、页面或新权限，后续审计查看继续归客服/对话审计项目统一设计。
- 租户完整性审计登记显式零租户策略，并新增目标账号同租户关系和操作人全局引用关系。基线由 51/51 模型、64 表、125 关系提升为 52/52、65 表、127 关系；测试覆盖租户日志、平台零租户日志、跨租户目标和不存在操作人。
- 全仓 Go、定向 race、service 全包、go vet 和 diff 检查通过。无 DTO、enum、API、Gin 路由、权限、WebSocket、前端、AI 回复、模型调用、token、usage 或计费语义变化。
- `origin/codex/ai-billing@f2d2da4` 同时修改 `internal/models/models.go`。最终合并必须同时保留 UserRoleChangeLog 和 AI 分支新增模型注册，合并后重新运行策略覆盖与全量 AutoMigrate 测试；无需 migration 排序。本批契约应先于 71B 角色写入接线合并。
- 回滚可删除新 model、repository、模型注册、审计策略/关系、测试和本节文档；尚未接线时不会丢失在线日志。71B 开始写入后不得直接删除已生成的历史表，代码回滚也应保留数据供审计。

## 71B. 当前实施检查点：账号角色变更在线写入（2026-07-15）

本批把 71A 契约接入真实角色写链路。账号管理手动分配、账号创建、接入公司主管创建和邀请注册审核原本都汇聚到 `replaceUserRolesDB`；企业微信登录首次补 `store_staff` 是唯一额外运行时直写入口，也同步纳入。安装 migration 和客户审计仿真种子属于初始/测试数据，不伪装成人工角色变更。

- UserRoleRepository 新增一次性 LEFT JOIN 快照查询，始终返回按 role ID 排序的当前 ID/code；UserService 对目标角色去重、校验并排序后比较集合。集合相同直接返回，不再删除重建 UserRole，也不产生虚假日志。
- 集合确实变化时，职责依赖校验先于删除；UserRole 删除、新关系写入和 UserRoleChangeLog 创建位于同一事务。日志写入失败或外层后续步骤失败时，角色关系和日志一起回滚；权限不足、职责依赖未清理、注册审核失败均不留日志。
- 创建普通账号和租户主管时记录 `[] -> 初始角色`；注册审核通过记录无角色到审核角色，拒绝且角色集合为空时不记伪变化；同 request ID 重放不会重复写。企业微信首次补门店员工角色记录完整前后集合，操作人记为该登录账号自身，再次登录已持有角色时不重复写。
- 当前运行时代码扫描确认没有其他 UserRole 写入口：通用 UserRoleService 的写方法没有调用者；RoleService 只使用其只读查询。后续角色写入必须继续复用 UserService 事务边界，不能从 handler 或新 service 直接调用 repository 绕过日志。
- 测试覆盖排序快照、重复角色 ID、无变化保存、权限失败、四类职责依赖失败、强制外层事务回滚、普通账号创建失败回滚、主管创建、注册审核通过/拒绝/重放和企微自动角色幂等。定向 race、service 全包、全仓 Go、go vet 与 diff 检查通过。
- 无 model、AutoMigrate、DML migration、DTO、enum、API、Gin 路由、权限、WebSocket 或前端变化；不触及 AI 回复、模型调用、token、usage 或计费。
- 以共同基线复核，`origin/codex/ai-billing@f2d2da4` 在本批文件中只同时修改 `wxwork_login_service.go`。最终必须逐段保留 AI 分支邮箱绑定逻辑与本批企微角色日志事务，不能整文件选边；`user_service.go` 和本批测试当前无 AI 同文件修改。71A 必须先合并并完成 AutoMigrate，本批无 migration 排序要求。
- 回滚在线写入逻辑可停止生成新日志，但应保留 71A 表和既有数据。若连同“相同集合不重建”一起回滚，会恢复无意义 UserRole ID/AuditFields 变化；不得用回滚脚本删除已经形成的角色历史。

## 72. 当前实施检查点：租户方案最终复扫与 AI 分支合并门槛（2026-07-15）

本轮按现有页面、权限、service、数据模型、状态机和非 HTTP 链路重新核对租户公司、账号注册、全局角色权限、客服组/小组、排班、派单、通知、文件和完整性审计。`codex/customer-audit` 当前未发现新的本分支运行时越权缺口；公开注册仍按正式配置默认关闭。剩余工作不是新增一套租户或派单功能，而是把 `codex/ai-billing` 的新增能力并入同一租户与权限契约。

### AI 新增模型分类

- 必须增加 TenantID 并从权威父对象继承：AIManualResumeTask 从 Conversation，FastGPTDatasetJob 从 KnowledgeBase/Store，KnowledgeResourceGroup 从 KnowledgeBase/WxWorkInstance，KnowledgeResourceItem 从 Group，WxWorkCustomerHandoffSetting 从 Customer/Instance，AIUsageEvent 与 AIUsageGatewayCall 从 Conversation/Message/KnowledgeBase 所属租户。CompanyID/StoreID 不能代替租户根。
- ReplyIntentProfile 是行业级平台模板，继续保持平台全局；租户可选择但不能按普通租户角色修改。EmailVerificationCode 是以全局唯一邮箱和不可逆 challenge 为边界的认证安全记录，继续作为平台认证数据，不进入租户业务列表。
- 合并后的每个新增 TenantID 模型必须登记 TenantIntegrity policy、父子关系和双租户测试；模型覆盖基线将高于当前 52/52、65 表、127 关系，不能通过放宽审计绕过。

### 运行时和权限阻断

- AI 分支媒体理解仍存在全局 Message、Conversation、WxWorkInstance 和 Channel 读取；合并必须保留本分支 Message/Conversation 同租户校验、`ResolveForMessage` 租户路由和企微语音按租户取 Channel，再叠加 usage 记录，禁止用 AI 文件整段覆盖。
- FastGPT dataset 的 provision、upload、delete collection 当前都只要求 `knowledgeBase.view`。必须分别映射 `knowledgeBase.create/update/delete`；collections/search test 可保留 view。权限必须进入权限管理和角色绑定，前端显隐与后端 RequirePermission 同步。
- FastGPTDatasetService、KnowledgeResourceService 与 AI usage service 仍有全局 Store/KnowledgeBase/Instance/Asset/route 查询。写入前必须使用 ActiveTenantID 或父实体 TenantID，后台 worker 必须从持久化任务 TenantID 恢复上下文，不能依赖操作者当前切换公司。
- 客服小组、排班和派单是本分支新增并已验证的能力。AI 分支基线中没有这些文件，但没有显式删除提交；正常 Git 合并应保留。集成时禁止以 AI 分支目录或页面整包覆盖本分支，否则会人为丢失已确认能力。

### 合并规模与顺序

- 共同基线 `e67e207` 上双方共有 53 个修改文件；`git merge-tree --write-tree HEAD origin/codex/ai-billing` 当前报告 24 个文本冲突，集中在 models/config/auth、RAG/知识库/媒体、企微实例、登录、知识页面和导航。必须按领域逐个解决，不能自动接受 ours/theirs。
- AI DML migration 使用 21-33，本分支租户 migration 使用 34-56，当前无版本号重复。合并必须同时保留两段并重新执行 migration runner 的版本/remark 测试；UserRoleChangeLog 仍由 AutoMigrate 建表，不占 migration 号。
- 合并顺序：先保留最终模型字段与注册表 -> 补 AI 新模型 TenantID/策略/回填 -> 合并 runtime 和 handler 权限 -> 保留客服小组/派单与租户导航 -> 运行 AutoMigrate/migration -> 双租户全链路、全量 Go/前端/构建和真实只读审计。
- 在上述阻断全部关闭前，`tenantRegistration.enabled` 必须保持 false；公司主管账号、邀请码查看和内部账号创建可继续使用，但不得把邀请注册链接作为已上线能力发给租户用户。

## 73. 当前实施检查点：角色写入审计旁路契约（2026-07-15）

第 71B 批已把所有现有在线角色入口接入 UserRoleChangeLog，但生成式 UserRoleService 仍保留无人调用的 Create/Update/Delete 等通用方法。它们没有权限、职责依赖或角色集合日志语义，后续功能一旦误用就会绕过已确认边界。本批不新增平行角色 API，而是删除这些无调用写方法并用源码契约阻止旁路重新出现。

- UserRoleService 只保留 Get/Take/Find/FindOne/分页/Count 查询；在线角色集合写入必须经过 `UserService.replaceUserRolesDB`。企业微信首次登录补 `store_staff` 保留为唯一额外入口，并在原事务内追加完整角色快照。
- 新增 AST 测试扫描 `internal/services` 下所有非测试 Go 文件。它同时识别 UserRoleRepository/UserRoleService 写方法、带 `models.UserRole` 的 GORM Create/Save/Update/Delete，以及引用 `t_user_role` 的 Exec；只有 `replaceUserRolesDB` 和 `assignDefaultStoreStaffRole` 两个函数获准。
- 检测器表驱动测试覆盖 repository/service/GORM 链式调用/原始 SQL 四类写法，并确认查询、其他模型写和无关 SQL 不误报。migration 2 的内置角色初始化和 `customer_audit_seed` 仿真数据写入位于 service 运行时之外，继续作为明确例外；它们不代表在线账号操作。
- 定向 race、完整 service、全仓 Go、go vet 和 diff 检查通过。无 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端或 AI/计费变化。
- 共同基线复核显示 AI 分支不修改 `user_role_service.go`，新增契约测试也无同文件冲突；无需 migration 顺序或 rebase。合并 AI 分支后该测试会自动检查其新增 service 是否出现未审计角色写入。
- 可独立回滚 service 收口、测试和本节文档，不涉及数据回滚；回滚会重新暴露可绕过日志的通用写方法，因此不应把它作为兼容接口恢复。

## 74. 当前实施检查点：角色变更快照语义审计（2026-07-15）

UserRoleChangeLog 的 TenantID、目标账号和操作人关系已由第 71A 批纳入普通完整性审计，但 JSON 快照若被手工写坏，表和父子关系仍可能全部通过。本批扩展现有只读审计，不新增修复脚本或日志编辑入口。

- Repository 按 ID 顺序只读取四个快照 JSON 列。before/after role IDs 必须是非 null 的 JSON 数组，可为空集合，但元素必须为正数、严格升序且不重复；role codes 同样必须是非 null JSON 数组，元素非空、无首尾空格、严格升序且不重复。
- 每一侧 IDs 与 codes 数量必须一致，before/after ID 集合不得完全相同。违规统一报告 `USER_ROLE_CHANGE_LOG_PAYLOAD_INVALID`，entity 为 `UserRoleChangeLog.role_snapshots`，总数按日志行计，样本遵循全局 sampleLimit。
- 不要求历史 ID 继续引用当前 Role，也不要求历史 code 等于 Role 当前 code：角色模板后续可能删除或改名，快照职责正是保留变更发生时证据。当前租户/账号/操作人关系继续由原 127 条普通关系检查。
- 测试包含合法平台/租户初始角色记录，以及非法 JSON、逆序、重复、ID/code 数量不一致、无实际变化和带空格 code 六类损坏记录；总数和前两个样本 ID 精确验证。审计仍只读，不自动排序、去重或覆盖证据。
- 定向 race、完整 service、全仓 Go、go vet 和 diff 检查通过。模型/表/关系基线继续为 52/52、65、127；无 model、AutoMigrate、migration、DTO、API、权限、WebSocket、页面或 AI/计费变化。
- AI 分支不修改本批 repository/service/test，当前无同文件冲突或 migration 顺序要求。可独立回滚本批读取、规则、测试和文档；回滚只会失去损坏快照发现能力，不应删除已存在日志。

## 75. 当前实施检查点：角色变更并发顺序与日志连续性（2026-07-15）

单条快照合法仍不能证明在线角色写入没有漏记或被并发覆盖。本批在第 71B/74 批基础上补充写入顺序和只读连续性证据，不要求上线前已有账号伪造历史日志。

- `replaceUserRolesDB` 在读取旧角色前通过 UserRepository.GetForUpdate 锁定目标 User 行；锁由账号创建、手动分配、主管创建或注册审核的现有外层事务持有。MySQL 使用 `FOR UPDATE` 串行化同账号角色替换，SQLite 保持单写事务语义。
- GORM callback 测试直接确认 User 查询携带 `FOR` locking clause，避免只凭方法名称推断加锁。角色校验、职责依赖、UserRole 替换和日志写入继续位于同一事务。
- 审计按 `user_id + log id` 读取已通过 payload 校验的记录，同一账号下一条 before IDs 必须等于上一条 after IDs；最后一条 after IDs 必须等于当前 UserRole 集合。违规报告 `USER_ROLE_CHANGE_LOG_CHAIN_BROKEN`，样本为发生断点的下一条日志或终态漂移的最后一条日志。
- 连续性只比较 role IDs，不比较 codes，避免角色模板合法改名造成误报。没有任何日志的历史账号不参与；同账号存在无效 payload 时只报告第 74 批违规并跳过该账号链检查，避免重复噪声。
- 测试覆盖两段合法链、相邻 before/after 断裂和最新 after 与当前角色不一致，两类错误各命中精确日志 ID；定向 race、完整 service、全仓 Go、go vet 和 diff 检查通过。
- 无 model、AutoMigrate、migration、DTO、API、权限、WebSocket、前端或 AI/计费变化。AI 分支不修改本批运行文件，无同文件冲突或 migration 顺序要求。
- 可独立回滚行锁、批量当前角色查询、连续性审计和测试；不涉及数据回滚，但会重新允许同账号并发替换形成歧义，并失去日志漏记/旁路写入发现能力。

## 76A. 当前实施检查点：角色权限集合变更审计契约（2026-07-15）

账号只能绑定角色、权限只能绑定角色的产品边界已经成立，但 RolePermission 仍只保存当前关系，替换时会删除旧记录；AuditFields 无法还原全局角色权限集合前后变化。本批先建立追加式数据契约，在线写入和旁路收口放在独立 76B，避免把“表已存在”误报为“审计已生效”。

- 新增 RolePermissionChangeLog，保存 RoleID、变更时 RoleCode、前后 permission ID/code JSON、操作人 ID/名称和创建时间。Role/Permission 都是平台全局模板，日志不增加 TenantID。
- RoleID 不建立当前父级关系：非内置 Role 在不再被账号使用后可以合法删除，历史日志必须继续保存被删角色证据；RoleCode 是删除后的稳定展示快照。正数 OperatorID 仍通过普通完整性关系要求引用真实 User，0 表示系统初始化入口。
- 模型注册进 models.Models，由 AutoMigrate 建表；repository 目前只暴露 Create，不提供更新/删除。没有 DML migration、API、页面或新权限，后续审计查看仍归统一审计项目。
- TenantIntegrity 模型策略仍为 52/52；必需表由 65 增至 66，普通关系由 127 增至 128。测试覆盖缺失操作人违规，并确认模型注册、表和关系覆盖完整。
- 定向 race、完整 service、全仓 Go、go vet 和 diff 检查通过。无 DTO、enum、Gin 路由、WebSocket、前端、AI runtime、token、usage 或计费变化。
- AI 分支同时修改 `internal/models/models.go`，最终必须同时保留 RolePermissionChangeLog、UserRoleChangeLog、租户模型和 AI 新模型注册；其余本批文件无同文件冲突，无 migration 排序要求。76A 必须先于 76B 合并。
- 76B 上线写入前可整体回滚本契约；开始产生日志后即使停写也应保留表和历史数据，禁止把业务回滚实现成删除审计记录。

## 76B. 当前实施检查点：角色权限集合在线事务审计（2026-07-15）

76A 只建立日志表契约，本批把角色管理现有权限分配入口接入同一事务。页面、API 和权限点均不新增；角色仍由平台管理员在原角色管理页绑定权限，公司主管仍只能给本公司账号赋角色。

- `RoleService.AssignPermissions` 进入事务后使用 `RoleRepository.GetForUpdate` 锁定目标 Role，并在锁内重新执行角色管理等级与平台账号校验。MySQL 通过 `FOR UPDATE` 串行化同角色写入，SQLite 保持单写事务语义。
- 输入 permission ID 先去重，再逐项确认权限存在、启用且 scope 可分配；全部通过后才删除旧关系。租户角色仍禁止持有 platform 权限，缺失、禁用或跨 scope 请求不会清空已有权限，也不会产生审计行。
- repository 按 permission ID 读取排序后的当前 ID/code 快照；新集合同样按 ID/code 独立排序后写入 `RolePermissionChangeLog`。相同 ID 集合重复提交直接返回，不删除重建 RolePermission，也不写无变化日志。
- RolePermission 替换和日志追加位于同一事务。日志表缺失或写入失败会回滚关系删除与新增，保证“当前权限”和“变更证据”不会分离；日志继续保存操作时 RoleCode 和 PermissionCode 快照，不要求历史模板永久存在。
- 本批只修改 role/role-permission repository、RoleService 和既有权限 service 测试数据库/权限矩阵测试；没有 model、AutoMigrate、DML migration、DTO、enum、Gin 路由、权限码、WebSocket、页面、AI runtime、模型调用、token、usage 或计费变化。
- 测试覆盖成功替换及排序快照、重复 ID 去重、相同集合不重建、租户角色拒绝平台权限、缺失/禁用权限保留旧集合、日志落库失败整笔回滚和 Role 行锁 clause。聚焦 race、完整 services 包、全仓 Go、vet 和 diff 检查均已通过。
- 与 `origin/codex/ai-billing@f2d2da4` 对照，本批七个代码/文档文件无同文件修改，不要求 rebase 或 migration 排序。合并顺序保持 76A 数据契约 -> 76B 在线写入 -> 76C 写旁路及日志语义/连续性审计。
- 可紧急回滚 76B 的 service/repository/test 接线以停止新日志，但必须保留 RolePermissionChangeLog 表和已有行；回滚会恢复无历史证据的权限替换，不应作为长期方案。

## 76C. 当前实施检查点：角色权限写旁路与审计连续性收口（2026-07-15）

76B 已让现有角色权限入口同事务写日志，但通用 RolePermissionService 仍暴露无鉴权、无快照的 CRUD 方法，审计也只检查操作人引用。本批关闭这些旁路，并把日志语义纳入现有 tenant-integrity-audit，不创建第二套审计工具。

- RolePermissionService 收敛为只读查询，删除 Create/Update/Updates/UpdateColumn/Delete。新增 AST 契约扫描 `internal/services` 非测试文件，识别 repository/service 写方法、GORM RolePermission 写链和 `t_role_permission` 原始 SQL；唯一在线允许点是 `role_service.go:replaceRolePermissions`。
- AST 表名匹配使用完整单词边界，不把 RolePermissionChangeLog 写入误判为 RolePermission 修改。migration 2/34/52/53/56 的初始化和幂等数据修复不属于在线 service 扫描，继续作为显式离线例外。
- RolePermissionChangeLog 四个 JSON 快照列必须是非 null 数组：ID 为正数且严格升序，code 非空、无首尾空格且严格升序；同侧 ID/code 数量必须一致，before/after ID 集合必须不同，RoleID 必须为正数。违规码为 `ROLE_PERMISSION_CHANGE_LOG_PAYLOAD_INVALID`。
- 同一 RoleID 的相邻日志必须满足上一条 after IDs 等于下一条 before IDs；最新 after IDs 必须等于当前 RolePermission 集合。违规码为 `ROLE_PERMISSION_CHANGE_LOG_CHAIN_BROKEN`，相邻断链记录后一条日志，终态漂移记录最后一条日志，同一日志去重。
- 任一角色存在坏 payload 时跳过该角色连续性检查，避免一条根因产生连锁误报。快照中的历史 Role/Permission ID 和 code 不与当前模板回查；自定义角色或权限合法删除后，追加式证据仍有效。只有当前 RolePermission 关系继续受现有父级完整性审计约束。
- 测试覆盖 AST 正反例、合法双租户与已删除模板快照、坏 JSON、逆序、重复、数量不一致、无变化、空格 code、非法 RoleID、相邻断链和当前终态漂移。聚焦 race、完整 services 包、全仓 Go、vet 和 diff 检查均已通过。
- 本批修改两个 repository、RolePermission 只读 service、AST 契约、完整性审计 service/test 和两份文档；无 model、AutoMigrate、DML migration、DTO、enum、API、权限、WebSocket、前端或 AI/计费变化。
- 与 `origin/codex/ai-billing@f2d2da4` 对照，本批八个文件无同文件修改，不要求 rebase 或 migration 排序。76C 依赖 76A 日志契约和 76B 在线写入，必须按 A -> B -> C 合并。
- 可独立回滚本批代码与文档且无需数据库回滚；回滚会重新暴露未审计的通用 RolePermission 写方法，并失去 payload/断链/终态漂移发现能力，不建议回滚。RolePermissionChangeLog 表和历史行始终保留。

## 77. 当前实施检查点：角色删除与账号赋角并发闭环（2026-07-15）

76B/76C 收口了角色权限替换，却没有覆盖自定义角色删除：旧 DeleteRole 只删除 Role 主表，已有 RolePermission 会成为孤儿，也不会形成权限集合清空日志。进一步并发检查发现账号赋角只锁 User、不锁目标 Role，可能与角色删除交错后写出指向已删除角色的 UserRole。

- DeleteRole 改为单事务：`FOR UPDATE` 锁定 Role，使用事务连接复核操作者等级，拒绝系统角色，通过 UserRoleRepository 确认无人使用，再调用唯一的 `replaceRolePermissionsDB(..., nil)` 清空权限并追加 before -> [] 日志，最后删除 Role。
- RolePermission 清空、RolePermissionChangeLog 追加和 Role 删除同事务提交。日志表缺失、日志写失败或最后 Role 删除失败时，角色、权限关系和日志全部回滚；已被账号使用及系统内置角色在任何写入前拒绝。
- 账号角色替换先锁 User，再对去重且按 ID 排序的目标 Role 逐个 `FOR UPDATE`。分配先持有 Role 锁时，删除在其提交后能看到 UserRole 并拒绝；删除先持有 Role 锁时，分配在角色删除后得到“角色不存在”，不会创建孤儿关系。排序锁定避免多角色分配形成反向锁序。
- 已有账号替换角色在 User 行锁内重新读取当前 UserRole 和 Role，复核租户可见范围及目标账号最高等级，防止外层预检后目标账号被并发提权再由旧请求覆盖。当前关系引用缺失时保守拒绝管理，不把脏关系当作低权限。
- 新建账号首次赋角单独使用 `assignInitialUserRolesDB`：新租户创建时平台管理员尚未切换 ActiveTenant，不能套用已有账号可见范围；该入口只在账号刚插入的同一事务内调用。首次赋角和已有账号替换仍共用唯一实际写函数 `replaceUserRolesInternalDB`，角色状态、scope、分配等级、日志与回滚规则完全一致。
- Role/RolePermission/UserRole 写操作均通过 repository；RoleRepository.Delete 开始返回错误，UserRoleRepository 新增 ExistsByRoleID/DeleteByUserID。两个 AST 契约的唯一实际写允许点更新为 `replaceRolePermissionsDB` 和 `replaceUserRolesInternalDB`，企微默认门店员工角色仍保留原受审计例外。
- 测试覆盖删除成功及排序快照、在用/系统角色拒绝、日志失败回滚、Role 删除失败回滚、账号赋角 Role 行锁、锁内目标等级复核、新租户创建/邀请审核和企微首次角色兼容。聚焦 race、完整 services 包、全仓 Go、vet 和 diff 检查均已通过。
- 本批修改 Role/UserRole repository、Role/User service、两份 AST 契约、角色权限测试和两份文档；无 model、AutoMigrate、DML migration、DTO、enum、API、路由、权限、WebSocket、前端或 AI/计费变化。
- 与 `origin/codex/ai-billing@f2d2da4` 对照，本批九个文件无同文件修改，不要求 rebase 或 migration 排序。依赖 76A-76C，建议在其后合并；最终集成后必须重跑角色删除、账号赋角和两个 AST 契约。
- 可回滚本批代码与文档且无需数据库回滚，但回滚会恢复角色删除孤儿关系和赋角/删除竞态，不建议回滚；RolePermissionChangeLog 历史行始终不得删除。
