package migration

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

func defaultReplyIntentConfigs() []defaultReplyIntentConfig {
	return []defaultReplyIntentConfig{
		{Code: "hotel_info", Name: "酒店信息", Keywords: "wifi,wi-fi,无线网,无线密码,网络密码,网连不上,网络连不上,网不好,发票,发飘,专票,普票,抬头,报销,开发票,送水,拿水,矿泉水,拖鞋,牙刷,纸巾,浴巾,毛巾,用品,早餐,早饭,停车,车位,入住,办理入住,怎么入住,退房,续住,押金,洗衣,电视,投屏,空调,热水,电梯,前台,营业时间,几点", PositiveExamples: "房间网连不上\n怎么开发票\n怎么办理入住\n早餐几点\n停车场在哪", NeedsKnowledge: true, Priority: 200, PromptPack: "这是酒店信息大分类。只要用户问酒店规则、设施、服务、自助领取、WiFi、发票、早餐、停车、入住/退房、洗衣、电视投屏、空调热水等门店信息，就必须查当前门店知识库/门店资料后回答；知识不足时只说当前资料没写明并追问一个关键点，不编门店规则，不承诺帮客人执行动作。办理入住属于 hotel_info/checkin_process，并可附加小程序变量动作。", ReplyPlan: "基于当前门店知识库/门店资料回答；如果知识不足，只追问一个关键字段或说明当前资料没写明。", ValidationRules: "不得编造门店规则；不得承诺送水、维修、排查、已安排；不得把电话、小程序、定位当知识库答案。"},
		{Code: "hotel_variable", Name: "酒店变量", Keywords: "电话,客服电话,手机号,联系号码,联系电话,号码多少,电话多少,定位,发定位,定位发我,位置发我,导航,酒店在哪里,酒店在哪,门店地址,地址发我,怎么去,怎么走,入住小程序,小程序,安心宿,入住码,自助入住入口,重新扫码,加过你们", PositiveExamples: "你们电话多少\n发下酒店定位\n发下入住小程序", NeedsResource: true, ResourceType: "store_variable", Priority: 190, PromptPack: "这是酒店变量大分类。只有用户明确索要电话、定位/地址/导航、入住小程序入口时才读取当前企微员工号配置的变量；不编造号码、定位、小程序链接。单纯问“怎么入住/我要办理入住”不是纯变量，必须同时走 hotel_info/checkin_process。", ReplyPlan: "根据子意图读取当前门店配置变量：电话 provide_phone、定位 provide_location、小程序 provide_mini_program。", ValidationRules: "不得查询知识库替代变量；不得把 A 门店变量用于 B 门店；不得默认同时发送未请求的电话、定位、小程序。"},
		{Code: "service_request", Name: "服务请求", Keywords: "送水,送拖鞋,送牙刷,补纸巾,维修,漏水,马桶,叫醒,行李,帮拿,拿行李,打扫,保洁,上门,来处理", PositiveExamples: "帮我送两瓶水\n行李很多能帮我拿下楼吗\n叫人来看看空调", NeedsKnowledge: true, Priority: 150, PromptPack: "服务请求表示客户明确要求门店人员执行现实动作。仍先看当前门店知识库是否有自助路径、处理边界或必要信息；没有明确系统工具或人工路由结果时，不能承诺已安排、已通知、同事会过去或后续处理。真正人工/投诉/风险只由 human_complaint_risk 处理。", ReplyPlan: "先给自助路径或追问一个必要字段；需要人工时让意图识别进入 human_complaint_risk，而不是口头假装执行。", ValidationRules: "不得表达动作已执行、已转告、已安排；不得因为设备或服务词汇直接转人工。"},
		{Code: "human_complaint_risk", Name: "人工/投诉/风险", Keywords: "人工,真人,客服,转人工,找人,别机器人,退款,退钱,赔偿,投诉,差评,举报,报警,摔倒,受伤,流血,安全事故,隐私,身份证,订单异常", PositiveExamples: "转人工\n我要投诉\n我摔倒流血了", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Priority: 230, PromptPack: "只有明确人工、明确投诉升级、赔付退款、订单风险或安全风险才进入本类。回复必须走转人工确认/接待路由的真实结果，不编处理结果，不口头假装已经通知。单纯吐槽、辱骂、价格疑问或设施怎么处理，优先判到 interaction 或 hotel_info。"},
		{Code: "interaction", Name: "互动", Keywords: "谢谢,好的,好,嗯,收到,可以,行,ok,OK,哈哈,哈,嘿嘿,笑死,表情,闲聊,天气,你是谁,你在吗,无语,冷淡,像机器人,骂人,不开心,没事,不用了,先不用,随便聊", PositiveExamples: "好的谢谢\n哈哈\n你是谁\n无语了\n你回得太冷淡了", Priority: 80, PromptPack: "互动不是固定短答。所有闲聊、感谢、确认、表情、玩笑、纠错、单纯不满或辱骂但没有人工/投诉/安全诉求，都自然接住当前话题；不查知识、不取变量、不转人工，不承诺任何真实动作。表达不明确时只追问一个关键点。"},
	}
}
