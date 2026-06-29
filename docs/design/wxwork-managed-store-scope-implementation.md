# 企微员工号托管策略、客服组知识权限与已读闭环实施设计

## 1. 目标与约束

本轮把企微员工号运营逻辑从“协议实例配置”提升到“公司-门店-门店员工系统账号-协议实例”的业务结构，解决以下问题：

- 小程序与企业绑定，员工企业微信与企业绑定；知识库按公司/门店授权给客服组组长查看和维护。
- 一个客服可作为多个客服组组长，权限按这些客服组绑定的公司、门店、员工号范围汇总。
- 门店托管模式绑定到门店员工系统账号：每个门店只允许一个门店员工账号，该账号绑定公司和门店，协议实例再绑定这个门店员工账号。
- AI 自动回复成功后，必须调用 wework 文档中的“标记会话已读”接口，避免客户侧已读状态异常。
- 外部群真实获取失败需要改为严格按 wework 文档补齐群详情链路，不要求用户手写群 ID。
- 智能客服配置里的兜底策略不能只回复“记录一下”，需要变成自然、可执行、可转人工的策略。

协议相关开发继续遵守 `https://wework.apifox.cn/llms.txt`，尤其消息发送和标记已读使用 `conversation_id`，单聊 `S:`，群聊 `R:`。

## 2. 数据与权限设计

### 2.1 门店员工系统账号

新增门店员工绑定模型 `StoreStaffBinding`：

- `userId`：系统用户 ID，来自企业微信 OAuth/扫码登录 AgentDesk。
- `companyId`：所属企业/品牌。
- `storeId`：所属门店，唯一索引；每个门店只允许一个门店员工系统账号。
- `managedMode`：托管模式，枚举：
  - `full` 全托管：转人工全部进入总部网页端，不发门店群提醒。
  - `semi` 半托管：按服务时间/排班判断，值班时间发门店群，非值班进入总部网页端。
  - `none` 非托管：转人工只发门店群，不进入总部网页端。
- `serviceHours`、`storeRoomConversationId`、`storeRoomAtList`、`storeRoomNotifyEnabled`、`fallbackToHQ`、`manualTimeoutMinutes`：原员工号上的门店运营配置迁移到这里作为最终生效配置。
- 审计字段：创建/更新用户、时间。

`WxWorkProtocolInstance` 新增 `storeStaffBindingId`，运行时通过实例找到门店员工系统账号，再找到托管模式和门店群策略。兼容期保留实例上的旧字段，迁移时回填到 `StoreStaffBinding`；如果实例尚未绑定门店员工账号，则继续读取旧字段作为降级兼容，但 UI 不再鼓励编辑旧字段。

### 2.2 公司、知识库与客服组长范围

客服组 `AgentTeam` 新增/使用公司范围字段：

- 如果已有公司范围字段则复用；如果没有，新增 `companyScopeIds`，逗号分隔。
- 客服组的 `storeScopeIds` 和 `wxWorkInstanceScopeIds` 继续保留。
- 一个用户可作为多个客服组组长：通过 `AgentTeam.leaderUserId = userId` 汇总所有启用客服组。
- 对客服组长可见范围：
  - 公司：所有所管理客服组的 `companyScopeIds`；若为空，则由组内 `storeScopeIds` 对应门店反推公司。
  - 门店：客服组显式 `storeScopeIds`，加上公司下所有门店。
  - 员工号：客服组显式 `wxWorkInstanceScopeIds`，加上可见门店下绑定的员工号。
  - 知识库：可见公司/门店下的知识库。
  - 知识进化：只展示可见知识库对应的候选项。

超管/管理员不受范围限制。普通客服仍按现有客服档案/客服组范围看会话，不获得知识库维护权限。

### 2.3 门店托管模式路由

转人工决策以 `StoreStaffBinding.managedMode` 为准：

- 全托管 `full`：始终创建总部网页端待接管，显示弹窗和感叹号；不发门店群。
- 半托管 `semi`：命中门店服务时间且群已绑定时，发门店群通知；否则进入总部网页端。
- 非托管 `none`：始终发门店群通知；如果未绑定群或发送失败，只记录配置告警，不自动进入总部网页端，避免违背门店非托管设定。

## 3. 后端实现

### 3.1 模型、迁移与 DTO

- 新增 `StoreStaffBinding` 模型并加入 AutoMigrate。
- 新增 `ManagedMode` 枚举或字符串常量：`full/semi/none`。
- `WxWorkProtocolInstance` 增加 `StoreStaffBindingID`。
- `AgentTeam` 增加 `CompanyScopeIDs`。
- 新增幂等 migration：
  - 同步新字段和默认角色/权限。
  - 按现有 `WxWorkProtocolInstance.storeId` 自动创建或复用门店绑定；同一门店只创建一条绑定。
  - 将实例旧门店群/服务时间/超时配置回填到绑定。
  - 将实例绑定到对应 `StoreStaffBindingID`。

### 3.2 企业微信登录 AgentDesk

企业微信 OAuth 首次登录后：

- 继续创建 `User + UserIdentity`。
- 默认分配 `store_staff` 角色。
- 如果登录链接带门店开户链接 token，则绑定到该 token 对应的 `companyId/storeId`；若该门店已有绑定，则提示“该门店已绑定门店员工账号”。
- 门店员工系统账号不等于协议实例登录账号；协议实例只通过 `storeStaffBindingId` 关联它。

### 3.3 客服组长知识权限过滤

新增服务方法 `AgentTeamScopeService.ResolveManagedScope(user)`：

- 输入当前登录用户。
- 输出 `companyIds/storeIds/wxWorkInstanceIds/knowledgeBaseIds/isUnrestricted`。
- 超管/管理员 `isUnrestricted=true`。
- 客服组长按所有 `leaderUserId=userId` 的启用客服组汇总。

以下接口应用该 scope：

- 知识库列表、详情、创建/更新/删除、重建索引。
- 知识进化候选列表、批量通过/拒绝、质量检查、对话分析、导出。
- 员工号账号选择列表和门店工作台可见账号。

写操作必须校验目标知识库在 scope 内；不在范围内返回无权限。

### 3.4 AI 回复后标记企微会话已读

- 在企微协议 outbox 文本/媒体消息发送成功后，拿到当前 `conversation_id`。
- 异步或同步调用 `/msg/report_unread`，body 使用 wework 文档字段：`guid`、`conversation_id`。
- 标记已读失败不回滚消息发送，但写 `MessageSyncLog` 和结构化日志。
- UI 可显示消息 sent，不因已读标记失败误判 failed。

### 3.5 外部群真实获取

当前 `/room/get_room_list` 在部分账号上只返回有限群列表，不能要求运营人员手填群 ID。真实链路改为三步，字段严格来自 `wework.apifox.cn`：

- `/room/get_room_list`：body 为 `guid/start_index/limit`，用于获取当前员工号可直接枚举的客户群。
- `/room/batch_get_room_detail`：body 为 `guid/room_list`，用于对已知群 ID 批量拉取详情。选择群、保存群或从回调里发现新群后，都应先通过该接口校验群仍可用。
- `/room/sync_room_info`：body 为 `guid/room_id/version`，用于增量同步群信息。`version` 默认 `0`，后续如果协议响应返回版本号再持久化。
- `/room/batch_get_member_detail`：body 为 `guid/room_id/user_list`，用于成员详情补全和 @ 成员下拉。若协议没有返回全量成员列表，页面只能展示接口真实返回的成员，不允许编造成员 ID。
- 如果文档确认某接口只能返回群主客户群，则 UI 文案明确“只展示该员工号可管理/可同步的群”，不允许手填猜测 ID。

### 3.6 智能客服兜底策略

更新账号专属 `AIAgent` 默认系统提示词和运行时兜底逻辑：

- 禁止固定回复“记录一下”。
- 知识不足时先区分：可追问、可转人工、可发小程序/定位、需要门店处理。
- 对可回答的常见问题必须基于知识库/门店配置给简短自然答复。
- 对需要人工或门店动作的问题，不承诺已处理；缺房号/数量/时间时自然追问一个关键字段；无法自动处理时转人工。
- 保持真人微信口吻，短句，不用“亲/这边/为您/～/固定 emoji”。

## 4. 前端实现

### 4.1 门店工作台

`/dashboard/store-workbench` 做成真实自适应页面：

- 顶部显示当前绑定公司、门店、门店员工账号。
- 配置托管模式：全托管/半托管/非托管，带简短说明。
- 配置服务时间、门店群、@ 成员、一键获取坐标。
- 显示当前绑定员工号实例和智能客服状态。
- 门店员工只能看到本门店数据。

### 4.2 客服组长工作台与知识页面

- 客服组长可在客服组页面配置公司范围、门店范围、员工号范围。
- 知识库页面和知识进化页面按 scope 过滤。
- 知识进化增加全选/批量通过/批量拒绝按钮，批量操作仍校验 scope。

### 4.3 账号管理

- 员工号实例编辑页不再直接编辑托管模式；显示绑定的门店员工账号和托管模式摘要。
- “更换登录员工号”继续保留。
- 门店群选择迁移到门店工作台；总部仍可在账号详情查看只读摘要。

## 5. 测试与验收

- 企业微信登录：新用户默认拥有 `store_staff`，只能看到门店工作台；绑定门店后只能管理自己的门店。
- 唯一绑定：同一门店第二个门店员工账号绑定失败。
- 托管模式：
  - 全托管转人工只进入总部网页端。
  - 半托管值班时间发群，非值班进总部。
  - 非托管只发群，不进总部；未配群只告警。
- 客服组长：同一用户作为多个客服组组长时，知识库/知识进化/员工号可见范围为多个组的并集。
- 知识进化：全选和批量通过只作用于当前 scope 内候选项。
- AI 已读：AI 回复成功后调用 `/msg/report_unread`；body 为 `guid/conversation_id`，其中 `guid` 由后端实例注入，`conversation_id` 使用当前企微协议会话 ID。失败只写日志，不能把已成功发送的 outbox 改为失败。
- 外部群：能通过群列表/同步/详情链路拿到真实群和成员；无法获取时 UI 明确提示协议限制，不允许手填猜 ID。
- 智能兜底：知识不足不再固定“记录一下”，能追问、转人工或调用门店配置资源。
