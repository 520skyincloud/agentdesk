# 企业微信获客助手到店链接引擎交接

> 状态：代码实现完成，生产真实验收结果以部署记录为准
> 日期：2026-07-30
> 分支：`codex/customer-acquisition-link-engine`
> 基线：`weibao/main`

## 1. 目标

将到店联动的生产二维码主链从企业微信 `add_contact_way` 切换为获客助手，同时保持
`arrival_scan_input.v1` 与 `arrival_scan_result.v2` 小程序契约不变。旧 Provider 保留
数据兼容和显式回滚，不允许因错误自动降级。

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
- 新模型和三条外键关系进入 Tenant 完整性审计。

## 3. 数据与接口

DDL 继续由 `AutoMigrate` 执行，无 DML migration。新增表：

```text
ArrivalAcquisitionLink
```

唯一约束：

```text
tenantAuthorizationId + storeId + contactMemberFingerprint
```

敏感字段：

- 官方链接 URL 只保存 ciphertext + nonce；
- 客户、成员和扫码状态只保存现有密文或 HMAC 指纹；
- Provider 错误只保存阶段、HTTP 状态、错误码、清洗后短消息和重试属性。

小程序公开接口、字段和枚举没有变化。管理接口只扩展内部 DTO：

```text
contactProvider
acquisitionLinkStatus
acquisitionQuotaTotal
acquisitionQuotaBalance
acquisitionFailureCode
acquisitionLastVerifiedAt
```

## 4. 配置

新增非秘密环境变量：

```text
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=customer_acquisition
```

允许值仅为：

```text
customer_acquisition
contact_way
```

生产环境必须显式设置，非法值会阻止配置加载。企业微信仍处于安装测试阶段时
`AGENT_DESK_WECOM_AUTH_TYPE=1`，正式发布后改为 `0`，每次切换都必须强制重建容器。

## 5. 错误与恢复

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

## 6. 权限

没有新增平行权限。管理操作继续复用：

```text
arrivalConnection.view
arrivalConnection.manage
arrivalConnection.invite
arrivalAudit.view
```

Tenant 与 Store 数据范围仍是强制上限。

## 7. 验证

自动化覆盖：

- 真实 Provider 请求方法、路径、单成员请求体和分页；
- 额度成功、`48002`、额度耗尽及空链接拒绝；
- 同门店复用、跨门店隔离和并发不重复创建；
- 同次扫码幂等、不同扫码状态不同及不透明状态格式；
- 标准/艺术二维码逐字解码和回退；
- 精确客户归因、`legacy_unmapped`、已绑定重扫、实例离线和频控；
- 敏感值不进入日志、错误或持久化诊断；
- SQLite AutoMigrate 和 Tenant 完整性审计。

MySQL 测试入口为：

```bash
TEST_MYSQL_DSN='<isolated test database dsn>' \
go test ./internal/bootstrap -run TestArrivalSchemaAutoMigrateMySQL -count=1
```

不得把未配置 `TEST_MYSQL_DSN` 的跳过结果描述为 MySQL 已验收。2026-07-30 部署前另使用
生产同版本 MySQL 8.4 创建隔离临时库，并让最终镜像真实启动执行 AutoMigrate。应用健康
检查通过，`t_arrival_acquisition_link` 的全部字段及
`tenant_authorization_id + store_id + contact_member_fingerprint` 唯一索引均已核对，
随后删除临时容器和临时库。

## 8. 并行分支影响

共享文件包括 models 注册、Arrival repository/service、配置、管理 DTO、页面、双语资源、
Compose 和 Tenant 完整性审计。合并其他分支时必须保留双方新增项，禁止整文件覆盖。

本次没有改变 AI Runtime、NewAPI、FastGPT、计费、行业意图、客户标签、派单、WebSocket
或员工号协议契约。后续 rebase 前仍应检查上述共享文件是否被 `main` 更新。

## 9. 生产验收

只有以下真实步骤完成后才能宣称闭环：

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

## 10. 回滚

运行回滚：

```text
AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=contact_way
```

修改仓库外生产环境文件后，以同一 Compose project 强制重建 `agent-desk`。代码回滚只回退
本功能提交；新表与新增列保留，不执行破坏性 DDL。清理生产数据需要独立审批和恢复验证。

本次部署前镜像固定为：

```text
mlogclub/agent-desk:rollback-20260730-1218
sha256:c1be7f35b2ef0cba7117f5ca153f74468636d726ee329fe0f980de6db4c05b7e
```
