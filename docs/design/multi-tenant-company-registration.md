# 多租户公司、账号注册与数据隔离设计

> 状态：当前统一项目权威设计
>
> 更新时间：2026-07-27
>
> 适用分支：`codex/tenant-ai-unified-integration`
>
> 数据基线：只支持由当前代码创建的全新 SQLite/MySQL，不支持旧库原地升级

## 1. 产品层级

AgentDesk 是平台运营方提供给不同公司的 SaaS 客服系统。购买并使用系统的公司是
`Tenant`，也是业务数据唯一隔离根。Tenant 下直接管理门店 `Store`，不存在 Company
或“客户企业 -> 门店”的中间层。

```text
平台
  -> Tenant
     -> 公司主管、客服组长、客服、门店员工系统账号
     -> Store
        -> 唯一活动 store_staff 账号绑定
        -> WxWorkProtocolInstance
        -> 门店知识库与 Store Credential
     -> 客户、会话、客服组织、派单、运营分析、人工质检
```

- `User` 是登录 AgentDesk 的系统账号。
- `store_staff` 是角色；获得该角色的账号代表一家稳定 Store。
- `WxWorkProtocolInstance` 是实际企微员工号，只是 Store 的渠道身份。
- Customer 主档归 Tenant；客户与不同门店的关系由 `StoreCustomerRelation` 分开表达。
- Company 模型、字段、repository、接口、页面、权限和 migration 均不存在。

## 2. 角色与权限

### 2.1 平台角色

- `super_admin`：平台最高权限。
- `admin`：按 Role 中实际权限管理平台资源和 Tenant。

平台侧负责：

- 创建、核验、停用和切换 Tenant。
- 维护平台行业 Profile、意图分类和固定行业标签模板。
- 维护 Model Profile、九个用途槽和 Store Profile 指派。
- 跨 Tenant 查询获授权的运营、账单和审计信息。
- 管理全局 Permission 目录和可管理 Role 的权限集合。

平台不替 Tenant 承担日常客服组织、排班、账号审核或门店运营。

### 2.2 Tenant 角色

- `tenant_admin`：公司主管，管理本 Tenant 的账号、角色赋予、客服组织、门店、知识、
  运营策略和获授权的敏感操作。
- `cs_team_leader`：在负责客服组/小组范围内管理排班、派单和团队运营。
- `cs_user`：处理本人被分配的会话，并查看权限允许的本人数据。
- `store_staff`：在唯一 Store 范围内使用门店工作台，并按 Store policy 决定能否自行
  录入模型凭据。

权限关系固定为：

```text
Permission -> RolePermission -> Role -> UserRole -> User
```

User 不能直接获得隐藏权限。页面显隐、Handler 权限和 Service 数据范围必须同时生效；
权限只决定操作资格，平台/Tenant/客服组/本人/Store 范围始终是不可突破的上限。

## 3. Tenant 创建事务

平台管理员在“接入公司”创建 Tenant，至少提供：

- 法定名称、简称。
- 登记类型和唯一法定登记号。
- 业务联系人、联系方式、地址和备注。
- 必选且已发布的行业 Profile。
- 首个公司主管账号资料。

`TenantService.CreateTenant` 在同一事务完成：

1. 校验平台权限、行业 Profile 和法定登记号唯一性。
2. 生成系统 TenantCode 和公司邀请码。
3. 创建 Tenant，并把 `Tenant.IntentProfileID` 写为唯一行业来源。
4. 投影该行业的固定标签目录和 Tenant 行业策略。
5. 创建首个 `tenant_admin` User 与 UserRole。
6. 创建默认“综合客服组”。
7. 创建仅作内部接待策略身份的默认 `AIAgent`。
8. 创建加密保存的有效 TenantInvitation。

任一步失败全部回滚。创建事务不生成 Store、企微实例、真实模型 Key 或测试业务数据。

## 4. 邀请与注册

### 4.1 公司邀请码

- 邀请码只决定 TenantID，不携带角色、权限、客服组、Store 或企微身份。
- 数据库保存不可逆摘要和受保护密文；日志、错误和审计不输出明文。
- 公司主管可查看当前邀请码、生成带码注册链接和轮换邀请码。
- 生产邀请密钥只能通过受限环境变量注入，缺失时敏感动作失败关闭。

### 4.2 两种账号入口

公司主管可以：

1. 直接创建本 Tenant 账号，系统生成一次性初始密码并强制首次改密。
2. 发送邀请注册链接，注册账号进入待审核状态，再由公司主管审核和分配角色。

这两种都是账号注册，不使用“开户”语义。公开注册默认关闭；启用时必须完成验证码、
限流、邀请码密钥和 Tenant 隔离配置。

### 4.3 角色与 Store 创建

邀请码和注册动作不自动赋权。只有审核或账号管理明确分配 `store_staff` 时，才要求门店
名称，并在角色事务内调用 `StoreStaffBindingService`：

- 首次分配创建 1 个 Store、1 个活动 Binding。
- 同一事务创建未配置 `StoreModelCredential`、Store Credential policy 和默认关闭的
  客户标签运行策略。
- 重新分配时复用原 Store/Binding，不创建第二家门店。
- 移除角色或停用账号时清空活动占用并停用 Store、Binding 和相关企微实例。
- 一个有效 User 最多占用一个活动 Store，数据库唯一索引与 Service 校验共同保证。

## 5. Tenant、Store 与渠道

```text
Tenant
  -> StoreStaffBinding(UserID, ActiveUserID, StoreID)
  -> Store
  -> WxWorkProtocolInstance
  -> ConversationRouteState
  -> Customer / Conversation / KnowledgeBase
```

- StoreID 由当前数据库创建链生成，禁止硬编码来源环境 ID。
- 企微扫码、企微 OAuth 和绑定链接只能绑定既有 User/Store/Binding，不能隐式注册账号、
  分配角色或创建第二个 Store。
- 企业微信协议字段必须以 `https://wework.apifox.cn/llms.txt` 及其链接页面为唯一依据。
- 消息发送使用文档规定的 `conversation_id`；单聊以 `S:` 开头，群聊以 `R:` 开头。

详细生命周期见
`docs/design/wxwork-managed-store-scope-implementation.md`。

## 6. 行业、模型、知识与标签

### 6.1 行业

- `Tenant.IntentProfileID` 是唯一行业绑定。
- Store、企微实例、知识库和会话不能覆盖行业。
- 行业 Profile 决定 IntentDetect Prompt、JSON Schema、意图分类和标签模板。
- Tenant 只能在允许范围内启停标签和设置显示别名，不能修改 SemanticKey 或物理删除。

### 6.2 模型与凭据

```text
平台 ModelProfileTemplate + 9 个 ModelProfileSlot
  -> StoreModelProfileAssignment
  -> StoreModelCredential
  -> ModelCallResolver
```

- 平台维护 Profile 和单一 NewAPI 网关配置。
- Store 只有一个 active Assignment，可有一个待验证的 pending revision。
- 每个 Tenant + Store 只有一条 Credential 事实；默认无 Key。
- 平台管理员和公司主管按权限录入；门店员工只有在 policy 允许且是该 Store 唯一活动绑定
  时可自助录入。
- 密钥加密保存、永不回显；候选需要密码复核、二次确认、真实九槽测试，并可要求不同
  公司主管审批。
- 验证或 FastGPT 同步失败时保留旧 active revision；首次配置失败时保持未就绪。
- Tenant 用户只看到模型名、revision 和 readiness，不看到 Provider、BaseURL、Prompt、
  Schema 或密钥。

不存在 AIConfig、Tenant 授权池、Tenant 默认模型、企微覆盖或平台共享 Key fallback。

### 6.3 FastGPT 与客户标签

- FastGPT Team、Dataset、Profile 和任务均绑定 Tenant + Store。
- KnowledgeBase 必须属于相同 Tenant + Store，运行时只读取 applied revision。
- 客户标签绑定 `StoreCustomerRelation`，同一客户在不同 Store 的标签相互独立。
- 运营分析和人工回复质检只读取本版本运行时产生的 Tenant 事实，不跨租户聚合。

## 7. 页面信息架构

平台上下文：

- 接入公司。
- 行业 Profile、意图分类、行业标签模板。
- Model Profile。
- 角色、权限、平台设置。
- 选择活动 Tenant 后按权限进入该 Tenant 的业务页面。

Tenant 上下文：

- 总览、运营分析。
- 会话、规则派单、会话记录、工单。
- 客户、知识库、客户标签。
- 客服组、小组、排班、账号管理。
- 企微员工号、门店工作台。
- Store 模型凭据和账单入口只按权限与数据范围展示。

`/dashboard/companies`、Company 详情和旧 AIConfig/授权池页面不是当前产品入口，路由和
导航均应拒绝访问。

## 8. 数据隔离规则

### 8.1 请求与写入

- TenantID 只能来自认证上下文或已验证父对象。
- 普通 request DTO 不能用调用方提交的 TenantID 越过 ActiveTenant。
- 创建或更新子资源前必须校验所有父对象属于同一 Tenant。
- 平台账号未选择活动 Tenant 时不能写 Tenant 业务数据。
- 跨 Tenant ID 注入必须显式失败。

### 8.2 读取、导出与实时事件

- Repository 使用 Tenant 条件方法；Service 同时裁剪组织/本人/Store 范围。
- 列表、详情、统计、导出、WebSocket 与后台 worker 使用同一数据范围。
- 异步任务必须持久化 TenantID/StoreID 并在领取、执行和提交时重新校验。
- 外部回调无法从可信父链确定 Tenant 时拒绝处理，不落入默认 Tenant。

### 8.3 OIDC fallback

当前认证流程保留一个由 Migration 35 创建的 `legacy-default` OIDC fallback Tenant。
它只用于当前通用 OIDC 自动建号兼容，不是业务示例 Tenant，不包含 Store、会话、Key 或
历史客户数据，也不能作为跨租户默认归属。退出它必须作为独立认证迁移完成。

## 9. Fresh 数据库契约

- DDL 只由 `AutoMigrate(models.Models...)` 创建。
- DML runner 只保留版本 `2/15/35/68/69`。
- 新 Tenant 和 Store 的业务基础对象由当前创建 Service 原子建立。
- 不提供历史 Tenant/Store/Company 回填、旧库升级或清表工具。
- 当前 Schema 不包含 Company、AIConfig、Grant、StoreSetting、ConversationTag 和本地
  Document/FAQ/Chunk。
- 旧备份只能配套旧源码在隔离环境只读恢复。

## 10. 验收不变量

- 新 Tenant 原子得到行业投影、公司主管、默认客服组、邀请和内部接待策略。
- 邀请码只绑定 Tenant，不赋角色。
- `store_staff` 分配原子得到 1 Store、1 Binding、Credential/Policy 和标签策略。
- 重复分配不新增 Store 或 Binding；一个 User 不得占用多个活动 Store。
- 企微绑定不创建 User、Role、Store 或 Binding。
- Tenant A 无法读取、修改、统计、导出或订阅 Tenant B 的数据。
- 公司主管、组长、客服和门店员工的数据范围符合职责。
- 所有操作权限都能在权限管理中查看并由 Role 分配。
- Company 和旧模型/标签/知识链在代码、路由和 fresh Schema 中均不存在。
- 空 SQLite 与空 MySQL 首次启动和幂等重跑得到同一业务契约。

关键验证：

```bash
go test ./internal/migration ./cmd/migration -count=1
go test ./internal/services -count=1
go test ./... -count=1
go vet ./...
pnpm --dir web typecheck
pnpm --dir web lint
pnpm --dir web build
```
