package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	svc "agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

type replyCommitService struct{}

type replyCommitInput struct {
	Conversation   models.Conversation
	Message        models.Message
	AIAgent        models.AIAgent
	ReplyText      string
	Trace          *aiReplyTraceData
	ClientPrefix   string
	IncrementRound bool
}

type structuredVariableReply struct {
	ResourceType string
	MessageType  enums.IMMessageType
	Content      string
	Payload      string
}

func newReplyCommitService() *replyCommitService {
	return &replyCommitService{}
}

func (s *replyCommitService) HasStructuredVariableReply(trace *aiReplyTraceData) bool {
	return len(structuredVariableResourceTypesFromTrace(trace)) > 0
}

func (s *replyCommitService) SendAIReply(input replyCommitInput) (*models.Message, error) {
	structuredReplies := s.buildStructuredVariableReplies(input)
	replyText := strings.TrimSpace(input.ReplyText)
	if replyText == "" && len(structuredReplies) == 0 {
		return nil, nil
	}
	commitStartedAt := time.Now()
	var replyMessage *models.Message
	commitMessages := make([]map[string]any, 0, len(structuredReplies)+1)
	if replyText != "" {
		textMessages := splitReplyTextForCommit(input.Trace, replyText)
		for index, text := range textMessages {
			clientMessageID := fmt.Sprintf("%s_%d", strings.TrimSpace(input.ClientPrefix), input.Message.ID)
			if len(textMessages) > 1 {
				clientMessageID = fmt.Sprintf("%s_text_%d_%d", strings.TrimSpace(input.ClientPrefix), index+1, input.Message.ID)
			}
			message, err := s.sendAIMessage(input, clientMessageID, enums.IMMessageTypeText, text, "")
			commitMessages = append(commitMessages, buildCommitMessageTrace(message, enums.IMMessageTypeText, "", text, err))
			if err != nil {
				s.updateCommitTrace(input, commitStartedAt, replyMessage, commitMessages, err)
				return message, err
			}
			replyMessage = message
		}
	}
	for index, structured := range structuredReplies {
		message, err := s.sendAIMessage(
			input,
			fmt.Sprintf("%s_%s_%d_%d", strings.TrimSpace(input.ClientPrefix), strings.TrimSpace(structured.ResourceType), index+1, input.Message.ID),
			structured.MessageType,
			structured.Content,
			structured.Payload,
		)
		commitMessages = append(commitMessages, buildCommitMessageTrace(message, structured.MessageType, structured.ResourceType, structuredRunLogReplyText(structured), err))
		if err != nil {
			s.updateCommitTrace(input, commitStartedAt, replyMessage, commitMessages, err)
			return message, err
		}
		replyMessage = message
	}
	s.updateCommitTrace(input, commitStartedAt, replyMessage, commitMessages, nil)
	if !input.IncrementRound {
		return replyMessage, nil
	}
	if err := s.IncrementAIReplyRounds(input.Conversation.ID, input.Conversation.AIReplyRounds+1, input.AIAgent.Name); err != nil {
		return nil, err
	}
	return replyMessage, nil
}

func (s *replyCommitService) sendAIMessage(input replyCommitInput, clientMessageID string, messageType enums.IMMessageType, content string, payload string) (*models.Message, error) {
	return svc.MessageService.SendAIMessageWithRequestID(
		input.Conversation.ID,
		input.AIAgent.ID,
		clientMessageID,
		messageType,
		content,
		payload,
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
	)
}

func (s *replyCommitService) updateCommitTrace(input replyCommitInput, commitStartedAt time.Time, replyMessage *models.Message, commitMessages []map[string]any, err error) {
	if input.Trace != nil {
		input.Trace.CommitMs = time.Since(commitStartedAt).Milliseconds()
		input.Trace.ReplySent = err == nil && len(commitMessages) > 0
		if len(commitMessages) > 0 && input.Trace.FinalAction != "resource" {
			input.Trace.FinalAction = "reply"
		}
		if replyMessage != nil {
			input.Trace.ReplyMessageID = replyMessage.ID
		}
		if len(commitMessages) > 0 {
			finishReason := "committed_reply"
			if hasOnlyStructuredCommitMessages(commitMessages) {
				input.Trace.FinalAction = "resource"
				finishReason = "committed_structured_resources"
			} else if hasStructuredCommitMessages(commitMessages) {
				input.Trace.FinalAction = "resource"
				finishReason = "committed_reply_and_structured_resources"
			}
			updateRuntimeTraceCommitOutput(input.Trace, commitMessagesReplyText(commitMessages), finishReason, commitMessages)
		}
	}
}

func (s *replyCommitService) sendStructuredVariableReply(input replyCommitInput, structured structuredVariableReply) (*models.Message, error) {
	if strings.TrimSpace(structured.Content) == "" || strings.TrimSpace(structured.Payload) == "" {
		return nil, nil
	}
	commitStartedAt := time.Now()
	replyMessage, err := svc.MessageService.SendAIMessageWithRequestID(
		input.Conversation.ID,
		input.AIAgent.ID,
		fmt.Sprintf("%s_%s_%d", strings.TrimSpace(input.ClientPrefix), strings.TrimSpace(structured.ResourceType), input.Message.ID),
		structured.MessageType,
		structured.Content,
		structured.Payload,
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
	)
	structuredReplyText := structuredRunLogReplyText(structured)
	if input.Trace != nil {
		input.Trace.CommitMs = time.Since(commitStartedAt).Milliseconds()
		input.Trace.ReplySent = err == nil && replyMessage != nil
		input.Trace.FinalAction = "resource"
		input.Trace.ReplyMessageType = string(structured.MessageType)
		if replyMessage != nil {
			input.Trace.ReplyMessageID = replyMessage.ID
		}
		updateRuntimeTraceOutput(input.Trace, structuredReplyText, "committed_structured_"+structured.ResourceType)
	}
	if err != nil || !input.IncrementRound {
		return replyMessage, err
	}
	if err := s.IncrementAIReplyRounds(input.Conversation.ID, input.Conversation.AIReplyRounds+1, input.AIAgent.Name); err != nil {
		return nil, err
	}
	return replyMessage, err
}

func (s *replyCommitService) buildStructuredVariableReply(input replyCommitInput) (structuredVariableReply, bool) {
	resourceTypes := structuredVariableResourceTypesFromTrace(input.Trace)
	if len(resourceTypes) == 0 {
		return structuredVariableReply{}, false
	}
	replies := s.buildStructuredVariableReplies(input)
	if len(replies) == 0 {
		return structuredVariableReply{}, false
	}
	return replies[0], true
}

func (s *replyCommitService) buildStructuredVariableReplies(input replyCommitInput) []structuredVariableReply {
	resourceTypes := structuredVariableResourceTypesFromTrace(input.Trace)
	if len(resourceTypes) == 0 {
		return nil
	}
	instance := s.resolveWxWorkInstance(input.Conversation.ID)
	if instance == nil {
		for _, resourceType := range resourceTypes {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, "", 0, "missing", "当前会话未绑定企微员工号")})
		}
		return nil
	}
	ret := make([]structuredVariableReply, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		switch resourceType {
		case "location":
			content, payload, err := svc.WxWorkProtocolDefaultResourceService.BuildDefaultLocationMessage(instance)
			if err != nil {
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeLocation), 0, "missing", err.Error())})
				continue
			}
			reply := structuredVariableReply{
				ResourceType: resourceType,
				MessageType:  enums.IMMessageTypeLocation,
				Content:      content,
				Payload:      payload,
			}
			appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(reply.MessageType), 0, "prepared", "")})
			ret = append(ret, reply)
		case "mini_program":
			content, payload, err := svc.WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(instance)
			if err != nil {
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeMiniProgram), 0, "missing", err.Error())})
				continue
			}
			reply := structuredVariableReply{
				ResourceType: resourceType,
				MessageType:  enums.IMMessageTypeMiniProgram,
				Content:      content,
				Payload:      payload,
			}
			appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(reply.MessageType), 0, "prepared", "")})
			ret = append(ret, reply)
		case "phone":
			content, payload, err := svc.WxWorkProtocolDefaultResourceService.BuildDefaultPhoneMessage(instance)
			if err != nil {
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeText), 0, "missing", err.Error())})
				continue
			}
			reply := structuredVariableReply{
				ResourceType: resourceType,
				MessageType:  enums.IMMessageTypeText,
				Content:      content,
				Payload:      payload,
			}
			appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(reply.MessageType), 0, "prepared", "")})
			ret = append(ret, reply)
		}
	}
	return ret
}

func (s *replyCommitService) resolveWxWorkInstance(conversationID int64) *models.WxWorkProtocolInstance {
	route := svc.ConversationRouteService.GetByConversationID(conversationID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return nil
	}
	return svc.WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
}

func structuredVariableResourceTypeFromTrace(trace *aiReplyTraceData) string {
	resourceTypes := structuredVariableResourceTypesFromTrace(trace)
	if len(resourceTypes) == 0 {
		return ""
	}
	return resourceTypes[0]
}

func structuredVariableResourceTypesFromTrace(trace *aiReplyTraceData) []string {
	if trace == nil || len(trace.Runtime) == 0 {
		return nil
	}
	data := struct {
		Pipeline struct {
			Intent struct {
				MatchedIntentCode string   `json:"matchedIntentCode"`
				PrimaryIntent     string   `json:"primaryIntent"`
				NeedsResource     bool     `json:"needsResource"`
				ResourceAction    string   `json:"resourceAction"`
				ResourceActions   []string `json:"resourceActions"`
				ResourceType      string   `json:"resourceType"`
				SubIntent         string   `json:"subIntent"`
			} `json:"intent"`
		} `json:"pipeline"`
	}{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return nil
	}
	intent := data.Pipeline.Intent
	if !intent.NeedsResource {
		return nil
	}
	if strings.TrimSpace(intent.PrimaryIntent) != "hotel_variable" &&
		strings.TrimSpace(intent.MatchedIntentCode) != "hotel_variable" &&
		len(intent.ResourceActions) == 0 &&
		strings.TrimSpace(intent.ResourceAction) == "" {
		return nil
	}
	ret := make([]string, 0, len(intent.ResourceActions)+1)
	add := func(resourceType string) {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" || resourceType == "store_group" || resourceType == "store_variable" {
			return
		}
		for _, existing := range ret {
			if existing == resourceType {
				return
			}
		}
		ret = append(ret, resourceType)
	}
	for _, action := range intent.ResourceActions {
		add(structuredVariableResourceTypeFromAction(action))
	}
	add(structuredVariableResourceTypeFromAction(intent.ResourceAction))
	for _, value := range []string{intent.ResourceType, intent.SubIntent} {
		switch strings.TrimSpace(value) {
		case "location":
			add("location")
		case "mini_program":
			add("mini_program")
		case "phone":
			add("phone")
		}
	}
	return ret
}

func structuredVariableResourceTypeFromAction(action string) string {
	switch strings.TrimSpace(action) {
	case "provide_location":
		return "location"
	case "send_miniprogram", "provide_mini_program":
		return "mini_program"
	case "provide_phone":
		return "phone"
	default:
		return ""
	}
}

func structuredRunLogReplyText(structured structuredVariableReply) string {
	switch structured.MessageType {
	case enums.IMMessageTypeLocation:
		return "[位置] " + strings.TrimSpace(structured.Content)
	case enums.IMMessageTypeMiniProgram:
		return "[小程序] " + strings.TrimSpace(structured.Content)
	default:
		return strings.TrimSpace(structured.Content)
	}
}

func updateRuntimeTraceOutput(trace *aiReplyTraceData, replyText string, finishReason string) {
	updateRuntimeTraceCommitOutput(trace, replyText, finishReason, nil)
}

func updateRuntimeTraceCommitOutput(trace *aiReplyTraceData, replyText string, finishReason string, commitMessages []map[string]any) {
	if trace == nil || len(trace.Runtime) == 0 {
		return
	}
	data := map[string]any{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return
	}
	output, _ := data["output"].(map[string]any)
	if output == nil {
		output = map[string]any{}
		data["output"] = output
	}
	output["replyText"] = strings.TrimSpace(replyText)
	output["finishReason"] = strings.TrimSpace(finishReason)
	if len(commitMessages) > 0 {
		output["commitMessages"] = commitMessages
		appendActionLedgerItemsToRuntimeData(data, "committedActions", commitMessagesToCommittedActionLedgerItems(commitMessages))
	}
	raw, err := json.Marshal(data)
	if err == nil {
		trace.Runtime = raw
	}
}

func buildCommitMessageTrace(message *models.Message, messageType enums.IMMessageType, resourceType string, content string, err error) map[string]any {
	item := map[string]any{
		"messageType":  string(messageType),
		"resourceType": strings.TrimSpace(resourceType),
		"content":      strings.TrimSpace(content),
	}
	if message != nil {
		item["messageId"] = message.ID
	}
	if err != nil {
		item["status"] = "error"
		item["errorMessage"] = err.Error()
	} else {
		item["status"] = "sent"
	}
	return item
}

func appendRuntimeTraceActionLedger(trace *aiReplyTraceData, field string, items []map[string]any) {
	if trace == nil || len(trace.Runtime) == 0 || len(items) == 0 {
		return
	}
	data := map[string]any{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return
	}
	appendActionLedgerItemsToRuntimeData(data, field, items)
	raw, err := json.Marshal(data)
	if err == nil {
		trace.Runtime = raw
	}
}

func appendActionLedgerItemsToRuntimeData(data map[string]any, field string, items []map[string]any) {
	if data == nil || strings.TrimSpace(field) == "" || len(items) == 0 {
		return
	}
	ledger, _ := data["actionLedger"].(map[string]any)
	if ledger == nil {
		ledger = map[string]any{}
		data["actionLedger"] = ledger
	}
	existing, _ := ledger[field].([]any)
	for _, item := range items {
		if len(item) == 0 || actionLedgerContains(existing, item) {
			continue
		}
		existing = append(existing, item)
	}
	if len(existing) > 0 {
		ledger[field] = existing
	}
}

func actionLedgerContains(existing []any, item map[string]any) bool {
	key := actionLedgerDedupeKey(item)
	if key == "" {
		return false
	}
	for _, raw := range existing {
		current, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if actionLedgerDedupeKey(current) == key {
			return true
		}
	}
	return false
}

func actionLedgerDedupeKey(item map[string]any) string {
	action := strings.TrimSpace(fmt.Sprint(item["action"]))
	resourceType := strings.TrimSpace(fmt.Sprint(item["resourceType"]))
	messageID := strings.TrimSpace(fmt.Sprint(item["messageId"]))
	status := strings.TrimSpace(fmt.Sprint(item["status"]))
	reason := strings.TrimSpace(fmt.Sprint(item["reason"]))
	return strings.Join([]string{action, resourceType, messageID, status, reason}, "|")
}

func buildResourceActionLedgerItem(resourceType string, messageType string, messageID int64, status string, reason string) map[string]any {
	resourceType = strings.TrimSpace(resourceType)
	item := map[string]any{
		"action":       actionFromStructuredVariableResourceType(resourceType),
		"resourceType": resourceType,
		"status":       strings.TrimSpace(status),
	}
	if strings.TrimSpace(messageType) != "" {
		item["messageType"] = strings.TrimSpace(messageType)
	}
	if messageID > 0 {
		item["messageId"] = messageID
	}
	if strings.TrimSpace(reason) != "" {
		item["reason"] = strings.TrimSpace(reason)
	}
	return item
}

func actionFromStructuredVariableResourceType(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case "location":
		return "provide_location"
	case "mini_program":
		return "provide_mini_program"
	case "phone":
		return "provide_phone"
	default:
		return strings.TrimSpace(resourceType)
	}
}

func commitMessagesToCommittedActionLedgerItems(commitMessages []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(commitMessages))
	for _, message := range commitMessages {
		if strings.TrimSpace(fmt.Sprint(message["status"])) != "sent" {
			continue
		}
		resourceType := strings.TrimSpace(fmt.Sprint(message["resourceType"]))
		if resourceType == "" {
			continue
		}
		var messageID int64
		switch value := message["messageId"].(type) {
		case int64:
			messageID = value
		case int:
			messageID = int64(value)
		case float64:
			messageID = int64(value)
		}
		items = append(items, buildResourceActionLedgerItem(resourceType, strings.TrimSpace(fmt.Sprint(message["messageType"])), messageID, "committed", ""))
	}
	return items
}

func hasStructuredCommitMessages(items []map[string]any) bool {
	for _, item := range items {
		if strings.TrimSpace(fmt.Sprint(item["resourceType"])) != "" {
			return true
		}
	}
	return false
}

func hasOnlyStructuredCommitMessages(items []map[string]any) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(fmt.Sprint(item["resourceType"])) == "" {
			return false
		}
	}
	return true
}

func commitMessagesReplyText(items []map[string]any) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(fmt.Sprint(item["status"])) == "error" {
			continue
		}
		if content := strings.TrimSpace(fmt.Sprint(item["content"])); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *replyCommitService) CommitAIReply(input replyCommitInput) (*models.Message, error) {
	input.IncrementRound = true
	return s.SendAIReply(input)
}

func splitReplyTextForCommit(trace *aiReplyTraceData, replyText string) []string {
	replyText = strings.TrimSpace(replyText)
	if replyText == "" {
		return nil
	}
	normalized := strings.ReplaceAll(replyText, "\r\n", "\n")
	for _, marker := range []string{"<<NEXT_MESSAGE>>", "<NEXT_MESSAGE>", "[[NEXT_MESSAGE]]"} {
		if strings.Contains(normalized, marker) {
			return splitReplyTextByMarker(normalized, marker)
		}
	}
	if textCommitTaskCountFromTrace(trace) <= 1 {
		return []string{replyText}
	}
	parts := splitReplyTextByBlankLine(normalized)
	if len(parts) > 1 {
		return parts
	}
	return []string{replyText}
}

func splitReplyTextByMarker(text string, marker string) []string {
	rawParts := strings.Split(text, marker)
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return []string{strings.TrimSpace(text)}
	}
	return parts
}

func splitReplyTextByBlankLine(text string) []string {
	lines := strings.Split(text, "\n")
	parts := make([]string, 0)
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		part := strings.TrimSpace(strings.Join(current, "\n"))
		if part != "" {
			parts = append(parts, part)
		}
		current = nil
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return parts
}

func textCommitTaskCountFromTrace(trace *aiReplyTraceData) int {
	if trace == nil || len(trace.Runtime) == 0 {
		return 0
	}
	data := struct {
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []struct {
					Intent string `json:"intent"`
					Output string `json:"output"`
				} `json:"taskPlans"`
			} `json:"replyPlan"`
		} `json:"pipeline"`
	}{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return 0
	}
	count := 0
	for _, task := range data.Pipeline.ReplyPlan.TaskPlans {
		output := strings.TrimSpace(task.Output)
		intent := strings.TrimSpace(task.Intent)
		if output == "structured_resource_commit" || output == "human_route_confirmation_or_dispatch" || intent == "hotel_variable" {
			continue
		}
		if output == "" && intent == "" {
			continue
		}
		count++
	}
	return count
}

func (s *replyCommitService) IncrementAIReplyRounds(conversationID int64, nextRounds int, aiAgentName string) error {
	return repositories.ConversationRepository.Updates(sqls.DB(), conversationID, map[string]any{
		"ai_reply_rounds":  nextRounds,
		"update_user_id":   0,
		"update_user_name": strings.TrimSpace(aiAgentName),
		"updated_at":       time.Now(),
	})
}

func (s *replyCommitService) buildAIPrincipal(aiAgent models.AIAgent) *dto.AuthPrincipal {
	username := "AI"
	if strings.TrimSpace(aiAgent.Name) != "" {
		username = aiAgent.Name
	}
	return &dto.AuthPrincipal{
		UserID:   0,
		Username: username,
		Nickname: username,
	}
}
