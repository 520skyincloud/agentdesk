# 测试 PMS 六场景实施记录

## 范围

仅 test-2。现有 Intent 拆题，Executor 固定执行测试业务查询，客户确认已投递方案后办理。沿用 Commit、Outbox、员工接管和稳定消息 ID。不改外部 HPMS、生产或“薇薇”备份，不复制真实住客资料。

## 进度

- [x] 保留已有工作区修改并建立独立分支。
- [x] fetch origin 并核查 customer-audit、ai-billing。
- [x] 六场景固定执行及混合问题处理。
- [x] Sandbox 数据、事务办理、后台与权限。
- [x] 自动测试与前端验证。
- [x] 备份、发布并完成服务、端口和 Sandbox 数据源健康检查。
- [ ] 最多 12 个真实输入验证（等待测试会话逐项执行）。

代码和自动化验证已完成；部署前仍需在 test-2 备份当前运行版本、初始化演示数据，并
完成真实消息、办理回读和枕头商品卡片收件端验收。未完成这些步骤前，不把六场景标记为
生产可用。

## 配置边界

`pms.environment=test-2` 且 `pms.sandboxEnabled=true` 才开放测试后台。
`pms.enabled=true` 且 `pms.provider=sandbox` 才切换会话数据源。
Provider 由服务端选择，Sandbox 不可用时不能回落 HPMS。

## 共享影响

新增兼容 SQLite/MySQL 的 Sandbox 表，通过 AutoMigrate 建立；扩展 PMSOperation，不改变已有消息及 Outbox 契约。新增门店权限内的后台 DTO/API。旧空 Provider 记录解释为 HPMS，并且只由外部续住入口处理。

customer-audit 在 models、routes、权限、导航、reply_trigger、资源服务有并行变更；该分支的租户化修改未合并，不能整体 cherry-pick 覆盖。本次在独立分支完成新增业务契约，合并时先处理表/权限/路由，再接业务与 UI，逐项校验门店权限语义。ai-billing 当前没有本次运行文件的新增并行修改。

## 验证

聚焦 config、PMS、repositories、services、tools、executor、runtime 和新增 dashboard 路由测试；前端 typecheck/build 及桌面、移动端页面检查。真实验收分开记录分类、逐题回复、数据变化、回读和实际送达，未确认收件端打开的枕头卡片不能记为完成。

最近一次自动验证：

```bash
go test -p=1 \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./internal/pms \
  ./internal/repositories \
  ./internal/handlers/dashboard \
  ./internal/bootstrap \
  ./internal/pkg/config \
  ./cmd/reply-runtime-eval \
  -count=1
git diff --check
cd web && pnpm typecheck
cd web && pnpm exec eslint \
  app/dashboard/pms-sandbox/page.tsx \
  app/dashboard/pms-sandbox/_components/edit.tsx \
  lib/api/pms-sandbox.ts
```

上述自动检查已通过。真实 test-2 验收仍必须逐项记录：

| 项目 | 状态 |
| --- | --- |
| 分类正确 | 待部署后验证 |
| 逐题回复完整 | 待部署后验证 |
| 后台测试数据真实变化 | 待部署后验证 |
| 办理结果回查一致 | 待部署后验证 |
| 企微实际送达及枕头卡片可打开 | 待收件端验证 |

## 2026-09-15 演示问答收口

演示场景的客户文本改为一问一答：A 订单早餐与儿童政策、E 会员与生日权益、F 枕头商品使用固定短答；B 升房、C 换房、D 服务补救继续使用实际方案预览，客户确认后才办理。预览文案只保留办理事项、费用结论和确认动作，不再把后台方案原文整段发送。枕头文本答复与真实商品卡片仍由同一次 Commit 分开发送。

## test-2 发布记录

- 程序提交：`1ce3581d0a7f671851cd352472e773f5cc3490e0`，已推送 `origin` 和 `weibao`。
- 发布 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-1ce3581`。
- 原 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-a0ba8ce054e7`。
- 新二进制 SHA-256：`79cb12edc0dcc860c5b10e4bc1cc4f41af3a80b339b723d0ba91d7078a01d173`。
- 发布前备份：`/opt/agentdesk/shared/backups/pms-sandbox-six-scenes-20260915-final-predeploy`。
- 数据库备份 SHA-256：`2083fd6dfd1aa53186e2b9c559acd7971a20ca24080a2845ad122c139e7ff7c2`。
- 切换后服务 active/running、8083 返回 200、`NRestarts=0`；数据库迁移 77 已成功。
- test-2 已使用 `provider=sandbox`、`environment=test-2`、`sandboxEnabled=true`、`allowWrite=true`。
- 服务器当前已初始化 1 个测试批次和 1 笔测试订单；当前绑定仍是客户 ID `6`，最新“其风”客户 ID `1811` 尚未绑定。
- 枕头资源当前没有真实历史商品卡片（`sourceMessageID=0`、无 `cardPayload`），因此枕头卡片收件端验收尚未通过。
- 服务器既有企微坐席过期告警（错误码 9003）与本次程序发布无关；未修改企微登录状态。

本记录只证明程序发布和数据库备份完成，不代表六场景真实消息验收已经完成。绑定最新测试客户、导入同门店历史枕头商品卡片并完成不超过 12 轮的隔离真实验证后，才更新上方五项验收状态。

## 2026-09-15 一问一答版本发布

- 程序提交：`63a1c89`，已推送 `origin` 和 `weibao`。
- test-2 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-63a1c89`。
- Linux amd64 二进制 SHA-256：`378f14197dfd0cf23b8c2e0491688ee1eac773e55739731192963084d5de6ead`。
- 切换前 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-1ce3581`，保留为回滚点。
- 切换后 `agentdesk.service=active`、8083 返回 200、`NRestarts=0`；仅更新演示答复文案和测试断言，数据库与外部 HPMS 未修改。

## test-2 发布记录

- 发布日期：2026-09-15。
- 新 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-a0ba8ce054e7`。
- 二进制 SHA-256：`a0ba8ce054e76d679c3f3dd09172cfcc1a8ea51dd7a10adb8afcd0f642312bd0`。
- 切换前 release：`/opt/agentdesk/releases/20260915-member-query-6633836`。
- 部署前备份：`/opt/agentdesk/shared/backups/pms-sandbox-six-scenes-20260915-predeploy`，数据库 gzip 已完成 SHA-256 校验。
- 当前 test-2 已切换 `pms.provider=sandbox`、`pms.environment=test-2`、`pms.sandboxEnabled=true`，`allowWrite=true`。
- Sandbox 已初始化门店 1 数据批次 1，并绑定“其风”客户到演示订单和会员；聊天消息未改动。
- `agentdesk.service=active`、`NRestarts=0`、8083 返回 200。
- 已确认 test-2 当前历史没有合法枕头 `shop_product` 或可复用的枕头小程序卡片；F 场景保持明确“尚未绑定原商品卡片”，不伪造发送。

## 回退边界

关闭 Sandbox Provider 或回退测试程序，保留消息和操作审计。不得恢复旧客户数据库；旧测试方案在重置数据后失效。外部 HPMS 和生产从未切换。

## 固定演示文案更正

本节按用户后续确认的纯回复范围实施，覆盖前文“确认后办理”的演示说明；不新增办理流程。

- B 固定回复改为：`可以的，你是会员，可以为您升级大床房`。
- E 生日礼遇统一为 `50元券礼遇`，有效期30天；单问生日和会员/生日组合问法使用同一文案。
- F 已核对 test-2 门店1、资源1及原商品消息10863：商品为“丽斯严选零压力护颈椎枕头”，原始价格字段为 `18018`。按本次确认的小数点更正，固定回复为 `售价180.18元`；不再直接把原始价格字符串后面拼接“元”。
- 原商品卡片及其来源引用保持不变，仍由现有 Commit/Outbox 发送。此固定价格仅对应当前演示商品，后续换绑商品须同步核对文案。
- A 停车场、C 换房、D 空调补救文案不变；不附加确认办理提示。

变更文件仅为 `internal/services/pms_sandbox_scene.go`、对应服务测试及本文档。
无 model、migration、DTO、enum、API、权限、WebSocket、数据库数据、PMS配置或外部HPMS变更。
已 fetch origin；customer-audit 与 ai-billing 没有上述文件的并行新增修改，无需先 rebase，本次独立文案提交可单独合并或回退。
验证命令：`go test -p=1 ./internal/services ./internal/ai/runtime/executor -count=1`。
回退仅切回发布前 test-2 release，保留消息、卡片资源与审计，不恢复旧数据库。

发布结果：

- 程序提交 `ba6e7c4`，已推送 origin、weibao；上述服务与 Executor 测试均通过。
- test-2 当前 release：`/opt/agentdesk/releases/20260915-pms-demo-copy-ba6e7c4`。
- 回滚 release：`/opt/agentdesk/releases/20260915-pms-sandbox-six-scenes-9455e36`，未改动。
- Linux amd64 二进制 SHA-256：`92bd27e7a6781950ad067f8558173c92f78eafe00c4d07d5ce4bfa8f9da98fae`，上传、安装和运行文件一致。
- 服务 active/running，8083 HTTP 200，后台鉴权接口返回预期未登录 JSON；重启次数0，启动日志未发现 panic。
- 本轮未额外发送真实客户消息；固定文本和卡片引用保留已通过自动测试，不将其描述为收件端实测。

## 六个原句逐字命中

- 原因：之前仍依赖 Intent 模型先分类，A 的停车原句可能未进入 Sandbox，E 也可能被拆成两个不同回复。
- 新增 `internal/pms/sandbox/demo.go` 作为六组批准问答的唯一文本来源。六个完整原句只归一化输入空白及中英文常见标点，不用关键词/子串吞掉其他问题；输出不归一化、不改写。
- Executor 在 `buildRunMessages` 前命中原句，直接构建固定文本 Task，跳过 Intent/Judge/Generate。A-E 不要求订单或会员绑定；F 只读取当前门店已绑定商品资源，仍由原 Commit/Outbox 发送。
- 命中但商品卡片缺失时返回可追踪错误，不回退让模型编写不同话术，也不把文本口令当卡片成功。
- 仅 `config.PMSSandboxEnabled()` 的 test-2 生效。未命中的问题、员工接管、来源消息、历史记录、稳定消息 ID 和 Outbox 契约不变。
- 不增加 model/migration、DTO、enum、接口或运行配置；共享入口 `executor/service.go` 仅增加命中即返回的调用，不改模型配置、token统计与计费语义。
- 已 fetch origin，customer-audit 与 ai-billing 在本次文件没有并行新增修改；本次可独立合并/回退，无需前置 rebase。
- 验证：六个原句逐字相等、无模型调用、F 原资源引用、未绑定订单/会员可答、额外问题不被整轮拦截、非 test-2 不启用。
- 命令：`go test -p=1 ./internal/pms/sandbox ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1`。
- 2026-09-16 上述四个包全部通过；连续消息先按现有来源解析去除内部包络，E 分两条输入仍逐字命中，附加问题或图片不被忽略。`git diff --check` 通过。
- 发送链只读复核：固定文本经 Commit/Outbox 不增加前后缀，商品仍发送原 `shop_product`；员工接管与现有人工恢复通知逻辑不变。本轮不新增业务写入，也不替代收件端实测。

### 逐字问答发布结果

- 2026-09-16 已提交并推送两个远端，程序提交 `0720750`。
- test-2 原子切换至 `/opt/agentdesk/releases/20260916-pms-demo-exact-0720750`；回退点保留 `/opt/agentdesk/releases/20260915-pms-demo-copy-ba6e7c4`，未修改其文件。
- Linux amd64 二进制 SHA-256：`c3b6cb96e70f595073d411e06ff235e81037f76c30e6fc8d051295e5b3315547`，本地、上传、安装后校验一致；进程实际执行文件指向新 release。
- 服务 active/running，8083 HTTP 200，后台接口返回预期未登录 `3000`，NRestarts=0；启动日志无 panic/fatal 或监听失败。
- 配置仍为 test-2/sandbox，PMS 与 sandbox 开关均已启用；未改配置、数据库结构、PMS 数据、会话路由或历史消息。原商品资源仍为 sourceMessageID=10863 的 `shop_product`。
- 发布后 fetch origin 复核相关文件，customer-audit/ai-billing 无新增同文件变更，无需 rebase。本次可独立回退程序，不恢复旧数据库。
- 本轮没有额外向客户发送测试消息；六句逐字、零模型调用与原卡片引用通过自动回归，收件端展示留待用户实际发送验证。
