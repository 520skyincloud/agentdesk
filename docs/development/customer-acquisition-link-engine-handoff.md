# 到店联动三 Provider 与静态会话绑定交接

> 状态：代码与自动化实现完成；静态模式真机闭环尚未验收
> 日期：2026-07-30
> 分支：`codex/customer-acquisition-link-engine`
> 基线：`weibao/main`

## 1. 目标

在现有 `customer_acquisition`、`contact_way` 上新增
`static_plugin_ticket`。静态模式首次扫码返回门店真实 `plugId`，客户主动添加真实员工
后，由员工号真实单聊发送一次性 `bindTicket` 小程序卡片完成精确绑定。旧 Provider 保留
数据兼容和显式回滚，三种模式不允许因错误自动降级。

本次不修改微信小程序、AI 回复引擎、企微员工号协议或客户身份模型。

## 2. 实现范围

- 增加 `ArrivalAcquisitionLink`，按授权主体、门店和客户联系成员唯一复用；
- 调用真实额度、创建链接、查询详情和客户分页接口；
- 创建请求固定为单成员 `range.user_list`，不混入员工号协议标识；
- 获客 URL 加密存储，官方 `link_id` 不复用旧 `config_id`；
- 每次扫码生成独立 HMAC `customer_channel`，同次重试稳定、不同扫码不同；
- 从真实链接生成标准二维码，艺术码逐字解码失败时回退标准码；
- 官方回调和五分钟补偿对账复用同一门店关系确认事务；
- 已绑定二次扫码继续复用原员工号投递、频控和失败状态；
- 管理页在现有到店连接列表展示 Provider、链接状态、额度与故障码；
- 新模型和新增外键关系进入 Tenant 完整性审计；
- 门店连接新增真实 `staticContactPlugId` 和员工实例映射，管理入口仍为原到店连接页；
- 增加 `ArrivalBindingTicket` 和 `ArrivalStoreBinding.BindingProofType=card_ticket`；
- 增加 `/api/miniprogram/arrival/bind`，固定对齐 `2062/2066-2071` 错误码；
- 新好友真实会话自动发绑定卡片，存量好友从原会话工作台人工发卡；
- Message/Outbox 只保存内部 ticket ID，发送前临时物化票据，失败信息持久化前脱敏；
- Provider 切换、连接停用撤销 pending 票据，维护任务过期超时票据；
- 静态模式解除 Suite、永久授权码、客户联系回调和官方创建链接 API 运行依赖。

## 3. 数据与接口

DDL 继续由 `AutoMigrate` 执行，无 DML migration。新增表：

```text
ArrivalAcquisitionLink
ArrivalBindingTicket
```

关键唯一约束：

```text
ArrivalAcquisitionLink:
  tenantAuthorizationId + storeId + contactMemberFingerprint
ArrivalBindingTicket:
  ticketHash
  tokenEntropyHash
```

敏感字段：

- 官方链接 URL 只保存 ciphertext + nonce；
- 客户、成员和扫码状态只保存现有密文或 HMAC 指纹；
- 绑定票据原文不落库、Message、Outbox、审计或普通日志；
- Provider 错误只保存阶段、HTTP 状态、错误码、清洗后短消息和重试属性。

原 bootstrap/status 契约保持不变，新增：

```text
POST /api/miniprogram/arrival/bind
POST /api/dashboard/arrival-connection/provider/update
GET  /api/dashboard/arrival-connection/protocol-instance/options
POST /api/dashboard/conversation/send_arrival_binding_card
```

bind 请求与成功数据：

```json
{
  "schemaVersion": "arrival_bind_input.v1",
  "loginCode": "fresh wx.login code",
  "bindTicket": "opaque ticket"
}
```

```json
{
  "schemaVersion": "arrival_bind_result.v1",
  "bindingStatus": "bound",
  "store": {
    "name": "门店名",
    "brandName": "品牌名",
    "address": "门店地址",
    "phone": "门店电话"
  }
}
```

外层保持统一 `JsonResult`；响应禁止返回票据、openid/unionid、客户标识、guid、
`conversation_id`、sessionToken 或 secret。

管理 DTO 扩展：

```text
contactProvider
acquisitionLinkStatus
acquisitionQuotaTotal
acquisitionQuotaBalance
acquisitionFailureCode
acquisitionLastVerifiedAt
staticContactPlugId
wxWorkProtocolInstanceId
```

## 4. 配置

Provider 必须显式配置。新静态主链：

```text
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=static_plugin_ticket
AGENT_DESK_ARRIVAL_BIND_TICKET_TTL_MINUTES=30
AGENT_DESK_ARRIVAL_BIND_PENDING_SCAN_WINDOW_MINUTES=30
```

允许值为：

```text
static_plugin_ticket
customer_acquisition
contact_way
```

生产环境必须显式设置，非法值会阻止配置加载。静态模式只要求 Arrival 公共 HTTPS、
小程序 AppID/AppSecret、会话/HMAC/数据加密密钥、门店真实 `scene + plugId` 和可用员工
实例；不要求 Suite 配置。另两种服务商模式仍要求完整 Suite 与回调配置。企业微信仍处于
安装测试阶段时 `AGENT_DESK_WECOM_AUTH_TYPE=1`，正式发布后改为 `0`，每次切换都必须
强制重建容器。

## 5. 三种 Provider 的运行差异

| Provider | 首次未绑定扫码 | 绑定证据 | Suite 运行依赖 |
| --- | --- | --- | --- |
| `static_plugin_ticket` | 返回本地 `plugin_button + plugId` | 真实会话 `card_ticket` | 无 |
| `customer_acquisition` | 创建/复用官方获客链接二维码 | provider callback/补偿对账，Stage B 仍需确定性桥 | 有 |
| `contact_way` | 调用旧 `add_contact_way` 返回二维码 | provider callback，Stage B 仍需确定性桥 | 有 |

静态模式不调用 `add_contact_way`、获客链接 API、客户联系回调或
`external_userid/unionid` 转换。服务商模式也不会自动切到静态模式。

## 6. 门店配置与客户流程

配置一个真实静态门店：

1. 在 `/dashboard/arrival-connections` 找到目标门店，不新建平行门店或连接；
2. 联系模式选择“静态联系我 + 卡片绑定”；
3. 录入企业微信后台生成的真实 `plugId`，禁止从二维码或员工 ID 推导；
4. 选择当前 Tenant、Store 下状态可用且已配置小程序卡片模板的员工实例；
5. 保存后连接为 active；若实例已被其他 active 静态门店使用则拒绝；
6. 小程序继续使用该连接既有的不透明 `scene`。

新好友：扫码返回 plugId，客户主动添加员工，真实会话出现后自动发独立绑定卡片，客户
点击并 bind 后建立 `card_ticket` 绑定。存量好友不会再产生新好友事件，只能在会话工作台
确认门店归属后点击“发送到店绑定卡片”；门店归属不确定时禁止批量猜测。

## 7. 错误与恢复

获客链固定错误码：

```text
acquisition_permission_denied
acquisition_quota_exhausted
acquisition_link_create_failed
acquisition_link_invalid
acquisition_link_verify_failed
acquisition_customer_sync_failed
acquisition_member_out_of_scope
acquisition_member_unavailable
```

单个获客链接最多三次创建尝试。数据库唯一索引和条件 claim 防止并发重复创建。系统不会
在权限、额度或创建失败时改走旧 Provider，也不会写入假链接或返回假二维码。
官方创建成功后先持久化真实 `link_id`，再读取详情并激活；详情校验暂时失败时，后续
重试只恢复同一官方链接，不会再次调用创建接口。

静态 bind 固定错误码：

```text
1000 格式或 schema
2062 wx.login code 无效
2066 票据无效
2067 票据过期
2068 票据撤销
2069 身份、门店或会话冲突
2070 缺少同身份同门店的近期扫码
2071 门店、员工实例或真实会话暂不可用
```

同一员工实例只能有一个 active 静态门店。保存时事务内锁 Store 和实例；运行中若发现
歧义，不猜测、不发卡。绑定卡片是显式标记的 system outbound 消息，异常中断后由现有
Outbox 补漏恢复；普通 system 消息不进入外部投递。

## 8. 权限

没有新增平行权限。管理操作继续复用：

```text
arrivalConnection.view
arrivalConnection.manage
arrivalConnection.invite
arrivalAudit.view
```

Tenant 与 Store 数据范围仍是强制上限。

## 9. 验证

自动化覆盖：

- 真实 Provider 请求方法、路径、单成员请求体和分页；
- 额度成功、`48002`、额度耗尽及空链接拒绝；
- 同门店复用、跨门店隔离和并发不重复创建；
- 同次扫码幂等、不同扫码状态不同及不透明状态格式；
- 标准/艺术二维码逐字解码和回退；
- 精确客户归因、`legacy_unmapped`、已绑定重扫、实例离线和频控；
- 敏感值不进入日志、错误或持久化诊断；
- SQLite AutoMigrate 和 Tenant 完整性审计；
- 静态模式无 Suite 配置、首次扫码 plugId 和服务商零调用；
- ticket HMAC/TTL、原文不落库、重复消费、跨身份/会话冲突及固定错误码；
- 新好友无欢迎内容时仍发绑定卡片，存量会话人工发卡；
- `card_ticket` 二次扫码只投递原真实会话，A/B 门店不串店；
- system 消息 Outbox 补漏和上游错误票据脱敏；
- Provider 切换/连接停用撤销和维护任务过期。

2026-07-30 本分支最终检查结果：

```text
gofmt -l <本次修改的 Go 文件>              通过（无输出）
go test ./...                              通过
go vet ./...                               通过
git diff --check                           通过
web: ./node_modules/.bin/tsc --noEmit      通过
web: ./node_modules/.bin/eslint .          通过（0 error，33 个项目既有 warning）
web: ./node_modules/.bin/next build --webpack
                                             通过（48 个页面）
SQLite Arrival AutoMigrate                 通过且二次执行幂等
敏感信息模式扫描                           未发现真实密钥或 bindTicket 明文落库
```

`pnpm typecheck` 在该临时工作树中被共享 `node_modules` 校验器阻止，并尝试联网重装依赖；
因此改用同一已安装依赖中的 `tsc --noEmit` 完成等价类型检查。默认 Turbopack 构建也会因
`node_modules` 符号链接指向工作树外而失败，最终使用项目支持的 Webpack 生产构建完成
验证。这两项均属于临时工作树依赖布局限制，不是 TypeScript 或页面构建错误。

MySQL 测试入口为：

```bash
TEST_MYSQL_DSN='<isolated test database dsn>' \
go test ./internal/bootstrap -run TestArrivalSchemaAutoMigrateMySQL -count=1
```

不得把未配置 `TEST_MYSQL_DSN` 的跳过结果描述为 MySQL 已验收。2026-07-30 获客助手版本
曾使用生产同版本 MySQL 8.4 隔离临时库执行 AutoMigrate，应用健康检查通过，
`t_arrival_acquisition_link` 的全部字段及
`tenant_authorization_id + store_id + contact_member_fingerprint` 唯一索引均已核对，
随后删除临时容器和临时库。新静态版本的 `t_arrival_binding_ticket` 尚未执行 MySQL 实机
迁移，因此不能沿用前次结果宣称新表已完成 MySQL 验收。

## 10. 并行分支影响

共享文件包括 models 注册、Arrival repository/service、配置、管理 DTO、页面、双语资源、
Compose 和 Tenant 完整性审计。合并其他分支时必须保留双方新增项，禁止整文件覆盖。

本次没有改变 AI Runtime、NewAPI、FastGPT、计费、行业意图、客户标签、派单、WebSocket
或员工号协议契约。后续 rebase 前仍应检查上述共享文件是否被 `main` 更新。

## 11. 生产验收

`customer_acquisition` 只有以下真实步骤完成后才能宣称闭环：

1. 测试企业重新授权并获得获客助手权限；
2. 额度预检成功；
3. 第一次小程序扫码显示真实可识别二维码；
4. 客户主动添加成员并形成精确门店关系；
5. 再次扫码不显示二维码，真实员工号会话收到到店卡片。

若 Stage B 的确定性身份桥仍不可用，应保持 `legacy_unmapped`，并明确记录第 5 步未完成。

2026-07-30 12:42:22（Asia/Shanghai）在最终生产镜像上执行了真实连接校验。结果为：

```text
authorizationOK=true
memberOK=true
instanceOK=true
providerMode=customer_acquisition
providerOK=false
errorCode=acquisition_permission_denied
quotaTotal=0
quotaBalance=0
```

额度为 `0` 仅表示官方权限预检被拒绝，不代表真实额度耗尽。系统没有创建获客链接、没有
生成二维码、没有切换旧 Provider，也没有把连接伪装为成功。当前测试企业必须重新授权
第三方应用，使已保存的“获客助手权限”进入企业授权范围；完成后再次执行“校验连接”，
只有返回成功并得到真实额度，才继续首次扫码、客户添加和二次扫码验收。因此上述真实
验收步骤 2 至 5 目前均未完成。

`static_plugin_ticket` 只有以下真实步骤完成后才能宣称闭环：

1. 在到店连接页为试点门店录入企业微信后台真实 `plugId`，并选择该门店唯一员工实例；
2. 使用从未添加该员工的真实微信首次扫码并点击官方联系我组件；
3. 确认员工号侧形成真实 Customer、Conversation 和 `S:` 单聊；
4. 点击员工号发出的小程序绑定卡片并确认 bind 返回 bound；
5. 第二次扫同一门店码，确认卡片只投递到第 3 步的原会话；
6. 验证存量好友用会话工作台人工发卡，`-3006` 不被视为已绑定；
7. 在 Suite 配置为空时重复静态链路，并完成 MySQL 新表实机验证。

当前未取得试点门店真实 `plugId` 并完成上述真机步骤，静态生产闭环未验收。

## 12. 回滚

运行回滚：

```text
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=<已验收的 customer_acquisition 或 contact_way>
```

修改仓库外生产环境文件后，以同一 Compose project 强制重建 `agent-desk`。代码回滚只回退
本功能提交；`ArrivalBindingTicket` 等新表与新增列保留，不执行破坏性 DDL。清理生产数据
需要独立审批和恢复验证。

本次部署前镜像固定为：

```text
mlogclub/agent-desk:rollback-20260730-1218
sha256:c1be7f35b2ef0cba7117f5ca153f74468636d726ee329fe0f980de6db4c05b7e
```

## 13. 已绑定员工号设备重新登录

2026-07-30 补回已绑定但离线实例的设备登录入口。该流程不是到店 `plugId`、获客链接或
客户“联系我”链路，也不会创建第二个员工身份：

1. 在现有员工号实例菜单点击“扫码重新登录”；
2. 离线实例先通过既有 `recover` 接口，使用当前实例 ID 和真实 GUID 调用官方
   `/client/restore_client`；
3. 后端继续使用同一实例 ID 和 GUID 调用 `/login/get_login_qrcode`；
4. 若协议暂时返回 `1014`，页面每三秒重试取码，最多等待 30 秒；
5. 二维码出现后，页面每三秒调用 `/login/check_login_qrcode`；
6. 协议返回 `QRCODE_REQUIRE_VERIFY(10)` 时展示数字确认码输入框；
7. 确认码通过 `/login/verify_login_qrcode` 提交；
8. 登录成功后同步当前实例资料并刷新列表。

后台 `POST /api/dashboard/wxwork-protocol-instance/login_qrcode` 的 `data` 从协议原始字符串
收敛为：

```json
{
  "qrcode": "<可展示的二维码>",
  "qrcodeContent": "<协议返回的二维码内容>"
}
```

仓库内原先没有该接口的页面调用者；初次绑定和远程开户仍使用各自现有接口。响应不再向
浏览器暴露协议原始响应或内部登录 key。生成二维码要求 `channel.update`，查询和提交确认码
仍执行当前 Tenant/Store 的实例访问校验。没有新增权限、模型、表、字段或 migration。

新增测试覆盖二维码响应裁剪、缺失二维码拒绝、离线实例入口、三秒轮询、状态 10 的确认码
输入与提交、离线实例恢复、登录器启动等待，以及复用当前实例而不创建新身份。恢复动作
严格复用现有 `/client/restore_client` 契约，不修改 GUID，不通过数据库伪造在线状态。
等待结束后仍返回 `1014` 时，页面明确要求先恢复该有效坐席的异地登录器。真机验收必须由
员工本人扫码并填写企微端显示的确认码；完成前不得宣称设备已重新在线。

生产镜像中的 Web 构建固定使用 `next build --webpack`。默认 Turbopack 在当前 4 GiB 部署
主机上会耗尽可用内存并长期停在编译阶段；Webpack 已在同一提交上完成 48 个页面的生产
构建验证。该调整只影响构建器选择，不改变页面路由或运行时接口契约。

## 14. 过期员工号错误脱敏与登录门禁

2026-07-30 修复已绑定员工号扫码入口把协议原始错误响应直接显示给浏览器的问题。根因是
`postJSON` 和 `postWECDNJSON` 在业务错误后拼接了完整 `raw`，同时扫码与恢复动作没有在
调用供应商前核对本地实例池有效期。因此真实 `9003` 会显示 `err_code/err_msg/data` 原始
JSON，已过期实例仍可重复发起无效请求。

当前规则为：

1. `9003` 统一返回“该企微员工号实例已过期，请先续费或更换有效实例”；
2. `1014` 保留安全错误码和“异地登录器未启动”的可操作提示，供现有启动重试逻辑识别；
3. 其他协议业务错误只返回错误码，HTTP 错误只返回状态码，不返回供应商消息或响应体；
4. 原始响应仅作为 service 内部返回值供既有 `1002/-102 offline` 实例池判别使用，不进入
   error，也不由 handler 返回给浏览器；
5. `GetLoginQRCode` 和 `RestoreClient` 在供应商调用前检查绑定设备池的 `ExpiredAt`、
   `SyncStatus` 和 `State`；确认过期时供应商调用次数为零；
6. 实例列表增加 `protocolExpiresAt`、`protocolExpired`、`loginAvailable` 和
   `loginUnavailableReason` 展示字段；过期实例显示红色“实例已过期”，不再显示
   “扫码重新登录”，仍保留已有“更换登录员工号”路径；
7. 不修改 GUID、员工身份、实例绑定、在线状态或数据库事实，不通过写库伪造续费或可用。

本次没有新增表、字段、权限、路由或 migration，不影响小程序、到店联动、AI 回复引擎、
企微第三方应用授权及消息协议。DTO 仅向后兼容新增展示字段；并行分支合并时应保留
`WxWorkProtocolInstanceResponse` 和 `web/lib/api/admin.ts` 的四个字段，禁止整文件覆盖。

发布前验证：

```text
企业微信员工号协议文档                         已核对恢复实例、获取/检查登录二维码契约
新增错误脱敏与过期门禁 service 测试           通过
全部企微员工号相关 service 测试                通过
go test ./internal/services/...                通过
go test ./internal/handlers/dashboard/...      通过
go test ./... -count=1                         通过
Web 设备登录 Node 回归测试                     5/5 通过
web: ./node_modules/.bin/tsc --noEmit          通过
git diff --check                               通过
```

回滚只需切换到本次部署前镜像并强制重建 `agent-desk`；新增 DTO 字段不落库，无 DDL 回滚。
回滚后原始错误泄露和过期实例重复扫码问题会重新出现，因此不得长期回滚到旧镜像。

### 14.1 生产部署与真实复验

2026-07-31 已将本节修复部署到 `https://weibao.omnireva.com`。部署基线与回滚证据如下：

```text
代码提交：e8fdf355359c2899d865901b7aab01b2c4869149
源码归档 SHA-256：5f95f2be991ce3834a96f253c92502adc002049a52914789592ed0b53d4d4fbc
发布目录：/opt/agentdesk/releases/20260730-2356-wxwork-expiry-guard/app
部署前备份：/opt/agentdesk/backups/20260730-2356-wxwork-expiry-guard
回滚镜像：mlogclub/agent-desk:rollback-20260730-2356-wxwork-expiry-guard
新镜像：sha256:3de4e968bba6e5d5fe4c771419ea13f69ac5e1b7c06ad85f277b7470d4802d03
镜像创建时间：2026-07-30 23:58:53 +08:00
应用容器启动时间：2026-07-31 00:01:41 +08:00
```

部署时原子切换 `/opt/agentdesk/current`，使用 `--force-recreate --no-deps agent-desk`
只重建应用容器；MySQL 容器未重启，部署后应用和 MySQL 均为 `healthy`。公网员工号页面
返回 HTTP 200，启动日志未发现 migration、数据库连接或应用启动错误。

使用生产管理员登录“丽斯未来”后刷新 `/dashboard/wxwork-protocol-instances/`，真实复验
结果为：

1. “黄奇峰 / 合肥南七”显示红色“实例已过期”；
2. 展示安全提示“该企微员工号实例已过期，请先续费或更换有效实例”；
3. 操作菜单只有“接待人设”“欢迎语设置”“更换登录员工号”和“删除”，不再出现
   “扫码重新登录”；
4. 页面未再显示 `err_code/err_msg/data`、`raw` 或完整供应商响应；
5. 既有 GUID、门店绑定、员工身份、AI 托管状态和在线事实均未改写。

本修复不会把已过期供应商实例伪装成可登录状态。该实例恢复使用仍需在实例提供方完成
续费，或者通过现有“更换登录员工号”流程绑定有效实例。

## 15. 企微员工号直接二维码登录收敛

2026-07-31 以 `https://wework.apifox.cn/llms.txt` 及其链接的登录接口为唯一协议依据，
对照 `origin/wxwork-protocol-agentdesk` 后确认：第 13 节记录的“先
`/client/restore_client`、等待异地登录器、再重试取码”属于已废止历史实现，不再是当前
产品链路。当前三个登录入口统一直接调用：

```text
POST /login/get_login_qrcode
  -> 每 3 秒 POST /login/check_login_qrcode
  -> 仅 status=10 时展示确认码输入
  -> POST /login/verify_login_qrcode
```

获取二维码请求只包含 `guid` 和布尔 `verify_login=false`；检查请求只包含 `guid`；确认码
请求只包含 `guid` 和 `code`。页面不收集代理，登录请求不携带 `proxy`、`bridge`、
`restore` 或其他未在接口文档声明的字段。独立的 `set_proxy`、`restore_client` 运维动作
不属于登录状态机，不能作为扫码前置条件。

生产数据中存在一个旧流程遗留的 `disabled + recovering` 替换草稿。该记录的
`ReplacesInstanceID` 指向已过期旧员工实例，因此必须继续通过现有“更换登录员工号”生成
的替换绑定页完成，不能伪装成从未绑定过员工号的首次绑定。替换页复用原草稿和 GUID，
直接获取二维码；供应商接受取码请求后把草稿生命周期收敛为 `login_qrcode`。只有协议检查
真实返回成功后，页面才同步员工资料；原实例的停用与 `ReplacedByInstanceID` 写入仍由现有
替换完成事务处理。

首次绑定入口继续只允许没有已绑定员工实例的系统账号，或恢复自身
`HealthStatus=login_qrcode` 且没有 `EmployeeUserID` 的首次绑定草稿。不得把
`recovering` 替换草稿放进首次绑定选择器，否则同一 `StoreStaffBinding` 的旧实例与替换
实例会形成两个互相冲突的入口。

新增 service 测试覆盖取码成功后的 `login_qrcode` 生命周期；既有契约测试继续覆盖三个
入口均不传代理、每 3 秒轮询及仅状态 `10` 展示确认码。本次没有新增 model、migration、
DTO、enum、权限、路由或 WebSocket 契约，不修改 AI、计费、小程序、企业微信第三方应用
授权或消息协议。

验证结果：

```text
go test ./...                                  通过
前端 Node 契约测试                            169/169 通过
pnpm typecheck                                 通过
pnpm lint                                      0 error，33 条既有 warning
pnpm build                                     通过，48 个页面
git diff --check                               通过
```

最终 commit `3ecb6093fd0ce1e80c5bd1383cffd8f44678badb` 已部署到
`/opt/agentdesk/releases/20260731-1245-wxwork-direct-login-final/app`，生产镜像为
`sha256:bdedcf14030b5b6b9e3f9b8f72a363a88d08ea8f4087ff74bc72f6332727de07`。
真实替换绑定请求已返回非空二维码，首次检查为 `pending / statusCode=0 / 等待扫码`。
状态 `10` 确认码和登录成功仍待员工本人扫码触发；完成前不得把实例标记为已登录。

## 16. 系统出站小程序卡片方向展示修复

2026-07-31 核对生产消息、Outbox 和员工号消息引用后确认：到店联动小程序卡片在数据库中
始终是 `sender_type=system` 的系统出站消息，Outbox 与员工号消息引用也分别是 `sent` 和
`direction=out`。错误只发生在会话工作台前端：页面仅把 `agent`、`ai` 视为客服侧，
导致 `system` 落入客户侧分支，显示客户头像和“客户”徽标。

当前展示规则调整为：

- `customer` 继续显示在客户侧；
- `agent`、`ai`、`system` 显示在客服侧；
- `system` 名称显示“到店联动”，徽标显示“系统”，并展示真实发送状态；
- 居中的既有会话状态事件继续走原专用分支，不受本次调整影响。

本次不修改历史消息、发送协议、小程序、AI 回复、DTO、枚举、API、WebSocket payload、
model 或 migration。历史系统卡片的数据库方向本来正确，部署后刷新页面即可按新规则显示。
新增页面契约测试防止 `system` 再次落入客户展示分支；会话页面 12 项 Node 测试、
`pnpm typecheck`、`pnpm build` 和 `git diff --check` 均通过。

回滚只需回退本次前端提交并重新构建应用镜像。回滚不会改变消息事实，但系统出站卡片会
再次被错误显示在客户侧。
