# AgentDesk 开发续接说明

本文件用于换电脑后继续当前 Codex 会话和 AgentDesk 开发。

## 2026-09-24 客户目标契约与真实验收修正

- 目标：纠正身份、选择、取消和换主题时保持正确业务目标，不新增模型阶段、状态机或语义门。
  当前架构以代码及 `docs/design/reply-runtime-engine.md` 为准，下面更早记录不代表现状。
- 文件：Executor 的 `intent_model_detector.go`、`intent_runlog_context.go`、
  `jev_intent_detector.go`、`pms_read_execution.go`、`generate_recovery.go`、`service.go`
  及对应回归测试；评测器增加 `customer_goal_scenarios.go`、`journey_acceptance.go`。
- 手机号变更清除依赖旧身份的订单，包括留给下一轮的实体；取消优先于已填槽位，
  更换号码继续查询不误判成取消；当前房型选择优先于旧实体。定位恢复使用同一成功
  Task 的快照，较旧历史不能覆盖快照，多客户定位不混用。
- JEV 选定上下文后同时传递结构化实体；独立任务不继承。订单查询不提取目标房型，
  不能把“换号码”当房型；已识别时间/价格目标但未命中字面字段时只展示相应字段族，
  不兜底输出整单。保留已有有依据的客户答案，不再用通用说明覆盖。
- 查询失败只说明未完成并询问是否需要同事核实，不要求客户反复再问，不自动转接，
  不把接口异常说成没有订单。真实接口一项“不存在”、另一项异常时不能断言无订单。
- 数据/接口/权限：无 schema、migration、DTO、API、WebSocket、Outbox、计费变更；
  HPMS 写开关继续关闭，备份“薇薇”“薇薇2”不动。
- 自动验证：Executor、Runtime、Services、Tools、PMS、reply-runtime-eval 六包全部通过；
  Linux 服务端与评测器构建成功。回归先复现身份保留旧订单、取消未生效、
  实体类型丢失、旧房型覆盖新选择，再修正并验证。
- 真实隔离验证：
  - `rrt-20260924-172952-7aab5140`：首批12轮，发现失败话术重复要求再问、时间问答多报字段。
  - 修正后 `/tmp/goal-contract-20260924-r12/`：12轮回复检查通过，3轮超过12秒；
    纠正号码、取消且不调用工具、重启换房、咖啡指代、切换停车、多题混合均核对原答复。
  - `/tmp/goal-contract-20260924-ux/`：15轮原场景回归，差价因目标价格缺失未完成，
    2轮知识回复超12秒（最大约20.8秒）；其余自动检查通过。
- 不再按80分即通过：分别记录回复检查、业务证据、速度、自然度待复核和投递状态；
  多约束必须同时满足，空回复不能算通过，安全说明“无法报价”不等于报价完成。
- 未完成：真实目标房价/一致计价依据缺失；知识 Judge 延迟仍高（咖啡 trace 中11.4秒，
  检索约0.9秒）；自然度尚不能由关键词断言达标。ChannelID=0 未实际发送企微消息，
  商品资源提交不能作为企微收件端打开卡片的证明。
- 并行影响：fetch 后代码及设计文档未发现 customer-audit/ai-billing 同文件改动。
  customer-audit 对本交接文档新增历史声明，合并时同时保留该声明及本节记录；
  无字段/状态冲突，无强制 rebase 依赖，建议本独立提交整体 cherry-pick。
- 发布边界：仅 test-2 程序升级，保留旧 release `20260924-product-experience-570d05d`；
  回退只切程序链接，不恢复数据库，不改变已有消息和审计。

## 2026-09-23 客户目标与事实回复链路结构收口发布

- 运行提交：`cc2667e`，分支 `codex/pms-live-from-weiwei-20260920`，已推送到
  `origin` 和 `weibao`。
- JEV 现在区分新请求、追问、原因、推荐、候选选择、确认、纠正、不满和取消，并读取同
  session 最近唯一业务 Task 的结构化实体、已确认事实与缺失方面；原因和选择继续原业务目标，
  不再重新发起相同流程。
- 手机号和内部订单定位改为 `customer_phone/order_locator` Entity；PMS 只产生 Task 级
  结构化事实，不再预写 `pms_customer_answer`。PMS 查询先于知识 Judge，知识不足不能抢先
  抹掉已经可以回答的实时订单、房态、会员或差价事实。
- Judge 只裁决知识证据，并增加酒店会员、影视会员、商品、房间、外卖和客房服务主体隔离。
  JEV 已负责拆题与上下文时，知识 coverage 不再二次重拆或改写 Task。
- Generate 负责最终自然回复。有事实的 `AnswerText` 不锁定，单 Task 允许自然文本；多 Task
  仍严格校验 Task ID、数量、顺序和最多三条消息。`coveredFactIds` 只引用实际采用的事实，
  本地继续校验引用合法、必要字面值和内部协议泄漏，不做第二次自然语言语义裁决。
- PMS Generate 失败不会把库存、冲突统计、净脏房、内部订单 ID 或原始 PMS facts 发给客户；
  只追问明确缺失字段或给出安全重试提示。

验证通过：

```bash
go test -p=1 ./internal/ai/runtime/executor -count=2
go test -p=1 ./internal/ai/runtime/... -count=1
go test -p=1 ./internal/services -count=1
go test -race -p=1 ./internal/ai/runtime/executor \
  -run 'Test(Jev|RuntimePMS|MultiReply|GeneratedReply|KnowledgeEvidence|Intent|QuestionCoverage|EvidenceBacked|GenerateOwns)' \
  -count=1
make build-linux
git diff --check
```

- test-2 release：`/opt/agentdesk/releases/20260923-customer-goals-cc2667e`。
- Linux amd64 二进制 SHA-256：
  `481dcac8601e572decd3fca756ac96b97d8b9b038f520522cceae3a3f3245ab3`。
- 部署前 release：`/opt/agentdesk/releases/20260923-customer-context-81e57dc`；配置、环境文件、
  原 release 路径和旧二进制校验保存在
  `/opt/agentdesk/backups/20260923-pre-customer-goals-cc2667e`。
- 部署后 `agentdesk.service=active/running`、`NRestarts=0`、8083 HTTP 200；
  `AGENT_DESK_PMS_ENABLED=true`、`AGENT_DESK_PMS_ALLOW_WRITE=false`。本轮无数据库、Migration、
  DTO、enum、外部 API、WebSocket、企微协议、计费、Outbox 或 PMS 写入变更。
- 日志中的 FastGPT usage 同步告警和企微坐席过期 `err_code=9003` 为部署前既有环境告警，
  本次新进程未出现启动错误。异常时原子切回上述 `81e57dc` release，不恢复数据库、消息、
  PMS 数据或“薇薇/薇薇2”备份。
- `customer-audit` 与 `ai-billing` 均在 executor 同目录存在并行修改，合并时必须逐文件保留
  本提交的 JEV/PMS/Judge/Generate 职责边界；不要整文件覆盖。无 Migration 合并顺序要求。

## 2026-09-23 HPMS 文档复核与完整入住区间房号计算

- 重新逐字段核对三份原始文档：订单/房态/房情与批量改价、续住与手机号查单、会员只读查询。
  `channelOrderNumber` 只在订单详情响应出现；实时房态 `keyword` 只写“订单号”，未明确渠道订单号，
  因此只新增 `DeferredOperations.QueryOrderByChannelNumber` 契约，不注册工具、不发 HTTP 请求。
- 新增只读 `stay_room_availability`：读取实时房态 `homeCardList`，合并每个房间的
  `reserveOrderInfoList/checkInOrderInfoList`，按 `[checkInTime, checkOutTime)` 计算客户完整入住
  期间是否冲突，排除锁房和维修房，并可排除客户自己的预订单/接待单占用。
- 该计算不锁房、不排房。实时房态未来订单覆盖约 30 天；超出范围、订单时间缺失或房卡不完整时
  返回 `partial`。普通升房/换房仍可使用房型库存回答，只有客户明确问具体房号、哪间或可分配房时，
  具体房号查询失败才计入当前 Task 缺口。
- 临街、安静、靠电梯等属性没有 PMS 正式字段，继续只按知识库回答，不根据楼层或房号推断。
- 改房型、排房、在住换房、延迟退房只新增未注册的编译期 Adapter 契约。上下文仅使用已存在的
  `hotelId/reserveOrderId/receptOrderId/roomId/homeId/checkInTime/checkOutTime/orderStatus/homeStatus/controlStatus`；
  PMS 方补齐 endpoint、method、请求 DTO、操作原因、幂等键、版本/并发冲突、成功响应和回读规则前
  不得启用。当前无 model、Migration、DTO、外部 API、数据库、Outbox、企微协议或 PMS 写入变化。
- 聚焦验证连续两遍通过：
  `go test -p=1 ./internal/pms ./internal/ai/runtime/tools ./internal/ai/runtime/executor -count=2`。
  并行合并时需保留 `customer-audit` 对本文件的追加内容；`ai-billing` 无字段或计费语义影响。

## 2026-09-23 PMS 只读与回复链路结构收口

### 目标与文件

本轮在 `codex/pms-live-from-weiwei-20260920`、基线 `225f57f` 上修复同会话 PMS 定位复用、
续住日期计划、PMS 部分结果、维修拒绝人工、外卖机器人 Judge 超时和 JEV 多问题边界。
修改仅位于 `internal/ai/runtime/executor` 及对应测试，并同步以下设计文档：

- `docs/development/customer-service-pms-structural-optimization-20260922.md`
- `docs/design/reply-runtime-engine.md`
- `docs/development-handoff.md`

没有 model、Migration、DTO、enum、外部 API、WebSocket、企微协议、Outbox、计费或前端变化；
SQLite/MySQL 表结构均不变。PMS 保持 `enabled=true`、`allowWrite=false`，运行 allowlist 不含
`renew`，不新增 `PMSOperation`。

### 行为与权限边界

- RunLog 定位复用必须满足同会话、同 Agent、同 session、PMS 成功、Task 含 PMS 事实和消息
  已发送；当前消息的纠正优先，内部订单 ID 和完整手机号不得发给客户。
- 续住仅查询可行性、日期库存和候选，不提交订单；升房、换房、延退同样只读。
- 知识 Judge 超时后最多一次同协议紧凑恢复，总预算不增加；本地不增加业务语义裁决。
- 客户拒绝人工时维修问题不自动转接；明确人工和知识库明确转接仍走现有人工链路。
- Generate/Validate 全局失败不能恢复或覆盖已经成功、已发送的 sibling Task。

### 验证、并行分支与回滚

首轮 test-2 隔离矩阵共 22 轮，16 轮通过；报告为
`/opt/agentdesk/shared/live-tests/jev-runtime-smoke-pms-matrix-20260922-174359-fea74748.json`，
会话 ID 为 `2192`。失败已定位为三类确定原因：同一预订单两间同房型被误判为两次住宿；
HPMS 实际房型字段为 `roomId`，旧绑定仅读取 `productId/roomTypeId`；固定维修答复在 Generate
协议失败时未被锁定。外部代办两轮实际回复正确，失败来自测试脚本未把“没法”计为能力边界。

第二阶段修复只扩展房型字段、同预订单多房的订单级只读合并，以及非空服务端 `AnswerText`
保留。多房只有在预订单 ID、入住离店区间和真实房型全部一致时才能共享只读字段；接待单级
查询仍要求唯一接待单 ID。该规则不开放写操作，也不会猜选房间。

部署 `1b6f307` 后完整 22 轮矩阵为 20/22；报告为
`/opt/agentdesk/shared/live-tests/jev-runtime-smoke-pms-matrix-20260922-180833-71588ac3.json`，
会话 ID 为 `2193`。除续住外，其余十类场景均至少两种问法通过。两条续住均已真实查到预订单
和库存，失败原因是：可选接待单/续住候选被计入必答缺口；库存完整性错误检查了 PMS 返回的
全部房型；库存事实没有按当前房型过滤和明确续住边界。后续修复将续住候选改为增强信息，
仅按订单当前房型校验并展示续住日期库存；当前房型缺日期仍保持不确认。

续住修复后的完整矩阵最终为 22/22，通过报告：
`/opt/agentdesk/shared/live-tests/jev-runtime-smoke-pms-matrix-20260922-182834-fd3a1c99.json`，
会话 ID 为 `2196`。工具动作仅包含订单、库存、会员和房态查询；所有普通问题保持
`AI_SERVING`，会话 `2193-2196` 的 `t_pms_operation` 新增数为 0。

随后独立人工验收发现客户级“关闭自动转人工”错误拦截了客户明确人工和知识库明确转接。
修复后该设置只拦截普通自动升级；当前明确人工、知识库顶层答案为“转接”和安全风险仍走
现有幂等人工服务。单元测试对明确人工、知识转接和安全风险各至少两种表达连续执行两遍；
普通非明确请求及无当前消息的旧图工具仍保持不转接。

提交前验证：

```bash
go test -p=1 ./internal/pms ./internal/ai/runtime/... -count=2
go test -race -p=1 ./internal/pms ./internal/ai/runtime/tools ./internal/ai/runtime/executor ./internal/ai/runtime/graphs -count=2
make build-linux
git diff --check
```

部署后每类至少两种问法验证：手机号查单连续追问、升房/换房、续住一晚/两晚、15:00/15:30
延退、空调/漏水且拒绝人工、外卖自助边界、外卖机器人纠正、PMS+停车混合问题、明确人工和
知识库明确转接。验收同时检查没有 PMS 写请求和新增 `PMSOperation`。

最终运行提交为 `e5454cd80584eefdb5f09e45b70f70e3d4a11573`，test-2 release 为
`/opt/agentdesk/releases/20260923-pms-readonly-e5454cd`，二进制 SHA-256 为
`28e651b1980307c2522c1cffbecedc20309e04c5850cd6ad43c30b35c31b5aec`。服务状态为
`active/running`、`NRestarts=0`，`8083` 返回 200；PMS 为 `enabled=true`、
`allowWrite=false`。

最终提交上的第一轮 22 场景复验实际业务结果全部正确；测试脚本最初只接受“可选/可售/库存”
等字面词，把正确回复“现在还有儿童房、橙意和沐阳可以选”误记为 21/22。验收词组补充自然
表达“可以选”后，未修改运行程序，再次完整运行达到 22/22：
`/opt/agentdesk/shared/live-tests/jev-runtime-smoke-pms-matrix-20260922-190940-797ba8b4.json`，
会话 ID 为 `2204`。此前 `75a7674` 完整矩阵也为 22/22，因此各普通能力至少完成两次真实
运行；最终矩阵 22 轮路由均为 `AI_SERVING/ai`，工具动作仅为预订单、接待单、会员、库存和
房态只读查询。

人工路由另使用四个独立会话验证，避免首次转接后的人工状态污染后续输入：明确人工两种表达
分别为会话 `2198/2199`，门店知识精确“转接”两种表达分别为会话 `2201/2202`。四轮都只
发送一次 `帮您转接到同事了`，路由均为 `STORE_WECOM_MANUAL/store_wecom`。门店知识已经有
正文答案的问题不能拿通用库“转接”做验收，例如“酒店有洗衣机吗”应优先直接回答门店正文；
最终知识转接样本使用无竞争正文的门店精确 FAQ“怎么出车？”和“水单怎么开？”。

会话 `2198-2204` 的 `t_pms_operation` 总数为 0。完整矩阵最慢一轮为 83.178 秒，功能和投递
正确，但不作为延迟达标证据；性能优化仍需单独设定 P90/P95 目标，不能与本次功能通过混为一谈。

共享高风险文件为 `internal/ai/runtime/executor`。开始和 push 前均需 `git fetch origin`，检查
`codex/customer-audit`、`codex/ai-billing` 同文件修改；建议本提交作为独立 Runtime 收口提交
合并。程序异常只回退 test-2 release，不恢复数据库；实施前回滚点为
`/opt/agentdesk/releases/20260922-judge-budget-0279055`，消息和 PMS 数据保持现状。

## 代码与运行状态

- 远端仓库：`git@github.com:520skyincloud/agentdesk.git`
- 最新主分支提交：`083937b` 起包含完整迁移备份；本文件之后的提交会包含 Codex 会话备份。
- 当前主要分支：`main` 与 `wxwork-protocol-agentdesk` 保持同步。
- 完整运行备份：`backups/migration-20260630-090044/`
- 恢复脚本：`scripts/restore_full_backup.sh`

新电脑恢复项目：

```bash
git clone git@github.com:520skyincloud/agentdesk.git
cd agentdesk
chmod +x scripts/restore_full_backup.sh
./scripts/restore_full_backup.sh backups/migration-20260630-090044
```

## Codex 会话备份

当前长会话的原始 Codex rollout 文件已备份到：

- `backups/codex-session/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl.gz.part-*`
- `backups/codex-session/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

因为原始会话文件较大，已按 50MB 切分，避免超过 GitHub 单文件限制。

新电脑重组会话备份：

```bash
chmod +x scripts/restore_codex_session_backup.sh
./scripts/restore_codex_session_backup.sh
```

脚本会生成：

- `.codex-session-restore/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl`
- `.codex-session-restore/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

如果新电脑 Codex 客户端支持导入本地 rollout/thread 文件，可以导入上述 JSONL。若不支持，开新 Codex 线程后把本文件和 `.codex-session-restore` 里的 JSONL 作为上下文即可继续开发。

## 最近关键改动

- 企微员工号协议唯一依据固定为 `https://wework.apifox.cn/llms.txt`。
- CLI / 企业微信客服号作为产品入口已废弃，新主链路是企微员工号协议 SAAS + AgentDesk 会话工作台。
- 会话页新增账号是两列弹窗：现场扫码与远程门店自助开户链接。
- 实例池支持清理未登录临时占用：`resolve_login_binding`，不自动解绑真实登录账号。
- 每个员工号绑定独立智能客服配置，原全局智能客服入口只保留兼容。
- 全托管/半托管/非托管模式影响转人工提醒走总部网页端还是门店群。
- 知识库 guard 已修正：知识库未命中不再直接固定兜底，先做意图判断；办入住、定位、小程序、转人工、寒暄、确认等走智能服务链路。
- 完整运行数据已备份到 `backups/migration-20260630-090044/`。

## 继续开发注意事项

- 修改企微员工号协议前，必须查 `wework.apifox.cn` 对应接口页面，不猜字段。
- 后端遵守 `models -> repositories -> services -> handlers`。
- 前端业务接口统一走 `web/lib/api/admin.ts`，不要在页面组件里裸 `fetch`。
- 改完后至少跑：

```bash
pnpm --dir web typecheck
docker run --rm -v "$PWD":/src -v agentdesk-go-cache:/go/pkg/mod -v agentdesk-go-build:/root/.cache/go-build -w /src golang:1.26-alpine sh -lc '/usr/local/go/bin/go test ./internal/handlers/dashboard ./internal/bootstrap ./internal/services ./internal/ai/runtime/executor -run "TestKnowledgePolicy|TestAIHandoff|TestAgentTeamSchedule|TestAuth" -count=1'
docker compose build agent-desk && docker compose up -d agent-desk
```

## 2026-08-21 回复知识裁决与通用库收口

> 历史快照：本节记录 2026-08-21 当时的 V1 行为，不再代表当前运行契约。
> 当前链路以 `docs/design/reply-runtime-engine.md` 和本文 2026-08-31 的
> “Judge 非破坏式裁决收口”章节为准，禁止据此恢复
> `direct/supporting/unrelated`、零异常重试或 Judge 失败转接人工的旧逻辑。

### 目标与运行边界

本轮基于生产基线 `003dfb7` 做局部修复，生产回滚基线命名为“原神”。
目标是解决单层或双层错误召回、通用知识未正确兜底、公开经营主体问题未查
知识、信息问询误追房号，以及多问题中“可回答问题 + 待接待问题”互相吞掉。

回复主链路保持不变：一次 Intent、各原子问题并行知识检索、最多一次批量
Judge、一次 Generate、Commit、Outbox。没有新增第二次 Intent 或 Generate，
也没有修改计费口径、消息聚合、Task/Outbox、语音识别或接管状态机。

### 代码改动

- `internal/ai/runtime/executor/answerability_gate.go`：按原子问题保留检索结果，
  汇总一次 Judge，重建胜出证据，并保留混合批次中的可回答问题。
- `internal/ai/runtime/executor/knowledge_evidence_judge.go`：新增严格 JSON 的
  `direct/supporting/unrelated` 批量裁决；4 秒、2,048 token、零重试。
- `internal/ai/runtime/internal/impl/retrievers/knowledge_retriever.go`：保留
  `RawHits`，支持在不重新检索、不重复写日志的情况下重建授权证据。
- `internal/ai/runtime/internal/impl/callbacks/*`：Trace 新增
  `pipeline.evidenceJudge`，记录候选指纹、模型耗时、逐题选择和延迟接待任务。
- `internal/ai/runtime/reply_trigger_service.go`：混合批次先提交已有知识答案，
  再复用现有确认服务处理延迟接待，不增加第二次 Generate。
- `internal/services/model_profile_template_service.go` 与
  `internal/services/store_ai_model_setting_service.go`：新增内部 usage
  `knowledge_judge_llm`，不触发旧 FastGPT Profile 同步。
- `internal/pkg/replyintent/defaults.go`：将酒店、品牌、公司及老板、创始人、
  董事长等公开身份归入 `hotel_info/company_profile` 并要求查知识。
- `internal/services/conversation_handoff_confirmation_service.go`：追问房号优先
  使用真实客户原消息；语音使用 `mediaText/mediaSummary`。只有明确的客房内
  故障、送物、换物或现场动作才追房号，普通设施问询不追问。

对应测试位于：

- `internal/ai/runtime/executor/knowledge_evidence_judge_test.go`
- `internal/ai/runtime/deferred_knowledge_handoff_test.go`
- `internal/ai/runtime/executor/intent_pipeline_test.go`
- `internal/ai/runtime/executor/intent_human_route_test.go`
- `internal/services/conversation_human_dispatch_service_test.go`
- `internal/services/model_profile_template_service_test.go`
- `internal/services/store_ai_model_setting_service_test.go`

### 裁决规则

门店库和通用库继续并行检索。所有有候选的原子问题共享一次批量 Judge，
即使某题只有一个知识层也要判断，避免单层错误召回直接进入 Generate。
Judge 只判候选是否直接回答、仅能补充或无关，最终由代码固定裁决：

```text
store direct > general direct > no direct evidence
```

胜出层可以携带同层 supporting 证据，另一层完全不进入 Generate。当前实现中，
Judge 不可用、超时、调用失败或返回非法协议时不重试，Trace 记为 `fallback`；
这些候选按 `insufficient` 处理，未经 Judge 选择的 Hits/Context 不得进入
Generate。同轮还有小程序、定位等独立 Resource 时，先保留并提交真实 Resource，
知识问题再按现有 deferred handoff 路径处理。

多问题中若一部分有 direct、另一部分无 direct 或命中“转接”，Generate 只
回答有证据部分；答案提交成功后，再对待处理部分发送现有接待确认。若全部
问题都需接待，仍沿用 Generate 前的既有接待路径。

### 接口与数据影响

- 无数据库表、Migration、外部 API、DTO、枚举、WebSocket 或前端变更。
- 继续使用既有 `SystemConfig` 键
  `reply_runtime.general_knowledge_base_by_store`，值为 Store ID 到 Agent Desk
  KnowledgeBase ID 的映射。
- 模型模板新增内部槽 `knowledge_judge_llm`。生产模板应配置
  `deepseek-v4-flash`、`timeoutMs=15000`、`maxOutputTokens=2048`、
  `maxRetryCount=0`。
- 运行 Trace 新增内部 `pipeline.evidenceJudge` 字段；其
  `deferredHandoff/deferredTaskIds` 只用于提交后调用现有接待确认服务。
- 通用知识库切换只更新现有 KnowledgeBase 的 `dataset_id`，不改变门店映射
  或租户边界。

### 聚焦验证

最终提交前运行：

```bash
go test ./internal/ai/runtime/executor -run 'Test(KnowledgeEvidenceJudge|NormalizeKnowledgeEvidenceJudgeConfig|ParseKnowledgeEvidenceJudgeResponse|KnowledgePolicy)' -count=1
go test ./internal/ai/runtime -run 'TestDeferredKnowledgeHandoffFromTrace' -count=1
go test ./internal/services -run 'TestConversationHandoff(CollectsRoomForInRoomCategories|DoesNotCollectRoomForInformationQuestions|RoomDecisionUsesVoiceTranscript)' -count=1
go test ./internal/ai/runtime/executor ./internal/ai/runtime/internal/impl/retrievers ./internal/ai/runtime ./internal/services -count=1
```

覆盖重点包括：通用 direct 可以越过无关门店候选；两层均 direct 时门店优先；
Judge 失败不得暴露未经选择的知识，且同轮独立 Resource 仍可提交；多个原子问题
只调用一次 Judge；单层候选仍判定；
有答案与无答案/转接并存时先答后确认；“有空调不”“小程序不能用”“电梯坏了”
和“停车场很吵”不追房号；客房内明确故障、送换物和现场动作仍追房号。

上述命令已在 2026-08-21 最终代码收口后重新执行，四个相关包全部通过；
结果不是较早并行修改前的缓存结论。

### 生产冒烟发现的连续消息漏答修复

首次生产 C02 冒烟中，合并输入已经包含“早餐有吗 / 停车免费吗 / 剃须刀在哪”，
但 Intent 只产出最后一题，导致 Judge、检索和 Generate 均只处理剃须刀。后段
没有截断答案，根因是连续消息在 Intent 阶段被错误缩成最后一条。

2026-08-28 复查生产会话后确认，检索层替 Intent 补题会形成第二套拆题结果，
不能继续作为正式方案。当前实现改为：每次 Intent 都固定逐条扫描 `U1...Un`，
由模型独立决定一条消息内是 0、1 还是多个 Task；检索、Judge 和 Generate 只消费
模型 Task，不再根据标点、关键词或剩余文字二次拆题。纯背景如“好困啊”由模型
作为咖啡 Task 的 context source 绑定，不生成独立回答。

混合场景“空调坏了 / 我住1302 / 早餐几点”还补充了短消息组房号复用：只在
同 conversation、同 session、8 秒内、最后一次 AI/人工回复之后读取明确房号，
用于待处理空调任务的接待确认；旧房号和跨回复房号不会复用。

评测器新增逐问题 `RequiredOutcomes`，C02 必须分别覆盖早餐、停车、剃须刀；
X03 必须同时有早餐答案和空调故障的 deferred handoff。旧的
`MustContainAny` 不再把“只答中一题”误判为通过。

### Judge 后 active ReplyPlan 与接待确认收口

生产 X03 回归暴露了两个结构边界：Judge 的 `T1/T2` 来自真实检索问题顺序，
不能按数字直接映射为 Intent ReplyPlan 的 `task-1/task-2`；同时 deferred 场景
若 Generate 返回非法 JSON 或缺少可回答 part，空 `ReplyText` 会绕过原先位于
Commit 分支内的接待确认，Job 被当作成功但客户收不到消息。

本轮在 Judge 后按 `batch.Questions` 的真实客户顺序重建 active ReplyPlan：保留
可回答知识任务、补回 Intent 漏掉但检索已恢复的问题，并排除将由接待流程处理
的任务。Intent 专项 Prompt、多任务输出契约、Generate 用户输入、变量混合范围
和输出解析统一读取该 active plan，不再依赖 `Tn -> task-n` 位置转换。明确的
定位、小程序、人工等非知识任务不会被连续消息补题误送入知识检索；原 ReplyPlan
中 Text 为空的服务请求仍保留原 Intent/SubIntent，只补入真实问题文本。

deferred 时即使只剩一个可回答任务也要求结构化输出；非法或缺失 part 继续
fail-close，不放出未经归属的模型文本。进入接待确认的任务原文不会再写入
Generate 提示，避免模型把待处理主题复述进其他答案。外层执行把 deferred dispatch 与文本
Commit 解耦：有答案时先提交答案再发送接待确认；无答案文本时仍经过消息时效
检查后发送接待确认。没有新增 Intent、Judge、Generate、协议修复重试或固定等待。

本收口仅修改 Runtime/Executor 内部实现和测试；没有数据库、Migration、外部
API、DTO、枚举、WebSocket、前端、模型配置、计费或 Token 口径变化。聚焦验证为：

```bash
go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime/internal/impl/retrievers ./internal/ai/runtime ./internal/services ./cmd/reply-runtime-eval -count=1
```

回滚可直接部署 `0f82a1d`，或完整恢复“原神”锚点
`backup/yuanshen-20260821-003dfb7`；本轮没有外部数据迁移需要反向处理。共享文件
主要是 Runtime Trace collector 和回复触发 service，push 前需核对并行分支同文件
修改，建议本提交作为 Judge/连续消息提交之后的独立收口提交合入。

### 通用知识库蓝绿上线

清洗成品为 `/private/tmp/agentdesk-general-kb-final/general-kb-final-90.csv`，
包含 90 条 FAQ、379 个问法，SHA256 为
`f7ef325546e62b8b686fa1e466952f1dbf7f47fdaddaca0c43885ca0b5f72533`。
精确操作单位于
`/private/tmp/agentdesk-general-kb-final/fastgpt-blue-green-rollout.md`。

上线时在南七 Store Team 内新建未被生产引用的 staging Dataset，使用 backup
模式导入并等待训练完成，再做真实检索验证。通过后以旧 `dataset_id` 为条件，
CAS 更新 Agent Desk KnowledgeBase `7` 的 `dataset_id`；
`reply_runtime.general_knowledge_base_by_store={"1":"7"}` 保持不变。旧 Dataset
和 collection 不删除、不禁用，以便原子回滚。严禁直接向当前生产引用的通用
Dataset 追加 90 条数据，因为创建 collection 后会立即参与真实检索。

代码部署后还需把生产 Intent Profile `1` 更新为当前默认 Prompt，并给实际按
`id ASC` 生效的模型模板 `1` 增加 `knowledge_judge_llm` 槽。完成 Dataset 切换
后，用空调存在性/故障、早餐+马桶堵塞、公开经营主体、吹风机、地巾、禁烟、
加盟及长文字/语音多问题做真实冒烟，并确认 Trace 中 Judge 每轮最多一次、
Generate 仍为一次。

### 风险与回滚

- Judge 的代码上限为 15 秒；它的语义误判会影响候选选择，因此必须观察
  `pipeline.evidenceJudge`。失败时不会把未经筛选的检索结果交给 Generate，
  但仍不能解决检索本身没有召回正确答案的问题。
- 混合批次的知识答案和接待确认是先后两个既有提交动作；确认消息使用同一
  handoff token 作为稳定发送 ID，并做三次短重试，失败不会回滚已经提交的知识
  答案。仍需观察极端进程退出窗口和既有 Outbox 日志。
- 通用库数据切换属于外部 FastGPT/生产数据库操作，不随代码 commit 自动完成。
  staging 未训练完成、Store Team 不一致或 CAS 影响行数不是 1 时必须停止上线。
- `conversation_handoff_confirmation_service.go`、模型 usage 常量及 Runtime Trace
  是共享高风险文件。合并前后需要核对 `codex/customer-audit` 与
  `codex/ai-billing` 的同文件修改，保留双方字段和语义。

“原神”回滚锚点：Git 标签 `backup/yuanshen-20260821-003dfb7`，bundle 为
`/private/tmp/agentdesk-yuanshen-20260821-003dfb7.bundle`。应用完整备份位于
`/opt/agentdesk/backups/yuanshen-20260821-101926`，FastGPT 完整备份位于
`/opt/backups/yuanshen-20260821-101926`。代码回滚可重新部署该标签对应 release；
通用库回滚只需 CAS 恢复 KnowledgeBase `7` 的旧 `dataset_id`；Judge 可通过
恢复旧模板/Intent Profile 或部署“原神”关闭。不要删除旧 Dataset，直到观察期
结束。

### 并行分支影响

本轮没有 Migration 或公开契约，业务合并可以独立回滚。`codex/ai-billing`
可能同时修改模型模板 usage 与计费记录相关文件，`codex/customer-audit` 可能
读取 Runtime Trace 或接待状态。提交和 push 前必须先 `git fetch origin`，检查
双方同文件差异；建议先保留计费分支的模型/usage 语义，再合入本轮新增的
`knowledge_judge_llm` 和 Trace 字段，最后运行上述回复链路聚焦测试。禁止通过
覆盖文件解决冲突。

## 2026-08-26 Active Answer Task 逐题闭环收口

> 历史快照：本节记录 2026-08-26 当时的实现和验收口径。当前行为、测试规模与
> 回滚边界以 `docs/design/reply-runtime-engine.md` 及本文 2026-08-31 两节为准；
> “三遍 50 轮”、完整 V2 Semantic Gate 和字面 span 校验不再是当前要求。

### 当前状态

- 工作分支：`codex/reply-runtime-active-answer-tasks`。
- 代码基线：`18b19997fe1c5663e0fdecbb4b80d26775abd993`。
- 历史快照截至 2026-08-26；当时改动仍在工作区，尚未记录为已部署生产。
- 150 轮真实模型验收和隔离企微最终投递尚需由主任务按真实结果记录，不能用
  单元测试结论代替。
- 现行权威设计见 `docs/design/reply-runtime-engine.md`；不要从旧 FAQ、旧 Hook
  Bridge 或旧独立 Agent 文档恢复已废弃链路。

### 真实代码改动

本轮将 Intent 后的 `ReplyPlan.TaskPlans` 作为 Active Answer Task 清单，字段包括
`TaskID`、`Intent/SubIntent`、`OriginalText/ResolvedText`、`SourceRefs`、
`OutputKind/ReplyRequired`、选中知识层、候选 ID、`SupportedFacts` 和
`MissingAspects`。结构只存在于运行时与 Trace，不新增数据库持久化。

`resolvedText` 为“那麦田呢”“外卖地址再说一遍”等明确回指补成自包含检索问题，
`sourceRefs` 保留当前短消息组的 primary/context 来源；旧 Profile 不输出新字段时
回退到原 `text`。检索优先使用 `resolvedText`，原省略问法只做来源覆盖和去重，
不会重复发起第二条知识查询。业务问题与感谢、语气纠正同时出现时，互动任务降为
`context_only`，不会挤占或增加一个强制回复任务；纯互动仍正常回复。

知识裁决升级为 `knowledge_evidence_judge.v2`，支持
`direct_single/direct_combined/partial/insufficient` 和事实清单
`factId/aspect/statement/criticalValues`。门店和通用知识不得跨层拼接，代码优先级
固定为门店转接、门店完整、通用完整、门店部分、通用部分、接待路由。
`partial` 保留已确认事实进入 Generate，只把 `missingAspects` 交给现有 deferred
handoff 逻辑并继续遵循既有接待策略，避免一项缺失导致整条多问题回复被吞掉。

Generate 现在按每个文本 Task 输出 `replyParts`，本地校验 Task 完整性、
`coveredFactIds` 和数量、价格、电话、地址、房型等 `criticalValues`，通过后才按
原顺序合并为最多三条客户消息。Resource 和 Handoff 继续由 Action Ledger、
结构化 Commit 和真实接待服务处理，不进入文本生成。

外层 `reply_trigger_service.go` 已删除“协议失败后从 Intent 开始整条重跑三次”。
Executor 内正常只调用一次 Generate；协议错误、429、可重试 5xx、超时或连接
异常最多只重试一次 Generate，并复用冻结的 Task 和证据。重试前检查消息是否
仍可由 AI 回复；两次失败后优先使用 Judge 已确认事实确定性兜底，避免客户空回复
或看到内部错误。Intent、检索和 Judge 不重复执行或计费。

Generate 上下文已隔离：Intent 仍可读必要角色历史；Generate 默认只接收当前
Task 来源、`resolvedText`、选中事实、缺失方面和必要媒体对象。2026-08-28 修复
了过度隔离：短确认、槽位回答、回指、纠正和会话回顾会按 TaskID 获得有界历史，
普通承接最多相邻两条，会话回顾最多最近八条；独立新题仍看不到旧业务问题。
长期记忆在 Intent 阶段读取真实摘要正文，不再误传 `conversation_session_summary`
来源标签。媒体内容统一优先完整 `mediaText`，为空才使用 `mediaSummary`。

事件消费和 Commit 形成两道防线：逐题协议无法解析、内部头部出现在正文、清理
后为空，或仍含 `replyParts/taskId/coveredFactIds` 外形时均不得原样发送。精确位于
开头的 `[历史消息]`、`[AI客服]`、`[人工客服]`、`[人工作答]` 可以安全移除后
重新校验；普通“人工、同事、转接”词语不受影响。

Trace 新增或补齐：

```text
pipeline.replyPlan.activeTaskCount
pipeline.replyPlan.replyRequiredTaskCount
pipeline.replyPlan.taskPlans[].resolvedText/sourceRefs
pipeline.replyPlan.taskPlans[].supportedFacts/missingAspects
pipeline.generate.attemptCount
pipeline.generate.fallbackMode
pipeline.generate.composedMessageCount
pipeline.generate.blockedInternalMarker
```

### 接口、数据与并行分支

- 无数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端或权限变更。
- 不修改模型供应商、超时配置、计费和 Token 统计口径。
- 不修改数据库 Task、稳定发送 ID、Outbox、人工状态机、房号追问、ASR/OCR
  和媒体回调。
- `git fetch origin` 后按当前 merge-base 核对，本工作区与
  `codex/customer-audit` 的代码同文件交集只有
  `internal/ai/runtime/reply_trigger_service.go`。本分支删除外层整链路重跑，
  审计分支在同文件增加租户范围 Agent 读取；另有共同修改的本交接文档，合并时
  必须同时保留代码语义并合并文档记录。
- 当前与 `codex/ai-billing` 没有同文件交集，也没有计费语义冲突。push 前必须
  再次核对，禁止整文件覆盖。

### 验证与回滚边界

聚焦测试命令：

```bash
go test -p=1 \
  ./internal/ai/application/runtime \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1
```

必须覆盖多题逐题输出与最多三条合并、回指补全、Judge 组合/部分事实、必要值
完整性、只重试 Generate、事实兜底、员工接管后停止恢复、内部协议和历史标签
不外泄，以及 Resource/Handoff/Outbox 既有幂等行为。

当前替代验收口径为计划内 10 至 15 个代表场景和隔离企微出站冒烟，并如实记录
空回复、问题覆盖、事实槽位、协议泄漏、外推和延迟。未经用户明确同意不主动运行
50 轮；当前文档更新不声明这些外部验收已经通过，也不声明已经部署。

代码回滚边界为 `18b1999` release。本轮没有 Migration，无需反向数据库迁移；
若上线时另行修改生产 Intent Profile 或运行配置，应使用部署前备份独立恢复。

## 2026-08-26 Intent 轻量语义契约收口

### 目标与实现

本轮基于 `18b1999` 和当前未提交的 Active Answer Task 改动继续收口，没有从旧
文档重建第二套链路。Intent Task 新增并贯穿 Trace/ReplyPlan 的轻量字段为
`objective`、`relationToPrevious`、`resolutionState`、`entities`；原有
`text/resolvedText/sourceRefs` 继续作为客户原话、自包含问题和来源绑定。

新增的 Semantic Consistency Gate 完全在本地执行，不调用第二个模型。它会把
`service_request + availability/quantity/location/method` 等信息型目标修正回
`hotel_info`，因此“有没有空调”不会因为设施词被当成送修或追问房号；“叫人来
看看空调”仍保持真实服务动作。`ambiguous/unresolved` 只隔离当前 Task，其他清晰
问题继续回答。`answer_rejected` 必须同时满足紧邻 AI 答复、关系字段和接待分类，
避免普通追问或单纯语气被误转人工。

连续消息改用 `internal/pkg/utils/runtime_burst.go` 的机器标记和共享解析器。物理
消息边界不再由中文提示词或换行猜测，一条多行文字/语音只生成一个 `URef`。
文字、语音统一进入同一 Intent；语音仅在 `mediaUnderstandingStatus=understood`
时使用完整 `mediaText`，为空才回退 `mediaSummary`。`pending/failed/empty` 或缺少
状态的语音即使 payload 残留文本，也不会进入当前 Intent 或历史 Intent 上下文。
已经收进 Burst 的早先语音/文字会从媒体上下文和最近历史中去重。

Intent Prompt 不再包含本地 `POSSIBLE_ATOMIC_TASKS` 或“本地认为有 N 题”的提示。
无标点长口语、文字和完整语音转写均由同一次 Intent 创建 Task；本地不会自行拆分、
合并、补造或改写 Task。代码会使用保守原子候选验收模型协议，拒绝可机械证明的漏题、
重题、串题、额外 Task、非法 compound 和逆序，失败只触发现有一次 Intent 协议修复。
旧 Profile 仍保留单来源 `U1` 兼容。

`sourceRefs[0]` 是问题所有权：每个包含自包含业务问题的 URef 必须至少由一个可执行
Task 以该 URef 为 primary 主认领；后续 context refs 只能授权指代补全，不能消费更早
独立问题。`text` 可以是合理改写，但必须结合 objective、主题、实体和文本锚点归属于
primary 原子问题；`clear` 的 `resolvedText` 必须由当前问题支撑，只有
`resolved_from_context` 才能使用声明的更早 URef 或 BoundContext。“好困啊 + 有没有
咖啡”仍允许咖啡 Task 使用 `U2 primary + U1 context`，因为 U1 不包含独立业务问题。

2026-08-28 补充两个安全闸门：没有 `SelectedLayer + SupportedFacts` 的知识文本
Task 若是唯一任务则直接进入确定性安全兜底；若同轮还有天气、资源或其他可执行
Task，则只把无证据 Task 约束为固定安全短答，其他 Task 继续执行。已选事实旁新增
无依据的存在性、能力、政策、方法、位置、范围、时间、数量或价格同样判为 Generate
协议错误，复用现有单阶段重试和事实兜底。

### 文件、接口与数据边界

- 主要实现位于 `internal/ai/runtime/executor/intent_semantic_*.go`、
  `intent_model_detector.go`、`intent_config_matcher.go`、
  `reply_trigger_service.go`、应用 Runtime service 和消息 history adapter。
- 无数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端、权限、模型
  供应商、计费或 Token 统计口径变化。
- 不新增 Intent/Judge/Generate 调用，不修改 ASR/OCR、Task、Outbox、稳定发送
  ID、房号追问或人工状态机。
- 默认 Prompt/Schema 已支持新字段；生产数据库里的存量 Intent Profile 不会由
  Migration 强推。部署代码后需要先备份并单独更新生效 Profile，旧 Profile 在
  更新前继续走兼容模式。

### 验证、风险与并行分支

已真实通过：

```bash
go test -p=1 ./internal/ai/runtime/executor -count=1
go test -p=1 \
  ./internal/ai/application/runtime \
  ./internal/pkg/replyintent \
  ./internal/ai/runtime/internal/impl/adapter \
  ./internal/ai/runtime/internal/impl/callbacks \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1
```

自动测试覆盖长文字/语音多问、复合问题不误拆、Burst 多行边界、媒体 Prompt
去重、失败语音门禁、`sourceRefs` 严格校验、信息咨询与现实动作区分、旧 Profile
低置信降级和陈旧资源字段清理。真实模型重复评测、生产 Profile 更新和隔离企微
出站仍属于后续部署验收，当前不声明已执行或已部署。

本轮没有共享数据契约或 Migration。`reply_trigger_service.go` 仍与
`codex/customer-audit` 存在同文件修改，合并时必须同时保留本分支的 Burst/重试
语义和审计分支的租户范围读取；当前没有修改 `codex/ai-billing` 的计费语义。
回滚代码可恢复 `18b1999`，若上线时更新了生产 Intent Profile，需独立恢复其备份。

## 2026-08-27 AI 服务通知阻塞续答修复

生产会话 `1890` 暴露出人工超时恢复的真实竞态：客户消息后已发送的
`ai_handoff_success_*` 转接成功通知被 Runtime 当成普通 AI 回答，导致 debounce
和 Commit 都误判为“已有更新回复”，恢复任务最终以“未提交回复”失败。

本轮将现有 AI 服务通知识别收敛到 `utils.IsAIServiceNoticeMessage`。明确的转接
成功通知和带 `serviceEvent` 的系统通知不再参与 Runtime 最新业务消息判断，也不
进入 Intent 历史；普通 AI 回答、员工回复和更新的客户消息仍会阻止旧任务提交。
实时人工路由检查保持不变，人工状态未合法恢复前不会放行普通 AI 回复。

涉及 `internal/pkg/utils/message.go`、`internal/services/message_service.go`、
`internal/ai/runtime/reply_trigger_service.go` 和 Runtime history adapter。无数据库
结构、Migration、外部 API、DTO、枚举、WebSocket、前端、模型、计费或 Token
统计变化。聚焦测试覆盖服务通知后允许续答、普通 AI 回答仍阻止旧提交及服务通知
不进入模型历史。

本轮与 `codex/customer-audit` 在上述三个非 adapter 文件存在同文件演进，后续
合并需保留双方语义；与 `codex/ai-billing` 无同文件交集。回滚只需切回
`f6ca7b7` release，不涉及数据库回滚。

## 2026-08-31 Judge 非破坏式裁决收口

本轮基于 `40cc24b` 只修改知识裁决、检索结果重建和内部 Trace。Retriever 的
`RawHits` 永远保留两层原始候选，`EffectiveHits/Hits/ContextResults/ContextText`
只保存 Judge 最终授权的胜出层；Judge 失败不会再通过重建结果覆盖原始候选。

Judge 协议状态明确区分 `insufficient`、`protocol_invalid`、`timeout` 和
`malformed`。未知 Candidate、重复或缺失知识层、非法枚举交集以及显式对象或房型
错配按 Task/Layer 隔离，不再伪装成“资料不足”并误触发人工。模型已选 Candidate
的 Fact JSON 无法使用时，只从该 FAQ 原文机械重建事实，不允许本地换 Candidate、
按分数改判或跨 FAQ 拼接对象；未知但有原文依据的 aspect 归一为 `other`。

每轮仍只调用一次 Judge。协议失败不重跑 Judge、Intent 或 Retriever，授权知识
上下文保持为空，ReplyPlan 在 Generate 前进入既有确定性安全短答：该 Task 只携带
固定安全事实，不允许自由生成酒店事实、不转人工，也不向客户暴露 RawHits 或内部协议。
同一批次中已成功 Task 的 `SelectedLayer/SupportedFacts` 必须继续保留，不能被失败
Task 清空或一起降级。内部
`pipeline.evidenceJudge.latencyMs` 记录这一次 Judge 的实际耗时。

Judge 请求沿用原稳定 usage 事件键。计价公式、Token 字段和 provider receipt 语义
没有变化，协议异常不会增加第二次模型调用或额外费用事件。

严格 exact FAQ 恢复只接受 FAQ 问法或显式 alias 的机械相等，不读取向量分数或字符
相似度。知识转接还要求答案严格为“转接/转人工”，同层同问法存在正文冲突时禁止
直接转接。同层 `RawCandidates` 只要还有可信、值得 Judge 复核的竞争正文，即使正文未进入本轮 Judge
预算，精确转接也不能吞掉 Judge 异常；一个槽位时先保留正文，至少两个槽位时正文与
精确转接共同进入 Judge，再考虑通用兜底。Judge 已经实际看见两项并明确选择精确转接
时可以执行；模型没看见正文时禁止由异常 fallback 自动转接。多主体、多维度完整性按客户问题里的主体与维度配对
核验，不把多个分句做全组合，“不确定/待确认”不能算确定事实。没有固定事实维度的
身份或描述题允许保留已落地的 `other` 事实，但礼貌话、联系门店等泛化引导仍会被
过滤；Task 明确要求方法、位置、存在性、数量、价格、时间、范围或配置字段时，
`other` 绝不能绕过对应机械完整性校验。“有没有”存在性问句只有在答案明确肯定且
同一结构化实体同时出现在问题和答案中时，才机械还原为肯定事实。非精确转接候选即使
被模型包装成 supportedFact，也必须按
`protocol_invalid` 隔离；转接候选不得参与 `partial/direct_combined`。门店合法
完整答案继续覆盖通用层；若门店层只有协议错误但通用层已有合法完整答案，则使用
通用答案。若通用层失败但门店已有合法部分答案，则先回答已确认事实并只延后缺失
方面。只有没有任何合法层时，协议错误才进入安全恢复。

候选预算仍固定为 28。`0.70` 只作为“是否值得送入 Judge 复核”的可见性下限，
不是召回阈值，也不能直接生成客户答案；主体、范围、条件或操作不一致的候选仍不优先。
配额至少两条且两层均有候选时，通常由门店与通用各保留一条
最佳候选；若门店没有单条完整答案、但两条同层候选能共同覆盖多个明确事实维度、
主体或配置字段，则这两条必要证据优先于通用兜底。近重复 FAQ 和没有结构化要求的
“同时询问”不触发该例外。Runtime Retriever Trace 在 Judge 裁决及 deferred、retry、
handoff 最终清理后覆盖为最终授权状态，持久化 Retriever 日志不再把 Judge 前的
预选上下文写成最终 UsedHits。

`knowledge_evidence_judge.go` 中旧的 `highConfidence*` 和 score-rescue helper 只保留给
历史隔离测试，生产 `JudgeBatch/apply` 没有调用。`0.70` 只影响 Judge 可见性，`0.85`
只参与 Judge 前的高置信候选保留；两者都不能在 Judge 失败后直接改判客户答案。

本轮无数据库表、Migration、外部 API、DTO、枚举、WebSocket、前端、模型配置、
计价公式、Intent、消息收敛、人工状态机或 Outbox 改动。Judge 仍保持每轮一次真实
调用和一条 usage 事件。与
`codex/customer-audit`、`codex/ai-billing` 的当前远端差异不要求本提交改变共享
契约；合并时需保留本轮 `trace_callback.go` 的向后兼容新增字段。

## 2026-08-31 Intent、上下文与人工恢复边界收口

本轮在 Judge 非破坏式裁决之上完成 Release B，不新增跨运行“已答题目”状态，
也不恢复本地关键词拆题器。当前 burst 中有几个业务问题、短句之间是合并还是拆分，
仍由一次 Intent 模型决定；本地只验证 JSON、真实 URef、来源顺序、完全重复 Task
和无法由当前来源或紧邻上下文证明的实体。`text` 是模型给出的当前 Task 表达，
客户原始物理文本保存在 `input.currentTurnSources`；`resolvedText` 只负责把明确回指
补成自包含检索问题，不能凭空切换房型、地点或业务对象。

普通消息收敛和人工恢复统一保留真实物理消息边界。每条客户消息继续映射到独立
URef，不再通过普通换行把多条消息扁平化为一个假来源。Intent 可以读取有界近期
历史；Judge 和 Generate 都只在 `follow_up/reference_previous/clarification_answer`、
纠正、`answer_rejected`、`resolved_from_context` 或会话回顾等确有承接关系时读取
有限 BoundContext，不再按“这个、那个、刚才”等词表猜关系。完整独立新题仍不携带
旧业务问答。历史只用于理解当前 Task，不会重新激活已经 Commit 的旧题。

相邻 BoundContext 固定为紧邻的一条客户问题，加其后最多三条连续、同一发送方类型的
AI 或人工客服答复。AI 与人工不能混成一组，历史末尾不是客服回复时不建立相邻组；
空消息和已注册服务通知跳过。四条以上只取最新三条，并对每条分别截断，保证最后的
纠正或补充不会被前面长文本挤掉。Intent、Judge、Generate 使用相同边界和时间顺序。

人工超时恢复优先复用现有 Runtime Trace 中的 `DeferredTaskIDs` 和 ReplyPlan。
已正常回答的兄弟 Task 不会再次进入 Intent、Retriever 或 Generate；只恢复真正
延后的 Task。人工期间新增的客户消息使用新的 URef，并且整次恢复最多执行一次
Intent。找不到兼容 Trace 时才走现有重新识别路径，不新增数据库表、Task 状态、
消息状态机或持久恢复账本。

混合问题中的 Deferred Task 不再从 ReplyPlan 删除。正常轮将其记录为
`deferred_knowledge_handoff/handoff/replyRequired=false`，所以 Generate 不会复述或
猜测该题，但 RunLog 仍保存稳定 TaskID、来源和必要字段。人工恢复时只去掉这一临时
执行标记，把该 Task 重新激活为知识文本任务；已回答兄弟题不会跟着恢复。该闭环同时
避免了“Trace 只有 DeferredTaskID、却找不到 TaskPlan”导致的整轮重新识别和重复回答。

知识库未配置或 Retriever 不可用等 Judge 前来源不可用路径也写入逐题显式契约：
`no_evidence_handoff + insufficient + DecisionSource=source_unavailable`。新版 V2 Trace
不得依赖空 disposition 恢复；空值只保留给有界 legacy 兼容。恢复语义按处置区分：
`no_evidence_handoff` 仍可恢复，`knowledge_direct_handoff`、明确转人工，以及答案已
真实提交的 `answer_then_handoff` 均视为已完成转接，在原超时点静默恢复 AI，不再
重新回答原题。

人工路由继续使用既有超时矩阵，不追加第二段等待窗口：总部网页待接入 3 分钟、门店
待跟进 5 分钟、员工真实接管后的空闲期 10 分钟。到期后要么准备一次未完成 Task 的
真实续答，要么静默恢复 AI；不会再重新计一段十分钟。

V2 Trace 与 legacy Trace 严格区分：只有结构完整且来源可验证的 V2 数据才能走
Deferred Task 精确恢复，旧 Trace 不会被误当成新协议。RunLog/Trace 增加的恢复
上下文仍是内部向后兼容字段，不改变外部 API、DTO、WebSocket、数据库结构、
Migration、计费或 Token 语义。

### 文件与真实边界

Release B 的主要文件为：

```text
internal/ai/runtime/executor/context_builders.go
internal/ai/runtime/executor/intent_model_detector.go
internal/ai/runtime/executor/intent_pipeline.go
internal/ai/runtime/executor/intent_config_matcher.go
internal/ai/runtime/executor/intent_protocol_validation.go
internal/ai/runtime/executor/manual_resume_plan.go
internal/ai/runtime/executor/answerability_gate.go
internal/ai/runtime/executor/reply_tag_context.go
internal/ai/runtime/internal/impl/adapter/message_adapter.go
internal/ai/runtime/internal/impl/callbacks/runlog_callback.go
internal/ai/runtime/internal/impl/callbacks/trace_callback.go
internal/ai/runtime/reply_commit_service.go
internal/ai/runtime/reply_trigger_service.go
internal/pkg/replyruntime/manual_resume_context.go
internal/services/ai_manual_resume_task_service.go
internal/services/message_service.go
internal/services/channel_message_outbox_service.go
```

`input.currentTurnSources` 的实际内部契约为
`ref/messageId/messageType/text`。`seqNo/sentAt` 不重复写入 Trace；需要校验顺序和
时间时按 `messageId` 回查现有 Message。Judge 协议失败若无法由严格 exact FAQ
机械恢复，会保留 `protocol_invalid/timeout/malformed` Trace 并发送不含酒店事实的
安全短答，不伪装成 `insufficient`、不转人工，也不增加第二次 Judge。当前普通异步
回复没有独立持久化 Job 重试器，贸然整链路重跑会重复模型费用和真实动作，因此这是
对计划中 `judge_protocol_retry` 名称的最终实施收口。

`reply_commit_service.go` 为每条真实消息记录 `taskIds[]`。非稳定 ID 核对持久化
Message 的 request ID、消息类型、正文及资源身份；稳定的 `manual_resume` Task/资源
归属 ID 命中时保留第一次已落库内容，只修复对应 Outbox。对于企微外部渠道，只有真实 Message
存在且对应 Outbox 为 `sent` 才算客户可见；RunLog 或 Message 自身的 `sent` 不足以
结算业务 Task。转接成功、人工恢复提示等 notice-only 消息不能冒充业务答案。

普通 `ai_reply` 与 AI 服务通知继续使用既有 ClientMsgID 和 Commit。发送端统一通过
Outbox claim，再在外部调用前重新校验人工路由；员工接管会取消 `pending`、`failed`
和已 claim 的 `sending` 普通 AI Outbox，AI 服务通知继续旁路。所有 `sending` 都不会被
`ListPending` 自动重放，因为当前企微外部接口没有可复用的幂等键。

企微客服入站同步由 `internal/services/wxwork_kf_inbound_service.go` 保证页面级重放纪律：
当前页任一消息消费失败时立即返回且不保存 `NextCursor`，此前已成功项依靠 `wx_msg_id`
和稳定 ClientMsgID 在重放时幂等跳过。`enter_session`、`session_status_change`、
`msg_send_fail` 与未知事件的 `WxWorkKFMessageRef`、`ConversationEventLog` 在同一数据库
事务内写入，事件日志使用微信 `msg_id` 作为稳定 request ID；任一写入失败时两者共同
回滚，重放后最终只保留一份 Ref 和一份事件日志。该边界由
`internal/services/wxwork_kf_inbound_service_test.go` 的真实数据库故障、游标不前移和
原子重放测试覆盖。本轮未新增表、字段或 Migration；回滚代码即可恢复旧行为，无数据
结构回滚。

`manual_resume` 的恢复 request ID 绑定来源消息 ID，ClientMsgID 使用 `ai_manual_resume_`
加 request ID 的 SHA-256 前 24 字节十六进制。严格匹配恢复 Message 时可以补建缺失
Outbox，符合条件且尚未开始外部发送的 `cancelled` Outbox 可以原子恢复为 `pending`。
`pending`、仍可重试的 `failed` 和五分钟内的 `sending` 只表示 `delivery_pending`；
`sent` 后复核完成。陈旧 `sending` 或 claim 后被人工取消的投递进入
`delivery_uncertain`：不重放、不重跑模型，恢复 Task 终止为失败，会话保持人工复核且
没有自动过期时间。迟到 CLI 回执只能更新仍为 `sending` 的行，不能复活已取消或已完成
Outbox。

`failed` 且 `next_retry_at=nil` 单独记为终态 `delivery_failed`，不重放、不补发、不重跑
模型、不增加恢复 Task 的 `RetryCount`；恢复 Task 直接失败并停止排期，会话保持或恢复
门店人工，清空自动过期时间并要求人工跟进。即使 Message 与终态 Outbox 已落库、RunLog
尚未来得及写入，请求绑定 Message 也会阻止再次运行模型；已有提交但缺少权威 Trace 的
其他情况进入 `delivery_uncertain` 人工复核。

残余边界：CLI Poll 已把 claimed 消息返回给外部桥接端之后，服务器无法阻止桥接端在
人工接管后继续发送已取走的数据。彻底关闭该窗口需要修改桥接 API，增加 attempt token
和发送前 CAS；本轮没有外部接口变更，因此不能宣称外部发送绝对原子。

### 风险与并行分支

- 主要残余风险是生产 Intent Profile 的 V2 字段稳定性、Judge 模型偶发协议失败，
  以及旧 RunLog 缺少可验证来源时只能走 legacy 兼容恢复；三者都必须通过有限真实
  场景和上线观察验证。
- 2026-08-31 的统计口径为：先执行 `git fetch origin`；本工作区取
  `git diff --name-only 40cc24b --` 的已跟踪路径，并行分支分别取从其与 `40cc24b`
  的 merge-base 到远端 tip 的路径；两组路径排序、去重后求交集。
- 按上述口径，与 `codex/customer-audit` 的完整同路径交集共 12 个：
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
- 合并时先保留审计分支的租户范围读取和发送约束，再合入本分支的真实 Burst/URef、
  统一 `ClaimForDispatch` 与人工恢复语义，并合并两边测试，禁止整文件覆盖。
- 按同一口径，当前与 `codex/ai-billing` 的同路径交集为 0；本轮不修改 usage 字段、
  计价公式或事件键。建议先合入本分支，再让审计分支基于最新提交 rebase/cherry-pick
  并重跑 Runtime、services 和路由相关测试。
- 2026-08-31 提交前按当前 13 个未提交文件重新 `fetch` 并核对：与
  `codex/customer-audit` 的实际待提交同路径交集只剩
  `docs/development-handoff.md`，与 `codex/ai-billing` 仍为 0。上面的 12 个文件是从
  `40cc24b` 统计整个历史分支差异的旧口径，不代表本次提交会触碰 12 个共享文件。

截至 2026-08-31，最新定向测试、以下普通测试、完整 Race、Vet 与 Linux amd64 构建
已在当前差异上通过；这些结果仍不能替代最终复审、提交、推送、部署和真实出站验收：

```bash
go test -p=1 \
  ./internal/ai/application/runtime \
  ./internal/ai/runtime/executor \
  ./internal/ai/runtime \
  ./internal/services \
  ./cmd/reply-runtime-eval \
  -count=1

go test -race -p=1 ./... -count=1
```

目标覆盖真实 URef、连续短句、模型拥有拆题权、回指后切换新主题、有界历史、
Deferred Task 精确恢复、恢复期新增消息、旧 Trace 兼容和竞态条件。真实模型、企微
最终投递及生产观察仍必须在 A、A+B 两阶段部署后单独记录，不能用自动测试替代。

截至 2026-08-31，当前工作树实现已收口，并重新通过全仓普通测试、完整 Race 集合、
`go vet ./...`、`git diff --check` 和 Linux amd64 双产物构建。Server SHA-256 为
`e9bcd0e551f40ffbfa57fd899000baa7d305443035cfc92618e3eafde8ed3d59`，评测器
SHA-256 为 `5fa0c9c34f374f4e24d63e092521c4274c15547717ffbb6a4611dbc1b502e068`。
本节不声明本轮已经提交、推送、部署、完成新的真实模型复测或企微最终出站验收；这些
步骤仍须按实际记录单独更新。

Release B 异常时优先回滚到已验证的 Release A；Release A 异常再回滚到生产基线
`40cc24b`。本轮没有数据库或知识库变更，回滚只切换程序 release，不回滚消息、
会话、当前运行配置或客户数据。

最终发布拓扑必须是：Release A = `40cc24b` 加最终 A-only 修复；Release A+B = 最终
Release A 再叠加 B 修复。不能从已经混合 A+B 的单一线性 tip 反向声明出 A。提交和
构建完成后分别记录两个最终 commit、release 目录和 Linux 二进制 SHA-256。

## 2026-08-31 A+B 定向验收后最终收口

原始修复基线是 `40cc24b`；此前 A+B 运行记录中的提交 `39e8656` 和 release
`/opt/agentdesk/releases/20260831-142758-context-judge-ab-39e8656` 仅作历史参考，
不代表本轮工作树修改已经随该 release 部署。首轮定向真实模型验收暴露的问题集中在
现有 Intent/Judge 边界，本次没有扩大到数据库、知识库、人工状态机、Outbox、计费、
模型配置或外部接口。

当前工作树实现：

- 已完成业务答复后的比较、复述和省略追问重新进入业务 Task；
  `clarification_answer` 只用于紧邻 AI 或人工客服正在追问的必要字段。
- AI 或人工客服以“有没有/是否/您是问……吗”等是非式问题确认必要字段后，客户回复
  “是的啊”必须回到原业务 Task，不能继续作为 `interaction/clarify`；姓名、房号、偏好、
  条件、范围和选项等直接回答使用同一套紧邻上下文规则，泛化帮助询问保持原行为。
- “外卖地址再说一遍”等名词式业务目标若表达明确的信息或动作目标，会触发一次
  Intent 协议修复；不能因为没有问号就降为普通闲聊或泛化澄清。
- 上一轮含多个问题时，复述锚点只有唯一命中其中一题才强制回业务 Task；裸“再说一遍”
  仍保持澄清。客服追问槽位后，“谢谢、好的、不用了”等已明确标注的纯互动可以收尾，
  “是的/不是”、房号、姓名等真实槽位值仍必须继承原业务任务。
- `objective=action_request` 且原话已经是自包含执行请求时，包括发资源、换房、派人维修
  和配送用品，即使模型漏掉 Resource/Tool 标记，也必须触发 Intent 协议修复；房号等
  紧邻槽位回答仍保留上下文语义。
- 动作自包含判断采用“模型声明 action_request + 当前原话存在具体目标”的边界，不依赖
  有限动作动词表；“预约早餐、申请发票、配送矿泉水”等完整目标会触发修复，“帮我、
  我要、需要”以及“我想要一份、给我送一个”等只有数量量词而无对象的请求继续允许澄清。
- 肯定 FAQ 前缀只有在后续没有否定、限制、数量改口或当前必要事实不确定时才能确认
  前提；“是的，但具体数量不确定”不能机械继承问题中的数量，无关细节的不确定性不会
  抹掉已经明确确认的业务事实。
- 多主体 FAQ 若答案显式只写其中一部分主体，不能用开头“是的”把事实扩到其他主体；
  “都是/都有/均为”只有在当前子句列出全部主体，或答案开头仍保持问题整体主语范围时
  才能继承全部主体，无关对象或已经缩窄后的整体谓词不得重新扩大范围；整体结论后的
  单主体补充不会抹掉前面的整体覆盖。FAQ 问题已完整列出同类型主体时，只允许
  “不同平台、各平台、这些房型”等受控全称总称继承，部分、其他、某个及混合类型不继承。
- `interaction/clarify + resolved_from_context` 触发现有一次 Intent 协议修复，覆盖
  “外卖地址再说一遍”被降成闲聊的真实失败形态，不使用文本关键词分类器。
- 办公桌与沙发等不同答案结果按模型 Task 分开；同一对象的紧密答案目标仍可合并。
- Judge 普通单题保持 15 秒，只对 Task/Candidate 较多的批次扩到最多 28 秒；父级回复
  deadline 始终为 Generate、Commit 等下游保留 12 秒，再裁剪 Judge；没有阶段预算时
  不发起 Judge 模型请求。
- 多题拆分仍由 Intent 模型负责。本地只用保守原子候选做协议验收，不创建 Task；
  “分别、各自、逐项、逐个”等明确逐题要求可作为覆盖证据，用来拒绝明显错误合并。
  相同任务不会因 `resolvedText` 的不同措辞重复执行；同一 URef 内仅在 Task 文本能唯一
  定位时校验客户原文顺序。
- Judge 的 Fact 或 missingAspects 局部类型错误只修当前层，兄弟 Task 不受影响；
  错误设施、跨房型矩阵、未知 Candidate 和非法 decision 仍严格失败。
- “两瓶是否都免费”必须同时保留数量和费用；只机械要求所选 FAQ 已确认的查询数量，
  不把“一间房、两个人、三间房”等范围数量一律当成答案值；`2瓶/两瓶` 等价。
- “加一条浴巾、帮我拿两瓶水、推荐一个房型”中的数量是服务参数或结果基数，不作为
  知识事实槽；“两瓶是否免费、房间有两个枕头吗”等事实数量仍严格绑定。
- 完整 FAQ 被 Judge 误判为 `partial` 时，只清理同一主体、同一事实维度已机械证明的
  陈旧缺失项并晋升 direct；其他主体数量、配送范围和条件仍保持缺失。
- 单一明确主体下，所选 FAQ 的唯一数量与客户明确数量冲突时严格失败，禁止用“四瓶”
  回答“两瓶”；Intent 未输出实体时，单一查询数量和单一所选 FAQ 仍检查同单位冲突；
  多主体 compound 按“主体 × 数量”绑定，每个主体都必须由对应 FAQ 分句或问答单元
  支持，模型 Fact 不能交换主体和值；合计数量只接受明确总量，或全部命名主体的同单位
  唯一分项机械求和，缺项、异单位或冲突总量都严格失败。
- 同一时间维度按开始、结束和时长逐槽检查；同时询问工作日、周末或节假日时按
  “主体 × 条件 × 时间槽”闭环。“早餐几点开始、几点结束”只有开始时间时继续保持部分答案，
  不能被晋升为完整答案。
- 配送地址的不同问法不再被本地冲突签名误杀，门店名、楼层和房间号仍必须完整进入
  Generate 事实。
- 自包含的业务问句或已经声明知识、资源、工具、接待动作的 Task 不允许伪装成
  `interaction/clarify`；没有问号的“早餐时间告诉我”等信息请求同样按结构化
  `objective + 当前原话 + 未决候选` 校验，不新增业务关键词分类器。真正缺少对象或
  多个同类型地点尚待客户选择的澄清仍保持原行为。
- 数量保护检查全部所选 FAQ：正确数量与同主体、同范围、同单位冲突数量并存时严格失败，
  Intent 缺少实体的多候选组合也不能绕过；会议室等明确不同范围不会污染客房答案，
  “四瓶饮料”这类明确其他物品分句也不会污染“两瓶矿泉水”，无主体“四瓶”仍继续检查。
- 单主体价格或时间可以沿用 FAQ 问题中的唯一主体，但该 FAQ 问题或答案必须明确包含
  当前 Task 主体，且答案分句处于同一范围、没有另一个显式业务主体；“早餐不收费”
  不能支撑“停车免费”，“晚餐六点”不能支撑“早餐几点”。
- Intent 缺少实体时，会从数量相邻文本机械恢复可唯一配对的主体；单一数量和
  “矿泉水两瓶、饮料四瓶”这类多数量均逐主体校验，其他物品不污染，主体间也不能
  交换数量。“都有/同时有/都配/均有”等谓词只共享其后的数量；无标点的“两个和四个”
  结构在查询和候选答案中统一按连接词相对数量的位置绑定前后主体。无法唯一恢复时
  保持保守拒绝。
- 中文自然时刻、“几点开始和结束”以及 FAQ 问题含主体、答案只写时段的结构均纳入
  现有时间槽校验；多个开始时间不会被解释为开始和结束，中文时段前缀会覆盖完整范围，
  “七点开始到九点”等相连范围保留起止端点，不同表达的同一时刻按机械等价处理；
  FAQ 主体过滤首个无主体时间分句，工作日与周末条件互补；“晚上八点到两点”和
  “晚上十点到次日两点”都保留跨午夜边界，不把凌晨两点误写成下午两点。
- 同一 Task 询问多个时间主体时按主体逐槽校验；已经写明午餐等其他主体的时间事实不会
  被强行改写成早餐事实。时间值按分句与显式主体绑定，主体只传播到后续无主体时间分句；
  “营业/供应/开放时间”可绑定 FAQ 唯一主体，入住、退房、开门、关门不可被重绑。
  “从什么时候到什么时候”“几点至几点”同样要求开始、结束两槽。
- malformed `missingAspects` 只在重建后的所选 FAQ 已机械完整时允许从 `partial` 晋升，
  配送范围等真实缺失仍保持部分答案。
- 多个所选配送地址若给出不同门店名或不同街道地址则严格失败；同一地址补充楼层、
  房号等模板信息仍可组合；“南七店/东七店”短门店名参与冲突检查，泛称不作为地址值。
- 老板等人物姓名比较会忽略“先生、女士、老师”等称谓，不把同一人误判为冲突；
  锚定裸姓名也参与冲突检查，未知、保密和角色词不会被当成人名；不同姓名的选中
  答案严格拒答，不能进入 Generate。

验证已经完成：全仓库普通测试、完整 Race 集合、`go vet ./...`、`git diff --check`、
Linux amd64 Server 和评测器构建均通过。部署前仍需完成三遍完整 diff 审查、提交推送、
生产 release/数据库/Intent Profile/配置备份。部署后只复测 AA01、AA03、AA04、AA05、
AA06、AA08，不运行 50 轮测试。

## 2026-08-31 三遍完整审查后的五项最小收口

三遍审查针对完全相同的代码差异执行，最终只补充以下五类边界，没有扩大回复架构：

- Task 去重增加主 `sourceRef` 所有权。不同消息中的相同短句不会再合并；同一来源和同一
  目标的合法重复仍可折叠。
- 槽位追问后只正向检查可机械确认的槽位值。存在房号、姓名或其他明确槽位内容时，
  即使同时带有“谢谢”或抱怨，也必须回到原业务 Task；`哈哈`、`晚点再说` 等普通互动
  不会被误判成槽位答案。“还有什么需要吗”等开放帮助询问不属于槽位追问；明确取消
  上一任务时必须使用 `cancel_previous`，避免冻结任务之后再次恢复。
- 单一上一题的复述同样检查锚点。裸“再说一遍”可以复述唯一上一题；“名字再说一遍”
  等带目标的请求不能错误绑定到早餐、停车等无关上一题。
- 动作请求里的数量不会因为裸 `是否/是不是/有没有` 被当成知识事实。
  `帮我拿两瓶水，是否可以送到房间` 的“两瓶”是动作参数；“两瓶是否免费”仍要求数量
  和价格证据同时闭环。
- Intent 缺少 `entities` 时，非比较、唯一 `price` aspect 的单主体问题从自包含 Query
  保守恢复主体并执行价格冲突保护；`早餐免费` 不能回答 `停车免费`，多主体或无法唯一
  恢复时不做本地猜测。

2026-08-31 的冻结差异已通过聚焦测试、全仓普通测试、完整 Race、`go vet ./...`、
`git diff --check` 和 Linux amd64 双产物构建。当时 Server SHA-256 为
`3f4c3c9f8cdb56265ea198a909a6334981bbf9b6ebd36d2be307c8769bf1fe5a`，评测器
SHA-256 为 `693e69cec620eebb919fc7edc19ff9ca23a54ea387e1e47668c71306c9e1185e`。
这些 SHA 不对应当前未提交源码，最终提交后必须从干净 detached worktree 重建并记录
新的 commit、产物 SHA、release 和部署结果。

本轮未修改 model、Migration、DTO、枚举、WebSocket、数据库、知识库、运行配置、
模型供应商、计费、Token 统计、人工状态机或 Outbox。重新同步远端后，当前 16 个已跟踪
待提交文件与 `origin/codex/customer-audit` 仅重叠本文，与 `origin/codex/ai-billing`
重叠为 0；合并审计分支时必须同时保留其顶部历史资料警告和本节记录。

本节仍不把提交、推送、生产部署、真实模型复测或企微最终出站写成已完成；这些动作完成后
应按真实 commit、release、备份和场景结果继续补记。

## 2026-09-01 发布阻断复审后的最终修复

三遍完整只读审查基于同一冻结差异执行，发现两个 P1 和三个 P2。最终修复仍限定在
Intent 协议校验和 Judge 证据落地，没有增加模型阶段或改动外围状态机：

- 合法 `interaction/weather_query + needsTool=true` 不再被协议校验拒绝；AI 身份和正常
  社交问句也不会因为是自包含疑问句被误判成酒店业务目标。业务矛盾校验只约束
  `interaction/clarify`，意图拆题和非业务互动仍由 Intent 模型决定。
- 紧邻上一条真实 AI 答复时，明确“答非所问、我问的是……、你刚才不是说……”却被
  模型输出成普通 interaction，会触发现有一次 Intent 协议修复；本地不创建
  `answer_rejected` Task，不对人工答复、普通速度抱怨或孤立“真的吗/为什么”生效。
- Intent 漏掉 `entities` 的单主体存在性问题，从自包含 Query 保守恢复唯一主体并贯穿
  Candidate 匹配、所选 FAQ 问答单元和 Fact grounding；“有早餐吗”不能使用“有晚餐”
  的知识，办公桌/书桌等现有规范化同义词仍可匹配，多主体问题不做本地猜测。
- 同一主体的数量按“主体 + 条件 + 单位”比较；工作日两瓶、周末四瓶是两个合法条件值，
  只有相同条件下的不同数值才构成冲突。
- FAQ 问题中的窄条件只在答案没有重新声明该条件维度时继承。答案明确写“每天、每日、
  全年、不分工作日和周末”时只扩宽日期类型；写“全天、全时段、所有时段”时只扩宽
  昼夜时段。一个维度的全称表达不能抹掉另一维度的限制，也不能把两条各自只覆盖一个
  条件的 FAQ 拼成条件交集。

以下验证和产物只对应较早冻结差异，不对应当前未提交源码：

```text
go test -p=1 ./... -count=1
go vet ./...
go test -race -p=1 ./... -count=1
git diff --check
Linux amd64 Server / reply-runtime-eval 双产物构建
```

历史 Server SHA-256：
`a2e6e8f637da06f269790253b36075323cea49d8ab9ab952bc957ce5868d7694`；
历史评测器 SHA-256：
`5678ee6ab36a739030c5a4c1a02a076787dfa894c634a0783ed23375da7fb083`。

本轮仍未修改数据库、知识库、模型配置、计费、Token 统计、人工状态机、Outbox、外部
API、DTO、枚举或 WebSocket。最终提交后必须从干净 detached worktree 重新运行全部验证
并构建；此处不声明已经推送、部署或完成真实模型/企微出站验收。

## 2026-09-02 Judge 存在性子类最小修复

本轮只修改 `knowledge_evidence_judge.go`、对应测试和现行设计文档。针对“有拖鞋吗”已召回
门店“一次性拖鞋”正文，却因其他候选是“洗澡用/塑料拖鞋不提供”而被 Judge 本地校验判成
`protocol_invalid` 的问题，统一修正存在性主体关系：肯定的具体子类可以证明上位类别存在，
否定具体子类不能证明整个类别不存在，不同子类的一正一负不再互相冲突；同一主体、房型、
范围或条件下的真实冲突仍保留。

普通确定性 FAQ 选择继续使用 `0.85`。只有门店用品类 `service_request`，以及
`hotel_info + availability` 在 Judge 成功返回 `insufficient` 后，允许以 `0.70` 为最低分
执行一次本地完整 FAQ 恢复；`protocol_invalid` 不被分数规则覆盖。恢复仍校验预算内外候选
一致、事实完整和同层无冲突。没有修改 Intent、Retriever、Generate、转人工、消息聚合、Outbox、数据库、知识库、
模型配置、计费、外部 API、DTO、枚举或 WebSocket；也没有新增模型调用或 Migration。

### 提交与生产部署结果

- 修复提交：`cfa049eb203ef01c90b89a96bb944253c10b2553`，分支
  `codex/reply-runtime-core-fix-20260902`，已推送到 `origin` 和 `weibao`。
- Linux amd64 Server SHA-256：
  `20c66f97659d2c1e80587d68fe5c2e4df2b0e5bce9d4910481f1e8c6d912032d`。
- 生产 release：`/opt/agentdesk/releases/20260902-090246-judge-cfa049e`；原子切换前 release：
  `/opt/agentdesk/releases/20260902-064841-intent-retry-b354b14`。
- 当前配置实际连接数据库为 `cs_ai_agent_ai_billing_4db7993`。数据库、旧 release、配置、环境文件及
  SHA-256 清单位于 `/opt/backups/agentdesk-20260902-085829-pre-judge-cfa049e`。
- 聚焦四包测试、`go vet` 和 `git diff --check` 均通过。部署后 `agentdesk.service=active`、
  `NRestarts=0`、`8083` 返回 HTTP 200；生产消息总数/最大消息 ID 保持 `4757/17193`，
  没有新增 `pending/failed` Outbox。
- 本轮未执行 50 轮或其他批量主动评测，也未修改生产 Intent Profile、知识库、模型配置或数据库结构。

## 2026-09-02 本地协议校验收口修复

- 修复提交：`d61a8e1d9ba6c0253dba940873a91d5dcc5a5612`。只修改 Intent/Judge
  协议校验及对应测试，没有新增模型调用、状态或业务入口。
- Judge 仅在 Intent 已判定为周边推荐时，不再把口语中的“有没有”追加为单主体
  `existence` 要求；早餐/晚餐、停车/早餐价格、房型和配置范围等明确冲突保护保持不变。
- Intent 仅修正连续消息中被模型错误标成 `resolved_from_context` 的自包含业务问题；保留
  Task 数量、顺序、原文和全部 `sourceRefs`，不安全的 `resolvedText` 回退到当前问题原文。
- `go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1`
  与 `git diff --check` 通过。`customer-audit`、`ai-billing` 未修改相同文件，无 Migration、
  DTO、枚举、WebSocket、数据库结构、知识库、模型配置、计费、Outbox 或转人工变化。
- 生产 release：`/opt/agentdesk/releases/20260902-141410-local-validation-d61a8e1`；Server
  SHA-256：`dd6148ff13352ad64753e7095916e6dfdee2ec702ada8104132fcd8bd1b39557`。
- 回滚点：`/opt/agentdesk/releases/20260902-090246-judge-cfa049e`；部署前完整备份：
  `/opt/backups/agentdesk-20260902-141301-pre-local-validation-d61a8e1`。
- 隔离生产冒烟会话 `2065` 验证“附近好玩儿 + 停车”，会话 `2066` 验证
  `OK → 我好困呐 → 有没有咖啡`，均真实完成并收到回复；未执行 50 轮测试。

## 2026-09-04 Intent 本地校验瘦身与外部代办边界

- Intent 模型继续唯一负责拆题、意图分类和 `resolvedText`。本地仅保留 JSON 字段、枚举、
  `sourceRefs` 范围与顺序、`text` 原文归属、上下文指针和精确重复 Task 合并；删除 90 个
  已不可达的旧中文语义判断函数，不再用关键词、字符重叠或本地候选重新做一遍 NLU。
- 同轮上下文补全只能引用更早 URef，且 `relationToPrevious=independent`；跨轮回指继续要求
  previous 关系和紧邻客户/客服问答。`conversation_recap` 只要求存在实际可供生成阶段使用的
  有界历史，不再误要求完整紧邻问答对。
- 新增精确子类 `service_request/external_proxy_action + action_request`：代点外卖、叫车、
  代买、代订等外部操作，Judge 只选择地址、电话、入口或步骤等自助信息；有知识时回答
  真实能力边界和自助方案，知识源不可用或无证据时只说明不能代操作，不因此转人工。
  酒店内部送物、补用品、维修、开门、换房、清洁，以及明确人工请求和知识库明确“转接”
  均保持原路径。
- `EvidenceQuery` 仅用于提高召回，不再作为 Judge 的客户语义问题；外部代执行任务只在
  检索侧补充同一目标的地址、电话、入口或自助步骤。Judge 选中的单条纯“转接”门店候选
  不再因客户问法与 FAQ 标题不完全相同而被本地协议误杀，同层冲突保护仍保留。
- 未修改数据库、Migration、知识库、模型配置、计费、Outbox、人工状态机、API、DTO、枚举
  或 WebSocket。`customer-audit`、`ai-billing` 未发现同文件改动。
- 验证通过：
  `go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services -count=1`
  和 `git diff --check`。

### 提交、部署与生产冒烟

- 最终修复提交：`4e35ee4`，已推送到 `origin` 和 `weibao`；生产 release 为
  `/opt/agentdesk/releases/20260904-065600-external-proxy-4e35ee4`，Server SHA-256 为
  `1d6369d177c18508a435b36009466f5657f50d4dbe6add0bc6822afd46882e0d`。回滚点保留为
  `/opt/agentdesk/releases/20260904-055152-reply-core-9c58cf0`。
- 部署后 `agentdesk.service=active`、`NRestarts=0`、`8083` 正常。隔离生产会话 `2084`
  通过线上服务真实发送“帮我点个外卖”，7.806 秒完成，最终回复为：
  `不好意思，这类外部操作我们没法直接替您完成。外卖地址可填写：丽斯未来酒店合肥南七店+对应楼层房间号。`
- Trace 确认 Intent 为 `service_request/external_proxy_action`、`objective=action_request`；本轮只请求
  一次 `knowledge_lookup`，检索 Query 为“外卖办理时酒店地址怎么填写；外卖如何自行办理”。Judge
  使用 `deepseek-v4-pro`，门店层裁决为 `direct_single`，选中南七店外卖地址；最终保持
  `AI_SERVING`，未转人工、未产生虚假代下单承诺，Generate 仅执行一次。
- 本轮未运行 50 轮或批量测试，未新增任何代码修改、数据库结构变更、知识库变更或配置变更。

## 2026-09-05 Judge 适用前提与业务政策最小修复

- 代码提交 `c22d479`：仅修改 `knowledge_evidence_judge.go` 的提示词和对应测试。明确否定前提后，
  依赖该前提的问题不再列为缺失；同题完整业务政策可以直接回答，不能强改成肯否或价格结论。
- 未修改 Intent、检索、阈值、Generate、本地校验、知识库、模型配置、计费、转人工、Outbox、
  数据库结构、DTO、接口、WebSocket 或权限，无 Migration。隔离测试仅新增测试会话与消息。
- `go test -p=1 ./internal/ai/runtime/executor -count=1`、gofmt 和 `git diff --check` 通过。
  提示词约束与模拟 Judge 协议测试不能替代模型效果验证。
- 已推送 origin/weibao；fetch 后两个并行分支未修改相同 Judge 文件，无须为本改动 rebase，
  修复提交可独立 cherry-pick。customer-audit 对本交接文档有追加内容，合并时保留双方记录。
- 生产 release：`/opt/agentdesk/releases/20260905-050232-judge-policy-c22d479`；SHA-256：
  `06a7d25576e04c14db651929b99b5bccc65c805e9163c1e329cc1a433f380392`。
  回滚点：`/opt/agentdesk/releases/20260904-065600-external-proxy-4e35ee4`；配置及旧二进制校验备份：
  `/opt/backups/agentdesk-20260905-050232-pre-judge-policy-c22d479`。无需数据库或配置回滚。
- 仅执行两个真实模型隔离轮次，均为 1 Intent + 1 Judge + 1 Generate，服务 active、NRestarts=0。
  会话 2086 / runlog 7646：平台价格 Judge 为 direct_single，保留权益差异与比价建议，不再转接；
  但 Generate 输出“不同平台价格和权益可能不一样”，扩大了 Judge 只确认权益的措辞，不能算完整通过。
  会话 2087 / runlog 7647：步行题仍为 partial 并转接，目标尚未解决。检索日志原文只有“需要驾车前往”，
  没有“不能步行”；此前根据旧 Judge 输出推定知识库明确禁止步行不准确。未继续扩大修改或增加模型测试。
- 两题运行耗时分别 9882ms、9983ms。完整 Trace 在服务器
  `/tmp/agentdesk-judge-policy-c22d479-smoke.jsonl`。测试完成后仅将隔离会话 2087 恢复 AI，取消其
  恢复任务 104，保留全部测试消息；真实客户会话未修改。当前保留新 release，不宣称两类问题已全部解决。

## 2026-09-05 连续30轮后限定修复

- 仅修改五个运行文件：`intent_model_detector.go`、`intent_human_route.go`、
  `knowledge_evidence_judge.go`、`multi_reply_output.go` 和 `conversation_handoff_confirmation_service.go`，
  以及对应回归测试。Intent 只允许缺少 Markdown 闭合标记但正文为完整唯一 JSON 对象的本地拆包，
  截断、尾随输出、未知字段仍拒绝；不新增重试。
- 转接仅对 DeferredTaskIDs 中明确的 hotel_info 咨询关闭房号收集；实际服务及旧调用保留原策略。
  混合任务只把待转接服务 Task 文本传给既有房号判断，不使用已回答任务或咨询文本触发房号。
  新入口是向后兼容的内部服务方法，无外部 API、DTO、数据库、Migration、权限或状态变化。
- Judge 同次裁决检查事实与未知项一致性，部分枚举不冒充穷尽名单；Generate 保留主体、范围及确定程度。
  未修改检索、知识库、模型、计费、Outbox、人设或上下文。完整 JSON 模拟测试不代表所有截断输出可恢复。
- gofmt、`git diff --check`、`go test -p=1 ./internal/ai/runtime/executor ./internal/services -count=1` 通过。
  customer-audit 在转接服务有租户隔离修改，本次不覆盖其函数；合并时保留双方改动，内部服务入口与调用方
  应同一提交合并。ai-billing 无同文件修改，无字段/状态语义冲突。部署回滚点为
  `/opt/agentdesk/releases/20260905-050232-judge-policy-c22d479`；不需要数据库或配置回滚。
  真实验证限定不超过12轮，不运行30/50轮。

### 部署及限定验证结果

- 修复提交：`3cbee836a62c1016cc777636a7685666259f4839`，已推送 origin/weibao。生产 release：
  `/opt/agentdesk/releases/20260905-075914-reply-boundaries-3cbee83`；Server SHA-256：
  `ba2941119621547a828fd26c1e14fcea56d0528e349b3f5a5d39836d00c0382d`。切换前备份：
  `/opt/backups/agentdesk-20260905-075914-pre-reply-boundaries-3cbee83`。最终只读健康检查确认
  当前 release 未变、服务 active、NRestarts=0、8083 HTTP 200，部署以来 systemd error 级日志无记录。
- 使用线上程序和真实模型完成且仅完成12轮，隔离会话为2089至2096，ChannelID=0，不代表企微最终投递验收。
  原始记录为服务器 `/tmp/agentdesk-targeted12-3cbee83-20260905-080039.jsonl`；保留测试消息，
  测试结束清理了隔离会话人工恢复任务并恢复AI，未修改真实客户会话。
- 12轮均有回复，但不能记为12/12业务通过。能力咨询转接不再追问房号；马桶维修及混合任务仍先问房号、
  补充后真实转接。房型组合事实在本次样本中正确；五问顺序及必要内容完整，合并为三条消息，耗时20532ms。
  完整JSON缺失闭合代码围栏的兼容由自动回归覆盖，真实WiFi追问未再出现协议失败。
- 未解决项：Judge本次保留了权益政策、驾车要求和配送范围未知，但Generate仍把权益差异扩写成价格差异、
  把驾车要求扩写成走路不方便，混合任务仍用“有的”回答未知的机器人送房能力。生成规则确实进入提示词，
  不能归因于规则未注入；提示词约束不足以证明消除了事实外推。
- WiFi追问回答了大堂与房间配置相同，但召回知识仅写酒店账号密码，未明确两个区域相同，因此只确认本轮
  有回复及协议正常，不把区域范围正确性记为通过。模型轮次Trace未见新增模型阶段或额外Generate尝试。
- 当前保留3cbee83 release及原回滚点，不追加运行代码、模型测试或配置修改。验收结论为核心房号分流已验证、
  表达边界及范围推断仍有问题；不能据此宣称回复质量全部修复。后续合并交接文档须保留customer-audit追加记录。

## 2026-09-21 PMS 只读问答与人工路由收口

- 修复提交：`0d91c98`，已推送 `origin` 和 `weibao` 分支
  `codex/pms-live-from-weiwei-20260920`。并行检查确认 `customer-audit`、`ai-billing`
  在本轮目标运行文件上没有新增同文件修改。
- 运行链路调整为：PMS 只读任务保留 `needsTool=true`；同时需要门店政策时保留
  `needsKnowledge=true` 并行检索。知识未命中、检索失败、Judge 不足和 `answer_rejected`
  不再单独授权人工路由；只有客户明确要求人工、知识库明确写明转人工或严重安全风险才路由。
  只读 PMS 结果不得表述为已换房、已升房、已锁房或已延退。
- PMS 增加只读 `price_difference` 评估，使用真实订单详情、库存、日期、币种和计价口径；
  空价格、跨日期缺失和口径不一致只返回待确认/无法确认，不把空值解释为免费。未开放任何 PMS 写操作。
- 验证通过：
  `go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime/tools ./internal/pkg/replyintent ./internal/pkg/toolx ./internal/pms ./internal/services -count=1`
  以及 `git diff --check`。
- `test-2` release：`/opt/agentdesk/releases/20260921-pms-live-0d91c98`；
  Server SHA-256：`2ebb61f1643cdd70c63650bd7b68988334269578c7f47653f88e84289052a769`。
  切换前备份：`/opt/agentdesk/backups/20260921-1218-pms-readonly-0d91c98`；
  回滚点：`/opt/agentdesk/releases/20260920-pms-live-d95f117`。
- 部署后确认 `agentdesk.service=active`、`NRestarts=0`、`8083 HTTP 200`，
  `AGENT_DESK_PMS_ENABLED=true`、`AGENT_DESK_PMS_ALLOW_WRITE=false`。未修改数据库、知识库、
  运行配置或“薇薇”备份。现有企微坐席过期告警（`err_code=9003`）仍属环境问题，未在本轮改动。

## 2026-09-21 人工路由与 PMS 优先级最小修复

- 本轮只修改 `answerability_gate.go`、`intent_human_route.go`、`handoff_graph.go`、
  `handoff_graph_tool.go`、`pkg/utils/handoff.go` 及对应回归测试。
- 当前原话必须明确表达“转人工/找同事/人工客服”等诉求，Intent、Graph Tool 和
  Handoff Graph 三层均执行守门；普通服务请求、投诉、价格/赔偿咨询、知识不足和
  PMS 查询失败不再单独触发人工路由。
- 知识库明确要求转人工时仍可转接；同一轮存在 PMS 只读任务时，PMS 查询优先，
  知识库转接指令不能抢占订单、房态、会员或差价回答。
- Judge/覆盖修复失败在消息仍可继续处理时不再吞掉原始 Task 或独立 PMS 任务；
  仅在实时路由已失效时保留失败退出。
- 无数据库、Migration、模型、知识库、外部 API、Outbox 或 PMS 写入变更。
- 聚焦验证通过：
  `go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime/graphs ./internal/ai/runtime/tools ./internal/pkg/utils ./internal/services ./internal/pms -count=1`。
- 提交前需保留 `0d91c98` release 作为回滚点；部署目标仅为 `test-2`，并保持
  `AGENT_DESK_PMS_ENABLED=true`、`AGENT_DESK_PMS_ALLOW_WRITE=false`。

## 2026-09-22 PMS 客户目标确定性执行与回复链路结构收口

- 目标：系统性解决手机号重复追问、订单已存在但查不到、升房/换房漏调用、差价与日期错误、PMS 成功被知识失败抹掉、普通问题误转人工及失败定位困难。
- 运行链路保持 `Intent -> Knowledge/Judge -> PMS Read Plan -> Generate -> Commit -> Outbox`；没有新增 Judge、Agent、消息状态机或本地中文语义门。
- 新增 `pms_read_planner.go` 与 `pms_read_execution.go`：每个 PMS 客户目标使用确定性只读计划；相同查询在单轮复用，差价复用同轮订单和库存事实，不重复查询。
- 上下文只复用同 session 已确认手机号/订单；当前纠正、否定、取消优先。日期支持完整日期、月日、日号和相对日期，续住目标日不再被改写成单晚。
- 目标房型必须明确；库存逐房型逐日覆盖完整入住期。订单事实补充读取 `reserveProductList`，但仍只输出客户侧白名单。
- 知识/PMS 混合 Task 分别守边界；PMS 事实不替代知识证据。PMS 优先只作用于同一 Task，独立知识明确转接仍可执行。
- 同 Task 的知识转接改为结果后裁决：PMS 有可用事实时直接回答，PMS 缺参数、空结果或失败时恢复 Direct/AnswerThenHandoff；原 Judge 事实、候选和答案不会在 PMS 前被清空。
- 订单查询支持手机号或 `customerNo`，会员查询仍只接受手机号；多日期纠正保留替换后的完整区间，库存只按请求日期且每天具备 `available` 才形成可售结论。
- 预订单/接待单等独立只读请求，以及日期已明确时的库存/会员请求可并发执行；依赖步骤仍串行，运行级相同查询缓存已改为并发安全。
- 人工路由收紧为客户当前明确要求或当前 Task 知识明确转接；知识不足、PMS 失败、普通服务和投诉不自动转接。明确建维修工单继续使用现有确认工具。
- 无 model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox 或计费变更；HPMS 保持只读，`allowWrite=false`。
- 双轮验证通过：`go test -p=1 ./internal/pms -count=2`、`go test -p=1 ./internal/ai/runtime/tools -count=2`、`go test -p=1 ./internal/ai/runtime/executor -count=2`、`go test -p=1 ./internal/ai/runtime/... -count=2`，以及 `git diff --check`。并发聚焦路径另通过 `go test -race ... -count=2`。
- 并行影响：`customer-audit` 可能继续追加本文件，合并时保留双方段落；`ai-billing` 无字段和计费语义变化。建议本轮回复链路提交先独立 review，再合并其他同时修改 Intent/Judge/人工路由的提交。
- 回滚边界：程序可回到部署前 release；数据库、消息、知识库和“薇薇2”不回滚。PMS 写能力始终关闭，不存在需要逆向撤销的 PMS 操作。

## 2026-09-23 客户真实需求连续对话收口

- 目标：修复其风会话中“个人退房时间被答成通用政策、查查我的丢上下文、查到订单后又说没查到、PMS 原始信息倾倒、换房不主动给选择、沐阳不带房字无法识别、枕头购买没有商品卡”等问题。
- JEV 增加同 session 最近唯一业务 Task 上下文；只用于“查查我的、就是这个、那你回答”等真实省略续问，多业务题时不猜。成功 PMS RunLog 同时恢复 Intent/ReplyPlan 中的手机号和订单定位。
- “我什么时候退房”进入个人订单查询；“你们有会员吗”走通用知识，“我是会员有啥优惠”走个人会员查询并复用已确认手机号。
- 预订单和接待单按关联 ID/入住区间合并成一次住宿，客户侧只输出当前问题所需字段。完整结果不再追加通用缺失话术，也不展示 PMS 名称、内部状态码、订单 ID 或操作免责声明。
- 换房未指定房型时排除当前房型和零库存，主动提供真实可选房型；指定“沐阳”等不带“房”字的真实房型时，核对完整入住期库存、候选房号和差价。价格或库存缺失时明确说明未知，不把空值当成免费或无房。
- 单个已锁定 Judge/PMS 答案直接使用服务端内容；单个普通无事实 Task 可返回自然文本。多 Task、工具调用结果和事实覆盖仍保持严格 `replyParts` 协议。
- 枕头购买新增 `provide_pillow_product -> pillow_product -> shop_product` 资源链，复用企微现有 Outbox 富媒体发送；商品 payload 来自其风历史真实 `content_type=597` 卡片。送/换/加枕头及脏、坏、不舒服仍走客房服务，夸赞枕头不自动营销。
- 企微入站 DTO 兼容字符串和对象型 `content`，597 商品卡完整保留商品与店铺协议字段。无数据库、Migration、外部 API、WebSocket、计费或 PMS 写入变化；`allowWrite=false` 保持不变。
- 双轮通过：`go test -p=1 ./internal/ai/runtime/executor ./internal/pms ./internal/ai/runtime/tools -count=2`；枕头 DTO、Service、Intent、Commit 聚焦测试连续两轮通过；`internal/services` 全包单轮通过。全包 `-count=2` 会触发既有测试共享数据库的重复初始化失败，本轮未修改这些无关测试。
- 并行影响：`customer-audit` 同样修改 `intent_model_detector.go`，后续合并需人工保留双方变更；`ai-billing` 无计费语义变化。部署仅更新 test-2，程序异常回退 release，不回滚数据库、消息或“薇薇/薇薇2”。

## 2026-09-23 首问可靠性与知识回复延迟收口

- 目标：修复新企微会话的首条客户问题被欢迎语/欢迎资源误判为过期，以及简单知识问题在 Judge 已形成完整答案后仍重复调用 Generate、出现空输出重试和约 45 秒延迟的问题。
- 新好友欢迎文本、欢迎图片、默认入住小程序和欢迎定位使用精确 `clientMsgID` 前缀识别。它们仍正常发送并保留为客户可见历史上下文，但不再作为更新客户轮次阻断首问；普通 AI 文本、小程序、员工消息和更新客户消息仍会使旧运行失效。
- 当 ReplyPlan 中全部任务都是彼此独立的酒店知识文本任务、Judge 已分别给出完整 `AnswerText` 与受支持事实、且没有缺失维度、工具、资源或人工路由时，才尝试按原顺序直接提交；每条答案仍须通过内部协议、JSON、肯否极性、事实边界和动作安全校验。任一任务不满足即整体回到原 Generate 链路。上下文追问、纠正、PMS、工具、资源、服务请求和部分证据任务继续走原 Generate 链路。
- 真实耗时定位：修复前样本 `conversation_id=2205/message_id=19240` 总耗时 `44703ms`，其中检索 `808ms`、Judge `12287ms`、Generate `30069ms`；Generate 第一次无客户可见输出后重试。新直出分支去掉该样本中重复的 Generate 阶段，不调整 JEV/Intent 或 Judge 模型。
- test-2 真实验证：快速连续发送“附近有什么玩的地方”“酒店停车免费吗”，旧运行在 `1095ms` 内取消且未发送兜底，新运行在 `17220ms` 内按原顺序发送两条答案，`Generate=skipped`；新会话先问“酒店有没有咖啡”再追问“在哪拿”，正确承接为咖啡位置，没有被欢迎消息或停车上下文带偏。
- 真实换房查询首次出现一次 `recept_order_by_phone` 短暂不可用，旧兜底错误回复“暂时没法准确回答”；同一问题重试后能查到订单、完整入住期间库存、房态和差价依据。只读 PMS GET 因此只对网络错误、响应读取错误或 5xx 自动重试一次，4xx 和业务错误不重试；换房规划同时接受预订单和接待单，失败兜底按当前订单/房态目标说明，不再误追问可由订单补齐的日期。
- PMS 生成约束补充完整入住期间口径：订单日期派生的库存不得缩写成“今天满房”；任一入住日无库存时只能说明完整入住期间不能满足。聚焦及全量相关回归通过：`go test -p=1 ./internal/pms ./internal/pkg/utils ./internal/ai/runtime/internal/impl/adapter ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`；`go test -p=1 ./internal/services ./internal/ai/runtime/internal/impl/factory ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`。
- 无 model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox、计费或 PMS 写入变化；`AGENT_DESK_PMS_ALLOW_WRITE=false` 保持不变。
- 验证通过：`go test -p=1 ./internal/pkg/utils ./internal/ai/runtime/internal/impl/adapter ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`；`go test -p=1 ./internal/services ./internal/ai/runtime/internal/impl/factory ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`；Linux amd64 构建通过；`git diff --check` 通过。
- 并行影响：`origin/codex/customer-audit` 在本轮全部运行文件上均有同文件修改，合并时必须人工保留双方变更，禁止整文件覆盖。建议本提交独立 review/cherry-pick；无需 rebase 数据模型或迁移。回滚仅切回 test-2 上一 release，不回滚数据库、消息或“薇薇/薇薇2”。

## 2026-09-23 PMS 连续追问订单定位复用

- 目标：修复换房连续对话中首轮已成功查到订单，后续“沐阳吧，差价多少”仍退回手机号查单，因上游接口超时而无法继续回答的问题。
- 根因：成功 PMS 运行会把规范化手机号和订单定位写入 `ReplyPlan.Task.Entities`，但会话定位恢复只读取 ReplyPlan 文本和 Intent Task 实体，遗漏了真实生产 Trace 中的 ReplyPlan 实体。
- 修复：恢复同 session 最近成功 PMS 运行时合并 `ReplyPlan.Task.Entities`；仍只接受已成功工具调用、已发送消息和同一会话的数据，不扩大跨会话或失败运行复用范围。
- 回归测试改为使用与生产一致的 ReplyPlan 实体形态，确认手机号和接待单/预订单定位均可恢复；无新模型阶段、判断门、数据库、Migration、DTO、enum、外部 API、WebSocket、Outbox、计费或 PMS 写入变化。
- 验证通过：`go test -p=1 ./internal/ai/runtime/executor -run TestRuntimePMSSessionLocatorRecoversOnlySuccessfulSameSessionRuns -count=1`；`go test -p=1 ./internal/pms ./internal/pkg/utils ./internal/ai/runtime/internal/impl/adapter ./internal/services ./internal/ai/runtime/internal/impl/factory ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`。
- 并行影响：本次 `git fetch origin` 后，`customer-audit` 和 `ai-billing` 相对共同基线均未新增目标文件修改；本提交可独立 review/cherry-pick。部署仅更新 test-2，并保持 `AGENT_DESK_PMS_ALLOW_WRITE=false`；回滚只切回上一 release，不回滚数据库、消息或“薇薇/薇薇2”。

## 2026-09-23 PMS 当前选择与自然兜底收口

- 真实复测确认订单定位已复用：后续追问直接调用 `recept_order_detail`，没有再次按手机号查单；同时发现客户已选择“沐阳”，目标房型仍被历史“大床房”覆盖，且 Generate 空输出后发送了要求客户重发的机械兜底。
- 当前轮明确选择现在优先于历史目标：`selection` / `confirm_selection_and_continue_goal` 只从当前客户原话提取本次房型，再用真实库存匹配房型 ID；历史文本仍用于订单、日期和目标上下文，不再覆盖客户最新选择。
- 当前房型选择不再依赖 JEV 必须输出特定 `dialogueAct`：像“沐阳吧，差价多少”“沐阳差价多少”直接从当前客户原话提取候选，再交给真实库存唯一匹配；“差价多少”“有房吗”“那个吧”等泛指问法不会被当成房型。
- PMS 正常仍向 Generate 提供类型化事实；同时从同一结构化查询结果保留一份客户侧安全回复，仅在模型无客户可见输出时使用。该兜底继续经过内部字段和协议泄漏清洗，但不再重复执行本地自然语言语义推断，避免把已确认的房型、入住区间和差价再次误杀。
- 单个 PMS Task 在查询完成、无缺失项且已形成客户侧安全回复时直接提交，跳过无增益的 Generate；混合问题、缺失字段、仍需工具/资源/人工路由的任务继续走原链路。真实查询事实、只读边界、Commit、Outbox 和动作安全账本不变。
- 相关回归覆盖当前选择覆盖历史目标、订单定位复用、安全 PMS 自然兜底及知识/人工路由既有链路。验证通过：`go test -p=1 ./internal/pms ./internal/pkg/utils ./internal/ai/runtime/internal/impl/adapter ./internal/services ./internal/ai/runtime/internal/impl/factory ./internal/ai/runtime/executor ./internal/ai/runtime -count=1`。
- 无 JEV、model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox、计费或 PMS 写入变化；test-2 保持 `AGENT_DESK_PMS_ALLOW_WRITE=false`。
- 最终 test-2 release：`/opt/agentdesk/releases/20260923-pms-current-selection-802b629`，切换前备份：`/opt/agentdesk/backups/20260923-pre-pms-current-selection-802b629`，Server SHA-256：`1e2438cc09c4d1569824a472d57d4582a5a9d4144912898e2d2e7ca1fab6fc6d`。服务 active、`NRestarts=0`、8083 HTTP 200。
- 真实连续会话 `conversation_id=2208`、`message_id=19282` 验证：“沐阳吧，差价多少”在 Intent 标为 `follow_up` 时仍复用既有订单定位，4 个 PMS 查询均成功，回复当前房型、沐阳完整入住期间库存、候选房号及差价待核对边界；耗时 2383ms，`Generate=skipped`。未重复询问手机号，未暴露内部 ID/PMS 字段，未转人工，未宣称已经换房。

## 2026-09-24 当前目标上下文与回复速度收口

- 目标：修复真实会话中短句续问丢失对象、纠正后旧值继续参与、房间推荐和具体房号选择反复回到起点，以及单题知识 Judge 输出预算过大造成的等待。
- JEV 继续作为意图识别入口，不新增模型阶段或本地语义判断门。当前明确问题优先；只有“放在哪里、多少钱、到几号、哪个好、那就这个”等省略表达才使用同 session 最近唯一业务目标。消息明确点名新主题时不继承旧目标；纠正或修改会替换旧补充值，但保留已确认的订单定位等结构化基础事实。
- 活跃目标文本只保留最多三条基础信息和最近两条有效补充，避免整段历史不断累积。个人表达“我的房到几号/住到几号”按订单离店日期处理，不再误识别为房号询问。
- 换房只读流程在客户要求推荐时给出一个真实候选；客户选择具体候选房号后继续核对可用性和差价，并明确当前只完成查询、尚未实际换房。没有开放 PMS 写入。
- Judge 继续负责知识语义裁决，没有增加本地语义旁路。test-2 曾隔离对比单题 `1024` 输出预算及现有轻量模型；输出预算未改善 Luna 的实际延迟，DeepSeek、Qwen 和豆包候选均未稳定产出当前 Judge 协议，因此代码恢复原输出预算、线上恢复 `gpt-5.6-luna`，不保留无收益的配置改动。
- 无 model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox、计费或 PMS 写入变更。`customer-audit` 只与本交接文档存在同文件追加，合并时保留双方记录；`ai-billing` 无同文件修改。
- 自动验证通过：`go test -p=1 ./internal/ai/runtime/executor -count=1`、`go test -p=1 ./internal/ai/runtime/internal/impl/callbacks ./internal/ai/runtime -count=1`、`go test -p=1 ./internal/services ./internal/pms ./internal/pkg/utils -count=1`，以及 Linux amd64 构建和 `git diff --check`。
- 最终 test-2 release：`/opt/agentdesk/releases/20260924-active-goal-71b8f12`；切换前备份：`/opt/agentdesk/backups/20260924-pre-active-goal-71b8f12`。服务 `active/running`、`NRestarts=0`、8083 健康检查通过，部署后无 error 级 systemd 日志；PMS 保持 `enabled=true`、`allowWrite=false`，Judge 已确认恢复 `gpt-5.6-luna`。
- 真实隔离会话验证：订单连续追问 `conversation_id=2218` 3/3；换房与具体房号 `2220` 3/3；手机号纠正及纠正后退房日期 `2221` 的产品行为 3/3（首轮回复“没能查到”，旧测试断言未覆盖该同义表达）；知识续问和新主题切换 `2226` 3/3；让客服推荐房间并选择推荐房号 `2227` 3/3。PMS 场景墙钟约 2.2-3.3 秒，没有重复索要手机号、没有暴露内部字段、没有宣称已换房。
- 已知剩余问题：知识检索约 0.9 秒，但 Luna Judge 单题仍约 11-16 秒，客户墙钟约 12-20 秒。缩小输出预算没有改善；DeepSeek v4 flash/pro、Qwen 3.7 plus、豆包 2.0 mini 的隔离对照均未稳定产出当前 Judge 协议，已经全部撤回，不能将速度标为已解决。后续需要优化 Judge 协议输入或提供兼容的低延迟 Judge 模型，不能用本地语义旁路牺牲准确性。

## 2026-09-24 换房代选、汇总行与价格回复收口

- 目标：修复其风连续换房对话中“随便选”被说成客服主观推荐、客户已选房号仍被归功于客服、房型解释问题重复库存、库存汇总行“合计”被当成房型，以及价格字段缺失时暴露“没能整理成可靠回复”等内部故障话术。
- 当前客户说“随便选/帮我挑一间”时，回复明确为“先替您选”；客户直接指定具体房号时只确认该候选可用，不再声称是客服推荐。两者均保持只读边界，不宣称已经换房、锁房或排房。
- `inventory` 中 `rowType=TOTAL/SUMMARY/SUBTOTAL/AGGREGATE` 及名称为“合计/总计/小计/汇总”的行统一从房型解析、目标匹配、库存覆盖和客户选项中排除。
- 房型解释问题优先回答“这些是房型名称”，无真实房型配置字段时按床型、空间、楼层引导客户选择，不重复倾倒库存数据。
- 真实接口核对结果：当前订单只返回订单金额 `376`，沐阳库存返回可售房与房型 ID，但 `price/currency/consumeAmountType` 及逐日价格为空；因此不能准确计算差价，也不能用订单总额按天拆算。客户回复改为说明已知订单金额、目标房价未显示并明确不乱报；接口返回精确差价时仍直接回复补/退金额。
- 最后兜底不再要求客户重发，也不暴露“没能整理/可靠回复”等内部生成状态。无 model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox、计费或 PMS 写入变化；`AGENT_DESK_PMS_ALLOW_WRITE=false` 保持不变。
- 验证通过：`go test -p=1 ./internal/ai/runtime/executor -count=1`、`go test -p=1 ./internal/pms ./internal/services ./internal/ai/runtime -count=1` 及 `git diff --check`。`customer-audit` 仅与本交接文档存在同文件追加，合并时保留双方记录；`ai-billing` 无同文件修改。
- 修复提交：`2632a24`，已推送 `origin` 与 `weibao`。test-2 release：`/opt/agentdesk/releases/20260924-room-choice-2632a24`；回滚点：`/opt/agentdesk/releases/20260924-active-goal-71b8f12`。部署后 `HTTP 200`、`active/running`、`NRestarts=0`，无 error 级日志；Server SHA-256：`9b5c5e7456683981886c6aa18842c4e437dac32628e48d0f26c0978b4d3d1`。

## 2026-09-24 真实客户旅程端到端验收

- 目标不是按内部功能清单自测，而是从住客视角验证五条连续旅程：换房并让客服代选后追问差价、个人订单补手机号后连续追问退房、知识回指后切换新主题、外卖知识与代下单边界、枕头同款购买与商品资源提交。评测场景位于 `cmd/reply-runtime-eval/main.go`，共 15 个客户轮次。
- 本轮系列提交为 `a53e317`、`4a56347`、`42c20a0`、`570d05d`。运行文件主要涉及 Runtime 上下文恢复、PMS 只读客户侧回答和资源提交；最后一笔 `570d05d` 只补充快速连续消息下从客户历史恢复已选择房型，并把缺少目标价格时的兜底从内部术语“目标房型”改为“您刚选的房型”。
- 最终真实运行 `rrt-20260924-154156-20f09776` 在 test-2 当前 release 上通过 `15/15`，事实槽位 `20/20`，错误 `0`，平均延迟 `4849ms`、P90 `14347ms`、最大 `18869ms`。换房链路能持续保留“沐阳”，回答订单金额 `376.00元`，并在目标价格缺失时明确不乱报；订单、知识回指、主题切换、外卖能力边界和枕头资源提交均通过。
- 自动验证通过：`go test -p=1 ./internal/ai/runtime/executor ./internal/ai/runtime ./internal/services ./cmd/reply-runtime-eval -count=1` 及 `git diff --check`。Linux amd64 Server 与评测器构建并校验 SHA-256 后原子部署。
- 当前 test-2 release：`/opt/agentdesk/releases/20260924-product-experience-570d05d`；回滚 release：`/opt/agentdesk/releases/20260924-product-experience-42c20a0`；部署后服务 `active/running`、`NRestarts=0`、8083 HTTP 健康检查通过。提交已推送 `origin` 与 `weibao`。
- 本轮没有 model、Migration、DTO、enum、外部 API、WebSocket、数据库、企微协议、Outbox、计费或 PMS 写入变更；PMS 继续 `enabled=true`、`allowWrite=false`。`customer-audit`、`ai-billing` 与本轮四个运行/测试文件均无同文件交集，不需要调整合并顺序。
- 已知产品风险：咖啡知识首问和“在哪拿”本轮分别约 `14.35s`、`18.87s`，主要长尾仍在知识 Judge；正确性已通过但速度不能标为已解决。评测器使用真实模型、FastGPT、MySQL 和 PMS，但 `ChannelID=0`，只验证 Commit/资源创建，不等同于真实企微客户端已收到并打开商品卡。评测健康项中本机 `127.0.0.1:6333/readyz` 不可用，但本轮 FastGPT 检索请求正常完成。
