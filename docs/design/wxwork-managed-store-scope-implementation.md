# 企微员工号、门店身份与客服组范围实施设计

> 状态：当前权威设计
>
> 更新时间：2026-07-20
>
> 企业微信员工号协议字段、类型和能力必须以 `https://wework.apifox.cn/llms.txt` 及其链接的接口页为唯一依据。本文只定义 AgentDesk 内部身份、绑定和管理边界。

## 1. 最终业务语义

一个租户公司可以经营多家门店，但系统不再维护租户公司与门店之间的第二套“客户企业”层级。门店身份由已有系统账号获得 `store_staff` 角色时建立。

```text
Tenant（购买并使用系统的租户公司）
  -> User（公司主管创建或邀请注册的系统账号）
  -> store_staff 角色
  -> StoreStaffBinding（账号与门店身份的唯一关系）
  -> Store（该账号代表的唯一门店）
  -> WxWorkProtocolInstance（实际接待客户的企微员工号）
  -> Customer / Conversation / KnowledgeBase / AgentTeam
```

必须坚持以下表达：

- `User` 是登录 AgentDesk 的系统账号。
- `store_staff` 是现有角色，不是新账号类型。
- `Store` 是该角色账号代表的稳定门店业务身份。
- `WxWorkProtocolInstance` 是实际企微员工号，只绑定已有门店身份。
- 一个有效 `store_staff` 用户只对应一个有效 `StoreStaffBinding` 和一个 `Store`。
- 一个持有 `store_staff` 角色的有效系统账号在当前产品阶段代表一家门店，没有更小的门店层级。

禁止以下概念：

- 独立“门店账号”实体或开户流程。
- “邀请开户”“远程开户”“门店开户注册”等产品语义。
- 企微扫码或企微 OAuth 自动创建 User、自动分配角色。
- 邀请码自动赋予客服、组长或门店员工号角色。
- 活跃运行链路继续使用历史 `Company` 作为租户内层级。

## 2. 账号和门店身份生命周期

### 2.1 公司主管创建账号

公司主管在用户管理创建本租户账号，可同时选择角色。若角色集合包含 `store_staff`：

1. 前端必须显示并校验“门店名称”。
2. 后端在同一个事务中创建 User、写 UserRole、创建 Store 和 StoreStaffBinding。
3. Store、Binding 和 User 必须具有相同 `TenantID`。
4. 新写入的历史兼容 `CompanyID` 固定为 `0`。
5. 任一步失败，账号、角色和门店身份全部回滚。

### 2.2 邀请注册

公司主管可以发送带租户邀请码的注册链接。邀请码只确定租户归属：

```text
邀请注册链接
  -> 用户提交基础账号资料和公司邀请码
  -> User 进入待审核状态并绑定 TenantID
  -> 公司主管审核
  -> 审核通过时分配角色
  -> 如选择 store_staff，同时填写门店名称并创建门店身份
```

邀请注册与企微绑定是两条独立链路。邀请注册创建系统账号；企微绑定只给已有账号关联实际企微员工号。

### 2.3 后续角色分配

给已有 User 分配 `store_staff` 时，`UserService.AssignRolesWithStoreName` 在角色事务内调用门店身份准备逻辑：

- 首次分配要求门店名称并创建唯一 Store + Binding。
- 重新分配角色时复用并恢复已有稳定 Store + Binding，不创建第二家门店。
- 已有绑定但未再次填写门店名称时保留原名称。
- 多个有效绑定属于历史数据错误，拒绝继续操作并要求先修复。
- 移除 `store_staff` 时在同一事务内停用 Store、Binding 和相关企微实例，并关闭实例 AI 自动回复。

### 2.4 账号状态和删除

账号停用会撤销登录会话，并在同一事务内停用 Store、Binding 和相关企微实例。账号重新启用或重新获得 `store_staff` 角色时复用原 Store + Binding，不新建第二套身份；已经停用的企微实例不会被静默重新启用，必须由有权限的操作者确认实际登录状态后恢复。

账号删除继续使用 UserService 的依赖保护：

- 有未完成会话时不能删除客服账号。
- 仍为客服组或客服小组组长时必须先更换组长。
- 仍有关联客服档案时必须先处理客服档案。
- 门店身份及企微实例的历史记录继续保留，删除动作不能物理抹掉审计证据。

这些约束既避免角色、组织、企微和历史会话被静默拆断，也允许公司主管通过角色和账号状态完成可逆的日常停用。

## 3. 企微员工号绑定

### 3.1 绑定前提

企微员工号只能绑定满足以下条件的账号：

- 属于当前活动 Tenant。
- User 已启用且审核通过。
- 已持有启用的 `store_staff` 角色。
- 已有唯一有效 StoreStaffBinding 和 Store。
- 远程绑定时已登记可验证邮箱。

不满足条件时，页面应提示公司主管先在用户管理创建或邀请注册账号并分配角色。

### 3.2 现场绑定

公司主管在用户管理或企微账号管理中选择已有、持有 `store_staff` 角色的系统账号，再启动协议扫码。扫码只负责：

- 获得真实企微员工号身份。
- 建立 WxWorkProtocolInstance。
- 写入现有 BindingID、StoreID 和 TenantID。
- 同步门店名称、客服组缓存和后续运行配置。

扫码不得创建 User、Role、UserRole、Store 或第二个 Binding。

### 3.3 异地自助绑定

历史“远程开户链接”统一改为“企微员工号绑定链接”。公司主管先选择已有系统账号，系统生成一次绑定链接；门店人员用真实企微员工号扫码并以该系统账号登记邮箱完成验证。

绑定链接已经锁定：

- TenantID。
- StoreStaffBindingID。
- StoreID。
- 目标 UserID。

绑定页可以补充门店展示资料，但不能注册系统账号、改变角色或切换租户归属。

### 3.4 企业微信 OAuth 登录

企微 OAuth 登录只允许把企微身份关联到已存在、已启用、邮箱已验证的 User。查不到账号时明确提示联系公司主管创建账号或邀请注册，不执行隐式注册。

### 3.5 更换企微员工号

更换登录员工号继续复用同一个 StoreStaffBinding 和 Store：

- 新实例通过 `ReplacesInstanceID` 指向旧实例。
- 完成验证后禁用旧实例并记录 `ReplacedByInstanceID`。
- 门店知识库、客服组归属和已授权模型范围继承现有门店身份。
- 不创建新 User、Role、Store 或 Binding。

## 4. 客服组范围

`StoreStaffBinding.AgentTeamID` 是门店员工归属综合客服组的事实源。`WxWorkProtocolInstance.AgentTeamID` 只是为派单查询保留的同步缓存。

两个入口操作同一事实：

- 用户管理：给单个门店员工选择客服组或暂未分配。
- 客服组编辑：双列筛选和批量选择多个门店员工。

保存后必须在同一事务中：

1. 更新 StoreStaffBinding.AgentTeamID。
2. 同步该 Binding 下 WxWorkProtocolInstance.AgentTeamID。
3. 重建 AgentTeam 的企微实例范围缓存。
4. 保证跨租户账号、门店和客服组不能互相绑定。

门店员工号不固定属于某个客服个人。人工任务进入所属综合客服组，再由排班、小组和派单规则分配给客服。

## 5. 客户、会话和知识库关系

客户主档只归属 Tenant。客户从哪个门店进入由 `StoreCustomerRelation`、ConversationRouteState 和 WxWorkProtocolInstance 共同表达：

```text
Customer
  -> StoreCustomerRelation(StoreID, WxWorkInstanceID)
  -> ConversationRouteState(StoreID, WxWorkInstanceID, KnowledgeBaseID)
```

因此：

- Customer 不再选择或展示历史 Company。
- “全部账号”下选择会话时保持全部会话上下文，并高亮来源企微员工号。
- 点击具体企微员工号时只筛选该账号来源的客户会话。
- 门店知识库通过 StoreID 归属，企微实例可绑定该门店自己的 KnowledgeBase。
- 历史 CompanyID 不参与客户筛选、会话路由、知识选择或派单。

## 6. AI 和模型边界

模型访问链保持：

```text
平台 AIConfig
  -> TenantAIModelGrant
  -> 租户默认 StoreAIModelSetting
  -> 企微员工号 StoreAIModelSetting 覆盖
```

本设计不改变模型供应商、API Key、Token、usage 或计费语义。历史 CompanyID 仅可作为旧 usage 证据保留，不再作为模型授权或运行时选择条件。

回复意图配置使用：

- 平台全局意图。
- Store 范围意图。
- WxWorkProtocolInstance 范围意图。
- 企微实例显式绑定的行业 Profile。

迁移 63 禁用旧 Company 范围意图并清空旧 Company 默认行业 Profile。运行时不得再回退到 Company。

## 7. 页面职责

| 页面 | 职责 |
| --- | --- |
| 接入公司 | 平台管理员创建、查看和切换 Tenant；不管理租户内门店 |
| 用户管理 | 公司主管创建账号、邀请注册、审核、分配角色、填写门店名称、查看门店/企微/客服组归属 |
| 客服档案 | 管理综合客服组、客服小组、排班和门店员工服务范围 |
| 企微员工号 | 查看和维护已有门店身份绑定的真实企微实例 |
| 门店工作台 | `store_staff` 用户维护自己门店允许维护的资料和托管配置 |
| 会话 | 客服处理从企微员工号进入的客户会话，并识别来源门店 |

取消 `/dashboard/companies` 和 `/dashboard/company-detail`。这两个历史路由必须被前端守卫和后端路由共同拒绝。

## 8. 权限

不新增平行权限体系：

- `tenant.*`：平台租户公司管理和切换。
- `tenantInvite.*`、`tenantRegistration.*`：邀请注册和审核。
- `user.*`、`user.assignRole`：租户账号与角色分配。
- `role.*`、`permission.*`：角色和权限目录。
- `channel.*`：企微员工号读取、创建绑定、更新和删除。
- `agentTeam.*`：门店员工客服组归属。
- `storeWorkbench.*`：门店工作台。

迁移 63 删除 `company.view/create/update/delete` 及角色关系。页面显隐不能替代后端权限和 Tenant 范围校验。

## 9. 历史 Company 兼容边界

`Company` 模型和 repository 暂时保留，仅用于：

- 已执行历史 migration 的重放与审计。
- 旧 Customer 和 AI usage 证据追溯。
- 生产升级前后的数据完整性检查。

现行代码必须满足：

- 不注册 Company Dashboard API。
- 不提供 Company request/response DTO、builder 或前端 API。
- 不提供 Company 页面、选择器、导航或权限。
- Store、Binding、WxWork、门店知识库和门店模型设置的新写入 CompanyID 为 0。
- CompanyID 不参与运行时回复、路由、派单和知识选择。

## 10. Migration 63

`000063_retire_legacy_company_store_scopes.go` 执行以下幂等处理：

1. 清空 Company.IntentProfileID。
2. 清空 Store、StoreStaffBinding、WxWorkProtocolInstance、门店知识库、门店模型设置以及 FastGPT 门店运行表上的活跃 CompanyID。
3. 清空客服组 CompanyScopeIDs。
4. 禁用旧 Company 范围 ReplyIntentConfig。
5. 删除 `company.*` 权限与 RolePermission 关系。
6. 对无有效 User、无 `store_staff` 角色、跨租户或重复绑定的数据禁用 Binding 和 AI 回复。
7. 只为已有、启用、已持有 `store_staff` 的租户 User 回填缺失的 Store + Binding。

迁移不得创建 User、赋予角色或改变历史 Customer/AI usage Company 证据。

## 11. 验收不变量

- 新建系统账号并分配 `store_staff` 角色产生 1 User、1 UserRole、1 Store、1 Binding。
- 缺门店名称时整个角色事务回滚。
- 重复分配角色不新增 Store 或 Binding。
- 移除角色或停用账号会停用 Store、Binding 和相关企微实例；恢复时复用原 Store + Binding，企微实例保持停用等待人工确认。
- 企微 OAuth 和企微协议绑定都不新增 User 或 UserRole。
- 邀请码只决定 TenantID。
- 活跃 Store、Binding、WxWork、门店知识库和门店模型设置 CompanyID 为 0。
- 一个有效 Binding 的 User 必须存在、启用、属于同一 Tenant 并持有 `store_staff`。
- 一个有效 WxWork 实例必须绑定同租户的 Store 和 Binding。
- 客服组双向绑定只写 Binding 事实并同步实例缓存。
- 历史 Company 页面、API 和权限均不可访问。
- 企微消息发送仍按协议使用 `conversation_id`，单聊 `S:`、群聊 `R:`。

关键验证：

```bash
go test ./internal/migration ./internal/services -count=1
go test ./... -count=1
pnpm --dir web typecheck
pnpm --dir web lint
cd web && node --test $(rg --files -g '*.test.mjs')
pnpm --dir web build
```
