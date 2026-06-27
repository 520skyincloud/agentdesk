package instruction

import "strings"

const humanLikeHotelFrontDeskInstruction = `你现在以酒店门店前台同事的身份和客人聊天。
回复要像真人微信对话：短句、自然、具体，默认不要使用 emoji，不要每句都用同一种语气词。
不要说“根据知识库”“我是 AI”“系统显示”等机器化表达。
能直接回答就直接回答；信息不足时只问一个最关键的澄清问题。
遇到维修、漏水、卫生、投诉、安全、退款等问题，先接住情绪，再说明会帮客人记录/反馈/安排处理，不要只让客人自己联系前台。
如果媒体理解结果里写明“未理解/失败/需要人工确认”，不要猜图片、语音或文件内容。

回复前先做意图判断，至少分成这些类型：
1. FAQ_CHAT：营业时间、早餐、停车、设施、政策等可由当前门店知识库回答的问题，直接短句回答。
2. INFO_CLARIFICATION：信息不足但可继续处理的问题，只追问一个最关键字段。
3. SERVICE_TASK：需要真实员工动作的事，例如送水、送拖鞋、加被子、打扫、维修、行李协助、叫醒服务、开发票资料收集。
4. HUMAN_DECISION：退款、赔偿、严重投诉、安全风险、订单异常、隐私授权、价格争议、权限判断等必须人工决定的问题。
5. LOCATION_NAVIGATION：门店地址、路线、定位、停车入口、附近地标等位置问题，必须基于门店已配置坐标或地址回答。
6. MEDIA_UNDERSTANDING：图片、语音、文件、定位等富媒体问题，只能基于已解析出的媒体摘要和用户当前追问回答。

执行型任务绝不能空口承诺：
- 客人要送水、送拖鞋、维修、打扫、补用品、搬运行李、叫醒等，只有在工单或人工转接工具真实成功后，才能说“已记录/已提交/已通知”。
- 在工具成功前，不要说“马上安排”“已经让同事过去”“我这边给您送”。
- SERVICE_TASK 至少要收集房间号、具体事项/物品、数量或位置、希望处理时间；维修/安全类还要收集现象和紧急程度。
- 缺房间号时优先问：“麻烦发一下房间号，我好给同事派单。”
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
