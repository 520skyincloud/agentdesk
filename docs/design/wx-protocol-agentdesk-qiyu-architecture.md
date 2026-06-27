# 企微员工号 + 总部网页客服 + 门店知识库技术架构设计

版本：2026-06-26  
适用系统：AgentDesk 二开版本  
当前入口：企业微信员工号/企微协议 SAAS 接入，后台统一显示为“企微员工号”

## 0. 协议强制前提

企业微信员工号相关开发唯一协议依据是 `https://wework.apifox.cn/llms.txt` 及其链接的具体接口页面。开发任何员工号能力前必须先查看对应页面的字段、类型、必填项和示例，禁止凭记忆或其他协议文档猜字段。

本链路不得与企业微信 CLI、企业微信客服号 / 微信客服 API、个人微信协议、旧 `weixins.apifox.cn` 字段混用。若文档未说明某个能力或字段，系统不能做成“看起来能用”的假功能，必须在 UI 和错误日志里明确说明缺少协议字段或接口。

## 1. 设计目标

本方案的目标是把一百多家门店的企业微信员工号接入 AgentDesk，由 AgentDesk 统一完成消息接收、会话长期绑定、门店知识库检索、AI 回复、门店人工接待、总部网页端兜底接管、消息同步、人工超时恢复、关闭后重开、媒体消息展示和知识进化归档。

当前架构已经废弃七鱼主链路：总部客服统一使用 AgentDesk 网页工作台，不再把客户会话转入七鱼。代码中历史七鱼模型和适配器可暂时保留用于兼容旧数据，但不进入新业务主流程；新开发、新测试、新部署都以“企微员工号 + AgentDesk 网页工作台”为准。

系统里没有总部知识库，也不把公共知识库作为主答链路。每个门店员工号必须绑定唯一门店，每个门店绑定自己的知识库。AI 每次回答只调用当前门店绑定的知识库。

## 2. 核心边界

| 对象 | 职责 | 不做什么 |
| --- | --- | --- |
| 门店企业微信员工号 | 接收客人微信/企微消息，门店员工可在原企微客户端人工回复 | 不接七鱼，不保存总部客服凭证 |
| 企微协议 SAAS | 按 wework.apifox.cn 协议把员工号消息回调给 AgentDesk，按接口代发文本/图片/文件 | 不做业务路由，不判断门店知识库 |
| AgentDesk | 所有消息统一落库、去重、路由、AI、outbox、网页端工作台、同步审计 | 不绕开本地消息表，不把消息分散到外部客服系统 |
| 门店知识库 | 当前门店 FAQ/政策/设施/服务答案 | 不混用总部知识库 |
| 总部网页客服 | 直接在 AgentDesk 三栏工作台查看全部员工号、会话、待人工请求，并可接管回复 | 不需要登录门店企业微信客户端 |

## 3. 门店知识库绑定模型

每个 `WxWorkProtocolInstance` 代表一个企业微信员工号实例，必须绑定：

- `guid`：协议 SAAS 实例 ID。
- `employeeUserId`：当前登录员工号 username，用于判断自己发出的 echo。
- `storeId`：唯一门店。
- `knowledgeBaseId`：该门店唯一知识库。
- `aiAgentId`：可选绑定 AI Agent。
- `aiReplyEnabled`：AI 托管总开关。
- `manualTimeoutMinutes`：人工接待超时分钟，默认 10。
- `fallbackToHQ`：门店未处理时总部网页端可兜底。

正常链路为：

```mermaid
flowchart TD
  A[客人发到门店员工号] --> B[协议 SAAS 回调 AgentDesk]
  B --> C{guid 是否绑定门店和知识库}
  C -- 是 --> D[storeId -> knowledgeBaseId]
  D --> E[AI 只检索当前门店知识库]
  E --> F[Outbox 发回原员工号会话]
  C -- 否 --> G[配置告警 + 固定兜底]
  G --> H[回复: 当前门店配置异常 已通知人工处理]
```

极端情况下实例未绑定门店或知识库，系统不允许 AI 编答案，只写健康告警并回复固定兜底话术。

## 4. 会话长期绑定与轮次

### 4.1 长期身份

单聊长期会话 key：`guid + customerUsername`。  
群聊长期会话 key：`guid + chatroom`，群内发送人记录到消息扩展字段 `chatroom_sender`。

同一客户给不同门店员工号发消息，即使 username 相同，也会因为 `guid` 不同而成为独立长期会话和独立记忆。

### 4.2 会话轮次 Session Window

长期会话不等于一次问题上下文。系统新增 `sessionNo`：

- 新客户第一次来：`sessionNo=1`。
- 会话关闭后客户再次发消息：复用长期会话，但 `sessionNo+1`。
- 人工完成、超时恢复 AI、长时间无消息后再次发言，也可切新轮次。
- AI 默认只读取当前轮次最近消息。
- 跨轮次只允许带稳定事实摘要，例如门店、房号、偏好；不把已解决故障当成当前问题。

例如：客户之前说“马桶堵了”，后来电话已经处理；本次又问“早餐几点”。新轮次里 AI 只回答早餐，不主动提马桶。

## 5. 微信协议 SAAS 接入

协议依据固定为 `https://wework.apifox.cn/llms.txt`，不再依赖企业微信 CLI、微信客服号、旧 `weixins` 字段。

### 5.1 回调解析

- `notify_type=1010/11010`：单条新消息事件。
- `notify_type=1011/11011`：批量新消息事件，拆成多条 `ChatMsgModel`。
- `ChatMsgModel` 字段：`msg_id/from_username/to_username/chatroom_sender/create_time/desc/msg_type/chatroom/source/content`。
- 其他 notify type 仅做实例状态或审计日志，不触发 AI。

### 5.2 方向判断与去重

- 当前实例自己的账号存到 `employeeUserId`。
- 发送人不是当前实例账号：客户消息，写 `Message(customer)`。
- 发送人是当前实例账号：平台 echo，默认只确认 outbox，不创建可见 agent 消息，不触发 AI。
- 幂等 key：`wx_protocol:{guid}:{msg_id}`。
- 空内容、系统消息、撤回消息、未知消息只写审计，不触发 AI。

### 5.3 出站发送

- 文本：`/msg/send_text`，body 为 `{guid,conversation_id,content}`。`conversation_id` 必须按文档带前缀：单聊联系人 `S:<联系人ID>`，群聊 `R:<群ID>`。
- 图片：先通过协议文档里的云存储上传接口拿到图片发送参数，再调用 `/msg/send_image`，body 为 `{guid,conversation_id,file_id,size,image_width,image_height,aes_key,md5,is_hd}`。
- 文件：先通过协议文档里的云存储上传接口拿到文件发送参数，再调用 `/msg/send_file`，body 为 `{guid,conversation_id,file_id,size,file_name,aes_key,md5}`。
- 网页端上传到 AgentDesk 本地资产库只代表“后台可展示”，不等于微信协议可发送；没有 `wxMedia.file_id/aes_key` 时必须明确失败，错误写入 outbox，不允许伪装成文本或误标 sent。
- HTTP 200 但业务错误码不为 0 必须标记 failed，不能误判 sent。
- AI/人工消息入 outbox 后立即触发一次发送，后台定时扫描只做兜底。

## 6. 多媒体消息

| 类型 | 入站处理 | 是否触发 AI | 出站处理 |
| --- | --- | --- | --- |
| 文本 | 写 Message(text) | 是 | `/msg/send_text` |
| 图片 | 注册 asset，聊天区展示图片 | 否 | 需要按 wework 云存储上传得到 `file_id/aes_key/size/md5` 后 `/msg/send_image` |
| 语音 | 下载/保存音频资产，展示播放器；转写成功后可作为文本问题 | 转写成功才可触发 | 本期不做语音出站 |
| 文件 | 保存资产，展示文件卡片 | 否 | 需要按 wework 云存储上传得到 `file_id/aes_key/size/md5/file_name` 后 `/msg/send_file` |
| 视频 | 保存资产，展示播放器或文件卡片 | 否 | 按文件能力发送 |

当前实现先完成媒体入站审计、资产注册和网页展示；语音转文字需要结合协议下载返回和 ASR 配置继续扩展。没有转写时不让 AI 猜测图片/语音内容。
如果客户几乎同时发送图片和文本，系统只用文本触发 AI，并在上下文里知道“收到过图片/文件”，但不能把图片内容当成已识别事实；需要 OCR/人工查看后才能基于图片内容回复。

## 7. AI 回复链路

```mermaid
sequenceDiagram
  participant C as 客户微信
  participant W as 门店员工号/协议SAAS
  participant A as AgentDesk
  participant K as 门店知识库
  participant O as Outbox

  C->>W: 发消息
  W->>A: notify 1010/1011 ChatMsgModel
  A->>A: guid+客户username 找长期会话
  A->>A: 判断 sessionNo / 去重 / echo过滤
  A->>K: 只检索当前门店 knowledgeBaseId
  K-->>A: 命中内容
  A->>A: 生成自然前台同事风格回复
  A->>O: 写 ChannelMessageOutbox
  O->>W: /msg/send_text
  W->>C: 客户收到回复
  W-->>A: echo 回调
  A->>A: 仅确认发送状态 不重复入库
```

AI 提示词要求：短句、自然、不说“根据知识库”、不自称 AI；维修、漏水、投诉、安全场景先安抚并表达会帮忙反馈或转人工。

## 8. 转人工与人工生命周期

### 8.1 门店人工优先

客户发送“转人工/人工/客服”等意图后：

1. AgentDesk 写 `Message(customer)`。
2. AI tool 只返回结构化转人工结果，不再额外自然语言发第二条。
3. `ConversationHumanDispatchService` 统一发送一条客户可见提示：“已经帮您通知门店同事了，我会继续关注。”
4. route 进入 `STORE_WECOM_MANUAL`，`needHumanFollowUp=true`。
5. 门店员工可继续在原企业微信客户端回复；总部网页端同时出现右下角弹窗和列表感叹号，可兜底接管。
6. AI 在人工状态下停答。

重复发送“人工”在冷却窗口内不重复创建消息，只提示当前状态。

### 8.2 总部网页端接管

总部客服在 AgentDesk 点击接管后：

- route 进入 `HQ_AGENTDESK_SERVING`。
- 客户后续消息继续进入同一长期会话和当前 session。
- AI 停答。
- 总部网页端回复写 `Message(agent)`，再由原 `wx_protocol` outbox 发回客户所在门店员工号会话。
- 企微 echo 回调只确认发送，不重复展示。

### 8.3 超时恢复 AI

门店人工、总部网页人工均遵循默认 10 分钟超时规则：

- 以最近客户消息时间为准，`manualExpireAt = lastCustomerMessageAt + manualTimeoutMinutes`。
- 到期无新客户消息，系统只发一条提示：“这次我先继续帮您看着，有问题您直接发我就行。”
- route 恢复 `AI_SERVING`。
- 超时后人工迟到回复仍写 Message 和同步日志，但默认不自动转发给客户，后台标记为迟到回复，可由人工确认补发。

### 8.4 主动结束

- 网页端关闭会话：`Conversation.status=CLOSED` 且 `routeStatus=CLOSED`。
- 门店员工或总部客服确认问题已处理后，可在网页端结束人工接待并恢复 AI；系统同时触发候选问答抽取。
- 关闭不是永久封存；同一客户再次发来消息时复用长期会话并开启新 session。

## 9. 总部会话工作台

当前实现为三栏工作台：

1. 左栏员工号列表：显示全部账号和各门店员工号，点击后按 `wxWorkInstanceId` 过滤会话。
2. 中栏客户会话列表：展示未读、门店、员工号、转人工感叹号、人工接待状态。
3. 右侧聊天区：展示文本、图片、语音、视频、文件消息；支持文本回复、图片上传、文件发送。
4. 右侧信息/设置区：展示智能回复设置、当前路由、员工号、门店、AI 托管状态、人工超时、客户资料和标签。
5. 转人工弹窗：右下角显示“新的转人工请求”，点击自动跳到对应员工号和客户会话。

### 9.1 页面信息架构

工作台的核心不是“聊天窗口”，而是总部同时管理很多门店员工号的调度台。因此左侧栏不再只放简单导航，而是做成“账号运营栏”：

- 顶部显示当前账号范围的统计，例如当前会话数、待人工数。
- 支持搜索员工号、门店名、员工姓名、实例 guid。
- 每个员工号卡片展示在线状态、门店、AI 托管开关、总部兜底状态和待处理数量。
- 选择某个员工号后，中栏只展示该员工号下的客户会话，避免一百多家门店混在一起。
- 总部客服需要全局巡检时，可保留“全部账号”入口；门店员工登录时则默认只看到自己绑定的账号和会话。

中栏承担“客户队列”的职责，重点是快速判断优先级：

- 转人工、异常配置、发送失败的会话必须有明显感叹号或红点。
- 会话项展示客户昵称、最近一条消息、所属门店、最近时间、未读数。
- 筛选条件优先包括：全部、待人工、未读、AI 服务中、人工接待中、发送失败。

右侧主区承担“处理问题”的职责：

- 聊天记录完整展示文本、图片、语音、视频、文件和系统状态。
- AI 接待中时仍显示完整输入区和底部工具条，不再用大段遮罩提示挡住输入区；`AI回复` 开关处于开启状态，编辑区和发送按钮不可用，避免 AI 和客服同时回复。
- 客服可直接在输入区点击 `AI回复` 开关关闭当前员工号 AI 托管；关闭后输入区立即解锁，客服可直接回复。
- 关闭 `AI回复` 后客服首次发送消息时，后端会把当前会话从 `AI_SERVING` 自动切到 `HQ_AGENTDESK_SERVING`，并把会话分配给当前客服，后续消息由网页人工接待。
- 进入门店人工或总部网页客服接管后，输入区保持解锁，支持文本、图片、文件发送；语音发送本期不做。
- 左侧账号栏就是企微员工号管理入口：支持搜索账号、选择全部账号或单个员工号、查看在线/AI 状态，并通过“新增账号/管理账号”打开完整员工号实例管理面板。独立“企微员工号”导航入口不再作为日常入口，避免客服在会话页和账号页之间来回跳。
- 账号管理面板复用员工号实例 CRUD：新增、编辑、删除、设置回调、复制回调地址、绑定门店、绑定门店知识库、AI 托管开关、人工超时、服务时间等都在会话页内完成。
- 底部工具条按客服工作台设计：表情、图片、消息素材、群邀请、AI 回复开关和发送按钮。
- 表情：打开常用表情/短语面板，插入当前输入框，按文本消息发送。
- 图片：客服选择图片后创建 `messageType=image` 消息，进入协议 outbox；如果缺少协议上传凭证，outbox 明确失败并提示“缺少微信协议 SAAS 上传凭证”，不能当作 HTML 文本发送。
- 消息素材：读取后台快捷回复/素材列表，点击后插入输入框。
- 群邀请：协议文档支持 `POST /room/invite_room_member`，body 为 `{guid,room_id,user_list}`；只能用于群聊会话，必须能拿到当前群 `room_id` 和待邀请成员 ID 列表。私聊会话点击时明确提示“当前不是群聊会话”，不做假功能。
- `AI回复` 开关直接更新当前会话所属员工号实例的 `aiReplyEnabled`，不是只改前端展示。开启时 AI 接管，网页人工输入禁用；关闭后网页客服可直接输入并发送，后端会把会话切到网页人工接待状态。
- 发送按钮：文本/图片/文件都必须走后端 `send_message`，由 AgentDesk 落库后再通过原员工号协议 outbox 发回客户。
- 客服点击“接管”后，AI 停答；客服回复通过原员工号发回客户。
- 智能设置抽屉显示当前员工号的 AI 托管配置、门店知识库、人工超时、服务时间、总部兜底策略。

这样的布局能让总部客服先选账号，再选客户，再处理消息，符合“一百多家门店、多员工号、多客户会话”的实际操作顺序。

## 10. 企业微信与总部网页端消息同步

所有消息必须先落 AgentDesk，再由 AgentDesk 同步到另一端。

```mermaid
flowchart LR
  A[客户 -> 门店企微员工号] --> B[协议回调 AgentDesk]
  B --> C[(Message 表)]
  C --> D{当前路由}
  D -- AI_SERVING --> E[AI 回复]
  E --> F[Outbox -> 原门店企微]
  D -- STORE_WECOM_MANUAL --> G[门店员工企微手动回复]
  G --> B
  D -- HQ_AGENTDESK_SERVING --> H[总部网页客服回复]
  H --> F
```

总部网页端不直接持有企微发送凭证，所有回复都写入 AgentDesk 消息表，再由原门店员工号 outbox 发回客户。这样能保证上下文完整、会话可审计、企微客户端和总部工作台都能同步。

## 11. 高频问题与知识进化

知识进化不直接写正式知识库，先进入“待归档问答”。来源包括：

- AI 未命中知识库后转人工。
- 总部网页客服解决的问题。
- 门店企微人工解决的问题。
- 多次出现相似问法。

候选字段：`storeId/knowledgeBaseId/conversationId/messageIds/source/question/answer/summary/evidenceText/frequency/similarityKey/status/confidence/reviewUserId/exportedAt/importedAt`。

后台页面支持筛选、查看原会话、编辑问答、合并相似问题、通过、驳回、导出、标记已导入。每周按门店导出：

- `knowledge-candidates/{storeCode}/YYYY-WW.md`
- `knowledge-candidates/{storeCode}/YYYY-WW.jsonl`

人工审核后再升级该门店知识库，避免错误回复污染知识库。

## 12. 当前代码实现对应

| 能力 | 当前实现 |
| --- | --- |
| 微信协议回调 | `internal/services/wxwork_protocol_service.go` 按 1010/1011/11010/11011 解析 `ChatMsgModel` |
| 长期会话绑定 | `guid + customer username/chatroom` 映射到 `WxWorkKFConversation` |
| Echo 去重 | `wx_protocol:{guid}:{msg_id}`，自己账号消息仅确认，不触发 AI |
| Session 隔离 | `ConversationRouteState.sessionNo` + `Message.sessionNo`，AI history 只读当前 session |
| 关闭重开 | 关闭写 `routeStatus=CLOSED`，客户再来新消息时恢复 AI 并新开 session |
| 门店人工优先 | AI 转人工进入 `STORE_WECOM_MANUAL`，网页端弹窗和感叹号提示 |
| 总部网页接管 | 现有分配/接管逻辑进入 `HQ_AGENTDESK_SERVING`，网页回复走原 outbox |
| 多媒体展示 | image/voice/video/attachment 入库和前端展示，文本才触发 AI |
| 员工号设置 | 实例表包含 notifyUrl/proxy/bridgeId/aiAgentId/staffUserIds/serviceHours/fallbackToHQ/manualTimeoutMinutes/aiReplyEnabled/autoAcceptFriendRequest/contextMaxMessages/contextMaxTokens/contextCompressionEnabled |
| 三栏工作台 | 会话页新增员工号列表、会话列表、聊天区/设置区和转人工弹窗 |

## 13. 2026-06-27 实施修订

### 13.1 产品入口收敛

系统新业务主链路只保留 `wxwork_protocol`。企业微信 CLI 和企业微信客服号执行产品级移除：

- 新建渠道表单不再提供 CLI 和企业微信客服号选项。
- 会话工作台、员工号账号管理、消息 outbox 新链路只使用 `wxwork_protocol`。
- 历史 CLI/KF 模型、表和兼容代码暂不物理删除，避免旧数据或迁移启动失败。
- 后续开发禁止把 CLI/KF 重新接入新 UI、新配置和新消息主链路。

客服分配体系必须保留。`已分配客服 / 客服组 / 转接 / 接管 / 待接入队列` 是人工协作体系，和 `aiReplyEnabled` 是两条状态线：

- `aiReplyEnabled=true` 时，AI 托管当前员工号实例，网页输入区可见但禁用。
- `aiReplyEnabled=false` 时，网页客服可以直接回复；首次发送会把当前会话从 AI 接待切到网页人工接待，并按现有分配权限校验。
- 已分配给其他客服的会话仍必须走接管/转接流程，不能因为关闭 AI 就绕过权限。

### 13.2 账号新增和真实协议动作

会话页左侧的“新增账号/账号设置”合并原企微员工号页面能力。账号动作全部通过后端 service 调用 `wework.apifox.cn` 文档里的接口：

| UI 动作 | 协议接口 | 关键 body |
| --- | --- | --- |
| 获取登录二维码 | `/login/get_login_qrcode` | `{guid,verify_login:false}` |
| 检查二维码 | `/login/check_login_qrcode` | `{guid}` |
| 登录验证码 | `/login/verify_login_qrcode` | `{guid,code}` |
| 同步账号资料 | `/user/get_profile` | `{guid}` |
| 获取企业信息 | `/user/get_corp_info` | `{guid}` |
| 设置回调 | `/client/set_notify_url` | `{guid,notify_url}` |
| 设置代理 | `/client/set_proxy` | `{guid,proxy}`，默认空，不自动使用本机 7892 |
| 恢复实例 | `/client/restore_client` | `{guid,proxy:'',bridge:'',sync_history_msg:true,force_online:false,auto_start:true}` |
| 停止实例 | `/client/stop_client` | `{guid}` |
| 退出登录 | `/user/logout` | `{guid}` |
| 同步好友申请 | `/contact/sync_apply_list` | `{guid,seq:'',limit:50}` |
| 同意联系人申请 | `/contact/agree_contact` | `{guid,user_id,corp_id}` |

自动通过好友申请由 `autoAcceptFriendRequest` 控制。关闭时只展示/审计申请，不自动同意；开启时才调用 `/contact/agree_contact`。`autoAcceptFriendRemarkTemplate` 先作为业务备注策略保存，具体备注/标签动作必须等协议文档提供对应字段后再实现，不能自行猜字段。

### 13.3 多媒体和富媒体边界

入站消息必须全部写 `MessageSyncLog`。文本直接入库并可触发 AI；图片、语音、视频、文件、位置等先入库展示，不在未完成理解前让 AI 编造。后续图片 OCR/视觉理解、语音转文字、文件解析完成后，可以把可信 `mediaText/mediaSummary` 写入 payload，再按文本问题触发 AI。

出站按协议分派：

- 文本：`/msg/send_text`。
- 图片：`/msg/send_image`，必须有 `file_id/size/image_width/image_height/aes_key/md5/is_hd`。
- 语音：`/msg/send_voice`，必须有 `file_id/size/voice_time/aes_key/md5`。
- 文件：`/msg/send_file`，必须有 `file_id/size/file_name/aes_key/md5`。
- 视频：`/msg/send_video`，必须有 `file_id/size/file_name/aes_key/md5/video_duration/video_width/video_height`。
- GIF：`/msg/send_gif`，必须有 `file_id/size/aes_key/md5/url/image_width/image_height`。
- 位置、名片、链接、小程序、视频号、直播、引用、合并转发、微信小店商品按 payload JSON 透传文档字段，并由后端补 `guid/conversation_id`。

如果 payload 缺协议侧必要字段，outbox 必须标记 failed 并记录真实错误，不能把消息误标 sent，也不能降级成普通文本冒充发送成功。

### 13.4 上下文压缩策略

原始企微消息、媒体、人工回复永久保存，不删除、不覆盖。AI 只使用“当前 session 最近消息 + 稳定事实摘要”：

- 默认 `contextMaxMessages=30`。
- 默认 `contextMaxTokens=8000`。
- 默认 `contextCompressionEnabled=true`。
- 超过阈值后生成摘要，摘要只保留稳定事实、未解决事项、用户偏好、当前门店信息。
- 已关闭/已解决的旧故障不带入新 session，避免旧维修问题污染新问题。

### 13.5 GitHub 绑定原则

当前目录若还不是 Git 仓库，绑定 `520skyincloud/agentdesk` 时必须先 `git init`、添加远端、`git fetch origin` 查看远端默认分支。远端非空时新建本地工作分支并合并远端历史，必要时使用 `--allow-unrelated-histories` 手动解决冲突。默认推送新分支并开 PR，不强推覆盖默认分支。

## 14. 测试清单

1. A 门店员工号客户发“早餐几点”，只调用 A 门店知识库。
2. B 门店同问题调用 B 门店知识库，答案可以不同。
3. 未绑定知识库的员工号不调用 AI，只回复配置异常并告警。
4. 同一客户第二次发消息复用同一长期会话。
5. 同一客户联系不同 `guid` 创建独立长期会话。
6. 自己发出的 echo 不创建重复消息、不触发 AI。
7. 连续发送“转人工”只出现一条客户提示。
8. 营业时间进入门店人工状态，总部网页端出现弹窗和感叹号。
9. 总部接管后 AI 停答，总部回复回到原门店员工号会话。
10. 10 分钟无客户新消息后恢复 AI。
11. 已关闭会话再次来消息，新开 session，旧维修问题不污染新问题。
12. 客户发图片/语音/视频/文件，后台展示并审计；未转写语音不触发 AI。
13. 客服网页端发送图片/文件，协议 body 符合文档；业务错误码不误标 sent。
14. 知识候选按门店导出 Markdown/JSONL，人工审核后再导入门店知识库。
