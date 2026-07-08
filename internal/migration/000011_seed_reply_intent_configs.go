package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type defaultReplyIntentConfig struct {
	Code             string
	Name             string
	Keywords         string
	PositiveExamples string
	RequiredContext  string
	NeedsKnowledge   bool
	NeedsResource    bool
	ResourceType     string
	NeedsHumanRoute  bool
	HumanRoutePolicy string
	NoReply          bool
	Priority         int
	PromptPack       string
	ReplyPlan        string
	ValidationRules  string
}

func init() {
	register(11, "seed unified reply intent classifications", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			for _, item := range defaultReplyIntentConfigs() {
				current := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", item.Code, "global")
				if current != nil {
					continue
				}
				config := &models.ReplyIntentConfig{
					Code:               item.Code,
					Name:               item.Name,
					Description:        item.Name,
					ScopeType:          "global",
					Priority:           item.Priority,
					MatchMode:          "hybrid",
					Keywords:           item.Keywords,
					PositiveExamples:   item.PositiveExamples,
					RequiredContext:    item.RequiredContext,
					NeedsKnowledge:     item.NeedsKnowledge,
					NeedsResource:      item.NeedsResource,
					ResourceType:       item.ResourceType,
					NeedsHumanRoute:    item.NeedsHumanRoute,
					HumanRoutePolicy:   item.HumanRoutePolicy,
					PromptPack:         item.PromptPack,
					ReplyPlanTemplate:  item.ReplyPlan,
					ValidationRules:    item.ValidationRules,
					NoReplyWhenMatched: item.NoReply,
					Status:             enums.StatusOk,
					SortNo:             0,
					AuditFields: models.AuditFields{
						CreatedAt:      now,
						UpdatedAt:      now,
						CreateUserID:   constants.SystemAuditUserID,
						UpdateUserID:   constants.SystemAuditUserID,
						CreateUserName: constants.SystemAuditUserName,
						UpdateUserName: constants.SystemAuditUserName,
					},
				}
				if err := repositories.ReplyIntentConfigRepository.Create(ctx.Tx, config); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func defaultReplyIntentConfigs() []defaultReplyIntentConfig {
	return []defaultReplyIntentConfig{
		{Code: "hotel_info", Name: "酒店信息", Keywords: "wifi,wi-fi,无线网,无线密码,网络密码,网连不上,网络连不上,网不好,发票,发飘,专票,普票,抬头,报销,开发票,送水,拿水,矿泉水,拖鞋,牙刷,纸巾,浴巾,毛巾,用品,早餐,早饭,停车,车位,退房,续住,押金,洗衣,电视,投屏,空调,热水,电梯,前台,营业时间,几点", PositiveExamples: "房间网连不上\n怎么开发票\n能送两瓶水吗\n早餐几点\n停车场在哪", NeedsKnowledge: true, Priority: 200, PromptPack: "这是酒店信息大分类。只要用户问酒店规则、设施、服务、自助领取、WiFi、发票、早餐、停车、退房、洗衣、电视投屏、空调热水等门店信息，就必须查当前门店知识库/门店资料后回答；知识不足时只追问一个关键点，不编门店规则，不承诺帮客人执行动作。", ReplyPlan: "基于当前门店知识库/门店资料回答；如果知识不足，只追问一个关键字段或说明需要补充。", ValidationRules: "不得编造门店规则；不得承诺送水、维修、排查、已安排；不得把电话、小程序、定位当知识库答案。"},
		{Code: "hotel_variable", Name: "酒店变量", Keywords: "电话,客服电话,手机号,联系号码,联系电话,号码多少,电话多少,定位,发定位,定位发我,位置发我,导航,酒店在哪里,酒店在哪,门店地址,地址发我,怎么去,怎么走,入住,办理入住,小程序,安心宿,入住码,自助入住入口,重新扫码,加过你们", PositiveExamples: "你们电话多少\n发下酒店定位\n发下入住小程序", NeedsResource: true, ResourceType: "store_variable", Priority: 190, PromptPack: "这是酒店变量大分类。电话、定位、地址、小程序等都来自当前门店账号配置，不是知识库答案；只读取当前企微员工号绑定门店的变量，不编造号码、定位、小程序链接。", ReplyPlan: "根据子意图读取当前门店账号变量：电话 provide_phone、定位 provide_location、小程序 send_miniprogram。", ValidationRules: "不得查询知识库替代变量；不得把 A 门店变量用于 B 门店。"},
		{Code: "service_request", Name: "服务请求", Keywords: "维修,漏水,马桶,空调坏,空调故障,电视坏,叫醒,行李,异味,不舒服,帮拿,拿行李,e3,故障,打扫,保洁", PositiveExamples: "空调坏了\n行李很多能帮我拿下楼吗", NeedsKnowledge: true, NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Priority: 150, PromptPack: "服务请求先看当前门店知识库是否有自助路径；无法解决时按托管模式和人工路由策略处理；不要假承诺已安排。"},
		{Code: "human_complaint_risk", Name: "人工/投诉/风险", Keywords: "人工,真人,客服,转人工,找人,别机器人,退款,退钱,赔偿,投诉,不满意,差评,举报,安全,害怕,危险,门锁,隐私,报警,价格不一样,价差,太贵", PositiveExamples: "转人工\n我要投诉\n价格不一样", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Priority: 230, PromptPack: "进入人工/投诉/风险处理策略；按当前门店托管模式和排班路由；回复要安抚并说明会按接待路由处理，不编处理结果，不口头假装已经通知。"},
		{Code: "social_confirm", Name: "轻互动/确认", Keywords: "谢谢,好的,好,嗯,收到,可以,行,ok,OK,哈哈,哈,嘿嘿,笑死,表情", PositiveExamples: "好的谢谢\n哈哈", Priority: 80, PromptPack: "自然短句回应，不要只有哈哈，不主动转人工，语气不要淡。"},
		{Code: "unknown_clarify", Name: "未知/澄清", Priority: 1, PromptPack: "未能确定意图时，只围绕当前问题短答或追问一个关键点，不调用知识、资源或人工路由。"},
	}
}
