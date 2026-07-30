# 知悉微宝完整部署手册

本文说明如何从空数据库部署当前 `main` 版本，涵盖 Docker Compose、HTTPS、外部服务、
备份、升级、回滚和验收。所有示例使用占位域名 `weibao.example.com`，不得把示例值原样
用于生产。

## 1. 部署边界

- 当前版本支持 SQLite 和 MySQL，生产推荐 MySQL 8.4。
- 当前版本只支持从 fresh 空数据库初始化，不支持直接连接历史业务库升级。
- `.env`、`production.env`、数据库、备份、证书、私钥和外部服务 Token 不属于源码。
- 门店 NewAPI Key 只能通过门店凭据页面录入，不得写入环境文件或 Git。
- 活动服务必须保持 `AGENT_DESK_BACKGROUND_WORKERS_ENABLED=true`。
- 到店联动、FastGPT、企业微信等可选功能在凭据和 HTTPS 未就绪前保持关闭。

## 2. 服务器要求

建议生产起步配置：

| 项目 | 建议 |
| --- | --- |
| 操作系统 | 64 位 Linux |
| CPU | 4 核及以上 |
| 内存 | 8 GB 及以上 |
| 磁盘 | 80 GB 及以上，数据库与备份按业务量扩容 |
| Docker | Docker Engine 26+ |
| Compose | Docker Compose v2.27+ |
| 域名 | 独立 HTTPS 域名 |

防火墙建议只开放：

- `22/tcp`：限制运维来源地址；
- `80/tcp`：仅用于跳转 HTTPS 或证书签发；
- `443/tcp`：正式业务入口；
- `8083/tcp`：只绑定回环地址或由安全组限制，不直接暴露公网。

MySQL 不需要映射宿主机端口，默认只在 Compose 网络中访问。

## 3. 获取代码

```bash
git clone https://github.com/520skyincloud/weibao.git
cd weibao
git checkout main
```

私有仓库需要先为部署账号配置只读 Deploy Key 或受限访问凭据。禁止把个人 GitHub Token
写进部署脚本、镜像层或服务器命令历史。

## 4. 生成生产环境文件

生产环境文件建议放在仓库之外：

```bash
sudo install -d -m 0700 /opt/weibao/shared
sudo install -d -m 0700 /opt/weibao/backups
umask 077
cp .env.example /opt/weibao/shared/production.env
chmod 0600 /opt/weibao/shared/production.env
```

至少为以下项目生成互不相同的随机值：

```bash
openssl rand -hex 32
openssl rand -hex 32
openssl rand -base64 32
openssl rand -base64 32
openssl rand -base64 48
openssl rand -base64 48
```

分别用于 MySQL 应用密码、MySQL root 密码、邀请码加密密钥、门店凭据加密主密钥、
客户会话秘密和资产 URL 签名秘密。禁止把同一随机值复用于多个变量。

fresh 初始化还必须设置：

```text
AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME=<初始超级管理员用户名>
AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD=<高强度初始密码>
```

MySQL DSN 格式：

```text
cs_ai_agent:<应用数据库密码>@tcp(mysql:3306)/cs_ai_agent?charset=utf8mb4&parseTime=True&loc=Local
```

完整变量格式、轮换和恢复边界见
[生产密钥与外部凭据手册](production-secrets.md)。

## 5. 配置校验

只使用不会展开秘密值的校验命令：

```bash
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  config --quiet
```

禁止把不带 `--quiet` 的完整 Compose 配置输出到聊天、Issue、CI 日志或交接文档。

首次部署前建议执行：

```bash
cd web
pnpm install --frozen-lockfile
pnpm typecheck
pnpm lint
pnpm build
cd ..
go test ./... -count=1
go vet ./...
```

## 6. 构建与启动

```bash
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  build --pull agent-desk

docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  up -d
```

查看状态：

```bash
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  ps
```

本机探测：

```bash
curl -fsS http://127.0.0.1:8083/ >/dev/null
```

查看应用日志：

```bash
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  logs --tail=200 agent-desk
```

日志中不得出现密码、Token、API Key、永久授权码、预授权码、客户身份原文或完整密文。

## 7. HTTPS 反向代理

下面是最小 Nginx 示例，证书路径按实际环境替换：

```nginx
server {
    listen 80;
    server_name weibao.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name weibao.example.com;

    ssl_certificate     /etc/letsencrypt/live/weibao.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/weibao.example.com/privkey.pem;

    client_max_body_size 25m;

    location / {
        proxy_pass http://127.0.0.1:8083;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
    }
}
```

企业微信员工号回调如果通过 query token 鉴权，应对对应精确路径关闭 Nginx access log，
避免 token 落盘。不要全局关闭错误日志。

## 8. 首次初始化

1. 确认 MySQL 数据卷为空。
2. 确认初始管理员用户名和密码已从生产环境文件注入。
3. 启动应用并等待 AutoMigrate 与 DML 初始化完成。
4. 登录 `/dashboard/login/`。
5. 立即创建日常管理员账号，按最小权限分配角色。
6. 初始超级管理员密码只用于受控初始化和恢复，不用于日常共享。
7. 创建 Tenant、Store、公司主管和唯一门店员工账号。

初始管理员变量不会在重启时覆盖已有账号。不得通过直接写数据库伪造注册、授权或连接成功。

## 9. FastGPT 与 NewAPI

启用 FastGPT 前设置：

```text
AGENT_DESK_FASTGPT_ENABLED=true
AGENT_DESK_FASTGPT_BASE_URL=https://fastgpt.example.com
AGENT_DESK_FASTGPT_INTEGRATION_TOKEN=<由 FastGPT 集成服务签发>
```

要求：

- Base URL 与 Integration Token 必须来自同一环境；
- FastGPT 必须实现项目规定的托管 Tenant、Dataset、检索和 Usage 契约；
- 每个 Store 使用独立 Dataset；
- NewAPI Key 不进入 `production.env`；
- 平台或门店授权人员通过“门店凭据”页面提交 NewAPI Key；
- 九个模型用途槽测试全部通过后，候选 revision 才能切换为 active；
- ASR 槽未配置可用渠道时应明确显示不可用，不得伪造成功。

## 10. 企业微信服务商与到店联动

启用到店联动前设置：

```text
AGENT_DESK_ARRIVAL_ENABLED=true
AGENT_DESK_ARRIVAL_PUBLIC_BASE_URL=https://weibao.example.com
AGENT_DESK_MINIPROGRAM_APP_ID=<小程序 AppID>
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=static_plugin_ticket
AGENT_DESK_ARRIVAL_BIND_TICKET_TTL_MINUTES=30
AGENT_DESK_ARRIVAL_BIND_PENDING_SCAN_WINDOW_MINUTES=30
```

静态模式还需要小程序 AppSecret、Arrival 会话/HMAC/数据加密密钥，并在到店连接页为每个
门店录入企业微信后台真实 `plugId` 和唯一员工实例。它不要求 Suite、永久授权码、客户联系
回调或官方创建链接权限。

选择 `customer_acquisition` 或 `contact_way` 时，另行配置
`AGENT_DESK_WECOM_SUITE_ID` 及完整服务商秘密。秘密值只从环境注入，详见
`.env.example`。服务商模式的企业微信后台使用：

```text
数据回调：https://weibao.example.com/api/third/wecom/provider/data-callback
指令回调：https://weibao.example.com/api/third/wecom/provider/command-callback
应用设置：https://weibao.example.com/wecom/provider/settings
授权回调：https://weibao.example.com/api/wecom/provider/authorization/callback
```

服务商应用处于企业微信“安装测试”阶段时：

```text
AGENT_DESK_WECOM_AUTH_TYPE=1
```

正式发布后改为：

```text
AGENT_DESK_WECOM_AUTH_TYPE=0
```

切换后必须重新创建容器加载环境变量：

```bash
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  up -d --force-recreate agent-desk
```

服务商成员绑定由管理员分别选择官方客户联系成员和当前 Store 的员工实例进行人工确认。
两个系统的成员标识属于不同命名空间，不得强制字符串相等，也不得按姓名自动绑定。

`customer_acquisition` 使用企业微信获客助手。启用前必须在第三方应用中保存“获客助手权限”，
并让当前测试企业重新授权，使新权限进入授权范围。部署后先在到店联动页面执行连接验证：

1. 真实额度接口返回成功且剩余额度大于零；
2. 第一次未绑定扫码创建或复用单成员获客链接；
3. 二维码可解码，且页面只暴露受控 PNG 资源 URL；
4. 客户添加成员后由回调或补偿对账精确写入门店关系；
5. 未完成员工号协议会话映射时保持 `legacy_unmapped`，不得伪造二次扫码发卡成功。

`static_plugin_ticket` 真机验收必须使用新微信：首次扫码返回真实插件配置、客户主动添加
员工、真实单聊收到绑定卡片、bind 成功、二次扫码只投递原会话。`-3006` 和存量好友不
视为已绑定，后者从会话工作台人工发卡。三种 Provider 禁止因权限、额度或发送错误自动
切换。切换 Provider 后必须使用 `--force-recreate` 重建应用容器。

## 11. 企微员工号协议

员工号协议只以 `https://wework.apifox.cn/llms.txt` 及其具体接口页为依据：

- 实例必须先在实例池真实登录且未过期；
- 接入时读取真实资料并绑定当前 Tenant、Store；
- 消息发送使用协议返回的 `conversation_id`；
- 单聊联系人前缀为 `S:`，群聊前缀为 `R:`；
- 设置通知地址使用当前短回调路径；
- 只有出现可证明的序号缺口时才允许执行受限补漏；
- 不得把企业微信服务商、微信客服、个人微信或旧协议字段混入员工号运行链。

## 12. 备份

备份数据库：

```bash
backup_dir="/opt/weibao/backups/$(date +%Y%m%d-%H%M%S)"
install -d -m 0700 "$backup_dir"
docker compose \
  --project-name weibao \
  --env-file /opt/weibao/shared/production.env \
  exec -T mysql sh -lc \
  'MYSQL_PWD="$MYSQL_PASSWORD" mysqldump --single-transaction -u"$MYSQL_USER" "$MYSQL_DATABASE"' \
  | gzip > "$backup_dir/mysql.sql.gz"
chmod 0600 "$backup_dir/mysql.sql.gz"
```

备份本地资产卷：

```bash
docker run --rm \
  -v weibao_agent-desk-data:/source:ro \
  -v /opt/weibao/backups:/backup \
  alpine:3.22 \
  tar -czf "/backup/agent-desk-data-$(date +%Y%m%d-%H%M%S).tgz" -C /source .
```

数据库备份、资产备份和加密主密钥必须分别保管。只生成备份文件不算完成，必须定期恢复到
隔离环境并验证表数量、Migration、管理员登录和关键数据可读性。

## 13. 升级

推荐每次发布使用独立目录和不可变镜像标签：

1. 拉取目标 commit，并记录 commit SHA。
2. 运行完整测试和配置校验。
3. 备份数据库、资产卷和仓库外环境文件。
4. 为当前镜像添加回滚标签。
5. 构建新镜像，不覆盖旧回滚标签。
6. 使用 `up -d --force-recreate agent-desk` 切换应用。
7. 检查 Migration、健康状态、重启次数、登录和关键回调。
8. 验收通过后再清理旧 release。

不要直接在运行目录执行不可追溯的源码覆盖。生产发布记录至少包含 commit、镜像摘要、
启动时间、Migration、备份位置、验证结果和回滚边界。

## 14. 回滚

代码回滚前先判断本次是否包含数据库结构或不可逆数据变更：

- 仅服务或页面逻辑变更：可切回旧镜像，通常不需要恢复数据库；
- 新增兼容字段：旧代码是否能忽略新字段需要实际验证；
- 数据重写或清理：必须使用部署前备份在隔离环境验证后再恢复；
- 凭据格式或主密钥变化：禁止只回滚代码而不回滚配套配置。

回滚后重新执行健康、登录、Tenant 隔离、消息、派单和外部回调验收。

## 15. 验收清单

- 应用与 MySQL 均为 healthy，重启次数为 0；
- 管理后台和客服工作台可登录；
- fresh Migration 全部成功；
- 超级管理员、Tenant、Store、公司主管和门店员工范围正确；
- 规则派单不依赖大模型选人；
- AI 回复、FastGPT、NewAPI 九槽和账单归因按启用范围通过；
- 企微员工号入站、出站和回调幂等通过；
- 企业微信服务商数据与指令回调按真实请求通过；
- 到店联动门店连接为 active，成员映射不覆盖员工实例身份；
- 日志无秘密、客户身份原文、panic 或 fatal；
- 数据库与资产备份已独立恢复验证；
- 回滚镜像和操作步骤可执行。

## 16. 常见问题

### 容器反复重启

先检查 `docker compose ps` 和应用日志。常见原因是生产必填秘密为空、格式不合法、数据库
DSN 错误、数据库不是 fresh 基线或外部 HTTPS 配置无效。

### 页面能打开但登录失败

确认初始化 Migration 已执行、初始管理员变量在第一次启动前已经注入，并检查账号锁定与
Tenant 范围。不要通过直接改密码哈希绕过认证流程。

### 企业微信授权失败

检查公网 HTTPS、回调路径、Suite 配置和 `AGENT_DESK_WECOM_AUTH_TYPE` 阶段值。修改环境
变量后必须 force recreate，普通 restart 不会重新加载。

### 门店 AI 不可用

依次检查 Store Credential、active Model Profile 九槽、FastGPT Dataset 和统一 NewAPI
网关。任一必需槽失败都应保持不可用，不允许自动退回旧明文配置。
