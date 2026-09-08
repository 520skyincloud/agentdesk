# 服务知识优先与发送关联修复

## 2026-09-08 用户原意澄清与验收纠正

用户确认消息18060的“纸币”是笔误，实际需求是纸笔/草稿纸，本轮不处理现金场景。
下文9月7日记录保留当时观察，但第四、第五轮“选错主体”的结论撤回：
会话2116、2117均完成五题检索，按知识回答前四项及纸笔/草稿纸不提供，
符合用户现已确认的意思；不能继续以现金转接或澄清作为通过条件。
此前真实存在的合题、服务字段误杀、澄清被写成断言等问题及修复记录仍然有效。

仅撤销3120e22新增的防错字主体替换限制及对应断言，恢复原有主体一致性规则，
保留问题覆盖、有界修复、澄清模式、类别存在性与转接事项归属改动。
不新增物品特例、模型阶段、数据库/接口/权限/知识/配置变更，不执行Migration。
原始脏工作区不动；customer-audit仍仅有前述三个共享文件的租户分歧，
ai-billing无目标文件分歧，本次两处运行/测试修改无并行冲突，无需整体rebase。
回滚仍只切发布前程序，不回退消息。自动验证沿用下文三个Go包。
剩余真实测试限定同一隔离会话3轮：无标点五问、领取位置回指、
矿泉水数量费用与无法出门送毛巾混合请求；累计不超过8个AI轮次。
不运行30/50轮，不把历史两轮正确答复当作新版本重复性或延迟已验收。
发布与三轮实际结果完成后追加。

### 正常三轮实测及限定收口

`a4e15917c09e981ddea04e63b2b7a28c6412ba9b` 已推送两远端并在服务器部署，
备份 `/opt/backups/agentdesk-20260908-question-coverage-normal` 的数据库gzip和配置SHA已校验。
同一隔离会话2118连续3轮，未新增现金/错字测试：

- 消息18093：无标点五问形成5个独立检索任务；咖啡、剃须刀、牙刷、毛巾提供情况
  和领取位置、纸笔不提供均按知识答复，共3条消息，无误转接，24.287秒。
- 消息18097：回指保留了前轮四个可用对象，但仍合成1个检索任务，回答遗漏毛巾，
  不能算上下文验收通过；11.721秒。
- 消息18099：矿泉水两瓶且免费答全，1315房号被使用，没有二次追问。
  送毛巾实际转接成功，但先重复了客户已无法采用的自取方法，且通知只写
  “能否送到1315房间”遗漏对象，不能算服务答复和通知验收通过；17.301秒。

三轮Judge各1次，Generate各1次，无内部协议泄漏和空回复；没有触发覆盖修复。
程序自动断言因通知不含毛巾而失败，人工检查另外识别出第二轮漏答及第三轮冗余。
测试路由和恢复任务仅针对2118清理，消息保留；pending/failed Outbox为0，
历史7条sending未修改。原始数据位于服务器
`/tmp/agentdesk-question-coverage-normal-20260908.jsonl`，不提交生成报告。

只进一步修改3个现有运行文件及原有测试：
question_coverage.go要求回指对象仍逐项建立检索任务，现有Judge覆盖检查读取
resolvedText中的实际对象集合；保持模型拆题权和一次修复上限，不创建本地拆题器。
knowledge_evidence_judge.go将已有“方案不适用就不作为部分答案”规则前移，
先判断适用性再提取事实，不增加第二次模型或本地语义判断。
intent_human_route.go将待确认方面与其原始问题同时保留，防止同一个“是否送到房间”
丢失物品归属；仅 deferred Task 可进入通知，已回答任务不加入。
回归覆盖同一句来源的多对象回指修复及独立检索、保留通知对象、判断规则优先顺序。
不改知识、房号、数据库、API、模型配置、Outbox和接待状态机，兼容旧调用。
并行分支对本次三个Executor文件无分歧；共享service的既有租户分歧不合入，合并顺序不变。
累计真实模型预算已用满8轮，最后收口只做自动测试及服务健康验证，
未追加真实模型轮次，不得声称最后两项已通过真实模型复验或达到延迟目标。

### 2026-09-08 最终发布身份

程序提交 `6108b44fbecc740badc6515f74d59af4e97070fd` 已推送origin、weibao，
发布 `/opt/agentdesk/releases/20260907-request-answer-6108b44`；
目录日期沿用既有发布脚本，实际切换发生在2026-09-08。
Binary SHA256：`f321e6aabb76dbec5a3cfd0c0ecaf9b37b6a859c8aacf8ade980f6c3d91f14d0`。
Executor、Runtime、Services三个包完整测试通过，耗时分别7.226、0.830、16.934秒。
切换前确认无活跃AI运行，使用已校验备份，SHA核对后原子切换；
systemd active/running，NRestarts=0，8083 HTTP200，检查的近期启动日志无新错误。
无pending/failed Outbox，7条历史sending保持原样；2118无待处理动作或活跃恢复任务。
消息、知识、运行配置保留，未执行Migration或恢复SQL。
最近程序回退点为a4e1591，原生产5a8dcf4 release和本轮备份继续保留；
若切回只切程序，禁止回退消息。最后收口没有真实模型复验，没有企微设备投递验收。
推送后fetch确认三个Executor运行文件无并行新分歧，无需rebase；
后续合并仍须逐块处理customer-audit的三个共享service/runtime文件租户改动。
本轮到此停止代码修改和模型测试，不追加30/50轮。

## 问题覆盖修复开发记录

本轮生产基线为 `5a8dcf42ae0ae176819dcd12263843d373344246`，
开发分支 `codex/intent-source-repair-20260907`，原始脏工作区保持不动。
其风消息18060包含咖啡、剃须刀、牙刷、毛巾、纸币五个目标，Run7876却只有一个
compound_information Task。混合检索被剃须刀和牙刷的近重复候选占据；Judge 只保留
这两类事实，其他三项被当作证据不足而转接。此前单问已召回咖啡和毛巾知识。
本次不以降低分数、补知识、删除校验或改变自助规则掩盖错误合题。

修改范围：
- `question_coverage.go`、`intent_model_detector.go`：问题优先提示与一次有界修复，
  原文来源验证和正确任务保留；不新增本地语义拆题器。
- `knowledge_evidence_judge.go`、`answerability_gate.go`：同次 Judge 对照当前来源，
  仅错误合题、漏题、检索目标偏移触发修复；仅重查变化任务并合并原正确裁决。
- `context_builders.go`、`service.go`：传播修复后的计划，失败不能提交残缺答案。
- `intent_human_route.go`、`reply_trigger_service.go`、两个 handoff service：
  仅沿现有真实转接入口传递待处理事项，使成功通知可追溯到具体问题。
- 对应 Executor 和 Services 测试与当前设计文档。

无 Model/Migration、DTO、HTTP、WebSocket、权限、知识库或运行配置变更。
正常调用次数不变；覆盖异常路径最多额外一次 Intent、局部检索、一次 Judge，
沿用实际调用计费。现有房号收集、转接状态机、员工优先、Outbox 幂等和恢复时长不变。
`noticeSubjects` 是原 pending JSON 中的可选兼容字段，不新增数据库列。

验证命令：
`go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1`。
已通过完整相关包回归；部署后只做少量隔离真实模型代表场景，不运行30轮或50轮。
自动覆盖：合题修复只重查变化题、正确 Task ID/证据保留、一次修复失败后停止、
真资料不足不重试、五对象独立检索、同层近重复预算、遗漏来源和通知事项。
线上结果须追加真实记录，不以模拟输出代替模型验收。

并行分支：`customer-audit` 在 `reply_trigger_service.go` 与两个 handoff service
有租户改动，本轮只扩展兼容可选事项参数，不合入或覆盖租户逻辑；
`ai-billing` 无相关目标文件的新分歧。无需整体 rebase，建议先合并 Executor，
再逐块合并通知参数提交，禁止整文件覆盖。回滚只切回5a8dcf4 release，保留全部消息、
当前知识和运行配置；本轮不执行 Migration。

### 首次覆盖实测与边界修正

`58b87cf`、`92b2f37` 已推送两远端，`92b2f37` 曾部署；
隔离会话2113、消息18066原始五问已拆为五题，咖啡、剃须刀、牙刷、毛巾均完整答出，
并保持三条合并消息。但第五题纸币被标 ambiguous，现有正常化规则将其变为
interaction/clarify，Generate 未经知识验证给出否定答案。此轮验收未通过，已停止，
程序切回5a8dcf4，测试及客户消息均保留。

仅补齐同类语义边界：问题可理解但答案未知不能标为问题有歧义；
Judge 的覆盖输入带任务实际知识开关，错误降为互动从而跳过检索也属于检索目标偏移。
仍由模型判断，不增加本地词表或修改正常化、知识和 Generate。
累计实测预算仍控制在8个 AI 轮次内，不重新运行长会话。

`4f57def` 第二轮会话2114、消息18071真实触发一次覆盖修复，修复后五对象独立检索；
纸币正确进入单独接待，通知明确标出纸币。四条用品知识因 Judge 在 hotel_info 上
附带 hasUsableSelfService=true，被旧解码规则判 self_service_without_service_evidence，
进入旧安全短答，验收未通过。停止并切回5a8dcf4。
限定修正该字段的适用范围：非 service_request 不消费此路由标志，也不因此否决合法
知识；service_request 仍保留缺失字段、无证据自助和转接混入事实的校验。
新增完整问答保留和真实服务字段仍生效的回归，不增删候选或事实。

`c341fa6` 会话2115、消息18077四类用品知识完整保留，但纸币澄清任务仍被 Generate
写成无证据否定，毛巾被 Judge 追加未询问的“普通日常毛巾”条件而误转接。第三轮
仍未通过，程序切回5a8dcf4。定位不是检索或合并丢失：逐题输出契约只保留文本/事实，
未明确携带澄清任务模式；Judge 在类别存在性判断中扩大了当前请求。
必要范围补充 `multi_reply_output.go`：从已有 interaction/clarify 传递只追问模式，
不新建任务、不改解析或新增文本语义校验。Judge 前移并明确原有类别存在性规则，
不引入物品词表；客户明确指定的类型、用途或条件仍必须保留。剩余实测最多5个 AI
轮次，允许真实歧义澄清，不强迫所有无知识问题立即转接，也不允许无证据肯否。

`7522b15` 会话2116、消息18083：五题均单独检索，四类用品完整可答且未误转毛巾；
但“纸币”错误选择了“我需要纸和笔，酒店有吗”的候选。检索日志8136原文已核实，
这是不同主体的证据选择，不是知识库明确否定提供纸币。第四轮未通过并停止，
程序切回5a8dcf4。仅修正 Judge 既有主体一致性段落，禁止因音近、字近或候选更常见
而擅自纠正客户对象；真实已确认的别名、上下文纠正仍有效，不添加字符匹配拦截。
剩余4个 AI 轮次验证最终版本；类别问题允许按实际知识回答具体种类，而不是强迫
回复重复客户的上位名词。

### 本轮最终状态：未通过线上验收

最终修复代码提交 `3120e227d3f8035d92c7d19d57d6fc15efa68efc` 已推送 origin、
weibao，自动测试三个相关包全部通过，但不能据此认定业务问题已解决。
第五次隔离实测会话2117、消息18088仍将“纸币”选成“草稿纸”的知识，
故停止剩余测试。代码路径已确认：运行解码采用 protocol-only，DecisionSource=model
的选择不被 exact FAQ 救援或指导事实重建覆盖。现有日志只保存解析后裁决和候选，
没有原始 Judge 响应全文，不把复原分析当作原始响应证据。

累计只发送5个 AI 客户轮次，每个均使用线上程序和真实模型、当前知识库。
没有一轮通过完整五目标验收：前四项逐渐恢复完整答复，但第五项有误解或误选。
第2轮触发过一次覆盖修复，使用2次 Judge；其他4轮各1次 Judge，5轮 Generate
Trace 均为1次。不能将这些记录表述为“全部通过”或证明同一输入已稳定。
无标点、后续上下文、多目标房间服务等剩余代表场景因即停规则没有执行，
不列为真实模型通过。未执行30轮、50轮或企微设备最终投递测试。

当前生产已切回并核实：
- Release：`/opt/agentdesk/releases/20260907-request-answer-5a8dcf4`
- Commit：`5a8dcf42ae0ae176819dcd12263843d373344246`
- Binary SHA256：`e1dbf323fe1bd1a7256e9e65e574d3f38ce9d5c04e76e792b5ebd0cce7a9c11d`
- systemd active/running，NRestarts=0，8083 HTTP 200。
- pending/failed Outbox 为0，原7条历史 sending 保持原样。
- 测试会话2113至2117路由均已清理为 AI_SERVING，待处理动作为空，
  活跃人工恢复任务为0；消息保留，未改真实客户的接待状态。

备份位于 `/opt/backups/agentdesk-20260907-question-coverage-92b2f37`，
数据库gzip与SHA已验证。所有回滚只切换程序，不恢复旧数据库或运行配置。
各次只保留新 release 与代码供审查，不把未通过版本继续留在生产。
真实测试是 ChannelID=0 的服务器隔离会话，不等同于企微手机端投递验收。
后续应先处理 Judge 对语义相近但主体不同候选的误选，不继续追加物品词表、
扩大本地语义拦截或反复用原输入赌通过。本轮到此停止产品代码和模型测试。
并行分支边界仍为前述三个共享 service/runtime 文件，无新增契约冲突；
保留独立修复分支以便审查和选择性合并，不要求回退其他开发分支。

## 范围与基线

生产基线 c42cb8447f82917f371226f44cf1c9b764756237。
独立分支 codex/service-knowledge-handoff-20260907，原 customer-audit 脏工作区不修改。
目标：同目标自助知识先答；部分未知不直接等价于必须接待；
真实企微空 requestID 的答案保护、转接顺序、发送回显归属正确。

## 文件与契约

- knowledge_evidence_judge.go、answerability_gate.go：一次 Judge 增加内部
  hasUsableSelfService，保留 partial 的真实未知，不增加本地语义裁判。
- trace_callback.go：仅配套内部诊断字段，不增加客户敏感文本。
- reply_trigger_service.go：按 Commit 实际消息 ID 保护本轮回复。
- channel_message_outbox_service.go：复用 payload，记录稳定转接通知 ID；
  旧 requestID 方法保留兼容，真实员工优先规则保持。
- wxwork_protocol_service.go：记录真实外部消息 ID 后再结算 Outbox，
  在途同会话员工回显等待关联后去重；不按同文判断人工。
- 配套测试：用品类知识与边界、知识层级、协议异常、三条答案顺序、
  空 requestID、发送中转接、提前回显和真实员工同文回复。

无 Model/Migration、DTO、HTTP、WebSocket、权限或计费变化。
不改 Intent、检索阈值、知识库、模型、人设、房号流程或人工恢复时长。
仅内部 Trace 和 Outbox JSON 增加兼容字段。
协议依据为 wework.apifox.cn 的发送文本 api-276644016.md、
接收消息 doc-7013959.md；使用既有 conversation_id 与真实返回 data.msg_data.id，
不添加未文档化的透传字段。

## 验证与并行开发

命令：go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1。
2026-09-07 首次完整回归通过；发布前仍须对最终提交再次验证。
前端复用既有构建产物，index.html SHA256 与当前服务器一致，未修改前端代码。
运行时外部协议/模型不可原子提交；发送结果未知仍不得盲目重发。
在途 echo 等待已有发送调用完成，非全局等待；不改变客户消息的业务处理入口。

customer-audit 与 reply_trigger_service.go、channel_message_outbox_service.go、
wxwork_protocol_service.go 有同文件分歧，禁止整文件覆盖。
ai-billing 未发现上述目标文件分歧。不在此修复分支 rebase 整个审计分支；
建议独立 cherry-pick 知识语义提交，再合并发送关联提交，逐块核对。

## 发布与回滚

提交按知识判断、发送关联拆分。发布前备份数据库、shared 配置、当前 release 身份，
构建 Linux amd64，校验 SHA256 后切换 release。不执行 Migration。
小范围真实模型验收上限 10 轮；真实外部投递必须与 ChannelID=0 隔离评测分开记录，
未做外部投递时不得声称已完成该验收。
回滚只切换发布前 release；不回退任何消息或会话数据。
本文件初始记录代码级验证，提交、部署、模型及出站结果以发布后追加记录为准。

## 首次真实验证发现与收口

dc4619a 首轮隔离会话 2103、消息17931未通过：
Judge 漏返回 hasUsableSelfService，门店层记录 missing_service_resolution，
随后原层级兜底选择通用转接。已停止后续轮次，程序切回 c42cb84，消息保留。
修复仅补齐最终 JSON 示例的必填字段，并禁止门店协议异常回落到通用转接；
不放松协议校验，不推测字段真假，不重跑整套长对话。
后续真实验证总预算仍含本次失败轮次，不超过10轮。

第二次隔离验证会话2104已发4轮：行李、拖鞋、牙刷加矿泉水正常回答；
毛巾知识返回 direct_combined，同时列出1项 missingAspects，触发
direct_combined_cardinality。发现后停止脚本，保留消息并切回 c42cb84。
补充8行运行代码：仅服务任务在明确返回自助判断、合法候选数量且存在未知项时，
将 direct 标签保守归一为 partial；不修改事实、答案、自助真假或未知项，
不放过错误 Candidate、跨层、重复项和无证据。另补提示词明确该字段关系。
三个相关包最终再次全通过。累计已发5轮，最终验证最多剩余5轮。
脚本同时增加协议错误/知识防线兜底即停检查，避免“有回复但未回答”被当作通过。
并行分支冲突范围未扩大，仍只有前述三个 service/runtime 文件与 customer-audit 重叠。

第三次隔离验证会话2105首轮遇到旧解码器拒绝额外 intent 字段：
json: unknown field "intent"。已即停并切回基线。
同一 Judge 文件让运行入口按既有类型只读取约定字段，
额外说明不会存入裁决或回复；旧非运行入口保留严格顶层解码行为。
必填字段、类型、候选、层级和尾随内容校验保留。
累计已发6轮，剩余验收最多4轮，不追加模型阶段或扩大至其他解析器。

## 最终发布与验收

2026-09-07 最终程序提交 d2ef4e308901e4191fc8e399574b2ffa8f2ce0b2，
已推送 origin、weibao。当前 release：
`/opt/agentdesk/releases/20260907-service-knowledge-d2ef4e3`。
二进制 SHA256：`eda63d5bfa82adaee8794ca5b653740d42f0953dd46fd4e3e4f1c2f022aa193f`。
备份：`/opt/backups/agentdesk-20260907-service-knowledge`，数据库 gzip 与 SHA 已校验。
程序回滚点仍为 `20260906-101045-bounded-reply-c42cb84`，不恢复旧数据库或配置。

最终版本隔离会话2106连续4轮通过：

- 17947：请求送毛巾，回答压缩毛巾/面巾自取地点和送房未知边界，保持 AI_SERVING。
- 17949：接受自行领取后问行李和矿泉水，按顺序回答寄存柜及两瓶矿泉水，无转接。
- 17952：明确1315房间且无法出门，知识说明后发送一次“帮您转接到同事了”，
  状态 STORE_WECOM_MANUAL，pending_action 为空。
- 17955：取消接待后恢复 AI_SERVING，不再等待人工确认。

前三轮运行耗时分别10980/13258/11657毫秒，各一次 Generate；取消走原接待取消入口。
最终三个相关包全通过；发送与回显专项 race 测试通过。
全过程共10个已发客户轮次：早期3轮协议失败已记录并修正，其余7轮通过；
不能将本次结果描述为“10/10一次性全部通过”。

发布后8083返回200，systemd active/running，NRestarts=0。
pending/failed 和近期 sending 合计0；原有7条历史 sending 未改动、未重发。
隔离会话路由和恢复任务已清理，消息记录保留。
ChannelID=0 验证的是服务器真实回复、知识与路由链路，不代表企微手机端投递验收。
没有可用的已批准隔离企微外部联系人，外部投递及回显竞态仅通过官方格式模拟测试，
尚未用真实企微收件设备完成验证；不触碰真实客户会话进行出站测试。

本次未修改数据库结构、知识库、Intent、人设、模型配置或计费。
customer-audit 同文件冲突仍限前述三个文件，ai-billing 无目标文件冲突；
继续按独立修复提交逐块合并，不覆盖其他分支。此记录为文档追加，不需要重建已验收二进制。

## 需求类答复精简后续修复

基线程序 d2ef4e3。本轮只修改 knowledge_evidence_judge.go 的提示词和最终 JSON 示例，
以及 service_self_help_test.go 和本节文档，不修改判断代码或增加协议字段。
目标是同目标适用知识直接回答；明确不提供的政策作为完整答复；
内部未知不自动变成客户可见声明，未触发的特殊条件不触发人工接待。
已拒绝自助、确实尝试失败、明确人工指令和门店精确转接仍沿用现有流程。

无 Model/Migration、数据、接口、DTO、权限、WebSocket、配置或计费变化。
不改 Intent、检索、Generate、房号、Outbox、转接及恢复状态机。
customer-audit 与 ai-billing 均无本轮目标文件分歧，无需 rebase；独立提交可单独合并。
自动验证：go test -p=1 ./internal/ai/runtime/executor -count=1。
单元测试冻结召回，验证用品、入住、寄存、停车及否定政策的裁决到输出契约；
模型语义效果另由服务器原消息入口隔离验证，最多6个客户轮次，不以模拟输出代替实测。
发布回滚仅切回 d2ef4e3 release，不恢复数据库、知识库或配置。

ab5ecd0 版本首批6轮验证：咖啡、否定政策、入住、寄存加停车均按知识答复，
没有冗余能力声明或误转接；取消恢复正常。
第5轮“已无法出房间”虽然转接成功，但正文仍复述了不可用的自取方法，不能算表达完全通过。
补充仅一条同类规则：仅剩当前已不可用的办法或相关背景时，不以 partial 强留正文，
由 Judge 判 insufficient 进入既有接待；其他独立问题不受影响。
因此额外只复核该边界1轮，本轮真实客户输入总上限调整为7轮，不重跑完整6轮。

## 需求类答复精简发布结果

2026-09-07 最终程序提交 `a965b39a9e505ad1d9cd0714ebb53524782b7753`，
已推送 origin、weibao，当前 release 为
`/opt/agentdesk/releases/20260907-request-answer-a965b39`。
二进制 SHA256：`98fb08fc2ff51a7a22f424b195ce5fd81305f7e3280ad5372dc4c4d8145794ca`。
备份位于 `/opt/backups/agentdesk-20260907-request-answer`，
数据库 gzip 与备份 SHA 已校验；回滚仅切回 d2ef4e3 程序，不恢复数据或配置。

首批隔离会话2107共6轮：咖啡直接给自取方式；床单按不提供额外床单的政策回答，
不附加送房未知、特殊通融或房号追问；入住流程、寄存加停车顺序答复、取消恢复正常。
第5轮已不能出门时仍复述自取方法的问题已记录并修正，不能描述为首批全部通过。
最终会话2108只复核该边界1轮：消息18003明确1315房间且无法出门，
Judge 判 insufficient，消息18004只发送一次“帮您转接到同事了”，
路由 STORE_WECOM_MANUAL、pending_action 为空，耗时9.125秒。
累计7个真实客户输入；未重跑整套测试，未运行30轮或50轮。

最终 Executor 包测试通过，8083返回200，systemd active/running，NRestarts=0；
检查时无 pending/failed 或近期 sending，原7条历史 sending 保留且未重发。
两个隔离会话只清理测试路由与恢复任务，保留消息；未向真实客户发送测试。
ChannelID=0 验证服务器真实模型、知识和接待链路，不代表企微收件设备投递验收。
本轮仅一个运行文件的提示词发生变化，其余为对应测试及文档；
未改 Intent、检索、Generate、房号、人工状态机、Outbox、数据库、知识库或计费。
推送后再次 fetch，customer-audit 与 ai-billing 无本轮四个文件的新分歧，
无需 rebase；文档记录提交可独立合并，无需重新构建已验收程序。

## 长消息 Intent 来源协议修复

基线程序 a965b39；其风消息18021在同消息内部上下文校验失败，
18023及18024因问号来源被检索清洗为空而失败，均未进入检索和 Judge。
本轮只改 intent_protocol_validation.go、intent_model_detector.go：
允许同一物理消息内由模型补全上下文，保留跨消息来源及跨轮历史检查；
先按原文核验来源，使符号消息合法；一次协议修复携带首次 JSON 和错误，
修复后检查题数及原先合法任务，禁止以删题或改掉合法任务来通过。
对应测试在 intent_protocol_validation_test.go、intent_pipeline_test.go。

不增加模型调用次数，不修改模型拆题权、检索、Judge、Generate、房号、人工状态机、
Outbox、知识库、计费、数据库、DTO、接口、权限或 WebSocket；不执行 Migration。
生产 Intent Profile 不改，兼容规则随现有运行提示注入，回滚只需切换程序。
验证命令 go test -p=1 ./internal/ai/runtime/executor -count=1 已通过。
上线隔离实测最多5次客户输入，检查截图原文、问号加重发、上下文追问和新主题；
所有分题与答复须核对，不把仅有安全兜底回复视为通过。
customer-audit、ai-billing 对目标文件无新分歧，无需 rebase，可独立合并；
原始脏工作区不动。回滚点为 20260907-request-answer-a965b39，
保留当前消息、配置和知识库，发布结果在完成后追加。

### 发布及验收未通过项

程序提交 `bf4f8acbb949f6bba0a34aee0e790c8e9044ff3a` 已推送两个远端，
部署到 `/opt/agentdesk/releases/20260907-request-answer-bf4f8ac`。
二进制 SHA256：`0204abd29d0f3ad5d8c073a499385189a3f3c0e113919d026411b577dc86faed`。
备份 `/opt/backups/agentdesk-20260907-intent-source` 的数据库 gzip、配置及 SHA 已验证。

会话2109、消息18027：截图原文由模型拆成6题，6题均选择门店知识并按序回复，
合并3条消息；电车 Task 使用 U1 内部上下文，来源协议通过，耗时30.762秒。
会话2110、消息18032/18033：实际先收到问号、后收到原文，Trace 确认 U1/U2，
来源协议通过，模型保留6个业务题及1个问号互动，没有丢题或触发 Intent 安全兜底。
但整体回答未通过，耗时31.436秒，已停止剩余测试，累计仅3次客户输入：

- 问号被 Intent 归 interaction/clarify + unresolved，现有任务整理未将其转成
  context_only，产生多余的“您具体想问哪方面呀”。
- 餐饮、游玩均被 Judge 选为混合知识，answerText 各自包含两个主题，形成重复内容。
- 停车 answerText 未覆盖 Judge 自己返回的全部 criticalValues，触发事实兜底；
  发票 criticalValues 为“增值税电子普或专票”，而 answerText/statement 为
  “增值税电子普票或专票”，逐字校验不通过，最终该题变成无法准确回答。

不得把本轮描述为完整验收通过；后续追问及新主题的两次模型输入未执行。
暂不扩改 intent_pipeline.go、knowledge_evidence_judge.go 或 multi_reply_output.go，
需要额外确认互动归属与 Judge 答复/关键值契约的最小修复范围。
当前保留已验证有效的入口修复；8083返回200，systemd active/running、NRestarts=0，
无 pending/failed 或近期 sending，7条历史 sending 保持原样。
隔离会话只清理测试路由，保留消息；未向真实企微客户发送测试。
ChannelID=0 只验证服务器链路，不代表企微手机端投递验收。

## 逐题答复最小修复

基线程序 bf4f8ac，继续使用独立修复工作树，不修改原 customer-audit 脏工作区。
目标：完整问题到达后消解多余澄清；混合 FAQ 分题取用；
不再因关键值标注与事实不一致而丢掉完整答案；单题异常不覆盖其他已校验回复。

运行文件严格限定 intent_model_detector.go、knowledge_evidence_judge.go、
multi_reply_output.go、generate_recovery.go。前两者整理现有提示及记录标注异常；
后两者在 Task 内恢复已选事实，并在本次 Generate 恢复中保留已校验的兄弟答复。
标注错误必须以完整 Statement 保真恢复，不删除数量、金额、条件或凭据来通过校验。
没有新增模型阶段、持久化已答状态、数据库/模型、Migration、DTO、接口、WebSocket、
权限、计费或运行配置；不改检索、知识库、房号、转人工、Outbox。
错误及恢复日志只记 Task/Fact ID 和类型，不新增客户敏感全文。

对应测试为 per_task_reply_recovery_test.go，加上两项旧整批兜底断言的语义更新。
自动验证 go test -p=1 ./internal/ai/runtime/executor -count=1 已通过；
新测试在修改前已复现发票/数量/金额/凭据标注失败、六题整批失败和正确互动被兜底覆盖。
真实模型最多5次客户输入，逐题核验六问、符号加重发、上下文追问、新主题；
不运行30/50轮。单元测试不代表模型语义或企微手机投递验收。

开始时 fetch 后 customer-audit、ai-billing 对四个目标运行文件均无新分歧，无需 rebase；
独立提交可 cherry-pick，文档随后合并，不修改共享字段或状态语义。
备份当前数据库、shared 配置与 release 身份后原子部署，回滚点为
20260907-request-answer-bf4f8ac；只切程序，不回退消息和配置。
部署和真实验收结果待完成后追加，不能以已发送兜底话作为问题解决。

### 逐题修复发布与实际验收

2026-09-07 程序提交 `5a8dcf42ae0ae176819dcd12263843d373344246`，
已推送 origin、weibao，并部署到
`/opt/agentdesk/releases/20260907-request-answer-5a8dcf4`，未回滚。
二进制 SHA256：`e1dbf323fe1bd1a7256e9e65e574d3f38ce9d5c04e76e792b5ebd0cce7a9c11d`。
数据库和 shared 配置备份 `/opt/backups/agentdesk-20260907-task-answer` 已校验，
回滚点仍为 bf4f8ac。前端首页 SHA 与发布前相同，没有重建或修改前端。

隔离会话2111、2112共5次客户输入、4次AI业务轮次，严格未追加测试：

- 18038：六题完整按序回复18039至18041，三条消息，28.684秒。
- 18043/18044：实际问号加长文连续到达，只保留六个业务回复，
  回复18045至18047，无多余澄清或原来的整段重复，27.290秒。
- 18048：发票时效追问，回复18049保留1至3个工作日和下载步骤，9.637秒。
- 18050：切换矿泉水主题，回复18051保留两瓶、免费，不继承发票，9.621秒。

14个业务Task均有正文，无空回复、错误转接、通用失败短答或内部协议泄漏；
四轮Generate均为一次，没有触发整批事实兜底。自动检查通过不等于完整人工验收：
首轮游玩答复仍夹带菜名及景点背景，第二轮仍有过多景点介绍；首轮停车也多带导航细节。
这说明“最小必要答案”仍不稳定，不能宣称回答范围和简短性已全部解决。
长消息仍为27至29秒，Judge分别占14.559、13.625秒，Generate占1.967、1.689秒；
其余耗时未在本次继续扩查，不把全部延迟归因于Judge，也不声称达到15秒目标。
保留已解决的逐题闭环改动，未追加提示词或第6次模型输入。

发布后8083为200、systemd active/running、NRestarts=0，无pending/failed Outbox，
原7条历史sending保留。隔离路由及恢复任务已清理，消息保留。
结果原始文件 `/tmp/agentdesk-task-answer-live-20260907.jsonl` 位于服务器，
不提交生成报告。ChannelID=0验证服务器真实模型链路，不代表企微收件设备投递验收。
推送后fetch复核两个并行分支，四个运行文件无新同文件分歧，无需rebase。
