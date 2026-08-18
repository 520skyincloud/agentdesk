package executor

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/utils"
)

// runtimeUserMessageText is the single text projection used by every runtime
// stage. Text, voice transcription, image understanding and burst messages must
// reach intent, retrieval, resource planning and generation with the same words.
func runtimeUserMessageText(message models.Message) string {
	if content := strings.TrimSpace(message.Content); strings.Contains(content, "客人刚才连续发了几条消息") {
		return strings.TrimSpace(currentTurnDisplayText(content))
	}
	mediaText, mediaSummary, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
	if strings.TrimSpace(mediaStatus) == "understood" {
		if text := strings.TrimSpace(mediaText); text != "" {
			return strings.TrimSpace(currentTurnDisplayText(text))
		}
		if summary := strings.TrimSpace(mediaSummary); summary != "" {
			return strings.TrimSpace(currentTurnDisplayText(summary))
		}
	}
	text := strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
	if text == "" {
		text = strings.TrimSpace(message.Content)
	}
	return strings.TrimSpace(currentTurnDisplayText(text))
}
