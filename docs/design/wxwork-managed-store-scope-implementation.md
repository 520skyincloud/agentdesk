# 企微员工号门店范围与会话连续性

> 状态：当前运行设计。员工号协议字段和接口只以 `https://wework.apifox.cn/llms.txt` 及其链接页面为准。

## 1. 唯一身份链

```text
Tenant
  -> Store
    -> StoreStaffBinding
      -> WxWorkProtocolInstance replacement chain
```

- Store 是稳定、独立、可先于账号存在的门店实体。
- 一个 Store 可同时绑定多个门店员工系统账号。
- 一个 User 同时最多有一个活动 StoreStaffBinding。
- StoreStaffBinding.AgentTeamID 是客服组事实源，实例 AgentTeamID 是运行缓存。
- WxWorkProtocolInstance 只表示一次真实企微登录，不创建 Store、User、Role 或 Binding。

门店员工账号创建、邀请或审核时必须选择已有 Store。扫码、OAuth 和异地绑定链接只允许绑定已经存在且通过权限、Tenant 和 Store 校验的账号关系。

## 2. 当前实例与替换

运行时只接受：

```text
status = enabled
AND replaced_by_instance_id = 0
AND (replaces_instance_id = 0 OR remote_setup_submitted_at IS NOT NULL)
```

替换流程：

```text
草稿 -> 二维码登录 -> 邮箱/远程设置验证 -> 原子切换 -> 旧实例归档
```

- 替换草稿可以接收登录状态回调，但不能接管消息、欢迎语、联系人自动化、到店联动、发送、知识或派单。
- 草稿完成验证前旧实例仍是当前实例。
- 切换后旧实例只归档已认证的迟到客户消息，写入原服务段并标记历史，不改变当前会话状态。
- 同一 Binding 出现零个或多个已激活当前实例时失败关闭。

## 3. 会话范围

门店会话键：

```text
TenantID + StoreID + CustomerID + StoreStaffBindingID
```

- 同 Binding 换企微：复用 Conversation，新建 ConversationChannelSession。
- 同 Store 不同 Binding：两条独立会话，通过同 Store + Customer 关系展示为相关会话。
- Binding 也更换：公司主管或客服组长显式建立 ConversationContinuityLink。
- 不同 Store：独立门店关系、会话、标签和记忆。

消息永久保留原 ConversationID、SessionNo 和协议引用。历史展示不搬迁或复制消息，不重复未读、统计、AI、标签、知识进化或账单。

新实例发送前必须先从该实例的真实入站消息获得有效 `conversation_id`。单聊使用 `S:`，群聊使用 `R:`；禁止复用旧实例标识。

## 4. 客户身份

- Customer 是 Tenant 客户主档，CustomerIdentity 保存外部身份映射。
- StoreCustomerRelation 保存该客户在单个 Store 的记忆和标签。
- 只使用协议明确稳定的外部标识自动关联。
- 无法证明同一客户时复用既有人工关联客户能力。
- 禁止用姓名、头像、guid、员工号字段或 conversation_id 猜测自然人身份。

## 5. 门店能力归属

| 能力 | 事实归属 |
| --- | --- |
| 地址、导航、联系方式 | Store |
| 知识库、FastGPT Dataset | Store |
| 行业标签、客户记忆、知识进化 | Store + Customer |
| 模型 Profile | Store |
| NewAPI Credential | StoreStaffBinding |
| AI/用量/账单明细 | StoreStaffBinding + Conversation + Instance |
| 客服组 | StoreStaffBinding |
| 登录、健康、消息序列、conversation_id | WxWorkProtocolInstance |

企微实例更换不创建新知识库或模型 Profile。同 Binding 保留凭据；更换 Binding 时新员工使用自己的凭据和账单归因，但继续共享 Store 知识、标签和客户记忆。

## 6. 派单与到店联动

- 人工任务先进入 Binding.AgentTeamID 对应综合客服组，再按排班、Presence、容量、公平债务和 SLA 派单。
- AI 只判断是否转人工，不指定客服。
- 连续性优先不能绕过公平带和接单资格。
- StoreArrivalConnection 一店一连接，但显式保存所选 Binding 与已激活当前实例。
- 企业微信官方客户联系成员 ID 与员工号协议 ID 是不同命名空间，由管理员人工确认映射，不做字符串或姓名匹配。

## 7. 页面与权限

- 门店管理独立维护 Store。
- 用户管理给门店员工角色账号选择已有 Store。
- 企微员工号页面按 Store/Binding 展示当前实例、替换草稿和历史链。
- 会话页面展示相关会话、继承链和服务段分隔线。
- `conversation.relatedView` 控制相关和继承历史查看。
- `conversation.inherit` 控制不同 Binding 的人工继承。
- 公司主管和客服组长默认拥有；Tenant、Store、客服组范围仍是硬上限。

## 8. 验收

```bash
go test ./internal/services -run 'StoreStaff|StoreConversation|WxWorkProtocol|Arrival|Dispatch' -count=1
go test ./internal/repositories ./internal/bootstrap ./internal/migration ./internal/models -count=1
go test ./... -count=1
pnpm --dir web typecheck
pnpm --dir web test:contracts
pnpm --dir web build
```

真实验收必须覆盖一店多账号、同 Binding 换企微、不同 Binding 独立会话、人工继承、迟到消息归档、出站 conversation_id、权限数据范围和 Store 级知识/标签保留。
