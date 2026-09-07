# 服务知识优先与发送关联修复

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
