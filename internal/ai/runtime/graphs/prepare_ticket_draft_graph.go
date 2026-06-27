package graphs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

type PrepareTicketDraftInput struct {
	Title           string `json:"title"`
	Description     string `json:"description"`
	Issue           string `json:"issue"`
	Impact          string `json:"impact"`
	ExpectedOutcome string `json:"expectedOutcome"`
	CurrentAttempt  string `json:"currentAttempt"`
	TaskCategory    string `json:"taskCategory"`
	RoomNumber      string `json:"roomNumber"`
	ServiceItem     string `json:"serviceItem"`
	Quantity        string `json:"quantity"`
	PreferredTime   string `json:"preferredTime"`
	Urgency         string `json:"urgency"`
}

type PrepareTicketDraftResult struct {
	Ready             bool     `json:"ready"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	MissingFields     []string `json:"missingFields,omitempty"`
	FollowUpQuestions []string `json:"followUpQuestions,omitempty"`
	ConversationFacts []string `json:"conversationFacts,omitempty"`
}

type PrepareTicketDraftGraph struct {
	conversation models.Conversation
}

func NewPrepareTicketDraftGraph(conversation models.Conversation) *PrepareTicketDraftGraph {
	return &PrepareTicketDraftGraph{conversation: conversation}
}

func (g *PrepareTicketDraftGraph) Run(_ context.Context, argumentsInJSON string) (string, error) {
	input, err := g.parseInput(argumentsInJSON)
	if err != nil {
		return "", err
	}
	messages, _, _ := services.MessageService.FindByConversationIDCursor(g.conversation.ID, 0, 6, "", "")
	result := buildPrepareTicketDraftResult(g.conversation, messages, input)
	buf, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func (g *PrepareTicketDraftGraph) parseInput(argumentsInJSON string) (PrepareTicketDraftInput, error) {
	var input PrepareTicketDraftInput
	if strings.TrimSpace(argumentsInJSON) == "" {
		return input, nil
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return input, fmt.Errorf("invalid prepare ticket draft arguments: %w", err)
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Issue = strings.TrimSpace(input.Issue)
	input.Impact = strings.TrimSpace(input.Impact)
	input.ExpectedOutcome = strings.TrimSpace(input.ExpectedOutcome)
	input.CurrentAttempt = strings.TrimSpace(input.CurrentAttempt)
	input.TaskCategory = strings.TrimSpace(input.TaskCategory)
	input.RoomNumber = strings.TrimSpace(input.RoomNumber)
	input.ServiceItem = strings.TrimSpace(input.ServiceItem)
	input.Quantity = strings.TrimSpace(input.Quantity)
	input.PreferredTime = strings.TrimSpace(input.PreferredTime)
	input.Urgency = strings.TrimSpace(input.Urgency)
	return input, nil
}

func buildPrepareTicketDraftResult(conversation models.Conversation, messages []models.Message, input PrepareTicketDraftInput) PrepareTicketDraftResult {
	result := PrepareTicketDraftResult{
		MissingFields:     make([]string, 0, 2),
		FollowUpQuestions: make([]string, 0, 2),
		ConversationFacts: buildConversationFacts(conversation, messages),
	}
	result.Title = buildDraftTitle(conversation, input)
	result.Description = buildDraftDescription(conversation, messages, input)
	if strings.TrimSpace(result.Title) == "" {
		result.MissingFields = append(result.MissingFields, "title")
		result.FollowUpQuestions = append(result.FollowUpQuestions, "请补充一个简洁的工单标题，明确概括用户遇到的问题。")
	}
	if !hasSufficientIssueContext(input, result.Description) {
		result.MissingFields = append(result.MissingFields, "issue")
		result.FollowUpQuestions = append(result.FollowUpQuestions, "请补充具体问题现象、报错信息或用户诉求，以便整理成工单。")
	}
	result.applyServiceTaskRequirements(input)
	result.Ready = result.Title != "" && result.Description != "" && len(result.MissingFields) == 0
	return result
}

func (r *PrepareTicketDraftResult) applyServiceTaskRequirements(input PrepareTicketDraftInput) {
	category := normalizeTaskCategory(input.TaskCategory)
	taskText := inputTextForTask(input, r.Description)
	serviceLike := category == "service_task" || containsAny(taskText, serviceTaskKeywords...)
	repairLike := category == "repair" || containsAny(taskText, repairTaskKeywords...)
	cleanLike := category == "cleaning" || containsAny(taskText, cleaningTaskKeywords...)
	if !serviceLike && !repairLike && !cleanLike {
		return
	}
	if strings.TrimSpace(input.RoomNumber) == "" && !looksLikeContainsRoomNumber(taskText) {
		r.addMissingField("roomNumber", "麻烦发一下房间号，我好给同事派单。")
	}
	if serviceLike && strings.TrimSpace(input.ServiceItem) == "" && !containsAny(taskText, serviceTaskKeywords...) {
		r.addMissingField("serviceItem", "需要同事送什么物品？比如水、拖鞋、毛巾或被子。")
	}
	if repairLike && !hasSufficientIssueContext(input, r.Description) {
		r.addMissingField("repairIssue", "麻烦说一下具体哪里需要维修，以及现在的现象。")
	}
}

func (r *PrepareTicketDraftResult) addMissingField(field string, question string) {
	for _, item := range r.MissingFields {
		if item == field {
			return
		}
	}
	r.MissingFields = append(r.MissingFields, field)
	if strings.TrimSpace(question) != "" {
		r.FollowUpQuestions = append(r.FollowUpQuestions, question)
	}
}

func buildDraftTitle(conversation models.Conversation, input PrepareTicketDraftInput) string {
	switch {
	case input.Title != "":
		return limitText(input.Title, 80)
	case input.Issue != "":
		return limitText(input.Issue, 80)
	case strings.TrimSpace(conversation.LastMessageSummary) != "":
		return limitText(conversation.LastMessageSummary, 80)
	default:
		return ""
	}
}

func buildDraftDescription(conversation models.Conversation, messages []models.Message, input PrepareTicketDraftInput) string {
	if input.Description != "" {
		return input.Description
	}
	parts := make([]string, 0, 6)
	if input.Issue != "" {
		parts = append(parts, "问题现象："+input.Issue)
	}
	if input.Impact != "" {
		parts = append(parts, "影响范围："+input.Impact)
	}
	if input.ExpectedOutcome != "" {
		parts = append(parts, "用户诉求："+input.ExpectedOutcome)
	}
	if input.CurrentAttempt != "" {
		parts = append(parts, "已尝试处理："+input.CurrentAttempt)
	}
	if input.TaskCategory != "" {
		parts = append(parts, "任务分类："+input.TaskCategory)
	}
	if input.RoomNumber != "" {
		parts = append(parts, "房间号："+input.RoomNumber)
	}
	if input.ServiceItem != "" {
		parts = append(parts, "服务事项/物品："+input.ServiceItem)
	}
	if input.Quantity != "" {
		parts = append(parts, "数量："+input.Quantity)
	}
	if input.PreferredTime != "" {
		parts = append(parts, "期望时间："+input.PreferredTime)
	}
	if input.Urgency != "" {
		parts = append(parts, "紧急程度："+input.Urgency)
	}
	if strings.TrimSpace(conversation.LastMessageSummary) != "" {
		parts = append(parts, "会话摘要："+strings.TrimSpace(conversation.LastMessageSummary))
	}
	if recent := buildRecentMessageDigest(messages); recent != "" {
		parts = append(parts, "最近消息："+recent)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func normalizeTaskCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "service", "delivery", "service_task", "送物", "送东西", "客房服务":
		return "service_task"
	case "repair", "maintenance", "维修", "报修":
		return "repair"
	case "clean", "cleaning", "housekeeping", "打扫", "保洁", "卫生":
		return "cleaning"
	default:
		return value
	}
}

func inputTextForTask(input PrepareTicketDraftInput, description string) string {
	return strings.ToLower(strings.Join([]string{
		input.Title,
		input.Description,
		input.Issue,
		input.Impact,
		input.ExpectedOutcome,
		input.CurrentAttempt,
		input.TaskCategory,
		input.RoomNumber,
		input.ServiceItem,
		input.Quantity,
		input.PreferredTime,
		input.Urgency,
		description,
	}, "\n"))
}

func looksLikeContainsRoomNumber(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "房间") || strings.Contains(value, "房号") || strings.Contains(value, "room") {
		return true
	}
	runes := []rune(value)
	consecutiveDigits := 0
	for _, r := range runes {
		if r >= '0' && r <= '9' {
			consecutiveDigits++
			if consecutiveDigits >= 3 {
				return true
			}
			continue
		}
		consecutiveDigits = 0
	}
	return false
}

var serviceTaskKeywords = []string{"送水", "矿泉水", "拖鞋", "毛巾", "浴巾", "牙刷", "纸巾", "抽纸", "加被", "被子", "枕头", "洗漱", "用品", "送一下", "拿一下", "补一下", "客房服务", "行李", "叫醒"}

var repairTaskKeywords = []string{"维修", "修一下", "坏了", "漏水", "堵了", "马桶", "空调", "电视", "门锁", "灯", "热水", "没有电", "异响", "打不开"}

var cleaningTaskKeywords = []string{"打扫", "保洁", "卫生", "清理", "换床单", "换被套", "垃圾", "脏", "有味", "异味"}

func hasSufficientIssueContext(input PrepareTicketDraftInput, description string) bool {
	if input.Issue != "" || input.Description != "" {
		return true
	}
	return len([]rune(strings.TrimSpace(description))) >= 30
}

func buildConversationFacts(conversation models.Conversation, messages []models.Message) []string {
	facts := make([]string, 0, 4)
	if strings.TrimSpace(conversation.LastMessageSummary) != "" {
		facts = append(facts, "最近摘要："+strings.TrimSpace(conversation.LastMessageSummary))
	}
	if digest := buildRecentMessageDigest(messages); digest != "" {
		facts = append(facts, "最近消息："+digest)
	}
	return facts
}

func buildRecentMessageDigest(messages []models.Message) string {
	if len(messages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(messages))
	for i := range messages {
		content := strings.TrimSpace(messages[i].Content)
		if content == "" {
			continue
		}
		parts = append(parts, messageSenderLabel(messages[i].SenderType)+"："+limitText(content, 60))
	}
	return strings.Join(parts, " | ")
}

func messageSenderLabel(senderType enums.IMSenderType) string {
	switch senderType {
	case enums.IMSenderTypeCustomer:
		return "用户"
	case enums.IMSenderTypeAgent:
		return "客服"
	case enums.IMSenderTypeAI:
		return "AI"
	default:
		return "消息"
	}
}

func limitText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}
