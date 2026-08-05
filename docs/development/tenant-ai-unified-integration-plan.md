# Tenant、门店、AI 与会话连续性统一集成方案

> 状态：当前唯一权威方案，更新于 2026-08-01。
>
> 代码事实优先级：真实运行链与测试 > 本文 > 历史 handoff。若三者不一致，停止发布，先修正代码或本文。历史实施过程由 Git 追溯，不再把过期决定堆叠在本文末尾。

## 1. 目标与分支

- 唯一实施分支：`codex/tenant-ai-unified-integration`。
- 发布仓库：`520skyincloud/weibao`。
- 租户、客服、派单和审计来源：`origin/codex/customer-audit@c706815`。
- AI、模型和计费行为来源：`origin/codex/ai-billing@4db7993`。
- 两个来源分支只用于逐符号审计，不整体 merge、不整文件覆盖、不恢复平行事实源。
- 最终只保留一套 Tenant、Store、客户、会话、模型、凭据、知识、标签、派单和权限运行链。

## 2. 冻结术语

| 对象 | 唯一语义 | 明确不是 |
| --- | --- | --- |
| Tenant | 接入公司和隔离根 | 客户公司下的门店层级 |
| Store | Tenant 内长期稳定的独立门店业务身份 | 员工账号的展示标签 |
| StoreStaffBinding | 一个系统门店员工账号占用一个 Store 的活动关系 | 企微 UserID 或登录实例 |
| WxWorkProtocolInstance | 一次真实企微员工号协议登录及运行状态 | Store 或系统账号 |
| Customer | Tenant 内客户主档 | Store 私有客户副本 |
| StoreCustomerRelation | 同一客户在某 Store 的独立记忆、标签和活跃关系 | 第二套客户身份 |
| Conversation | 一个客户与一个 StoreStaffBinding 的逻辑会话 | 单次企微登录连接 |
| ConversationChannelSession | Conversation 内由某企微实例承接的不可变消息段 | 新客户或新逻辑会话 |
| ConversationContinuityLink | 不同 Binding 之间经人工确认建立的线性继承关系 | 消息搬迁或自动合并 |

核心关系：

```text
Tenant
  -> Store
    -> StoreStaffBinding (一店多账号)
      -> WxWorkProtocolInstance replacement chain

Tenant
  -> Customer
    -> StoreCustomerRelation
      -> Conversation per StoreStaffBinding
        -> ConversationChannelSession per active protocol instance
```

## 3. 门店与门店员工号

### 3.1 Store 独立存在

- Store 必须通过门店管理独立创建，拥有稳定 ID、StoreCode、名称、地址、导航、联系方式和知识库引用。
- 创建、邀请或审核门店员工账号时必须选择已有 Store；不得因为赋予 `store_staff` 角色而隐式创建 Store。
- 一个 Store 可以没有员工、绑定一个员工或同时绑定多个员工。
- 一个系统 User 同时最多占用一个活动 StoreStaffBinding；数据库使用可空 `ActiveUserID` 唯一约束，停用历史不占用该唯一键。
- 同一 User 转店时先停用旧 Binding，再在目标 Store 建立新 Binding；不得改写有历史会话的 Binding.StoreID。

### 3.2 生命周期

- 停用 User 或移除 `store_staff` 角色时，原子停用活动 Binding、相关企微实例和 AI 回复，不删除历史。
- Store 停用后停止新消息、AI、派单、知识和到店运行，但客户、会话、标签、账单和审计继续可读。
- 新员工不得接管离职员工的登录账号。应创建新 User 和 Binding，再按第 5 节安排会话继承。
- Store、Binding、实例、会话和消息不因人员变化物理删除。

## 4. 企微实例与替换

### 4.1 当前实例

一个 Binding 最多只有一个“已激活当前实例”。统一判定为：

```text
status = enabled
AND replaced_by_instance_id = 0
AND (replaces_instance_id = 0 OR remote_setup_submitted_at IS NOT NULL)
```

- `replaces_instance_id > 0` 且 `remote_setup_submitted_at IS NULL` 是替换草稿，不得参与会话、发送、欢迎语、联系人自动化、到店连接、知识校验、派单或 readiness。
- 登录二维码、登录资料、邮箱验证和替换配置流程可以操作草稿。
- 运行时找出零个或多个已激活当前实例时失败关闭，不猜选最大 ID。

### 4.2 替换流程

```text
创建替换草稿
  -> 获取真实二维码并扫码
  -> 完成协议登录
  -> 完成邮箱/远程设置验证
  -> 同事务归档旧实例并激活新实例
```

- 草稿完成验证前旧实例仍是当前事实；不得由扫码状态提前切换。
- 激活后写 `ReplacesInstanceID/ReplacedByInstanceID/ReplacedAt`，旧实例只接收并归档已认证的迟到消息。
- 旧实例迟到消息写回原 Conversation、原 SessionNo，并标记 `HistoricalOnly`；不得改变未读、最后消息、派单、AI、客户关系或当前路由。
- 不直接写数据库伪造扫码、验证、替换或在线成功。

## 5. 会话连续性

### 5.1 唯一键

门店企微逻辑会话唯一键固定为：

```text
TenantID + StoreID + CustomerID + StoreStaffBindingID
```

`ThreadKey` 的稳定格式为：

```text
store:<tenantId>:<storeId>:<customerId>:<storeStaffBindingId>
```

因此：

- 同店、同客户、不同 Binding 是两条独立 Conversation。
- 同 Binding 更换企微实例仍是同一 Conversation。
- 同客户进入不同 Store 时是不同门店关系和不同会话范围。

### 5.2 同 Binding 换企微

- 新实例收到该客户首条真实消息后复用原 Conversation，并新建 ConversationChannelSession。
- 旧段结束，新段 `StartReason=instance_changed`。
- UI 在新段前显示“以上为历史消息，已更换企微账号”，并展示原门店员工号、企微显示名和时间。
- 出站必须等待新实例获得自己的协议 `conversation_id`；严禁复用旧实例 conversation_id。

### 5.3 不同 Binding 同时服务

- 会话列表显示两条独立会话，各自具有路由、未读、派单、发送和账单归因。
- “同店同客相关会话”只提供关系提示和受权限控制的只读跳转，不自动继承、不合并统计。

### 5.4 Binding 也更换

- 客服组长或公司主管使用 `conversation.inherit` 预览并选择源会话、目标 Binding、目标已激活实例和原因。
- 目标 ThreadKey 已存在时连接既有目标会话；不存在时创建新 Conversation。
- 写入 ConversationContinuityLink，保持每条会话最多一个前序和一个后续，禁止循环、分叉、跨 Tenant、跨 Store 或跨 Customer。
- 原消息不移动、不复制、不改 ConversationID；目标页面将前序链作为 `InheritedHistory/HistoricalOnly` 只读展示。
- UI 显示“以上为历史消息，已由主管安排会话继承”。
- 预览版本包含 Binding、实例、会话及其更新时间；执行时重新加锁校验，防止过期批量操作。

### 5.5 消息、上下文和统计

- Message 固化 ConversationID、SessionNo、发送者、协议引用和时间；历史来源不得从当前路由反推。
- 当前会话的发送、撤回、未读和最后消息只处理当前物理消息。
- AI 使用当前会话窗口、既有摘要和 StoreCustomerRelation 记忆；人工继承不重复运行标签或知识进化。
- 运营、质检和账单按原物理会话/消息计数一次；继承展示不产生重复事实。

## 6. 客户身份与门店记忆

- Customer 是 Tenant 内唯一客户主档，CustomerIdentity 只保存协议或渠道外部身份映射。
- 外部身份唯一键为 `TenantID + ExternalSource + ExternalID`，不使用 Store 或实例作为第二套客户主键。
- 仅使用协议文档明确的稳定标识自动解析；姓名、头像、guid、conversation_id 和员工号字段不得用于猜测同一客户。
- 无法证明同一客户时使用既有 `conversation.linkCustomer` 人工关联能力，并写审计。
- StoreCustomerRelation 是 `TenantID + StoreID + CustomerID` 的门店上下文，持有稳定备注、活跃信息和门店标签。
- 同一客户在不同 Store 的标签、记忆、会话和知识上下文相互隔离。

## 7. 行业、标签与知识

### 7.1 行业和意图

- Tenant 强制绑定一个行业 IntentProfile；Store、Binding 和实例不能覆盖行业。
- 行业决定意图分类、固定 SemanticKey 标签目录、Prompt/Schema 和适用场景。
- 租户可以启停行业标签和设置显示别名，不得修改稳定 SemanticKey 或物理删除平台定义。

### 7.2 客户标签

- 不保留 ConversationTag。客户标签唯一事实为 StoreCustomerRelation 下的 CustomerTagRelation。
- 同一客户每 Store 最多六个活动标签。
- 标签来源只能是当前行业目录；AI 不得创造目录外标签。
- 标签演化和回复标签上下文是两个独立 Store 开关，可灰度和立即回滚。
- 每次变化写 append-only 证据，继承历史和重复回调不得重复演化。

### 7.3 FastGPT 与知识进化

- Store.KnowledgeBaseID 是门店知识库唯一事实源，同店所有 Binding 共享知识。
- FastGPT Team、Dataset、Collection 均以稳定 Store 身份隔离。
- 知识进化候选、证据、审核和发布属于 Store；更换 User、Binding 或企微实例不丢失。
- 检索必须同时校验 Tenant、Store、KnowledgeBase、模型 Profile、Binding 凭据及 applied revision。
- FastGPT 不提供第二套 Profile 编辑入口，也不回退旧本地 FAQ/Chunk 或公共 Dataset。

## 8. 模型、凭据与计费

### 8.1 模型方案

- ModelProfileTemplate/Slot 是平台发布的不可变配置版本。
- StoreModelProfileAssignment 是 Store 到 Profile 的唯一绑定，同店员工使用一致模型行为。
- 租户和门店只看到模型名、revision、槽状态和 readiness，不看到 Provider、BaseURL、Prompt、Schema 或密钥。
- 运行时只有 ModelCallResolver 可以解析模型，不存在 AIConfig、TenantAIModelGrant、StoreAIModelSetting 或实例级模型覆盖。

### 8.2 凭据

- StoreModelCredential 唯一键为 `TenantID + StoreID + StoreStaffBindingID`；一店多 Binding 时各自独立录入、审批、测试和轮换。
- StoreCredentialPolicy 仍属于 Store，统一控制是否允许该店员工自助录入和是否要求异人主管审批。
- API Key 只以 AES-256-GCM 密文保存，AAD 包含 Tenant、Store、Binding 和 revision；页面只显示掩码状态和末六位指纹。
- 候选 revision 全部测试并完成 FastGPT 同步后才原子激活；失败继续使用旧 active revision。
- 凭据操作要求密码复核、二次确认和不可修改审计；审批人不得是提交人。

### 8.3 计费

- 每次 AIUsageEvent 和 AIUsageGatewayCall 固化 TenantID、StoreID、StoreStaffBindingID、WxWorkInstanceID、ConversationID、模型、用途槽、revision、request ID 和人民币口径。
- 门店员工只查看自己的 Binding；公司主管查看 Tenant 聚合及 Store/Binding 明细；平台管理员可跨 Tenant。
- 本期只做 NewAPI 查询、内部归因和对账，不做充值、扣费、套餐、发票或额度强制拦截。

## 9. 派单、到店联动与权限

### 9.1 规则派单

- StoreStaffBinding.AgentTeamID 是门店员工所属综合客服组事实源，实例上的 AgentTeamID 只是同步缓存。
- AI 只判断是否需要人工，不选择客服。
- 派单继续依据客服组、小组、排班、Presence、容量、公平债务、SLA 和恢复规则。
- 历史真实接待人只能在公平带内获得连续性优先，不能绕过排班、在线、容量和权限。

### 9.2 到店联动

- StoreArrivalConnection 一店一连接，但必须显式保存选中的 StoreStaffBindingID 和当前已激活 WxWorkProtocolInstanceID。
- 企业微信官方客户联系成员 ID 与员工号协议登录 ID 属于不同命名空间，只允许管理员人工确认映射，不做字符串或姓名匹配。
- Store 稳定地址、导航、知识和小程序配置不因员工或企微替换丢失。

### 9.3 权限

- `store.view/create/update` 管理独立 Store。
- `conversation.relatedView` 查看同店同客相关会话和继承链。
- `conversation.inherit` 安排 Binding 更换后的会话继承。
- `conversation.linkCustomer` 处理无法自动证明的客户身份关联。
- 公司主管和客服组长默认拥有相关查看与继承权限；普通角色可授权。
- Permission 只决定操作资格，Tenant、Store、客服组和 Binding 数据范围始终是强制上限。

## 10. 页面信息架构

- 门店管理：独立 CRUD、地址、启停和门店基础事实。
- 用户管理：创建/邀请/审核账号，分配角色，并通过 Store 下拉选择已有门店。
- 企微员工号：按 Store 和 Binding 展示当前实例、替换草稿和历史链。
- 门店工作台：门店员工只管理自己 Binding 允许的门店资料、凭据和运行状态。
- 会话工作台：展示 Store、门店员工号、企微服务段、相关会话、继承链和只读历史。
- 知识库：按 Store 管理 FastGPT Dataset/Collection。
- 模型与账单：Store 选择 Profile，Binding 管凭据和明细。
- 不新增 Company、旧模型授权、会话标签或第二套员工账号入口。

## 11. Schema 与 Migration 72

### 11.1 AutoMigrate

新增或扩展：

- StoreStaffBinding 非唯一 Store 索引及 ActiveUserID 唯一活动占用。
- Conversation.StoreID、StoreStaffBindingID、ThreadKey。
- ConversationChannelSession、ConversationContinuityLink。
- StoreModelCredential、AI Usage、FastGPT 任务和到店记录的 StoreStaffBindingID。
- WxWorkCustomerHandoffSetting 改为 Customer + Binding 范围。

启动顺序固定为：

```text
校验并移除已知旧索引
  -> AutoMigrate
  -> 校验当前索引
  -> DML Migration 72
  -> Tenant 完整性审计
```

### 11.2 DML 回填

Migration 72：

- 依据实例、会话、消息、凭据 revision、FastGPT applied 状态等证据回填 Binding 归属。
- 为门店协议会话生成 ThreadKey、路由和初始 ConversationChannelSession。
- 将客户自动转人工偏好从实例范围收敛到 Binding。
- 回填到店连接、链接、票据和客户关系的 Binding 归属。
- 未配置的旧空 Credential 可按活动 Binding 扩展；已有密钥或账单事实必须唯一解析，不能猜选。
- ThreadKey 冲突、跨 Tenant/Store、多个当前实例、模糊 Credential 或缺失父对象时阻止迁移并要求修复。
- 不硬编码来源 Store ID，也不根据名称生成身份。

### 11.3 数据库支持

- 同一实现支持 SQLite 与 MySQL 8.4。
- 允许 fresh 初始化，也允许具有已知 Migration 历史的受控升级。
- 未知索引定义、未知 Migration 历史或不满足完整性契约的数据库拒绝启动。

## 12. 验证矩阵

固定工程验证：

```bash
gofmt -w <modified-go-files>
go test ./internal/services -count=1
go test ./internal/repositories ./internal/bootstrap ./internal/migration ./internal/models -count=1
go test ./... -count=1
go vet ./...
pnpm --dir web typecheck
pnpm --dir web lint
pnpm --dir web test:contracts
pnpm --dir web build
```

必须覆盖：

- 一 Store 多 Binding、一个 User 最多一个活动 Binding。
- 同 Binding 换企微形成新服务段并保留同一 Conversation。
- 未验证替换草稿不能抢会话、到店候选、自动化批次或 readiness。
- 同店同客不同 Binding 是独立且相关的会话。
- 人工继承创建/连接目标会话，拒绝跨店、跨租户、循环、过期预览和无权限操作。
- 历史消息只读、无重复未读、AI、标签、知识、派单和账单。
- Store 级知识/标签/记忆保留，Binding 级凭据/用量隔离。
- SQLite/MySQL Migration 72 成功与所有模糊数据失败关闭。

## 13. 生产发布

### 13.1 B13 前置门禁

1. 固定目标 commit 和镜像摘要。
2. 停止写入、worker 和定时任务。
3. 生成仓库外加密数据库及资产备份，记录 SHA-256，并下载离开生产主机。
4. 在独立 MySQL 恢复备份，运行新镜像和 Migration 72。
5. 对比 Tenant、Store、Binding、实例、Customer、Conversation、Message、Usage 和 Arrival 数量及关键关系。
6. 所有模糊数据先修复来源，再重新备份恢复；不得在生产迁移中猜选。
7. 使用 `--force-recreate` 部署，确认环境变量、Migration、健康、重启次数和日志。
8. 真实验收登录、Store CRUD、一店多账号、企微入站/出站、替换段、关联/继承、规则派单、FastGPT、标签、账单和到店联动。

### 13.2 B14 旧 Schema 清理

- 业务已批准旧 AIConfig、Grant、StoreSetting、ConversationTag 等退出，但物理删除只能在 B13 全部通过、正式停机、加密备份和独立恢复验证后单独执行。
- 普通应用启动和 Migration 72 不执行 B14，不携带通用删表工具。
- 如目标生产库仍存在旧对象，必须先生成只读清单并与已批准固定白名单逐项复核；不得扩大对象范围。
- B14 后再次独立恢复、启动和全链验收。任一前置缺失时保持旧对象不被运行链读取，但不删除。

### 13.3 回滚

- B14 前可回滚应用镜像；新增字段和表保留，旧应用能否忽略必须在恢复库验证。
- Migration 72 已重写的数据不得手工逆向；需要回到发布前状态时恢复整库备份。
- 凭据主密钥、FastGPT Token、企微秘密和数据库必须作为同一发布代际恢复。
- 不通过直接写数据库伪造会话、实例、授权、凭据或 readiness。

## 14. 并行分支影响

- 共享修改包含 models、AutoMigrate、Migration 72、DTO、权限、路由、会话/消息 service、WebSocket、`web/lib/api`、导航和多语言。
- `ai-billing` 的模型、凭据、FastGPT、Usage 行为已按 Binding 归属吸收；不得再 cherry-pick 同文件提交。
- `customer-audit` 的 Tenant、客服组、规则派单、权限和运营行为已按 Store/Binding/Conversation 新契约适配；不得回写来源分支形成第二套事实。
- 合并顺序：统一分支完整验证并发布后，后续工作只基于 `weibao/main`；两个来源分支冻结归档。

## 15. 完成判定

只有同时满足以下证据才可称为完成：

- 本文与真实代码、页面、Migration 和部署手册一致。
- 全量 Go、Web、SQLite 和 MySQL 验证通过。
- GitHub 上目标 commit 可追溯且无未提交交付代码。
- 生产加密备份已在独立 MySQL 恢复并运行 Migration 72。
- 新镜像健康，真实企微、会话、派单、知识、标签、账单和到店链路通过。
- 日志无秘密、客户身份原文、panic、fatal 或持续重试。
- B14 若执行，具备单独审批、固定清单和二次恢复证据。

## 16. 当前实施记录

### 2026-08-01 门店与会话连续性收敛

- 目标：从一店一账号旧投影升级为稳定 Store、一店多 Binding、企微实例可替换、同 Binding 自动分段、不同 Binding 人工继承。
- 主要文件：Store/Binding/Conversation/Instance models 与 repositories；Conversation、Message、WxWork、Arrival、FastGPT、Credential、Billing、Dispatch services；Migration 72；门店、用户、会话和企微页面。
- 数据变化：新增 ThreadKey、ConversationChannelSession、ConversationContinuityLink 和 Binding 级归因；旧消息不搬迁。
- 权限：新增 Store CRUD、相关会话查看和会话继承，默认公司主管/客服组长，数据范围继续硬限制。
- 并行影响：同时触及 `ai-billing` 与 `customer-audit` 的共享契约，统一分支是唯一后续基线。
- 本轮额外修复：未完成验证的替换草稿不再进入到店实例候选、联系人扫描批次、AI 行业切换计数或入站会话创建；已完成替换的测试夹具显式写入验证时间。
- 已验证：服务层全量测试通过；最终发布仍须完成本文第 12、13 节全矩阵与生产证据。

### 2026-08-01 合肥南七已有 FastGPT 数据集受控接入

- 目标：将 FastGPT 中已经归属合肥南七 Store managed Team 的既有数据集接入 AgentDesk，不复制数据集，不直接写数据库伪造状态，也不受当前 ASR 槽缺失阻塞。
- API：在现有知识库资源增加 `POST /api/dashboard/knowledge-base/fastgpt/adopt`，沿用知识库创建权限和 ActiveTenant/Store 数据范围；请求必须提供 Store、Dataset、准确名称和验收问题。
- 远端门禁：通过统一 FastGPT Integration Token 和 Store scope 依次校验 Dataset 归属与名称、非空且完成索引的集合、`configured/ready` Profile，以及指定问题至少一个真实检索命中。
- 本地事务：锁定 Store，幂等创建或复用 KnowledgeBase，更新 Store 唯一知识库引用和该 Store 的 ConversationRouteState，并创建唯一、已完成的 `adopt_dataset` 持久任务；任一步失败全部回滚。
- 生产验收约束：FastGPT 返回的多槽 Profile fingerprint 长度不固定，接入前统一规范化为 64 位 SHA-256 后再持久化；该值只用于变更比较，不保存原始槽指纹，也不需要扩大数据库列宽。
- 运行边界：接入只证明已有知识可浏览和检索，不写 StoreModelCredential、模型 Assignment 或 FastGPT applied revision，不把本地 AI 回复运行时标记为 ready。
- 数据与共享契约：新增向后兼容的 request/response DTO 和显式路由；无 model、AutoMigrate、DML migration、enum、WebSocket payload、计费、ASR、企微协议或前端页面变化。
- 测试：覆盖成功绑定、重复收敛、名称不一致、空集合、索引未完成、Profile 不可用、无检索命中和跨 Store Dataset 拒绝，并确认失败不产生本地写入、成功不伪造模型凭据状态。
- 并行影响：`origin/codex/ai-billing@4db7993` 在这些 FastGPT 文件上存在历史差异，但其语义已吸收进统一分支，不得整文件覆盖；`origin/codex/customer-audit@c706815` 无本次同文件改动。发布顺序仍以本统一分支提交为唯一基线。
- 回滚：部署前可回滚本提交；已经完成真实接入后，回滚代码不会删除 KnowledgeBase 或远端 Dataset。需要解除绑定时必须走现有受权限保护的知识库删除/停用流程，禁止手工删表或改 Store 外键。
- 工程验证：`go test ./internal/services/... ./internal/handlers/dashboard/...`、`go test ./internal/bootstrap/...`、`go test ./...` 和 `git diff --check` 全部通过。
- 生产验收（2026-08-01）：运行提交 `10ba7db`，release 为 `/opt/agentdesk/releases/20260801-181002-nanqi-fingerprint/app`，镜像摘要为 `sha256:7b3064f2498d3abc498c8e9d205e18b15f637c1a2d6b8fcecbd761539b8a2ea2`。合肥南七店接入成功，远端返回 1 个集合、20089 条内容；KnowledgeBase、Store 外键、`adopt_dataset` 任务和既有 ConversationRouteState 全部一致。验收问题命中 12 条并包含昭潭路入口资料；门店凭据和 AI runtime 继续保持未配置，未伪造 ready 状态。容器健康、重启数为 0，公网页面和认证选项接口均为 HTTP 200，部署后日志未发现错误或敏感配置字段。

### 2026-08-03 ASR 延期期间的模型凭据启用规则

- 目标：当前统一 NewAPI 网关没有任何 ASR 模型或渠道时，允许显式停用 ASR，继续对其余八个必需槽执行真实请求验证并激活门店 Binding 凭据。
- 模型契约：九槽结构和稳定 UsageCode 保持不变；ASR 是唯一可停用槽，其余八槽仍必须启用、配置模型并通过真实验证。以后恢复 ASR 时必须创建新 revision、启用 ASR 并重新验证，不修改既有 active revision。
- 运行行为：ASR 停用时语音转写解析继续 fail closed；不得以聊天模型、空响应或伪造结果替代 ASR。文本、视觉、文档、Embedding、Rerank、FastGPT 和计费归因不受影响。
- 页面：平台模型方案编辑器只为 ASR 暴露启用开关；测试文案改为“真实启用槽测试”。凭据录入、密码复核、主管审批、加密、审计和旧 active revision 保护不变。
- 数据与接口：不新增 model、migration、DTO、enum、路由或 WebSocket 字段，只使用既有 `ModelProfileSlot.Enabled`。
- 并行影响：`origin/codex/ai-billing@4db7993` 和 `origin/codex/customer-audit@c706815` 均不存在当前统一模型方案文件，不需要 rebase 或跨分支契约提交。
- 回滚：回滚应用后，包含停用 ASR 的方案将无法再通过旧版发布校验；已激活凭据和其他八槽数据不得手工改库，需先恢复 ASR 或发布兼容 revision。

### 2026-08-03 已有 FastGPT 数据集采用时补齐门店 Team 映射

- 目标：修复已有 Dataset 已通过受控采用、但本地缺少 `FastGPTStoreTenant`，导致门店凭据八槽测试通过后仍在 FastGPT 同步阶段以 `store_tenant_missing` 失败的问题。
- 远端顺序：仍先验证 Dataset 门店归属、名称、集合索引、模型快照和真实检索命中；全部通过后才调用统一 Integration API 的 `tenant/ensure` 解析该 Store 的 Team，失败不写任何本地采用状态。
- 本地事务：Store Team 映射与 KnowledgeBase、Store 唯一引用、ConversationRouteState 和幂等 `adopt_dataset` 任务在同一事务提交。新映射只保存非敏感 Team ID、名称和状态，`readiness_status=unconfigured`。
- 激活边界：采用不写 Target/Applied Profile、Binding、Credential revision、密钥指纹或 `ready`。重复采用只更新 Team 身份字段，保留现有目标 revision、已应用 revision、指纹和 readiness，避免已激活门店被降级。
- 测试：覆盖首次采用创建唯一非 ready Team 映射、重复采用不重复且不覆盖已应用状态，以及任一远端验证失败时不产生 Team 映射。
- 并行影响：`origin/codex/ai-billing` 在 FastGPT 数据集服务和测试上存在历史差异，`origin/codex/customer-audit` 已删除这些历史文件；本次只扩展统一分支现有实现，禁止整文件覆盖或回灌旧模型。
- 回滚：代码回滚不会删除已建立的 Team 或 Dataset。若需要解除本地映射，必须通过后续受权限保护的业务动作实施，禁止手工删表或清零 applied revision。
- 工程验证：FastGPT 定向测试、`go test ./... -count=1`、相关 service 竞态测试、`go vet ./...` 和 `git diff --check` 全部通过。
- 生产发布：release 为 `/opt/agentdesk/releases/20260803-154831-fastgpt-team-fix/app`，镜像摘要为 `sha256:f664a3221944e40aa53c4376df9ea84e9d707cc34d5cd080f824d836dc9b7cce`，应用于 `2026-08-03T07:53:18.694212386Z` 启动；容器健康、重启数为 0，本机和公网 HTTPS 均为 HTTP 200。
- 生产业务验收：通过受权限保护的 adopt API 重新采用合肥南七店数据集，返回 1 个集合、20089 条内容，页面先显示 Team `active`、readiness `unconfigured`；随后从现有“门店模型指派”对“合肥南七 / 门店员工1”重新提交凭据，八个启用槽真实测试及 FastGPT 同步均成功，模型方案 r1、凭据 r2 和 Team readiness 最终一致为 ready。
- 模型事实：回复、意图、摘要和客户标签使用 `deepseek-v4-pro`；视觉与文档解析使用 `qwen3.5-flash`；Embedding 使用 `text-embedding-v4`；Rerank 使用 `qwen3-vl-rerank`；ASR 刷新后仍为未配置且停用，系统没有 TTS 槽。
- 检索验收：后台以“停车场在哪里？”真实检索命中 12 条，首批结果包含昭潭路停车入口、免费停车和地下车库充电桩等南七门店资料；该操作不向客户发消息。
- 生产安全：切换前备份位于 `/opt/agentdesk/backups/20260803-154831-fastgpt-team-fix`，数据库压缩备份通过完整性校验；日志的启动/运行错误计数和明文 Key 模式命中均为 0。失败的首次候选 revision 保留在不可修改审计中，本次按顺序激活 r2，未改写历史。

### 2026-08-03 南七定位意图与首联资源收敛

- 目标：修复 IntentDetect 被 Runtime 固定 12 秒超时提前取消的问题，并收敛新会话普通欢迎小程序与延迟到店绑定票据重复出现的行为；位置咨询必须走定位动作，不允许用小程序兜底。
- 根因证据：生产消息记录显示 `16:33:43` 是普通欢迎小程序，`16:39:47` 是延迟联系人同步发送的 `arrival_bind_ticket_5`；“酒店在哪”位于两者之间，其 IntentDetect 超时后没有回复。两张卡属于首联资源，不是 AI 位置回复。
- Runtime：意图模型调用使用槽 `TimeoutMS`，生产为 30000ms；槽未配置时默认 60 秒。上游 Context 取消继续生效，不新增无限等待或模型 fallback。
- 首联资源：新增统一 `sendNewContactResources` 编排；首条客户消息与延迟联系人同步均可创建票据，并复用 `arrival_bind_ticket_<ticketID>` 幂等键。有效静态到店连接存在时，普通欢迎小程序被到店票据替代。
- 定位事实：合肥南七 Store 已通过现有管理接口保存导航名、详细地址、`117.263900,31.824091` 和 `amap`；没有直接写数据库。知识库确认名称与地址，地图 POI 补充坐标。
- 文件：`internal/ai/runtime/executor/intent_model_detector.go`、`intent_pipeline_test.go`、`internal/services/wxwork_protocol_default_resource_service.go`、`wxwork_protocol_contact_automation_service.go`、`wxwork_protocol_service.go`、`arrival_static_plugin_test.go` 及三份权威文档。
- 契约与数据：无 model、AutoMigrate、DML migration、DTO、enum、公开 API、WebSocket 或前端变化；Store 定位字段属于现有业务配置。没有新增身份、知识库或消息 fallback。
- 测试：service、AI runtime、bootstrap、全量 Go 测试、`go vet ./...` 和 `git diff --check` 全部通过；覆盖 30000ms 槽超时、60 秒默认值、静态到店连接抑制普通欢迎小程序，以及两入口稳定票据幂等键。
- 并行分支：本次文件未从 `origin/codex/ai-billing` 整体覆盖；继续以统一分支现有 Runtime 和 Tenant/Store 适配为基线。无共享 DTO 或 migration 合并顺序变化。
- 生产发布：release 为 `/opt/agentdesk/releases/20260803-171002-location-intent-welcome/app`，镜像摘要为 `sha256:6cafc98063c49bf04d27121991150e883c10d030af9d043918857366aae375e6`，容器于 `2026-08-03T09:11:34.207353601Z` 启动；healthy、重启数 0，本机与公网 HTTP 200，MySQL 未重建。
- 待验收：真实客户需要重新发送“酒店在哪”，确认 `provide_location -> location -> Outbox -> /msg/send_location` 全链成功且不新增小程序卡。在该证据出现前，只能称为代码、配置和部署完成。
- 回滚：可切回部署前固定镜像；Store 定位配置可保留。代码回滚会恢复固定 12 秒超时和旧欢迎卡行为，不需要回滚 Schema 或恢复数据库。

### 2026-08-03 已有联系人自动绑定卡重发收敛

- 根因：联系人增量同步把所有非删除记录交给首联资源编排；已有会话虽然抑制了普通欢迎语，但仍无条件调用绑定票据发送。旧票据 30 分钟过期后，新票据 ID 绕过基于 `arrival_bind_ticket_<ticketID>` 的消息幂等，导致同一客户再次收到卡片。
- 线上证据：重复卡为 `arrival_bind_ticket_6`，来源 `wx_contact_welcome`；上一张票据已过期。同期没有 AgentRunLog、没有 `ArrivalScanEvent`，会话仍为原 Session，因此与 AI、12 小时会话重开、24 小时知识进化和真实再次扫码均无关。
- 修复：`sendWelcome` 使用 `ensureConversation` 的真实 `created` 结果；只有 `created=true` 才进入统一首联资源编排。已有映射、并发下复用既有 Conversation 或存量会话均直接跳过自动欢迎和绑定卡。
- 保留链路：首条客户消息真实新建会话仍发送首次资源；后台受权限保护的人工发卡不变；已绑定小程序身份真实再次扫码仍通过 `ArrivalScanEvent`、`arrival_scan_<eventID>` 和既有频控投递。
- 数据与契约：无 model、AutoMigrate、DML migration、DTO、enum、公开 API、WebSocket、前端、小程序或 AI 引擎变化。
- 测试：覆盖首次资源只发一次、已有会话不重复、旧票据过期后联系人变更不创建新票据、真实再次扫码继续发送；services、AI runtime、bootstrap、全量 Go 测试、定向 race、`go vet ./...` 和 `git diff --check` 均通过。
- 并行分支：`origin/codex/customer-audit` 无同文件变化；`origin/codex/ai-billing` 对协议消息入口有历史差异，但当前统一分支已吸收其范围适配，本次不覆盖其 AI 语义。无共享 migration 或 DTO 合并顺序变化。
- 回滚：应用回滚会恢复联系人变更在票据过期后自动重发的缺陷；不需要数据库恢复，不得删除既有票据、扫码事件或审计历史。

### 2026-08-03 AI 回复引擎迁移审计与持久任务收敛

- 目标：逐文件、逐调用链核对 `origin/codex/ai-billing@4db7993`，确认成熟 Runtime 主体已迁移，并修复瞬时触发、媒体双入口、模型范围和 Binding 用量归因缺口。
- 审计结论：来源 `internal/ai` 共 168 个文件，80 个逐字节一致、49 个做 Tenant/Store/Binding/统一模型/FastGPT 适配、39 个为明确退役的 AIConfig、本地 RAG/Qdrant 和旧 skills；没有发现应恢复但遗漏的成熟 Runtime 模块。完整证据见 `docs/development/ai-reply-engine-migration-audit-20260803.md`。
- 数据：新增内部 `AIReplyJob` 表与 enum，唯一键为 Tenant + Conversation + Message；通过 AutoMigrate 注册，不新增 DML migration，不注册 generator。
- 运行：客户 Message 与任务原子提交；每秒最多处理 4 个，90 秒租约、30 秒续租，失败退避 15 秒/1 分钟/3 分钟，最多 4 次；15 分钟仍无人回复时进入现有人工池。
- 幂等：删除裸异步 Hook 和媒体第二入口；新消息、Session、人工状态、AI 开关和范围在模型前及 Commit 前重新验证；已有回复/RunLog 收敛任务，Outbox 失败不重跑模型。
- 模型与计费：Conversation、Route、当前实例的 Tenant/Store/Binding 必须一致；Reply/Intent Usage 补齐 Binding。未增加模型调用，未改变 token 或费用计算公式。
- 结构化语义：Intent JSON、resourceAction 和 Commit 协议不变；知识问题不产生小程序，定位只提交 location，欢迎/绑定票据/再次扫码继续走独立资源链。
- 共享契约：无公开 DTO、HTTP API、WebSocket 或前端变化；新增内部 model/enum 属于共享 Schema，部署前必须先 AutoMigrate 再启动 worker。
- 并行影响：`ai-billing` 作为行为来源不得整分支合并；`customer-audit` 若修改 Message/Conversation/Outbox，必须保留任务原子提交和稳定 RequestID。建议先合并本任务的 Schema/Repository，再合并 Runtime/Message 集成。
- 回滚：部署前可整体回滚；部署后回滚应用不会自动删除任务表。旧版本不会消费新任务，回滚前必须停入站、排空或明确保留任务，再切换版本，禁止手工伪造完成状态。
- 本轮边界：只在本地实现和测试，不提交、不推送、不部署；真实 NewAPI/FastGPT/企微验收必须在后续发布窗口单独执行。

### 2026-08-05 九槽模型方案直接编辑

- 目标：平台管理员可以从当前生效、待门店应用或待应用修改状态直接进入同一个九槽编辑器，不再要求用户手工创建“新版本”。
- 数据语义：已发布 revision 继续保持不可变；编辑 active/candidate 时，服务层在事务内创建或复用同编码的 draft revision，保存后由既有真实启用槽测试、发布和门店切换流程生效。
- 并发保护：更新请求新增 `confirmRevision`，服务层锁定来源 revision 和目标 draft；确认值过期、状态不可编辑或并发变化时 fail closed。
- 页面：同编码的历史 revision 收敛为一个逻辑方案，按 draft、candidate、active 顺序展示当前操作对象；按钮统一为“编辑配置”和“应用修改”，历史 revision 仍保留在数据库与审计记录中。
- 门店指派：同编码只提供最新的 candidate/active revision，避免管理员误将门店重新指派到仍保留的旧模型历史版本。
- 数据与接口：无 model、AutoMigrate、DML migration、enum、路由、WebSocket 或计费变化；只向现有更新 DTO 增加 revision 二次确认字段。
- 并行影响：修改模型 Profile repository/service、请求 DTO 和模型方案页面；不改 AI Runtime、消息链路、员工号协议或 Store Credential 密钥语义。后续发布只基于 `weibao/main`。
- 回滚：可回滚应用；已创建的 draft revision 可以保留，不影响门店继续使用原 active revision，禁止手工覆盖 active 槽或员工号凭据。
