package executor

import (
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

const recentAnsweredTurnWindow = 10 * time.Minute

type recentAnsweredTurn struct {
	CustomerTexts []string
	AITexts       []string
	ResourceTypes []string
}

func buildRecentAnsweredTurnInstruction(req RunInput, history adapter.HistoryBuildResult) string {
	turn := findRecentAnsweredTurn(req, history)
	if turn == nil {
		return ""
	}
	parts := []string{
		"【紧邻上一轮承接】下面内容是刚刚已经完成的上一轮问答，只用于判断当前问题是否在重复确认、补充条件、缩小范围或纠正上一答案，不能把它当成客户本轮的新诉求。",
		"上一轮客户问题：" + preview(strings.Join(turn.CustomerTexts, "；"), 260),
	}
	if len(turn.AITexts) > 0 {
		parts = append(parts, "上一轮 AI 已回复："+preview(strings.Join(turn.AITexts, "；"), 360))
	}
	if len(turn.ResourceTypes) > 0 {
		parts = append(parts, "上一轮已经提交的结构化资源："+strings.Join(turn.ResourceTypes, "、")+"。是否需要再次发送由 Commit 阶段判断，本阶段不要承诺已重发。")
	}
	parts = append(parts,
		"若当前消息是独立新主题，完全忽略上一轮答案，只回答当前主题。",
		"若当前消息只是再次询问同一事实，简短承接上一答案，不要逐字复述；若增加了新条件或缩小了范围，只回答新增差异；若在纠正上一答案，必须依据本轮重新检索到的知识回答。",
		"不得直接复用上一轮知识作为本轮事实，门店事实仍以本轮检索和当前配置为准。",
	)
	return strings.Join(parts, "\n")
}

func findRecentAnsweredTurn(req RunInput, history adapter.HistoryBuildResult) *recentAnsweredTurn {
	items := history.RawItems
	if len(items) == 0 || req.UserMessage.SessionNo <= 0 {
		return nil
	}
	lastAIIndex := -1
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if item.SessionNo != req.UserMessage.SessionNo || !runtimeHistoryMessageUsable(item) {
			continue
		}
		switch item.SenderType {
		case enums.IMSenderTypeSystem:
			continue
		case enums.IMSenderTypeAgent, enums.IMSenderTypeCustomer:
			return nil
		case enums.IMSenderTypeAI:
			lastAIIndex = index
		}
		if lastAIIndex >= 0 {
			break
		}
	}
	if lastAIIndex < 0 {
		return nil
	}

	currentAt := runtimeMessageReferenceTime(req.UserMessage)
	lastAIAt := runtimeMessageReferenceTime(items[lastAIIndex])
	if currentAt.IsZero() || lastAIAt.IsZero() || currentAt.Before(lastAIAt) || currentAt.Sub(lastAIAt) > recentAnsweredTurnWindow {
		return nil
	}

	requestID := strings.TrimSpace(items[lastAIIndex].RequestID)
	firstAIIndex := lastAIIndex
	aiTexts := make([]string, 0, 3)
	resourceTypes := make([]string, 0, 3)
	for index := 0; index <= lastAIIndex; index++ {
		item := items[index]
		if item.SessionNo != req.UserMessage.SessionNo || item.SenderType != enums.IMSenderTypeAI || !runtimeHistoryMessageUsable(item) {
			continue
		}
		if requestID != "" {
			if strings.TrimSpace(item.RequestID) != requestID {
				continue
			}
		} else if index != lastAIIndex {
			continue
		}
		if index < firstAIIndex {
			firstAIIndex = index
		}
		if text := runtimeAnsweredMessageText(item); text != "" {
			aiTexts = append(aiTexts, text)
		}
		if resourceType := runtimeAnsweredResourceLabel(item.MessageType); resourceType != "" {
			resourceTypes = appendIfMissing(resourceTypes, resourceType)
		}
	}

	customerTexts := make([]string, 0, 3)
	for index := firstAIIndex - 1; index >= 0; index-- {
		item := items[index]
		if item.SessionNo != req.UserMessage.SessionNo || !runtimeHistoryMessageUsable(item) {
			continue
		}
		switch item.SenderType {
		case enums.IMSenderTypeSystem:
			continue
		case enums.IMSenderTypeCustomer:
			text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload))
			if text != "" {
				customerTexts = append([]string{text}, customerTexts...)
			}
			if len(customerTexts) >= 3 {
				index = -1
			}
		default:
			index = -1
		}
	}
	if len(customerTexts) == 0 || len(aiTexts) == 0 && len(resourceTypes) == 0 {
		return nil
	}
	return &recentAnsweredTurn{
		CustomerTexts: customerTexts,
		AITexts:       aiTexts,
		ResourceTypes: resourceTypes,
	}
}

func runtimeHistoryMessageUsable(item models.Message) bool {
	return !item.HistoricalOnly && item.RecalledAt == nil && item.SendStatus != enums.IMMessageStatusFailed && item.SendStatus != enums.IMMessageStatusRecalled
}

func runtimeMessageReferenceTime(item models.Message) time.Time {
	if item.SentAt != nil && !item.SentAt.IsZero() {
		return *item.SentAt
	}
	return item.CreatedAt
}

func runtimeAnsweredMessageText(item models.Message) string {
	switch item.MessageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
		return strings.TrimSpace(item.Content)
	default:
		return ""
	}
}

func runtimeAnsweredResourceLabel(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "图片"
	case enums.IMMessageTypeLocation:
		return "定位"
	case enums.IMMessageTypeMiniProgram:
		return "小程序"
	default:
		return ""
	}
}

func resolveClarifyKnowledgeProbeQuery(req RunInput, history adapter.HistoryBuildResult) string {
	currentText := runtimeUserMessageText(req.UserMessage)
	if currentText == "" {
		return ""
	}
	if !isPureReferentialClarifyText(currentText) {
		return currentText
	}
	turn := findRecentAnsweredTurn(req, history)
	if turn == nil || len(turn.CustomerTexts) != 1 {
		return ""
	}
	return strings.TrimSpace(turn.CustomerTexts[0]) + "\n当前追问：" + currentText
}

func isPureReferentialClarifyText(text string) bool {
	normalized := strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "",
		"？", "", "?", "", "！", "", "!", "", "。", "", ".", "", "，", "", ",", "",
	).Replace(strings.TrimSpace(text))
	switch normalized {
	case "这个", "这个呢", "那个", "那个呢", "它", "它呢", "它有吗", "这个有吗", "那个有吗", "还有吗", "还有没有", "有吗", "呢",
		"这是什么", "这是什么服务", "这是啥", "这是干嘛的", "这是干什么的", "这个是什么", "这个啥意思", "这是什么意思", "什么意思":
		return true
	default:
		return false
	}
}
