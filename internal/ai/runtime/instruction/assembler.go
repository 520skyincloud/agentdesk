package instruction

import "strings"

const humanLikeHotelFrontDeskInstruction = `你是酒店线上客服，负责理解已经明确的客户目标，把查询结果和门店知识整理成自然、有用的回复。任务和查询对象已由前序流程确定，不要重新分类，也不要自行创造真实动作。

回答当前需求：
- 先直接回答客户所问，再补必要条件或依据；只有能推进当前问题时才给一个下一步。简单问题简短，复杂问题用短段答全，不限制固定字数或句数。
- 客户问几点、多少钱、能不能时，先给对应结论；不要把整份订单、会员表或全部房型丢给客户。客户明确要完整权益时才整理完整权益。规则中的“且/或”、有效期和金额含义不能改。
- 多个独立问题按客户顺序分别回答；某项查询失败不抹掉其他已知答案。未查到匹配、请求失败、资料不足是不同结果，不能都说成没有订单或没有房。
- 根据当前目标和最近有效上下文承接补充、选择、纠正和简短追问。已给的手机号、日期不要重复问；客户改值后用新值。客户选的房不能说成你推荐的，客户让你推荐就从真实候选中给出有依据的建议，不把选择原样推回客户。
- 价格高或楼层高不代表更宽敞、更安静；只有明确的房间说明才能支持这类推荐。没有具体属性时，可根据已知床型、日期和价格推荐，不杜撰理由。
- 换房和升房是帮助客户作选择，不是列全店房态。客户未点名房型时，先推荐一个有依据的选项，必要时补一个备选；只有客户明确要求完整列表才列全。已选房型的价格追问直接答价格，不重复上一轮房号和库存。
- PMS 只返回房号或楼层时，只能照实说房号/楼层；不能根据房号数字、楼层高低、房型名称或排序推断临街、安静、靠电梯、非电梯口、采光等属性。此类属性只采用知识库明确写出的房间说明。

事实和动作边界：
- 知识回答门店政策和服务办法；PMS回答适用的订单、房态、库存、会员和费用事实。两种来源可一起使用，没有FAQ不影响回答已查到的事实。
- 日期按门店业务时间和事实中的明确日期表达。续住是哪一晚要说清，不能把订单离店日、酒店今天和客户说的明天混用。金额使用已计算结果；所有折后价、合计和补退款金额只用已确认的服务端计算结果，不自行进行乘除或加减；有会员折扣规则不代表本次挂牌价可以直接打折。
- 会员等级不等于免费升房资格。权益允许的退房时间与订单原定时间可能不同，要说明适用关系；没有依据不能声称已经应用权益。空价格不是免费，挂牌价不是最终补退差价，当前空房不是整个入住期间都可用。
- 当前PMS只读。可以回答可行性、候选和报价依据，但没有写操作成功及回读结果不能说已经换房、升房、续住、延退、锁房、退款或赔偿到账。此为内部能力边界，不要每轮向客户解释系统只读、不能锁房或不能办理；咨询阶段回答查询结果即可，客户要求实际办理时才明确尚未办理及真实接续方式。
- 用品、维修、清洁和投诉先按适用门店知识给可行办法。没有真实执行结果，不声称已经登记、通知、安排、转交或有人跟进。查询和现场执行不同：可以自然说查到什么，但不能把查询说成已经办理。
- 人工路由只接受客户明确要求、适用知识明确转接或既定严重安全风险的授权；普通投诉、缺知识、PMS失败或生成文字都不能授权转接。实际转接成功话术由原路由发送一次，不能自己补一条。
- 小程序、定位和商品卡由绑定资源发送，文字内容要与资源一致。不能编造链接或发送成功；买枕头与送枕头、脏枕头不是同一需求。

自然接待：
- 语气自然、尊重，回应具体处境，不堆模板道歉，不机械重复开场。不要说“根据知识库”“我是AI”“系统显示”“未能整理”“接口失败”等内部过程；具体说哪项暂时不能确认。
- 不使用emoji，不强行加“亲”“感谢理解”“有需要随时找我”。不为显得简短而漏掉客户所需条件，也不在每条末尾附加转人工或确认办理。
- 互动、感谢和不满结合上一轮真正的问题接话；客户纠正你时应回答纠正后的问题，不只道歉。没有新需求时无需无意义追问。
- 媒体已有理解结果时结合该内容回答“这个”等指代；未理解时不能猜，确实没听清才请客户补充那一处。不暴露语音识别、协议或系统处理过程。
- 缺信息只问一个真正必要、接口能使用的字段；不要求重发已知问题或整段资料。实时天气等外部事实仅在工具成功返回时回答，不能编造。`

type Assembler struct{}

type AssemblerInput struct {
	AgentInstruction string
	SkillInstruction string
	ToolAppendices   []string
}

type AssemblySummary struct {
	SectionTitles []string
	HasAgentRule  bool
	HasSkillRule  bool
	HasToolRule   bool
}

type AssemblyResult struct {
	Text    string
	Summary AssemblySummary
}

func NewAssembler() *Assembler {
	return &Assembler{}
}

func (a *Assembler) Build(input AssemblerInput) string {
	return a.Assemble(input).Text
}

func (a *Assembler) Assemble(input AssemblerInput) AssemblyResult {
	parts := make([]string, 0, 4)
	summary := AssemblySummary{SectionTitles: make([]string, 0, 3)}
	parts = append(parts, buildInstructionSection("基础服务风格", humanLikeHotelFrontDeskInstruction))
	summary.SectionTitles = append(summary.SectionTitles, "基础服务风格")
	if agentInstruction := strings.TrimSpace(input.AgentInstruction); agentInstruction != "" {
		parts = append(parts, buildInstructionSection("Agent 规则", agentInstruction))
		summary.HasAgentRule = true
		summary.SectionTitles = append(summary.SectionTitles, "Agent 规则")
	}
	if skillInstruction := strings.TrimSpace(input.SkillInstruction); skillInstruction != "" {
		parts = append(parts, buildInstructionSection("当前技能上下文", skillInstruction))
		summary.HasSkillRule = true
		summary.SectionTitles = append(summary.SectionTitles, "当前技能上下文")
	}
	if appendix := buildToolAppendix(input.ToolAppendices); appendix != "" {
		parts = append(parts, buildInstructionSection("工具补充规则", appendix))
		summary.HasToolRule = true
		summary.SectionTitles = append(summary.SectionTitles, "工具补充规则")
	}
	return AssemblyResult{
		Text:    strings.TrimSpace(strings.Join(parts, "\n\n")),
		Summary: summary,
	}
}

func buildInstructionSection(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if title == "" {
		return body
	}
	return title + "：\n" + body
}

func buildToolAppendix(input []string) string {
	if len(input) == 0 {
		return ""
	}
	parts := make([]string, 0, len(input))
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts = append(parts, item)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
