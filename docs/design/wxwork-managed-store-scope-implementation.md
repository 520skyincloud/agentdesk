# 企微员工号、门店身份与客服组范围实施设计

> 状态：当前统一项目权威设计
>
> 更新时间：2026-07-27
>
> 企业微信员工号协议唯一依据：
> `https://wework.apifox.cn/llms.txt` 及其链接的具体接口页面

本文只定义 AgentDesk 内部 User、Store、企微实例、客服组、模型和知识范围。协议请求
字段、类型、必填项、状态码和示例必须在开发前重新查阅官方接口页，禁止与企业微信 CLI、
微信客服 API、个人微信协议或旧协议字段混用。

## 1. 身份模型

```text
Tenant
  -> User + store_staff Role
  -> StoreStaffBinding
  -> Store
  -> WxWorkProtocolInstance
  -> Customer / Conversation / KnowledgeBase / AgentTeam
```

- User：登录 AgentDesk 的系统账号。
- `store_staff`：User 的角色，不是独立账号类型。
- StoreStaffBinding：User 与稳定 Store 的唯一关系。
- Store：该门店员工系统账号代表的业务门店。
- WxWorkProtocolInstance：该 Store 的实际企微渠道身份。

一个有效 `store_staff` User 最多占用一个活动 Store。数据库使用
`StoreStaffBinding.ActiveUserID` 的可空唯一索引保证这一点，Service 事务同时校验
User、Role、Tenant、Store 和 Binding。

Company 模型和 CompanyID 不存在。系统也不存在“门店账号开户”“邀请开户”或
“客户企业 -> 门店”的第二层。

## 2. 系统账号与 Store 生命周期

### 2.1 创建或邀请账号

公司主管可以直接创建本 Tenant User，或发送带 Tenant 邀请码的注册链接。邀请码只绑定
Tenant，注册完成后仍需审核和显式分配 Role。

若角色包含 `store_staff`：

1. 必须填写门店名称。
2. User/UserRole 与 Store/Binding 在同一业务事务内提交。
3. Store、Binding、User 的 TenantID 必须一致。
4. 同一事务建立未配置 Store Credential/Policy 和默认关闭的客户标签运行策略。
5. 任一步失败全部回滚。

### 2.2 角色变更

- 首次分配 `store_staff` 创建 Store + Binding。
- 重复分配或重新启用复用原 Store + Binding。
- 已有多个 Binding、跨 Tenant User、缺 Role 或缺 Store 时拒绝继续。
- 移除角色或停用 User 时清空 `ActiveUserID`，停用 Binding、Store 和相关企微实例。
- 恢复 User 或 Role 不会静默重新启用已停用企微实例；必须重新确认真实协议状态。

Store 的历史 ID 只能由当前数据库生成，不能使用来源库 ID 或固定编号。

### 2.3 删除与转移

- Store/Binding/企微历史和审计事实不能因删除账号而物理抹除。
- 仍有关联客服组、客服档案、进行中会话或运行任务时，必须先按现有依赖保护处理。
- Store 停用、转移或删除只停止本系统使用 Store Credential，不负责上游 NewAPI Key
  的停用、旋转或删除。

## 3. 企微实例绑定

### 3.1 绑定前提

目标系统账号必须：

- 属于当前活动 Tenant。
- 已启用并完成必要审核/改密。
- 持有启用的 `store_staff` Role。
- 拥有唯一活动 StoreStaffBinding 和 Store。
- 异地绑定时满足现有邮箱验证要求。

不满足时，页面提示先完成账号与角色链，不能让扫码隐式补建身份。

### 3.2 现场扫码

扫码只负责：

- 按官方协议获取真实企微身份。
- 建立或更新 WxWorkProtocolInstance。
- 写入已验证的 TenantID、StoreID 和 StoreStaffBindingID。
- 同步 Store 展示资料、客服组缓存和当前运行配置。

扫码不得创建 User、Role、UserRole、Store 或第二个 Binding。

### 3.3 异地绑定链接

“企微员工号绑定链接”在生成时锁定：

- TenantID。
- UserID。
- StoreID。
- StoreStaffBindingID。

使用者只能完成目标企微实例绑定，不能切换 Tenant、注册系统账号、修改角色或更换 Store。
链接必须有过期、使用状态和审计。

### 3.4 OAuth 与替换

- 企微 OAuth 只能关联已经存在、已启用且满足验证条件的 User。
- 查不到账号时明确要求联系公司主管，不自动注册。
- 更换企微实例复用相同 Store/Binding；新旧实例通过
  `ReplacesInstanceID/ReplacedByInstanceID` 留下替换链。
- 历史实例可保留审计，但同一渠道运行链只能使用当前有效实例。

## 4. 协议消息约束

- 消息发送必须使用官方文档的 `conversation_id`。
- 单聊联系人 ID 以 `S:` 开头，群 ID 以 `R:` 开头。
- 文档没有说明的能力或字段不得实现成假功能；后端返回明确错误，页面标注协议暂不支持。
- 实例 ID、会话 ID 等协议整数必须按文档精度处理，禁止经前端 Number 造成丢失。
- 入站和出站必须从实例反查 Tenant + Store，不能信任调用方提交的范围。
- 出站消息先提交 Message 和持久投递意图，再幂等确保 Outbox，最后发布 WebSocket。

## 5. 客服组与规则派单

`StoreStaffBinding.AgentTeamID` 是门店员工所属综合客服组的事实源。
`WxWorkProtocolInstance.AgentTeamID` 是同步缓存，只用于高频路由查询。

两个入口复用同一能力：

- 用户管理：给单个门店员工选择客服组。
- 客服组管理：批量选择多个门店员工。

同一事务必须：

1. 更新 Binding.AgentTeamID。
2. 同步该 Binding 下当前企微实例的 AgentTeamID。
3. 重建客服组实例范围缓存。
4. 拒绝跨 Tenant User、Store、Binding、实例或客服组。

门店员工账号不等于具体客服。人工任务先进入综合客服组，再按客服小组、排班、
Presence、容量、公平债务、SLA 和恢复规则确定性派单。AI 不能直接选择客服。

## 6. 客户、会话与知识

```text
Customer(TenantID)
  -> StoreCustomerRelation(StoreID, WxWorkInstanceID)
  -> ConversationRouteState(StoreID, WxWorkInstanceID, KnowledgeBaseID)
```

- Customer 主档归 Tenant。
- 同一自然客户在不同 Store 有独立 StoreCustomerRelation 和客户标签。
- ConversationRouteState 固化当前会话来源，回复、派单、知识和统计都从它恢复范围。
- KnowledgeBase 必须属于同一 Tenant + Store。
- “全部企微账号”显示全范围会话并标识来源；选择具体实例时只筛选该实例来源。
- 不存在 Company 选择器、Company 过滤或 Company fallback。

## 7. 行业与模型边界

### 7.1 行业

- 行业唯一来自 `Tenant.IntentProfileID`。
- Store、Binding、企微实例和知识库不能覆盖行业。
- `WxWorkProtocolInstance.PersonaPrompt` 只影响 Generate 表达语气，不能改变行业
  Intent Prompt/Schema、标签目录、模型或派单。

### 7.2 模型

```text
StoreModelProfileAssignment
  + StoreModelCredential
  + Store FastGPT readiness
  -> ModelCallResolver
```

- Profile 和 Credential 都属于 Store，不属于 WxWorkProtocolInstance。
- 企微页面只可展示 Store 的模型名、revision 和 readiness。
- 租户侧不得看到 Provider、BaseURL、Prompt、Schema、密钥、nonce 或完整 fingerprint。
- 没有 AIConfig、Tenant 授权池、Tenant 默认模型、企微覆盖和平台 Key fallback。

更换企微实例不会创建新 Profile、Credential、FastGPT Team 或 KnowledgeBase；这些事实
继续跟随 Store。

## 8. 页面职责

| 页面 | 当前职责 |
| --- | --- |
| 接入公司 | 平台创建、查看和切换 Tenant |
| 用户管理 | Tenant 账号创建、邀请、审核、角色和 Store 身份 |
| 客服组织 | 综合客服组、小组、排班及门店员工组归属 |
| 企微员工号 | 已有 Store 身份的扫码、状态、替换和渠道配置 |
| 门店工作台 | `store_staff` 只管理自己 Store 允许的资料和策略 |
| 会话 | 客服处理客户会话并查看来源 Store/企微实例 |
| 知识库 | 当前 Tenant + Store 的 FastGPT 托管知识 |
| 模型凭据/账单 | 复用 Store 能力并按权限与数据范围裁剪 |

Company 页面、Company 详情、旧模型授权页和企微模型覆盖配置均不存在。

## 9. 权限与范围

- `tenant.*`：平台 Tenant 管理与切换。
- `tenantInvite.*`、`tenantRegistration.*`：邀请、注册和审核。
- `user.*`、`user.assignRole`：Tenant 账号与角色。
- `channel.*`：企微实例管理。
- `agentTeam.*`：客服组织与门店员工组归属。
- `storeWorkbench.*`：门店工作台。
- `storeModelCredential.*`、`billing.*`：Store 模型凭据与账单。

页面隐藏不能替代 Handler 权限和 Service Tenant/Store 范围校验。所有权限必须出现在权限
管理中，通过 Role 分配。

## 10. Fresh 数据库与验收

当前版本不提供 Company/CompanyID 迁移、历史 Store 回填或企微实例搬运。空 SQLite/MySQL
由 AutoMigrate 创建当前 Schema；Store 和实例只能经当前产品链产生。

必须满足：

- 分配 `store_staff` 原子产生 1 Store、1 Binding 和默认 Store 配置。
- 缺门店名称或任一范围冲突时事务回滚。
- 一个 User 不能拥有多个活动 Store 占用。
- 企微扫码/OAuth/绑定链接不新增 User、Role、Store 或 Binding。
- 停用后恢复复用原 Store/Binding，企微实例不自动启用。
- 有效实例必须绑定同 Tenant 的 Store 和 Binding。
- 客服组批量操作只写 Binding 事实并同步实例缓存。
- 消息发送继续使用协议 `conversation_id`，单聊 `S:`、群聊 `R:`。
- 旧 Company 与旧模型链在代码、路由和 fresh Schema 中不存在。

关键验证：

```bash
go test ./internal/services -run 'StoreStaff|WxWork|AgentTeam|Dispatch' -count=1
go test ./internal/bootstrap ./internal/models ./internal/migration -count=1
go test ./... -count=1
pnpm --dir web typecheck
pnpm --dir web build
```
