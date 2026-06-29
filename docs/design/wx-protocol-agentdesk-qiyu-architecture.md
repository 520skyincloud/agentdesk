# 企微员工号 + 总部网页客服 + 门店知识库技术架构设计

版本：2026-06-27  
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
| 语音 | 展示“语音消息 + 时长 + 转文字状态/结果”卡片；转写成功后可作为文本问题 | 协议翻译成功才可触发 | 入站语音不默认展示浏览器播放器，除非已确认资产是浏览器可播放文件 |
| 文件 | 保存资产，展示文件卡片 | 否 | 需要按 wework 云存储上传得到 `file_id/aes_key/size/md5/file_name` 后 `/msg/send_file` |
| 视频 | 保存资产，展示播放器或文件卡片 | 否 | 按文件能力发送 |
| 表情包/GIF | `msg_type=10` 或 `content_type=104/source_type=101` 识别为 `gif`，展示动图/表情卡片 | 是，轻互动短回复 | `/msg/send_gif` 或文档指定的 GIF URL 发送接口 |

当前实现先完成媒体入站审计、资产注册和网页展示；语音转文字主链路已经接入协议 `/msg/apply_voice_id` + `/msg/query_voice_text`，ASR 仅作为协议翻译失败后的兜底。没有协议翻译或 ASR 转写结果时，不让 AI 猜测图片/语音内容。
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

AI 提示词要求：像酒店前台同事在微信里回消息，默认 1 句、最多 2 句，普通回复尽量 8 到 22 个字；少用“您”，优先说“你”；禁止“亲”“为您”“这边”“感谢理解”“请稍等片刻”“祝您生活愉快”等模板话术；不说“根据知识库”、不自称 AI。维修、漏水、投诉、安全场景先接住问题，再收集必要信息或转人工。

2026-06-28 起新增一条硬规则：AI 回复前必须先做意图分类，不能把所有问题都当 FAQ 聊天处理。至少分为 `FAQ_CHAT`、`INFO_CLARIFICATION`、`SERVICE_TASK`、`HUMAN_DECISION`、`LOCATION_NAVIGATION`、`MEDIA_UNDERSTANDING` 六类。凡是送水、送拖鞋、加被子、维修、打扫、叫醒、行李协助等需要真实员工动作的 `SERVICE_TASK`，AI 不能空口说“已经安排/马上送/已通知”；只有工单或转人工工具真实成功后才能这样说。缺房号时先追问房号，缺物品/数量/位置/时间时继续追问最关键字段。退款、赔偿、严重投诉、安全风险、订单异常、隐私授权、价格争议等 `HUMAN_DECISION` 一律转人工或人工接管，AI 不自行下结论。

当前代码已把这些规则写进 Agent 基础 instruction，并扩展 `prepare_ticket_draft` 工具入参：`taskCategory/roomNumber/serviceItem/quantity/preferredTime/urgency`。工具检测到送物、维修、保洁类任务缺房号时返回 `ready=false` 和追问建议，阻止 AI 创建不完整工单或口头承诺。工具成功前禁止说“马上安排”“已经让同事过去”“送过去”“通知维修”“登记叫醒”“安排师傅”“我帮你登记”；有房号但工具未成功时，只能说“收到，这个我让同事接一下。”或追问一个缺失字段。

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
2. 中栏客户会话列表：默认展示“全部未关闭会话”，展示未读、门店、员工号、转人工感叹号、人工接待状态。主界面不再拆成“处理中/待接入/AI 接待中/已关闭”四个分类 tab，避免客服在日常接待时被状态分类打断。
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
- 主列表默认是 `all_open`：同一员工号下所有未关闭会话。待人工、AI 服务中、人工接待中、发送失败等作为会话标签和小红点展示；后续如需高级筛选，应放到搜索/更多筛选里，不作为顶部强制分类 tab。

右侧主区承担“处理问题”的职责：

- 聊天记录完整展示文本、图片、语音、视频、文件和系统状态。
- 富媒体必须真实渲染：图片直接预览，文件显示下载卡片，语音显示“时长 + 转文字状态/结果”卡片，视频显示播放器，位置显示定位卡片并可打开地图，链接/视频号/小程序/名片/合并转发/微信小店显示结构化卡片。若 payload 有 `assetId` 但没有签名 URL，前端使用 `/api/asset/file/{assetId}` 读取资产，不能只显示“图片”“文件”几个字。语音在未确认浏览器可播放前，不展示 `<audio>` 控件，避免客服以为能直接听。
- 网页端消息渲染统一收敛在 `web/lib/im-message.ts`：`image` 渲染图片预览，`voice` 渲染语音状态/转文字卡片，`attachment` 渲染文件下载卡片，`video` 渲染视频播放器，`gif` 渲染动图，`location` 渲染定位卡片并链接地图，`link/mini_program/contact_card/feed/feed_live/quote/merged_forward/shop_product` 渲染结构化卡片。新增协议类型必须先补 `messageType -> render` 和 `messageType -> summarize` 两处，不能落到普通文本或错误媒体卡片。
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
- `AI回复` 开关直接更新当前会话所属员工号实例的 `aiReplyEnabled`，不是只改前端展示。开启时 AI 接管，网页人工输入禁用；关闭后网页客服可直接输入并发送，后端会把会话切到网页人工接待状态。开关切换不能改变当前列表筛选、不能跳转到“处理中”等其他分类，必须在当前会话页面内完成。
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

知识进化不直接写正式知识库，先进入“待归档问答”。它不是把所有人工会话都粗暴入库，而是先由系统和 AI 分析“这是不是一个值得沉淀成 FAQ 的语言答案”。正确来源包括：

- AI 未命中知识库后转人工，人工用语言回答了一个可复用问题。
- 总部网页客服解决了一个知识库没有覆盖的咨询类问题。
- 门店企微人工手动回答了一个可复用、非行动派单类问题。
- 多个客户多次出现相似问法，且人工答案稳定一致。

不进入知识进化的内容：送水、拖鞋、维修、打扫、行李、叫醒等行动任务；退款、赔偿、严重投诉、安全风险、订单异常、隐私授权等人工决策；客户闲聊、致谢、无效确认；媒体未理解时的兜底话术；人工只是说“稍等/已安排/同事过去”的过程话术。

抽取流程必须分三步：

1. 先取当前会话轮次内最近消息和人工接管后的消息，保留原文证据。
2. AI/规则共同判断候选资格：客户是否提出知识型问题、AI 是否未答好、人工是否给出语言型答案、该答案是否可复用到同门店其他客户。这里的“语言型答案”是指可写成 FAQ 的规则、流程、位置、费用、时间、材料说明，不是“我已安排人过去”这种行动结果。
3. 只有通过资格判断后，才生成 `question/answer/summary/evidenceText/confidence`，进入待审核；审核通过后再周导出，不自动写正式知识库。

当前确定性兜底规则已经落代码：只从文本/HTML 客户问题 + 人工语言答案抽取；自动过滤行动派单、维修执行、赔偿投诉、安全决策、低价值闲聊和“无法查看/不清楚”兜底话术。后续可在这个基础上再接 LLM 分析器，让模型按同一套准入标准判断“是否值得入库”、改写问答、给置信度和冲突提示。

候选字段：`storeId/knowledgeBaseId/conversationId/messageIds/source/question/answer/summary/evidenceText/frequency/similarityKey/status/confidence/reviewUserId/exportedAt/importedAt`。

后台页面支持筛选、查看原会话、编辑问答、合并相似问题、通过、驳回、导出、标记已导入。每周按门店导出：

- `knowledge-candidates/{storeCode}/YYYY-WW.md`
- `knowledge-candidates/{storeCode}/YYYY-WW.jsonl`

人工审核后再升级该门店知识库，避免错误回复污染知识库。

知识进化至少覆盖以下 12 种情况：

| 情况 | 是否入候选 | 处理方式 |
| --- | --- | --- |
| AI 不会答早餐政策，人工回答了具体时间和规则 | 是 | 改写成标准 FAQ |
| AI 不会答停车收费，人工回答收费和入口 | 是 | 沉淀门店停车 FAQ |
| AI 不会答发票抬头流程，人工给出材料要求 | 是 | 生成流程型问答 |
| 客户问周边便利店，人工给出稳定地点 | 是 | 作为门店周边 FAQ，标证据 |
| 客户问可否延迟退房，人工说明本店规则 | 是 | 若规则稳定，入候选 |
| 客户问会员权益，人工给出品牌统一政策 | 是 | 若该门店适用，入候选 |
| 客户要送水/拖鞋，人工说已安排 | 否 | 属行动任务，只留工单/服务记录 |
| 客户报修马桶/空调，人工派维修 | 否 | 属执行事件，不沉淀为 FAQ；可另做故障统计 |
| 客户投诉赔偿，人工协商结果 | 否 | 属个案决策，禁止污染知识库 |
| 客户发图片问“这是啥”，人工识别客人物品 | 视情况 | 若是通用设施说明可入；客人物品识别不入 |
| 人工只说“稍等/已通知/同事过去” | 否 | 过程话术不是知识答案 |
| 人工回答和知识库已有答案冲突 | 进入待审核但低置信 | 需人工确认是否更新旧知识 |
| 多个相似问法命中同一候选 | 是 | frequency 增加，不重复新增 |

开发和审核时必须额外注意这些边界：

- 人工回答“早餐 7:00-10:00，在一楼餐厅”是知识答案；人工回答“我让同事给您送早餐券”是行动处理，不入 FAQ。
- 人工回答“停车场入口在酒店东侧，住客扫码免费 24 小时”是知识答案；人工回答“我帮您抬杆”是行动处理。
- 人工回答“发票需要提供抬头、税号、邮箱，退房后 1 个工作日开具”是知识答案；人工回答“我现在帮您开好了”不入 FAQ。
- 人工回答“延迟退房到 14:00 免费，之后按半日房费”若为门店稳定规则可入库；单次给客户特殊豁免不入库。
- 人工回答“这张图是客房内的空气净化器面板，蓝灯表示运行中”若是门店设施说明可入库；识别客户私人物品不入库。
- 人工回答“语音里您说想续住，续住价格要看当天房态”属于可能需要实时价格，默认转人工/低置信候选。
- 人工处理“马桶堵了、空调不制冷、漏水”优先进入工单/故障统计，不变成 FAQ；只有人工给出通用使用说明时才可入候选。
- 投诉、赔偿、退款、差评挽回、报警、安全隐患、隐私授权、订单争议不自动入库，必须人工单独审核。
- AI 已正确回答、人工只是重复确认时不新增候选，只可增加该问法命中统计。
- 人工答案和现有知识库冲突时不直接覆盖，候选状态保持 pending 并标记低置信和冲突证据。

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

会话页左侧的“新增账号/账号设置”合并原企微员工号页面能力。新增账号必须是扫码优先流程，但 **`guid` 不能由 AgentDesk 本地生成**。

`wework.apifox.cn` 文档统一请求格式为 `{app_key, app_secret, path, data}`，其中 `data.guid` 是“实例列表里的设备 ID”。因此 AgentDesk 的职责是保存绑定关系和业务配置，不能伪造 `pending guid` 或本地 UUID 冒充协议实例。

真实新增流程：

1. 协议平台先完成设备初始化，设备进入协议平台实例列表。
2. 企微员工号渠道配置 `devicePoolUrl`，用于调用上游协议平台的实例列表/设备池接口，返回真实 `guid` 和可用状态。
3. 客服在会话页左侧点击“自动绑定空闲实例”或“生成远程开户链接”。
4. AgentDesk 后端调用 `devicePoolUrl`，过滤本地已绑定 `guid`，选择一个空闲设备 ID；若未配置设备池接口或没有空闲设备，直接报错，不创建占位账号。
5. 后端创建 `WxWorkProtocolInstance` 本地绑定记录，默认 `aiReplyEnabled=true`、`healthStatus=login_qrcode`，代理字段为空。
6. 后端立即按 Apifox 业务接口调用 `/login/get_login_qrcode`，body 的业务参数为 `{guid, verify_login:false}`，把真实二维码返回给前端。
7. 前端按 3 秒间隔调用 `/login/check_login_qrcode` 轮询扫码结果。
8. 登录成功后调用 `/user/get_profile` 同步员工号资料。
9. 运营再进入“账号设置/智能客服配置”绑定门店、知识库、客服组、AI 托管和自动通过好友申请策略。

远程开户注册同样先占用一个真实空闲 `guid`，再生成 `remoteSetupToken`。门店打开 `/wxwork-remote-setup?token=...` 后只负责扫码和补充门店资料，不需要也不允许手填截图里的实例 ID。若以后协议平台提供官方“创建/初始化设备”接口，可以把第 1 步也纳入自动化；在接口未明确前，系统只绑定已存在且可用的设备。

账号动作全部通过后端 service 调用 `wework.apifox.cn` 文档里的接口：

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
| 同步好友申请 | `/contact/sync_apply_contact` | `{guid,seq:'',limit:50}` |
| 同意联系人申请 | `/contact/agree_contact` | `{guid,user_id,corp_id}` |

自动通过好友申请由 `autoAcceptFriendRequest` 控制。关闭时只展示/审计申请，不自动同意；开启时才调用 `/contact/agree_contact`。`autoAcceptFriendRemarkTemplate` 先作为业务备注策略保存，具体备注/标签动作必须等协议文档提供对应字段后再实现，不能自行猜字段。

### 13.2.1 存储设置和 OSS/WECDN 配置

系统新增“存储设置”页面，作为运行时文件存储和企微富媒体公网链路的唯一配置入口。配置保存到 `t_system_config.config_key = storage.asset`，不写入代码仓库。当前测试环境默认参数为：

| 配置项 | 当前测试值 | 说明 |
| --- | --- | --- |
| 默认存储类型 | `oss` | 富媒体上传默认进 OSS |
| OSS Endpoint | `oss-cn-beijing.aliyuncs.com` | 阿里云华北 2 北京 |
| OSS Bucket | `skychucun` | 测试桶 |
| OSS 目录前缀 | `desk` | 所有 AgentDesk 文件写入该目录 |
| OSS Base URL | `https://skychucun.oss-cn-beijing.aliyuncs.com` | 公网读取地址；如果后续使用 CNAME，可改为 CNAME 域名 |
| AgentDesk 公网地址 | `http://112.124.109.106:2332` | 协议云存储从这里拉取本地资产，例如 `/api/asset/file/{assetId}` |
| wecdn_web 地址 | `http://112.124.109.106:34789` | 调 `/cloud/c2c_upload` 前必须配置，否则富媒体 outbox 失败 |

AccessKey ID 和 AccessKey Secret 只能保存在运行时配置中，文档和 Git 仓库不得记录明文。后台返回设置时只返回 `ossAccessKeySecretSet=true/false`，不回显 Secret。更新时 Secret 留空或填 `********` 表示沿用原值。

资产读写规则：

- 网页端上传文件时，`AssetService` 使用运行时存储设置，不再只读静态 `config.yaml`。
- OSS 存储 key 会自动拼接全局目录前缀，例如 `desk/conversation/...`。
- `/api/asset/file/{assetId}` 用于让 wecdn_web 或外部协议服务拉取 AgentDesk 资产；本地资产走流式输出，外部 URL 资产走重定向。
- 协议发送时优先读取“存储设置”的全局 `wecdnBaseUrl/publicAssetBaseUrl`；只有全局为空时才兜底使用协议渠道 JSON 里的历史值。这样可以避免旧渠道残留地址覆盖后台新配置。

WECDN 部署规则：

- 当前测试包来自 `/Users/openclaw/Downloads/wecdn_dist_v2.8.3.zip`，服务器解压运行目录为 `/home/wecdn_dist_v2.8.3`。
- `wecdn_service` 监听 `127.0.0.1:50056`，`wecdn_web` 监听公网 `112.124.109.106:34789`。
- `wecdn_service_config.ini` 中 `cloud_storage=aliyun`，OSS endpoint/bucket/access key 只写入服务器运行配置，不写入 Git。
- `wecdn_web` swagger 可用于连通性检查：`http://112.124.109.106:34789/swagger/index.html`。

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

网页端发送图片、语音、文件、视频、GIF 等本地资产时，完整链路为：

```mermaid
flowchart TD
  A[网页客服选择文件] --> B[AgentDesk 创建 Asset]
  B --> C[上传到 OSS: desk/...]
  C --> D[生成公网资产 URL /api/asset/file/{assetId}]
  D --> E[调用 wecdn_web /cloud/c2c_upload]
  E --> F[获得 file_id/aes_key/md5/size 等协议媒体字段]
  F --> G[调用 /msg/send_image 或 send_file 等真实发送接口]
  G --> H[平台 echo 回调只确认发送状态 不创建重复消息]
```

真实发送测试前置条件：员工号必须扫码登录成功，当前会话必须有可发送目标，OSS AccessKey 已在“存储设置”中保存，`publicAssetBaseUrl` 必须能被协议服务公网访问，`wecdnBaseUrl` 必须指向可用的私有化云存储服务。缺任一条件时，不允许 mock 成功，只能让 outbox 标记 failed 并写明原因。

2026-06-27 真实测试结论：

- 客户发图片已真实入站，`Message(id=193)` 保存为 image 消息，payload 带协议侧 `file_id/aes_key/md5/size/image_width/image_height`，本地 asset 可展示。
- 网页端/AI 出站图片已真实跑通，`Message(id=199/200)` 经 OSS `desk/...`、公网 `/api/asset/file/{assetId}`、WECDN `/cloud/c2c_upload` 后取得 `file_id/aes_key/md5`，最终 `/msg/send_image` outbox 为 `sent`。
- 网页端/AI 出站文件已真实跑通，`Message(id=201)` 经同一链路换取 `file_id/aes_key/md5/file_size` 后调用 `/msg/send_file`，outbox 为 `sent`。
- 曾出现 `broken data stream when reading image file` 的失败样本来自 70 字节损坏 PNG 测试素材；正常 PNG 可发送。后续测试必须使用能被图片解码器校验通过的真实文件。
- 2026-06-27 第二轮补测后，普通富媒体 outbox 白名单已扩展到 `voice/gif/location/link/feed_live/merged_forward/quote/shop_product` 等类型。此前这些消息能入库但不进 outbox，是“看起来有按钮但实际不发送”的根因。
- 已真实发送成功：位置 `Message(id=208)`、链接 `id=209`、视频号直播 `id=210`、合并转发 `id=211`、语音 `id=212`、GIF `id=213`、普通视频 `id=214`，对应 outbox `97-103` 均为 `sent`。
- 操作型接口已真实成功：`/msg/report_unread` 标记会话已读、`/msg/apply_voice_id` 获取语音翻译 ID、`/msg/confirm_msg` 确认企微内部消息已读、`/msg/send_quote_msg` 发送引用消息。
- 操作型接口真实失败但原因明确：`/msg/send_room_at` 需要真实群聊 `R:` 会话；`/msg/query_voice_text` 需要真实语音消息的 `msgid + voiceid` 组合；`/msg/revoke_msg` 需要可撤回窗口内且匹配的协议 `msgid`；`/msg/send_finder_product` 需要真实微信小店 `shop_id/product` 数据，假数据会返回缺 `shop_id`。
- 大视频不是普通 `/cloud/c2c_upload`，协议文档要求先走 `/cloud/cdn_big_upload` 上传视频，再走 `/cloud/upload_video_preview` 上传预览图，最后调用 `/msg/send_big_video`。这条 big cdn 闭环尚未实现，不能宣称支持大视频生产发送。

富媒体理解边界：

- 图片：保存 asset 后调用 vision 模型写入 `mediaText/mediaSummary`，理解成功才进入 AI 上下文。视觉模型只负责描述图片中能确定的对象和文字，不输出“建议人工确认”“需人工处理”等客服处理建议，避免后续 AI 被摘要带偏成固定兜底话术。视觉接口偶发失败时保留失败原因，不编造；AI 回复触发只短暂等待最近媒体理解完成，等待窗口约 4 秒，避免客服场景里因为图片理解拖慢整体回复。
- 语音：主链路不是先下载音频再 ASR，而是严格按企微协议 SAAS 文档先调用 `/msg/apply_voice_id` 获取 `voiceid`，再调用 `/msg/query_voice_text` 获取协议侧语音翻译文本；翻译成功后写入 `mediaText/mediaSummary` 并触发 AI。只有协议翻译失败、且本地/WECDN 能拿到真实音频文件时，才进入独立 ASR 模型兜底。AI 不能在没有协议翻译或 ASR 结果时猜测语音内容。
- 文件：文本类文件可抽取内容；PDF/Word/Excel 等未接解析器前只展示和转人工/提示，不让 AI 假装读过。
- 视频/GIF：先展示和审计，不作为已理解事实，除非后续接入视频理解。
- 位置：展示定位卡片；客户要求“发定位”时走门店坐标配置和 `/msg/send_location`，不是由模型自由生成坐标。

- 当前已完成的是“真实收发、资产保存、后台展示、协议字段回填、outbox 状态确认”，并新增 `MediaUnderstandingService` 作为媒体理解入口。
- 图片理解链路已经接入模型配置类型 `vision`：读取 asset 或下载 URL -> 调 OpenAI-compatible `/chat/completions` 多模态接口 -> 生成 `mediaText/mediaSummary` -> 写回 message payload -> 再允许 AI 基于图片内容回答。
- 2026-06-27 第一轮代码验证：`Message(id=215)` 通过真实 `/api/third/wxwork-protocol/callback` 重放入站图片后进入 `MediaUnderstandingService`，但协议原始 `file_id` 是微信临时下载地址，直接 HTTP GET 返回 400。该问题不是模型问题，而是缺少协议下载闭环。
- 2026-06-27 第二轮修复：按 `wework.apifox.cn` 文档接入 `/cloud/wx_download` 和 `/cloud/c2c_download` 两类私有化云存储下载。若入站 `file_id` 是 `http/https` 微信临时 URL，优先走 `/cloud/wx_download`；若是普通协议 `file_id`，走 `/cloud/c2c_download`。下载成功后上传到本系统 OSS，再由 `MediaUnderstandingService` 读取 OSS asset 调视觉模型。
- 2026-06-27 真实闭环结果：`Message(id=233)` 通过真实回调入站图片，asset 已保存到 OSS `desk/wx_protocol/inbound/...jpg`，`mediaUnderstandingStatus=understood`，视觉模型写回 `mediaText=图片显示Steam登录确认页面...`，随后触发 AI 回复 `Message(id=234)`。因此入站图片“接收 -> 私有化下载 -> OSS 保存 -> 视觉理解 -> 写回 payload -> 触发 AI”已经跑通。
- 视觉/多模态配置：后台 `AI 配置` 已新增模型类型 `vision/asr/tts`。2026-06-28 已按测试要求把启用视觉模型切到 SiliconFlow OpenAI-compatible `https://api.siliconflow.cn/v1`，模型名 `gpt-5.5`；密钥只保存在运行数据库，不写入代码和文档。
- 语音理解优先使用协议 `/msg/apply_voice_id` 和 `/msg/query_voice_text`；如果协议转写失败，再走私有化云存储下载 + 独立 ASR 模型。后台已新增 `asr` 类型，测试模型为 `TeleAI/TeleSpeechASR`，Base URL 为 `https://api.siliconflow.cn/v1`。新入站媒体 payload 会保存 `wxMedia.msg_id/conversation_id/file_id/aes_key/auth_key`，旧消息可从 `t_wx_work_kf_message_ref.raw_payload` 反查 `msgid`。未配置 API Key 或没有转写结果时，AI 只能知道“收到语音”，不能猜语音内容。实现时必须过滤 `fmt.Sprint(nil)` 产生的 `"<nil>"` 字符串，不能把 `"<nil>"` 当作 `msgid/conversation_id` 传给协议接口，否则会触发 `-2003 head rsp code error`。
- 2026-06-28 真实闭环结果：客户语音 `Message(id=271)` 的原始协议回调中 `msgid=1001449`，应用从 `t_wx_work_kf_message_ref.raw_payload` 反查 `msgid` 和 `S:externalUserId`，调用 `/msg/apply_voice_id` 得到 `voiceid=178262045610216689923745887247881302995969629`，再调用 `/msg/query_voice_text` 得到文本“你现在听得清楚我说话吗？”，最终写回 `mediaUnderstandingStatus=understood`、`mediaText=你现在听得清楚我说话吗？`。该验证确认：客户语音优先走协议翻译结果，而不是 ASR。
- 语音延迟需要分两段记录：第一段是协议平台消息时间 `sendtime` 到 AgentDesk 入库 `created_at` 的回调到达延迟；第二段是 AgentDesk 收到后调用协议翻译、写入 `mediaText`、触发 AI 的处理延迟。`Message(id=271)` 样本中平台 `sendtime` 到入库约 19 秒，这部分发生在协议平台回调到系统之前；系统侧可通过日志监控和告警，但不能用 ASR 优化这段外部延迟。
- TTS 已作为独立 `tts` 类型进入模型配置，测试模型为 `fnlp/MOSS-TTSD-v0.5`。当前用于配置管理和后续语音生成文件，出站客服语音发送仍以已有音频文件上传后 `/msg/send_voice` 为准，不自动把文本转语音。
- 文件理解当前支持 `txt/md/csv/json` 等文本类文件抽取内容；PDF/Word/Excel 需要继续接文档解析器，解析后写入 `mediaText/mediaSummary`；二进制压缩包、未知格式只展示和审计，不触发 AI 内容判断。
- 视频/GIF 默认只展示和审计；如果要 AI 理解视频，需要额外做抽帧、封面识别或视频理解模型，不应把文件名当作视频内容。
- 表情包/GIF 不是普通图片问答，不进入视觉识别。入站真实样本为 `msg_type=10, content_type=104, desc=动画表情, url=https://wework.qpic.cn/...`，系统入库为 `Message(gif)`，content 写“动画表情”，payload 保留真实 URL、宽高和协议字段。GIF 可触发 AI，但上下文只表达“客户发了动画表情”，AI 只能短句接住，例如“哈哈”“收到”“好嘞”，不要分析表情内容。
- 入站外部媒体 URL 必须支持幂等复用：同一个表情包、同一张图片或同一个文件 URL 多次回调时，`Asset` 先按 `storageKey` 查已有记录，存在则复用原 `assetId`，不能重复插入导致 `uk_storage_key` 唯一索引报错。2026-06-28 用同一真实表情包 URL 重放验证，第二次消息复用 `assetId=c52995874c22494dbc073e9fa6a8978f`，新消息正常入库为 `Message(id=283, type=gif)`。
- 入站定位必须按真实协议字段优先识别：真实回调样本为 `content_type=6, msg_type=3, longitude=117.281937, latitude=31.716152, title=丽斯未来酒店..., address=安徽省合肥市...`。这类消息必须入库为 `Message(location)`，payload 保留 `longitude/latitude/title/address/zoom/content_type/msg_type`，网页端展示定位卡片并可打开地图。不能按旧映射把 `msg_type=3` 当语音，否则会错误进入语音翻译和语音卡片。2026-06-28 已用真实定位回调重放验证，`Message(id=292)` 正常入库为 `location`，并修正历史错入库消息 `id=289`。

媒体理解和 AI 回复触发规则：

- 文本消息继续直接触发 AI。
- 图片、语音、附件先落库和展示，再异步进入 `MediaUnderstandingService`；只有当 payload 写入可信 `mediaText/mediaSummary` 后才触发 AI。
- AI 上下文构建已经读取 payload 中的 `mediaText/mediaSummary`，并把它作为“媒体理解”文本带入当前 session。
- 媒体理解失败时写 `mediaUnderstandingStatus=failed` 和 `mediaUnderstandingError`；AI 不得根据文件名或占位符编造内容。
- AI 触发前增加短窗口聚合：客户新消息落库后先等待约 1.5 秒，如果这期间有更新的客户消息，则旧触发直接取消，只让最新消息触发 AI。
- 图文/语音追问场景增加短媒体等待：如果当前文本消息之前 8 秒内有客户发来的图片、语音或附件，且媒体理解状态仍未完成，AI 最多等待约 4 秒；等待期间如果客户继续发新消息，当前触发取消，交给最新消息处理。超过等待窗口后，如果媒体还没理解完成，AI 不能编造，只能围绕最新文本追问或转人工。
- 这条规则用于解决“先发图片，马上问这是啥”“连续发两个问题”“语音后补一句说明”等场景。AI 最终只对最新问题回复一次，同时上下文里能看到前面媒体的 `mediaText/mediaSummary`。
- 如果媒体理解最终失败，AI 必须如实说明暂时没法看清/听清，并询问客户补充信息；不得把旧图片、文件名或上一次问题当作当前事实。
- AI 对客话术不能暴露“语音识别不准”“图片识别不准”“系统识别”等技术过程。语音已转文字时按客人原话自然回复；确实没听清时只说“这条语音我没听清，方便打字发我一下吗？”当前不能出站语音时，只说“现在只能文字回你，打字发我就行。”
- 轻互动场景要像真人：客户只发动画表情、表情包、哈哈、OK 时，只回一句极短自然话，不超过 6 个字，不固定 emoji，不追加“有需要随时找我”这类机器人尾巴。2026-06-28 真实回放表情包后，AI 新回复为“哈哈”，outbox `id=132` 已发送。
- 工单创建、转人工等 HITL 确认中断恢复时，不能只读取 `Message.content`，必须通过 `BuildRuntimeMessageTextWithPayload(messageType, content, payload)` 把语音/图片等媒体 payload 里的 `mediaText/mediaSummary` 作为用户回复文本。这样客户用语音说“确认确认”“嗯确认”“可以的”“行，就这样”也能识别为确认。确认判定必须先匹配否定词，例如“不确认”“不要创建”“先不建”，再匹配确认词，避免把否定句误判为同意。
- 知识库未命中时不能直接机械回复“记录一下让同事确认”。如果用户结合图片问“这个酒店有吗”，且媒体理解已经识别出对象，应先复述对象再说明需要确认，例如“你问的是图里这个桌面音箱吗？房间里不一定有，我帮你确认下。”系统不能编造“有/没有”，但也不能装作没看见图片。
- 2026-06-28 已执行 30 个酒店客服场景 × 10 轮提示词压测。覆盖早餐、停车、送水、维修、投诉、发票、图片、语音、表情包、定位、退款、安全风险、房间异味、连续上下文等场景。前几轮主要问题是“马上安排/通知维修”的空口承诺、“这边/为您”的模板味、轻互动加尾巴。最终提示词收敛为：短句、无模板尾巴、轻互动不超过 6 个字、执行任务工具成功前只收集信息或转同事、不暴露识别过程。回归 30 场景后，普通 FAQ 能直接短答，送水/叫醒/维修会追问房号或转同事，表情包回复为“收到/哈哈/好嘞”类短句。

消息速度策略：

- 出站消息创建后会立即触发 `WxWorkProtocolService.DispatchPendingOutbox(10)`，后台 `cron` 每 1 秒兜底扫描一次 pending outbox；因此系统侧不依赖 5 秒或 30 秒慢轮询。
- 入站速度分两段看：协议平台 `sendtime` 到 AgentDesk `created_at` 是外部回调到达延迟；AgentDesk 入库后到媒体理解/AI/outbox 是系统处理延迟。外部回调慢不能靠 ASR 或前端优化解决，必须用回调日志量化后找协议服务商；系统侧要避免额外等待，所以 AI 防抖约 0.7 秒，媒体等待约 4 秒。
- 图片/语音理解不允许无限等待。若理解超时或失败，当前轮回复不编造内容；后续理解完成后可写回 payload，并由最新客户消息决定是否再触发 AI。

### 13.3.1 定位消息和门店坐标策略

定位不是模型自由生成的答案，而是门店/员工号配置驱动的协议发送动作。当前实现先在 `WxWorkProtocolInstance` 上保存门店定位字段，后续可再抽象到门店主数据供多个员工号共享。每个员工号实例需要在后台维护：

- `storeAddress`：门店详细地址。
- `storeNavigationName`：地图里展示的导航名称，例如“丽斯未来酒店杭州西湖店”。
- `storeLongitude/storeLatitude`：经纬度，统一使用协议文档要求的坐标系和字段格式。
- `storeMapProvider`：坐标来源，例如 `browser_geolocation`、高德、腾讯、百度，用于后续坐标转换审计。

账号设置页已加入“门店定位绑定”测试区，包含门店地址、导航名称、纬度、经度、坐标来源、一键获取当前坐标和当前绑定状态。列表里也用“已绑定位/未绑定位”标签提醒配置是否完整。一键获取坐标调用浏览器 `navigator.geolocation`，适合门店员工在门店现场自行填入；它只获取当前设备坐标，不做地址转坐标。若要通过地址自动解析经纬度，需要后续接入腾讯/高德地图 key，并在 UI 中明确坐标系。

微信定位卡片的发送原理：AgentDesk 创建 `location` 类型消息，payload 里放 `longitude/latitude/address/title/zoom`，outbox 调用协议 `/msg/send_location`。微信/企业微信客户端收到后会自动渲染成定位卡片。腾讯地图或高德地图不是发送定位的必需依赖，它们只用于“根据地址查坐标”“坐标系转换”“门店批量校准”。之前天安门测试就是直接发送经纬度和标题地址，微信端渲染为定位。

2026-06-29 修正规则：客户问“你们酒店在哪里 / 怎么去 / 到店路线 / 酒店地址 / 导航发我”等明确位置意图时，不再固定回复“要我把定位发您吗？”这种二段式话术，而是直接创建 `location` 类型消息并进入 outbox。`/msg/send_location` 必须严格按 `wework.apifox.cn` 的 `SendLocationModel` 发送：`guid`、`conversation_id`、`longitude(number)`、`latitude(number)`、`address`、`title`、`zoom(number)`。经纬度不得作为普通文本发送，也不得用文本“丽斯未来酒店门店位置”冒充定位卡片。只有“离我多远 / 附近有什么”等需要客户当前位置或更多语义判断的问题，才回到 AI 自然对话或追问，不使用固定确认模板。

位置意图由规则只做“确定动作路由”，不能替代 AI 思考。也就是说，规则层可以决定“发送门店定位卡片”这一真实动作，但不能把所有位置相关问题都改写成统一话术；AI 仍负责自然表达、上下文判断、缺字段追问和异常转人工。若员工号实例没有坐标，系统不得编造地址或坐标，应转人工/提示配置缺失。

### 13.3.2 员工号独立智能客服配置

2026-06-29 起，企微员工号 AI 配置收敛为单一来源：每个 `WxWorkProtocolInstance` 必须绑定自己的独立 `AIAgent`。原“AI 能力 / 智能客服”页面保留历史兼容，但不再作为新业务入口；会话页账号管理中的“智能客服配置”按钮是员工号智能客服的唯一产品入口。

生效规则：

| 配置项 | 数据来源 | 生效范围 | 说明 |
| --- | --- | --- | --- |
| 智能客服配置 | `WxWorkProtocolInstance.aiAgentId -> AIAgent` | 当前员工号所有会话 | 包含模型、系统提示词、欢迎语、知识库、技能、工具、转人工/兜底策略 |
| AI 回复开关 | `WxWorkProtocolInstance.aiReplyEnabled` | 当前员工号所有会话 | 只控制是否托管，不承载提示词/知识库/技能配置 |
| 门店知识库绑定 | `WxWorkProtocolInstance.knowledgeBaseId` | 当前员工号 | 智能客服编辑时锁定为该知识库，避免误配成多知识库 |

运行时不再执行“全局 Agent + `personaPrompt` 追加”的提示词叠加逻辑。新企微会话创建和旧企微会话复用时，都优先使用当前 `WxWorkProtocolInstance.aiAgentId` 指向的启用 Agent；如果员工号没有绑定独立智能客服，或该 Agent 被禁用/删除，系统不得调用模型编答案，应记录配置异常并等待人工/后台修复。

迁移规则：系统启动迁移 `000008_migrate_wxwork_protocol_ai_agents` 会幂等扫描 `status=启用`、`knowledgeBaseId>0`、`aiAgentId=0` 的员工号，为每个账号复制一个启用的基础 Agent，合并原账号 `personaPrompt` 到新 Agent 的 `SystemPrompt`，并把当前 `knowledgeBaseId` 写入新 Agent 的 `knowledgeIds`。迁移后该账号拥有自己的 Agent，修改 A 门店不会影响 B 门店。迁移只处理已绑定知识库的账号；未绑定知识库的账号仍保持未配置状态，必须先完成门店/知识库绑定后，再在账号管理中点击“智能客服配置”初始化。

前端规则：账号编辑表单不再展示 `AI Agent ID`、`personaPrompt`、上下文消息数/Token 等碎片字段；这些内容统一在“智能客服配置”弹窗里编辑。原 `/dashboard/ai-agents` 页面和接口保留，但导航入口、Dashboard 快捷入口和告警跳转都收敛到会话页，防止运营继续从两个入口配置导致生效规则混乱。

接口规则：账号侧新增 `POST /api/dashboard/wxwork-protocol-instance/init_ai_agent` 和 `POST /api/dashboard/wxwork-protocol-instance/update_ai_agent`。前者用于未配置账号按当前门店知识库复制独立 Agent；后者用于保存该账号绑定 Agent 的模型、系统提示词、欢迎语、技能、工具、转人工、兜底策略等完整配置。前端必须通过 `web/lib/api/admin.ts` service 调用，不允许页面或业务组件裸 `fetch`。

2026-06-29 文本模型配置：当前 LLM 从 DeepSeek 切换为 OpenAI-compatible `qwen3.6-flash`，base URL 为阿里云兼容接口；DeepSeek 文本配置已禁用，当前启用员工号智能客服 Agent 应绑定新的 LLM 配置。视觉/ASR/TTS 仍按各自 `modelType` 独立选择，禁止把文本模型、视觉模型、ASR/TTS 混用。

员工号实例绑定 `storeId` 后，客户在该员工号会话里的定位相关表达由账号默认资源意图层处理：明确指令如“把酒店定位发我”“发个定位”“你们酒店在哪里”“怎么去”“到店路线怎么走”会直接发送 `location` 卡片；依赖客户当前位置或更多上下文的问题，如“离我多远”“附近有什么”，交给 AI 自然对话或追问。定位动作不能用固定话术冒充，必须读取当前 `WxWorkInstanceID` 绑定的门店坐标，创建 `location` 类型 `Message`，再通过 `/msg/send_location` 进入 outbox。典型流程：

```mermaid
flowchart LR
  A[客户位置表达] --> B{是否可直接发送门店定位}
  B -->|是| C[读取当前 WxWorkInstanceID 的 address/navigationName/lng/lat]
  C --> D[创建 location 类型 Message 和 Outbox]
  D --> E[协议 /msg/send_location 发回原员工号会话]
  B -->|否，需要更多上下文| F[交给账号独立智能客服自然回复/追问]
```

入站定位也会反向维护账号配置：客户或门店通过当前会话发来 `location` 消息时，payload 中的 `longitude/latitude/title/address` 会被 `BindInboundLocation` 写回当前 `WxWorkProtocolInstance` 的 `storeLongitude/storeLatitude/storeNavigationName/storeAddress/storeMapProvider=wxwork_inbound_location`。这适合门店首次上线时让员工直接从微信发一次门店定位完成绑定。

一百多家门店时，短期可以由每个门店员工号在账号设置里填坐标，或让门店发送一次定位自动绑定，便于快速落地和测试；中长期建议把坐标上移到门店表，再让多个员工号绑定同一门店共享坐标。若同一客户联系不同门店员工号，定位按当前 `guid` 对应门店发送。门店没有坐标时，系统不能让 AI 编坐标，只能提示配置缺失并转人工。

出站乱码防线：历史回调或人工录入里可能出现 UTF-8 被错误当作 Windows-1252/Latin-1 展示后的乱码，例如微信端显示 `ã¸½æ–...`。系统在三个层面修复：账号设置保存 `storeAddress/storeNavigationName/defaultMiniProgramPayload/welcomeMessage` 时修复；入站定位自动绑定时修复 `title/address`；最终调用 `/msg/send_location`、`/msg/send_weapp` 等富媒体发送接口前，对 payload 中所有字符串递归执行 `RepairMojibakeText`。这样即使历史 payload 里有脏中文，也不会继续把乱码发给客户。

模型与话术配置规则：

- `AIModelType` 已扩展为 `llm/embedding/rerank/vision/asr/tts`，前端 AI 配置页同步展示这些类型。
- 客服 Agent 组装 instruction 时强制加入“酒店前台同事”基础服务风格：短句、自然、不固定使用 emoji、不说“根据知识库”、不自称 AI；维修、漏水、卫生、投诉、安全、退款等问题先接住，再收集必要信息或转同事。执行型任务在工单/转人工工具成功前，不能说已经安排、已经登记、已经通知。
- 每个门店员工号的品牌语气、门店差异、知识库、技能和转人工策略，都写入该账号独立 `AIAgent.SystemPrompt/knowledgeIds/skillIds/tools`，不再通过 `personaPrompt` 做运行时叠加。

加好友/首次会话欢迎链路：员工号实例新增 `welcomeMessage/welcomeSendMiniProgram/welcomeAskLocation`。当 `guid + externalUserId/chatroom` 第一次创建长期会话时，系统不走普通 Agent 欢迎语，而是使用当前员工号实例配置：先发送 `welcomeMessage`，再按开关发送默认小程序，最后按开关询问“要我把某某门店定位发您吗？”。欢迎里的小程序和定位都来自当前员工号绑定的变量，不是全局固定值；一百多家门店每个账号可以有自己的定位、欢迎语和小程序 payload。

GIF/表情包收发规则：入站根据协议回调里的 `msg_type/content_type/source_type` 识别为 `gif`，优先下载并保存为 asset，网页端渲染为动图卡片；没有可访问资产时保留协议 payload 并显示“表情消息”卡片。AI 只能把表情视为轻互动，不能臆测表情背后的业务含义。出站时，网页客服上传 `.gif` 或 MIME 为 `image/gif` 的文件会创建 `Message(gif)`，outbox 按员工号协议走 WECDN 上传并调用 `/msg/send_gif`，不能降级成普通附件或文本。普通 emoji 字符仍按文本发送，不走 GIF 接口。

客户身份隔离规则：同一个微信客户可能住过多个门店、同时联系多个员工号。AgentDesk 可以在客户主数据层识别同一自然人，但会话、AI 记忆、session 摘要、门店知识库和默认资源必须按 `guid + externalUserId/chatroom` 隔离；再结合 `storeId/knowledgeBaseId` 选择当前门店。A 门店员工号收到的早餐、定位、小程序、投诉和工单上下文，不能被 B 门店员工号直接复用为当前事实。跨门店只允许沉淀“稳定客户事实”（例如称呼偏好）并且要显式标注来源，不得把旧门店已解决故障带入新门店会话。

企微员工号渠道适配器边界：

- 新增 `WxWorkProtocolAdapter` 接口，当前默认实现封装在 `defaultWxWorkProtocolAdapter`，用于承接员工号协议发送能力。
- 业务层仍通过 AgentDesk 的会话、消息、outbox、路由状态工作；协议细节集中在 `wxwork_protocol_service` 和 adapter 内，不再把 CLI/KF 字段混入新链路。
- 后续如果协议供应商接口变更，优先替换 adapter 的上传、下载、发送、操作型接口实现，避免大面积改会话和 AI 业务层。

### 13.4 上下文压缩策略

原始企微消息、媒体、人工回复永久保存，不删除、不覆盖。AI 只使用“当前 session 最近消息 + 稳定事实摘要”：

- 默认 `contextMaxMessages=30`。
- 默认 `contextMaxTokens=8000`。
- 默认 `contextCompressionEnabled=true`。
- 超过阈值后生成摘要，摘要只保留稳定事实、未解决事项、用户偏好、当前门店信息。
- 已关闭/已解决的旧故障不带入新 session，避免旧维修问题污染新问题。

### 13.4.1 高频客服场景处理清单

下面场景作为企业微信员工号渠道后续开发和测试的基础验收集。原则是：原始消息永久保存，AI 只看当前 session 和稳定事实；人工状态优先于 AI；媒体只有理解成功才进入问答事实。

| 场景 | 系统处理规则 |
| --- | --- |
| 1. 客户只发文本问题 | 当前 session 内检索门店知识库并回复 |
| 2. 客户先发图片再问“这是啥” | 等待图片理解完成，只回复一次，答案结合图片理解和追问 |
| 3. 客户先发图片但不追问 | 图片理解成功后可自然确认收到，不主动编业务需求 |
| 4. 图片理解失败后客户追问 | 如实说明暂时没看清，请客户补充文字或转人工 |
| 5. 客户连续发两个文本问题 | 短窗口内取消旧触发，优先回答最后一条；必要时合并回答两个问题 |
| 6. 客户连续发多张图片 | 等待最近一批图片理解，按最新追问组织回复 |
| 7. 客户发语音后补一句“你听下” | 等语音转写，结合追问回复 |
| 8. 语音转写失败 | 展示语音并提示暂时听不清，可让客户文字补充或转人工 |
| 9. 客户发文件让看内容 | 文本类文件解析后回答；PDF/Word/Excel 未接解析器前只展示并转人工/提示 |
| 10. 客户发视频/GIF | 展示和审计，不把内容当作已理解事实，除非后续接视频理解 |
| 11. 客户发位置 | 落库展示，必要时结合门店距离/路线能力；当前先作为位置信息审计 |
| 12. 客户要求“发定位” | 读取员工号绑定门店坐标，发送 location 消息 |
| 13. 门店坐标缺失 | 不编坐标，提示地址并产生配置告警 |
| 14. 客户问早餐/停车/发票等标准 FAQ | 只查当前门店知识库，不能混总部或其他门店答案 |
| 15. 客户问维修/漏水/安全 | 先安抚并记录，必要时进入人工待处理，不只让客户自己找前台 |
| 16. 客户投诉服务 | 降低话术机械感，先表达会处理，再收集房号/联系方式并提醒人工 |
| 17. 客户要求转人工 | 幂等触发人工，不重复发多条转人工提示 |
| 18. AI 回复开关关闭 | 网页客服可直接发消息，首次发送自动进入网页人工接待 |
| 19. 已分配其他客服 | 不能绕过分配直接抢发，必须接管/转接 |
| 20. 门店员工在企微客户端手动回复 | 回调落库为人工来源，AI 进入停答窗口 |
| 21. 总部网页客服接管 | AI 停答，总部回复通过原员工号 outbox 发出 |
| 22. 人工 10 分钟无客户新消息 | 自动恢复 AI，并只发一条自然提示 |
| 23. 人工超时后人工迟到回复 | 默认只入库和提醒后台，不自动发给客户 |
| 24. 会话关闭后客户又发消息 | 复用长期会话身份，新开 session，不带旧已解决故障 |
| 25. 客户跨门店联系 | `guid + customerUsername` 隔离记忆和知识库 |
| 26. 群聊客户消息 | 会话 key 用 `guid + chatroom`，群内发送人写扩展字段 |
| 27. 群里多人同时说话 | 按 chatroom 维持会话，必要时只回复被 @ 或明确对客服的问题 |
| 28. 平台 echo 回调 | 只确认发送状态，不创建可见重复消息，不触发 AI |
| 29. 重复回调同一 msg_id | 幂等跳过并写同步日志 |
| 30. 客户撤回消息 | 更新消息状态，不触发 AI；已生成但未发出的 AI 回复应取消 |
| 31. 客户发敏感/高风险内容 | 优先转人工，AI 不做承诺和判断责任 |
| 32. 客户问订单但系统暂无订单 | 询问必要信息，不编订单状态 |
| 33. 客户问同一问题多次 | 可简短复述结论并提示可转人工，不机械重复完整长答案 |
| 34. 高频答不上问题 | 进入待归档问答，按门店聚合 frequency，周导出审核 |
| 35. 模型或知识库异常 | 回复固定兜底、写健康告警，不让客户看到技术错误 |
| 36. outbox 发送失败 | 消息保持 failed/retry 状态，前端可见真实错误，不误标 sent |

### 13.4.2 网页端消息身份展示

会话窗口中所有非客户侧消息必须显示来源标签：

- `AI回复`：`senderType=ai`，由 AgentDesk AI 生成并经 outbox 发出。
- `人工`：`senderType=agent`，由网页客服或门店人工同步产生。
- `客户`：`senderType=customer`，由协议回调入站。

该标签只展示真实消息来源，不作为业务状态判断。客服分配、AI 开关、人工接管仍以后端会话路由状态为准。

外部联系人的“已读/未读”和“在线/离线”只有在协议提供可靠回执时才允许展示。当前员工号协议链路不能稳定证明客户是否已读，也不能稳定同步外部联系人在线状态，因此网页端不再显示“客户未读/用户离线”这类误导标签；AI/人工侧消息只展示发送状态，例如 `发送中/已发送/发送失败/已撤回`。客户头像同理，只有协议联系人资料真实同步到头像 URL 后展示，否则使用姓名首字母或默认头像。

### 13.4.3 执行型任务和工单闭环

执行型任务必须落到真实系统动作，不能只靠话术。推荐处理流程：

```mermaid
flowchart TD
  A[客户消息] --> B{意图分类}
  B -->|FAQ_CHAT| C[查当前门店知识库并回复]
  B -->|INFO_CLARIFICATION| D[追问一个关键字段]
  B -->|SERVICE_TASK| E{房号/事项/数量/时间是否齐全}
  E -->|否| D
  E -->|是| F[prepare_ticket_draft]
  F --> G{ready}
  G -->|否| D
  G -->|是| H[create_ticket_with_confirmation]
  H --> I[工单成功后才告知已记录]
  B -->|HUMAN_DECISION| J[handoff_to_human 或网页人工接管]
  B -->|LOCATION_NAVIGATION| K{实例是否有坐标}
  K -->|有| L[/msg/send_location]
  K -->|无| J
  B -->|MEDIA_UNDERSTANDING| M{媒体是否解析成功}
  M -->|成功| C
  M -->|失败| J
```

最少覆盖的任务分类和场景：

| 分类 | 典型场景 | 系统动作 |
| --- | --- | --- |
| FAQ_CHAT | 早餐时间、停车、Wi-Fi、退房时间、发票规则 | 查当前门店知识库，短句回复 |
| INFO_CLARIFICATION | “帮我处理一下”“这个怎么弄”但缺对象 | 只问一个关键问题 |
| SERVICE_TASK | 送水、送拖鞋、送毛巾、加被子、打扫、维修空调、马桶堵、行李协助、叫醒服务、补纸巾 | 收集房号/事项/数量/位置/时间，建工单或转人工 |
| HUMAN_DECISION | 退款、赔偿、投诉升级、订单异常、价格争议、安全风险、隐私授权、客户情绪激烈 | 转人工，不自行承诺 |
| LOCATION_NAVIGATION | 发定位、怎么走、停车入口、附近地铁、导航名称 | 使用实例坐标发送定位或转人工 |
| MEDIA_UNDERSTANDING | 图片问物品/环境、语音说明需求、文件让核对、客户发定位 | 等媒体解析结果；失败不猜 |

十个必须重点验收的真实客服场景：送水缺房号、送拖鞋缺数量、马桶堵需维修位置、空调不制冷需房号和现象、客诉要求赔偿、发定位但门店未填坐标、客户发图片再补问、客户连发两问、客户发语音报修、客户已关闭会话后再次发新问题。每个场景都要验证 AI 不空口承诺、不会把旧问题带入新问题、工具成功前不说已安排。

### 13.5 GitHub 绑定原则

当前目录若还不是 Git 仓库，绑定 `520skyincloud/agentdesk` 时必须先 `git init`、添加远端、`git fetch origin` 查看远端默认分支。远端非空时新建本地工作分支并合并远端历史，必要时使用 `--allow-unrelated-histories` 手动解决冲突。用户未特别说明“分支/PR”时，后续默认把确认完成的代码合并/推送到主分支；如果用户明确要求分支，则推工作分支或开 PR。

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
12. 客户发图片/语音/视频/文件，后台展示并审计；图片/语音/文件只有理解成功并写回 `mediaText/mediaSummary` 后才触发 AI。
13. 客服网页端发送图片/文件/语音/GIF/普通视频/位置/链接/视频号直播/合并转发/引用消息，协议 body 符合文档；业务错误码不误标 sent。
14. 群@必须使用真实 `R:` 群聊会话测试，不能用单聊会话冒充。
15. 大视频必须补齐 big cdn 上传和预览图上传后再测试 `/msg/send_big_video`。
16. 微信小店商品必须使用真实小店商品数据测试，假 `content` 不允许标记为成功。
17. 入站图片必须通过 `/cloud/wx_download` 或 `/cloud/c2c_download` 下载后再视觉理解；真实验收样本 `Message(id=233)` 已完成 OSS 保存、`mediaText/mediaSummary` 写回和 AI 触发。
18. 知识候选按门店导出 Markdown/JSONL，人工审核后再导入门店知识库。

### 13.8 账号配置入口重构与远程开户注册

企微员工号后台配置拆成三层：

| 层级 | 配置内容 | 入口 |
| --- | --- | --- |
| 企业资源 | 企业绑定小程序、统一品牌资源、全局协议资源 | 企业/系统资源设置，不放在单个账号编辑 |
| 员工号实例 | guid、员工资料、门店资料、坐标、服务时间、门店群通知、AI 开关、好友申请开关 | 会话页左侧账号管理的账号编辑 |
| 员工号智能客服 | 模型、提示词、欢迎语、知识库、技能、工具、转人工策略 | 账号行操作“智能客服配置” |

实现规则：

- `WxWorkProtocolInstance` 新增 `remoteSetupToken / remoteSetupExpiresAt / remoteSetupSubmittedAt`，用于远程门店开户页面定位单个待配置实例。
- 后台接口 `POST /api/dashboard/wxwork-protocol-instance/create_remote_setup` 创建待配置实例并返回 `/wxwork-remote-setup/{token}` 链接。
- 公开接口 `/api/wxwork-protocol-remote-setup/*` 不走 dashboard 鉴权，仅通过 token 操作当前实例。
- 远程页可调用真实协议登录二维码接口、检查扫码状态、保存门店名称/地址/经纬度/服务时间/门店群通知配置。
- 远程页不允许修改知识库、模型、提示词、小程序 payload；这些由总部在员工号智能客服和企业资源设置中维护。
- 智能客服配置保存时，`UpdateBoundAIAgent` 以提交的 `knowledgeIds` 为准，并把第一个知识库同步到员工号 `knowledgeBaseId`，确保运行时仍能快速按员工号找到唯一门店知识库。

### 13.9 聚合智能实例池与自动认领 GUID

2026-06-29 起，企微员工号新增扫码不再允许创建 `ad_...` 这类占位 GUID，也不再依赖手工粘贴候选 GUID。系统管理新增“实例池”页面，负责登录聚合智能后台、同步真实 XBot 实例，再由新增员工号流程自动认领空闲实例。

接口边界必须区分两条链路：

| 链路 | 用途 | 接口 | 说明 |
| --- | --- | --- | --- |
| 聚合智能后台管理 | 同步实例池 | `POST https://chat-api.juhebot.com/admin/login`、`POST /admin/ListInstance` | 只用于拿真实实例列表和状态，凭据保存在运行时数据库，不写入代码或文档 |
| 企微员工号协议业务 | 登录二维码、消息收发、资料同步 | `POST https://chat-api.juhebot.com/open/GuidRequest`，body 中包含 `app_key/app_secret/path/data` | 仍严格按 `wework.apifox.cn` 的具体接口页传字段 |

实例池本地表 `WxWorkProtocolDevicePoolInstance` 保存：`guid / providerInstanceId / uin / providerUserId / clientType / seatName / bridgeId / state / expiredAt / syncStatus / lastSyncedAt / boundWxWorkProtocolInstanceId / rawJSON`。`rawJSON` 用于审计上游原文，页面展示只用结构化字段。

空闲判断规则：

1. 聚合智能 `/admin/ListInstance` 返回真实 `guid`。
2. `uin` 为空，表示该实例当前未登录企微账号。
3. `expiredAt` 未过期。
4. 本地不存在非删除状态的 `WxWorkProtocolInstance.guid = guid`；如果已存在，则实例池显示“已绑定”。
5. 认领前用企微协议业务链路探测 `/login/get_login_qrcode`，且 `error_code=0` 才允许创建本地员工号扫码记录。

新增账号流程：会话页或企微员工号管理点击“新增扫码”时，如果请求没有显式传入 `guid`，后端调用 `WxWorkProtocolDevicePoolService.ClaimAvailableGUID`。没有配置实例池、没有同步、没有空闲实例或探测二维码失败时，直接返回可读错误：“请先在系统管理 > 实例池配置聚合智能账号并同步设备列表”或“实例池暂无空闲实例”。系统不得创建假的本地账号记录。

绑定回写规则：

- `CreateLoginInstance`、远程开户链接、手动创建员工号只要成功创建本地 `WxWorkProtocolInstance`，就调用 `BindGUIDToInstance` 把实例池行标记为 `bound`。
- 实例池同步后会重新扫描本地所有未删除员工号，修正 `boundWxWorkProtocolInstanceId`，因此旧数据也能在页面里显示绑定关系。
- 已登录实例例如 `uin` 非空但本地未绑定时，页面显示“已登录”，不会被自动认领；需要人工确认是否补录绑定。

当前真实验证结果：聚合智能账号同步返回 2 个实例，其中 `44406fbf-4fd2-3b9c-a389-71546fa52e0d` 已登录并绑定本地员工号 `吴朝伟`；`7828cf99-9376-30f7-a9ac-01ac6ca87076` 在聚合智能侧 `uin` 为空，但本地已有待扫码绑定记录，因此显示为已绑定，不再被当作空闲实例重复使用。若未来给出 100 个实例 ID，系统按上述规则自动筛选空闲项，不使用截图里的 ID 猜测。
