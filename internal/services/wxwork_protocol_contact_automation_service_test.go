package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
)

func TestWxWorkContactAutomationFriendApplyCallbackAcceptsOnlyPendingNonContact(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	var mu sync.Mutex
	agreeCalls := make([]map[string]any, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestBody struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode protocol request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requestBody.Path {
		case "/contact/sync_contact":
			_, _ = w.Write([]byte(`{"error_code":0,"error_message":"ok","data":{"last_seq":"20","contact_list":[{"seq":"20","user_id":"existing-user","name":"已存在客户","flag":0,"add_time":1}]}}`))
		case "/contact/sync_apply_contact":
			_, _ = w.Write([]byte(`{"error_code":0,"error_message":"ok","data":{"last_seq":"30","contact_list":[{"seq":"29","user_id":"existing-user","name":"已存在客户","corp_id":"corp-1","flag":2},{"seq":"30","user_id":"pending-user","name":"新申请客户","corp_id":"corp-2","flag":2}]}}`))
		case "/contact/agree_contact":
			mu.Lock()
			agreeCalls = append(agreeCalls, requestBody.Data)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"error_code":0,"error_message":"ok","data":{}}`))
		default:
			http.Error(w, "unexpected path", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	configJSON, _ := json.Marshal(map[string]any{"baseUrl": server.URL, "appKey": "key", "appSecret": "secret"})
	channel := &models.Channel{
		Name:        "企微协议测试",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-contact-automation",
		ConfigJSON:  string(configJSON),
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:                    "contact-auto-guid",
		ChannelID:               channel.ID,
		EmployeeUserID:          "employee-1",
		AutoAcceptFriendRequest: true,
		WelcomeEnabled:          false,
		Status:                  enums.StatusOk,
		AuditFields:             models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if err := WxWorkProtocolContactAutomationService.HandleFriendApply(instance.ID); err != nil {
		t.Fatalf("handle friend apply callback: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(agreeCalls) != 1 {
		t.Fatalf("expected exactly one agree call, got %d: %#v", len(agreeCalls), agreeCalls)
	}
	if got := strings.TrimSpace(toString(agreeCalls[0]["user_id"])); got != "pending-user" {
		t.Fatalf("expected pending-user to be accepted, got %q", got)
	}
	updated := WxWorkProtocolInstanceService.Get(instance.ID)
	if updated == nil || updated.FriendRequestSyncSeq != "30" || updated.ContactAutomationLastAt == nil || updated.ContactAutomationLastError != "" {
		t.Fatalf("unexpected automation state: %+v", updated)
	}
}

func TestWxWorkWelcomeImageCreatesStructuredImageMessage(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		Default: enums.AssetProviderLocal,
		Local: config.LocalStorageConfig{
			Root:    t.TempDir(),
			BaseURL: "http://localhost/assets",
		},
	}})
	now := time.Now()
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     "welcome-image-user",
		ExternalName:   "欢迎语图片客户",
	}, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	asset := &models.Asset{
		TenantID:    101,
		AssetID:     "welcome-image-asset",
		Provider:    enums.AssetProviderLocal,
		StorageKey:  "wxwork-welcome/welcome.jpg",
		Filename:    "welcome.jpg",
		FileSize:    1024,
		MimeType:    "image/jpeg",
		Status:      enums.AssetStatusSuccess,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		WelcomeEnabled:      true,
		WelcomeImageAssetID: asset.AssetID,
		Status:              enums.StatusOk,
	}

	WxWorkProtocolDefaultResourceService.SendNewFriendWelcome(conversation, instance, "welcome-image-test")
	messages := make([]models.Message, 0)
	if err := db.Where("conversation_id = ?", conversation.ID).Order("id ASC").Find(&messages).Error; err != nil {
		t.Fatalf("find welcome messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one welcome image message, got %d", len(messages))
	}
	if messages[0].MessageType != enums.IMMessageTypeImage || !strings.Contains(messages[0].Payload, asset.AssetID) {
		t.Fatalf("expected structured image payload, got %+v", messages[0])
	}
}

func TestWxWorkContactAutomationTreatsZeroSeqAsInitialBaseline(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestBody struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode protocol request: %v", err)
		}
		if requestBody.Path != "/contact/sync_contact" {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error_code":0,"error_message":"ok","data":{"last_seq":"13550273","contact_list":[{"seq":"13550273","user_id":"historical-user","name":"历史联系人","flag":0,"add_time":1}]}}`))
	}))
	t.Cleanup(server.Close)

	configJSON, _ := json.Marshal(map[string]any{"baseUrl": server.URL, "appKey": "key", "appSecret": "secret"})
	channel := &models.Channel{
		Name:        "企微协议基线测试",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-contact-baseline",
		ConfigJSON:  string(configJSON),
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:           "contact-baseline-guid",
		ChannelID:      channel.ID,
		EmployeeUserID: "employee-baseline",
		WelcomeEnabled: true,
		WelcomeMessage: "欢迎添加",
		ContactSyncSeq: "0",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if count := WxWorkProtocolContactAutomationService.Scan(20); count != 1 {
		t.Fatalf("expected one instance handled, got %d", count)
	}
	updated := WxWorkProtocolInstanceService.Get(instance.ID)
	if updated == nil || updated.ContactSyncSeq != "13550273" {
		t.Fatalf("expected baseline cursor to advance, got %+v", updated)
	}
	var conversationCount int64
	if err := db.Model(&models.Conversation{}).Count(&conversationCount).Error; err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	if conversationCount != 0 {
		t.Fatalf("historical contacts must not receive welcome messages, conversations=%d", conversationCount)
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.(string))
}
