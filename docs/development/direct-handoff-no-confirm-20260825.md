# 直接转人工取消二次确认交接

## 目标

生产基线 `925d16f0738afc1abcf80c0d729690de5a37906c` 上取消转人工的“确认/取消”步骤，并保留原有房号补充、真实路由、人工超时恢复和客户取消人工后的 AI 恢复能力。

## 行为

- 无需房号：立即执行真实转接，成功后只发送 `帮您转接到同事了`。
- 缺少房号：只发送 `方便说下是哪个房间吗？`，客户补充房号后立即转接。
- 已在人工状态：复用当前人工路由，不重复发送成功消息、门店通知或创建恢复任务。
- 非服务时间：沿用真实非服务时间提示，不发送转接成功话术。
- 旧版已发出的确认消息在原五分钟有效期内仍可消费；新请求不再创建确认状态。

## 改动边界

- 数据与接口：无表结构、Migration、DTO、枚举、HTTP、WebSocket 或前端变更。
- 权限与配置：无变化。
- AI 链路：Intent、Judge、Generate 的业务判断、知识检索、多问题拆分、语音/图片、Task、Commit、计费和模型配置均未改变；仅把各入口、工具说明和运行追踪中的“确认后转接”契约同步为直接转接。
- 发送可靠性：转接成功消息与门店/总部通知使用稳定标识；重试可补建缺失 Outbox，不重复创建消息、通知或人工恢复任务。
- 建工单确认流程未改变。

## 验证

```bash
go test -p=1 \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  -count=1

go test -p=1 \
  ./internal/ai/runtime/graphs \
  ./internal/ai/runtime/tools \
  ./internal/pkg/toolx \
  -count=1
```

覆盖直接转接、房号补充、已带房号、多问题延后转接、重复 Job 幂等、成功消息 Outbox 自愈、门店/总部通知幂等、不同来源已在人工状态、可用客服组路由、非服务时间、旧确认兼容和准确成功话术。

## 并行分支

本次修改的回复引擎文件与 `origin/codex/ai-billing`、`origin/codex/customer-audit` 存在同文件演进，后续合并应以本次提交为独立补丁逐文件处理，不建议整分支直接覆盖。无 model、migration 或对外契约冲突。

## 2026-09-03 转接成功话术调整

- 运行提交：`45d5e4c11340d3cf5201a7cdcd8fd06fb40cd84f`。仅修改转接成功常量、精确文案测试及本文档；房号收集、真实转接、人工状态和恢复逻辑未变。
- 无数据库、Migration、权限、配置、DTO、枚举、HTTP、WebSocket、模型、知识库或计费变化。
- 聚焦测试通过：`go test -p=1 ./internal/services ./internal/ai/runtime ./internal/ai/runtime/executor -count=1`。
- 生产 release：`/opt/agentdesk/releases/20260903-055023-handoff-copy-45d5e4c`；Server SHA-256：`727426614a5b85f5f98330999a872d2db799d917f311f2f01d8a4b175da7184d`。
- 回滚点：`/opt/agentdesk/releases/20260903-031918-judge-thin-7336d13`。本次无数据变更，异常时只需原子切回旧 release。
- `origin/codex/ai-billing` 未修改目标 service；`origin/codex/customer-audit` 有该 service 的历史演进，合并时只带入本次常量补丁，不覆盖其租户路由修改。

## 回滚

本次无数据变更。生产异常时只需把 `/opt/agentdesk/current` 原子切回：

```text
/opt/agentdesk/releases/20260821-131711-deferred-prompt-925d16f
```

部署前备份位于 `/opt/agentdesk/backups/pre-direct-handoff-20260825-082955`，Git 备份标签为 `backup/pre-direct-handoff-20260825-162712-925d16f`。

## 2026-08-26 六类回复链路修复

### 目标与实现

基于生产提交 `b60bacd1d9202bc7f25670889ce026d6c79205c7` 完成局部修复，正常主链路仍为一次 Intent、并行知识检索、一次既有 Judge、一次 Generate、Commit 和 Outbox，没有新增模型阶段或正常路径调用。

- 极短的“转接、人工、找客服、接同事”等明确人工请求由 Intent 识别为 `human_complaint_risk/explicit_handoff`，直接复用真实转接服务。
- 可信企微员工 self echo 在消息事务内锁定路由并切换 `StoreWecomManual`；客户或员工的新消息都把人工空闲截止时间续到消息时间后十分钟。
- 普通 AI Commit 和 Outbox 发送前均复核实时路由；员工接管时取消排队或重试中的普通 AI Outbox，真实系统服务通知保留。
- 人工超时扫描使用条件更新和路由锁；最后一条为员工消息时静默恢复，最后一条为未答客户消息时创建真实 AI 续答任务。客户在恢复任务 `ready/retry/running` 期间补发消息时，同一事务会把任务退回等待并更新来源，旧执行不能提交旧答案；首次转人工与客户补发并发时也只会保留一条恢复任务。
- Judge 升级为 `knowledge_evidence_judge.v2`，按门店/通用知识层分别选择 `direct_single`、`direct_combined` 或 `insufficient`，Generate 只读取选中的证据。
- FAQ 的问题和答案按完整语义理解；被“是的/可以/不需要”等回答确认的对象、数量和条件可作为事实，答案为“转接”时不得把问题前提当事实。
- 单任务和多任务统一拆包 `replyParts`；Markdown、字符串包装、裸 `taskId/content` 对象或数组等内部协议外形无法安全解析时触发最多三次的异常路径有限重试，Commit 还有最终拒绝保护。普通模型错误不重试，每次协议重试前都会重新检查人工路由和来源消息。
- 默认及生产生效人设调整为礼貌、温和、有耐心，允许自然使用“您、为您、这边、呀、啦、～”，但禁止堆叠和虚假承诺。
- 评测清理补充 `t_ai_manual_resume_task`，隔离转接场景不再遗留恢复任务。

### 数据、接口与权限

- 无数据库表结构、Migration、外部 API、DTO、WebSocket、权限或前端变化。
- 内部契约新增 Judge V2 分层选择和 Outbox `cancelled` 状态；已有数据无需迁移。
- 部署时需要同步生产 Intent Profile 1 和企微实例 7 的生效人设；修改前备份位于 `/opt/agentdesk/backups/pre-six-class-repair-20260826-011729`。

### 验证

```bash
go test -p=1 \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  -count=1

go test -p=1 \
  ./internal/ai/runtime/internal/impl/retrievers \
  ./internal/ai/runtime/internal/impl/callbacks \
  ./cmd/reply-runtime-eval \
  -count=1
```

生产同配置、`ChannelID=0` 的隔离真实模型验证通过：

- `S31` “转接”：`human_complaint_risk/explicit_handoff`，4.984 秒，直接回复转接成功话术。
- `S32` 沙发和办公桌组合：正确回答“合柴和艺林”，6.512 秒。
- `S33/S34/S35` 矿泉水数量原句和两种改写：均回答“两瓶”，7.662 到 8.282 秒。
- `C02` 三个连续酒店问题：生成三条对应答案，10.178 秒，没有漏答或泄漏内部 JSON。

### 已知风险与并行分支

- 同一实体、同一房型和同一适用范围能否组合仍由既有 Judge 模型按严格提示裁决；代码保证不跨门店/通用层，并保留非法协议回退和追踪。
- 多问题真实模型样本为 10.178 秒，略高于十秒目标约 0.18 秒；没有新增串行模型步骤，延迟主要来自三个并行知识任务及模型网络波动。
- 本次与 `origin/codex/ai-billing`、`origin/codex/customer-audit` 存在回复引擎和 service 同文件演进，合并时应按独立补丁逐文件处理；无 model、migration 或外部契约冲突。

### 回滚边界

代码异常时原子切回部署前 release；Prompt 异常时恢复已备份的 Intent Profile 和企微实例人设。本次无数据库结构变化，不需要反向 Migration。
