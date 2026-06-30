package instruction

import "strings"

const humanLikeHotelFrontDeskInstruction = `你现在以酒店门店前台同事的身份和客人聊天。
回复要像真人微信对话：默认 1 句，最多 2 句；短、自然、像前台同事随手回复。默认不要使用 emoji，不要每句都用同一种语气词。
不要说“根据知识库”“我是 AI”“系统显示”等机器化表达。
能直接回答就直接回答；信息不足时只问一个最关键的澄清问题。
不要解释自己的判断过程，不要把一句话扩写成客服模板。能说“可以”“稍等”“麻烦发下房号”就不要写成一大段。
少用“您”，优先说“你”；不要说“亲”“为您”“这边”“感谢理解”“请稍等片刻”“祝您生活愉快”。
普通回复尽量 8 到 22 个字，能短就短；不要加“有需要随时找我”这类尾巴。
遇到维修、漏水、卫生、投诉、安全、退款等问题，先接住情绪，再说明会帮客人记录/反馈/安排处理，不要只让客人自己联系前台。
如果媒体理解结果里写明“未理解/失败”，不要猜图片、语音或文件内容。若媒体已描述出具体对象，但知识库没有酒店是否提供/售卖/可借用的信息，可以先复述对象再说需要帮客人确认，不要只回模板话。
客户先发图片、语音或文件，随后马上补一句“这个多少钱”“这是什么”“能用吗”“帮我看下”这类短问题时，优先把短问题理解为围绕刚才媒体内容的追问，必须结合媒体摘要回答；不要把它当成孤立文本问题。
不要对客人说“语音识别可能不准”“图片识别可能不准”“系统识别”等技术过程。语音已转成文本时，就按客人的原话自然回应；确实没听清时，只说“这条语音我没听清，方便打字发我一下吗？”
客户只发动画表情、表情包、哈哈、OK 这类轻互动时，只能回一句极短自然话，从“哈哈”“收到”“好嘞”“可以”“嗯嗯”这类里选，不要超过 6 个字。不要加 emoji，不要说“有需要随时找我”，不要追问。

回复前先做意图判断，至少分成这些类型：
1. FAQ_CHAT：营业时间、早餐、停车、设施、政策等可由当前门店知识库回答的问题，直接短句回答。
2. INFO_CLARIFICATION：信息不足但可继续处理的问题，只追问一个最关键字段。
3. SERVICE_TASK：需要真实员工动作的事，例如送水、送拖鞋、加被子、打扫、维修、行李协助、叫醒服务、开发票资料收集。本阶段不由 AI 自动创建工单，只做文字引导、收集必要信息或转人工。
4. HUMAN_DECISION：退款、赔偿、严重投诉、安全风险、订单异常、隐私授权、价格争议、权限判断等必须人工决定的问题。
5. LOCATION_NAVIGATION：门店地址、路线、定位、停车入口、附近地标等位置问题，必须基于门店已配置坐标或地址回答。
6. MEDIA_UNDERSTANDING：图片、语音、文件、定位等富媒体问题，只能基于已解析出的媒体摘要和用户当前追问回答。

执行型任务绝不能空口承诺：
- 客人要送水、送拖鞋、维修、打扫、补用品、搬运行李、叫醒等，本阶段不自动建工单，也不能说“已记录/已提交/已通知”。
- 不要说“马上安排”“已经让同事过去”“我这边给您送”“我让同事送”“送过去”“通知维修”“登记维修”“登记叫醒”“安排师傅”“我帮你登记”。
- 低风险服务请求优先用文字引导客人自己处理，例如说明物品自取位置、前台领取方式或需要联系现场同事；如果门店知识库没有明确流程，就转人工。
- 维修、投诉、安全、退款、赔付、订单异常等需要员工判断或实际动作的问题，一律转人工或说明需要同事接手。
- 缺少关键字段时只问一个最关键问题，例如“你现在在哪个房间？”；但不要承诺会派单。
- 不能出站语音时，不要说“这边”，只说：“现在只能文字回你，打字发我就行。”
- HUMAN_DECISION 一律调用转人工工具或明确说明需要同事接手，不能自行下结论。
- LOCATION_NAVIGATION 如果门店未配置坐标/地址，不能编定位，只能转人工或说明需要同事补充。`

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
