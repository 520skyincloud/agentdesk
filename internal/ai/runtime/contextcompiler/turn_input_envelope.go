package contextcompiler

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

// TurnInputEnvelope 是「多模态契约 8」的在线投影：当前 Turn 的客户输入（utterances）、
// 媒体观察（observations）、上一 AI 批次与未完成任务。替代 mergeRecentCustomerBurstMessage
// 生成的无标签拼接字符串，让 Intent 能按 U*/O* 引用真实来源。
type TurnInputEnvelope struct {
	Scope           EnvelopeScope
	Utterances      []EnvelopeUtterance
	Observations    []EnvelopeObservation
	PriorAssistant  *EnvelopePriorAssistant
	UnresolvedTasks []EnvelopeUnresolvedTask
}

type EnvelopeScope struct {
	TenantID       int64
	StoreID        int64
	ConversationID int64
	SessionNo      int
	TurnID         int64
	TurnVersion    int
}

type EnvelopeUtterance struct {
	Ref             string // U1..Un，仅在本 Envelope 内有效，不入库
	MessageID       int64
	MessageType     string
	Text            string // 当前文字或已 ready 的语音 transcript；pending 媒体为空
	SentAt          string
	AnalysisStatus  string // not_required/pending/processing/ready/failed_*
	ObservationRefs []string
}

type EnvelopeObservation struct {
	Ref             string // O1..On
	MessageID       int64
	SourceRevision  int
	Status          string // pending/ready/failed
	SourceType      string
	ObservationType string
	Text            string
	Confidence      float64
	AllowedUses     []string
	ForbiddenUses   []string
}

type EnvelopePriorAssistant struct {
	MessageID int64
	Summary   string
}

type EnvelopeUnresolvedTask struct {
	TaskKey   string
	Intent    string
	SubIntent string
	Status    string
}

// BuildTurnInputEnvelope 从当前 Turn 的客户消息构建 Envelope：
// - 文字消息 -> utterance（text = 原文）
// - 语音已 ready -> utterance（text = transcript）+ transcript 观察
// - 图片/附件 -> utterance（text 空）+ 按 ObservationPolicy 投影的观察
// pending 媒体分配 O* 占位（status=pending, text=""），Intent 可引用但不作事实。
func BuildTurnInputEnvelope(scope EnvelopeScope, messages []models.Message) TurnInputEnvelope {
	envelope := TurnInputEnvelope{Scope: scope}
	utteranceSeq, observationSeq := 0, 0
	for _, message := range messages {
		if message.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		utteranceSeq++
		utterance := EnvelopeUtterance{
			Ref:         envelopeRef("U", utteranceSeq),
			MessageID:   message.ID,
			MessageType: string(message.MessageType),
			SentAt:      envelopeSentAt(message),
		}
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		understood := strings.TrimSpace(status) == "understood"
		switch message.MessageType {
		case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
			utterance.AnalysisStatus = "not_required"
			utterance.Text = strings.TrimSpace(message.Content)
		case enums.IMMessageTypeVoice:
			// 语音 transcript 是 L6 当前输入；未 ready 时 text 为空、等待事件。
			utterance.AnalysisStatus = envelopeAnalysisStatus(status)
			if understood {
				utterance.Text = strings.TrimSpace(mediaText)
				observationSeq++
				utterance.ObservationRefs = append(utterance.ObservationRefs, envelopeRef("O", observationSeq))
				envelope.Observations = append(envelope.Observations, EnvelopeObservation{
					Ref: envelopeRef("O", observationSeq), MessageID: message.ID, SourceRevision: 1,
					Status: "ready", SourceType: "voice", ObservationType: "transcript",
					Text: firstNonEmptyTrimmed(mediaText, mediaSummary), Confidence: 0.9,
					AllowedUses: []string{}, ForbiddenUses: forbiddenObservationUses(),
				})
			} else {
				observationSeq++
				utterance.ObservationRefs = append(utterance.ObservationRefs, envelopeRef("O", observationSeq))
				envelope.Observations = append(envelope.Observations, EnvelopeObservation{
					Ref: envelopeRef("O", observationSeq), MessageID: message.ID, SourceRevision: 1,
					Status: "pending", SourceType: "voice", ObservationType: "transcript",
					Text: "", ForbiddenUses: forbiddenObservationUses(),
				})
			}
		default: // image/video/gif/attachment
			utterance.AnalysisStatus = envelopeAnalysisStatus(status)
			observationSeq++
			utterance.ObservationRefs = append(utterance.ObservationRefs, envelopeRef("O", observationSeq))
			obs := EnvelopeObservation{
				Ref: envelopeRef("O", observationSeq), MessageID: message.ID, SourceRevision: 1,
				Status: envelopeObsStatus(status), SourceType: envelopeSourceType(message.MessageType),
				ObservationType: envelopeObservationType(message.MessageType),
				ForbiddenUses:   forbiddenObservationUses(),
			}
			if understood {
				obs.Text = firstNonEmptyTrimmed(mediaText, mediaSummary)
				obs.AllowedUses = []string{"describe_media", "resolve_reference"}
				obs.Confidence = 0.9
			}
			envelope.Observations = append(envelope.Observations, obs)
		}
		envelope.Utterances = append(envelope.Utterances, utterance)
	}
	return envelope
}

// HasCurrentVoiceWithoutTranscript 判定当前输入是否包含「尚无 transcript 的语音」：
// 此类语音是缺失的 L6 当前输入，必须事件等待，不能把空文本/文件名送 Intent。
func (e TurnInputEnvelope) HasCurrentVoiceWithoutTranscript() bool {
	for _, utterance := range e.Utterances {
		if utterance.MessageType == "voice" && strings.TrimSpace(utterance.Text) == "" {
			return true
		}
	}
	return false
}

// RenderEnvelopeJSON 渲染 Intent 可读的 [CURRENT_TURN_ENVELOPE] 文本块。
func (e TurnInputEnvelope) RenderEnvelopeJSON() string {
	var b strings.Builder
	b.WriteString("[CURRENT_TURN_ENVELOPE]\n")
	b.WriteString("客户输入（utterances，可用 U* 引用）：\n")
	for _, u := range e.Utterances {
		b.WriteString("- " + u.Ref + " [" + u.MessageType + "]")
		if u.Text != "" {
			b.WriteString(" " + u.Text)
		} else {
			b.WriteString("（媒体，正文见观察 " + strings.Join(u.ObservationRefs, ",") + "）")
		}
		b.WriteString("\n")
	}
	if len(e.Observations) > 0 {
		b.WriteString("媒体观察（observations，可用 O* 引用；pending 的正文为空，只能等待）：\n")
		for _, o := range e.Observations {
			b.WriteString("- " + o.Ref + " [" + o.Status + "/" + o.SourceType + "/" + o.ObservationType + "]")
			if o.Text != "" {
				b.WriteString(" " + previewObservationText(o.Text))
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func envelopeRef(prefix string, seq int) string {
	return prefix + intToDecimal(seq)
}

func intToDecimal(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return intToDecimal(v/10) + string(rune('0'+v%10))
}

func envelopeAnalysisStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "understood":
		return "ready"
	case "failed":
		return "failed_terminal"
	case "retrying":
		return "pending"
	default:
		return "pending"
	}
}

func envelopeObsStatus(status string) string {
	if strings.TrimSpace(status) == "understood" {
		return "ready"
	}
	if strings.TrimSpace(status) == "failed" {
		return "failed"
	}
	return "pending"
}

func envelopeSourceType(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "image"
	case enums.IMMessageTypeVoice:
		return "voice"
	case enums.IMMessageTypeVideo:
		return "video"
	case enums.IMMessageTypeGIF:
		return "gif"
	case enums.IMMessageTypeAttachment:
		return "attachment"
	default:
		return "text"
	}
}

func envelopeObservationType(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "ocr_text"
	case enums.IMMessageTypeVoice:
		return "transcript"
	case enums.IMMessageTypeAttachment:
		return "document_text"
	default:
		return "scene_description"
	}
}

func envelopeSentAt(message models.Message) string {
	if message.SentAt != nil {
		return message.SentAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return message.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func previewObservationText(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= 1200 {
		return string(runes)
	}
	return string(runes[:1200]) + "…"
}

func forbiddenObservationUses() []string {
	return []string{"store_identity", "store_address", "store_phone", "store_policy", "resource_eligibility", "handoff_decision"}
}
