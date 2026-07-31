# 服务器部署交接（2026-07-28）

## 部署范围

- 部署分支：`codex/tenant-ai-unified-integration`
- 部署形态：Docker Compose，项目名 `agentdesk`
- 应用端口：`8083`
- 数据库：MySQL 8.4，仅 Compose 内网开放
- 发布目录：`/opt/agentdesk/releases/20260728-094934`
- 当前版本链接：`/opt/agentdesk/current`
- 生产环境变量：`/opt/agentdesk/shared/production.env`，权限 `0600`

本次部署使用当前统一集成工作树的完整源码状态。该状态包含尚未提交的到店链接及统一集成变更，因此不能仅凭 Git commit 还原；服务器发布目录中的最终源码归档及校验文件是本次部署的精确基线。

## 管理员初始化

超级管理员用户名通过以下环境变量初始化：

```text
AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME
AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD
```

生产用户名已设置为 `skyincloud`。密码仅保存在服务器生产环境文件中，不进入代码、提交、镜像日志或本文档。

初始化后验收结果：

- 用户表只有一个初始超级管理员
- 角色为 `super_admin`
- 平台账号标记正确
- `/api/auth/login` 实际登录返回 HTTP 200

## 数据与迁移

已成功执行迁移版本：

```text
2, 15, 35, 68, 69, 70, 71
```

初始数据库备份位于：

```text
/opt/agentdesk/backups/20260728-094934/agentdesk-initial.sql.gz
```

备份权限为 `0600`。已将备份恢复到临时数据库独立验证，确认 114 张表、全部迁移版本和超级管理员数据可恢复，随后删除临时数据库。

## 运行状态

- `agentdesk-agent-desk-1`：healthy，重启策略 `unless-stopped`
- `agentdesk-mysql-1`：healthy，重启策略 `unless-stopped`
- 应用镜像：`mlogclub/agent-desk:latest`
- 应用容器内健康检查、页面访问和管理员登录均通过

FastGPT、NewAPI、企业微信、OIDC、邮件和到店链接的生产外部连接仍关闭。当前服务器没有这些能力所需的生产 HTTPS 域名和凭据，不能用历史测试密钥冒充生产配置。

## 构建说明

服务器不能稳定访问默认 Go 模块源。Docker 构建已支持：

```text
AGENT_DESK_BUILD_GOPROXY
```

默认值为 `https://goproxy.cn,direct`，只影响镜像构建，不进入应用运行时业务配置。

## 外网入口

服务器本机访问 `http://127.0.0.1:8083/dashboard/login/` 正常，Docker 已监听 `0.0.0.0:8083`，主机防火墙也允许该端口。

公网访问仍需在腾讯云安全组中放行入站 `TCP 8083`。未擅自修改服务器现有 Nginx 的 `80/443` 配置，避免覆盖同机其他业务。正式生产还应配置独立域名、HTTPS 证书，再由 Nginx 反向代理到 `127.0.0.1:8083`。

## 运维命令

查看状态：

```bash
cd /opt/agentdesk/current
docker compose --project-name agentdesk \
  --env-file /opt/agentdesk/shared/production.env ps
```

查看应用日志：

```bash
cd /opt/agentdesk/current
docker compose --project-name agentdesk \
  --env-file /opt/agentdesk/shared/production.env logs --tail=200 agent-desk
```

停止本次部署：

```bash
cd /opt/agentdesk/current
docker compose --project-name agentdesk \
  --env-file /opt/agentdesk/shared/production.env down
```

停止不会删除 MySQL 或应用数据卷。禁止附加 `-v`，除非已经完成备份并明确批准删除数据。

本机此前没有 AgentDesk 生产版本，因此不存在可直接切回的旧应用 release。需要重新部署本次精确版本时，使用发布目录中的最终源码归档、生产环境文件和保留的数据卷重新执行 Compose。

## 企业微信服务商指令回调修复

2026-07-28 新增发布：

```text
/opt/agentdesk/releases/20260728-153602-wecom-callback/app
```

当前链接已切换至该发布，运行镜像：

```text
sha256:5e7ce05c03530d094b80e0720cc07f7061e0c1cc7fb8159a1db1653a1dc27602
```

修复边界：

- 指令回调 URL 的 GET 校验继续执行时间窗、签名和 AES 解密，但解密后的 `receiveId` 只要求非空；项目当前没有独立的服务商企业 CorpID 配置，未新增或硬编码 CorpID。
- 指令 POST 回调仍严格要求 `receiveId == SuiteID`。
- 数据回调规则未改变。
- 回调失败日志只记录 `kind`、`method`、`stage` 和 `requestId`，不记录凭据、身份值、密文或解密原文。

部署前验证：

```text
go test ./internal/services/... ./internal/handlers/third/... 通过
go test ./internal/bootstrap/... 通过
go test ./... 通过
```

部署后验证：

- 容器健康，重启次数为 0，公网后台页面返回 HTTP 200。
- 使用生产回调配置生成的本机签名 GET 探针命中指令回调，返回 HTTP 200 和原始解密明文；探针临时文件已删除，凭据未输出。
- 2026-07-28 15:43:48 收到企业微信真实 `suite_ticket` POST，接口返回 HTTP 200，数据库状态为 `processed`。
- 尚未观察到企业微信后台“申请校验”触发的官方指令 GET。必须由管理员在企业微信后台点击“申请校验”后，以线上 GET 200 日志作为最终官方验收证据。

部署前镜像已保留为：

```text
mlogclub/agent-desk:rollback-20260728-100941
sha256:312a29ba065876ca98d25e9f75dc1213d84283a6091ddc9166a6261b99deca75
```

旧 release 的源码目录不再作为该次回滚的权威来源；首次创建新 release 时曾复制到符号链接目标，旧目录中的回调文件随后被覆盖。需要回滚时应使用上述已固定标签的部署前镜像，不得从旧源码重新构建。回滚不涉及数据库结构或数据恢复。

## 企微员工号短回调部署

2026-07-28 当前发布：

```text
/opt/agentdesk/releases/20260728-181618-wxwork-short-callback/app
```

运行镜像配置 ID：

```text
sha256:87cbf365ddd29db859fe02e9a615e0a7dea894c9159afdd781da288a90f53eed
```

本次根因不是实例被占用或登录冲突。真实 `/user/get_profile` 和业务绑定已经成功，Provider
在保存原 112 字节回调地址时返回：

```text
Error 1406 (22001): Data too long for column 'callback_url'
```

修复保留 43 字符高熵 Channel token，通过同一 Handler 增加短别名：

```text
/api/third/wxp?t=<token>
```

生产最终回调地址长度为 87 字节。原
`/api/third/wxwork-protocol/callback?token=...` 继续兼容；Nginx 对两个精确 location 均
关闭 access log，避免 query token 落盘。实现仍严格遵循 `wework.apifox.cn`：
`set_notify_url` 只发送 `guid` 与 `notify_url`，回调仍读取 `guid`、`notify_type` 和
`data.seq`，没有增加私有协议字段。

切换前备份：

```text
/opt/agentdesk/backups/20260728-181618-wxwork-short-callback
```

数据库 dump 已恢复到独立临时库验证，结果为 114 张表、7 条 Migration；临时库随后删除。
回滚镜像：

```text
mlogclub/agent-desk:rollback-20260728-181618-wxwork-short-callback
```

部署后已确认：

- 应用和 MySQL 均为 healthy，应用重启数为 0。
- 真实 `client/set_notify_url` 返回成功。
- 实例池记录 `2` 仍绑定业务实例 `1`，状态从 `callback_error` 恢复为 `bound`。
- 业务实例保持 Tenant `2`、Store `1`、`online`，没有登出、重新扫码、抢占或重建。
- 实例池同步返回 2 条记录、1 条已绑定。
- 新旧回调路径的非法 POST 均返回 `400`，不返回假成功。

当前尚未在新通知地址上观察到一条新的真实客户私聊。因此这里只确认“真实资料接口、实例
绑定和通知地址已接通”，不宣称双向消息已经验收。最终复验必须由真实客户向该员工号发送
一条私聊，再确认客户入站、AgentDesk 出站、企微原生员工回复回流和重复回调幂等。没有
序号缺口时禁止调用已废弃且受限频的 `sync_msg`。

## 企业微信授权类型请求契约修复

2026-07-29 初始修复发布（已由下节安装测试发布取代）：

```text
/opt/agentdesk/releases/20260729-144925-wecom-auth-type/app
```

运行镜像配置 ID：

```text
sha256:04df2ede2dfe3548142c6fe1c035adbe62a96ec0cfee5111c3eaa6808811839c
```

修复前，`set_session_info.session_info.auth_type` 被序列化为 `[]int{0}`。该初始版本改为
强类型请求 DTO，`auth_type` 只允许 JSON 整数 `0` 或 `1`；但生产预检仍固定要求 `0`，
无法支持企业微信“安装测试”阶段。下节发布已将生产预检修正为接受合法阶段值 `0/1`。
授权 `state` 在任何上游调用前校验为 1 至 128 位 ASCII 字母或数字，系统生成值使用
32 字节 CSPRNG 后 hex 编码为 64 位合规字符串。

请求体契约测试分别覆盖 `auth_type=1` 和 `auth_type=0`，使用 `json.Decoder.UseNumber`
确认值为 JSON number，并锁定请求体只有 `pre_auth_code` 与单字段 `session_info`。测试还
覆盖非法 state、非法授权类型均在发出上游请求前失败，以及随机 state 的长度、字符集和
去重。发布前 `go test ./... -count=1`、`go vet ./...`、`git diff --check` 均通过。

切换前备份位于：

```text
/opt/agentdesk/backups/20260729-144925-wecom-auth-type
```

备份包含部署前生产环境、当前 release 源码、当前镜像 ID 和 MySQL dump，权限保持受限。
MySQL dump 已恢复到独立临时库，确认 114 张表和 7 条 Migration 后删除临时库。回滚镜像：

```text
mlogclub/agent-desk:rollback-20260729-144925-wecom-auth-type
```

部署后已确认：

- 应用和 MySQL 均为 healthy，重启次数均为 0。
- 容器内及生产环境文件中 `AGENT_DESK_WECOM_AUTH_TYPE` 唯一且为 `0`。
- 当前 release、上传源码归档和镜像文件系统均未发现旧 `"auth_type": []int{0}`。
- `https://weibao.omnireva.com/dashboard/login/` 与公网 `8083` 登录页均返回 HTTP 200。

本次没有修改 model、Migration、DTO、enum、权限、页面、员工号协议、AI、FastGPT、
NewAPI、Billing 或派单。上述证据确认请求契约和部署状态，不等同于企业微信后台完成一次
新的正式应用安装授权；真实授权闭环仍应由管理员发起安装，并以企业微信返回成功和本地
授权记录为最终业务验收。

## 企业微信安装测试授权链路复验

2026-07-29 安装测试发布：

```text
/opt/agentdesk/releases/20260729-161037-wecom-install-test/app
```

运行镜像配置 ID：

```text
sha256:59c9a205a8ea55cb63c4bf0acb1f27fe7878cabccc4a05c5bae48fc81dfd6a94
```

本次修正生产预检的阶段语义：启用 Arrival 时，`AGENT_DESK_WECOM_AUTH_TYPE` 只允许
整数 `0` 或 `1`，但不再把生产环境等同于正式发布阶段。企业微信后台处于“安装测试”
时配置 `1`，应用正式发布后配置 `0`；阶段切换必须修改仓库外生产环境文件并强制重建
应用容器，普通 restart 不会重新加载环境变量。

授权 URL 回归测试同时锁定：

- host 必须为 `open.work.weixin.qq.com`；
- path 必须为 `/3rdapp/install`；
- `redirect_uri` 只编码一次，解码后必须精确等于
  `https://weibao.omnireva.com/api/wecom/provider/authorization/callback`；
- `state` 只允许 1 至 128 位 ASCII 字母或数字，系统生成值为 64 位十六进制字符串；
- `set_session_info` 请求体仅包含 `pre_auth_code` 和单字段 `session_info`，
  `auth_type=1/0` 均为 JSON number，不接受数组、字符串或布尔值。

切换前备份：

```text
/opt/agentdesk/backups/20260729-161037-wecom-install-test
```

数据库备份已在独立临时库恢复验证，结果为 114 张表、7 条 Migration；临时库随后删除。
回滚镜像：

```text
mlogclub/agent-desk:rollback-20260729-161037-wecom-install-test
```

部署使用 `docker compose ... up -d --force-recreate agent-desk` 重新创建容器，不是普通
restart。部署后证据：

- 容器于 `2026-07-29 16:16:49`（Asia/Shanghai）启动，状态 `healthy`，重启次数 `0`；
- 容器内和 `/opt/agentdesk/shared/production.env` 中
  `AGENT_DESK_WECOM_AUTH_TYPE` 唯一且实际为 `1`；
- `2026-07-29 16:24:54` 收到重建后的新 `suite_ticket`，处理状态为 `processed`；
- 指令与数据回调路由继续可达；无合法签名参数的探测请求按契约返回 `400`，未返回假成功；
- 授权完成回调路由可达并按未携带授权参数的探测请求返回重定向；
- 从“丽斯未来 / 合肥南七”的到店联动页面创建了全新一次性授权邀请，没有复用旧邀请、
  旧预授权码或旧 state；
- 新邀请成功跳转到真实企业微信官方
  `https://open.work.weixin.qq.com/3rdapp/install`，页面标题为“企业微信”，未停留在
  AgentDesk 的 `redirect_uri` 不一致错误页。

官方安装页是受保护的企业管理员操作面，Agent 不读取其内部内容，也不代替管理员点击最终
确认。只有管理员本人确认安装、授权完成回调成功、Corp 授权记录落库且连接校验通过后，
才能把本次业务授权记为完成。当前安装测试结束并正式发布应用后，必须将
`AGENT_DESK_WECOM_AUTH_TYPE` 改回 `0`，再次使用 `--force-recreate` 重建并复验。

### 已暴露企业微信凭证的轮换

此前截图暴露过 SuiteSecret、回调 Token 和 EncodingAESKey。完成本次管理员安装确认后，
必须在同一维护窗口轮换三项凭证：

1. 暂停创建新授权邀请，记录当前容器、环境文件和企业微信后台配置的非秘密指纹与时间，
   加密备份当前仓库外生产环境文件，并完成独立恢复可读性检查。
2. 由企业微信管理员在官方后台生成新 SuiteSecret，并设置一组新的回调 Token 与
   EncodingAESKey；新值只进入受控秘密通道，不进入聊天、工单、截图、命令历史或日志。
3. 在服务器生成 `/opt/agentdesk/shared/production.env` 的候选文件，权限设为 `0600`，
   只替换对应三项秘密；校验变量唯一、格式合法后使用原子 rename 替换正式文件。
4. 企业微信后台与服务器变更作为一个维护事务提交。当前运行时不支持新旧双凭据并行，
   因此维护窗口内允许短暂拒绝回调，但不允许一侧失败后继续运行；任一步失败必须将后台
   和服务器一起回滚到旧的一致组合。
5. 使用 `docker compose --env-file /opt/agentdesk/shared/production.env up -d
   --force-recreate agent-desk` 重新创建容器，禁止普通 restart。
6. 依次复验数据 GET、指令 GET、真实新 `suite_ticket`、全新安装测试邀请和授权完成回调；
   日志只记录阶段、HTTP 状态、requestId 和脱敏指纹。
7. 全部验收后立即废止旧凭证、删除候选临时文件，登记轮换时间、操作者、审批人、配置
   指纹和验收结果。任何失败都不得通过数据库写入伪造授权成功。

本次只修改企业微信服务商授权请求、阶段配置、测试和交接文档；没有修改微信小程序、
AI 回复引擎、企微员工号协议、页面业务模型、权限、计费或派单。

## 到店联动成员跨命名空间绑定部署与真实验收

2026-07-29 最终发布：

```text
/opt/agentdesk/releases/20260729-195400-arrival-cross-namespace-binding-resume/app
```

运行镜像配置 ID：

```text
sha256:7dc951d1e6a27523124783618f321742ebaf60ececdc2850a6f05e41df96d035
```

本次根因是成员绑定曾把企业微信官方客户联系成员 UserID 与员工号协议登录资料中的
`EmployeeUserID` 视为同一命名空间并强制字符串相等。修复后由管理员分别选择两侧对象并
人工确认映射；连接记录独立保存官方成员密文、nonce、指纹和员工实例 ID，不覆盖员工实例
资料，也不按 ID 或姓名自动推断同一成员。审计只记录
`mappingMode=operator_confirmed_cross_namespace` 等非敏感结果。

部署前自动化验证全部通过：

```text
go test ./internal/services -count=1
go test ./... -count=1
go vet ./...
pnpm typecheck
node --test（前端 160 项）
pnpm build
git diff --check
```

切换前备份：

```text
/opt/agentdesk/backups/20260729-192300-arrival-cross-namespace-binding
```

回滚镜像：

```text
mlogclub/agent-desk:rollback-20260729-192300-arrival-cross-namespace-binding
```

应用容器于 `2026-07-29 19:57:53`（Asia/Shanghai）启动，当前为 `healthy`，重启次数
为 `0`，授权阶段仍为安装测试 `AGENT_DESK_WECOM_AUTH_TYPE=1`。公网设置页返回 HTTP
200；`2026-07-29 20:05:01` 新的 `suite_ticket` 已处理成功。

用户明确批准创建一次性替代邀请。生产审计最终显示该恢复操作被重复执行了两次，分别于
`21:00:40` 和 `21:03:43` 创建两条复用现有 active 授权的替代邀请，并分别完成同一人工
映射；这与“只创建一次”的操作预期不一致，本文按真实审计记录保留。两次都没有重新安装、
卸载应用或打开新的官方授权页。最终结果：

- 门店“合肥南七”的连接状态由 `pending_binding` 变为 `active`；
- 企业微信授权主体保持 `active`；
- 官方成员密文、nonce、指纹与员工实例 ID 分别存在；
- 员工实例 `EmployeeUserID` 的绑定前后 SHA-256 一致，未被修改或回填；
- 两条替代邀请均已使用并停用，对应 authorization attempt 均已完成并停用；当前有效邀请
  数量为 `0`，不存在可再次复用的邀请；
- 最新审计只包含实例在线状态和
  `mappingMode=operator_confirmed_cross_namespace`，没有原始成员标识。

绑定完成后的两小时容器日志复检结果为：`panic/fatal` 命中 `0`；成员 UserID、
`EmployeeUserID`、guid、`conversation_id`、永久授权码、access token、预授权码、
授权 state、密文/nonce/指纹及服务商秘密相关字段命中 `0`。检查只输出计数，没有输出
任何原始身份或凭据值。

当前管理后台已经刷新并显示“合肥南七 / 已连接 / 黄奇峰 online”，最近校验时间为
`2026-07-29 21:04:12`，无需再次刷新页面。
本次没有修改微信小程序、AI 回复引擎、企微员工号协议字段、权限、计费或派单。代码回滚
不要求回滚已保存的映射数据，但会恢复错误的跨命名空间字符串相等限制，因此只应在明确
接受成员绑定再次失败时使用。

## 企业微信“联系我”失败诊断与受控恢复

2026-07-29 从统一项目提交
`ff1d3735a609ca83e09cf6c3f84d6a939643b84e` 建立独立修复工作树，并重新核对企业微信
官方“联系我管理”、服务商获取企业凭证、联系我小程序插件、企业授权信息及全局错误码
文档。请求仍严格使用 `externalcontact/add_contact_way` 的 `type=1`、`scene=2`、单个
真实客户联系成员 UserID 和不超过 30 字节的不透明 `state`，没有混入小程序插件参数。

本次修复内容：

- HTTP 200 只有在 JSON 合法且 `errcode=0` 时才成功；失败结构保存调用阶段、HTTP 状态、
  官方错误码、清洗后的短消息和是否可重试；
- corp access token 缓存和锁按授权主体隔离；`40014/42001` 只条件清除本次实际使用的
  旧 token 版本，最多刷新重试一次；
- 同一扫码事件通过唯一记录和数据库原子 claim 防止并发重复创建，最多三次受控尝试；
- 已有官方 `config_id` 时只重试二维码下载、验码和发布，不再次调用
  `add_contact_way`；
- 5 分钟维护任务只接管可重试失败、超时 provisioning，以及一次尚无诊断的历史通用失败；
- `GET /status` 保持只读，小程序 V2 契约没有增加诊断字段或伪造成功；
- 官方 hint 编号、来源 IP、诊断 URL、身份字段、凭据字段和长不透明值在写库与日志前
  删除；`48002` 固定落为 `contact_way_permission_denied`。

自动化验证全部通过：

```text
go test ./internal/services -count=1
go test ./internal/bootstrap/... -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

本机没有 `TEST_MYSQL_DSN`，因此可选 MySQL 单测未执行；SQLite AutoMigrate 测试通过，
生产 MySQL 启动后确认 9 个新增诊断列全部存在。

部署前备份：

```text
/opt/agentdesk/backups/contact-way-20260729-231049
```

备份包含权限受限的生产环境文件、上一 release、上一镜像 ID、MySQL dump 和校验文件。
MySQL dump 已恢复到独立临时库，源库与恢复库均为 114 张表、7 条 Migration；临时库随后
删除。最终源码 release：

```text
/opt/agentdesk/releases/20260729-235842-contact-way-final/app
```

最终运行镜像：

```text
sha256:c1be7f35b2ef0cba7117f5ca153f74468636d726ee329fe0f980de6db4c05b7e
```

后端二进制 SHA-256：

```text
8ad05b9b2d8a049e1c1a2835bfdd5dabe2397887cb79e6955c6729c83c7d39a5
```

服务器只有约 3.6 GiB 内存且无 Swap，完整 Docker 构建的 Next.js 阶段持续占满内存并将
负载推高，15 分钟后主动停止。由于本次没有任何前端改动，改为在本地从同一最终源码交叉
编译 Linux amd64 静态后端，并覆盖到已备份生产镜像；原 `web/out`、运行配置和审计工具
均未变化。该方式避免在低内存生产机继续执行无关前端构建，二进制哈希已在上传前后和
容器内三次核对一致。

首次重建时 Compose 因当前目录是符号链接而临时推断项目名为 `current`，创建了一套未
接管流量的空容器和空卷，并因 `8083` 已被正式容器占用而失败。正式
`agentdesk-agent-desk-1` 与生产 MySQL 始终健康；临时 `current-*` 容器、网络和空卷已
全部删除。最终命令显式使用 `-p agentdesk --no-deps`，只重建应用容器：

```text
docker compose -p agentdesk --env-file /opt/agentdesk/shared/production.env \
  up -d --force-recreate --no-deps agent-desk
```

部署结果：

- 应用于 `2026-07-29 23:52:41 CST` 启动，镜像和二进制哈希均为上述值；
- 应用 healthy、重启次数 0；生产 MySQL 保持原容器、原数据卷和 healthy；
- 容器内 `AGENT_DESK_WECOM_AUTH_TYPE=1`，仍符合当前“安装测试”阶段；
- 公网登录页返回 200；无合法签名参数的指令回调返回 400，没有假成功；
- 重建后的新 `suite_ticket` 于 `2026-07-29 23:55:22 CST` 正常落库；
- 启动后日志中 panic/fatal 数量为 0；
- 9 个诊断列均已由 AutoMigrate 创建。

真实企微复验发生于 `2026-07-29 23:38:09` 至 `23:38:10 CST`。维护任务接管 4 条历史
`contact_way_api_failed + attempt_count=0` 记录；授权主体、客户联系成员上下文、suite
token、corp token 和真实 HTTP 请求均已通过，4 次 `add_contact_way` 均返回：

```text
HTTP 200
errcode 48002
errmsg api forbidden
retryable false
```

数据库现已把这 4 条记录统一收敛为
`contact_way_permission_denied / add_contact_way / 48002 / api forbidden`，尝试次数
为 2、待重试数 0；所有记录的 `config_id` 仍为空，因此没有重复创建官方联系码。历史
诊断消息中的 hint、来源 IP 和 URL 已安全清洗，相关残留计数为 0。

该结果证明 AgentDesk 的授权读取、token 获取、真实调用、诊断落库和受控恢复链已经工作，
也证明当前剩余阻塞不是传输或重试代码，而是企业微信第三方应用权限。二维码成功仍需：

1. 企业微信管理员在服务商第三方应用中开放客户联系“配置联系我”所需能力。
2. 如果企业微信要求新增权限重新授权，使用全新授权流程完成，不直接改库。
3. 发起一次全新真实扫码，确认 `add_contact_way errcode=0`。
4. 确认保存真实 `config_id` 和加密二维码引用，bootstrap 返回
   `available=true/mode=qr_code`。
5. 重放同一 `scanEventId`，确认不产生第二个官方配置；确认 `GET /status` 不触发创建。

在完成上述官方权限操作和真实 `errcode=0` 前，不得宣称“联系我二维码已完全修复”。

脱敏补丁回滚镜像：

```text
mlogclub/agent-desk:rollback-contact-way-redaction-20260729-234933
```

整项功能回滚镜像：

```text
mlogclub/agent-desk:rollback-contact-way-20260729-231049
```

回滚只切换镜像并使用显式 `-p agentdesk --no-deps` 强制重建应用容器；新增列为兼容性诊断
字段，不执行 DDL 删除。回滚后不得把已经确认的 `48002` 改为可重试，也不得通过手工写库
伪造二维码成功。

## 企业微信获客助手到店链接引擎

2026-07-30 最终发布目录：

```text
/opt/agentdesk/releases/20260730-1218-customer-acquisition/app
```

最终运行镜像：

```text
sha256:bf31cfd7145fbcc61af733c4420c2d80c9607342f47cc546bb246a28e6d31a98
```

本次将到店二维码生产主链从 `externalcontact/add_contact_way` 切换为企业微信获客助手，
新增真实额度预检、单成员获客链接创建与复用、链接详情恢复、客户分页对账、加密 URL
保存、不透明 `customer_channel`、标准/艺术二维码反向解码校验以及回调与补偿任务共用的
客户关系确认事务。旧 `contact_way` 只保留兼容和显式回滚，不会因权限、额度或创建失败
自动降级。小程序公开契约、AI 回复引擎、员工号协议和客户身份模型均未修改。

部署前验证：

```text
go test ./...                                  通过
go vet ./...                                   通过
pnpm typecheck                                 通过
pnpm lint                                      0 error，33 条项目既有 warning
next build --webpack                           通过，48 个静态页面
git diff --check                               通过
MySQL 8.4 隔离库 AutoMigrate + 镜像健康检查   通过
```

MySQL 兼容验证使用生产同版本数据库的隔离临时库。最终镜像真实执行 AutoMigrate 后，
`t_arrival_acquisition_link` 的字段、类型和三列唯一索引均存在；验证完成后临时容器和
临时库已删除，没有接触生产业务数据。

切换前备份：

```text
/opt/agentdesk/backups/20260730-1221-customer-acquisition
```

备份包含权限为 `0600` 的生产环境文件和通过 gzip 完整性检查的 MySQL dump。dump
SHA-256：

```text
e450e66618ba9a950cea699577daecee850bafab10c1f29d3439c9d3308bea36
```

部署前镜像固定为：

```text
mlogclub/agent-desk:rollback-20260730-1218
sha256:c1be7f35b2ef0cba7117f5ca153f74468636d726ee329fe0f980de6db4c05b7e
```

服务器只有约 3.6 GiB 内存且没有常驻 Swap。首次过时源码构建在 Next.js 阶段长时间
阻塞后被停止，当前线上容器未受影响。最终构建临时启用 4 GiB swap，完整 Docker
多阶段构建在约 85 秒内完成；新镜像生成后关闭 swap 并删除临时文件，没有写入
`/etc/fstab`。

使用以下方式原地重建应用容器，MySQL 容器和数据卷未重建：

```text
docker compose --project-name agentdesk \
  --env-file /opt/agentdesk/shared/production.env \
  up -d --force-recreate agent-desk
```

部署结果：

- 应用容器于 `2026-07-30 12:28:14`（Asia/Shanghai）启动并进入 `healthy`；
- 容器实际镜像为最终摘要，实际
  `AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=customer_acquisition`；
- 公网 `/` 与 `/dashboard/` 返回 HTTP 200；
- 无签名参数的指令回调按契约返回 HTTP 400，没有假成功；
- 启动后日志未发现 panic、fatal 或 error；
- `2026-07-30 12:35:44` 收到新 `suite_ticket`，数据库状态为 `processed`；
- README 已替换为当前知悉微宝“客服运营总览”和“客服运营报表”页面截图，不再引用
  旧“贝壳AGENT”截图；截图不包含客户消息、凭据、租户内部 ID 或企微成员标识。

`2026-07-30 12:42:22` 从到店联动页面执行真实获客助手预检。授权、官方客户联系成员和
员工实例校验均通过，但 Provider 返回：

```text
acquisition_permission_denied
```

审计结果为 `providerOK=false`，额度字段保持 `0`，连接状态按真实结果标记为异常。该值
不是“真实额度为零”，而是当前企业授权尚未包含获客助手权限。系统没有创建获客链接、
没有生成二维码、没有自动降级到旧 Provider，也没有写库伪造成功。

剩余人工步骤：

1. 测试企业重新授权第三方应用，使服务商后台已保存的“获客助手权限”进入企业授权范围。
2. 在到店联动页再次执行“校验连接”，确认返回真实 `quotaTotal/quotaBalance`。
3. 发起一次新的小程序首次扫码，确认创建或复用真实单成员获客链接并返回可解码二维码。
4. 由客户主动扫码添加成员，确认回调或补偿对账形成精确门店关系。
5. 再次扫码，确认不再显示二维码，并由现有员工号会话真实投递到店卡片。

上述五步完成前，不得宣称企业微信获客助手全链路已经验收。

## 企微员工号异地登录环境修复

2026-07-31 按聚合智能官方登录流程修复 `1014`。原实现虽然在二维码前调用
`/client/restore_client`，但把 `proxy` 固定为空字符串；设备池认领还会提前调用
`/login/get_login_qrcode`，从而把“登录环境未启动”误判为实例不可用。

修复后的统一链路为：

```text
扫码设备启动聚合聊天本地代理
  -> AgentDesk 校验并提交代理
  -> POST /client/restore_client
  -> 有界等待运行环境
  -> POST /login/get_login_qrcode
  -> POST /login/check_login_qrcode
  -> 状态 10 时 POST /login/verify_login_qrcode
```

现场绑定、已有实例重新登录和公开远程绑定页均使用该链路。完整代理只在服务端保存和发送
给供应商，不回显到浏览器；普通实例编辑不会再意外清空代理。二维码和状态响应已移除供应商
原文与内部 key。

聚合智能 API 应用的全局事件回调按用户提供的供应商环境配置为：

```text
http://112.124.109.106:2332/api/third/wxwork-protocol/callback
```

该环境级地址与 AgentDesk 每实例 `set_notify_url`、扫码设备异地代理是三套不同概念。
本次没有把该 IP 硬编码进代码。部署前探测已确认地址可达，但只有供应商真实登录/消息事件
成功回传后，才能标记端到端验收完成。

部署前验证：

```text
go test ./...                                  通过
node --test web/**/*.test.mjs                  168 项通过
tsc --noEmit                                   通过
eslint .                                       0 error，33 条项目既有 warning
next build --webpack                           通过，48 个静态页面
git diff --check                               通过
```

本节的最终 release 目录、镜像摘要、容器启动时间、全局回调测试和真实扫码结果在生产部署后
补录；在此之前不得宣称线上 `1014` 已修复。
