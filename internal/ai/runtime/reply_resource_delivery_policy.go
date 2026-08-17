package runtime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	svc "agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const recentResourceDedupeWindow = 10 * time.Minute

type recentAIResourceDelivery struct {
	ResourceType string
	Fingerprint  string
	Message      models.Message
	Outbox       *models.ChannelMessageOutbox
}

type resourceResendDirective struct {
	Requested   bool
	Ambiguous   bool
	TargetTypes map[string]bool
}

func (s *replyCommitService) applyRecentResourceDeliveryPolicy(input replyCommitInput, replies []structuredVariableReply, replyText string) ([]structuredVariableReply, string) {
	filtered, text, _ := s.applyRecentResourceDeliveryPolicyDetailed(input, replies, replyText)
	return filtered, text
}

func (s *replyCommitService) applyRecentResourceDeliveryPolicyDetailed(input replyCommitInput, replies []structuredVariableReply, replyText string) ([]structuredVariableReply, string, []svc.AIReplyTurnActionSuppression) {
	if len(replies) == 0 || strings.TrimSpace(input.ClientPrefix) != "ai_reply" || strings.HasPrefix(strings.TrimSpace(input.Message.RequestID), "manual_resume_") {
		return replies, replyText, nil
	}
	replies, suppressions := s.dedupeCurrentResourceBatch(input, replies)
	previous := s.findRecentAIResourceDeliveries(input)
	if len(previous) == 0 {
		return replies, replyText, suppressions
	}

	directive := detectResourceResendDirective(input.Message.Content, previous)
	previousByFingerprint := make(map[string]recentAIResourceDelivery, len(previous))
	for _, item := range previous {
		if item.Fingerprint != "" {
			previousByFingerprint[item.Fingerprint] = item
		}
	}

	filtered := make([]structuredVariableReply, 0, len(replies))
	if suppressions == nil {
		suppressions = make([]svc.AIReplyTurnActionSuppression, 0, len(replies))
	}
	suppressedTypes := make([]string, 0, len(replies))
	pendingReused := false
	for _, reply := range replies {
		fingerprint := structuredResourceFingerprint(input.Conversation.StoreID, reply)
		previousItem, duplicate := previousByFingerprint[fingerprint]
		if !duplicate || fingerprint == "" {
			filtered = append(filtered, reply)
			continue
		}

		if directive.Ambiguous {
			suppressedTypes = appendUniqueResourceType(suppressedTypes, reply.ResourceType)
			s.recordResourceDeliveryDecision(input.Trace, reply, "suppressed", "ambiguous_resend_requires_clarification")
			suppressions = appendPreparedActionSuppression(suppressions, reply, previousItem, "ambiguous_resend_requires_clarification")
			continue
		}

		if recentResourceDeliveryPending(previousItem) {
			pendingReused = true
			suppressedTypes = appendUniqueResourceType(suppressedTypes, reply.ResourceType)
			s.expediteRecentResourceDelivery(previousItem)
			s.recordResourceDeliveryDecision(input.Trace, reply, "reused", "pending_delivery_reused")
			suppressions = appendPreparedActionSuppression(suppressions, reply, previousItem, "pending_delivery_reused")
			continue
		}

		if directive.Requested && directive.TargetTypes[reply.ResourceType] {
			filtered = append(filtered, reply)
			s.recordResourceDeliveryDecision(input.Trace, reply, "allowed", "explicit_resend_allowed")
			continue
		}

		suppressedTypes = appendUniqueResourceType(suppressedTypes, reply.ResourceType)
		s.recordResourceDeliveryDecision(input.Trace, reply, "suppressed", "recent_duplicate_suppressed")
		suppressions = appendPreparedActionSuppression(suppressions, reply, previousItem, "recent_duplicate_suppressed")
	}

	switch {
	case directive.Ambiguous && len(suppressedTypes) > 0:
		replyText = appendResourcePolicyNotice(replyText, ambiguousResourceResendNotice(previous), true)
	case pendingReused:
		replyText = appendResourcePolicyNotice(replyText, "刚才的内容正在重新发送，请稍等一下。", true)
	case len(filtered) == 0 && len(suppressedTypes) > 0 && strings.TrimSpace(replyText) == "":
		replyText = recentResourceAlreadySentNotice(suppressedTypes)
	}
	return filtered, strings.TrimSpace(replyText), suppressions
}

func (s *replyCommitService) dedupeCurrentResourceBatch(input replyCommitInput, replies []structuredVariableReply) ([]structuredVariableReply, []svc.AIReplyTurnActionSuppression) {
	if len(replies) < 2 {
		return replies, nil
	}
	filtered := make([]structuredVariableReply, 0, len(replies))
	indexByFingerprint := make(map[string]int, len(replies))
	suppressions := make([]svc.AIReplyTurnActionSuppression, 0, len(replies)-1)
	for _, reply := range replies {
		fingerprint := structuredResourceFingerprint(input.Conversation.StoreID, reply)
		if fingerprint == "" {
			filtered = append(filtered, reply)
			continue
		}
		keptIndex, duplicate := indexByFingerprint[fingerprint]
		if !duplicate {
			indexByFingerprint[fingerprint] = len(filtered)
			filtered = append(filtered, reply)
			continue
		}
		kept := &filtered[keptIndex]
		keptActionKey := strings.TrimSpace(kept.ActionKey)
		duplicateActionKey := strings.TrimSpace(reply.ActionKey)
		if duplicateActionKey != "" && keptActionKey != "" && duplicateActionKey != keptActionKey {
			suppressions = append(suppressions, svc.AIReplyTurnActionSuppression{
				ActionKey: duplicateActionKey, TaskKey: strings.TrimSpace(reply.TaskKey),
				PreparedRevision:   strings.TrimSpace(reply.PreparedRevision),
				CoveredByActionKey: keptActionKey,
				ResultCode:         "same_batch_duplicate_suppressed",
			})
		} else if taskKey := strings.TrimSpace(reply.TaskKey); taskKey != "" {
			kept.CoveredTaskKeys = appendUniqueCommitTaskKeys(kept.CoveredTaskKeys, taskKey)
		}
		s.recordResourceDeliveryDecision(input.Trace, reply, "suppressed", "same_batch_duplicate_suppressed")
	}
	return filtered, suppressions
}

func appendPreparedActionSuppression(items []svc.AIReplyTurnActionSuppression, reply structuredVariableReply, previous recentAIResourceDelivery, reason string) []svc.AIReplyTurnActionSuppression {
	actionKey := strings.TrimSpace(reply.ActionKey)
	if actionKey == "" {
		return items
	}
	for _, item := range items {
		if item.ActionKey == actionKey {
			return items
		}
	}
	return append(items, svc.AIReplyTurnActionSuppression{
		ActionKey: actionKey, TaskKey: strings.TrimSpace(reply.TaskKey),
		PreparedRevision:   strings.TrimSpace(reply.PreparedRevision),
		CoveredByMessageID: previous.Message.ID, ResultCode: strings.TrimSpace(reason),
	})
}

func (s *replyCommitService) findRecentAIResourceDeliveries(input replyCommitInput) []recentAIResourceDelivery {
	if sqls.DB() == nil || input.Conversation.ID <= 0 || input.Conversation.TenantID <= 0 || input.Message.ID <= 0 || input.Message.SessionNo <= 0 {
		return nil
	}
	messages := svc.MessageService.Find(sqls.NewCnd().
		Eq("tenant_id", input.Conversation.TenantID).
		Eq("conversation_id", input.Conversation.ID).
		Eq("session_no", input.Message.SessionNo).
		Eq("sender_type", enums.IMSenderTypeAI).
		Eq("historical_only", false).
		Lt("id", input.Message.ID).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Desc("id").
		Limit(30))
	if len(messages) == 0 {
		return nil
	}
	latest := messages[0]
	currentAt := resourcePolicyMessageTime(input.Message)
	latestAt := resourcePolicyMessageTime(latest)
	if currentAt.IsZero() || latestAt.IsZero() || currentAt.Before(latestAt) || currentAt.Sub(latestAt) > recentResourceDedupeWindow {
		return nil
	}
	blockers := svc.MessageService.Find(sqls.NewCnd().
		Eq("tenant_id", input.Conversation.TenantID).
		Eq("conversation_id", input.Conversation.ID).
		Eq("session_no", input.Message.SessionNo).
		Eq("historical_only", false).
		In("sender_type", []string{string(enums.IMSenderTypeCustomer), string(enums.IMSenderTypeAgent)}).
		Gt("id", latest.ID).
		Lt("id", input.Message.ID).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("id"))
	for _, blocker := range blockers {
		if blocker.SenderType == enums.IMSenderTypeAgent || input.Message.AIReplyTurnID <= 0 || blocker.AIReplyTurnID != input.Message.AIReplyTurnID {
			return nil
		}
	}

	requestID := strings.TrimSpace(latest.RequestID)
	ret := make([]recentAIResourceDelivery, 0, 3)
	for _, message := range messages {
		if requestID != "" {
			if strings.TrimSpace(message.RequestID) != requestID {
				continue
			}
		} else if message.ID != latest.ID {
			continue
		}
		resourceType := resourceTypeForCommittedMessage(message)
		if resourceType == "" {
			continue
		}
		fingerprint := committedResourceFingerprint(input.Conversation.StoreID, resourceType, message.MessageType, message.Payload)
		if fingerprint == "" {
			continue
		}
		outbox := recentResourceMessageOutbox(message)
		ret = append(ret, recentAIResourceDelivery{
			ResourceType: resourceType,
			Fingerprint:  fingerprint,
			Message:      message,
			Outbox:       outbox,
		})
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Message.ID < ret[j].Message.ID })
	return ret
}

func recentResourceMessageOutbox(message models.Message) *models.ChannelMessageOutbox {
	channelType := strings.TrimSpace(message.OutboundChannelType)
	if channelType != "" {
		if outbox := svc.ChannelMessageOutboxService.GetByMessageIDInTenant(channelType, message.ID, message.TenantID); outbox != nil {
			return outbox
		}
	}
	return svc.ChannelMessageOutboxService.FindOne(sqls.NewCnd().
		Eq("tenant_id", message.TenantID).
		Eq("conversation_id", message.ConversationID).
		Eq("message_id", message.ID))
}

func resourcePolicyMessageTime(message models.Message) time.Time {
	if message.SentAt != nil && !message.SentAt.IsZero() {
		return *message.SentAt
	}
	return message.CreatedAt
}

func resourceTypeForCommittedMessage(message models.Message) string {
	switch message.MessageType {
	case enums.IMMessageTypeImage:
		return "knowledge_image"
	case enums.IMMessageTypeLocation:
		return "location"
	case enums.IMMessageTypeMiniProgram:
		return "mini_program"
	default:
		return ""
	}
}

func structuredResourceFingerprint(storeID int64, reply structuredVariableReply) string {
	return committedResourceFingerprint(storeID, reply.ResourceType, reply.MessageType, reply.Payload)
}

func committedResourceFingerprint(storeID int64, resourceType string, messageType enums.IMMessageType, payload string) string {
	resourceType = strings.TrimSpace(resourceType)
	switch resourceType {
	case "knowledge_image":
		var body struct {
			AssetID string `json:"assetId"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &body) != nil || strings.TrimSpace(body.AssetID) == "" {
			return ""
		}
		return "knowledge_image:" + strings.TrimSpace(body.AssetID)
	case "location":
		var body struct {
			Longitude float64 `json:"longitude"`
			Latitude  float64 `json:"latitude"`
			Address   string  `json:"address"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &body) != nil || body.Longitude == 0 || body.Latitude == 0 {
			return ""
		}
		return fmt.Sprintf("location:%d:%.6f:%.6f:%s", storeID, body.Longitude, body.Latitude, normalizeResourceFingerprintText(body.Address))
	case "mini_program":
		body := map[string]any{}
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &body) != nil {
			return ""
		}
		appID := firstResourceMapString(body, "appid", "appId", "app_id")
		pagePath := normalizeMiniProgramFingerprintPath(firstResourceMapString(body, "page_path", "pagePath", "path"))
		businessID := firstResourceMapString(body, "businessResourceId", "resourceId", "storeId", "storeID", "storeCode")
		if businessID == "" {
			businessID = miniProgramQueryBusinessID(pagePath)
		}
		if businessID == "" && storeID > 0 {
			businessID = strconv.FormatInt(storeID, 10)
		}
		if appID == "" {
			return ""
		}
		return strings.Join([]string{"mini_program", strings.ToLower(appID), pagePath, businessID}, ":")
	default:
		_ = messageType
		return ""
	}
}

func normalizeResourceFingerprintText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func firstResourceMapString(body map[string]any, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(body[key]))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func normalizeMiniProgramFingerprintPath(pagePath string) string {
	pagePath = strings.TrimSpace(pagePath)
	if pagePath == "" {
		return ""
	}
	base := pagePath
	rawQuery := ""
	if index := strings.Index(pagePath, "?"); index >= 0 {
		base = pagePath[:index]
		rawQuery = pagePath[index+1:]
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil || len(values) == 0 {
		return base
	}
	return base + "?" + values.Encode()
}

func miniProgramQueryBusinessID(pagePath string) string {
	index := strings.Index(pagePath, "?")
	if index < 0 || index+1 >= len(pagePath) {
		return ""
	}
	values, err := url.ParseQuery(pagePath[index+1:])
	if err != nil {
		return ""
	}
	for _, key := range []string{"bindTicket", "resourceId", "storeId", "storeCode", "id"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func detectResourceResendDirective(text string, previous []recentAIResourceDelivery) resourceResendDirective {
	normalized := strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.TrimSpace(text)))
	requested := containsAnyResourcePhrase(normalized, []string{
		"再发", "重发", "重新发", "发一遍", "再来一遍",
		"没收到", "未收到", "没有收到", "没看到", "看不到", "打不开", "点不开",
		"图片呢", "照片呢", "定位呢", "小程序呢", "卡片呢",
	})
	directive := resourceResendDirective{Requested: requested, TargetTypes: map[string]bool{}}
	if !requested {
		return directive
	}
	if containsAnyResourcePhrase(normalized, []string{"图片", "照片", "看图", "图呢"}) {
		directive.TargetTypes["knowledge_image"] = true
	}
	if containsAnyResourcePhrase(normalized, []string{"定位", "地址", "导航", "位置"}) {
		directive.TargetTypes["location"] = true
	}
	if containsAnyResourcePhrase(normalized, []string{"小程序", "卡片", "入住入口"}) {
		directive.TargetTypes["mini_program"] = true
	}
	if len(directive.TargetTypes) == 0 {
		if len(previous) == 1 {
			directive.TargetTypes[previous[0].ResourceType] = true
		} else if len(previous) > 1 {
			directive.Ambiguous = true
		}
	}
	return directive
}

func containsAnyResourcePhrase(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func recentResourceDeliveryPending(item recentAIResourceDelivery) bool {
	if item.Outbox == nil {
		return false
	}
	switch enums.ChannelMessageOutboxStatus(strings.TrimSpace(item.Outbox.SendStatus)) {
	case enums.ChannelMessageOutboxStatusPending, enums.ChannelMessageOutboxStatusSending, enums.ChannelMessageOutboxStatusFailed:
		return true
	default:
		return false
	}
}

func (s *replyCommitService) expediteRecentResourceDelivery(item recentAIResourceDelivery) {
	if item.Outbox == nil {
		return
	}
	status := enums.ChannelMessageOutboxStatus(strings.TrimSpace(item.Outbox.SendStatus))
	if status != enums.ChannelMessageOutboxStatusPending && status != enums.ChannelMessageOutboxStatusFailed {
		return
	}
	now := time.Now()
	if err := svc.ChannelMessageOutboxService.UpdatesInTenant(item.Outbox.ID, item.Outbox.TenantID, map[string]any{
		"next_retry_at": now,
		"updated_at":    now,
	}); err != nil {
		slog.Warn("expedite reused AI resource outbox failed",
			"tenant_id", item.Outbox.TenantID,
			"conversation_id", item.Outbox.ConversationID,
			"message_id", item.Outbox.MessageID,
			"outbox_id", item.Outbox.ID,
			"error", err,
		)
	}
}

func (s *replyCommitService) recordResourceDeliveryDecision(trace *aiReplyTraceData, reply structuredVariableReply, status string, reason string) {
	appendRuntimeTraceActionLedger(trace, "suppressedActions", []map[string]any{
		buildResourceActionLedgerItem(reply.ResourceType, string(reply.MessageType), 0, status, reason),
	})
}

func appendUniqueResourceType(items []string, resourceType string) []string {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return items
	}
	for _, item := range items {
		if item == resourceType {
			return items
		}
	}
	return append(items, resourceType)
}

func appendResourcePolicyNotice(replyText string, notice string, force bool) string {
	replyText = strings.TrimSpace(replyText)
	notice = strings.TrimSpace(notice)
	if notice == "" || replyText != "" && !force {
		return replyText
	}
	if replyText == "" {
		return notice
	}
	return replyText + "\n\n" + notice
}

func recentResourceAlreadySentNotice(resourceTypes []string) string {
	if len(resourceTypes) == 1 {
		switch resourceTypes[0] {
		case "location":
			return "刚才的定位还在上面，可以直接点开查看。"
		case "mini_program":
			return "刚才的小程序卡片还在上面，可以直接点开。"
		case "knowledge_image":
			return "刚才的图片还在上面，可以直接查看。"
		}
	}
	labels := resourceTypeLabels(resourceTypes)
	if len(labels) == 0 {
		return "刚才的内容还在上面，可以直接查看。"
	}
	return "刚才的" + strings.Join(labels, "和") + "还在上面，可以直接查看。"
}

func ambiguousResourceResendNotice(previous []recentAIResourceDelivery) string {
	types := make([]string, 0, len(previous))
	for _, item := range previous {
		types = appendUniqueResourceType(types, item.ResourceType)
	}
	labels := resourceTypeLabels(types)
	if len(labels) == 0 {
		return "想让我重新发哪一个内容？"
	}
	return "想让我重新发哪一个：" + strings.Join(labels, "、") + "？"
}

func resourceTypeLabels(resourceTypes []string) []string {
	labels := make([]string, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		label := ""
		switch resourceType {
		case "location":
			label = "定位"
		case "mini_program":
			label = "小程序卡片"
		case "knowledge_image":
			label = "图片"
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}
