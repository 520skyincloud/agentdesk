package runtime

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/utils"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestReplyEligibilityCanReply(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	eligibility := newReplyEligibility()
	conversation := newConversationFixture()
	message := newCustomerMessageFixture("hello")
	aiAgent := newAIAgentFixture()
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID: conversation.ID,
		RouteStatus:    enums.ConversationRouteStatusAIServing,
		RouteTarget:    "ai",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}

	if !eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected customer message to be replyable")
	}

	message.SenderType = enums.IMSenderTypeAgent
	if eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected non-customer message to be rejected")
	}

	message = newCustomerMessageFixture("hello")
	conversation.HandoffAt = ptrTime(time.Now())
	if !eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected historical handoff metadata not to permanently block AI")
	}

	conversation = newConversationFixture()
	conversation.CurrentAssigneeID = 1
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("current_assignee_id", 1).Error; err != nil {
		t.Fatalf("assign conversation: %v", err)
	}
	if eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected assigned conversation to be rejected")
	}

	conversation = newConversationFixture()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("current_assignee_id", 0).Error; err != nil {
		t.Fatalf("clear conversation assignment: %v", err)
	}
	aiAgent.ServiceMode = enums.IMConversationServiceModeHumanOnly
	if eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected human-only agent to be rejected")
	}

	aiAgent = newAIAgentFixture()
	message.Content = "   "
	if eligibility.CanReply(conversation, message, aiAgent) {
		t.Fatalf("expected blank message to be rejected")
	}
}

func TestReplyEligibilityAllowsAssignedManualResume(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	conversation := models.Conversation{
		ID:                9002,
		Status:            enums.IMConversationStatusActive,
		CurrentTeamID:     1,
		CurrentAssigneeID: 101,
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create assigned conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:    conversation.ID,
		RouteStatus:       enums.ConversationRouteStatusHQAgentDeskServing,
		RouteTarget:       "agentdesk_hq",
		NeedHumanFollowUp: true,
		SessionNo:         1,
	}).Error; err != nil {
		t.Fatalf("create manual route: %v", err)
	}
	token := "runtimeeligibility"
	if err := db.Create(&models.AIManualResumeTask{
		TaskKey:                "manual_resume:" + token,
		HandoffToken:           token,
		ConversationID:         conversation.ID,
		OriginMessageID:        10,
		LatestWaitingMessageID: 10,
		RouteStatus:            string(enums.ConversationRouteStatusHQAgentDeskServing),
		TaskStatus:             "running",
	}).Error; err != nil {
		t.Fatalf("create running manual resume task: %v", err)
	}
	message := models.Message{
		ID:             10,
		ConversationID: conversation.ID,
		RequestID:      "manual_resume_" + token,
		SenderType:     enums.IMSenderTypeCustomer,
		Content:        "刚才的问题还没处理",
	}
	if !newReplyEligibility().CanReply(conversation, message, newAIAgentFixture()) {
		t.Fatal("expected runtime eligibility to allow the assigned conversation's legal manual resume")
	}
}

func TestReplyCommitRejectsStaleManualResumeSource(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	now := time.Now()
	conversation := models.Conversation{
		ID:     9010,
		Status: enums.IMConversationStatusActive,
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID:    conversation.ID,
		RouteStatus:       enums.ConversationRouteStatusStoreWecomManual,
		RouteTarget:       "store_wecom",
		NeedHumanFollowUp: true,
		SessionNo:         1,
	}).Error; err != nil {
		t.Fatalf("create manual route: %v", err)
	}
	origin := models.Message{
		ID:             101,
		ConversationID: conversation.ID,
		SessionNo:      1,
		ClientMsgID:    "runtime-manual-resume-origin",
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "原来的问题",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now),
	}
	latest := origin
	latest.ID = 102
	latest.ClientMsgID = "runtime-manual-resume-latest"
	latest.Content = "最新补充的问题"
	latest.SeqNo = 2
	if err := db.Create(&origin).Error; err != nil {
		t.Fatalf("create origin message: %v", err)
	}
	if err := db.Create(&latest).Error; err != nil {
		t.Fatalf("create latest message: %v", err)
	}
	token := "runtimecommitstalesource"
	if err := db.Create(&models.AIManualResumeTask{
		TaskKey:                "manual_resume:" + token,
		HandoffToken:           token,
		ConversationID:         conversation.ID,
		OriginMessageID:        origin.ID,
		LatestWaitingMessageID: latest.ID,
		RouteStatus:            string(enums.ConversationRouteStatusStoreWecomManual),
		TaskStatus:             "running",
	}).Error; err != nil {
		t.Fatalf("create running manual resume task: %v", err)
	}
	service := newReplyCommitService()
	aiAgent := models.AIAgent{ID: 77, Name: "runtime-test-ai"}
	origin.RequestID = "manual_resume_" + token
	if _, err := service.SendAIReply(replyCommitInput{
		Conversation: conversation,
		Message:      origin,
		AIAgent:      aiAgent,
		ReplyText:    "这条旧回复不应提交",
		ClientPrefix: "runtime_manual_resume_stale",
	}); err == nil {
		t.Fatal("expected reply commit to pass the stale source ID and be rejected")
	}
	latest.RequestID = origin.RequestID
	if _, err := service.SendAIReply(replyCommitInput{
		Conversation: conversation,
		Message:      latest,
		AIAgent:      aiAgent,
		ReplyText:    "我继续处理最新问题。",
		ClientPrefix: "runtime_manual_resume_latest",
	}); err != nil {
		t.Fatalf("expected latest manual resume source to commit: %v", err)
	}
}

func TestReplyLatestMessageIgnoresAIServiceNotice(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	now := time.Now()
	conversationID := int64(9011)
	customer := models.Message{
		ID:             201,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "manual-resume-customer",
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "刚才的问题还没回答",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now),
	}
	serviceNotice := models.Message{
		ID:             202,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "ai_handoff_success_direct_9011_201",
		RequestID:      "handoff_direct_9011_201",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "帮您转接同事啦～",
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now.Add(time.Second)),
	}
	for _, item := range []models.Message{customer, serviceNotice} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create message %d: %v", item.ID, err)
		}
	}
	service := newAIReplyService()
	if !service.isStillLatestCustomerMessage(conversationID, customer.ID) {
		t.Fatal("AI service notice must not look like a newer customer message")
	}
	if !service.canCommitReplyForMessage(conversationID, customer.ID) {
		t.Fatal("AI service notice must not block the pending customer reply")
	}

	ordinaryReply := serviceNotice
	ordinaryReply.ID = 203
	ordinaryReply.SeqNo = 3
	ordinaryReply.ClientMsgID = "ordinary-ai-reply"
	ordinaryReply.RequestID = "ordinary-request"
	ordinaryReply.Content = "这是一条真实回答。"
	ordinaryReply.SentAt = ptrTime(now.Add(2 * time.Second))
	if err := db.Create(&ordinaryReply).Error; err != nil {
		t.Fatalf("create ordinary AI reply: %v", err)
	}
	if service.isStillLatestCustomerMessage(conversationID, customer.ID) {
		t.Fatal("ordinary newer AI reply must still block the stale customer reply")
	}
	if service.canCommitReplyForMessage(conversationID, customer.ID) {
		t.Fatal("ordinary newer AI reply must still block commit")
	}
}

func TestResolveReplyTimeout(t *testing.T) {
	service := newAIReplyService()
	aiAgent := newAIAgentFixture()

	if got := service.resolveReplyTimeout(aiAgent); got != 180*time.Second {
		t.Fatalf("expected default timeout, got %v", got)
	}

	aiAgent.ReplyTimeoutSeconds = 30
	if got := service.resolveReplyTimeout(aiAgent); got != 30*time.Second {
		t.Fatalf("expected exact timeout, got %v", got)
	}

	aiAgent.ReplyTimeoutSeconds = 999
	if got := service.resolveReplyTimeout(aiAgent); got != 600*time.Second {
		t.Fatalf("expected clamped timeout, got %v", got)
	}
}

func TestShouldWaitForRecentMediaUnderstanding(t *testing.T) {
	if shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "早餐几点"}) {
		t.Fatal("ordinary hotel-info text should not wait for media understanding")
	}
	if shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "你好"}) {
		t.Fatal("greeting should not wait for media understanding")
	}
	if !shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "帮我看下这张图片是什么"}) {
		t.Fatal("media follow-up should wait for recent media understanding")
	}
	if !shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeHTML, Content: "这个文件什么意思"}) {
		t.Fatal("file follow-up should wait for recent media understanding")
	}
	if !shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "这个多少钱"}) {
		t.Fatal("implicit image follow-up should wait for recent media understanding")
	}
	if !shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "能用吗"}) {
		t.Fatal("short implicit media question should wait for recent media understanding")
	}
	if shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeText, Content: "发一下酒店定位"}) {
		t.Fatal("location intent should not wait for media understanding")
	}
	if shouldWaitForRecentMediaUnderstanding(models.Message{MessageType: enums.IMMessageTypeImage}) {
		t.Fatal("media message itself is handled by media understanding worker")
	}
}

func TestMergeRecentCustomerBurstMessageKeepsMediaContext(t *testing.T) {
	setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(1001)
	image := models.Message{
		ID:             1,
		ConversationID: conversationID,
		ClientMsgID:    "img-1",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "room.jpg",
		Payload:        `{"mediaText":"图片里是一间客房，桌上有两瓶水。","mediaUnderstandingStatus":"understood"}`,
		SentAt:         &now,
	}
	questionTime := now.Add(2 * time.Second)
	question := models.Message{
		ID:             2,
		ConversationID: conversationID,
		ClientMsgID:    "text-2",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "这个多少钱",
		SentAt:         &questionTime,
	}
	if err := sqls.DB().Create(&image).Error; err != nil {
		t.Fatalf("create image message: %v", err)
	}
	if err := sqls.DB().Create(&question).Error; err != nil {
		t.Fatalf("create question message: %v", err)
	}

	merged := service.mergeRecentCustomerBurstMessage(conversationID, question)
	if !strings.Contains(merged.Content, "图片里是一间客房") || !strings.Contains(merged.Content, "这个多少钱") {
		t.Fatalf("expected merged burst to keep media understanding and follow-up, got: %s", merged.Content)
	}
	if !strings.Contains(merged.Content, "按顺序合并理解") {
		t.Fatalf("expected explicit burst instruction, got: %s", merged.Content)
	}
}

func TestMergeRecentCustomerBurstMessageKeepsTextAndVoiceInEitherOrder(t *testing.T) {
	testCases := []struct {
		name           string
		conversationID int64
		first          models.Message
		second         models.Message
		voiceText      string
		otherText      string
	}{
		{
			name:           "text_then_voice",
			conversationID: 10014,
			first: models.Message{
				ClientMsgID: "text-first",
				MessageType: enums.IMMessageTypeText,
				Content:     "早餐几点",
			},
			second: models.Message{
				ClientMsgID: "voice-last",
				MessageType: enums.IMMessageTypeVoice,
				Content:     "voice-last.amr",
				Payload:     `{"mediaText":"停车免费吗","mediaSummary":"客户询问停车。","mediaUnderstandingStatus":"understood"}`,
			},
			voiceText: "停车免费吗",
			otherText: "早餐几点",
		},
		{
			name:           "voice_then_text",
			conversationID: 10015,
			first: models.Message{
				ClientMsgID: "voice-first",
				MessageType: enums.IMMessageTypeVoice,
				Content:     "voice-first.amr",
				Payload:     `{"mediaText":"早餐几点","mediaSummary":"客户询问早餐。","mediaUnderstandingStatus":"understood"}`,
			},
			second: models.Message{
				ClientMsgID: "text-last",
				MessageType: enums.IMMessageTypeText,
				Content:     "房间有几瓶矿泉水，免费吗",
			},
			voiceText: "早餐几点",
			otherText: "房间有几瓶矿泉水，免费吗",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			setupRuntimeReplyMessageTestDB(t)
			service := newAIReplyService()
			now := time.Now()
			firstAt := now
			secondAt := now.Add(2 * time.Second)

			first := testCase.first
			first.ID = 1
			first.ConversationID = testCase.conversationID
			first.SessionNo = 1
			first.SeqNo = 1
			first.SenderType = enums.IMSenderTypeCustomer
			first.SentAt = &firstAt

			second := testCase.second
			second.ID = 2
			second.ConversationID = testCase.conversationID
			second.SessionNo = 1
			second.SeqNo = 2
			second.SenderType = enums.IMSenderTypeCustomer
			second.SentAt = &secondAt

			for _, item := range []models.Message{first, second} {
				if err := sqls.DB().Create(&item).Error; err != nil {
					t.Fatalf("create message %s: %v", item.ClientMsgID, err)
				}
			}

			merged := service.mergeRecentCustomerBurstMessage(testCase.conversationID, second)
			if !utils.IsRuntimeCustomerBurstEnvelope(merged.Content) {
				t.Fatalf("expected machine-marked burst envelope, got %q", merged.Content)
			}
			if !strings.Contains(merged.Content, testCase.voiceText) || !strings.Contains(merged.Content, testCase.otherText) {
				t.Fatalf("expected merged burst to preserve text and voice in order, got %q", merged.Content)
			}
			if count := strings.Count(merged.Content, testCase.voiceText); count != 1 {
				t.Fatalf("expected voice transcript exactly once, got %d occurrences in %q", count, merged.Content)
			}
			if strings.Contains(merged.Content, ".amr") || strings.Contains(merged.Content, "语音摘要是") {
				t.Fatalf("expected merged burst to use the complete transcript without filename or summary, got %q", merged.Content)
			}
		})
	}
}

func TestRuntimeBurstMessageTextRejectsUnfinishedVoice(t *testing.T) {
	for _, status := range []string{"", "pending", "failed", "empty"} {
		message := models.Message{
			MessageType: enums.IMMessageTypeVoice,
			Content:     "voice.amr",
			Payload:     `{"mediaText":"早餐几点","mediaUnderstandingStatus":"` + status + `"}`,
		}
		if got := runtimeBurstMessageText(message); got != "" {
			t.Fatalf("status %q must not enter a burst, got %q", status, got)
		}
	}
}

func TestMergeRecentCustomerBurstMessageSkipsPreviousSession(t *testing.T) {
	setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(10012)
	oldRoom := models.Message{
		ID:             1,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "old-room",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "我住1201",
		SentAt:         &now,
	}
	currentTime := now.Add(2 * time.Second)
	current := models.Message{
		ID:             2,
		ConversationID: conversationID,
		SessionNo:      2,
		ClientMsgID:    "current-aircon",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "空调不制冷",
		SentAt:         &currentTime,
	}
	if err := sqls.DB().Create(&oldRoom).Error; err != nil {
		t.Fatalf("create old message: %v", err)
	}
	if err := sqls.DB().Create(&current).Error; err != nil {
		t.Fatalf("create current message: %v", err)
	}

	merged := service.mergeRecentCustomerBurstMessage(conversationID, current)
	if strings.Contains(merged.Content, "1201") || strings.Contains(merged.Content, "连续发") {
		t.Fatalf("expected previous session message excluded from burst, got: %s", merged.Content)
	}
	if merged.Content != "空调不制冷" {
		t.Fatalf("expected current message unchanged, got: %s", merged.Content)
	}
}

func TestMergeRecentCustomerBurstMessageStartsAfterLastOutbound(t *testing.T) {
	setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(10013)
	locationQuestion := models.Message{
		ID:             1,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "location-question",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "定位发我一个",
		SentAt:         &now,
	}
	aiReplyAt := now.Add(2 * time.Second)
	aiReply := models.Message{
		ID:             2,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "ai-location",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "酒店定位：https://uri.amap.com/marker?position=117.263908,31.824097&name=丽斯未来酒店。",
		SentAt:         &aiReplyAt,
	}
	currentAt := now.Add(4 * time.Second)
	current := models.Message{
		ID:             3,
		ConversationID: conversationID,
		SessionNo:      1,
		ClientMsgID:    "checkin-question",
		SeqNo:          3,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "我要办理入住",
		SentAt:         &currentAt,
	}
	for _, item := range []models.Message{locationQuestion, aiReply, current} {
		if err := sqls.DB().Create(&item).Error; err != nil {
			t.Fatalf("create message %s: %v", item.ClientMsgID, err)
		}
	}

	merged := service.mergeRecentCustomerBurstMessage(conversationID, current)
	if merged.Content != current.Content {
		t.Fatalf("expected current message unchanged after outbound boundary, got: %s", merged.Content)
	}
	if strings.Contains(merged.Content, "定位发我一个") || strings.Contains(merged.Content, "连续发") {
		t.Fatalf("expected previous answered location question excluded, got: %s", merged.Content)
	}
}

func TestCanCommitReplySkipsMediaFollowUpWhenTrailingMediaArrives(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(1002)
	seedRuntimeAIServingConversation(t, db, conversationID)
	question := models.Message{ID: 1, ConversationID: conversationID, ClientMsgID: "q-1", SeqNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "这是干嘛的", SentAt: &now}
	imageTime := now.Add(time.Second)
	image := models.Message{ID: 2, ConversationID: conversationID, ClientMsgID: "img-2", SeqNo: 2, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage, Content: "funny.jpg", Payload: `{"mediaText":"图片是一只幽默摆拍的小动物，无实际酒店服务相关信息。","mediaUnderstandingStatus":"understood"}`, SentAt: &imageTime}
	if err := sqls.DB().Create(&question).Error; err != nil {
		t.Fatalf("create question: %v", err)
	}
	if err := sqls.DB().Create(&image).Error; err != nil {
		t.Fatalf("create image: %v", err)
	}
	if service.canCommitReplyForMessage(conversationID, question.ID) {
		t.Fatal("expected media follow-up text reply to wait for trailing media understanding")
	}
}

func TestCanCommitReplyAllowsTrailingPlainMediaForIndependentText(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(1003)
	seedRuntimeAIServingConversation(t, db, conversationID)
	question := models.Message{ID: 1, ConversationID: conversationID, ClientMsgID: "q-1", SeqNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点", SentAt: &now}
	imageTime := now.Add(time.Second)
	image := models.Message{ID: 2, ConversationID: conversationID, ClientMsgID: "img-2", SeqNo: 2, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage, Content: "funny.jpg", Payload: `{"mediaText":"图片是一只幽默摆拍的小动物，无实际酒店服务相关信息。","mediaUnderstandingStatus":"understood"}`, SentAt: &imageTime}
	if err := sqls.DB().Create(&question).Error; err != nil {
		t.Fatalf("create question: %v", err)
	}
	if err := sqls.DB().Create(&image).Error; err != nil {
		t.Fatalf("create image: %v", err)
	}
	if !service.canCommitReplyForMessage(conversationID, question.ID) {
		t.Fatal("expected trailing plain media not to cancel independent text reply")
	}
}

func TestCanCommitReplySkipsWhenTrailingUnderstoodVoiceArrives(t *testing.T) {
	db := setupRuntimeReplyMessageTestDB(t)
	service := newAIReplyService()
	now := time.Now()
	conversationID := int64(1004)
	seedRuntimeAIServingConversation(t, db, conversationID)
	question := models.Message{ID: 1, ConversationID: conversationID, ClientMsgID: "q-1", SeqNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "你现在呢", SentAt: &now}
	voiceTime := now.Add(time.Second)
	voice := models.Message{ID: 2, ConversationID: conversationID, ClientMsgID: "voice-2", SeqNo: 2, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice, Content: "wx_protocol_1003259.mp3", Payload: `{"mediaText":"我没给你发语音大哥。","mediaUnderstandingStatus":"understood"}`, SentAt: &voiceTime}
	if err := sqls.DB().Create(&question).Error; err != nil {
		t.Fatalf("create question: %v", err)
	}
	if err := sqls.DB().Create(&voice).Error; err != nil {
		t.Fatalf("create voice: %v", err)
	}
	if service.canCommitReplyForMessage(conversationID, question.ID) {
		t.Fatal("expected understood trailing voice to cancel stale text reply")
	}
}

func setupRuntimeReplyMessageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "runtime_reply_test_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.AIManualResumeTask{},
		&models.ConversationReadState{},
		&models.ConversationEventLog{},
		&models.Message{},
	); err != nil {
		t.Fatalf("migrate message: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func seedRuntimeAIServingConversation(t *testing.T, db *gorm.DB, conversationID int64) {
	t.Helper()
	if err := db.Create(&models.Conversation{
		ID:     conversationID,
		Status: enums.IMConversationStatusAIServing,
	}).Error; err != nil {
		t.Fatalf("create conversation %d: %v", conversationID, err)
	}
	if err := db.Create(&models.ConversationRouteState{
		ConversationID: conversationID,
		RouteStatus:    enums.ConversationRouteStatusAIServing,
		RouteTarget:    "ai",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create route %d: %v", conversationID, err)
	}
}

func TestBuildRunLogPlan(t *testing.T) {
	summary := &applicationruntime.Summary{
		PlannedSkillCode: "knowledge_router",
		PlanReason:       "manual",
	}
	action, toolCode, reason := buildRunLogPlan(summary)
	if action != "skill" || toolCode != "" || reason != "manual" {
		t.Fatalf("unexpected skill plan result: action=%q toolCode=%q reason=%q", action, toolCode, reason)
	}

	summary = &applicationruntime.Summary{
		Interrupted: true,
		TraceData: `{
			"graphTools": {
				"items": [
					{
						"toolCode": "` + toolx.GraphTriageServiceRequest.Code + `",
						"recommendedAction": "create_ticket",
						"ticketDraftReady": true
					}
				]
			}
		}`,
	}
	action, toolCode, reason = buildRunLogPlan(summary)
	if action != "graph" || toolCode != toolx.GraphTriageServiceRequest.Code || reason == "" {
		t.Fatalf("unexpected graph interrupt result: action=%q toolCode=%q reason=%q", action, toolCode, reason)
	}

	summary = &applicationruntime.Summary{
		InvokedToolCodes: []string{toolx.BuiltinToolSearch.Code},
		TraceData: `{
			"toolSearch": {
				"items": [
					{
						"targetToolCode": "mcp/test/search"
					}
				]
			}
		}`,
	}
	action, toolCode, reason = buildRunLogPlan(summary)
	if action != "tool" || toolCode != "mcp/test/search" || reason != "agent invoked dynamic tool via tool_search" {
		t.Fatalf("unexpected dynamic tool result: action=%q toolCode=%q reason=%q", action, toolCode, reason)
	}

	summary = &applicationruntime.Summary{ReplyText: "done"}
	action, toolCode, reason = buildRunLogPlan(summary)
	if action != "reply" || toolCode != "" || reason != "agent replied directly" {
		t.Fatalf("unexpected reply result: action=%q toolCode=%q reason=%q", action, toolCode, reason)
	}
}

func TestResolveInterruptPrompt(t *testing.T) {
	summary := &applicationruntime.Summary{
		Interrupts: []applicationruntime.InterruptContextSummary{
			{
				ID:          "interrupt-1",
				Type:        "question",
				InfoPreview: `{"message":"请补充订单号"}`,
			},
		},
	}
	if got := resolveInterruptPrompt(summary); got != "请补充订单号" {
		t.Fatalf("unexpected interrupt prompt: %q", got)
	}

	summary.Interrupts[0].InfoPreview = "直接补充手机号"
	if got := resolveInterruptPrompt(summary); got != "直接补充手机号" {
		t.Fatalf("unexpected raw interrupt prompt: %q", got)
	}
}

func newConversationFixture() models.Conversation {
	return models.Conversation{ID: 9001, Status: enums.IMConversationStatusAIServing}
}

func newCustomerMessageFixture(content string) models.Message {
	return models.Message{
		ID:             1,
		ConversationID: 9001,
		SenderType:     enums.IMSenderTypeCustomer,
		Content:        content,
	}
}

func newAIAgentFixture() models.AIAgent {
	return models.AIAgent{}
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
