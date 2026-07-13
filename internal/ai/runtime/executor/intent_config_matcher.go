package executor

import (
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"github.com/mlogclub/simple/sqls"
)

type runtimeIntentScope struct {
	CompanyID        int64
	StoreID          int64
	WxWorkInstanceID int64
	CustomerID       int64
	IntentProfileID  int64
	HasWxWorkAccount bool
}

func loadEnabledIntentConfigs(scope runtimeIntentScope) []models.ReplyIntentConfig {
	var list []models.ReplyIntentConfig
	db := sqls.DB()
	if db == nil {
		return list
	}
	if scope.IntentProfileID <= 0 {
		return list
	}
	err := db.Where("status = ?", enums.StatusOk).
		Where("intent_profile_id = ?", scope.IntentProfileID).
		Where("(scope_type = ? OR (scope_type = ? AND company_id = ?) OR (scope_type = ? AND store_id = ?) OR (scope_type = ? AND wx_work_instance_id = ?))", "global", "company", scope.CompanyID, "store", scope.StoreID, "instance", scope.WxWorkInstanceID).
		Order("priority DESC").Order("sort_no ASC").Order("id ASC").Find(&list).Error
	if err != nil {
		return nil
	}
	return collapseIntentConfigsByScope(list)
}

func promptTraceFromConfig(config models.ReplyIntentConfig, intent callbacks.IntentTraceData) callbacks.IntentPromptTraceData {
	instructions := []string{
		"先按当前用户问题回答，历史只作辅助。",
		"不要假承诺任何未执行的真实动作或处理结果。",
		"回复像微信真人，通常 1-3 句。",
	}
	if promptPack := strings.TrimSpace(config.PromptPack); promptPack != "" {
		instructions = append(instructions, splitIntentLines(promptPack)...)
	}
	if template := strings.TrimSpace(config.ReplyPlanTemplate); template != "" {
		instructions = append(instructions, "回复计划模板："+template)
	}
	if validation := strings.TrimSpace(config.ValidationRules); validation != "" {
		instructions = append(instructions, "发送前校验："+validation)
	}
	return callbacks.IntentPromptTraceData{PackName: intent.PrimaryIntent, Instructions: instructions}
}

func collapseIntentConfigsByScope(list []models.ReplyIntentConfig) []models.ReplyIntentConfig {
	selected := make(map[string]models.ReplyIntentConfig, len(list))
	order := make([]string, 0, len(list))
	for _, item := range list {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		current, exists := selected[code]
		if !exists {
			selected[code] = item
			order = append(order, code)
			continue
		}
		if intentConfigRank(item) > intentConfigRank(current) {
			selected[code] = item
		}
	}
	ret := make([]models.ReplyIntentConfig, 0, len(order))
	for _, code := range order {
		ret = append(ret, selected[code])
	}
	return ret
}

func intentScopeRank(config models.ReplyIntentConfig) int {
	switch strings.TrimSpace(config.ScopeType) {
	case "instance":
		return 4
	case "store":
		return 3
	case "company":
		return 2
	default:
		return 1
	}
}

func intentConfigRank(config models.ReplyIntentConfig) int {
	rank := intentScopeRank(config) * 10
	if config.IntentProfileID > 0 {
		rank += 1
	}
	return rank
}

func findInteractionConfig(configs []models.ReplyIntentConfig) (models.ReplyIntentConfig, bool) {
	for _, config := range configs {
		if canonicalIntentCode(config.Code) == "interaction" {
			return config, true
		}
	}
	return models.ReplyIntentConfig{}, false
}

func canonicalIntentCode(code string) string {
	switch strings.TrimSpace(code) {
	case "hotel_info":
		return "hotel_info"
	case "service_request":
		return "service_request"
	case "human_complaint_risk":
		return "human_complaint_risk"
	case "handoff", "complaint_or_risk":
		return "human_complaint_risk"
	case "interaction", "social_confirm", "thanks_confirm", "social", "unknown_clarify", "unknown_or_clarify":
		return "interaction"
	case "hotel_variable":
		return "hotel_variable"
	default:
		return strings.TrimSpace(code)
	}
}

func resolveRuntimeIntentScope(req RunInput) runtimeIntentScope {
	scope := runtimeIntentScope{CustomerID: req.Conversation.CustomerID}
	db := sqls.DB()
	if db == nil {
		return scope
	}
	if req.Conversation.ID <= 0 || !db.Migrator().HasTable(&models.ConversationRouteState{}) {
		return scope
	}
	state := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", req.Conversation.ID)
	if state != nil {
		scope.StoreID = state.StoreID
		scope.WxWorkInstanceID = state.WxWorkInstanceID
	}
	if scope.WxWorkInstanceID > 0 {
		instance := repositories.WxWorkProtocolInstanceRepository.Get(db, scope.WxWorkInstanceID)
		if instance != nil {
			scope.HasWxWorkAccount = true
			if instance.IntentProfileID > 0 {
				scope.IntentProfileID = instance.IntentProfileID
			}
			if instance.CompanyID > 0 {
				scope.CompanyID = instance.CompanyID
			}
			if scope.StoreID <= 0 {
				scope.StoreID = instance.StoreID
			}
			if scope.StoreID > 0 && scope.CompanyID <= 0 {
				store := repositories.StoreRepository.Get(db, scope.StoreID)
				if store != nil {
					scope.CompanyID = store.CompanyID
				}
			}
		}
	}
	if scope.StoreID > 0 && scope.CompanyID <= 0 {
		store := repositories.StoreRepository.Get(db, scope.StoreID)
		if store != nil {
			scope.CompanyID = store.CompanyID
		}
	}
	if scope.IntentProfileID <= 0 && scope.CompanyID > 0 {
		company := repositories.CompanyRepository.Get(db, scope.CompanyID)
		if company != nil && company.IntentProfileID > 0 {
			scope.IntentProfileID = company.IntentProfileID
		}
	}
	if scope.IntentProfileID <= 0 && !scope.HasWxWorkAccount {
		if profile := repositories.ReplyIntentProfileRepository.Take(db, "code = ? AND status = ?", replyintent.DefaultHotelProfileCode, enums.StatusOk); profile != nil {
			scope.IntentProfileID = profile.ID
		}
	}
	return scope
}

func resolveRuntimeIntentProfile(scope runtimeIntentScope) *models.ReplyIntentProfile {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.ReplyIntentProfile{}) {
		return nil
	}
	if scope.IntentProfileID > 0 {
		if profile := repositories.ReplyIntentProfileRepository.Get(db, scope.IntentProfileID); profile != nil && profile.Status == enums.StatusOk {
			return profile
		}
	}
	if scope.HasWxWorkAccount {
		return nil
	}
	if profile := repositories.ReplyIntentProfileRepository.Take(db, "code = ? AND status = ?", replyintent.DefaultHotelProfileCode, enums.StatusOk); profile != nil {
		return profile
	}
	return repositories.ReplyIntentProfileRepository.Take(db, "status = ?", enums.StatusOk)
}

func intentContextMatches(config models.ReplyIntentConfig, req RunInput, history adapter.HistoryBuildResult, mediaText string) bool {
	required := strings.ToLower(strings.TrimSpace(config.RequiredContext))
	if required == "" {
		return true
	}
	terms := splitIntentTerms(required)
	for _, term := range terms {
		switch strings.ToLower(term) {
		case "media", "image", "图片", "媒体":
			if !isRuntimeMediaMessage(req.UserMessage.MessageType) && !hasRecentMediaContext(history) && strings.TrimSpace(mediaText) == "" {
				return false
			}
		case "text", "文本":
			if strings.TrimSpace(req.UserMessage.Content) == "" {
				return false
			}
		case "memory", "summary", "记忆", "压缩记忆":
			if strings.TrimSpace(history.MemorySource) == "" {
				return false
			}
		}
	}
	return true
}

func currentAndRecentMediaText(req RunInput, history adapter.HistoryBuildResult) string {
	parts := make([]string, 0)
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		mediaText, mediaSummary, status := utilsMediaUnderstanding(req.UserMessage)
		if strings.TrimSpace(status) == "understood" {
			parts = append(parts, mediaText, mediaSummary)
		}
	}
	for i := len(history.RawItems) - 1; i >= 0 && len(parts) < 8; i-- {
		item := history.RawItems[i]
		if !isRuntimeMediaMessage(item.MessageType) {
			continue
		}
		mediaText, mediaSummary, status := utilsMediaUnderstanding(item)
		if strings.TrimSpace(status) != "understood" {
			continue
		}
		parts = append(parts, mediaText, mediaSummary)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func utilsMediaUnderstanding(message models.Message) (string, string, string) {
	return utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
}

func splitIntentTerms(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == '|'
	})
}

func splitIntentLines(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func normalizeConfiguredIntentText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}
