# 多租户公司、账号注册与数据隔离设计

> 状态：当前权威设计
>
> 更新时间：2026-07-20
>
> 本文替代该文件此前的阶段性推演。历史方案可从 Git 记录追溯，不得继续以旧章节恢复“客户企业”、隐藏权限、企微自动注册或 Company 运行时层级。

## 1. 产品边界

AgentDesk 是平台运营方提供给不同公司的 SaaS 客服系统。购买并使用系统的公司是 `Tenant`，每个 Tenant 的账号、门店、客户、会话、客服组织、知识库和运营数据必须隔离。

```text
平台
  -> Tenant A
     -> 公司主管、客服、客服组长、持有 store_staff 角色的系统账号
     -> 多家门店
     -> 企微员工号、客户、会话、知识库、派单和统计
  -> Tenant B
     -> 完全独立的同类数据
```

租户公司下面只有门店，不再增加“客户企业 -> 门店”的中间层。一个获得 `store_staff` 角色的系统账号代表一家门店，具体实现见 `docs/design/wxwork-managed-store-scope-implementation.md`。

## 2. 角色和管理边界

### 2.1 平台角色

- `super_admin`：平台最高权限，管理全平台角色、权限、租户和模型接入。
- `admin`：平台管理员，在自身权限范围内管理平台资源和租户。

平台管理员负责：

- 创建 Tenant。
- 创建该 Tenant 的首个公司主管账号。
- 核验租户法定资料和唯一登记号。
- 管理平台 AIConfig，并给 Tenant 授权可用模型。
- 切换活动 Tenant 后按授权进入租户业务页面。

平台管理员不负责租户后续日常账号注册、角色安排、客服组织或门店企微绑定。

### 2.2 租户角色

- `tenant_admin`：公司主管，位于平台管理员之下、客服组长之上，管理本 Tenant 的全部业务资源和角色赋予。
- `cs_team_leader`：客服组长，管理授权客服组、小组、排班、派单和团队运营范围。
- `cs_user`：客服，处理分配给自己的会话和被授权的业务页面。
- `store_staff`：门店员工号角色，账号代表一家门店并可绑定实际企微员工号。

账号只能被赋予已有角色，不能直接分配权限。权限只能先在权限管理中登记，再由管理员及以上角色给 Role 分配。

## 3. Tenant 创建

平台管理员在“接入公司”创建新 Tenant。最小资料包括：

- 法定名称和简称。
- 系统内唯一 TenantCode。
- 登记类型和唯一登记号。
- 业务联系人和联系方式。
- 状态、核验状态和备注。
- 首个公司主管账号资料。

创建必须在一个事务内完成：

1. 校验登记类型 + 登记号唯一。
2. 创建 Tenant。
3. 创建首个 `tenant_admin` User。
4. 写入 UserRole。
5. 创建默认综合客服组。
6. 创建有效 TenantInvitation。
7. 写入审计字段。

任何一步失败全部回滚。平台不能创建第二个平行租户根，也不能把历史 Company 直接改成 Tenant。

## 4. 公司邀请码

每个 Tenant 维护一个当前有效的邀请码。数据库只保存邀请码密文或不可逆标识，不在日志和错误中输出明文。

公司主管可以：

- 查看当前邀请码。
- 复制普通注册链接。
- 生成已经带邀请码的邀请注册链接。
- 轮换邀请码，使旧链接失效。

邀请码只用于确定 TenantID，不包含角色、权限、客服组、门店或企微员工号信息。

邀请码明文使用独立 AES-256-GCM 密钥加密。生产环境应通过 `AGENT_DESK_INVITATION_ENCRYPTION_KEY` 注入；缺少有效密钥时，租户创建、邀请码查看和轮换必须明确失败，不能降级为明文存储或使用固定默认密钥。日志、合并文档和测试报告都不得记录真实邀请码或密钥。

## 5. 公开注册

普通注册链接要求：

- 用户名、昵称和密码等账号基础信息。
- 公司邀请码。
- 必要的验证码和安全校验。

注册流程：

```text
提交注册
  -> 验证邀请码和租户状态
  -> 创建 Pending User，写入 TenantID
  -> 记录 TenantRegistrationLog
  -> 公司主管在用户管理审核
  -> 通过时选择角色
  -> 如选择 store_staff，必须填写门店名称
  -> 角色和门店身份在同一事务提交
```

公开注册不能：

- 自动成为公司主管、组长、客服或门店员工。
- 根据邀请码隐藏赋权。
- 调用企微 OAuth 创建账号。
- 跨 Tenant 复用用户名、手机或邮箱。

公开注册由 `tenantRegistration.enabled` 或对应环境变量显式控制，默认关闭。关闭时公司邀请码仍可由公司主管管理，但页面必须标明注册链接暂不可发送；启用前需要完成隔离验收、配置邀请码密钥和必要验证码/限流能力。

## 6. 公司主管账号管理

公司主管在用户管理拥有两种合法入口：

1. **直接创建账号**：系统生成一次性初始密码，首次登录必须改密。
2. **邀请注册**：发送带公司邀请码的注册链接，注册后由公司主管审核和分配角色。

这两种方式都属于账号注册，不存在“开户”语义。

用户管理统一展示：

- 账号基础资料和状态。
- 注册来源和审核状态。
- 已分配角色。
- 若为 `store_staff`，展示门店名称、企微员工号和客服组归属。
- 公司主管有权限时可直接发起企微员工号绑定。

公司主管只能管理本 Tenant 账号，且不能创建、分配或删除与自己同级或更高等级的角色。

账号与门店身份生命周期统一由角色和账号状态驱动：首次分配 `store_staff` 时创建唯一 Store + StoreStaffBinding；移除该角色或停用账号时原子停用门店身份及相关企微实例；重新分配角色或启用账号时复用原 Store + Binding。为避免在协议登录已经失效时误发消息，企微实例不会随账号恢复而自动启用。

## 7. 权限模型

权限是全局目录，Role 是权限集合，User 只绑定 Role：

```text
Permission -> RolePermission -> Role -> UserRole -> User
```

所有新业务动作必须先审计现有权限；可复用时不得新增重复权限。确需新增时必须同时完成：

- 后端权限常量。
- 权限目录 migration。
- 默认角色映射。
- Handler `RequirePermission`。
- Service 数据范围。
- 前端导航和动作显隐。
- 权限管理页面名称。
- 直接请求和页面测试。

平台账号可以为 Role 分配权限；普通操作者只能给 User 分配自己有权分配的 Role。

## 8. 页面访问与数据范围

页面是否显示由权限和上下文共同决定，不按角色名称写死：

- 平台账号未选择 Tenant：只显示平台页面。
- 平台账号选择活动 Tenant：显示平台页面和其权限允许的租户页面。
- Tenant 账号：固定在自己的 Tenant，只显示租户页面。
- 公司主管：本 Tenant 全局范围。
- 客服组长：其负责客服组范围。
- 客服：本人范围。
- 门店员工号角色：自己的 StoreStaffBinding / Store 范围。

角色职责决定默认权限，但实际页面仍按权限判断。若客服需要查看“今日数据”，应给客服角色分配 `dashboard.view` 或对应分析权限，同时 Service 只返回本人数据；不能通过隐藏接口或角色特判绕开权限目录。

## 9. 租户隔离

### 9.1 写入

- TenantID 只能从认证上下文、父对象或可信系统任务继承。
- Request DTO 不接受调用方任意指定 TenantID。
- 创建子资源前必须验证父资源属于同一 Tenant。
- 平台管理员未选择活动 Tenant 时不得写租户业务数据。
- 跨租户 ID 注入必须返回明确业务错误。

### 9.2 读取

- Repository 提供 `GetInTenant`、`UpdatesInTenant` 等租户条件方法。
- Service 统一添加 Tenant 和组织范围。
- Handler 不能只校验权限后返回全租户 model。
- 列表、详情、导出、统计、WebSocket 和异步任务必须使用同一范围。

### 9.3 异步与外部入口

以下入口同样必须能确定 TenantID：

- 企微协议消息和回调。
- 客户 Web 会话。
- WebSocket 连接与事件。
- 派单和超时任务。
- 知识库同步和 FastGPT 任务。
- AI usage、运行日志和网关调用证据。
- 评价公开 Token。

无法从可信归属确定 TenantID 时必须拒绝，不得落入默认租户。

## 10. 信息架构

### 平台管理

- 接入公司：Tenant 列表、创建、状态、切换、模型授权。
- 模型接入：平台 AIConfig 和供应商凭证。
- 角色管理、权限管理、平台设置。

### 租户管理

- 总览与运营分析。
- 会话、派单、会话记录、工单。
- 客户管理。
- 客服档案、客服小组、排班。
- 用户管理、邀请注册和审核。
- 企微员工号和门店工作台。
- 知识库、标签、快捷回复等服务能力。

历史 `/dashboard/companies` 不再是产品入口；“接入公司”只表示 Tenant 管理，不表示企微渠道或租户内门店。

## 11. 客户、门店和会话

- Customer 直接归属 Tenant。
- Store 表示一个 `store_staff` 账号代表的门店。
- StoreCustomerRelation 表示客户在某门店的业务关系。
- WxWorkProtocolInstance 表示该门店实际接待客户的企微员工号。
- ConversationRouteState 固化当前会话来源 Store、实例、知识库和人工路由。
- AgentTeam 通过 StoreStaffBinding 纳管门店员工范围。

Customer 不再绑定或展示历史 Company。客户公司名称等画像信息若未来需要，应作为客户档案字段或标签单独设计，不能恢复 Company 层级。

## 12. 模型授权边界

平台统一管理模型接入：

```text
AIConfig（平台凭证）
  -> TenantAIModelGrant（租户可用模型集合）
  -> StoreAIModelSetting（租户默认用途配置）
  -> WxWorkProtocolInstance 覆盖（只能选租户已授权模型）
```

Tenant 用户看不到供应商密钥。平台管理员进入活动 Tenant 且具有 `aiConfig.view/update` 时，才能查看和配置租户模型用途。

本设计不修改 Token、usage、价格、余额和计费公式。CompanyID 仅保留旧证据，不参与授权解析。

## 13. 历史 Company 退役

历史 Company 曾同时表示客户企业、门店上级公司和模型范围，已经与当前 Tenant + Store 语义冲突。现行处理是退役，不改名复用：

- 保留 model/repository 供旧 migration、历史 usage 和审计读取。
- 删除 Company Dashboard handler/service/builder/DTO/API。
- 删除公司页面、详情页、选择器和导航。
- 删除 `company.*` 权限。
- Customer、Knowledge、ReplyIntent 等公开 DTO 不再暴露 Company。
- 现行 Store、Binding、WxWork、门店知识库和门店模型设置 CompanyID 写 0。
- AI 回复和意图匹配不再读取 Company。

迁移细节见 migration 63 和门店身份设计文档。

## 14. 完整性审计

`TenantIntegrityAuditService` 必须覆盖所有注册且包含 TenantID 的模型，并验证关键父子关系。当前代码基线包含：

- 76 个显式 Tenant 模型策略。
- 89 张必需表。
- 207 条关系。

新增 TenantID 模型时，测试必须因缺少策略而失败，直到补齐明确策略和关系。FastGPTStoreTenant 与 FastGPTUsageSyncState 已纳入 Store、KnowledgeBase 和历史 Company 证据关系。

## 15. Migration 与发布

- DDL 由 AutoMigrate 执行。
- DML 修复使用单调 migration。
- migration 版本提交前必须与 main 和所有活动分支核对。
- 生产升级前先在可恢复的 MySQL 副本重复执行 migration 和完整性审计。
- 历史数据只能确定性映射；冲突或孤儿数据必须中止或形成明确人工映射。
- 不得把未知数据强制归入默认 Tenant。

## 16. 验收清单

- 平台创建 Tenant 时只创建首个公司主管和默认租户基础数据。
- 公司主管可直接创建账号或邀请注册。
- 邀请码只绑定 Tenant，不赋角色。
- 审核通过时只分配选择的 Role。
- `store_staff` 必须填写门店名称并原子创建稳定门店身份。
- 角色移除和账号停用必须原子停用 Store、Binding、企微实例和 AI 自动回复，恢复时不得创建重复门店身份或静默启用企微实例。
- 企微 OAuth 和企微绑定不创建 User 或角色。
- Tenant A 无法读取、修改、导出、统计或订阅 Tenant B 数据。
- 公司主管、客服组长、客服和门店员工的数据范围符合职责。
- 权限管理页能看到所有有效权限，不存在隐藏权限。
- Company 页面、API、权限和运行时依赖已退役。
- 丽斯未来仿真租户满足 1 Tenant、0 活跃旧 Company、100 Store、100 个持有 store_staff 角色的系统账号、100 Binding、100 企微实例、500 Customer，并保留预期会话和派单数据。
- SQLite 和 MySQL 均可完成全新安装、重复 migration、Seed/report 和完整性审计。

## 17. 关键验证命令

```bash
go test ./... -count=1
go vet ./...
pnpm --dir web typecheck
pnpm --dir web lint
cd web && node --test $(rg --files -g '*.test.mjs')
pnpm --dir web build

go run ./cmd/customer_audit_seed --config <config> --action seed
go run ./cmd/customer_audit_seed --config <config> --action report
go run ./cmd/tenant_integrity_audit --config <config>
```
