package contextcompiler

import (
	"sort"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/cloudwego/eino/schema"
)

func BuildHistoryTurns(items []models.Message, currentMessages []models.Message, estimator TokenEstimator, modelName string) []HistoryTurn {
	currentIDs := make(map[int64]struct{}, len(currentMessages))
	for _, item := range currentMessages {
		if item.ID > 0 {
			currentIDs[item.ID] = struct{}{}
		}
	}
	filtered := make([]models.Message, 0, len(items))
	for _, item := range items {
		if _, current := currentIDs[item.ID]; current || !historyMessageVisible(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].SeqNo == filtered[j].SeqNo {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].SeqNo < filtered[j].SeqNo
	})

	turns := make([]HistoryTurn, 0)
	var current *HistoryTurn
	for _, item := range filtered {
		switch item.SenderType {
		case enums.IMSenderTypeCustomer:
			if current == nil || len(current.AssistantMessages) > 0 {
				turns = append(turns, HistoryTurn{})
				current = &turns[len(turns)-1]
			}
			current.CustomerMessages = append(current.CustomerMessages, item)
			setHistoryTurnRange(current, item)
		case enums.IMSenderTypeAI, enums.IMSenderTypeAgent:
			if current == nil || len(current.CustomerMessages) == 0 {
				continue
			}
			current.AssistantMessages = append(current.AssistantMessages, item)
			setHistoryTurnRange(current, item)
		}
	}
	complete := make([]HistoryTurn, 0, len(turns))
	for _, turn := range turns {
		if len(turn.CustomerMessages) == 0 || len(turn.AssistantMessages) == 0 {
			continue
		}
		messages := historyTurnMessages(turn)
		turn.TokenCount = estimator.CountMessages(modelName, messages)
		complete = append(complete, turn)
	}
	return complete
}

func historyMessageVisible(item models.Message) bool {
	if item.HistoricalOnly || item.RecalledAt != nil || item.SendStatus == enums.IMMessageStatusFailed || item.SendStatus == enums.IMMessageStatusRecalled {
		return false
	}
	if item.SenderType != enums.IMSenderTypeCustomer && item.SenderType != enums.IMSenderTypeAI && item.SenderType != enums.IMSenderTypeAgent {
		return false
	}
	return visibleMessageContent(item) != ""
}

func visibleMessageContent(item models.Message) string {
	return strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(item.MessageType, item.Content, item.Payload))
}

func setHistoryTurnRange(turn *HistoryTurn, item models.Message) {
	if turn.FirstSeqNo == 0 || item.SeqNo < turn.FirstSeqNo {
		turn.FirstSeqNo = item.SeqNo
	}
	if item.SeqNo > turn.LastSeqNo {
		turn.LastSeqNo = item.SeqNo
	}
}

func historyTurnMessages(turn HistoryTurn) []*schema.Message {
	messages := make([]*schema.Message, 0, len(turn.CustomerMessages)+len(turn.AssistantMessages))
	if content := joinVisibleMessages(turn.CustomerMessages); content != "" {
		messages = append(messages, schema.UserMessage(content))
	}
	for _, item := range turn.AssistantMessages {
		if content := visibleMessageContent(item); content != "" {
			messages = append(messages, schema.AssistantMessage(content, nil))
		}
	}
	return messages
}

func joinVisibleMessages(items []models.Message) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if content := visibleMessageContent(item); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}
