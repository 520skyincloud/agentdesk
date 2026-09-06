# 回复链路有界修复

基线：`002d638`，运行实现 `3cbee83`。依据 2026-09-05 三会话90轮的已保存证据，
不重复长对话测试，真实代表测试最多12轮。该预算已用完，不得自行追加真实模型轮次。

## 1. 人工路由

- `intent_human_route.go` 与 `reply_trigger_service.go`：直接和延迟转接复用同一房号策略；
  只按待转任务决定是否需要收房号，旧 Trace 缺字段时保留旧行为。
- `conversation_handoff_confirmation_service.go`：明确取消口令不依赖模型；
  长句仍由现有分类模型判断，单次预算由2秒改为5秒。失败保留人工状态，
  仅当消息首句含现有明确取消口令时，用系统通知和稳定ID告知取消方式；
  普通人工聊天不因此收到AI提示。降级仅识别完整口令，不再对子串做语义判断。
- 验证：房号策略一致性、直接/延迟转接、明确取消、分类不可用及提示幂等测试。
- 数据、权限、外部接口、模型配置和迁移无变化；不修改十分钟恢复、Outbox或计费。
- 并行分支：`customer-audit` 同文件修改租户隔离，当前修改不涉及该区域；
  `ai-billing` 无相关差异。独立提交可 cherry-pick，合并时保留双方修改，
  不为本轮部署合并审计分支。回滚只撤销本提交，不恢复数据库。

## 2. 上下文与证据

- `intent_model_detector.go`、`intent_protocol_validation.go`：保留模型拆题权，
  跨轮回指只要求实际有界历史，不硬性要求紧邻完整问答对；answer_rejected仍要求紧邻AI。
  Intent使用现有历史中的最近15条，不扩大配置窗口，不删除消息或新增已答状态。
- `context_builders.go`：普通追问使用最多8条已有历史。仅模型明确分类的会话回顾，
  额外读取当前session最多64条消息；每条300字、正文6000字预算。超出范围明示边界，
  不把历史重新当作待回答任务。
- `intent_model_detector.go`、`answerability_gate.go`及内部Trace类型：可选 `evidenceQuery`
  传递模型给出的当前检索目标，`resolvedText`完整保留对象与条件交给Judge；
  旧Profile未输出时使用既有查询，外部代操作继续复用原自助检索方式。
- `knowledge_evidence_judge.go`：明确放弃条件不再算缺证，同对象A/B证据不需第三条
  “同时具备”证明；每层独立完整性与配置字段边界由Judge判断。记录协议拒绝的具体字段
  或数量关系到既有Trace errorMessage，不额外记录原始模型全文、不凭猜测放宽协议。
- `multi_reply_output.go`、`generate_recovery.go`：对携带真实选中层、候选ID和事实的
  任务，直接组装已裁决Statement，Generate不再改写事实。保留Task和FactID校验、
  关键值与内部内容发送安全。互动仍自然生成，旧未带选中证据的调用保持兼容。
  外部代操作的可选自助事实与同层独立任务完全相同时，只在独立任务回答一次。
- 验证覆盖新旧协议兼容、上下文窗口/回顾预算、查询与条件分离、六题有序合并、
  事实原义、代操作补充归属、协议异常诊断与内容泄漏防线。
- 并行与回滚边界沿用上一节；无模型、数据库、知识库或外部DTO变化。

## 检索失败证据

旧B3在服务器日志 `2026-09-05 16:34:22 +08:00` 对应门店库3的FastGPT搜索POST返回EOF，
不是Judge错判，也不是分数不足。本轮不改检索阈值或为一次网络断连新增模型调用。
源服务可用性仍属于真实测试和上线观察项。

## 部署与验证

`1b43afb`、`65b19f7` 已推送至 origin/weibao，并部署至
`/opt/agentdesk/releases/20260905-171201-bounded-reply-65b19f7`。
其部署前备份为 `/opt/backups/agentdesk-20260905-171201-pre-bounded-reply`；
服务 active、HTTP 8083=200、NRestarts=0。未修改或恢复数据库、运行配置。

隔离会话 `2100` 实际完成12个客户轮次，消息17761至17788保留，
测试结束仅清理该隔离会话路由和恢复任务，已确认回到 AI_SERVING。
原始记录：服务器 `/tmp/agentdesk-bounded-65b19f7-20260906.jsonl`。
这不是12/12通过，也不是企微最终出站验收：

- R1 同房型办公桌和沙发组合可答；R2 空调、水数量费用、发票均保留，
  但发票重复退房条件，运行耗时约18.27秒，延迟仍有问题。
- R3 外部代操作真实边界与地址只回答一次；R4 酒店机器人配送能力错误分类为
  external_proxy_action，错误复述代点边界和地址，属于 Intent 主体/目的判断错误。
- R5 模型已输出 objective=cancel，旧归一化仍按 explicit_handoff 发起转接。
- R7 检索日志7993的门店5条全部为房型/办公桌/沙发，没有停车规则；
  evidenceQuery错误保留房型名。此例不是Judge拒绝了正确停车答案。
- R6/R8/R11被实际人工状态替换为取消操作，因此放弃沙发条件、WiFi起始问题和
  会话回顾未有效覆盖。R9/R10也不能作为完整WiFi连续追问测试；
  R9仍出现未经支持的门锁核对措辞，需要后续验证。
- R12 平台政策和知识中的驾车方式原义保留，无据部分进入现有接待。
  本批没有复现此前Judge协议错误，不能据此声称其所有不稳定问题已消失。

## 3. 代表测试后的限定收尾

只再修改 `intent_model_detector.go` 及对应 `intent_pipeline_test.go`：

- 按已存在的 `human_complaint_risk/explicit_handoff + objective=cancel` 字段，
  将撤回人工意愿留作互动，不得反向授权新转接；保留原任务、原话和来源。
  不做中文匹配，不屏蔽取消订单等业务动作，也不屏蔽同轮独立安全/人工请求。
- 替换原有Intent说明，明确询问酒店设备/服务能力与委托第三方代操作的区别，
  不从外卖等话题或上一轮动作自动继承代操作，不擅自增添执行主体。
- 检索目标以当前服务为中心；公共政策查询的房型背景仍保留在resolvedText供Judge
  判断适用性，而不是干扰召回。房型自身设施和不同区域配置仍保留目标对象。
  不增加本地语义分类、查询次数或放宽Judge。

自动回归通过：

```bash
go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1
```

回归覆盖撤回人工、新旧Intent契约、混合知识问题、独立安全请求、
取消订单、原有直转/送物/资源动作和既有整包测试。提示词断言只验证说明确实传入，
不等于模型分类已真实通过；收尾补丁的模型效果仍需用户另行批准少量验证。
本轮不再追加真实测试，不为发票冗余、延迟或未覆盖案例扩展修改。

收尾发布沿用原子切换与失败回退；新release的COMMIT及部署命令输出为发布身份依据，
回退点为上述65b19f7 release，数据库始终保留现状。
无新增数据、权限、接口、模型配置或迁移；已fetch并检查并行分支，收尾的Executor
文件无同文件修改，无需为本轮rebase；独立提交可cherry-pick。

收尾代码 `23b6a3b` 已推送两个远端并部署：
`/opt/agentdesk/releases/20260905-173535-bounded-reply-23b6a3b`。
备份：`/opt/backups/agentdesk-20260905-173535-pre-bounded-reply`。
SHA-256：`ea359feafe2ecac1ad7342c5015e4b8f779a0b88845f5c2cdcd35f05bbd41dd8`。
切换后复核active/running、HTTP 200、NRestarts=0，启动后的warning级日志为空。
部署静默查询已使用与应用TZ=Asia/Shanghai一致的MySQL会话时区，
避免UTC的NOW将本地时间消息误判为新消息；未修改数据库全局时区、数据或程序配置。
此发布之后没有追加真实对话，模型效果和上述覆盖缺口仍未验收。

## 4. 单会话30轮后的请求范围修复

基线 `23b6a3b` 的隔离会话2101已完成30轮，记录保存在本机
`客服测试记录/2026-09-06-单会话30轮`；没有空回复或协议泄漏，但存在请求扩大、
咨询误问房号、事实重复和锁定证据无效重试。本轮复用记录，不重跑30轮。

- Intent仅补全指代，保留客户的回答范围，不把存在性扩成配送能力或执行请求。
- Judge输入分开传递原话question和resolvedQuestion，当前sourceContext不再伪装成改写后的原话。
- 房号授权按hotel_info类别排除咨询，不再依赖objective白名单；实际service_request、
  明确人工、安全和缺少Task元数据的旧路径保持原行为。
- 目标文件为Executor的Intent、Judge输入、answerability和房号策略及对应测试。
  不改变模型、数据库、知识库、外部接口、权限、迁移或发送状态机。
- 已fetch，customer-audit/ai-billing无上述运行文件同文件差异，无需rebase；
  本步骤可单独cherry-pick或回退，先合请求边界再合表达改动。
- 验证先执行Executor相关自动回归；本轮真实新增验证最多10个客户轮次，
  不能把提示词断言当作真实模型验收。

## 5. 证据与答复分开

- 仍由同一次Judge返回每层的supportedFacts、missingAspects以及可选answerText。
  AnswerText经内部Trace传至当前Task；失效、转接和外部代操作无证据转换时一起清空，
  禁止旧层或旧任务的答复残留。不加数据库列或外部DTO。
- 普通知识直接使用选中层的简短答复，证据仍保留用于机械关键值检查；旧结果无该字段时
  继续原有Statement组装。partial答复自然说明缺失方面，不擅自肯否或承诺已通知。
- 代操作任务的空answerText表示自助信息由同轮独立Task回答，仅输出固定能力边界；
  旧结果仍使用原精确重复检查，不新增中文语义去重器。
- 锁定输入错误使用独立内部错误标识，Generate不无效重试；先尝试已知事实安全兜底，
  真正缺失的关键值或内部协议不能跳过。Generate自身漏Task/FactID的既有重试保留。
- 新字段涉及`callbacks/trace_callback.go`，只向后兼容新增内部JSON字段，不影响
  API、WebSocket、DB、权限、计费或模型参数。生成阶段、检索量和历史窗口不变。
- 自动回归覆盖新旧Judge协议、胜出层传递、原话限制、咨询/服务房号、三任务有序输出、
  数量费用完整、代操作地址归属、partial解释、锁定输入不重试、协议泄漏及密码标点。
- 并行分支检查无Executor/callback同文件差异；表达步骤独立提交，依赖上一请求边界提交。
  回滚仅切回23b6a3b程序；不恢复数据库、模型配置或知识库。

自动验证已通过：
`go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services ./internal/ai/runtime/internal/impl/callbacks -count=1`。
此处仅记录自动测试，不代表后续服务器真实模型验证已通过。

## 6. 本轮发布与10轮验证

请求边界提交`a30ed40`、表达提交`c42cb84`已推送origin/weibao，
再次fetch后与customer-audit/ai-billing无相关同文件差异，不需要为本轮rebase。

- Release：`/opt/agentdesk/releases/20260906-101045-bounded-reply-c42cb84`
- 备份：`/opt/backups/agentdesk-20260906-101045-pre-bounded-reply`
- SHA：`a98e578dbdb18e66032e94b7b730c862dfff829ffa98177424da6d8b4ad50c74`
- 原子切换前校验旧release及消息静默，保留23b6a3b回退点，未恢复或修改运行配置。
- 发布及验证后active/running、8083可用、NRestarts=0，warning日志为空。

隔离会话2102在同一窗口完成10轮，客户17862至17885，末条回复17886。10轮均有回复，
9个AgentRun/Generate均只执行一次，另1轮是服务直接取消接待；无协议泄漏、无Generate
重试或事实兜底。机器人存在性未误转，范围未知正常解释并转接、不问房号，取消恢复AI。
房型条件切换、办公桌/停车分答、水数量费用、拖鞋和发票等4Task/3消息均保留；
景点只列名称。第4轮账号复述由互动上下文完成，没有调用Judge；第6轮代操作和地址
被模型合为1个Task，因此两Task地址归属只算自动测试覆盖，不算本次真实覆盖。

未解决项：人物身份答复仍偏长；复杂多问运行耗时20.672秒（Judge8.356秒、
Generate1.759秒），不能宣称性能达标。没有为这两点继续扩大代码或追加模型测试。
ChannelID=0，不代表企微最终投递/员工self echo验收。实际送物房号保留自动回归，
本轮真实测试不重复上轮30轮已覆盖的送物/故障路径。

完整记录：本机`客服测试记录/2026-09-06-请求边界10轮`；
服务器`/tmp/agentdesk-scope-answer-live10-20260906.jsonl`。
测试后仅清理该隔离会话路由和恢复任务，历史全部保留，AI_SERVING、pending_action为空。
本轮到此停止，没有第11轮或新的30轮测试。
