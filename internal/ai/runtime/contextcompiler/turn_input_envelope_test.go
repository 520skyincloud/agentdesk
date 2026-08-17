package contextcompiler

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func envelopeTestScope() EnvelopeScope {
	return EnvelopeScope{TenantID: 1, StoreID: 1, ConversationID: 2, SessionNo: 1, TurnID: 333, TurnVersion: 2}
}

func TestBuildTurnInputEnvelopeImagePlusQuestion(t *testing.T) {
	messages := []models.Message{
		{ID: 1350, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage,
			Content: "img.jpg", Payload: `{"mediaText":"图片中包含升房、优惠和发票抬头相关对话。","mediaUnderstandingStatus":"understood"}`},
		{ID: 1351, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "这图里说的啥"},
	}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	if len(envelope.Utterances) != 2 {
		t.Fatalf("expected 2 utterances, got %d", len(envelope.Utterances))
	}
	if envelope.Utterances[0].Ref != "U1" || envelope.Utterances[1].Ref != "U2" {
		t.Fatalf("unexpected refs: %+v", envelope.Utterances)
	}
	if envelope.Utterances[0].Text != "" || envelope.Utterances[1].Text != "这图里说的啥" {
		t.Fatalf("image utterance must have empty text, follow-up keeps text: %+v", envelope.Utterances)
	}
	if len(envelope.Observations) != 1 || envelope.Observations[0].Ref != "O1" || envelope.Observations[0].Status != "ready" {
		t.Fatalf("expected one ready observation, got %+v", envelope.Observations)
	}
	if !strings.Contains(envelope.Observations[0].Text, "升房") {
		t.Fatalf("observation text must carry OCR content: %+v", envelope.Observations[0])
	}
}

func TestBuildTurnInputEnvelopePendingVoiceWaits(t *testing.T) {
	messages := []models.Message{
		{ID: 1360, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
			Content: "voice.amr", Payload: `{"mediaUnderstandingStatus":"pending"}`},
	}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	if !envelope.HasCurrentVoiceWithoutTranscript() {
		t.Fatal("pending voice must be detected as missing current input")
	}
	if envelope.Utterances[0].Text != "" {
		t.Fatalf("pending voice utterance must have empty text: %+v", envelope.Utterances[0])
	}
	if envelope.Observations[0].Status != "pending" || envelope.Observations[0].Text != "" {
		t.Fatalf("pending observation must be empty-text placeholder: %+v", envelope.Observations[0])
	}
}

func TestBuildTurnInputEnvelopeReadyVoiceIsUtterance(t *testing.T) {
	messages := []models.Message{
		{ID: 1354, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
			Content: "voice.amr", Payload: `{"mediaText":"你们酒店有拖鞋没有，然后有没有洗发水？","mediaUnderstandingStatus":"understood"}`},
	}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	if envelope.HasCurrentVoiceWithoutTranscript() {
		t.Fatal("ready voice must not be treated as missing")
	}
	if !strings.Contains(envelope.Utterances[0].Text, "拖鞋") {
		t.Fatalf("ready voice transcript must be the utterance text: %+v", envelope.Utterances[0])
	}
}

func TestBuildTurnInputEnvelopePromotesStandaloneActionableImageAnalysis(t *testing.T) {
	messages := []models.Message{{
		ID: 1361, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage,
		Content: "error.jpg", Payload: `{"mediaText":"电视屏幕显示网络连接失败。","mediaUnderstandingStatus":"understood","responseExpectation":{"mode":"reply","basis":"visible_error","confidence":0.98}}`,
	}}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	if got := envelope.Utterances[0]; got.Text != "电视屏幕显示网络连接失败。" || got.TextOrigin != "media_analysis" {
		t.Fatalf("standalone actionable media must become a provenance-labelled intent input: %+v", got)
	}
	if envelope.Utterances[0].ResponseExpectation == nil || envelope.Utterances[0].ResponseExpectation.Mode != "reply" {
		t.Fatalf("response expectation missing from envelope: %+v", envelope.Utterances[0])
	}
}

func TestBuildTurnInputEnvelopeDoesNotPromoteMediaWhenCustomerTextExists(t *testing.T) {
	messages := []models.Message{
		{ID: 1362, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage,
			Content: "error.jpg", Payload: `{"mediaText":"电视屏幕显示网络连接失败。","mediaUnderstandingStatus":"understood","responseExpectation":{"mode":"reply","basis":"visible_error","confidence":0.98}}`},
		{ID: 1363, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "这个怎么处理"},
	}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	if envelope.Utterances[0].Text != "" || envelope.Utterances[0].TextOrigin != "none" {
		t.Fatalf("media analysis must remain an observation when customer text supplies the request: %+v", envelope.Utterances[0])
	}
}

func TestRenderEnvelopeJSONCarriesRefs(t *testing.T) {
	messages := []models.Message{
		{ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有咖啡吗"},
	}
	envelope := BuildTurnInputEnvelope(envelopeTestScope(), messages)
	rendered := envelope.RenderEnvelopeJSON()
	if !strings.Contains(rendered, "U1") || !strings.Contains(rendered, "有咖啡吗") {
		t.Fatalf("rendered envelope must carry refs and text: %s", rendered)
	}
}
