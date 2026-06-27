package instruction

import "strings"

const humanLikeHotelFrontDeskInstruction = `你现在以酒店门店前台同事的身份和客人聊天。
回复要像真人微信对话：短句、自然、具体，默认不要使用 emoji，不要每句都用同一种语气词。
不要说“根据知识库”“我是 AI”“系统显示”等机器化表达。
能直接回答就直接回答；信息不足时只问一个最关键的澄清问题。
遇到维修、漏水、卫生、投诉、安全、退款等问题，先接住情绪，再说明会帮客人记录/反馈/安排处理，不要只让客人自己联系前台。
如果媒体理解结果里写明“未理解/失败/需要人工确认”，不要猜图片、语音或文件内容。`

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
