package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	svc "agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

type replyCommitService struct{}

type replyCommitInput struct {
	Conversation              models.Conversation
	Message                   models.Message
	AIAgent                   models.AIAgent
	ReplyText                 string
	ReplyParts                []contracts.ReplyPartV2
	PreparedActions           []contracts.PreparedActionV1
	ActionLedgerV2            *contracts.ActionLedgerV1
	ActionLedgerAuthoritative bool
	Trace                     *aiReplyTraceData
	ClientPrefix              string
	IncrementRound            bool
	JobID                     int64
}

type structuredVariableReply struct {
	ResourceType     string
	ResourceRef      string
	ActionKey        string
	TaskKey          string
	Sequence         int
	PreparedRevision string
	MessageType      enums.IMMessageType
	Content          string
	Payload          string
}

func newReplyCommitService() *replyCommitService {
	return &replyCommitService{}
}

func (s *replyCommitService) HasStructuredVariableReply(trace *aiReplyTraceData) bool {
	return len(structuredVariableResourceTypesFromTrace(trace)) > 0 || len(knowledgeResourceItemsFromTrace(trace)) > 0
}

func (s *replyCommitService) SendAIReply(input replyCommitInput) (*models.Message, error) {
	messages, err := s.SendAIReplyBatch(input)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	return &messages[len(messages)-1], nil
}

func (s *replyCommitService) SendAIReplyBatch(input replyCommitInput) ([]models.Message, error) {
	structuredReplies := preparedStructuredReplies(input.PreparedActions)
	if input.ActionLedgerAuthoritative {
		if err := validateAuthoritativePreparedActions(input, structuredReplies); err != nil {
			return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, err)
		}
	} else if len(structuredReplies) == 0 {
		var err error
		structuredReplies, err = s.buildStructuredVariableRepliesStrict(input)
		if err != nil {
			return nil, err
		}
		knowledgeReplies, knowledgeErr := s.buildKnowledgeResourceRepliesStrict(input)
		if knowledgeErr != nil {
			return nil, knowledgeErr
		}
		structuredReplies = append(structuredReplies, knowledgeReplies...)
	}
	replyText := strings.TrimSpace(input.ReplyText)
	isManualResume := strings.HasPrefix(strings.TrimSpace(input.Message.RequestID), "manual_resume_")
	if isManualResume {
		if replyText == "" {
			replyText = manualResumeCustomerNotice
		} else {
			replyText = manualResumeCustomerNotice + "\n<<NEXT_MESSAGE>>\n" + replyText
		}
	}
	var suppressedActions []svc.AIReplyTurnActionSuppression
	structuredReplies, replyText, suppressedActions = s.applyRecentResourceDeliveryPolicyDetailed(input, structuredReplies, replyText)
	if replyText == "" && len(structuredReplies) == 0 {
		return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorEmptyOutput, nil)
	}
	commitStartedAt := time.Now()
	drafts := make([]svc.AIOutboundMessageDraft, 0, len(structuredReplies)+1)
	type commitMetadata struct {
		messageType  enums.IMMessageType
		resourceType string
		content      string
		taskID       string
	}
	metadata := make([]commitMetadata, 0, len(structuredReplies)+1)
	if replyText != "" {
		textParts := normalizedCommitReplyParts(input.ReplyParts, replyText)
		textMessages := make([]string, 0, len(textParts))
		textTaskGroups := make([][]string, 0, len(textParts))
		for _, part := range textParts {
			textMessages = append(textMessages, strings.TrimSpace(part.Content))
			textTaskGroups = append(textTaskGroups, append([]string(nil), part.TaskKeys...))
		}
		if len(textMessages) == 0 {
			textMessages = splitReplyTextForCommit(input.Trace, replyText)
			textTaskGroups = textCommitTaskKeyGroupsFromTrace(input.Trace)
		}
		for index, text := range textMessages {
			clientMessageID := fmt.Sprintf("%s_%d", strings.TrimSpace(input.ClientPrefix), input.Message.ID)
			if len(textMessages) > 1 {
				clientMessageID = fmt.Sprintf("%s_text_%d_%d", strings.TrimSpace(input.ClientPrefix), index+1, input.Message.ID)
			}
			taskKeys := []string(nil)
			if index < len(textTaskGroups) {
				taskKeys = append([]string(nil), textTaskGroups[index]...)
				if len(taskKeys) > 0 {
					clientMessageID = stableTurnClientMsgID(input, "text", index+1, taskKeys)
				}
			}
			taskIndex := index
			if isManualResume && len(textMessages) > 1 {
				if index == 0 {
					taskIndex = -1
				} else {
					taskIndex = index - 1
				}
			}
			drafts = append(drafts, svc.AIOutboundMessageDraft{ClientMsgID: clientMessageID, MessageType: enums.IMMessageTypeText, Content: text, TaskKeys: taskKeys})
			metadata = append(metadata, commitMetadata{messageType: enums.IMMessageTypeText, content: text, taskID: textCommitTaskIDFromTrace(input.Trace, taskIndex)})
		}
	}
	for index, structured := range structuredReplies {
		taskKeys := []string(nil)
		if strings.TrimSpace(structured.TaskKey) != "" {
			taskKeys = []string{strings.TrimSpace(structured.TaskKey)}
		} else {
			taskKeys = structuredCommitTaskKeysFromTrace(input.Trace, structured.ResourceType, index)
		}
		clientMessageID := fmt.Sprintf("%s_%s_%d_%d", strings.TrimSpace(input.ClientPrefix), strings.TrimSpace(structured.ResourceType), index+1, input.Message.ID)
		if strings.TrimSpace(structured.ActionKey) != "" {
			clientMessageID = stableTurnClientMsgID(input, "action", index+1, []string{structured.ActionKey})
		} else if len(taskKeys) > 0 {
			clientMessageID = stableTurnClientMsgID(input, structured.ResourceType, index+1, taskKeys)
		}
		drafts = append(drafts, svc.AIOutboundMessageDraft{
			ClientMsgID: clientMessageID,
			MessageType: structured.MessageType, Content: structured.Content, Payload: structured.Payload, TaskKeys: taskKeys,
			ActionKey: structured.ActionKey, ActionPreparedRevision: structured.PreparedRevision,
		})
		metadata = append(metadata, commitMetadata{messageType: structured.MessageType, resourceType: structured.ResourceType, content: structuredRunLogReplyText(structured), taskID: structured.TaskKey})
	}
	messages, err := svc.MessageService.SendAIMessageBatchForTurnWithRequestIDAndActions(
		input.Conversation.ID,
		input.AIAgent.ID,
		drafts,
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
		input.Message.AIReplyTurnID,
		input.Message.AIReplyTurnVersion,
		input.JobID,
		suppressedActions,
	)
	if err != nil {
		var covered *svc.AIReplyTurnCoveredError
		if errors.Is(err, svc.ErrAIReplyTurnStale) || errors.Is(err, svc.ErrAIReplyTurnDuplicateAnswer) || errors.As(err, &covered) {
			s.updateCommitTrace(input, commitStartedAt, nil, nil, err)
			return nil, err
		}
		controlledErr := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, err)
		s.updateCommitTrace(input, commitStartedAt, nil, nil, controlledErr)
		return nil, controlledErr
	}
	if len(messages) != len(metadata) {
		controlledErr := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorCommitFailed, fmt.Errorf("committed message count mismatch"))
		s.updateCommitTrace(input, commitStartedAt, nil, nil, controlledErr)
		return nil, controlledErr
	}
	commitMessages := make([]map[string]any, 0, len(messages))
	for index := range messages {
		item := buildCommitMessageTrace(&messages[index], metadata[index].messageType, metadata[index].resourceType, metadata[index].content, nil)
		if metadata[index].taskID != "" {
			item["taskId"] = metadata[index].taskID
		}
		commitMessages = append(commitMessages, item)
	}
	replyMessage := &messages[len(messages)-1]
	s.updateCommitTrace(input, commitStartedAt, replyMessage, commitMessages, nil)
	return messages, nil
}

func validateAuthoritativePreparedActions(input replyCommitInput, replies []structuredVariableReply) error {
	if input.ActionLedgerV2 == nil {
		if len(input.PreparedActions) == 0 {
			return nil
		}
		return fmt.Errorf("authoritative action ledger is unavailable")
	}
	expected := make(map[string]contracts.ActionLedgerItemV1)
	for _, action := range input.ActionLedgerV2.Actions {
		if !commitOutboundActionType(action.ActionType) {
			continue
		}
		if action.Status != "prepared" {
			return fmt.Errorf("authoritative action %s is not prepared", action.ActionKey)
		}
		expected[strings.TrimSpace(action.ActionKey)] = action
	}
	seen := make(map[string]struct{}, len(input.PreparedActions))
	for _, prepared := range input.PreparedActions {
		actionKey := strings.TrimSpace(prepared.ActionKey)
		ledgerAction, ok := expected[actionKey]
		if !ok || actionKey == "" || strings.TrimSpace(prepared.TaskKey) != ledgerAction.TaskKey || strings.TrimSpace(prepared.ActionType) != ledgerAction.ActionType || strings.TrimSpace(prepared.PreparedRevision) == "" {
			return fmt.Errorf("prepared action %s is outside the authoritative ledger", actionKey)
		}
		if _, duplicate := seen[actionKey]; duplicate {
			return fmt.Errorf("prepared action %s is duplicated", actionKey)
		}
		seen[actionKey] = struct{}{}
	}
	if len(seen) != len(expected) || len(replies) != len(expected) {
		return fmt.Errorf("authoritative prepared action set is incomplete")
	}
	return nil
}

func commitOutboundActionType(actionType string) bool {
	switch strings.TrimSpace(actionType) {
	case "send_location", "send_mini_program", "send_phone", "send_knowledge_image":
		return true
	default:
		return false
	}
}

const manualResumeCustomerNotice = "同事暂时没能接入，接下来我先继续帮你处理。"

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
	if strings.TrimSpace(structured.Content) == "" || (structuredPayloadRequired(structured) && strings.TrimSpace(structured.Payload) == "") {
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
	replies, _ := s.buildStructuredVariableRepliesStrict(input)
	return replies
}

func (s *replyCommitService) buildStructuredVariableRepliesStrict(input replyCommitInput) ([]structuredVariableReply, error) {
	resourceTypes := structuredVariableResourceTypesFromTrace(input.Trace)
	if len(resourceTypes) == 0 {
		return nil, nil
	}
	instance := s.resolveWxWorkInstance(input.Conversation.ID, input.Conversation.TenantID)
	if instance == nil {
		for _, resourceType := range resourceTypes {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, "", 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
		}
		return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, fmt.Errorf("runtime instance unavailable"))
	}
	ret := make([]structuredVariableReply, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		switch resourceType {
		case "location":
			content, payload, err := svc.WxWorkProtocolDefaultResourceService.BuildDefaultLocationMessage(instance)
			if err != nil {
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeLocation), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
				return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, err)
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
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeMiniProgram), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
				return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, err)
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
			content, payload, err := svc.WxWorkProtocolDefaultResourceService.BuildRuntimePhoneMessage(instance)
			if err != nil {
				appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(enums.IMMessageTypeText), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
				return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, err)
			}
			reply := structuredVariableReply{
				ResourceType: resourceType,
				MessageType:  enums.IMMessageTypeText,
				Content:      content,
				Payload:      payload,
			}
			appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(resourceType, string(reply.MessageType), 0, "prepared", "")})
			ret = append(ret, reply)
		default:
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem(resourceType, "", 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
			return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, fmt.Errorf("unsupported resource type"))
		}
	}
	return ret, nil
}

func structuredPayloadRequired(structured structuredVariableReply) bool {
	return structured.ResourceType != "phone"
}

func (s *replyCommitService) buildKnowledgeResourceReplies(input replyCommitInput) []structuredVariableReply {
	replies, _ := s.buildKnowledgeResourceRepliesStrict(input)
	return replies
}

func (s *replyCommitService) buildKnowledgeResourceRepliesStrict(input replyCommitInput) ([]structuredVariableReply, error) {
	resources := knowledgeResourceItemsFromTrace(input.Trace)
	if len(resources) == 0 {
		return nil, nil
	}
	if s.resolveWxWorkInstance(input.Conversation.ID, input.Conversation.TenantID) == nil {
		appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
		return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, fmt.Errorf("runtime instance unavailable"))
	}
	ret := make([]structuredVariableReply, 0, len(resources))
	for _, resource := range resources {
		asset := svc.AssetService.GetByAssetID(resource.AssetID)
		if input.Conversation.TenantID > 0 {
			asset = svc.AssetService.GetByAssetIDInTenant(resource.AssetID, input.Conversation.TenantID)
		}
		if asset == nil || asset.Status != enums.AssetStatusSuccess {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
			return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, fmt.Errorf("knowledge image unavailable"))
		}
		payload, err := svc.BuildIMMessageAssetPayload(asset)
		if err != nil {
			appendRuntimeTraceActionLedger(input.Trace, "missingActions", []map[string]any{buildResourceActionLedgerItem("knowledge_image", string(enums.IMMessageTypeImage), 0, "missing", string(svc.AIReplyExecutionErrorResourceInvariantBroken))})
			return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorResourceInvariantBroken, err)
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
		}
		appendRuntimeTraceActionLedger(input.Trace, "preparedActions", []map[string]any{buildResourceActionLedgerItem(reply.ResourceType, string(reply.MessageType), 0, "prepared", "")})
		ret = append(ret, reply)
	}
	return ret, nil
}

func (s *replyCommitService) resolveWxWorkInstance(conversationID, tenantID int64) *models.WxWorkProtocolInstance {
	route := svc.ConversationRouteService.GetByConversationID(conversationID)
	if tenantID > 0 {
		route = svc.ConversationRouteService.GetByConversationIDInTenant(conversationID, tenantID)
	}
	if route == nil || route.WxWorkInstanceID <= 0 {
		return nil
	}
	var instance *models.WxWorkProtocolInstance
	if tenantID > 0 {
		instance = svc.WxWorkProtocolInstanceService.GetByTenantID(route.WxWorkInstanceID, tenantID)
	} else {
		instance = svc.WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
	}
	runtimeInstance, err := svc.StoreService.HydrateRuntimeInstanceDB(sqls.DB(), instance)
	if err != nil {
		return nil
	}
	return runtimeInstance
}

func structuredVariableResourceTypeFromTrace(trace *aiReplyTraceData) string {
	resourceTypes := structuredVariableResourceTypesFromTrace(trace)
	if len(resourceTypes) == 0 {
		return ""
	}
	return resourceTypes[0]
}

type knowledgeResourceTraceItem struct {
	AssetID string `json:"assetId"`
	Title   string `json:"title"`
	SortNo  int    `json:"sortNo"`
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
	seen := map[string]bool{}
	for _, item := range data.KnowledgeResources {
		item.AssetID = strings.TrimSpace(item.AssetID)
		item.Title = strings.TrimSpace(item.Title)
		if item.AssetID == "" || seen[item.AssetID] {
			continue
		}
		seen[item.AssetID] = true
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

func preparedStructuredReplies(actions []contracts.PreparedActionV1) []structuredVariableReply {
	ret := make([]structuredVariableReply, 0, len(actions))
	for _, action := range actions {
		messageType := enums.IMMessageType(strings.TrimSpace(action.MessageType))
		if strings.TrimSpace(action.ActionKey) == "" || strings.TrimSpace(action.TaskKey) == "" ||
			strings.TrimSpace(action.PreparedRevision) == "" || strings.TrimSpace(action.Content) == "" ||
			(messageType != enums.IMMessageTypeText && strings.TrimSpace(action.Payload) == "") || messageType == "" {
			continue
		}
		resourceType := strings.TrimSpace(action.ResourceType)
		if action.ActionType == "send_knowledge_image" {
			resourceType = "knowledge_image"
		}
		ret = append(ret, structuredVariableReply{
			ResourceType: resourceType, ResourceRef: strings.TrimSpace(action.ResourceRef),
			ActionKey: action.ActionKey, TaskKey: action.TaskKey, Sequence: action.Sequence,
			PreparedRevision: action.PreparedRevision, MessageType: messageType,
			Content: strings.TrimSpace(action.Content), Payload: strings.TrimSpace(action.Payload),
		})
	}
	sort.SliceStable(ret, func(i, j int) bool {
		if ret[i].Sequence == ret[j].Sequence {
			return ret[i].ActionKey < ret[j].ActionKey
		}
		return ret[i].Sequence < ret[j].Sequence
	})
	return ret
}

func normalizedCommitReplyParts(parts []contracts.ReplyPartV2, replyText string) []contracts.ReplyPartV2 {
	if len(parts) == 0 {
		return nil
	}
	ret := make([]contracts.ReplyPartV2, 0, len(parts))
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		part.Content = strings.TrimSpace(part.Content)
		part.TaskKeys = uniqueCommitStrings(part.TaskKeys)
		if part.Content == "" || len(part.TaskKeys) == 0 {
			return nil
		}
		texts = append(texts, part.Content)
		ret = append(ret, part)
	}
	if strings.TrimSpace(strings.Join(texts, "\n<<NEXT_MESSAGE>>\n")) != strings.TrimSpace(replyText) {
		return nil
	}
	return ret
}

func uniqueCommitStrings(items []string) []string {
	ret := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		ret = append(ret, item)
	}
	return ret
}

func stableTurnClientMsgID(input replyCommitInput, kind string, partIndex int, stableKeys []string) string {
	turnID := input.Message.AIReplyTurnID
	keys := uniqueCommitStrings(stableKeys)
	sort.Strings(keys)
	payload := fmt.Sprintf("%d/%d/%d/%s/%d/%s",
		input.Conversation.TenantID,
		input.Conversation.ID,
		turnID,
		strings.TrimSpace(kind),
		partIndex,
		strings.Join(keys, ","),
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:48]
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

func (s *replyCommitService) CommitAIReplyBatch(input replyCommitInput) ([]models.Message, error) {
	input.IncrementRound = true
	return s.SendAIReplyBatch(input)
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

func capReplyTextParts(parts []string, limit int) []string {
	if limit <= 0 || len(parts) <= limit {
		return parts
	}
	ret := append([]string(nil), parts[:limit-1]...)
	ret = append(ret, strings.Join(parts[limit-1:], "\n\n"))
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
	return len(textCommitTaskKeyGroupsFromTrace(trace))
}

func textCommitTaskIDFromTrace(trace *aiReplyTraceData, index int) string {
	if index < 0 {
		return ""
	}
	groups := textCommitTaskKeyGroupsFromTrace(trace)
	if index >= len(groups) || len(groups[index]) == 0 {
		return ""
	}
	return groups[index][0]
}

func textCommitTaskKeyGroupsFromTrace(trace *aiReplyTraceData) [][]string {
	if trace == nil || len(trace.Runtime) == 0 {
		return nil
	}
	data := struct {
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []struct {
					TaskKey     string `json:"taskKey"`
					AnswerGroup string `json:"answerGroup"`
					Intent      string `json:"intent"`
					Output      string `json:"output"`
				} `json:"taskPlans"`
			} `json:"replyPlan"`
		} `json:"pipeline"`
	}{}
	if err := json.Unmarshal(trace.Runtime, &data); err != nil {
		return nil
	}
	groups := make([][]string, 0, 3)
	groupIndexes := make(map[string]int, len(data.Pipeline.ReplyPlan.TaskPlans))
	for index, task := range data.Pipeline.ReplyPlan.TaskPlans {
		output := strings.TrimSpace(task.Output)
		intent := strings.TrimSpace(task.Intent)
		if output == "structured_resource_commit" || output == "human_route_confirmation_or_dispatch" || intent == "hotel_variable" {
			continue
		}
		if output == "" && intent == "" {
			continue
		}
		taskKey := strings.TrimSpace(task.TaskKey)
		if taskKey == "" {
			taskKey = fmt.Sprintf("task-%d", index+1)
		}
		groupKey := strings.TrimSpace(task.AnswerGroup)
		if groupKey == "" {
			groupKey = taskKey
		}
		if groupIndex, exists := groupIndexes[groupKey]; exists {
			groups[groupIndex] = append(groups[groupIndex], taskKey)
			continue
		}
		groupIndexes[groupKey] = len(groups)
		groups = append(groups, []string{taskKey})
	}
	if len(groups) <= 3 {
		return groups
	}
	ret := append([][]string(nil), groups[:2]...)
	merged := make([]string, 0)
	for _, group := range groups[2:] {
		merged = append(merged, group...)
	}
	return append(ret, merged)
}

func structuredCommitTaskKeysFromTrace(trace *aiReplyTraceData, resourceType string, occurrence int) []string {
	resourceType = strings.TrimSpace(resourceType)
	if trace == nil || len(trace.Runtime) == 0 || resourceType == "" || resourceType == "knowledge_image" {
		return nil
	}
	data := struct {
		Pipeline struct {
			ReplyPlan struct {
				TaskPlans []struct {
					TaskKey        string `json:"taskKey"`
					Intent         string `json:"intent"`
					Output         string `json:"output"`
					ResourceAction string `json:"resourceAction"`
				} `json:"taskPlans"`
			} `json:"replyPlan"`
		} `json:"pipeline"`
	}{}
	if json.Unmarshal(trace.Runtime, &data) != nil {
		return nil
	}
	matched := 0
	for index, task := range data.Pipeline.ReplyPlan.TaskPlans {
		if task.Output != "structured_resource_commit" && task.Intent != "hotel_variable" && strings.TrimSpace(task.ResourceAction) == "" {
			continue
		}
		if structuredVariableResourceTypeFromAction(task.ResourceAction) != resourceType && strings.TrimSpace(task.ResourceAction) != resourceType {
			continue
		}
		if matched != occurrence {
			matched++
			continue
		}
		taskKey := strings.TrimSpace(task.TaskKey)
		if taskKey == "" {
			taskKey = fmt.Sprintf("task-%d", index+1)
		}
		return []string{taskKey}
	}
	return nil
}

func stableTaskClientMsgID(prefix, kind, taskKey string, sourceMessageID int64) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "ai_reply"
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "message"
	}
	taskKey = strings.TrimSpace(taskKey)
	if len(taskKey) > 64 {
		taskKey = taskKey[len(taskKey)-64:]
	}
	return fmt.Sprintf("%s_%s_%s_%d", prefix, kind, taskKey, sourceMessageID)
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
