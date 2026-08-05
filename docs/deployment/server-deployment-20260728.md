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

## 企微员工号登录二维码直接链路修复

2026-07-31 复核 `https://wework.apifox.cn/llms.txt` 及其登录接口页面后确认：获取登录
二维码不要求用户填写代理，也不要求预先调用 `/client/restore_client`。此前 commit
`39ff075` 引入的“异地登录代理 -> restore -> 重试二维码”链路属于错误判断，已废止。

修复后的统一链路为：

```text
POST /login/get_login_qrcode
  -> 前端展示真实二维码
  -> 每 3 秒 POST /login/check_login_qrcode
  -> 仅状态 QRCODE_REQUIRE_VERIFY(10) 展示确认码输入
  -> POST /login/verify_login_qrcode
  -> 官方成功后同步员工资料与在线状态
```

`get_login_qrcode` 请求只发送 `guid` 与布尔 `verify_login=false`；`check_login_qrcode`
只发送 `guid`；`verify_login_qrcode` 只发送 `guid` 与用户输入的 `code`。现场绑定、已有
实例重新登录和公开远程绑定页均不再展示、接收或发送代理，也不会在登录前 recover/restore。
二维码和状态响应继续移除供应商原文与内部 key，空二维码明确失败。

聚合智能 API 应用的全局事件回调按用户提供的供应商环境配置为：

```text
http://112.124.109.106:2332/api/third/wxwork-protocol/callback
```

该环境级地址、AgentDesk 每实例 `set_notify_url` 和登录二维码接口是三套不同概念。本次
没有把该 IP 硬编码进代码。部署前探测已确认地址可达，但只有供应商真实登录/消息事件成功
回传后，才能标记端到端验收完成。

当前直接链路部署前必须验证：

```text
gofmt -w <修改的 Go 文件>
go test ./internal/services ./internal/handlers/api ./internal/handlers/dashboard
go test ./...
node --test web/**/*.test.mjs
pnpm typecheck
pnpm lint
pnpm build
git diff --check
```

历史错误版本的审计记录保留如下，仅用于追溯和回滚，不得继续部署：

```text
commit:  39ff075
release: /opt/agentdesk/releases/20260731-0947-wxwork-login-proxy/app
image:   sha256:497adb38064faea0f3d87e7ab4d6cc994fdfb7b25b8e695463757b3e0e522878
start:   2026-07-31 09:52:00 Asia/Shanghai
```

最终部署证据：

```text
代码提交：3ecb6093fd0ce1e80c5bd1383cffd8f44678badb
源码归档 SHA-256：808867fa34e03d1ac08f7b395b24d8c389c67de34b4f0ccd80400ead797eca5a
发布目录：/opt/agentdesk/releases/20260731-1245-wxwork-direct-login-final/app
部署前备份：/opt/agentdesk/backups/20260731-1245-wxwork-direct-login-final
MySQL dump SHA-256：e2dddda9d4e9d6b53181143d2b4751e8afac8ed0f2bd4d0655eecc930fb45244
回滚镜像：mlogclub/agent-desk:rollback-20260731-1245-wxwork-direct-login-final
回滚镜像摘要：sha256:101388ae8790def0325d7277fd328a048cf1accdc9871ca912ec7928ee3a8025
最终镜像摘要：sha256:bdedcf14030b5b6b9e3f9b8f72a363a88d08ea8f4087ff74bc72f6332727de07
镜像创建时间：2026-07-31 12:46:31 +08:00
应用容器启动时间：2026-07-31 12:46:53 +08:00
```

部署前备份包含权限为 `0600` 的生产环境文件和通过 gzip 完整性检查的 MySQL dump。发布
使用 Compose project `agentdesk` 和 `--force-recreate --no-deps agent-desk`，只重建
应用容器；MySQL 容器启动时间仍为 `2026-07-28 10:09:43 +08:00`。最终应用与 MySQL
均为 `healthy`，本机 `8083` 和公网员工号页面均返回 HTTP 200，启动日志没有
panic、fatal、migration 或数据库连接错误。

生产复验没有直接修改数据库或伪造协议结果。通过现有“更换登录员工号”入口复用了旧流程
留下的替换草稿，真实调用供应商后确认：

```text
replacement draft reused: true
qrcode present: true
check status: pending
status code: 0
requires code: false
message: 等待扫码
```

取码成功后草稿状态由旧 `recovering` 收敛为 `login_qrcode`，`EmployeeUserID` 仍为空且
实例仍为 disabled，符合“员工本人扫码成功前不得启用”的边界。生产 `.tsx` 页面已确认
不存在“异地登录代理”或“设置代理并获取登录二维码”文案。首次绑定入口没有接管该替换
草稿；原实例停用和替换关系写入仍由既有替换完成事务负责。

剩余真机步骤必须由员工本人完成：

1. 刷新员工号实例页，通过旧实例的“更换登录员工号”打开现有替换绑定页；
2. 点击“获取登录二维码”并使用目标企微员工号扫码；
3. 若供应商返回 `QRCODE_REQUIRE_VERIFY(10)`，确认页面自动出现确认码输入并由本人填写；
4. 确认协议返回成功、员工资料同步，并完成替换页后续验证；
5. 确认新实例变为 enabled + online、旧实例写入 `ReplacedByInstanceID`，再做真实消息回调
   和收发验收。

上述步骤完成前，当前结论仅为“直接二维码链路已部署且真实取码成功”，不宣称员工号已经
登录。截图中明文展示过供应商应用 Secret，完成链路核验后仍必须在供应商控制台轮换，并与
服务器配置原子同步。

## 在线员工号扫码重新登录入口生产修复

2026-08-01 已将有效在线实例的既有“扫码重新登录”入口恢复到生产环境。发布事实如下：

```text
代码提交：29e5e2d24d98807782d3feb63546fa1e3dc42d96
源码归档 SHA-256：79ceec8f9d027f34393ee53ec89f6b549886805f02f6f0e51f3e743e61f218f5
发布目录：/opt/agentdesk/releases/20260801-182746-wxwork-online-relogin/app
部署前备份：/opt/agentdesk/backups/20260801-182746-wxwork-online-relogin
回滚镜像：mlogclub/agent-desk:rollback-20260801-182746-wxwork-online-relogin
回滚镜像摘要：sha256:7b3064f2498d3abc498c8e9d205e18b15f637c1a2d6b8fcecbd761539b8a2ea2
生产镜像：sha256:f34c331fec08c273d978346d2112b61728ad2d94afa8cea317f0db04226d388c
镜像创建时间：2026-08-01 18:32:06 +08:00
应用容器启动时间：2026-08-01 18:33:23 +08:00
```

部署使用 Compose project `agentdesk`，原子切换 `/opt/agentdesk/current` 后执行
`--force-recreate --no-deps agent-desk`。应用容器为 `healthy`，服务器本机和公网 HTTPS
均返回 HTTP 200；MySQL 容器未重建，仍保持原数据卷和健康状态。

在生产管理员的“丽斯未来”公司上下文刷新 `/dashboard/wxwork-protocol-instances/` 后，
真实复验结果为：

1. 有效实例“其风”继续显示 `online`，操作菜单已经出现“扫码重新登录”；
2. 已过期实例“黄奇峰”继续显示过期提示，操作菜单没有“扫码重新登录”；
3. 复验只检查菜单可见性，没有请求新二维码、扫码或改写任何实例登录状态；
4. 取码后的状态保持规则已由自动化测试覆盖：在线实例保持 `online`，非在线实例进入
   `login_qrcode`，状态 `10` 的确认码链路保持原样。

启动日志另有一条与本次发布无关的 FastGPT usage sync MySQL `cursor` 条件语法告警；应用
健康检查、员工号页面和本次入口均不受影响。本次按强制边界未修改 AI、FastGPT 或计费
代码，应在对应功能任务中单独修复并回归。

## 替换草稿消息回调诊断与恢复入口生产修复

2026-08-03 已将未验证替换草稿的消息回调诊断和后台恢复入口部署到生产环境。发布事实：

```text
代码提交：2fc640293fc1cfc5325ce7c2713496e72a456c76
源码归档 SHA-256：16856174338ce32e70ec6fc7e8ec786c1d4068191ed76f48a67e7aa641b85eb1
发布目录：/opt/agentdesk/releases/20260803-094754-wxwork-replacement-recovery/app
部署前备份：/opt/agentdesk/backups/20260803-094754-wxwork-replacement-recovery
MySQL dump SHA-256：8e674f0d15480344c00b1c12858e8c4a50219419d76811c13e02a59e522b723a
回滚镜像：mlogclub/agent-desk:rollback-20260803-094754-wxwork-replacement-recovery
回滚镜像摘要：sha256:f34c331fec08c273d978346d2112b61728ad2d94afa8cea317f0db04226d388c
生产镜像摘要：sha256:7f88a87490a23114afa41bdf0a7c91ccb31934b0ae055da22be467b566a2666c
镜像创建时间：2026-08-03 09:58:16 +08:00
应用容器启动时间：2026-08-03 09:58:37 +08:00
```

部署继续使用 Compose project `agentdesk`，原子切换 `current` 后仅执行应用容器的
`--force-recreate --no-deps`。应用容器为 `healthy`，服务器本机首页和公网员工号实例页
均返回 HTTP 200；MySQL 容器未重建，启动时间仍为 `2026-07-31 16:06:59 +08:00`。
启动日志没有 panic、fatal、migration 或数据库连接错误。

生产调查确认真实客户消息 `11010` 已由供应商送达短回调 URL，但扫码成功的新实例是尚未
完成邮箱/远程设置验证的替换草稿，因此按现有安全规则不能接管会话。绑定的门店员工系统
账号当前没有登记邮箱，必须先在用户管理补充真实邮箱，再从员工号实例页点击“继续完成更换”
并由邮箱持有人完成验证码。此次发布没有直接修改实例替换字段、伪造验证、重放客户消息或
改写数据库业务状态。

完成验证后必须重新发送一条真实客户消息，确认 `11010` 返回 200、同步日志为 `success`、
消息和会话均成功落库；随后使用现有协议消息补偿能力恢复验证期间遗漏的消息。在用户完成
邮箱验证和真实复验前，结论只到“接入线路正常、阻塞状态已定位且恢复入口已部署”，不宣称
该替换实例已经激活或客户消息已经恢复。

## 南七模型方案与 FastGPT Team 映射生产修复

2026-08-03 修复“已有 FastGPT Dataset 已采用但本地没有 Store Team 映射”，并完成南七
门店模型凭据真实激活。发布事实：

```text
发布目录：/opt/agentdesk/releases/20260803-154831-fastgpt-team-fix/app
部署前备份：/opt/agentdesk/backups/20260803-154831-fastgpt-team-fix
回滚镜像：mlogclub/agent-desk:rollback-20260803-154831-fastgpt-team-fix
回滚镜像摘要：sha256:078877d336abeb02da9ac9b4914f7dfa1424539d5db76b8910deacc8b039cf04
生产镜像摘要：sha256:f664a3221944e40aa53c4376df9ea84e9d707cc34d5cd080f824d836dc9b7cce
服务二进制 SHA-256：a68b462cbc9b688d3b38f3c2f7d47affddec0b760fbd57b9753592812f9e005e
应用容器启动时间：2026-08-03T07:53:18.694212386Z
```

本次在本机完成 Linux amd64 交叉编译，服务器只基于已固定的回滚镜像替换服务二进制，
没有再次执行 Go 或 Next.js 源码构建。切换继续使用 Compose project `agentdesk` 和
`--force-recreate --no-deps agent-desk`，MySQL 容器及数据卷未重建。新容器为 healthy，
重启数为 0，本机与公网 HTTPS 返回 HTTP 200，部署后启动/运行错误和明文 Key 模式日志
命中均为 0。

代码行为：受控采用已有 Dataset 时，仍先完成门店归属、名称、集合索引、模型快照和真实
检索校验；全部通过后调用 FastGPT `tenant/ensure`，并在原采用事务内创建或更新非敏感
`FastGPTStoreTenant`。新映射保持 `unconfigured`，重复采用不覆盖已有 Target/Applied
Profile、Binding、Credential revision、指纹或 readiness。任一远端验证失败时不写本地
Team 映射。

生产业务验收全部通过正常页面和受权限保护的 API 完成，没有直接写数据库：

1. 重新采用合肥南七店既有 Dataset，返回 1 个集合、20089 条内容；页面显示 FastGPT
   Team `active`，凭据激活前 readiness 保持 `unconfigured`；
2. “合肥南七 / 门店员工1”重新提交 NewAPI Key，八个启用槽真实请求全部通过，FastGPT
   模型同步成功；模型方案 r1、凭据 r2 和 Team 最终均为 ready；
3. 回复、意图、摘要、客户标签使用 `deepseek-v4-pro`，视觉与文档解析使用
   `qwen3.5-flash`，Embedding 使用 `text-embedding-v4`，Rerank 使用
   `qwen3-vl-rerank`；ASR 未配置且停用，系统没有 TTS 槽；
4. 以“停车场在哪里？”执行真实知识库检索，710 ms 命中 12 条，结果包含昭潭路停车入口、
   免费停车和地下车库充电桩等南七资料；
5. 首次失败候选保留在不可修改审计中，因此成功 revision 为 r2；禁止将审计历史改写为
   r1，也禁止直接清表或修改 revision。

部署前数据库压缩备份已通过 `gzip -t` 完整性校验。本次没有 Schema 或 DML migration；
回滚应用时可切回上述固定镜像，但已经激活的凭据、FastGPT Team 映射和 applied revision
属于业务状态，不得随意回退或手工清零。

## 南七定位意图超时与首联小程序去重生产修复

2026-08-03 对“询问酒店位置后看到小程序”的生产记录完成了逐条核对。真实时间线是：

1. `16:33:43` 新会话创建时发送普通欢迎小程序；
2. 客户随后发送“酒店在哪”，该问题进入 IntentDetect，但被 Runtime 内独立硬编码的 12 秒
   超时截断，没有形成定位回复；
3. `16:39:47` 延迟联系人同步为同一联系人发送到店绑定票据
   `arrival_bind_ticket_5`。

因此，第二张卡恰好出现在位置问题之后，不代表 AI 将位置意图规划成小程序。根因是意图
模型调用被 12 秒上限提前取消，同时南七门店当时没有导航名、地址和经纬度，定位动作即使
进入计划也缺少可发送的 Store 事实。

代码修复包括：

- IntentDetect 改为读取当前模型槽 `TimeoutMS`；生产槽实际值为 `30000ms`，未配置时使用
  60 秒默认值，不再受独立 12 秒上限截断；
- 首条客户消息与延迟联系人同步统一调用首联资源编排，首条消息即可创建到店绑定票据；
- 存在有效静态到店连接时，不再发送普通默认欢迎小程序；
- 两条首联入口复用稳定 `arrival_bind_ticket_<ticketID>`，由现有消息幂等去重；
- 没有把小程序作为定位失败 fallback，也没有修改公开 API、DTO、model、migration、
  WebSocket 或前端页面。

南七门店定位通过现有管理接口正常保存，没有直接修改业务数据库：

```text
门店：合肥南七
导航名：丽斯未来酒店(合肥南七店)
地址：合肥市包河区水阳江路392号职工之家12-15整层
经度：117.263900
纬度：31.824091
地图服务：amap
```

地址和名称由既有南七知识库交叉确认；知识库没有坐标，坐标取自地图 POI，并与项目既有
测试坐标相差约 1 米。发布事实：

```text
源码基线：7c8ae19d98c26446e38d1dc5387d22e9d1c7dc57（叠加本次未提交工作树变更）
发布目录：/opt/agentdesk/releases/20260803-171002-location-intent-welcome/app
部署前备份：/opt/agentdesk/backups/20260803-171002-location-intent-welcome
备份 SHA-256：b329457cb5c4b40e0289ac4d56795f894721ae744bb1c706bfadf90c0f065e2d
回滚镜像摘要：sha256:f664a3221944e40aa53c4376df9ea84e9d707cc34d5cd080f824d836dc9b7cce
生产镜像摘要：sha256:6cafc98063c49bf04d27121991150e883c10d030af9d043918857366aae375e6
服务二进制 SHA-256：12390cb850f4933ce55c6331d4f94efa0dae4347faa38f81ce6681e885994fa9
应用容器启动时间：2026-08-03T09:11:34.207353601Z
```

工程验证已通过 `go test ./internal/services -count=1`、
`go test ./internal/ai/runtime/... -count=1`、`go test ./internal/bootstrap/... -count=1`、
`go test ./... -count=1`、`go vet ./...` 和 `git diff --check`。部署后应用容器为 healthy，
重启数为 0，服务器本机与公网均返回 HTTP 200，启动错误计数为 0；MySQL 未重建，本次无
Schema 或 DML migration。

尚未宣称真实定位消息端到端成功。最终复验必须由真实客户再次发送“酒店在哪”，并确认新
RunLog 不再在 12 秒超时、意图为 `provide_location`、消息类型为 `location`、Outbox 成功、
协议调用 `/msg/send_location`，且该问题之后不再新增小程序卡。应用回滚可切回上述固定
镜像；门店定位是稳定业务配置，可保留。回滚代码会恢复旧 12 秒截断和旧欢迎卡行为，且不
涉及数据库恢复。

## 已有联系人重复发送到店绑定卡修复

2026-08-03 对同一客户在持续会话中反复收到小程序卡的问题完成生产取证。数据库记录确认，
同一会话分别在 `16:39:47`、`17:31:36`、`18:01:36` 创建了三张到店绑定卡；最近一张的
请求来源为 `wx_contact_welcome_*`，不是 AI 回复、知识库检索或定位意图。旧实现只要联系人
增量再次出现且上一票据已经过期，就会为已有会话创建新票据，因此表现为大约每 30 分钟
重复发送。

本次将自动首联资源的必要条件收紧为 `ensureConversation` 真实新建会话。已有会话的联系人
增量无论旧票据是否过期，都不再自动创建或发送 `arrival_bind_ticket_*`；首条消息真实新建
会话、后台人工发送和真实 `ArrivalScanEvent` 扫码链路保持不变。改动没有触及 model、
migration、DTO、公开 API、WebSocket、前端、小程序或 AI 回复引擎。

发布事实：

```text
发布目录：/opt/agentdesk/releases/20260803-175828-contact-card-dedupe/app
部署前备份：/opt/agentdesk/backups/20260803-175828-contact-card-dedupe
回滚镜像：mlogclub/agent-desk:rollback-20260803-175828-contact-card-dedupe
回滚镜像摘要：sha256:6cafc98063c49bf04d27121991150e883c10d030af9d043918857366aae375e6
生产镜像摘要：sha256:3a2afab6734a4edc068be3fd5597ccf7f4a38b109e103556a60a8a9b8daa9310
服务二进制 SHA-256：0011eafc8e70408d18a2625e848a6bf0ef6ed5c2b10d3988153b754e574e7cef
数据库备份 SHA-256：a8802e4e65b9d34bc04f24ff8a7bcc6c832d7cc5a16134dfdd7c0875412df4fc
应用容器启动时间：2026-08-03T10:06:19.430218577Z
```

数据库备份使用 `--no-tablespaces` 生成，并在独立恢复库完成 118 张表的恢复核对。工程验证
通过定向首联/票据过期/真实二次扫码测试、`go test ./internal/services -count=1`、
`go test ./internal/ai/runtime/... -count=1`、`go test ./internal/bootstrap/... -count=1`、
定向 `go test -race`、`go test ./... -count=1`、`go vet ./...` 和 `git diff --check`。

首次镜像组装误继承了临时容器的 `sleep 3600` 启动命令，健康检查判定为 unhealthy，应用
没有启动；随后立即从固定回滚镜像重新覆盖已校验二进制，保留原始 CMD 后强制重建。最终
容器为 healthy、重启数为 0，本机与公网均返回 HTTP 200，MySQL 容器未重建。部署后的
首轮 5 分钟联系人补偿扫描在 `18:11:22` 执行，实例扫描时间已更新，部署后新增绑定票据数
仍为 0。真实生产终验仍需等待下一次包含联系人增量的官方事件，确认已有会话不再新增
`arrival_bind_ticket_*`；不得用人工写库或伪造回调代替该验收。

应用启动、迁移和健康检查没有错误；运行日志中仍存在本次发布前已经存在的 FastGPT Usage
同步 MySQL 1064 告警，原因是同步更新条件未正确转义 `cursor` 保留字。该告警与联系人首联
资源链无关，本次没有扩大发布范围修改计费同步；不得将本次发布描述为“全部运行日志错误
为 0”。

## 登录邮箱 SMTP 465 TLS 修复

2026-08-04 修复登录页发送邮箱验证码约 20 秒后失败的问题。生产数据库中的失败记录为
`初始化邮件服务器连接失败: EOF`；容器虽然已配置 163 SMTP 主机和账号，但 Compose 没有
透传邮件端口与 TLS 模式，运行时继续使用挂载配置中的 `587 + starttls`。163 邮箱当前配置
要求 `465 + tls`，因此服务在 SMTP 欢迎阶段读取到 EOF，尚未进入账号验证或邮件提交。

本次新增 `AGENT_DESK_EMAIL_PORT` 与 `AGENT_DESK_EMAIL_TLS_MODE` 环境变量支持：端口仅接受
`1-65535`，TLS 模式仅接受 `tls`、`starttls` 或 `plain`；Compose、示例环境、生产配置校验
和部署文档同步更新。没有修改登录 API、用户模型、数据库结构、Migration、前端页面、AI
回复引擎或企微员工号协议。

发布事实：

```text
Git 提交：5c61997730b478253bd03ea00c0951af2bc5d7e6
发布包 SHA-256：f2e872d2687bd531475708ac625e4415a004c3835bafc1f93c1050936424300d
发布目录：/opt/agentdesk/releases/20260804-161200-email-smtp-tls/app
部署前备份：/opt/agentdesk/backups/20260804-161200-email-smtp-tls
回滚镜像：mlogclub/agent-desk:rollback-20260804-161200-email-smtp-tls
回滚镜像摘要：sha256:3a2afab6734a4edc068be3fd5597ccf7f4a38b109e103556a60a8a9b8daa9310
生产镜像摘要：sha256:cf8cc0be0cbc9eef0dce330c94642310f59ab870738ef82746f8e9e74e10e730
服务二进制 SHA-256：f77956a89637273f5ac73a08eb7b5c3c50c7a453d10fa1ed4b4178f8ee274437
应用容器启动时间：2026-08-04T08:16:39.6259967Z
```

工程验证通过前端生产构建、`go test ./... -count=1`、`go vet ./...` 和
`git diff --check`。部署时只强制重建 `agent-desk`，MySQL 容器未重建；本次没有 Schema 或
DML migration，因此没有执行数据库回退型备份。生产环境文件与旧镜像均已建立独立回滚点。

部署后容器内实际值为 `AGENT_DESK_EMAIL_PORT=465`、
`AGENT_DESK_EMAIL_TLS_MODE=tls`，SMTP TLS 1.3 握手成功，本机与公网登录/认证接口均为
HTTP 200，容器 healthy、重启数 0、启动及运行错误计数 0。使用登录页现有已验证门店邮箱
执行一次真实发送后，页面进入“重新发送”状态，数据库中新验证码记录保持未消费且无发送
错误；未读取、记录或输出验证码。163 邮箱仅作为 SMTP 发件账号，不能直接替代用户表中的
已验证登录邮箱。

## 门店员工会话工作台权限与 Binding 隔离生产发布

2026-08-04 将门店员工账号接入现有会话工作台，并按当前有效
`StoreStaffBinding` 收紧企微实例、会话列表、消息读取、消息发送和 WebSocket 实时事件范围。
本次没有新增平行会话页面，也没有开放租户级账号或跨门店数据权限；管理员、主管和客服原有
工作台语义保持不变。

发布事实：

```text
Git 提交：9f1d59380dfc81b672e53b033f32ced669b1ed2c
发布目录：/opt/agentdesk/releases/20260804-173125-store-staff-workbench/app
部署前备份：/opt/agentdesk/backups/20260804-173125-store-staff-workbench
数据库备份 SHA-256：d9006b2d839502c9b5880676096c7f2bf0e3f65f5676e35737861d0bf273dcc1
回滚镜像：mlogclub/agent-desk:rollback-20260804-173125-store-staff-workbench
回滚镜像摘要：sha256:cf8cc0be0cbc9eef0dce330c94642310f59ab870738ef82746f8e9e74e10e730
生产镜像：mlogclub/agent-desk:release-9f1d593-store-staff-workbench
生产镜像摘要：sha256:098bf43dbcf2ebcf376bfa18f4713198c1f41b80c52739a5b429580f32a094d6
服务二进制 SHA-256：be9b10ad614ee1c7ceca2cb40e142f20ac85f3d970d424ba3672760509a950f1
租户审计二进制 SHA-256：f77016ba4203e72e7fb121252bedfb1cd1ab1b3d1b83d567dc29023ce8b24373
应用容器启动时间：2026-08-04T09:44:43.920248023Z
```

数据库压缩备份已通过 `gzip -t`，并在独立临时库完成恢复验证：119 张表，Tenant 2、Store 2、
Binding 2、企微员工号实例 2、Conversation 2、Message 162、Migration 8；临时库随后删除。
发布时原子切换 `current`，仅使用 `--force-recreate --no-deps agent-desk` 重建应用容器，MySQL
容器和数据卷未重建。

DML migration 74 在线执行成功且仅有一条成功记录。`store_staff` 系统角色最终各有且仅有一条
`conversation.view` 和 `conversation.send` 权限，角色权限总记录由 427 增至 429。新容器
为 healthy，重启数为 0；服务器本机 `/health` 与公网登录页均返回 HTTP 200，最近启动日志
没有 panic、fatal、migration 或数据库连接错误，敏感配置名称扫描命中为 0。

门店员工真实页面使用独立域名会话复验，保留管理员主域登录不退出。刷新后“门店员工1”已
出现“会话”和“门店工作台”入口；会话页显示“我的企微账号”，仅呈现其当前 Binding 下的
合肥南七企微账号和 2 个客户会话，没有开放租户全部账号。页面中的员工身份提示、AI/人工
状态和消息发送边界均来自现有工作台，没有创建第二套会话或客户身份。

工程验证通过 `go test ./... -count=1`、`go vet ./...`、`pnpm typecheck`、`pnpm build`，以及
19 项前端导航和账号权限契约测试。Lint 无 error，保留 32 条本次发布前已有 warning。

部署后仍可见两项发布前既有问题：FastGPT Usage 定时同步告警，以及租户审计将
`StoreModelProfileAssignment.template_id=0` 的 readiness 中间态报告为缺少父引用；新旧镜像
结果一致，均与本次门店员工会话权限改动无关。本次没有修改 AI 模型配置、计费同步或直接
改写相关业务数据。

## DeepSeek Flash 切换与九槽直接编辑生产发布

2026-08-05 将当前门店运行方案的回复、意图、摘要和客户标签四个语言槽从
`deepseek-v4-pro` 切换为 `deepseek-v4-flash`。r2 先使用已有门店 Binding 凭据完成真实启用槽
测试，再走既有发布、门店批量指派和 `activate_pending` 受保护接口完成切换；没有直接修改
数据库或员工号凭据。合肥南七和高铁南站店最终均使用 r2，pending 为 0，模型与 FastGPT
readiness 均为 ready；门店员工1凭据为 K2，门店员工2凭据为 K3。

模型方案页面同步恢复“生效后直接编辑”：active/candidate 保存时由服务层在事务内创建或复用
同编码 draft，已发布 revision 继续不可变；更新请求必须携带匹配的 `confirmRevision`。页面按
编码只展示一个逻辑方案，门店常规指派也只提供最新可发布 revision，避免误选旧模型历史。

发布事实：

```text
Git 提交：003b39304837067f92a20f1eee896f717ce89ff9
发布包 SHA-256：1a08b1004c315667a0b0ac5ec35f301198e7cd86113c84a619fc0442f7d7837a
发布目录：/opt/agentdesk/releases/20260805-144227-model-profile-edit/app
部署前备份：/opt/agentdesk/backups/20260805-144227-model-profile-edit
数据库备份 SHA-256：c63729a087ffcc781fa2041aabafabf139f948782abc09c779c2e80721e21437
回滚镜像：mlogclub/agent-desk:rollback-20260805-144227-model-profile-edit
回滚镜像摘要：sha256:098bf43dbcf2ebcf376bfa18f4713198c1f41b80c52739a5b429580f32a094d6
生产镜像：mlogclub/agent-desk:release-003b393-model-profile-edit
生产镜像摘要：sha256:a580db6bc61a7666cb678e44b5810e24845589d9a9a255b14bb2f442e9d1d49c
服务二进制 SHA-256：801add9755e2d168ed8d823e9e45f68bf12f9807de91f84d7920acdcb2cc1a68
租户审计二进制 SHA-256：5bd930d260165c13f62afc83d44dbe539c9b39b694acd9c8d46ea2d5269131ee
应用容器启动时间：2026-08-05T06:58:12.880775693Z
```

服务器只有 3.6 GiB 内存且未配置 swap，直接执行完整 Docker 多阶段构建时 Next.js 进入持续
内存压力。该构建在切换前主动终止，旧容器始终在线；随后使用本机已通过 `pnpm build` 的
`web/out` 交叉编译 Linux/amd64 服务与审计二进制，并从固定回滚镜像组装包含新前端资源的
候选镜像。候选镜像先校验二进制摘要、revision 标签和新服务契约，再原子切换 `current`，仅
使用 `--force-recreate --no-deps agent-desk` 重建应用容器，MySQL 未重建。

部署后新容器 healthy、重启数 0，本机 `/health` 和公网模型方案页均为 HTTP 200。启动后
84 行日志中 error/fatal/panic 和敏感值模式命中均为 0；租户完整性审计覆盖 98 个模型、114
张表和 287 个关系，结果 passed、0 violation。生产页面只显示一个 `standard` 当前生效方案，
“编辑配置”可打开完整九槽编辑器，四个语言槽均显示 `deepseek-v4-flash`。数据库复核两家门店
每家四个 Flash 槽、零个 Pro 槽，员工凭据均 active，方案 revision 均为 r2。

含 `deepseek-v4-pro` 的 r1 作为不可变发布历史保留，当前没有门店或员工 Binding 引用，也不再
出现在常规方案和门店指派选择中。保留历史是审计要求，不代表 Runtime 仍在使用旧模型。

## 知识库图片企微投递修复

2026-08-05 排查确认 AI 已正确检索知识记录并创建图片消息，故障发生在企微富媒体 Outbox：
全局 `storage.asset.publicAssetBaseUrl` 仍指向已无法解析的历史域名，并优先覆盖企微渠道自身的
正确公网地址，导致 `/cloud/c2c_upload` 返回错误码 `-1`，最终 `/msg/send_image` 未执行。
同一轮 AI 文本消息已成功发送，因此可以排除会话映射、员工号实例和知识检索整体故障。

线上先将全局公网资产地址修正为 `https://weibao.omnireva.com`。原失败图片任务随后自动重试
成功，Outbox 状态为 `sent`，错误清空，消息已取得协议 `file_id/aes_key` 并生成 sent 渠道消息
映射；没有重新创建知识图片或伪造渠道成功。代码同步删除后端与存储设置页面中的历史域名
默认值，全局值为空时继续使用企微渠道配置，并为 `get_cdn_info`、`c2c_upload` 和上传响应解析
增加受控阶段标识。

发布事实：

```text
源码基线：79704312acf7e99acfbff619a5130fd67b6e7cfa（含本次工作树修复）
发布目录：/opt/agentdesk/releases/20260805-155011-knowledge-image-delivery/app
部署前备份：/opt/agentdesk/backups/20260805-155011-knowledge-image-delivery
数据库备份 SHA-256：538f837bb00c7beb6a89306c6026a0547a35e57da051bb6798aff2e635f64019
源码包 SHA-256：f9f39a98298dddcd029c7a89bd21a6c40ca741736779cc943ff3b7850743c1de
服务二进制 SHA-256：2ad8669be9e1275b0f25375ce5cae3526e7a799c9d3787232ddadb797668a113
租户审计二进制 SHA-256：a62ffc57dee7a0b3257bdffc9929bde47fbd3b22a6e787f7e4e81780f3e9e57a
回滚镜像：mlogclub/agent-desk:rollback-20260805-155011-knowledge-image-delivery
生产镜像：mlogclub/agent-desk:release-20260805-155011-knowledge-image-delivery
生产镜像摘要：sha256:3551a73678aac8ed45ef7904992ca5efba4b5dee74949c1e61aedcd6f29bd70b
应用容器启动时间：2026-08-05T07:55:20.947486244Z
```

数据库备份使用 `--no-tablespaces --single-transaction`，通过 `gzip -t` 并在独立临时库恢复出
119 张表后删除临时库。新容器 healthy、重启数 0，本机与公网健康检查、会话页面均返回
HTTP 200；启动日志未发现 fatal、panic、migration/database 启动错误或敏感配置名。租户完整
性审计覆盖 98 个模型、114 张表和 287 个关系，结果 passed、0 violation。验证通过
`go test ./... -count=1`、`go vet ./...`、`pnpm typecheck` 和 `pnpm build`。本次没有模型、
Migration、DTO、公开接口、WebSocket 或 AI 知识检索协议变化。

## AI 回复调度降延迟与 DeepSeek 思考关闭生产发布

2026-08-05 按 `origin/codex/ai-billing@4db7993` 的最新回复链路核对并修复生产运行时。
`AIReplyJob` 到期任务改为非阻塞派发，单进程最多 4 个 worker；同一会话的新客户消息会取消旧执行，
旧任务重新检查消息新鲜度后收敛为 `superseded`，持久任务、租约、重试、恢复和 Outbox 幂等边界保持不变。

DeepSeek V4 的识别不再依赖官方 BaseURL。Runtime、辅助 LLM 和九槽凭据连通性验证经统一 NewAPI
网关调用 `deepseek-v4-flash` 时均显式发送 `thinking.type=disabled`。本次没有修改前端、公开 API、
DTO、数据库模型、Migration、WebSocket、计费口径或生产密钥。

发布事实：

```text
Git 提交：43948c68e08a2b88800a4933afcc2b4528ac1f3a
源码包 SHA-256：798f61a6473769d32095a8c61dc8bbe57851a8f0a10d6a42882b86054c31949b
发布目录：/opt/agentdesk/releases/20260805-171500-ai-reply-43948c6/app
部署前备份：/opt/agentdesk/backups/pre-43948c6-20260805T090717Z
旧发布目录备份 SHA-256：67c21ff34802a8219a0d9298c4709e2dd17dedbc82a1fb02abc0f6420e58249c
数据库备份 SHA-256：44723841751fa150e3a08e7fdd38d7971a90ba196bddbb030a86f053b443c9df1
旧镜像备份 SHA-256：eac66232a3cf8c9473760f54cc9fefbd1a2a06df78b042e5de698c118a57696a
回滚镜像：mlogclub/agent-desk:rollback-pre-43948c6
生产镜像：mlogclub/agent-desk:43948c6
生产镜像摘要：sha256:e36507720791ba3176a756dacdc3bd9a9060b5080b12d97adba510d7293a35ff
服务二进制 SHA-256：66e39a17643222acdf7ceb244ab14ca0f717319d532106af4b9c7a858d876b01
租户审计二进制 SHA-256：e1c1229caac8553ab14fef93c91f5e476f9b13e6b8a264ec75f1324fb5f32542
应用容器启动时间：2026-08-05T09:12:08.824045237Z（北京时间 17:12:08）
MySQL 容器启动时间：2026-08-03T07:23:35.617760019Z
```

数据库使用 `--no-tablespaces --single-transaction --quick --routines --triggers` 导出，压缩包通过
`gzip -t` 且包含 `Dump completed on` 完成标记。发布时原子切换 `current`，仅执行
`--force-recreate --no-deps agent-desk`；MySQL 容器 ID 和启动时间未变化，重启数为 0。

新应用容器为 `healthy`、重启数 0，容器内二进制摘要与本地发布产物一致。租户完整性审计退出码为
0；启动后日志中 panic、fatal 和 error level 均为 0。公网首页与会话页均返回 HTTP 200，实测耗时
分别约 0.298 秒和 0.284 秒。工程验证通过 `go test ./internal/ai/... -count=1`、
`go test ./internal/services -count=1`、指定范围 race 测试、`go test ./... -count=1` 和 `go vet ./...`。

发布后尚未代替客户伪造真实入站消息。需要由用户发送一组连续测试消息后，再只按聚合用量事实确认
成功 DeepSeek 调用的 `reasoning_tokens=0`，不得读取消息正文、Prompt、API Key 或访问令牌。
