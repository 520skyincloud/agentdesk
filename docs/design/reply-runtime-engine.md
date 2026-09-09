# Reply Runtime Engine

## 当前发布：2026-09-09 3c38150

当前程序3c38150已提交、推送两个远端并部署，hotel Profile应用原计划11处说明。
在同一隔离会话2127的10输入诊断之后，仅追加5输入复验，5/5内容与任务验收通过：
重复外卖咨询不再带入其他商品背景；两次地址与机器人均初次拆为两个Task，
无覆盖修复；天气、人物常识和配色三问逐题回答。无空答或内部协议泄漏。
四轮知识问题各记录一次Judge，五轮Generate均attemptCount=1；天气使用原有
工具调用过程，不将其当作无工具单次模型请求。单题服务器耗时9.218/9.651秒，
双题12.031/11.755秒，天气三问10.344秒；样本不足以证明整体P90或长期稳定率。
测试客户使用已有账号级关闭自动转接设置，ChannelID=0，未产生外部Outbox；
这不等于真实企微收件设备投递或转接通知验收。该项仍等待获准的收件人和通知范围，
因此整体目标尚未完成。原失败记录、回滚边界和备份位置详见开发记录。
下列18:50及更早段落均为历史过程，不代表当前生产已回滚。

## 2026-09-09 恢复后复验与协议收口

FastGPT真实检索恢复后，18:50再次部署418c535和11处Profile说明，
会话2127连续10输入已完成，但不能宣称通过：第2轮合题被Judge检出并修复，
第9轮带入无关售卖柜背景，第10轮因Judge未返回schemaVersion而空答；
颜色建议也未给出具体颜色，脚本被另一题的“李白”误判。已回切5b8ca94及旧Profile。
本轮补正仅涉及现有四个Executor运行文件：Intent最后重申逐题目标；Judge使用
包含schemaVersion、coverage和tasks的同一根对象示例，优先直接适用的重复方法；
运行解析兼容缺失冗余版本标记，但显式未知版本、Task、知识层、候选和覆盖纪律不变；
Generate明确的常识及建议任务必须回答本题方面，不用相邻工具结果代替。
不增加模型调用、语义拦截、数据字段或全局配置。真实复验结果见开发记录。

## 2026-09-09 最新发布验收状态

17:16（Asia/Shanghai）存储恢复后成功部署418c535并条件更新11处Profile说明。
隔离会话2126第一条输入的Intent正确，但门店库和通用库检索均收到FastGPT
6080端口EOF，Judge/Generate未执行，进入既有source_unavailable接待兜底。
只执行1条输入，0条验收通过；不能据此评价Judge语义或宣称10条验收完成。
按未通过验收的发布边界撤回程序及本次Profile变更。17:23核验当前为5b8ca94、
原Profile已恢复、8083返回200、NRestarts=0；全部消息与知识库保留。
当前阻断是知识服务连接，早先的存储启动阻塞已解除。没有为此改写提示词或知识。
ChannelID=0隔离了客户渠道，但转接仍会产生门店通知，不代表完全隔离外部发送；
后续真实客户投递及转接通知验收仍需获准的测试接收范围。
以下418c535规则为待验收代码，不是已验证的生产行为；详细证据见开发记录。

## 2026-09-09 独立答案目标与上下文口径收口

本轮仍由Intent模型拆题；JSON示例先输出intentTasks，每题先写原话、补全及来源，
再分类，最后给顶层汇总，字段集合不变。不同对象或办理结果不能因同话题合并；
同一对象紧密的数量/费用及整体比较交集保留，不按标点拆题。
现有Judge覆盖核对先输出coverage，再裁决各Task证据；检查错误合题不依赖
objective标签，direct_combined不能证明Task划分正确。仍只复用既有一次有界修复，
保留无关任务ID、查询和证据，本地不新增语义判断。
完整独立问题直接保留原话，单题/多题同规则；指代从已提供的有界历史选择唯一相关
对象，新主题不继承旧对象，不重新执行历史已答题。clarification_answer及
answer_rejected仍分别保留紧邻追问和紧邻AI答复边界。
同步生产Profile对应段落和输出示例，不覆盖其他定制；部署验收状态见开发记录。

## 此前首次发布验收：2026-09-09 5b8ca94

当时运行/opt/agentdesk/releases/20260907-request-answer-5b8ca94，首次发布未回滚。
本轮只改Judge提示，现有Profile和配置未写入；两个既有Go包回归通过。
隔离2125两次输入：外卖原问在与前次失败完全相同的十条召回上，改为直接回答
美团下单办法；追问地址及机器人时，两项均按现有知识回答，无兜底或转人工。
但第二条Intent将两个答案目标合为一个Task，未满足两个独立Task的验收要求；
因此只确认本次答案完整，不宣称拆题稳定性、逐Task隔离实测或全场景均通过。
保留失败断言和原始记录，不修改Intent、放宽生产协议或追加模型测试来通过验收。
两次完整用时11.727/12.733秒，不能据此宣称延迟目标或重复稳定性已达标。
发布身份、知识证据、验证限制及回滚点见开发记录。

## 2026-09-09 直接答案与相关背景的选择

本轮只调整现有 Judge 提示，不增加模型、字段或本地语义判断。
每个知识层先比较全部候选对当前目标的覆盖，直接办理建议优先于相关设施背景；
混合 FAQ 内的独立方法可以使用，保留其明示条件，不附带无关对象的否定。
客户只问设施是否存在时，存在性本身就是直接答案；申请、打印、领取等不同动作
不得相互替代。确实没有完整答案时仍保留适用部分知识和现有接待边界。
输出示例先给 answerText，再给证据及完整性判断，字段及解析契约不变。
不能把 mock 选择或相关词命中当作语义验收通过，真实结果以上述发布状态为准。

## 上次发布记录：2026-09-08 52b44f1

当时运行/opt/agentdesk/releases/20260907-request-answer-52b44f1，
hotel Profile仅同步常识、互动和短反馈三个说明段。下文各次发布/回退说明为历史记录。
天气工具中间事件不再触发最终输出协议错误；最新三问实测已按顺序回答天气、
人物常识和穿衣建议，无兜底、泄漏或误转接。
外卖原问已不再回答本子或转人工，但Judge仍只选机器人存在性，并保留
“酒店是否支持点外卖”缺失项。这是部分答复，不是完整直答验收通过。
不能根据临时脚本的passedInputs=2宣称全部语义已通过或全场景稳定。
完整证据、验证边界与回滚点见开发记录。

## 2026-09-08 完整问题检索稳定性补正

本节为6a3e036首轮失败后的补正，发布和实测结果以开发记录为准。
完整独立问题直接使用当前Task原话，忽略模型另选的较短evidenceQuery；
保留现有标点清理，不把多个Task重新拼成一个查询。回指仍使用补全文本或
来源合法的摘取，外部代操作查询及旧Profile兼容逻辑保持不变。
这是查询选择而非新增可回答性门槛，不增加检索次数或语义模型。
Judge从混合FAQ选择独立操作建议时，不把标题对象自动继承为建议的限制；
明示条件仍须保留，不复制无关否定结论，也不据此承诺配送或执行能力。
“完整事实”不是原文整句，逗号两侧对象不同可以只取当前所需的分句，
不能从选中FAQ反向补入无关结论。每个Task输入先给候选，最后重申当前问题，
内部字段不增减，不对模型结果增加本地语义裁剪。
普通Judge证据任务不再发送派生subIntent/objective/entities/retrievalQuery，
避免分类标签成为新增要求；原问题、补全、来源和完整候选保留。真正外部代办
仍发送其subIntent/objective边界，intent始终保留；内部Task和运行Trace不变。
下节工具事件及互动修复一并保留，本地校验、计费、知识层、接待流程不变。

## 2026-09-08 证据适用性、工具事件与普通互动

本节修复提交为 `6a3e036`，曾部署后因首个隔离输入错误转人工而回退；
当前生产恢复 `a1d89e2` 及原 hotel Profile。本节不是当前生产已生效的行为。
天气和互动没有执行后续真实模型验收，具体证据见开发记录。
Judge 先确定当前要求、适用事实和完整性，再组织 answerText 与事实。
选中候选不等于整段复制；混合 FAQ 的独立适用事实可用，其条件仍须保留，
无关对象、未触发建议及客户未问的方面不能进入答案或 missingAspects。
本地仍只负责协议与发送安全，不新增语义拦截。

带 ToolCalls 的 Assistant 事件是工具调用过程，不是最终客户答案；
先保留既有用量记录，再跳过最终 replyParts 校验。工具事件、终止型 Graph Tool、
真实最终答案的协议检查及已有防重复执行规则不变。
普通问答的错误恢复不再用招呼掩盖失败；只有真实 greeting 使用招呼兜底。
工具成功后最终答案不应因中间事件的格式产生错误而被替换。
恢复仍出现时必须检查 LastProtocolError，不能因最终发送了文字就算验收通过。

Intent 将问题明确性与酒店业务相关性分开：明确常识、解释及建议复用 interaction/chat，
只在回答依赖实时天气时调用既有天气工具。短反馈结合紧邻问答理解，
不删历史、不增加已答状态、不让明确新主题继承旧对象。
酒店事实、知识优先、房号与转人工等既有边界不变。

## 2026-09-08 检索表达来源约束

发布状态：生产为 `a1d89e2`，三个Go包回归与服务健康检查通过。
首批五轮模型测试四轮答案通过；混合场景的原话来源遗漏已修正并部署，
但修正后未追加模型复验，不能标记全部真实场景已通过。

完整新问题（`clear + independent`）以当前 Task 原话检索；需要回指或条件补全时，
继续使用模型的 `resolvedText`。`evidenceQuery` 可摘取当前原话或对应检索依据的连续片段，
不再自由改写问题。运行时忽略既有规范化允许的标点、空白和大小写后检查片段来源，
不满足则仍用原话或补全问题检索，不丢弃 Task、不增加检索或模型调用、不触发接待。
这不是语义等价证明：合法缩短查询的对象和必要条件仍由 Intent 保留，
Judge 继续收到未截短的原问题、补全问题和原有来源上下文。
外部代操作现有自助入口查询、旧 Profile 无字段时的回退、短标签补全保持不变。
本节与下节 `partial` 分流共同发布；实际部署与验收状态以开发记录为准。

## 2026-09-08 现有适用知识优先回答

历史发布状态：以下改动初次位于 `07bc113`，首轮真实模型验收未通过后回退至
`6108b44`，随后随上述检索表达修复一起发布至 `a1d89e2`。
最初失败轮的检索改写未召回机器人条目，Judge没有该候选；详情见下述开发记录。

本节覆盖下文“咨询 partial 必须追加人工”的旧描述。知识库内容不变，Judge
保留与当前目标直接对应且仍适用的服务、专用设施或办理方式；细节不足保留为
`partial/missingAspects`，不清空已有答案，也不把缺失维度扩写成已确认能力。
无关背景、已拒绝或已失效方案仍不可作为当前请求的可用答案。
`hotel_info` 的合法部分答案沿原文本路径回复，不仅因 partial 自动转人工。
实际服务任务继续消费现有 `hasUsableSelfService`，可用自助设施与方法可以回答，
无法自助、现场执行和精确转接指令仍走原接待流程。
通用转接指令不覆盖门店已选中的部分正文；通用完整正文兜底、门店精确转接
优先及知识层隔离不变。没有新字段、判断阶段、模型调用或本地语义推断。
程序继续使用 Judge 锁定的 answerText，未选中知识不得直接照发。
本次验证和发布身份见 `docs/development/service-knowledge-handoff-20260907.md`
的“现有适用知识回答修复”记录。

## 2026-09-07 问题覆盖与有界修复

发布状态：2026-09-08生产为 `6108b44`。用户确认末项实际为纸笔/草稿纸，
撤回历史两轮因现金解读造成的失败结论，撤销因此新增的防错字限制。
三轮实测的无标点五问已完整回答，回指和服务适用性仍暴露问题后做了限定收口；
最后收口通过自动回归和服务健康检查，未追加模型复验，不能声称语义全部验收。
具体记录见 `docs/development/service-knowledge-handoff-20260907.md` 的9月8日澄清及发布。

Intent 先建立独立答案目标，再分类。同类不同对象必须分别形成 Task 和检索问题；
同一对象的数量与费用、明确比较和交集不因此拆坏。本地不根据标点或关键词拆题。
现有单次 Judge 同时读取本轮完整来源与全部 Task，检查漏题、错误合题和检索目标偏移。
知识不足属于任务证据裁决，不等于漏题，不触发覆盖修复。

仅模型明确返回覆盖问题时，允许一次 Intent 修复、仅变化任务的并行检索及一次局部
Judge。原有正确 Task ID 和证据保留；修复结果仍不完整或协议异常时沿用失败处理，
不循环修复、不提交残缺计划，也不伪装为知识转接。真实路由和消息有效性检查继续生效。
正常路径仍为一次 Intent、一次 Judge、一次 Generate；异常覆盖修复最多额外一次
Intent 和一次 Judge，与既有 Generate 协议重试分开记录。
人工恢复使用既有冻结任务，不重新拆解已提交来源。没有知识阶段的纯互动或明确人工
指令保持原路径，不额外引入 Judge。
逐题 Generate 契约携带已有澄清任务模式，仅对该题追问真实歧义，不把澄清题当作
没有证据的酒店事实问答；其他已回答题不受影响。
9月8日补充：回指多个已确认对象时，仍由Intent按对象建立独立查询；
各Task可共享当前回指原话和来源，不能因共享一个位置/方法问题而合并检索。
Judge先排除当前明确无法采用的方案，再判断是否存在可用的部分答案；
不把不可用方案的位置或物品背景当作当前请求的部分答复。

成功转接通知使用现有 deferred Task 归属标明待处理问题；部分可答任务保留原问题，
并明确标注“仅待确认”的缺失事项，不能只保留缺失方面而丢掉物品/服务对象，
也不把其他已回答任务算入转接。待补房号时事项保存在现有 pending JSON 中，补房号后
继续使用同一成功通知 ID。无事项的旧调用仍发送原成功话术；不改变排班、房号策略、
状态机、Outbox 或十分钟恢复机制。

## 2026-09-07 逐题答复与恢复

本节覆盖下文与之冲突的旧整批兜底描述。Intent 先结合本轮全部来源判断澄清必要性；
已经被后续完整问题解释的互动只绑定为 context sourceRef，真实独立歧义仍须澄清，
不修改模型拆题权或本地 Task 归属校验。
Judge 先输出当前问题的最小完整 answerText，再提取其事实与关键值，混合 FAQ 可复用，
但各 Task 只回答自己的部分。知识层优先级、候选归属和服务自助判断不变。
关键值是 Statement 的精确标注，不是独立事实。标注不存在于 Statement 时，发送内容
必须保留该完整 Statement，不猜测修字，也不通过删除约束放行残缺答案。
锁定答案缺少必要值或不安全时，先在同一 Task 内使用已选中的完整事实恢复；
不能安全恢复时仍报协议错误。失败 Generate 中已通过检查的任务文本只在当前调用内保存，
最终兜底保留这些文本并补齐其余任务，不新增持久化状态或模型调用。
JSON 归属不明、重复或未知 Task 不提供可复用部分结果。恢复前复用现有有效性检查；
内部协议和消息标记防线、最多三条消息、Commit 与 Outbox 均保持原职责。

## 2026-09-07 服务知识与转接关联

服务请求沿用同一次 Judge，在内部结果中区分证据完整性与
`hasUsableSelfService`。门店知识已有适用于当前客户条件的同目标自助方案时，
即使执行能力仍为未知，也直接回答适用知识，不因 `partial` 自动转人工或追加能力声明。
`missingAspects` 是内部边界，不是必须逐项发给客户的清单。明确不提供当前请求的
适用政策也可以完整回答；未触发的特殊情况不扩展为缺失事项。此规则覆盖各类业务需求。
客户无法自助、已尝试失败、明确需要现场执行或门店精确转接指令仍使用原接待路径。
该判断由模型完成，本地不新增自然语言判断，也不新增已答题目状态。
运行解码只读取约定字段，不因额外说明字段丢弃正确裁决，也不将其传入回复。
服务裁决明确提供自助判断和未知项时，可将合法 direct 标签保守归一为 partial，
保留原事实、答案、未知项与自助判断；候选归属、必要字段及类型校验不放松。

转接前仅保护本轮 Commit 记录中的实际回复消息 ID，并以现有来源消息生成的
稳定转接通知 ID 绑定发送顺序，不再依赖可为空的 requestID。
真实员工接管仍可取消这些普通 AI 消息。企微成功发送的外部消息关联必须先记录，
不能因发送过程中切换路由而丢失；同会话在途发送的 self echo 等待发送结果关联，
以精确消息 ID 区分 AI 回显和员工发言，不按内容相等去重。
不增加模型调用、数据库结构、外部接口或普通消息固定等待。

## 2026-09-06 有界修复说明

本节覆盖下文历史描述中冲突的旧限制，代码与
`docs/development/reply-runtime-bounded-fixes-20260906.md`记录为准：
跨轮回指可使用已提供的有界历史，不要求紧邻完整问答对，answer_rejected的紧邻AI条件不变。
仅明确会话回顾按预算加载当前session更早消息，不增加持久化已答状态。
Intent可输出内部可选evidenceQuery用于召回，resolvedText仍保留完整裁决条件。
Judge负责语义与事实选择，本地记录具体协议错误，不做第二次语义裁决；
Judge输入区分客户原话question与resolvedQuestion，原话决定请求及回答范围，补全不能新增要求。
选中层携带内部可选answerText，程序使用该简短答复而非逐条拼接Statement；
旧结果未携带answerText时兼容原事实组装。Generate保留任务输出但不再自由改写事实，
无选中证据的互动回复行为不变。直接和延迟接待共用房号策略。
hotel_info咨询不授权房号收集，实际服务和缺少Task元数据的旧路径不变。
锁定答复的关键值或协议安全错误不重试Generate，沿用安全事实兜底；
Generate自己漏Task/FactID仍只重试Generate。数量、条件、账号密码等内容不得为通过检查而删除。
纯汉字名称中的中文展示引号只作比较兼容，不改写原文；含数字、字母或标点的关键值不走该兼容。
撤回人工意愿的`explicit_handoff + objective=cancel`不授权新的转接；
取消订单等业务动作和同轮独立风险任务不受影响。酒店能力询问与第三方代操作、
检索目标与完整裁决条件仍由现有Intent区分，不新增本地语义判断。
部署身份与真实验证范围见上述开发记录，未覆盖或未通过场景不得算作验收完成。

本文只描述原始修复基线 `40cc24be3972ab341af7f0ef83a4732e9630ad87`
之上的现行回复链路和截至 2026-08-31 的当前工作区实现。此前 A+B 运行记录中的
`39e8656a4e8d9bf25cd2df5e8619592af2ad5c67` 与
`/opt/agentdesk/releases/20260831-142758-context-judge-ab-39e8656` 仅作为历史部署
参考，不代表本轮工作区修改已经随该 release 部署；两者不得混写为同一个“生产基线”。
真实代码优先于历史交接材料；旧 FAQ、旧 Hook Bridge 和旧独立 Agent 设计不属于本文
架构依据。

截至 2026-08-31，本轮实现已在当前工作区收口，代码级验证已完成。本文不把本轮
提交、推送、新 release 部署、真实模型复测或企微最终出站验收写成已完成；这些动作
若后续执行，须以实际记录为准。

## 1. 运行边界

正常文本回复链路保持为：

```text
120ms 消息收敛
-> 1 次 Intent
-> 原子任务并行知识检索
-> 1 次现有 Knowledge Evidence Judge
-> 1 次 Generate
-> 本地逐题校验与最多三条消息合并
-> Commit
-> Outbox
```

本轮没有增加新的模型阶段。只有 Generate 出现协议错误、429、可重试 5xx、
超时或连接中断时，才使用同一批已冻结任务和证据额外重试一次 Generate。
Intent、检索和 Judge 不会因此重跑。

Resource 与 Handoff 仍由现有 Action Ledger、结构化资源提交和接待服务执行，
不伪装成 Generate 文本。数据库 Task、人工状态机和房号追问规则不在本轮变更范围内。
普通 `ai_reply` 与 AI 服务通知继续沿用既有 ClientMsgID、Commit、Outbox、发送重试和
幂等行为；本轮只为 `manual_resume` 增加第 8 节所述的请求绑定投递修复，不扩展到
普通回复或服务通知。

“客户已经看到业务答案”不以 RunLog 或 Commit Trace 的 `sent` 字样单独判断。站内
消息要求真实 Message 存在且未撤回；企微客服、企微 CLI 和企微员工号等外部渠道还
要求对应 Outbox 已经是 `sent`。转接成功、人工恢复提示等 AI 服务通知不绑定业务
`taskIds[]`，也不能结算文本或资源 Task。

## 2. Active Answer Task

Intent 后生成本轮唯一的活跃任务清单。代码使用运行时
`ReplyPlan.TaskPlans` 承载这一结构，不新增持久化模型：

```text
TaskID
Intent / SubIntent
Objective / RelationToPrevious / ResolutionState
Entities
Text / OriginalText / ResolvedText
SourceRefs
OutputKind / ReplyRequired
Output / ResourceAction
SelectedLayer / SelectedCandidateIDs
SupportedFacts / MissingAspects
```

`OutputKind` 只有四类：

- `text`：必须进入 Generate，并且每题恰好产生一个回复 part。
- `resource`：由 Commit 发送真实结构化资源，不进入文本生成。
- `handoff`：由现有接待流程处理，不进入文本生成。
- `context_only`：只帮助理解相邻业务问题，不单独强制回复。

当同一轮同时包含业务问题和感谢、语气纠正等普通互动时，互动任务降为
`context_only`，其 `sourceRefs` 合并到最近的业务任务。纯互动消息仍保留一个
正常 `text` 任务。旧 Profile 或旧测试数据未提供新字段时，代码继续从既有
`Output`、`Intent` 和 `Text` 推导，支持渐进上线。

## 3. 自包含问题与来源

Intent V2 的每个任务同时保留：

- `objective`：客户当前真正要获得的信息或动作，例如
  `availability/quantity/method/action_request`。
- `relationToPrevious`：只描述与紧邻上一轮的关系，例如
  `independent/follow_up/clarification_answer/reference_previous/answer_rejected`。
- `resolutionState`：`clear/resolved_from_context/ambiguous/unresolved`，只有真正歧义
  的任务才进入澄清；同轮其他清晰任务继续执行。
- `entities`：只保存当前任务明确出现或由紧邻上下文可靠补全的房型、设施、地点等
  轻量实体，不建立第二套持久化 Task Memory。
- `text` / `OriginalText`：当前 Task 在 `sourceRefs[0]` 对应客户消息中的连续原话；
  指代补全和语义改写只能写入 `resolvedText`。
- `resolvedText`：补全明确回指、比较或省略后的自包含语义问题，供 Judge 和 Generate
  使用。检索可从它派生仅用于召回的 `EvidenceQuery`，但该改写不得反向替换 Judge 的
  语义问题。
- `sourceRefs`：按 `U1`、`U2` 等引用当前短消息组；首项是主要问题来源，其余
  是该任务共同消化的上下文来源。

同一轮包含多少原子问题、每个问题的语义边界在哪里，只由一次 Intent 模型判断。
Intent Prompt 每轮都要求从 `U1` 到 `Un` 逐条扫描，且不能依赖标点、换行、空格
或固定连接词；代码不再向模型披露本地猜测的候选，也不会在模型返回后重新拆分、
合并或补造 Task。检索层严格使用模型给出的知识 Task，不再从客户原文二次拆题。

V2 本地协议只负责机械安全，不再实现第二套中文 NLU：校验 JSON、必填字段、枚举、
`sourceRefs` 范围、主要来源顺序，以及 `text` 能否追溯到 `sourceRefs[0]`；精确重复
Task 可以合并。代码不再按标点或关键词推算题数，不再重判 interaction/业务分类，
也不再用字符重叠、实体逐字出现或中文词表证明 `resolvedText`。协议失败仍只触发现有
一次 Intent 协议修复，修复提示要求保留模型已经识别的任务边界和语义。
来源核验先接受客户原文的直接匹配，问号、标点和表情不能因检索清洗后为空而失去来源。
协议修复会携带首次模型 JSON 和具体错误；修复后检查任务数量及原先合法任务的身份，
不能用删题、改写合法任务或重新拆题来绕过失败。

`resolved_from_context` 只做上下文指针校验。同一个 URef 内部可以包含问题及其上下文，
允许 independent + resolved_from_context，只引用该 URef 一次；跨 URef 的同轮承接
仍引用更早的 URef。跨轮承接必须使用 previous 类 relation，并存在已提供的有界历史
或紧邻客户与客服问答。本地不判断补全文字是否相似。`answer_rejected` 仍要求
`human_complaint_risk/answer_rejected` 与 relation 一致，且紧邻上一条确实是 AI 答复；
是否构成否定、矛盾或答非所问由 Intent 模型结合三段上下文判断。

例如 `U1=有早餐吗 / U2=几点 / U3=在哪吃` 的任务数量、承接关系和顺序由 Intent 模型
一次完成；`那麦田呢` 可以补成自包含的房型问题，随后完整的新主题 `早餐几点` 不应继承
旧房型。知识检索优先使用 `resolvedText`，缺失时才回退到 `text`。

V2 Intent 返回后继续执行动作权限收窄：`ambiguous/unresolved` 清空知识、资源、工具和
人工动作；酒店信息与服务请求只可查询知识，酒店变量只可提交白名单资源动作，只有
`human_complaint_risk` 可直接进入人工路由。未知资源动作和非法工具请求仍会被剥离。

外部平台代执行统一分类为
`service_request/external_proxy_action + objective=action_request`。Judge 只判断知识中是否
存在能帮助客户自行完成同一目标的地址、电话、入口或步骤；有证据时回答“无法代操作 +
自助方案”，无证据时只说明真实能力边界，不因此转人工。知识明确要求转接、客户明确找
人工或投诉时仍走原人工链路。酒店内部送物、补用品、维修、开门、换房和打扫不属于该类，
继续沿用原知识优先、房号追问和接待流程。

## 4. Judge V2 与知识层级

现有 Judge 使用 `knowledge_evidence_judge.v2`，对每个原子任务分别裁决
`store` 和 `general` 两层。模型协议只允许四种业务裁决：

```text
direct_single
direct_combined
partial
insufficient
```

以下三种是运行时记录的调用或协议失败状态，不是模型对“资料是否足够”的业务判断：

```text
protocol_invalid
timeout
malformed
```

结果携带：

```text
decisionSource
candidateCount
selectedCandidateIds
supportedFacts[] { factId, aspect, statement, criticalValues }
missingAspects[]
```

`decisionSource` 记录每个 Task、每个知识层的真实裁决来源：正常 Judge 输出为
`model`；模型已经选定 Candidate、但事实结构需要从该 FAQ 原文机械重建时为
`model_selected_repair`；FAQ 问法或显式 alias 与当前 Task 机械相等时的严格恢复为
`exact_faq_fallback`；同样满足严格相等且答案仅为“转接/转人工”时为
`deterministic_handoff`。同一批次中的不同 Task 可以有不同来源，因此该字段不能
只记录在 Judge 批次顶层。`candidateCount` 同时记录批次、Task 和知识层候选数量；
`supportedFacts/missingAspects` 也按 Task 和知识层分别保存，禁止把门店层已覆盖事实
与通用层缺失方面混成一份结果。

事实维度限定为 `existence`、`quantity`、`price`、`time`、`location`、
`method`、`scope`、`condition` 和 `other`。一个维度不能推导另一个维度：
“有外卖机器人”只证明存在，不能推出能送到房门；地点名称不能推出距离、
步行时间或路线。

Judge 只输出完整回答当前 Task 所需的最小事实集合。同一完整事实句已经包含短句时，
可以让多个 FactID 共同引用该完整句，不能再把摘要短句作为第二段客户内容。
`criticalValues` 只保存不能自然改写的精确值，例如数量、金额、时间、电话、地址、
房型、账号密码、收费状态或固定选项；“建议、选择、联系、回复、比较”等普通措辞
保留在 `statement` 的语义中，但不要求 Generate 逐字复述。

同一知识层内可以用 `direct_combined` 组合多条属于同一门店、同一对象和同一
适用范围的证据。不得跨门店库和通用库拼接，也不得组合不同房型、时间条件或
相互冲突的内容。用品自取 FAQ 的地点冲突按答案中的明确地点短语判断，不依赖
有限地点名称清单；完整地点与其简称可以兼容，两个不同地点则禁止本地救援。
FAQ 的问题与答案按一个完整语义单元理解；“问题中写两瓶、
答案肯定免费”可以形成数量和价格事实，但答案为“转接”时不能把问题文字当作
已确认事实。肯定前缀（如“是的、对的、有的”）只有在后续整段没有否定、限制或
数量改口时才确认前提；“是的，但实际没有提供”不能还原为肯定事实。对“有没有”
这类存在性问句，明确肯定的具体子类可以证明其上位类别存在，例如“一次性拖鞋”
可以回答“有拖鞋吗”；否定一个具体子类不能证明整个上位类别不存在。不同具体子类
的一正一负不视为冲突，只有同一主体、同一适用范围和同一条件下的相反结论才冲突。
该关系按规范化后的中文中心词后缀保守判断，不维护用品名称白名单，因此“早餐券”
不能回答“有早餐吗”，“静音空调房”也不能回答“房间有空调吗”。问句里的“没有”
不能被误当成答案的否定。
多主体问题还要检查答案的显式覆盖：问题询问 A、B，答案若写成“是的，A……”却没有
确认 B，不能把问句前提扩给 B；纯“是的”或“都是……”等没有缩窄主体的整体回答仍可
结合 FAQ 问题确认全部已列主体。模型 Fact 即使文字相似，也必须满足同一主体覆盖规则。

严格 exact FAQ 恢复按知识层独立执行，不读取向量分数，也不使用字符相似度或语义近似
改判。只有 FAQ 问法或显式 alias 与当前 Task 在去除标点、礼貌前后缀后机械相等，
同层所有相同问法答案不冲突，并且单条 FAQ 能机械重建完整事实时才允许恢复。另有一条
`0.85` 高置信候选规则仍是普通确定性 FAQ 选择的下限。唯一窄例外是：Judge 成功返回后，
门店层用品类 `service_request`，或 `hotel_info + availability`，被判为 `insufficient`，
且分数不低于 `0.70` 的同一完整 FAQ 在预算内候选和全部 `RawCandidates` 中仍是同一个
无冲突结果时，可以恢复为正文答案。该例外不处理 `partial`、`protocol_invalid`、
Judge 调用超时/整体协议失败、通用库或非用品问题。
答案仅为“转接/转人工”时只形成该层的确定性知识转接；不得把 FAQ 问题文字当作
事实，也不得跨 FAQ、跨知识层拼接对象、范围或条件。同层 `RawCandidates` 中只要
还存在一条可信、值得交给 Judge 复核的竞争正文 FAQ，精确转接就不能在
Judge 异常时自动恢复为转接；正文即使只存在于 `RawCandidates`、未进入本轮 Judge
预算，也必须参与该否决。紧预算只有一个槽位时优先保留可信待复核正文；至少两个槽位
时优先把同层正文和精确转接一起交给 Judge，再使用剩余槽位放通用兜底。Judge 已经
真实看见两项并明确选择精确转接时，可以执行该模型裁决；本地只阻止模型未见正文时
由异常 fallback 抢先转接。
模型若在非精确问法下选择
转接候选并把它包装成普通事实，或者让转接候选参与 `partial/direct_combined`，该
知识层直接记为 `protocol_invalid`，候选正文不得进入 Generate。

源码中宽泛的 `repairHighConfidenceInsufficientKnowledgeSelections` 仍只供历史隔离测试。
生产路径只通过 `store_service_faq_rescue` 使用上面的用品类窄例外。`0.70` 既是预算阶段
值得交给 Judge 复核的可见性下限，也是该窄例外的最低分；两处都仍要求主体、范围、
条件和操作一致，且同层没有真实冲突。其他任务继续使用 `0.85`，不得在 Judge 后改选答案。

房型成员问题会把实体中的“房型/客房”后缀规范化后再与肯定枚举成员对齐。例如
“部分房型配备办公桌，如合柴、麦田和艺林”可以确认“合柴房型有办公桌”，但仍不能
把该部分枚举冒充完整房型名单。

代码的胜出顺序固定为：

```text
无竞争正文的门店精确转接指令
-> 门店完整答案
-> 通用完整答案
-> 门店部分答案
-> 通用部分答案
-> 现有接待路由
```

`partial` 不再丢掉整题。已确认的 `supportedFacts` 继续进入 Generate，只有
`missingAspects` 交给现有 deferred handoff 逻辑；是否实际接待仍遵循既有接待
策略。Generate 只能看到胜出层的选中证据，不能看到被淘汰层或冲突候选。

线上运行时以 Judge 作为唯一语义裁决者。本地只校验 JSON、Task、知识层、Candidate ID、
决策数量关系和事实字段协议，不再推断主体、范围、价格、存在性、方法或肯否关系，也不再
通过 `model_selected_repair` 重建有效 Judge 结果；旧严格语义校验仅保留用于离线诊断测试。
完整性同时按客户问题中的主体与事实维度配对检查。例如同时询问矿泉水和枕头的
数量、费用时，不能用“矿泉水免费 + 枕头两个”冒充四个槽位都完整；多房型、多设施
也只接受明确分句中的配对，不做笛卡尔积。“不确定、待确认、资料未写明”等边界
可以作为条件说明，但不能充当存在性、数量、费用、位置或方法的确定证据。
“都是/均为”等整体谓词只有在当前子句显式列出全部主体，或答案开头仍保持 FAQ 问题
的整体主语范围时才能继承全部主体；前文已缩窄到单一主体，或整体谓词属于早餐、
停车位等其他对象时，不得重新扩大到客户所问主体。FAQ 问题已经完整列出同一类型
主体时，答案中的受控同类总称可以继承该组主体，例如“携程、抖音、美团”对应
“不同平台”；“部分平台、某个平台、其他平台”等非全称表达、混合类型实体和裸类型词
不得继承。整体结论后的单主体补充只增加细节，不反向抹掉前面已经成立的整体结论。
FAQ 以“是的/有的”开头时，后续若对当前 Task 所需主体或事实维度声明不确定，也不能
从 FAQ 问题机械继承该事实；例如“是的，但具体数量不确定”不能确认问题里的“两瓶”。
与当前 Task 无关的细节不确定性不会抹掉已经明确确认的业务事实。

`supportedFacts` 或 `missingAspects` 的局部 JSON 类型错误只隔离当前 Task/Layer，并在
Candidate、decision 和数量关系仍合法时尝试 `model_selected_repair`；兄弟 Task 和
兄弟知识层不会被整批丢弃。未知或重复 Candidate、非法 decision、错误的
`direct_single/direct_combined` 数量关系、明确对象冲突仍严格记为
`protocol_invalid`。事实格式坏时必须重新校验所选 FAQ 与当前 Task 的房型、设施、
配置范围和主题，不能靠标题或客户问题本身把错误 Candidate 修成正确事实。

Judge 单次调用的超时保留旧生产单题有效基线：未配置或配置超过旧上限时，普通单题
仍为 15 秒；只根据 Task 数和候选数向上扩容，阶段预算最多 28 秒。父级回复 deadline
固定为 Generate、Commit 等下游保留 12 秒，因此 Judge 实际使用
`min(阶段预算, 父级剩余时间 - 12 秒)`：父级只剩 30 秒时，即使 8 Task、28 Candidate
计算出的阶段预算为 28 秒，Judge 最多也只能使用 18 秒；只有父级至少还剩 40 秒时才可
获得完整 28 秒。没有有效阶段预算时不再调用模型，并沿用现有失败隔离与精确 FAQ 恢复。
不增加重试或第二次 Judge。
查询里的固定数量只有在所选 FAQ 答案
直接包含，或 FAQ 问题包含且被肯定回答机械确认时，才成为 direct 答案的必答关键值；
“一间房、三间房、两位客人入住”等范围数量不会被强制复述，“两瓶矿泉水、两个枕头”
等当前物品数量仍必须保留。“加一条浴巾、帮我拿两瓶水、推荐一个房型”中的数量属于
服务请求参数或结果基数，不要求知识答案重复该数量；若客户询问“两瓶是否免费、房间
有两个枕头吗”，数量仍是事实约束。中文和阿拉伯数字的同单位数量按机械等价处理，例如
“两瓶”和“2瓶”。多主体或 `compound_information` 数量按“主体 × 数量”绑定，
每个主体必须由同一 FAQ 分句或可证明的问答单元支持，模型 Fact 也必须保持同一
“主体 -> 数量”关系，不能只因主体和值分别出现在证据中就交换归属。`一共/总共/合计`
只在 FAQ 明确写出等价总量，或全部已命名主体的同单位唯一分项能够机械求和时成立；
任一主体缺失、单位不同或存在冲突总量都不能进入 direct。单一明确主体下，客户问题的数量与所选 FAQ 唯一明确数量冲突时，
该层记为 `protocol_invalid`，不能用“四瓶”回答“两瓶”；同一所选 FAQ 或多个所选 FAQ
只要还包含同主体、同适用范围、同单位的冲突数量，也不能被其中一个正确数量掩盖，
会议室等明确不同范围的数量不会污染客房答案；同一 FAQ 中“四瓶饮料”这类明确属于
其他物品的分句也不会污染“两瓶矿泉水”，没有写明其他物品的“四瓶”仍按当前主体
继续检查。Intent 偶发未输出实体时，从数量相邻文本机械恢复可唯一配对的物品主体，
单一问题和“矿泉水两瓶、饮料四瓶”这类多数量问题都按“主体 × 数量”验证，禁止数量
在主体间交换。“矿泉水和饮料都有两瓶”这类共享谓词只把谓词之后的数量分配给已明确
列出的同类主体；“矿泉水有两瓶和饮料有四瓶”“矿泉水有两瓶且枕头有三个”这类无标点
口语在客户问题和候选答案两侧都按连接词位于数量前后的位置绑定相邻主体。无法唯一恢复
时保持保守判断，不猜测关系。
同一主体在工作日、周末、节假日等不同条件下存在不同数量时，数量冲突检查按
“主体 × 条件 × 单位”比较；工作日两瓶和周末四瓶互不冲突，只有相同条件下出现不同值
才会使当前层失效。
FAQ 问题中的窄条件只在答案没有重新声明该条件维度时继承。答案明确写“每天、每日、全年、
不分工作日和周末”时，只把日期类型维度扩为全称；写“全天、全时段、所有时段”时，只把
昼夜时段维度扩为全称。一个维度的全称表达不能抹掉另一个维度的限制，也不能把分别满足
日期和时段的两条 FAQ 拼成二者交集。
单一查询数量
仍检查全部所选 FAQ 的同范围、同单位冲突。时间证据还要区分开始、结束
和时长；“几点开始和结束”与“几点到几点”都要求两个槽位，`7:00-9:30`、
“早上七点到九点”等明确时段均可机械覆盖；同时询问工作日、周末或节假日时，
完整性按“主体 × 条件 × 时间槽”逐格校验，不能用工作日时间消除周末缺失。
“工作日七点开始、周末八点开始”按条件分别保留且互为互补，不互相当作冲突或错误覆盖；
“下午两点到五点”的下午范围同时作用于两端；“晚上八点到两点”归一化为
`20:00-02:00`，显式“晚上十点到次日两点”同样保留跨午夜边界。“七点开始到九点”等
相连范围也必须保留两个端点。同一 Task 同时询问多个
时间主体时，按“主体 × 时间槽”逐项检查，不能用早餐时间覆盖晚餐时间。FAQ 问题已经
声明唯一主体而答案只写时间值时，以同一 FAQ 问答单元绑定主体；答案首个无主体时间
分句也继承该 FAQ 主体，不能把正确时间范围误判为无资料；单个开始时间仍不能冒充
完整时段。单主体价格与时间的省略答案必须由同一 FAQ 问题或答案明确包含当前 Task
主体；“早餐不收费”不能支撑“停车免费”，“晚餐六点”不能支撑“早餐时间”。时间事实
按分句提取槽位并维护显式主体：主体只向后继无主体时间分句传播，
遇到午餐、退房、入住等另一个显式主体立即停止；“营业时间/供应时间/开放时间”可在
唯一 FAQ 主体下绑定，“退房 12 点”不得绑定成早餐结束时间。“从什么时候到什么时候”
和“几点至几点”同样必须覆盖开始、结束两槽。`partial` 保留已确认事实；
如果同一所选 FAQ 已机械补齐全部必要主体、维度和关键值，则晋升为
`direct_single/direct_combined`，只删除已由同一主体事实证明的旧 `missingAspects`。
即使模型把 `missingAspects` 输出成错误 JSON 类型，也只能在重建事实确认完整后晋升；
真实缺失仍保持 `partial`。
配送范围、适用条件或其他真实缺失项继续保留，不能用存在性或其他对象的数量代替。
同层多个配送地址候选被 Judge 同时选中时，还必须两两检查其具体门店名或街道地址；
确定不同的地址不得进入 Generate，同一地址的补充楼层/房号说明仍允许组合；
“南七店/东七店”这类完整短门店名同样参与冲突检查，“本店/门店/到店”等泛称不作为
地址值。同一人物姓名在“先生、女士、老师”等称谓差异下按同一值处理；答案只有
“汤东强。”这类锚定裸姓名时也参与同层冲突检查，未知、保密或角色词不作为姓名值。

Judge 批次候选总预算固定为 28。配额只有一条时优先保留门店中可信、值得 Judge
复核的正文。`0.70` 是这一步的待复核可见性下限，不是召回阈值，也不代表答案成立；
主体、范围、条件、操作或事实完整性不一致的候选仍不能获得优先槽位。配额至少
两条且门店、通用均有候选时，通常各保留一条最佳候选，不能让本地“完整性”启发式
在 Judge 前删除整个通用层。唯一例外是：门店没有单条完整答案，但两条同层候选能
机械覆盖当前 Task 明确要求的多个事实维度、主体或配置字段，此时先保留这两条必要
证据；配额仍有剩余时再补通用兜底。泛泛的“同时询问”或两条近重复 FAQ 不构成该
例外。普通问题与 `compound_information` 共用这条规则，语义多样性只填充剩余槽位。

房型等枚举证据区分 `complete/partial/invalid`。“等、部分、例如、比如、诸如、但不限于”
必须在清洗成员前保留并判为 `partial`；只有完整枚举才能生成排他式交集结论。
不同事实 aspect 不通过文本相似度改写为同一句；数量、价格、位置、存在性、时间、
方法、范围和条件分别保留，肯定与否定事实永远不能合并。

每轮只调用一次 Judge。Judge 不可用、超时、调用失败或出现会影响最终选择的
`protocol_invalid/malformed` 时，不重跑 Judge、Intent 或 Retriever，也不降级为使用原始召回；
失败 Task 本身不新增人工接待，并在 Generate 前进入确定性安全短答。未经选择的
`Hits/ContextResults/ContextText` 会被清空，`RawHits` 只保留在内部 Trace 中用于
排障。协议失败只隔离对应 Task/Layer；同轮已经合法的选择和事实会被冻结保留，
不能被失败题清空。某层协议失败时，另一层已经合法的完整或部分答案仍按固定优先级
生效；只有所有候选层均无合法结果且至少一层协议失败时，Task 才进入协议恢复。
同轮已有合法门店完整答案时，不会因为无关通用层协议问题增加模型调用。若同轮另一个 Task 已经得到部分事实或严格知识转接，
其已确认事实、缺失方面接待动作和转接顺序继续保留；协议失败 Task 不能吞掉这些
兄弟任务，也不能把局部异常描述成“整个知识库不可用”。

## 5. 逐题生成与本地合并

所有 `ReplyRequired=true` 的文本任务使用统一协议：

```json
{
  "replyParts": [
    {
      "taskId": "task-1",
      "content": "房间内有两瓶矿泉水，都是免费的。",
      "coveredFactIds": ["F1", "F2"]
    }
  ]
}
```

本地校验要求：

- 每个文本 Task 恰好出现一次，顺序与活跃任务清单一致。
- 未知、重复、缺失、空 `taskId/content` 均判为协议错误。
- `coveredFactIds` 只能引用该 Task 的事实，且必须覆盖全部必答事实。
- `criticalValues` 中的数量、金额、电话、地址、日期、时间、房型名等必须真实出现
  在对应回复文本中。
- Markdown JSON 代码块、字符串包裹 JSON 和常见外层包装会先在本地拆包；
  无法可靠解析时不得把原协议发送给客户。

全部 Task 校验通过后才在本地合并。合并只连接连续 Task，不重新总结或删除
内容，最终最多形成三条客户文本消息。同一个 Task 内只删除完全相同的重复句；
包含关系、不同对象、不同条件、不同肯否定或互补事实都不会触发本地删句。
Resource 和 Handoff 仍按原路径独立提交。

## 6. Generate 单阶段恢复

外层 Trigger 不再因协议失败从 Intent 开始重跑整条 Executor。恢复流程为：

1. 冻结本轮 Active Answer Task、选中证据和事实清单。
2. 第一次 Generate 正常执行。
3. 只有协议错误或可重试的模型/网络错误才额外重试一次 Generate；修复提示会
   指出缺失的 Task、Fact 或关键值，但不改变问题和证据。
4. 重试前再次检查当前来源消息是否仍允许 AI 回复；员工接管或消息失效时停止。
5. 两次均失败时，用 Judge 已确认的 `SupportedFacts.Statement` 生成确定性文本；
   纯互动只使用已有安全短答。
6. 没有可用事实的 Resource/Handoff 继续走真实动作路径；协议或模型错误原因
   绝不能显示给客户。

正常任务仍是一次 Generate，异常任务最多两次 Generate。Graph Tool 已执行时
不会盲目重试，避免重复真实动作。

正常 Generate 的事实提示与确定性兜底共用同一套客户展示事实收敛：完整句可以承载
其已明确包含的短句 FactID，但数量与费用、位置与方法等互补事实仍分别保留。这样模型
恢复与本地兜底不会产生两套不同的重复或遗漏行为。

## 7. Generate 上下文隔离

Intent 仍可读取带角色的必要历史来做回指识别。进入 Generate 前默认移除长期
记忆和整段原始历史，并构建只包含以下内容的任务上下文：

- 当前文本 Task 的 primary/context 来源。
- `resolvedText`。
- Judge 选中的 `supportedFacts` 与必要值。
- `missingAspects` 的禁止补全说明。
- 必要的最近媒体对象。

明确依赖上一轮的任务使用单独的有界会话上下文，不恢复无约束整段历史：

- `follow_up`、`clarification_answer`、`reference_previous`、纠正或
  `resolved_from_context` 任务读取紧邻的一条客户问题，以及其后最多三条连续、同一
  发送方类型的 AI 或人工客服答复。
- `interaction/conversation_recap` 最多读取最近八条当前会话消息。
- 有界历史只用于解释当前 Task，不会重新创建、激活或补答旧 Task。
- 普通 `independent + clear` 任务仍完全看不到旧业务问题。

相邻上下文必须是紧邻的真实“客户 -> 同类型客服答复组”：AI 与人工不能混成一组，
空消息和已注册的 AI 服务通知不切断正文组；历史末尾不是客服答复时不建立相邻组。
四条以上连续答复只保留最新三条，并分别截断，不能让较早长文本挤掉最后一条纠正。
Intent、Judge 和 Generate 使用同一组、同一顺序。两条连续客户消息不能被拼成历史
问答；当前 burst 内的省略关系由 `sourceRefs + resolvedText` 表达，不会错误挂接更早历史。

原始知识上下文始终从 Generate 消息中移除。Generate 只读取 ReplyPlan 中 Judge
选中的结构化事实，避免从未选中候选扩写能力或重新回答旧问题。

语音、图片和普通文字进入同一 Active Answer Task 链路。连续客户消息使用共享
机器标记保存真实物理消息边界，一条多行文字或多行语音仍只对应一个 `URef`。
只有状态为 `understood` 的语音可以进入 Intent；优先使用完整 `mediaText`，仅在
为空时回退 `mediaSummary`。已经包含在当前 Burst 的语音不会再从当前 payload、
媒体上下文或原始历史重复加入 Prompt。本轮不修改 ASR、OCR 或媒体回调。

本地不生成 `POSSIBLE_ATOMIC_TASKS`，也不按问号、顿号、连接词或物理消息数量猜测
题目数。单条长文字、完整语音转写和连续短消息都由同一次 Intent 模型确定 Task 数量
与边界；V2 代码只校验真实 URef、来源覆盖、来源顺序和完全重复 Task，然后按模型
给出的 Task 检索。

## 8. 人工恢复边界

人工超时恢复复用现有 RunLog，不新增跨运行“已答题目”状态。恢复器只在 Trace
结构完整且来源可验证时读取 `ReplyPlan.TaskPlans` 与
`EvidenceJudge.DeferredTaskIDs`，冻结真正延后的 Task；同轮已经正常回答的兄弟 Task
不会重新进入 Intent、Retriever 或 Generate。人工期间新增的客户消息形成新的 URef，
整次恢复最多为这些新来源调用一次 Intent，再与冻结 Task 按来源顺序合并。

正常轮不会为了隐藏待转接问题而删除 Task。完整无答案或明确转接的 Task 以
`output=deferred_knowledge_handoff`、`outputKind=handoff`、`replyRequired=false`
保留在 ReplyPlan，因此 Generate 只看到可回答的文本 Task，人工恢复仍能通过稳定
TaskID 找回真实未完成项。恢复执行时该临时 handoff 输出标记不会被带回新 ReplyPlan；
Task 会重新成为 `knowledge_text_reply`，按原 `text/resolvedText/sourceRefs` 再走知识
检索和裁决。这个标记不是新的持久状态机，只是现有 RunLog 内的执行边界。

知识库未配置、Retriever 不可用等 Judge 前来源不可用路径同样使用显式逐题契约：
`disposition=no_evidence_handoff`、`decision=insufficient`、
`decisionSource=source_unavailable`。新版 V2 Trace 不允许依靠空 disposition 被恢复；
空值只保留给有界 legacy 兼容。

`no_evidence_handoff` 表示仍未完成、可以在原人工超时点恢复的知识 Task。
`knowledge_direct_handoff`、明确转人工，以及答案已真实到达客户的
`answer_then_handoff` 表示转接已完成，原超时点只静默恢复 AI，不重新回答原题。
系统沿用现有超时矩阵：总部网页待接入 3 分钟、门店待跟进 5 分钟、员工真实接管后
空闲 10 分钟；不会在任一超时之后再启动第二段十分钟等待。

V2 Trace 与 legacy Trace 使用不同契约。V2 要求每个来源都能回到真实客户消息 ID；
legacy 只做受限兼容，不能被误标为 V2。Trace 缺失、来源不可信或冻结 Task 无法验证
时，恢复器回到现有重新识别路径，不伪造 Deferred Task，也不建立新数据库表、Task
状态或恢复账本。恢复执行前后仍使用现有请求有效性和人工路由检查。

`manual_resume` 使用请求绑定的投递复核：恢复请求 ID 绑定来源消息 ID，ClientMsgID 使用
`ai_manual_resume_` 加请求 ID 的 SHA-256 前 24 字节十六进制。稳定的 Task/资源归属
ClientMsgID 同时编码请求、来源消息和 Task 集；命中该稳定 ID 时，已落库 Message 是客户
可见内容的权威版本，恢复器只校验 request ID、消息类型和稳定归属，不再拿重跑后的正文或
资源 payload 覆盖它。旧格式或非稳定 ID 仍要求正文或资源身份匹配。通过上述校验后才允许
补建缺失的 Outbox；符合恢复条件且尚未开始外部发送的 `cancelled` Outbox 可以原子恢复为
`pending`。`pending`、仍在
重试期的 `failed` 以及五分钟内的 `sending` 只标记为 `delivery_pending`，不重跑模型，
也不增加人工恢复 Task 的 `RetryCount`；Outbox 变为 `sent` 后，下一次复核完成恢复 Task。

`failed` 且 `next_retry_at=nil` 是终态 `delivery_failed`，与陈旧 `sending` 的
`delivery_uncertain` 分开处理：两者都不重放、不重跑模型，但终态失败使用明确失败事件，
恢复 Task 直接失败且不再排期，会话保持或恢复门店人工、清空自动过期并要求人工跟进。
即使恢复 Message 和终态 Outbox 已落库、对应 RunLog 尚未来得及写入，请求绑定 Message
也会作为崩溃恢复屏障；已经提交但缺少可验证 Trace 的其他投递只进入人工复核，不能再次
运行模型。

任何 `sending` 都不会由 `ListPending` 自动重放，因为企微协议、客服和 CLI 发送接口没有
可供本服务复用的外部幂等键。`sending` 超过五分钟，或员工接管取消了已经 claim 的普通
AI Outbox 时，投递结果记为 `delivery_uncertain`：不重放消息、不重跑模型，人工恢复 Task
终止为失败，会话保持需要人工复核且不设置自动过期时间。员工接管会取消 `pending`、
`failed` 和已 claim 的 `sending` 普通 AI Outbox；协议与客服发送器在外部调用前重新校验
路由，CLI 在把 claimed 项返回给桥接端前重新校验，迟到回执只能更新仍为 `sending` 的行。
AI 服务通知继续按原旁路规则发送。

该校验不能把外部网络调用变成数据库原子操作。特别是 CLI Poll 已把消息返回给外部桥接
端后、桥接端真正发送前的窗口，服务器无法撤回已返回的数据；要关闭这一窗口，需要在
桥接 API 增加 attempt token 与发送前 CAS 契约，本轮不修改外部接口。

## 9. 输出与 Commit 防线

Generate 事件消费阶段先解析 `replyParts`，再执行客户可见文本清理。以下精确
内部头部只允许出现在消息开头，并会安全移除：

```text
[历史消息]
[AI客服]
[人工客服]
[人工作答]
```

标记出现在正文中、清理后为空或结构不明确时，输出判为协议错误并进入 Generate
恢复或事实兜底。普通客户可见的“人工、同事、转接”等词不会因此被拦截。

Commit 会再次调用相同清理函数，并拒绝仍含 `replyParts`、`taskId` 或
`coveredFactIds` 协议外形的文本。只有完成逐题校验的客户文本和已登记的真实
结构化资源才允许提交。内部 Commit Trace 会为每条真实消息记录 `taskIds[]`，并校验
持久化 Message 的 request ID、消息类型和 Task/资源归属；非稳定 ID 继续校验正文或
结构化资源身份。稳定 `manual_resume` ID 命中时保留第一次已落库内容，只修复它的外部
投递，供人工恢复判断哪些 Task 已经真实提交。普通 `ai_reply` 与 AI 服务通知的发送和
幂等行为保持不变；`manual_resume` 的请求绑定 ClientMsgID 与 Outbox 修复只按第 8 节的
严格条件执行。

## 10. Trace

运行 Trace 是内部排障数据，不进入客户消息。本轮重点字段包括：

- `pipeline.replyPlan.activeTaskCount`
- `pipeline.replyPlan.replyRequiredTaskCount`
- `pipeline.replyPlan.taskPlans[].resolvedText/sourceRefs/outputKind`
- `pipeline.replyPlan.taskPlans[].selectedLayer/selectedCandidateIds`
- `pipeline.replyPlan.taskPlans[].supportedFacts/missingAspects`
- `pipeline.evidenceJudge.tasks[].layers[]`
- `pipeline.evidenceJudge.candidateCount`
- `pipeline.evidenceJudge.attemptCount`
- `pipeline.evidenceJudge.tasks[].candidateCount/decisionSource`
- `pipeline.evidenceJudge.tasks[].layers[].candidateCount/decisionSource`
- `pipeline.generate.attemptCount`
- `pipeline.generate.fallbackMode`
- `pipeline.generate.composedMessageCount`
- `pipeline.generate.blockedInternalMarker`
- `input.currentTurnSources[] { ref, messageId, messageType, text }`

这些字段用于定位漏题、事实缺失、错误外推、协议恢复和内部标记拦截；应用日志
仍应只打印必要预览和 ID，不另行输出客户敏感全文。Trace 结构不改变外部 API
或客户消息结构。

Retriever 的原始候选摘要在 Judge 前写入 Trace；Judge 裁决以及 deferred、retry、
handoff 等最终 Task 清理完成后，条目列表会用最终 `EffectiveHits/ContextResults`
覆盖其 `UsedInContext`、上下文排名和淘汰原因。即使
Judge 将所有 Task 判为 `insufficient`、清空 Generate 可见上下文并提前进入接待，
Trace 仍保留真实召回数量、原始排名、知识库和全部候选，同时明确没有候选被最终
授权使用。`pipeline.retriever.count` 表示合并去重后的 `RawHits` 数，不表示进入
上下文的条数。持久化 Retriever 日志只记录原始候选，不再把 Judge 前的预选上下文
标记成最终 UsedHits；最终授权证据以 Runtime Trace 和 EvidenceJudge Trace 为准。

## 11. 验证、发布与回滚

聚焦自动测试范围：

```bash
go test -p=1 \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  ./internal/pkg/replyintent \
  -count=1

go test -race -p=1 ./... -count=1

go test -p=1 ./... -count=1
go vet ./...
```

2026-08-31 的上一轮冻结差异已通过 `go test -p=1 ./... -count=1`、`go vet ./...`
以及文中列出的完整 Race 集合；当时重新构建的 Linux amd64 Server 和评测器也已通过
ELF 架构校验。该历史 Server SHA-256 为
`e9bcd0e551f40ffbfa57fd899000baa7d305443035cfc92618e3eafde8ed3d59`，评测器
SHA-256 为 `5fa0c9c34f374f4e24d63e092521c4274c15547717ffbb6a4611dbc1b502e068`。
2026-08-31 三遍审查后的冻结差异重新通过聚焦测试、全仓普通测试、完整 Race、
`go vet ./...` 和 `git diff --check`。当时 Linux amd64 Server SHA-256 为
`3f4c3c9f8cdb56265ea198a909a6334981bbf9b6ebd36d2be307c8769bf1fe5a`，评测器
SHA-256 为 `693e69cec620eebb919fc7edc19ff9ca23a54ea387e1e47668c71306c9e1185e`。
以上 SHA 都属于较早冻结差异，不对应当前未提交源码。最终提交后必须从干净 detached
worktree 重新构建，再记录 commit、Server/Eval SHA、release 目录和部署结果；这些
代码级结果仍不能替代真实模型和企微最终投递验收。

自动测试必须覆盖多题逐题输出、事实与关键值完整性、回指补全、知识层级、
`partial`、Generate 单阶段恢复、事实兜底、内部标记拦截、媒体文本优先、失败
语音门禁、真实 Burst 边界、无效 `sourceRefs` 拒绝，以及普通 Resource/Handoff/
Outbox 稳定发送 ID。人工恢复还必须覆盖请求绑定 ClientMsgID、缺失 Outbox 补建、合法
`cancelled -> pending`、`pending/可重试 failed/新鲜 sending -> delivery_pending` 不重跑
模型且不增加 `RetryCount`、`sent` 后复核完成，终态 `failed + next_retry_at=nil` 进入
`delivery_failed`，以及陈旧 `sending` 或 claim 后取消进入 `delivery_uncertain` 并保留人工
复核。测试还覆盖 request-bound Message 已提交但 RunLog 缺失的崩溃窗口，保证不再次运行
模型；任何 `sending` 不会被扫描重放，员工
接管能取消普通 AI 的已 claim Outbox，迟到 CLI 成功/失败回执不能覆盖终态。

代码测试不能替代真实模型和企微出站验收。本轮只执行计划内 10 至 15 个代表场景，
再观察首批自然生产回复；隔离企微客户最终投递和生产观察必须单独记录真实结果，
未执行时不得写成已通过。未经用户明确同意不运行 50 轮或更大批量主动评测。

本轮没有数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端、权限、
模型供应商、计价公式或 Token 统计字段变更。每轮只有一条 Judge usage/费用事件，
沿用现有稳定事件键；协议异常不会额外产生第二次 Judge 用量。Release B 异常时
优先回滚到已验证的 Release A，Release A 异常再回滚到基线 `40cc24b`；无数据结构
需要反向迁移。生产 Intent Profile 或运行配置若在部署阶段另行更新，必须独立备份
和恢复。

当前普通异步回复没有独立持久化的全链路 Job 重试器；因此 Judge
`protocol_invalid/timeout/malformed` 在严格 exact FAQ 无法恢复时，最终采用不含酒店
事实的安全短答，并在 Trace 中保留协议失败状态，不伪装成 `insufficient`、不转人工，
也不重复调用 Judge。这里是对原计划中 `judge_protocol_retry` 名称的实施收口：在没有
安全重试基础设施和独立 usage 事件键之前，不以整链路重跑换取表面上的自动重试。

## 12. 并行分支

2026-08-31 的统计口径为：先执行 `git fetch origin`；本工作区取
`git diff --name-only 40cc24b --` 的已跟踪路径，并行分支分别取从其与 `40cc24b`
的 merge-base 到远端 tip 的路径；两组路径排序、去重后求交集。

按上述口径，本轮工作区与 `codex/customer-audit` 的完整同路径交集共 12 个：

- `docs/development-handoff.md`
- `internal/ai/runtime/reply_trigger_service.go`
- `internal/services/channel_message_outbox_service.go`
- `internal/services/conversation_human_dispatch_service_test.go`
- `internal/services/conversation_route_service.go`
- `internal/services/message_service.go`
- `internal/services/message_service_test.go`
- `internal/services/wxwork_cli_bridge_service.go`
- `internal/services/wxwork_kf_inbound_service.go`
- `internal/services/wxwork_kf_outbound_service.go`
- `internal/services/wxwork_protocol_service.go`
- `internal/services/wxwork_protocol_service_test.go`

审计分支在这些路径增加租户隔离与发送约束，本分支增加真实 Burst/URef、统一
`ClaimForDispatch` 和人工恢复投递闭环。合并时必须同时保留两边语义，再合并测试和
文档记录，不能用整文件覆盖解决冲突。

按同一口径，当前工作区与 `codex/ai-billing` 的同路径交集为 0，也没有修改计价公式
或 usage 字段语义；Judge 仍维持每轮一次真实调用和一条稳定 usage 记录。每次提交和
push 前仍需重新 `git fetch origin` 并核对交集，因为并行分支会继续变化。

2026-08-31 提交前又按“当前未提交路径 vs 各远端分支自 merge-base 后路径”重新核对：
本次 13 个文件的实际待提交集合与 `codex/customer-audit` 只重叠
`docs/development-handoff.md`，与 `codex/ai-billing` 重叠为 0。上面的 12 个文件是从
`40cc24b` 统计整个历史分支差异的旧口径，不能解释成这一次提交会同时改动 12 个共享文件。

## 13. 2026-08-31 三遍完整审查后的最小收口

三遍完整审查没有改变模型拥有拆题权、一次 Intent、一次 Judge 和一次 Generate 的主链路，
只补齐五个已经能稳定复现的本地契约缺口：

- Task 去重必须同时满足主 `sourceRef` 所有权和结构化目标一致。不同物理消息中的相同短句
  不能互相折叠；同一主来源下，`U1` 与 `U1,U2` 这类合法重复仍沿用原有折叠行为。
- 槽位追问后改为正向确认当前消息是否真的携带所问槽位值。`谢谢，1208`、
  `1208，你烦不烦` 仍包含房号，必须触发 Intent 协议修复；`哈哈`、`晚点再说` 等
  普通互动不会因为短句而被强塞回槽位任务。“还有什么需要吗”等开放帮助询问也不视为
  槽位追问；明确取消上一任务时必须使用 `cancel_previous`，避免 Deferred Task 后续恢复。
- 只有唯一上一题且客户发送裸“再说一遍”时，才允许直接复述；“名字再说一遍”、
  “地址再说一遍”等带非空锚点的请求仍必须校验锚点是否匹配上一题。
- 混合动作请求中的数量仍是动作参数。`帮我拿两瓶水，是否可以送到房间` 的“两瓶”
  不能因裸 `是否/是不是/有没有` 被升级为必须由知识库证明的数量事实；只有明确询问
  数量、价格或存在性时才进入事实闭环。
- Intent 漏掉 `entities` 时，单主体 `price` 或 `existence` 问题会从自包含 Query 保守
  恢复唯一主体，并同时约束 Candidate、所选 FAQ 问答单元和 Fact grounding。
  `停车免费吗` 不能使用“早餐免费”的 FAQ，`有早餐吗` 不能使用“有晚餐”的 FAQ；
  比较题、多主体或无法唯一恢复主体时不启用该保护性校验。

本节没有新增关键词业务分类器、持久化 Task 状态、模型调用、数据库字段、知识库规则、
人工状态、Outbox 语义、模型配置或外部接口。2026-08-31 重新同步远端后，本次 15 个
已跟踪待提交文件与 `origin/codex/customer-audit` 只重叠
`docs/development-handoff.md`，与 `origin/codex/ai-billing` 重叠为 0。
