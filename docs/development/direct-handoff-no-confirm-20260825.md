# 直接转人工取消二次确认交接

## 目标

生产基线 `925d16f0738afc1abcf80c0d729690de5a37906c` 上取消转人工的“确认/取消”步骤，并保留原有房号补充、真实路由、人工超时恢复和客户取消人工后的 AI 恢复能力。

## 行为

- 无需房号：立即执行真实转接，成功后只发送 `帮您转接同事啦～`。
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

## 回滚

本次无数据变更。生产异常时只需把 `/opt/agentdesk/current` 原子切回：

```text
/opt/agentdesk/releases/20260821-131711-deferred-prompt-925d16f
```

部署前备份位于 `/opt/agentdesk/backups/pre-direct-handoff-20260825-082955`，Git 备份标签为 `backup/pre-direct-handoff-20260825-162712-925d16f`。
