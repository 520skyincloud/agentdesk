# JEV Intent 接入记录

## 目标与基线

- 只替换 Intent 模型的请求协议、拆题、分类和上下文选择；原 Task、知识检索、
  PMS 只读、Judge、Generate、Commit、Outbox 契约不变。
- 基于独立工作树 `zhixiweibao-pms-live` 的
  `b9cc11c0e1fe534bf9230442273d56f522af5fd7`，不修改主客服审计脏工作树。
- 接入前备份名为“薇薇2”，tag 为 `backup/weiwei2-20260922-b9cc11c`。

## 薇薇2备份

- 本地：`/Users/qifeng/Documents/agentdesk-backups/weiwei2-20260922-b9cc11c`。
- 服务器：`/opt/agentdesk/backups/weiwei2-20260922-b9cc11c`。
- 运行副本已下载到本地 `remote-runtime`；包含实际运行库、完整 shared 数据和附件、
  shared 配置与凭证、systemd 配置、release；源码和 Git bundle 在本地父目录。
- 以 `COMPLETE-MANIFEST.json`、`RESTORE-IMPORTANT.md`、`SHA256SUMS.complete`
  为准，22项文件远端和下载后均通过 SHA-256，gzip 完整性通过。
- 原 `database.sql.gz` 是旧库，不能恢复当前服务。实际运行库文件明确命名为
  `database-RUNTIME-cs_ai_agent_ai_billing_4db7993.sql.gz`，89张表，
  2026-09-22 14:14:54 至 14:14:57（北京时间）事务快照。
- 未执行实际恢复演练；外部 FastGPT、企微和 HPMS 不属于本机快照范围。

## 实现边界

- JEV 使用官方 `POST /v1/systemone` 的 `state/questions/answers` 协议，
  不是 OpenAI chat-completions 兼容接口。
- 第一步 choice 确定当前来源的题数；多题再用 ordinal choice 选择原文起点；
  只有 JEV 明确确认“每个终止标点段恰好对应一个完整问题”时才使用机械边界，
  避免把句号后的手机号、日期等补充字段错切成新任务。最后统一 choice 分类、
  上下文选择，noul 标记是否同时需要政策。
- 单条物理消息允许多题；文本、顺序和 URef 均来自客户原文。长文本的选项分组只
  规避单个 choice 的255选项限制，不截断消息。超过32个目标明确返回失败，
  不能静默丢题。
- 上下文携带角色和稳定引用，只能选择已存在的客户文本；客服答复用于解释追问，
  不当作客户提供的手机号、订单或确定事实。缺少工具参数不等于意图未理解。
- Judge 发现缺题、合题或检索目标偏移后，原有唯一一次覆盖修复仍然生效：
  previousTasks、previousIntentTasks 和 coverageIssues 作为结构化 state 交给 JEV，
  只修复反馈项并保留其他任务。
- JEV 启用时不调用旧 JSON 意图模型或其修复提示词；失败不暗中回落旧模型。
  其他环境仍可保留原实现，不修改其配置。
- `AIUsageEvent` 复用原记录接口，记录每个批次的实际模型版本和 usage；
  环境覆盖的 JEV 调用不冒用旧模型的 AIConfig ID 或凭证版本，不修改计费计算。
- JEV 响应必须包含完整 usage；typed answer 校验失败也保留已解析的模型和 token
  用于失败事件。远端地址只允许 HTTPS，HTTP 仅允许 loopback 单元测试；未知
  Intent provider 配置直接失败，不静默落到其他模型。
- 修复原共用 `IsExplicitHumanHandoffRequest` 将否定/取消误当授权的问题。
  不新增人工判断阶段；知识驱动转接、安全转接及员工接管流程不变。
- 无 models、DDL/DML、DTO、API、WebSocket 或 Outbox 修改；Provider 枚举
  仅新增 JEV 名称用于内部记录，不把 JEV 注册成 Generate/Judge 可选的聊天模型。

## 配置与凭证

只在 test-2 的独立 systemd 环境文件设置：

```text
AGENT_DESK_INTENT_DETECT_PROVIDER=typesafe_jev
AGENT_DESK_JEV_API_KEY=<受控凭证>
AGENT_DESK_JEV_BASE_URL=https://api.typesafe.ai/v1/systemone
AGENT_DESK_JEV_MODEL=jev-latest
```

不继承其他模型的 APIKey/BaseURL；不把密钥写入代码、Git 或普通日志。
PMS 写入开关继续关闭；回复和 Judge 模型配置保持原值。

## 验证

自动测试覆盖 typed HTTP 协议、usage、缺失字段和错误响应、无凭证泄漏、来源顺序、
上下文纠正、跨句补字段、多题、资源映射和否定人工请求。真实调用只使用合成中文会话，
通过 `AGENT_DESK_JEV_LIVE_TEST=1` 显式启用，普通测试不发网络请求。

```bash
go test -p=1 \
  ./internal/ai/jev \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime/tools \
  ./internal/pkg/replyintent \
  ./internal/pkg/toolx \
  ./internal/pkg/utils \
  ./internal/pms \
  ./internal/services \
  -count=1
git diff --check
```

初次实测发现逐字独立 noul 会将中文词句过度拆碎，该设计未部署，已由
题数与有序起点 choice 替代。2026-09-22 最终本地模块回归全部通过；真实 JEV
合成用例连续通过3轮，覆盖手机号补充与纠正、拒绝转人工、升房会员、多问题、
无标点多问题、天气、入住小程序，以及“句号后补手机号再问停车场”的边界反例。
部署和服务端隔离冒烟结果在发布后补充。

## 并行协作与回滚

已 fetch 并检查 `customer-audit` 与 `ai-billing`。共享高风险点是 Intent 入口、
provider 枚举和人工授权工具函数；合并必须保留其他分支的模型解析与租户权限。
本轮按一个可回滚的集成提交交付，无依赖 migration，不整文件覆盖并行分支。

程序回滚点为 `/opt/agentdesk/releases/20260921-pms-route-b9cc11c`；
移除本轮独立 JEV systemd drop-in 后切回该 release 并重启即可。
不得恢复旧数据库覆盖接入后消息，不改“薇薇”及“薇薇2”备份。
