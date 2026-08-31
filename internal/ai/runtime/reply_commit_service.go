package runtime

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	runtimeexecutor "agent-desk/internal/ai/runtime/executor"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"
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
	TaskIDs      []string
}

type textCommitPart struct {
	Content               string
	TaskIDs               []string
	FallbackResourceType  string
	FallbackResourceTypes []string
}

func newReplyCommitService() *replyCommitService {
	return &replyCommitService{}
}

func (s *replyCommitService) HasStructuredVariableReply(trace *aiReplyTraceData) bool {
	return len(structuredVariableResourceTypesFromTrace(trace)) > 0 || len(knowledgeResourceItemsFromTrace(trace)) > 0
}

func (s *replyCommitService) SendAIReply(input replyCommitInput) (*models.Message, error) {
	structuredReplies := s.buildStructuredVariableReplies(input)
	structuredReplies = append(structuredReplies, s.buildKnowledgeResourceReplies(input)...)
	replyText := strings.TrimSpace(input.ReplyText)
	var err error
	replyText, err = runtimeexecutor.SanitizeGeneratedReplyText(replyText)
	if err != nil {
		return nil, fmt.Errorf("refusing to commit unsafe generated reply: %w", err)
	}
	if containsInternalReplyProtocolShape(replyText) {
		return nil, fmt.Errorf("refusing to commit internal reply protocol payload")
	}
	isManualResume := strings.HasPrefix(strings.TrimSpace(input.Message.RequestID), "manual_resume_")
	textParts := buildTextCommitParts(input.Trace, replyText)
	textParts = bindFallbackResourceTextParts(input.Trace, structuredReplies, textParts)
	if isManualResume {
		textParts = append([]textCommitPart{{Content: manualResumeCustomerNotice}}, textParts...)
		textParts = capTextCommitParts(textParts, 3)
	}
	if len(textParts) == 0 && len(structuredReplies) == 0 {
		return nil, nil
	}
	clientPrefix := replyCommitClientPrefix(input)
	commitStartedAt := time.Now()
	var replyMessage *models.Message
	commitMessages := make([]map[string]any, 0, len(structuredReplies)+1)
	if len(textParts) > 0 {
		for index, part := range textParts {
			clientMessageID := textCommitClientMessageID(input, clientPrefix, part, index, len(textParts))
			message, err := s.sendAIMessage(input, clientMessageID, enums.IMMessageTypeText, part.Content, "")
			traceItem := buildCommitMessageTrace(message, enums.IMMessageTypeText, "", part.Content, part.TaskIDs, err)
			addFallbackResourceTypesToCommitTrace(traceItem, part)
			commitMessages = append(commitMessages, traceItem)
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
			structuredCommitClientMessageID(input, clientPrefix, structured, index),
			structured.MessageType,
			structured.Content,
			structured.Payload,
		)
		commitMessages = append(commitMessages, buildCommitMessageTrace(message, structured.MessageType, structured.ResourceType, structuredRunLogReplyText(structured), structured.TaskIDs, err))
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

func containsInternalReplyProtocolShape(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(normalized, "replyparts") || strings.Contains(normalized, "coveredfactids") {
		return true
	}
	return strings.Contains(normalized, "taskid")
}

const manualResumeCustomerNotice = "同事暂时没能接入，接下来我先继续帮你处理。"

func replyCommitClientPrefix(input replyCommitInput) string {
	prefix := strings.TrimSpace(input.ClientPrefix)
	requestID := strings.TrimSpace(input.Message.RequestID)
	if !strings.HasPrefix(requestID, "manual_resume_") {
		return prefix
	}
	sum := sha256.Sum256([]byte(requestID))
	return fmt.Sprintf("ai_manual_resume_%x", sum[:24])
}

func (s *replyCommitService) sendAIMessage(input replyCommitInput, clientMessageID string, messageType enums.IMMessageType, content string, payload string) (*models.Message, error) {
	message, err := svc.MessageService.SendAIMessageWithRequestIDAndSourceMessageID(
		input.Conversation.ID,
		input.AIAgent.ID,
		clientMessageID,
		messageType,
		content,
		payload,
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
		input.Message.ID,
	)
	if err != nil {
		return message, err
	}
	if err := validateCommittedMessageMatchesRequest(message, messageType, content, payload, input.Message.RequestID, clientMessageID); err != nil {
		return message, err
	}
	return message, nil
}

func validateCommittedMessageMatchesRequest(message *models.Message, messageType enums.IMMessageType, content string, payload string, requestID string, clientMessageIDs ...string) error {
	if message == nil {
		return fmt.Errorf("message commit returned no persisted message")
	}
	if message.MessageType != messageType {
		return fmt.Errorf("persisted message type %q does not match requested type %q", message.MessageType, messageType)
	}
	if strings.TrimSpace(message.RequestID) != strings.TrimSpace(requestID) {
		return fmt.Errorf("persisted message request ID does not match the current request")
	}
	if len(clientMessageIDs) > 0 && replyruntime.IsStableManualResumeClientMessageID(clientMessageIDs[0]) {
		return nil
	}
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment:
		requestedAssetID := commitPayloadAssetID(payload)
		persistedAssetID := commitPayloadAssetID(message.Payload)
		if requestedAssetID != "" || persistedAssetID != "" {
			if requestedAssetID != persistedAssetID {
				return fmt.Errorf("persisted media asset does not match the current request")
			}
			return nil
		}
		if !commitPayloadEquivalent(message.Payload, payload) {
			return fmt.Errorf("persisted media payload does not match the current request")
		}
		return nil
	default:
		if strings.TrimSpace(message.Content) != strings.TrimSpace(content) {
			return fmt.Errorf("persisted message content does not match the current request")
		}
		if !commitPayloadEquivalent(message.Payload, payload) {
			return fmt.Errorf("persisted message payload does not match the current request")
		}
		return nil
	}
}

func textCommitClientMessageID(input replyCommitInput, clientPrefix string, part textCommitPart, index int, total int) string {
	if !strings.HasPrefix(strings.TrimSpace(input.Message.RequestID), "manual_resume_") {
		if total <= 1 {
			return fmt.Sprintf("%s_%d", clientPrefix, input.Message.ID)
		}
		return fmt.Sprintf("%s_text_%d_%d", clientPrefix, index+1, input.Message.ID)
	}
	if strings.TrimSpace(part.Content) == manualResumeCustomerNotice && len(normalizeCommitTaskIDs(part.TaskIDs)) == 0 {
		return fmt.Sprintf("%s_notice_%d", clientPrefix, input.Message.ID)
	}
	if taskIDs := normalizeCommitTaskIDs(part.TaskIDs); len(taskIDs) > 0 {
		return fmt.Sprintf("%s_task_%s_%d", clientPrefix, commitOwnershipHash("text", taskIDs), input.Message.ID)
	}
	return fmt.Sprintf("%s_text_%d_%d", clientPrefix, index+1, input.Message.ID)
}

func structuredCommitClientMessageID(input replyCommitInput, clientPrefix string, structured structuredVariableReply, index int) string {
	resourceType := strings.TrimSpace(structured.ResourceType)
	if strings.HasPrefix(strings.TrimSpace(input.Message.RequestID), "manual_resume_") {
		if taskIDs := normalizeCommitTaskIDs(structured.TaskIDs); len(taskIDs) > 0 {
			return fmt.Sprintf("%s_resource_%s_%d", clientPrefix, commitOwnershipHash(resourceType, taskIDs), input.Message.ID)
		}
	}
	return fmt.Sprintf("%s_%s_%d_%d", clientPrefix, resourceType, index+1, input.Message.ID)
}

func commitOwnershipHash(kind string, taskIDs []string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(kind) + "\x00" + strings.Join(normalizeCommitTaskIDs(taskIDs), "\x00")))
	return fmt.Sprintf("%x", sum[:8])
}

func commitPayloadAssetID(payload string) string {
	var data struct {
		AssetID string `json:"assetId"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(payload)), &data) != nil {
		return ""
	}
	return strings.TrimSpace(data.AssetID)
}

func commitPayloadEquivalent(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == right {
		return true
	}
	if left == "" || right == "" {
		return false
	}
	var leftValue any
	var rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	leftJSON, leftErr := json.Marshal(leftValue)
	rightJSON, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
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
	replyMessage, err := svc.MessageService.SendAIMessageWithRequestIDAndSourceMessageID(
		input.Conversation.ID,
		input.AIAgent.ID,
		fmt.Sprintf("%s_%s_%d", replyCommitClientPrefix(input), strings.TrimSpace(structured.ResourceType), input.Message.ID),
		structured.MessageType,
		structured.Content,
		structured.Payload,
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
		input.Message.ID,
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
				TaskIDs:      resourceCommitTaskIDsFromTrace(input.Trace, resourceType),
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
				TaskIDs:      resourceCommitTaskIDsFromTrace(input.Trace, resourceType),
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
				TaskIDs:      resourceCommitTaskIDsFromTrace(input.Trace, resourceType),
			}
			appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(reply.MessageType), 0, "prepared", "")})
			ret = append(ret, reply)
		}
	}
	return ret
}

func (s *replyCommitService) buildKnowledgeResourceReplies(input replyCommitInput) []structuredVariableReply {
	resources := knowledgeResourceItemsFromTrace(input.Trace)
	if len(resources) == 0 {
		return nil
	}
	if s.resolveWxWorkInstance(input.Conversation.ID) == nil {
		appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", "当前会话未绑定企微员工号")})
		return nil
	}
	ret := make([]structuredVariableReply, 0, len(resources))
	for _, resource := range resources {
		asset := svc.AssetService.GetByAssetID(resource.AssetID)
		if asset == nil || asset.Status != enums.AssetStatusSuccess {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", "知识图片资产不存在或不可用")})
			continue
		}
		payload, err := svc.BuildIMMessageAssetPayload(asset)
		if err != nil {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", err.Error())})
			continue
		}
		content := strings.TrimSpace(resource.Title)
		if content == "" {
			content = strings.TrimSpace(asset.Filename)
		}
		reply := structuredVariableReply{
			ResourceType: "knowledge_image",
			MessageType:  enums.IMMessageTypeImage,
			Content:      content,
			Payload:      payload,
			TaskIDs:      append([]string(nil), resource.TaskIDs...),
		}
		appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(reply.ResourceType, string(reply.MessageType), 0, "prepared", "")})
		ret = append(ret, reply)
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

type knowledgeResourceTraceItem struct {
	AssetID string   `json:"assetId"`
	Title   string   `json:"title"`
	SortNo  int      `json:"sortNo"`
	TaskIDs []string `json:"taskIds,omitempty"`
}

func knowledgeResourceItemsFromTrace(trace *aiReplyTraceData) []knowledgeResourceTraceItem {
	if trace == nil || len(trace.Runtime) == 0 {
		return nil
	}
	data := struct {
		KnowledgeResources []knowledgeResourceTraceItem `json:"knowledgeResources"`
	}{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return nil
	}
	ret := make([]knowledgeResourceTraceItem, 0, len(data.KnowledgeResources))
	seen := map[string]int{}
	for _, item := range data.KnowledgeResources {
		item.AssetID = strings.TrimSpace(item.AssetID)
		item.Title = strings.TrimSpace(item.Title)
		item.TaskIDs = normalizeCommitTaskIDs(item.TaskIDs)
		if item.AssetID == "" {
			continue
		}
		if index, exists := seen[item.AssetID]; exists {
			ret[index].TaskIDs = normalizeCommitTaskIDs(append(ret[index].TaskIDs, item.TaskIDs...))
			continue
		}
		seen[item.AssetID] = len(ret)
		ret = append(ret, item)
	}
	return ret
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
	case enums.IMMessageTypeImage:
		if structured.ResourceType == "knowledge_image" {
			return "[知识图片] " + strings.TrimSpace(structured.Content)
		}
		return "[图片] " + strings.TrimSpace(structured.Content)
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

func buildCommitMessageTrace(message *models.Message, messageType enums.IMMessageType, resourceType string, content string, taskIDs []string, err error) map[string]any {
	item := map[string]any{
		"messageType":  string(messageType),
		"resourceType": strings.TrimSpace(resourceType),
		"content":      strings.TrimSpace(content),
	}
	if taskIDs = normalizeCommitTaskIDs(taskIDs); len(taskIDs) > 0 {
		item["taskIds"] = taskIDs
	}
	if message != nil {
		item["messageId"] = message.ID
		item["messageType"] = string(message.MessageType)
		item["content"] = strings.TrimSpace(message.Content)
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
	case "knowledge_image":
		return "send_knowledge_image"
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
			return capReplyTextParts(splitReplyTextByMarker(normalized, marker), 3)
		}
	}
	if textCommitTaskCountFromTrace(trace) <= 1 {
		return []string{replyText}
	}
	parts := splitReplyTextByBlankLine(normalized)
	if len(parts) > 1 {
		return capReplyTextParts(parts, 3)
	}
	return []string{replyText}
}

func buildTextCommitParts(trace *aiReplyTraceData, replyText string) []textCommitPart {
	parts := splitReplyTextForCommit(trace, replyText)
	if len(parts) == 0 {
		return nil
	}
	taskGroups := balanceCommitTaskIDs(textCommitTaskIDsFromTrace(trace), len(parts))
	ret := make([]textCommitPart, 0, len(parts))
	for index, part := range parts {
		item := textCommitPart{Content: strings.TrimSpace(part)}
		if index < len(taskGroups) {
			item.TaskIDs = append([]string(nil), taskGroups[index]...)
		}
		if item.Content != "" {
			ret = append(ret, item)
		}
	}
	return ret
}

func bindFallbackResourceTextParts(trace *aiReplyTraceData, structuredReplies []structuredVariableReply, parts []textCommitPart) []textCommitPart {
	if len(parts) == 0 || len(textCommitTaskIDsFromTrace(trace)) > 0 {
		return parts
	}
	committedTypes := make(map[string]struct{}, len(structuredReplies))
	for _, reply := range structuredReplies {
		if resourceType := strings.TrimSpace(reply.ResourceType); resourceType != "" {
			committedTypes[resourceType] = struct{}{}
		}
	}
	fallbackTypes := make([]string, 0)
	for _, resourceType := range structuredVariableResourceTypesFromTrace(trace) {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" {
			continue
		}
		if _, committed := committedTypes[resourceType]; committed {
			continue
		}
		fallbackTypes = append(fallbackTypes, resourceType)
	}
	if len(fallbackTypes) != len(parts) {
		return parts
	}
	for index, resourceType := range fallbackTypes {
		parts[index].FallbackResourceType = resourceType
		parts[index].FallbackResourceTypes = []string{resourceType}
		parts[index].TaskIDs = resourceCommitTaskIDsFromTrace(trace, resourceType)
	}
	return parts
}

func capTextCommitParts(parts []textCommitPart, limit int) []textCommitPart {
	cleaned := make([]textCommitPart, 0, len(parts))
	for _, part := range parts {
		part.Content = strings.TrimSpace(part.Content)
		part.TaskIDs = normalizeCommitTaskIDs(part.TaskIDs)
		setTextCommitPartFallbackResourceTypes(&part, textCommitPartFallbackResourceTypes(part))
		if part.Content != "" {
			cleaned = append(cleaned, part)
		}
	}
	if limit <= 0 || len(cleaned) <= limit {
		return cleaned
	}
	baseSize := len(cleaned) / limit
	extra := len(cleaned) % limit
	ret := make([]textCommitPart, 0, limit)
	start := 0
	for index := 0; index < limit; index++ {
		size := baseSize
		if index < extra {
			size++
		}
		end := start + size
		contents := make([]string, 0, size)
		taskIDs := make([]string, 0)
		fallbackResourceTypes := make([]string, 0)
		for _, part := range cleaned[start:end] {
			contents = append(contents, part.Content)
			taskIDs = append(taskIDs, part.TaskIDs...)
			fallbackResourceTypes = append(fallbackResourceTypes, textCommitPartFallbackResourceTypes(part)...)
		}
		merged := textCommitPart{
			Content: strings.Join(contents, "\n\n"),
			TaskIDs: normalizeCommitTaskIDs(taskIDs),
		}
		setTextCommitPartFallbackResourceTypes(&merged, fallbackResourceTypes)
		ret = append(ret, merged)
		start = end
	}
	return ret
}

func textCommitPartFallbackResourceTypes(part textCommitPart) []string {
	resourceTypes := make([]string, 0, len(part.FallbackResourceTypes)+1)
	resourceTypes = append(resourceTypes, part.FallbackResourceType)
	resourceTypes = append(resourceTypes, part.FallbackResourceTypes...)
	return normalizeFallbackResourceTypes(resourceTypes)
}

func addFallbackResourceTypesToCommitTrace(traceItem map[string]any, part textCommitPart) {
	if traceItem == nil {
		return
	}
	resourceTypes := textCommitPartFallbackResourceTypes(part)
	if len(resourceTypes) == 0 {
		return
	}
	traceItem["fallbackResourceType"] = resourceTypes[0]
	if len(resourceTypes) > 1 {
		traceItem["fallbackResourceTypes"] = resourceTypes
	}
}

func setTextCommitPartFallbackResourceTypes(part *textCommitPart, resourceTypes []string) {
	if part == nil {
		return
	}
	resourceTypes = normalizeFallbackResourceTypes(resourceTypes)
	part.FallbackResourceType = ""
	part.FallbackResourceTypes = nil
	if len(resourceTypes) == 0 {
		return
	}
	part.FallbackResourceType = resourceTypes[0]
	part.FallbackResourceTypes = resourceTypes
}

func normalizeFallbackResourceTypes(resourceTypes []string) []string {
	ret := make([]string, 0, len(resourceTypes))
	seen := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" {
			continue
		}
		if _, exists := seen[resourceType]; exists {
			continue
		}
		seen[resourceType] = struct{}{}
		ret = append(ret, resourceType)
	}
	return ret
}

func balanceCommitTaskIDs(taskIDs []string, groupCount int) [][]string {
	taskIDs = normalizeCommitTaskIDs(taskIDs)
	if len(taskIDs) == 0 || groupCount <= 0 {
		return nil
	}
	if groupCount > len(taskIDs) {
		groupCount = len(taskIDs)
	}
	baseSize := len(taskIDs) / groupCount
	extra := len(taskIDs) % groupCount
	ret := make([][]string, 0, groupCount)
	start := 0
	for index := 0; index < groupCount; index++ {
		size := baseSize
		if index < extra {
			size++
		}
		end := start + size
		ret = append(ret, append([]string(nil), taskIDs[start:end]...))
		start = end
	}
	return ret
}

func normalizeCommitTaskIDs(taskIDs []string) []string {
	ret := make([]string, 0, len(taskIDs))
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if _, exists := seen[taskID]; exists {
			continue
		}
		seen[taskID] = struct{}{}
		ret = append(ret, taskID)
	}
	return ret
}

func capReplyTextParts(parts []string, limit int) []string {
	if limit <= 0 || len(parts) <= limit {
		return parts
	}
	baseSize := len(parts) / limit
	extra := len(parts) % limit
	ret := make([]string, 0, limit)
	start := 0
	for index := 0; index < limit; index++ {
		size := baseSize
		if index < extra {
			size++
		}
		end := start + size
		ret = append(ret, strings.Join(parts[start:end], "\n\n"))
		start = end
	}
	return ret
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
	return len(textCommitTaskIDsFromTrace(trace))
}

func textCommitTaskIDsFromTrace(trace *aiReplyTraceData) []string {
	if trace == nil || len(trace.Runtime) == 0 {
		return nil
	}
	data := struct {
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []struct {
					TaskID        string `json:"taskId"`
					Intent        string `json:"intent"`
					OutputKind    string `json:"outputKind"`
					ReplyRequired bool   `json:"replyRequired"`
					Output        string `json:"output"`
				} `json:"taskPlans"`
			} `json:"replyPlan"`
		} `json:"pipeline"`
	}{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return nil
	}
	taskIDs := make([]string, 0, len(data.Pipeline.ReplyPlan.TaskPlans))
	used := make(map[string]struct{}, len(data.Pipeline.ReplyPlan.TaskPlans))
	for _, task := range data.Pipeline.ReplyPlan.TaskPlans {
		output := strings.TrimSpace(task.Output)
		outputKind := strings.TrimSpace(task.OutputKind)
		intent := strings.TrimSpace(task.Intent)
		if outputKind == "resource" || outputKind == "handoff" || outputKind == "context_only" ||
			output == "structured_resource_commit" || output == "human_route_confirmation_or_dispatch" || intent == "hotel_variable" {
			continue
		}
		if outputKind == "text" && !task.ReplyRequired {
			continue
		}
		if output == "" && intent == "" {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", len(taskIDs)+1)
		}
		if _, exists := used[taskID]; exists {
			taskID = fmt.Sprintf("task-%d", len(taskIDs)+1)
		}
		used[taskID] = struct{}{}
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

func resourceCommitTaskIDsFromTrace(trace *aiReplyTraceData, resourceType string) []string {
	if trace == nil || len(trace.Runtime) == 0 {
		return nil
	}
	data := struct {
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []struct {
					TaskID         string `json:"taskId"`
					Intent         string `json:"intent"`
					SubIntent      string `json:"subIntent"`
					OutputKind     string `json:"outputKind"`
					Output         string `json:"output"`
					NeedsResource  bool   `json:"needsResource"`
					ResourceAction string `json:"resourceAction"`
				} `json:"taskPlans"`
			} `json:"replyPlan"`
		} `json:"pipeline"`
	}{}
	if json.Unmarshal(trace.Runtime, &data) != nil {
		return nil
	}
	resourceType = strings.TrimSpace(resourceType)
	ret := make([]string, 0)
	for _, task := range data.Pipeline.ReplyPlan.TaskPlans {
		if !task.NeedsResource && strings.TrimSpace(task.OutputKind) != "resource" && strings.TrimSpace(task.Output) != "structured_resource_commit" && strings.TrimSpace(task.Intent) != "hotel_variable" {
			continue
		}
		taskResourceType := structuredVariableResourceTypeFromAction(task.ResourceAction)
		if taskResourceType == "" {
			switch strings.TrimSpace(task.SubIntent) {
			case "location", "mini_program", "phone":
				taskResourceType = strings.TrimSpace(task.SubIntent)
			}
		}
		if taskResourceType == resourceType {
			ret = append(ret, task.TaskID)
		}
	}
	return normalizeCommitTaskIDs(ret)
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
