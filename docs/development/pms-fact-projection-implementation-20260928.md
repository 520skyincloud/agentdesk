# PMS 事实投影交接

日期：2026-09-28。范围：当前独立 PMS 工作树，未提交、未部署、未执行外部 PMS 写操作。

## 目标与变更

- PMS 查询结果进入现有 Generate，不再预写整段客户 `AnswerText`。
- `pms_read_catalog.go` 按 `SubjectScope`、`RequestedAspects` 输出会员、历史订单、逐日挂牌价、区间库存和房间候选事实。
- `pms_read_execution.go` 按所求字段投影当前订单；保留部分成功事实和缺失项，日期使用客户可读格式。
- 公开会员等级不要求个人号码、不查询个人订单；个人权益只保留必要等级状态及所问权益。
- 历史住宿不能代替当前住宿；多笔未唯一定位时提供日期、房型选择信息，不任选第一单回答金额或离店时间。
- 客户当前房型选择在完整真实目录中绑定，覆盖带口语前缀的选择以及目录第六项之后的房型；多个真实名称仍需澄清。
- 关键值只保留该事实必要日期、金额、名称或状态，不把整段规则或后台记录用作不可改写字符串。

## 兼容边界

- 未改 models、migration、DTO、接口、权限、WebSocket、Outbox、PMS Provider 或数据库结构。
- 与 Intent 同步使用既有新增内部字段；旧记录缺少方面时只按原广义目标兼容，不重新使用词表解析客户需求。
- 与主任务 Generate/Validate 修改共同发布；原 direct Commit 路径由主任务负责移除。
- HPMS 仍只读。库存不代表锁房，挂牌价不代表最终差价，空金额不代表免费。
- “薇薇”“薇薇2”备份未修改。

## 验证

2026-09-28 本模块聚焦测试通过：

```sh
go test ./internal/ai/runtime/executor -run '(TestPMS|TestRuntimePMS|TestApplyRuntimePMS|TestExecuteRuntimePMS|TestOrderHistory|TestCatalog|TestPublicDiamond|TestResolveRuntimePMS|TestCustomerJourney|TestCurrentChoiceOverridesPersistedChoice)' -count=1
go test ./internal/pms -count=1
```

新增断言覆盖：

- 同类两种问法的会员等级、钻石退房权益、钻石升级 AND 条件、个人权益、历史离店时间和金额。
- 客户选房优先于旧房型；目录全量绑定，重名与多个名称不猜测。
- 指定目标在目录靠后仍有房价事实；空价格不产生零元报价。
- 退房当天不计为住宿夜；缺少任一住宿夜库存不能确认全程可售。
- 房间占用覆盖不完整时不返回确定可分配承诺，完整时保留只读边界。

未由本模块执行真实客户会话、企微送达或整包发布验收，不能据此标记用户体验已通过。

## 待主任务统一核对

- 当前轮会员号码与预订号码需分角色更新。历史快照已有 `MemberPhone`，但 `applyRuntimePMSRequiredSlotPreflight` 仍会把换房任务原话里的任意手机号写入 `customer_phone`；执行层也不能从原话抽号后覆盖正确角色。应在 Intent/定位合同处统一处理，不增加另一套关键词判断门。
- 多笔历史订单的总次数/完整历史列表需求不能等同于“选择一笔订单”。本轮只收口单笔字段与多笔选择，不声称已完成所有历史统计。
- 少量旧 `runtimePMSCustomer*` helper 仍供旧测试使用，生产主入口已不依赖整段预写回复；后续清理应以真实引用为准。

## 并行与回滚

本模块仅修改 PMS 执行、事实投影及对应测试；`customer_goal_contract_test.go` 仅迁移当前选择断言。未改 Intent、planner、知识裁决和 Generate 主体，便于主任务统一测试后按事实层提交。回退只回退程序，不恢复数据库，不撤销已发生外部业务；本轮本身没有外部写入。
