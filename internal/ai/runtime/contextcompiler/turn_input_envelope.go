package contextcompiler

import (
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

// TurnInputEnvelope 是「多模态契约 8」的在线投影：当前 Turn 的客户输入（utterances）、
// 媒体观察（observations）、上一 AI 批次与未完成任务。替代 mergeRecentCustomerBurstMessage
// 生成的无标签拼接字符串，让 Intent 能按 U*/O* 引用真实来源。
type TurnInputEnvelope struct {
	SchemaVersion   string                   `json:"schemaVersion"`
	Scope           EnvelopeScope            `json:"scope"`
	Utterances      []EnvelopeUtterance      `json:"utterances"`
	Observations    []EnvelopeObservation    `json:"observations"`
	PriorAssistant  *EnvelopePriorAssistant  `json:"priorAssistant"`
	UnresolvedTasks []EnvelopeUnresolvedTask `json:"unresolvedTasks"`
}

type EnvelopeScope struct {
	TenantID       int64 `json:"tenantId"`
	StoreID        int64 `json:"storeId"`
	ConversationID int64 `json:"conversationId"`
	SessionNo      int   `json:"sessionNo"`
	TurnID         int64 `json:"turnId"`
	TurnVersion    int   `json:"turnVersion"`
}

type EnvelopeUtterance struct {
	Ref                 string                                `json:"ref"` // U1..Un，仅在本 Envelope 内有效，不入库
	MessageID           int64                                 `json:"messageId"`
	MessageType         string                                `json:"messageType"`
	Text                string                                `json:"text"` // 当前文字、ASR transcript，或仅媒体输入时的分析摘要
	TextOrigin          string                                `json:"textOrigin"`
	SentAt              string                                `json:"sentAt"`
	AnalysisStatus      string                                `json:"analysisStatus"` // not_required/pending/processing/ready/failed_*
	ObservationRefs     []string                              `json:"observationRefs"`
	ResponseExpectation *contracts.MediaResponseExpectationV1 `json:"responseExpectation"`
}

type EnvelopeObservation struct {
	Ref             string   `json:"ref"` // O1..On
	MessageID       int64    `json:"sourceMessageId"`
	SourceRevision  int      `json:"sourceRevision"`
	Status          string   `json:"status"` // pending/ready/failed
	SourceType      string   `json:"sourceType"`
	ObservationType string   `json:"observationType"`
	Text            string   `json:"text"`
	Confidence      float64  `json:"confidence"`
	AllowedUses     []string `json:"allowedUses"`
	ForbiddenUses   []string `json:"forbiddenUses"`
}

type EnvelopePriorAssistant struct {
	MessageID int64    `json:"messageId"`
	TaskKeys  []string `json:"taskKeys"`
	Summary   string   `json:"summary"`
}

type EnvelopeUnresolvedTask struct {
	TaskKey               string                `json:"taskKey"`
	SourceMessageID       int64                 `json:"sourceMessageId,omitempty"`
	SequenceNo            int                   `json:"sequenceNo,omitempty"`
	Intent                string                `json:"intent"`
	SubIntent             string                `json:"subIntent"`
	Status                string                `json:"status"`
	QuestionText          string                `json:"questionText,omitempty"`
	CanonicalQuestionHash string                `json:"canonicalQuestionHash,omitempty"`
	ResolvedTopic         string                `json:"resolvedTopic,omitempty"`
	Requirements          []EnvelopeRequirement `json:"requirements,omitempty"`
}

// EnvelopeRequirement is the small, optional requirement projection used when
// an unfinished task is carried into a later intent turn. It deliberately
// mirrors only the seed fields; requirement keys and state remain server-owned.
type EnvelopeRequirement struct {
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Sequence int    `json:"sequence"`
}

// BuildTurnInputEnvelope 从当前 Turn 的客户消息构建 Envelope：
// - 文字消息 -> utterance（text = 原文）
// - 语音已 ready -> utterance（text = transcript）+ transcript 观察
// - 图片/附件 -> utterance（text 空）+ 按 ObservationPolicy 投影的观察
// pending 媒体分配 O* 占位（status=pending, text=""），Intent 可引用但不作事实。
func BuildTurnInputEnvelope(scope EnvelopeScope, messages []models.Message) TurnInputEnvelope {
	return BuildTurnInputEnvelopeWithAnalyses(scope, messages, nil)
}

// BuildTurnInputEnvelopeWithAnalyses 优先读取持久化 message_analysis.v2；Payload
// 只作为灰度兼容来源，并始终按最低权限投影，不能成为门店事实或动作依据。
func BuildTurnInputEnvelopeWithAnalyses(scope EnvelopeScope, messages []models.Message, analyses map[int64]contracts.MessageAnalysisV2) TurnInputEnvelope {
	envelope := TurnInputEnvelope{SchemaVersion: contracts.SchemaTurnInputEnvelopeV1, Scope: scope}
	mediaTextByRef := make(map[string]string)
	utteranceSeq, observationSeq := 0, 0
	for _, message := range messages {
		if message.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		utteranceSeq++
		utterance := EnvelopeUtterance{
			Ref:             envelopeRef("U", utteranceSeq),
			MessageID:       message.ID,
			MessageType:     string(message.MessageType),
			TextOrigin:      "none",
			SentAt:          envelopeSentAt(message),
			ObservationRefs: []string{},
		}
		analysis, hasAnalysis := analyses[message.ID]
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		understood := strings.TrimSpace(status) == "understood"
		if hasAnalysis {
			mediaText = strings.TrimSpace(analysis.NormalizedText)
			mediaSummary = mediaText
			status = analysis.Status
			understood = analysis.Status == "ready"
			utterance.ResponseExpectation = cloneMediaResponseExpectation(analysis.ResponseExpectation)
		}
		if utterance.ResponseExpectation == nil {
			if mode, basis, confidence, ok := replyengine.MediaResponseExpectationFromPayload(message.Payload); ok {
				utterance.ResponseExpectation = &contracts.MediaResponseExpectationV1{Mode: mode, Basis: basis, Confidence: confidence}
			}
		}
		if utterance.ResponseExpectation == nil && understood && message.MessageType != enums.IMMessageTypeVoice &&
			replyengine.MediaUnderstandingHasActionableIntent(strings.Join([]string{mediaText, mediaSummary}, " ")) {
			utterance.ResponseExpectation = &contracts.MediaResponseExpectationV1{Mode: "reply", Basis: "unknown", Confidence: 0.5}
		}
		mediaTextByRef[utterance.Ref] = strings.TrimSpace(mediaText)
		switch message.MessageType {
		case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
			utterance.AnalysisStatus = "not_required"
			utterance.Text = strings.TrimSpace(message.Content)
			utterance.TextOrigin = "customer_text"
		case enums.IMMessageTypeVoice:
			// 语音 transcript 是 L6 当前输入；未 ready 时 text 为空、等待事件。
			utterance.AnalysisStatus = envelopeAnalysisStatus(status)
			if understood {
				utterance.Text = strings.TrimSpace(mediaText)
				utterance.TextOrigin = "asr_transcript"
				if hasAnalysis {
					appendAnalysisObservations(&envelope, &utterance, analysis, &observationSeq)
				} else {
					observationSeq++
					utterance.ObservationRefs = append(utterance.ObservationRefs, envelopeRef("O", observationSeq))
					envelope.Observations = append(envelope.Observations, EnvelopeObservation{
						Ref: envelopeRef("O", observationSeq), MessageID: message.ID, SourceRevision: 1,
						Status: "ready", SourceType: "voice", ObservationType: "transcript",
						Text: firstNonEmptyTrimmed(mediaText, mediaSummary), Confidence: 0.9,
						AllowedUses: []string{}, ForbiddenUses: forbiddenObservationUses(),
					})
				}
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
			if hasAnalysis && understood && len(analysis.Observations) > 0 {
				appendAnalysisObservations(&envelope, &utterance, analysis, &observationSeq)
			} else {
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
					// Legacy Payload 没有可验证 contentRole，只允许描述和当前指代，
					// 禁止作为知识事实或资源资格。
					obs.AllowedUses = []string{"describe_media", "resolve_reference"}
					obs.Confidence = 0.6
				}
				envelope.Observations = append(envelope.Observations, obs)
			}
		}
		envelope.Utterances = append(envelope.Utterances, utterance)
	}
	promoteStandaloneMediaAnalysis(&envelope, mediaTextByRef)
	return envelope
}

func cloneMediaResponseExpectation(value *contracts.MediaResponseExpectationV1) *contracts.MediaResponseExpectationV1 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// promoteStandaloneMediaAnalysis gives Intent a source-bound text only when a
// turn otherwise contains no customer-authored text or ASR transcript. It does
// not promote ordinary media (mode=none), and it keeps the observation policy
// restrictions alongside the derived text.
func promoteStandaloneMediaAnalysis(envelope *TurnInputEnvelope, mediaTextByRef map[string]string) {
	if envelope == nil {
		return
	}
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) != "" {
			return
		}
	}
	for index := range envelope.Utterances {
		utterance := &envelope.Utterances[index]
		if utterance.ResponseExpectation == nil ||
			!replyengine.MediaResponseExpectationTriggersAI(utterance.ResponseExpectation.Mode) {
			continue
		}
		text := strings.TrimSpace(mediaTextByRef[utterance.Ref])
		if text == "" || utterance.AnalysisStatus != "ready" {
			continue
		}
		utterance.Text = text
		utterance.TextOrigin = "media_analysis"
	}
}

func appendAnalysisObservations(envelope *TurnInputEnvelope, utterance *EnvelopeUtterance, analysis contracts.MessageAnalysisV2, observationSeq *int) {
	if envelope == nil || utterance == nil || observationSeq == nil {
		return
	}
	for _, item := range analysis.Observations {
		(*observationSeq)++
		ref := envelopeRef("O", *observationSeq)
		utterance.ObservationRefs = append(utterance.ObservationRefs, ref)
		envelope.Observations = append(envelope.Observations, EnvelopeObservation{
			Ref: ref, MessageID: item.SourceMessageID, SourceRevision: item.SourceRevision,
			Status: item.Status, SourceType: item.SourceType, ObservationType: item.ObservationType,
			Text: item.Text, Confidence: item.Confidence,
			AllowedUses: append([]string{}, item.AllowedUses...), ForbiddenUses: append([]string{}, item.ForbiddenUses...),
		})
	}
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
	case "understood", "ready":
		return "ready"
	case "failed", "failed_terminal":
		return "failed_terminal"
	case "retrying", "pending", "processing", "failed_retryable":
		return "pending"
	default:
		return "pending"
	}
}

func envelopeObsStatus(status string) string {
	if strings.TrimSpace(status) == "understood" || strings.TrimSpace(status) == "ready" {
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
