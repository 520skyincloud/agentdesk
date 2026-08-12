# 会话规则派单与班次运行引擎

> 状态：当前实现依据（2026-08-12）
>
> 工作分支：`codex/dispatch-schedule-takeover`
>
> 适用范围：总部网页客服的待接入会话、规则派单、人工编排、班次交接、首响与追问恢复
>
> 代码优先：文档与代码冲突时，以 `internal/services/conversation_dispatch_*`、`conversation_human_dispatch_service.go`、`agent_team_schedule_service.go` 和 `service_analytics_capture_service.go` 的真实调用链为准。

## 1. 结论

当前派单只支持两种模式：

| 模式 | 运行语义 |
| --- | --- |
| `manual` | 系统不选择客服，任务留在综合客服组编排池，由有权限的主管派发 |
| `rule` | 系统按排班、确定性资格、容量、实时压力和本班工作量自动派发 |

模型选择客服已经从运行时、客服组配置、前端枚举和模型用途列表删除。派单不会调用 LLM，不产生新的派单模型 usage，也不存在低置信度降级路径。

该删除只针对“选择哪位客服”。AI 回复、意图识别、转人工判断、交接摘要、知识检索和模型计费链路不在本次删除范围，仍按各自权威设计运行。

`Conversation` 继续是唯一派单单位，`ConversationAssignment` 记录每次分配。系统不新增平行派单任务表，不把工单并入即时会话派单。

## 2. 运行职责

派单链路由既有对象共同完成：

```text
Conversation / ConversationRouteState
  -> 解析唯一综合客服组
  -> AgentTeamSchedule 确定当前计划值班范围
  -> AgentProfile / User / Permission 确定接待资格
  -> ConversationAssignment 固化派单结果
  -> ConversationEventLog / DispatchDecisionLog 固化审计证据
```

- `AgentTeam`：业务责任、门店员工号范围和派单模式。
- `AgentTeamSquad`：综合组内可复用人员集合，不形成独立任务池。
- `AgentTeamSchedule`：计划在什么时间由全组或某小组值班。
- `AgentPresenceSession`：客服在线、空闲、忙碌、休息和断开状态，仅用于工作台展示与运营分析，不决定派单资格。
- `ConversationAssignment`：某服务轮次分给谁、以何种方式、任务权重和小组快照。
- `DispatchDecisionLog`：候选快照、失败原因、人工覆盖和恢复证据。

有效排班表示客服在该时段承担接单责任。规则派单不再读取 Presence 作为门禁；离线、忙碌或休息状态不会阻止排班内客服接收任务，也不会触发已派会话自动转移。

## 3. 客服组解析

人工会话进入总部待接入后，沿用现有绑定事实解析综合客服组：

1. 企微员工号实例的 `AgentTeamID`。
2. 门店员工绑定的 `AgentTeamID`。
3. 门店或企微服务范围只命中一个综合客服组时使用该组。
4. 已有 `Conversation.CurrentTeamID`。
5. 租户内只有一个启用的默认客服组时使用默认组。

解析到多个不同客服组时不猜测，不跨组随机派发；解析不到唯一客服组时保留在待派池并通知租户主管。所有查询必须带 `TenantID`，模型、历史 Company 和已退役的旧 Agent 字段不参与客服组解析。

## 4. 持续派单触发

客户消息持续、不定时进入，因此派单采用“实时触发 + 周期补偿”：

1. AI 转总部人工并进入 `HQ_AGENTDESK_PENDING` 时触发。
2. 待派会话收到新客户消息时触发。
3. 会话被主管释放回待派池时触发。
4. 客服档案、自动接单、容量、账号或权限变化时触发。
5. 当前或刚结束的排班被创建、修改、删除时触发。
6. 会话关闭释放容量时，触发所在客服组继续消化队列。
7. 后台每 30 秒扫描待派会话，并检查未首响任务及客户追问超时后的硬失效任务，补偿进程重启、瞬时冲突和遗漏事件。

客服组模式/状态/来源范围、小组成员、当前排班、客服档案、账号状态、账号角色和角色权限提交成功后，会立即按租户执行一次失效 Assignment 回收，并通过现有客服组防抖队列继续消化待派任务。定时扫描只承担补偿，不是配置生效的唯一手段。

实时事件使用 800ms 防抖。同一会话和同一客服组分别有并发保护；高频客户消息或多个配置事件不会启动重复派单。事务提交前再次校验 `LastMessageID`，旧消息快照不能覆盖新状态。

周期扫描默认每批最多处理 50 条。扫描先按租户游标轮转，再在租户内优先轮转启用的规则客服组；人工组积压不会占用规则组补偿预算。未解析到唯一客服组的会话使用独立小窗口检查并通知，不挤占正常规则派单。一个租户、一组积压或一条失败会话都不会阻断其他队列。

实时客服组队列也不扫描租户全部待接入会话。数据库优先取 `CurrentTeamID`、企微员工号绑定或门店员工绑定明确指向该组的任务，再用有限余量检查尚未固化组别的全局池任务；服务层仍会重新解析并校验唯一客服组，查询命中不等于绕过路由规则。

## 5. 规则候选硬约束

自动候选必须同时满足：

- 会话、客服组、账号、客服档案和路由属于同一租户。
- 综合客服组启用且 `DispatchMode=rule`。
- 当前存在有效排班；小组班次只选该小组有效成员。
- 班次临时加入名单可以补充小组成员，临时排除名单优先排除。
- `User` 和 `AgentProfile` 启用，账号未删除。
- 客服具有 `conversation.view` 和 `conversation.send`。
- `AutoAssignEnabled=true`，且 `MaxConcurrentCount > 0`。
- 满足综合客服组和客服档案的门店、企微员工号服务范围。
- 当前处理中会话数小于最大并发。

`busy`、`break`、离线或 Presence 过期不影响派单。无排班、非值班小组、已满载、无回复权限或关闭自动接单仍不能接收新规则任务。

人工派发仍受租户、综合客服组、服务范围、账号状态和回复权限约束，但主管可以有意覆盖排班、自动接单和容量限制。人工派发同样不读取 Presence，操作必须填写理由并留下审计记录。

## 6. 待派任务顺序

任务排序只决定先处理哪条会话，不能绕过候选硬约束。

规则优先级来源：

- `Conversation.Priority` 的显式业务值，规范到 0-100。
- 安全、人身、治安或重大财产风险的既有转人工原因，最低提升到 100。
- Queue SLA 等待老化：达到目标的 80%、1 倍、2 倍、4 倍时逐级提升。
- 同优先级按进入人工池时间，再按会话 ID 稳定排序。

数据库读取和实际处理都保留双窗口：约 80% 名额按有效优先级，至少 20% 名额从最老任务中取得。两个窗口合并去重后再执行 SLA 老化排序，因此持续到来的高优先级任务不能让旧任务永远进不了处理预算；实时事件仍保证新紧急任务无需等待周期扫描。

工作量权重使用 `Conversation.DispatchWeight`，规范到 1-5；安全类最低为 5。规则引擎不再用投诉、退款、故障等自由文本猜测任务复杂度，也不让模型重写权重。上游有可靠业务分类时，应显式写入 `Priority` 和 `DispatchWeight`。

## 7. 公平负载

每次派单从数据库重新计算，不依赖页面缓存：

- `activeCount`：当前处理中会话数。
- `weightedOpenLoad`：当前会话权重，加上客户连续未回复消息压力。
- `pendingFirstReply`：已派发但该 Assignment 对应客服尚无当前轮有效人工首响的会话数。
- `pendingReplyCount`：客户最新发言后，当前 Assignment 对应客服仍未回复的会话数。
- `shiftWorkloadWeight`：当前班次内仍有效的工作量，包括活动分配和已产生人工回复的完成分配。
- `lastAssignedAt`：最近一次有效派单时间。

未回复就被释放或重派的旧 Assignment 不永久计入原客服本班工作量，避免一次失败重派持续惩罚原客服。已真实回复过的工作保留在本班累计量中。

实时压力公式：

```text
normalizedLoad =
  (weightedOpenLoad + pendingFirstReply * 0.75 + pendingReplyCount * 0.5)
  / max(MaxConcurrentCount, 1)
```

本班公平债务：

```text
shiftDebt = shiftWorkloadWeight / max(MaxConcurrentCount, 1)
```

客户连续未回复消息从第二条开始增加压力，最多增加两点；最早未回复消息等待满 10 分钟和 30 分钟分别再增加一点，防止客户拆分短句无限放大负载。

选择过程：

1. 候选先按实时压力、班次债务、活动会话数、最近派单、客服优先级和用户 ID 稳定排序。
2. 以最公平候选为基准建立公平带：`normalizedLoad` 差不超过 0.20，且 `weightedOpenLoad` 差不超过 1。
3. 只在公平带内比较本班债务。
4. 债务相同时，近 30 天在同一来源实际回复过该客户的客服优先；没有同来源记录时再使用跨来源客户连续性。复用同一 Conversation 的旧 `SessionNo` 也属于历史，当前轮 Assignment 不参与连续性判断。
5. 再按待首响、待回复、最久未获新任务、客服优先级和用户 ID稳定决胜。

历史连续性只能打破公平范围内的近似平局，不能让熟悉客户的忙客服绕过明显负载差距。

所有首响、待回复、恢复、连续性和班次工作量统一使用同一回复事实：消息必须与 Assignment 同租户、同会话、同服务轮次，`sender_type=agent`、`sender_id=Assignment.ToUserID`，发送时间位于该分配段内且状态不是失败或撤回。其他客服、AI 或系统消息不能替当前客服完成首响。

## 8. 事务与并发

最终派发在 service 事务中完成，并锁定会话、客服档案和客服组。提交前重新检查：

- 会话仍待接入、未分配、租户一致且最后消息未变化。
- 候选账号、档案、权限、排班、小组、覆盖范围仍有效。
- 客服组仍为规则模式，容量仍未满。
- 当前负载快照仍与候选计算一致。
- 同一轮恢复候选不处于冷却期。

同一事务写入 Assignment、Conversation、事件日志和 RouteState；任一步失败整体回滚。提交后才发布 WebSocket 会话变化和 `ConversationAssignedEvent`，避免页面、通知和数据库出现部分状态。

## 9. 首响与追问恢复

自动恢复只处理当前有效的规则 Assignment，且会话必须仍在当前服务轮次、处理中并保持原指派关系。人工派发/转派的 Assignment 不被规则恢复接管。

### 9.1 首次人工回复前

以下硬失效可立即重派：客服组/账号/档案停用、失去回复权限、离开当前班次或小组、来源服务范围失效。Presence 变化、关闭自动接单、修改最大并发或当前负载升高只影响展示或后续新任务，不打断已建立的指派关系。超过 First Response SLA 属于软失效：有合法替代人时重派，无替代人时保留原指派并通知主管，避免无意义释放。

### 9.2 已回复后的客户追问

服务连续性优先。只有同时满足以下条件才自动接力：

1. 当前客服在本 Assignment 内已经真实回复。
2. 后续客户消息尚未被当前客服回复。
3. 最老未回复客户消息已超过租户 `ResponseTargetSeconds`，默认 300 秒。
4. 当前客服发生硬不可用：离开班次、账号/档案/客服组停用、失去回复权限或来源范围失效。

离线、`busy`、`break`、负载偏高、关闭自动接单、修改最大并发或仅仅出现更空闲的客服，都不能打断已经建立的服务关系。客户没有待回复消息或等待尚未超时时也不转派。

两个阶段共用以下保护：

- 有合法替代客服时按同一公平规则重派；无替代人且属于硬失效时释放回原综合组待派池并通知主管，不静默跨组。
- 最近 90 秒接过该会话的客服进入冷却，不能在几人之间来回跳。
- 初次派发后最多自动重派 3 次；达到上限后释放到人工编排池并通知组长/公司主管。
- 事务内重新锁定 Conversation 和 Assignment，复核 `SessionNo`、`LastMessageID`、真实回复、最老未回复客户消息、SLA、原客服失效原因、候选资格、容量和负载；判断期间客服已回复或状态恢复时，本次动作冲突退出。
- 提交后才发送 WebSocket 变化、通知和审计事件。

因此班次结束不会立即抢走已实际服务的客户；只有客户仍在等待且原客服确实无法继续服务时，系统才进行有界接力。

## 10. SLA 与状态

派单工作台使用运营分析策略中的两个独立目标：

| 阶段 | 起点 | 终点 |
| --- | --- | --- |
| Queue SLA | 进入总部人工待接入 | 成功分配客服 |
| First Response SLA | 成功分配客服 | 当前轮第一条人工回复 |

达到目标时长 80% 显示“即将超时”，到达截止时间显示“已超时”。待派任务即使已预警或超时，仍可规则派发或人工派发。

`ConversationRouteState.ManualExpireAt` 是门店人工/总部人工恢复 AI 的路由超时字段，不是派单 SLA。派单 DTO 和派单页面不再展示或依赖该字段。

## 11. 通知

- 规则模式进入待派池时不向全体客服广播。
- 自动派发成功只通过既有 `ConversationAssignedEvent` 通知被选中的客服。
- 人工模式的新任务通知该综合客服组组长。
- 无法解析客服组时通知租户公司主管。
- 队列 SLA 超时仍无候选、恢复失败或达到恢复上限时通知组长；没有有效组长时回退公司主管。
- 同一会话、同一业务原因和同一服务阶段使用现有 Notification 去重，不重复轰炸。

## 12. 页面和权限

不新增平行配置入口：

- 客服组编辑页：只提供“人工派单 / 规则均衡”。
- 客服组排班页：现有日历、单条排班和批量排班承载班次、小组、替班和请假。
- 派单工作台：展示任务来源、Queue/首响 SLA、任务权重、规则理由、客服实时压力、本班工作量和不可用原因。
- 会话工作台：继续负责客服实际回复，不复制派单配置。
- 运营分析：当前规则派单单独统计；历史 `model/intelligent/hybrid` 只归为“历史兼容”，不能作为当前可配置能力。

权限继续来自全局权限管理：

- 页面入口：`conversation.handover`。
- 查看：`conversation.view`。
- 派发：`conversation.assign`。
- 转派：`conversation.transfer`。
- 释放：`conversation.recycle`。
- 客服组模式：`agentTeam.update`。
- 排班：`agentTeamSchedule.*`。

页面隐藏不代替 Handler 权限和 Service 数据范围。公司主管看本租户，组长只管理负责综合组，客服只接收并处理自己的会话。

客服组长、租户管理员和平台管理员在切入有效租户后，可以从派单工作台点击“我来接管”，也可以从会话工作台直接接管待总部人工会话。接管必须同时满足 `conversation.view`、`conversation.send`、`conversation.assign`，会话仍为待接入且未分配，路由为 `HQ_AGENTDESK_PENDING`、`NeedHumanFollowUp=true`，并且只能解析出一个启用的综合客服组。组长只能接管自己负责的组；管理员可管理当前租户全部组。主管接管不要求创建 `AgentProfile`，成功后当前指派关系本身即赋予会话可见和回复资格。

直接接管在同一事务内先通过会话状态条件更新争抢唯一处理权，再写 Assignment、事件和 RouteState。两个主管并发操作时只允许一个成功，失败方不得留下活动或历史脏 Assignment。

权限管理中的上述编码直接指向现有 `/conversation-dispatch/*` 契约；默认客服组长具备派发和转派权限。派单工作台通过仅授予 `conversation.handover` 用户的租户派单 WebSocket 主题刷新，普通客服不会订阅该主题；隐藏标签页每 60 秒低频补偿，重新可见时立即刷新。

派单工作台是实时运营面板，不是历史报表：待派发和处理中任务属于当前操作集；已完成任务只保留最近 24 小时用于当班复看与交接。完整历史进入“会话记录”和“运营分析”。列表批量读取路由、当前 Assignment、当前轮首响、客服、门店和企微实例，避免按任务逐行查询；当前任务和近 24 小时完成任务都设置数据库扫描上限，达到保护阈值会记录服务告警。

## 13. 排班运营建议

真实排班应遵循以下方式：

1. 综合客服组负责业务范围，小组负责白班、晚班、机动等人员组合。
2. 用批量排班生成周期班次，支持一天多个不重叠时段和跨午夜班次。
3. 请假使用班次排除，不为一次请假修改长期小组成员。
4. 临时替班使用班次加入，替班人仍必须属于同一综合客服组。
5. 保存前查看可接单人数和总并发；零人禁止保存，一人明确提示单点风险。
6. 白班/晚班交接可让两个不同小组短时间重叠值班；系统会合并两个窗口并按同一公平规则派发。同一小组重复班次以及任何全组班次与其他班次重叠仍被拒绝，避免重复表达同一候选范围。
7. 排班开始即表示进入自动接单责任范围；Presence 只用于主管了解客服当前状态，不暂停派单。
8. 高峰期先提高实际值班人数，不能只机械提高单人最大并发。

## 14. 数据兼容与迁移

DDL 继续由 AutoMigrate 完成，兼容 SQLite 和 MySQL：

- `AgentTeamSchedule.IncludedAgentProfileIDs`：本班临时加入。
- `AgentTeamSchedule.ExcludedAgentProfileIDs`：本班请假/临时排除。
- `Conversation.DispatchWeight` 和 `ConversationAssignment.WorkloadWeight`：规则工作量快照。
- `ConversationAssignment.DispatchMode`：新记录只写 `manual/rule`。

`AgentProfile.ServiceStatus`、`ReceiveOfflineMessage` 和 `LastStatusAt` 已从运行模型、DTO 和页面删除。在线状态统一由 `AgentPresenceSession` 表达，但仅用于展示和分析；数据库历史列由 AutoMigrate 保留，不再读写。

Migration 64：

- 把历史 `AgentTeam.DispatchMode=intelligent` 转为 `rule`。
- 软删除用途为 `dispatch_decision_llm` 的租户模型设置。
- 对最大并发小于等于 0 的档案关闭自动接单，避免“配置开启但永远不可派”的假状态。

Migration 65：同步现有客服角色的会话查看/回复权限，更新派单权限的现行 API 路径，并为默认客服组长补齐派发/转派权限。

历史 `ConversationAssignment.DecisionConfidence`、历史 `DispatchMode=intelligent`、旧 DecisionLog 和旧模型 usage 作为审计事实保留，不物理篡改。新运行链路不写置信度，Analytics 回填只在旧值大于 0 时保留历史快照。

## 15. 失败策略与回滚

- 无唯一客服组：留在待派池，通知公司主管。
- 无班次/全部满载：留在原综合组，达到 Queue SLA 后通知组长。
- 候选在提交前变化：事务拒绝旧决策，并重新调度。
- 服务重启：30 秒扫描恢复待派任务、未首响任务和追问超时后的硬失效任务。
- 需要停止自动派单：把客服组改为 `manual`，无需修改会话、AI 或计费配置。
- 回滚应用代码不能恢复 Migration 64 已软删除的模型用途；如确有历史取证需要，直接读取旧 Assignment、DecisionLog 和 usage，不恢复运行能力。

## 16. 明确不做

- 不调用模型选择客服，不新增派单提示词、模型用途或派单计费。
- 不让规则绕过租户、权限、排班、容量和服务范围。
- 不按自由文本随意猜测客服技能或任务复杂度。
- 不因负载、忙碌、容量或更优候选抢走已回复会话；只在客户追问超时且原客服硬不可用时在原综合组内有界接力。
- 不创建独立派单任务表、小组任务池或平行权限体系。
- 不以 WebSocket 在线、忙碌或休息状态暂停排班责任；Presence 仍保留为运营事实。

## 17. 验证

核心自动化覆盖：

- 多租户队列轮转、SLA 老化和安全优先级。
- 客服组有界扫描、明确企微/门店路由优先和实时工作台近 24 小时完成窗口。
- 规则公平带、本班债务、连续性平局和容量归一化。
- 排班小组、跨午夜、多时段、替班、请假、零覆盖和单点风险。
- User/Profile/Permission/排班/范围/容量硬约束及事务内复核。
- 高频触发防抖、组队列消化和 30 秒补偿。
- 首响前恢复、追问超时硬失效接力、90 秒冷却、三次上限和人工通知。
- Queue SLA 与 First Response SLA，且不读取路由 `ManualExpireAt`。
- 人工派发、转派、释放的独立权限和必填理由；人工指定离线或休息客服仍可成功。
- 组长无客服档案接管本组、管理员接管、跨组拒绝、错误路由拒绝、接管后可见可回复及并发单赢家。
- Migration 64/65 幂等及历史审计保留。

提交前至少执行：

```bash
go test ./internal/services -run 'TestRule|TestAutomaticDispatch|TestDispatch|TestConversationDispatchWorkbench|TestAgentDeskHandoffNotification|TestAgentTeamSchedule|TestWsPresence' -count=1
go test ./internal/migration -run 'TestRetireModelDispatch|TestSyncAgentReplyPermission' -count=1
go test ./... -count=1
go vet ./...
cd web && pnpm typecheck
cd web && pnpm lint
cd web && node --test $(rg --files -g '*.test.mjs')
cd web && pnpm build
git diff --check
```
