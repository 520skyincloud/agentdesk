package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func createWxWorkProtocolTestBinding(t *testing.T, db *gorm.DB, store *models.Store, suffix string) *models.StoreStaffBinding {
	t.Helper()
	if store == nil || store.ID <= 0 || store.TenantID <= 0 {
		t.Fatal("test store is required")
	}
	now := time.Now()
	role := &models.Role{
		Name:           "门店员工",
		Code:           constants.RoleCodeStoreStaff,
		Scope:          constants.RoleScopeTenant,
		AuthorityLevel: constants.RoleAuthorityMember,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Where("code = ?", role.Code).FirstOrCreate(role).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}
	user := &models.User{
		TenantID:    store.TenantID,
		Username:    "protocol-test-staff-" + suffix,
		Nickname:    "测试门店员工",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create store staff user: %v", err)
	}
	if err := db.Create(&models.UserRole{
		UserID:      user.ID,
		RoleID:      role.ID,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("assign store staff role: %v", err)
	}
	activeUserID := user.ID
	binding := &models.StoreStaffBinding{
		TenantID:             store.TenantID,
		UserID:               user.ID,
		ActiveUserID:         &activeUserID,
		StoreID:              store.ID,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: DefaultManualTimeoutMinutes,
		Status:               enums.StatusOk,
		AuditFields:          models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	return binding
}

func TestWxWorkProtocolPostJSONReturnsSafeBusinessErrors(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		want       string
		forbidden  []string
		statusCode int
	}{
		{
			name:       "expired seat",
			response:   `{"err_code":9003,"err_msg":"坐席已过期","data":{"guid":"private-guid"}}`,
			want:       wxWorkProtocolSeatExpiredMessage,
			forbidden:  []string{"raw=", "err_code", "private-guid", `{"`},
			statusCode: http.StatusOK,
		},
		{
			name:       "unknown provider error",
			response:   `{"err_code":7001,"err_msg":"provider detail private-guid","data":{"guid":"private-guid"}}`,
			want:       "错误码 7001",
			forbidden:  []string{"raw=", "err_code", "provider detail", "private-guid", `{"`},
			statusCode: http.StatusOK,
		},
		{
			name:       "http error body",
			response:   `{"detail":"private upstream response"}`,
			want:       "HTTP 503",
			forbidden:  []string{"raw=", "private upstream response", `{"`},
			statusCode: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			svc := &wxWorkProtocolService{httpClient: server.Client()}
			_, err := svc.postJSON(&dto.WxWorkProtocolChannelConfig{
				AppKey:    "test-app-key",
				AppSecret: "test-app-secret",
				BaseURL:   server.URL,
			}, "/login/get_login_qrcode", map[string]any{
				"guid":         "request-guid",
				"verify_login": false,
			})
			if err == nil {
				t.Fatal("postJSON() error = nil, want safe business error")
			}
			message := err.Error()
			if !strings.Contains(message, test.want) {
				t.Fatalf("postJSON() error = %q, want %q", message, test.want)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(message, forbidden) {
					t.Fatalf("postJSON() leaked %q in error %q", forbidden, message)
				}
			}
		})
	}
}

func TestWxWorkProtocolLoginRejectsExpiredDeviceBeforeProviderCall(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	if err := db.AutoMigrate(&models.WxWorkProtocolDevicePoolInstance{}); err != nil {
		t.Fatalf("migrate device pool: %v", err)
	}
	var providerCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		_, _ = w.Write([]byte(`{"err_code":0,"data":{"qrcode":"unused"}}`))
	}))
	defer server.Close()

	instance := createWxWorkProtocolLoginTestInstance(t, db, server.URL)
	expiredAt := time.Now().Add(-time.Minute)
	if err := db.Create(&models.WxWorkProtocolDevicePoolInstance{
		Guid:                          instance.Guid,
		ExpiredAt:                     &expiredAt,
		SyncStatus:                    "expired",
		BoundWxWorkProtocolInstanceID: instance.ID,
		Status:                        enums.StatusOk,
		AuditFields:                   models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create expired device pool instance: %v", err)
	}

	svc := &wxWorkProtocolService{httpClient: server.Client()}
	for name, action := range map[string]func(int64) (string, error){
		"get login qrcode": svc.GetLoginQRCode,
		"restore client":   svc.RestoreClient,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := action(instance.ID); err == nil || !strings.Contains(err.Error(), wxWorkProtocolSeatExpiredMessage) {
				t.Fatalf("%s error = %v, want expired instance error", name, err)
			}
		})
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want 0 for expired instance", providerCalls)
	}
}

func TestWxWorkProtocolLoginAllowsUnexpiredDevice(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	if err := db.AutoMigrate(&models.WxWorkProtocolDevicePoolInstance{}); err != nil {
		t.Fatalf("migrate device pool: %v", err)
	}
	var providerCalls int
	var providerPath string
	var providerData map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		var payload struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode protocol request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		providerPath = payload.Path
		providerData = payload.Data
		_, _ = w.Write([]byte(`{"err_code":0,"data":{"qrcode":"base64-image"}}`))
	}))
	defer server.Close()

	instance := createWxWorkProtocolLoginTestInstance(t, db, server.URL)
	expiresAt := time.Now().Add(time.Hour)
	if err := db.Create(&models.WxWorkProtocolDevicePoolInstance{
		Guid:                          instance.Guid,
		ExpiredAt:                     &expiresAt,
		SyncStatus:                    "offline",
		BoundWxWorkProtocolInstanceID: instance.ID,
		Status:                        enums.StatusOk,
		AuditFields:                   models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create active device pool instance: %v", err)
	}

	svc := &wxWorkProtocolService{httpClient: server.Client()}
	response, err := svc.GetLoginQRCode(instance.ID)
	if err != nil {
		t.Fatalf("GetLoginQRCode() error = %v", err)
	}
	if providerCalls != 1 || !strings.Contains(response, "base64-image") {
		t.Fatalf("provider calls = %d, response = %q", providerCalls, response)
	}
	if providerPath != "/login/get_login_qrcode" {
		t.Fatalf("provider path = %q, want direct qrcode endpoint", providerPath)
	}
	if providerData["guid"] != instance.Guid {
		t.Fatalf("provider guid = %#v, want %q", providerData["guid"], instance.Guid)
	}
	verifyLogin, ok := providerData["verify_login"].(bool)
	if !ok || verifyLogin {
		t.Fatalf("verify_login = %#v, want false boolean", providerData["verify_login"])
	}
	for _, forbidden := range []string{"proxy", "bridge", "sync_history_msg", "force_online", "auto_start"} {
		if _, exists := providerData[forbidden]; exists {
			t.Fatalf("direct qrcode request contains restore field %q: %#v", forbidden, providerData)
		}
	}
	current := WxWorkProtocolInstanceService.Get(instance.ID)
	if current == nil || current.HealthStatus != "login_qrcode" {
		t.Fatalf("instance health status = %#v, want login_qrcode after provider accepts qrcode request", current)
	}
}

func TestWxWorkProtocolLoginKeepsOnlineDeviceOnline(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	if err := db.AutoMigrate(&models.WxWorkProtocolDevicePoolInstance{}); err != nil {
		t.Fatalf("migrate device pool: %v", err)
	}
	var providerData map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode protocol request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if payload.Path != "/login/get_login_qrcode" {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		providerData = payload.Data
		_, _ = w.Write([]byte(`{"err_code":0,"data":{"qrcode":"base64-image"}}`))
	}))
	t.Cleanup(server.Close)

	instance := createWxWorkProtocolLoginTestInstance(t, db, server.URL)
	if err := db.Model(instance).Update("health_status", "online").Error; err != nil {
		t.Fatalf("mark protocol instance online: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour)
	if err := db.Create(&models.WxWorkProtocolDevicePoolInstance{
		Guid:                          instance.Guid,
		ExpiredAt:                     &expiresAt,
		SyncStatus:                    "online",
		BoundWxWorkProtocolInstanceID: instance.ID,
		Status:                        enums.StatusOk,
		AuditFields:                   models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create active device pool instance: %v", err)
	}

	svc := &wxWorkProtocolService{httpClient: server.Client()}
	if _, err := svc.GetLoginQRCode(instance.ID); err != nil {
		t.Fatalf("GetLoginQRCode() error = %v", err)
	}
	if providerData["guid"] != instance.Guid {
		t.Fatalf("provider guid = %#v, want %q", providerData["guid"], instance.Guid)
	}
	verifyLogin, ok := providerData["verify_login"].(bool)
	if !ok || verifyLogin {
		t.Fatalf("verify_login = %#v, want false boolean", providerData["verify_login"])
	}
	current := WxWorkProtocolInstanceService.Get(instance.ID)
	if current == nil || current.HealthStatus != "online" {
		t.Fatalf("instance health status = %#v, want online while relogin is pending", current)
	}
}

func TestWxWorkProtocolLoginStatusRequestContracts(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	type providerCall struct {
		Path string
		Data map[string]any
	}
	calls := make([]providerCall, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode protocol request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		calls = append(calls, providerCall{Path: payload.Path, Data: payload.Data})
		w.Header().Set("Content-Type", "application/json")
		switch payload.Path {
		case "/login/check_login_qrcode":
			_, _ = w.Write([]byte(`{"err_code":0,"data":{"status":10}}`))
		case "/login/verify_login_qrcode":
			_, _ = w.Write([]byte(`{"err_code":0,"data":{"status":2}}`))
		default:
			http.Error(w, "unexpected path", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	instance := createWxWorkProtocolLoginTestInstance(t, db, server.URL)
	svc := &wxWorkProtocolService{httpClient: server.Client()}
	svc.ResetLoginVerificationAttempts(instance.ID)

	status, err := svc.CheckLoginQRCodeStatus(instance.ID)
	if err != nil {
		t.Fatalf("CheckLoginQRCodeStatus() error = %v", err)
	}
	if !status.RequiresCode || status.StatusCode != 10 {
		t.Fatalf("check status = %#v, want verification required", status)
	}
	status, err = svc.VerifyLoginQRCodeStatus(instance.ID, "123456")
	if err != nil {
		t.Fatalf("VerifyLoginQRCodeStatus() error = %v", err)
	}
	if status.Status != "success" {
		t.Fatalf("verify status = %#v, want success", status)
	}

	if len(calls) != 2 {
		t.Fatalf("provider calls = %#v, want check then verify", calls)
	}
	if calls[0].Path != "/login/check_login_qrcode" || len(calls[0].Data) != 1 || calls[0].Data["guid"] != instance.Guid {
		t.Fatalf("check request contract = %#v", calls[0])
	}
	if calls[1].Path != "/login/verify_login_qrcode" || len(calls[1].Data) != 2 ||
		calls[1].Data["guid"] != instance.Guid || calls[1].Data["code"] != "123456" {
		t.Fatalf("verify request contract = %#v", calls[1])
	}
}

func TestWxWorkProtocolInstanceResponseHidesSavedProxy(t *testing.T) {
	item := &models.WxWorkProtocolInstance{Proxy: "socks5://user:password@proxy.example.test:1080"}
	result := response.BuildWxWorkProtocolInstanceResponse(item)
	if result.Proxy != "" {
		t.Fatalf("proxy response = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(encoded), "user:password") {
		t.Fatalf("instance response leaked proxy credentials: %s", encoded)
	}
}

func createWxWorkProtocolLoginTestInstance(t *testing.T, db *gorm.DB, baseURL string) *models.WxWorkProtocolInstance {
	t.Helper()
	now := time.Now()
	channel := &models.Channel{
		TenantID:    101,
		Name:        "企微员工号测试渠道",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-login-test",
		ConfigJSON:  `{"appKey":"test-app-key","appSecret":"test-app-secret","baseUrl":"` + baseURL + `"}`,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID:     101,
		ChannelID:    channel.ID,
		Guid:         "wxwork-login-test-guid-" + strings.NewReplacer("/", "-").Replace(t.Name()),
		HealthStatus: "offline",
		Status:       enums.StatusOk,
		AuditFields:  models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create protocol instance: %v", err)
	}
	return instance
}

func TestWxWorkProtocolLocationMessageIsNotVoice(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1001491",
		MsgType:     wxProtocolMsgLocation,
		ContentType: 6,
		Longitude:   117.281937,
		Latitude:    31.716152,
		Title:       "合成验收酒店(合肥滨湖时代广场店)",
		Address:     "安徽省合肥市包河区西藏路1318号众悦广场1501",
		Zoom:        15,
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgLocation {
		t.Fatalf("expected inferred location msg_type=%d, got %d", wxProtocolMsgLocation, got)
	}

	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeLocation {
		t.Fatalf("expected location message type, got %s", got)
	}
	content, payload, err := svc.buildInboundMessageContent(nil, enums.IMMessageTypeLocation, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != msg.Title {
		t.Fatalf("expected location title content, got %q", content)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("invalid payload json: %v", err)
	}
	if body["longitude"] != msg.Longitude || body["latitude"] != msg.Latitude || body["title"] != msg.Title || body["address"] != msg.Address {
		t.Fatalf("unexpected location payload: %#v", body)
	}
}

func TestWxWorkProtocolTextWithStaleVoiceTimeIsNotVoice(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1003888",
		ContentType: 16,
		Content:     "我没给你发语音大哥",
		VoiceTime:   3,
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgText {
		t.Fatalf("expected stale voice_time text to infer text msg_type=%d, got %d", wxProtocolMsgText, got)
	}
	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeText {
		t.Fatalf("expected text message type, got %s", got)
	}
}

func TestWxWorkProtocolProfileUpdatesReadNestedPersonAndPreserveVID(t *testing.T) {
	svc := &wxWorkProtocolService{}
	updates := svc.profileUpdatesFromResponse(`{
		"error_code": 0,
		"error_message": "success",
		"data": {
			"persons": [{
				"vid": 1688854374868249,
				"info": {
					"name": "企微测试员工",
					"avatar": "https://example.test/avatar.png"
				}
			}]
		}
	}`)

	if got := updates["employee_user_id"]; got != "1688854374868249" {
		t.Fatalf("employee_user_id=%#v, want exact nested vid", got)
	}
	if got := updates["employee_name"]; got != "企微测试员工" {
		t.Fatalf("employee_name=%#v", got)
	}
	if got := updates["employee_avatar"]; got != "https://example.test/avatar.png" {
		t.Fatalf("employee_avatar=%#v", got)
	}
	if got := updates["health_status"]; got != "online" {
		t.Fatalf("health_status=%#v", got)
	}
}

func TestWxWorkProtocolLoginOtherDeviceKeepsInstanceOnline(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		TenantID:       101,
		Guid:           "guid-login-other-device",
		EmployeeUserID: "employee-1",
		HealthStatus:   "online",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	callbackAt := now.Add(time.Minute)
	if err := WxWorkProtocolService.handleLoginOtherDevice(instance, `{"notify_type":11011}`, callbackAt); err != nil {
		t.Fatalf("handleLoginOtherDevice() error = %v", err)
	}

	var current models.WxWorkProtocolInstance
	if err := db.First(&current, instance.ID).Error; err != nil {
		t.Fatalf("load instance: %v", err)
	}
	if current.HealthStatus != "online" {
		t.Fatalf("health_status=%q, want online", current.HealthStatus)
	}
	if current.LastHeartbeatAt == nil || !current.LastHeartbeatAt.Equal(callbackAt) {
		t.Fatalf("last_heartbeat_at=%v, want %v", current.LastHeartbeatAt, callbackAt)
	}
	if !strings.Contains(current.Remark, "协议未声明当前实例退出") {
		t.Fatalf("unexpected remark: %q", current.Remark)
	}
}

func TestWxWorkProtocolSkipsReferencedMutationMessage(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		TenantID:       101,
		Guid:           "guid-refer",
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1003228",
		Sender:      "external-user-1",
		Receiver:    "employee-1",
		ContentType: 16,
		MsgType:     wxProtocolMsgVoice,
		VoiceTime:   2,
		ReferID:     json.RawMessage(`"1002966"`),
		SendTime:    now.Unix(),
	}, `{"id":"1003228","referid":"1002966","msg_type":6,"content_type":16,"voice_time":2}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	var messageCount int64
	if err := db.Model(&models.Message{}).Count(&messageCount).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("expected referenced mutation callback not to create a message, got %d", messageCount)
	}
	var log models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-refer:1003228").First(&log).Error; err != nil {
		t.Fatalf("expected sync log for skipped referenced mutation: %v", err)
	}
	if log.SyncStatus != enums.MessageSyncStatusSkipped || !strings.Contains(log.ErrorMessage, "referid=1002966") {
		t.Fatalf("unexpected sync log: %+v", log)
	}
}

func TestWxWorkProtocolSkipsInboundGroupMessageBeforeConversationCreation(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		Guid:           "guid-group-message",
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1005538",
		Sender:      "7881301988023128",
		SenderName:  "香雪海",
		Receiver:    "employee-1",
		RoomID:      "10775325120961882",
		ContentType: 101,
		MsgType:     wxProtocolMsgImage,
		URL:         "https://example.com/group-image.jpg",
		SendTime:    now.Unix(),
	}, `{"id":"1005538","sender":"7881301988023128","sender_name":"香雪海","receiver":"employee-1","roomid":"10775325120961882","content_type":101,"msg_type":5}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	for name, model := range map[string]any{
		"customers":             &models.Customer{},
		"customer identities":   &models.CustomerIdentity{},
		"conversations":         &models.Conversation{},
		"messages":              &models.Message{},
		"conversation mappings": &models.WxWorkKFConversation{},
		"message refs":          &models.WxWorkKFMessageRef{},
		"outbox messages":       &models.ChannelMessageOutbox{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("expected group callback not to create %s, got %d", name, count)
		}
	}

	var syncLog models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-group-message:1005538").First(&syncLog).Error; err != nil {
		t.Fatalf("expected skipped group message sync log: %v", err)
	}
	if syncLog.SyncStatus != enums.MessageSyncStatusSkipped || !strings.Contains(syncLog.ErrorMessage, "room_id=10775325120961882") {
		t.Fatalf("unexpected group message sync log: %+v", syncLog)
	}
}

func TestWxWorkProtocolReceivesCustomerMessageBeforeKnowledgeIsConfigured(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          45,
		TenantID:    101,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-new-account",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	store := &models.Store{
		ID:          77,
		TenantID:    101,
		StoreCode:   "knowledge-pending-store",
		Name:        "待配置知识库门店",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	binding := createWxWorkProtocolTestBinding(t, db, store, "knowledge-pending")
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "guid-new-account",
		ChannelID:           channel.ID,
		EmployeeUserID:      "employee-new",
		EmployeeName:        "新员工号",
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		KnowledgeBaseID:     0,
		AIReplyEnabled:      true,
		HealthStatus:        "online",
		Status:              enums.StatusOk,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	customer := &models.Customer{
		TenantID:    101,
		Name:        "新客户",
		Avatar:      "https://example.com/customer-avatar.jpg",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := db.Create(&models.CustomerIdentity{
		TenantID:       101,
		CustomerID:     customer.ID,
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     "wxwork_protocol:external-new-customer",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create customer identity: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1009001",
		Sender:      "external-new-customer",
		SenderName:  "新客户",
		Receiver:    "employee-new",
		RoomID:      "0",
		ContentType: wxProtocolMsgText,
		MsgType:     wxProtocolMsgText,
		Content:     "你好",
		SendTime:    now.Unix(),
	}, `{"id":"1009001","sender":"external-new-customer","receiver":"employee-new","roomid":"0","content":"你好","msg_type":2}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	var conversation models.Conversation
	if err := db.Order("id DESC").First(&conversation).Error; err != nil {
		t.Fatalf("expected conversation: %v", err)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil {
		t.Fatalf("expected route state")
	}
	if state.WxWorkInstanceID != instance.ID || state.StoreID != instance.StoreID || state.KnowledgeBaseID != 0 {
		t.Fatalf("expected instance-scoped route before knowledge binding, got %+v", state)
	}
	if state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || !state.NeedHumanFollowUp {
		t.Fatalf("expected AI paused and dashboard attention enabled, got %+v", state)
	}
	currentConversation := ConversationService.Get(conversation.ID)
	if currentConversation == nil || currentConversation.Status != enums.IMConversationStatusPending || currentConversation.CurrentAssigneeID != 0 {
		t.Fatalf("expected conversation to enter the dispatch pool, got %+v", currentConversation)
	}

	var customerMessages int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeCustomer).Count(&customerMessages).Error; err != nil {
		t.Fatalf("count customer messages: %v", err)
	}
	if customerMessages != 1 {
		t.Fatalf("expected one received customer message, got %d", customerMessages)
	}
	var aiMessages int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Count(&aiMessages).Error; err != nil {
		t.Fatalf("count AI messages: %v", err)
	}
	if aiMessages != 0 {
		t.Fatalf("expected no fake configuration reply to customer, got %d", aiMessages)
	}

	var syncLog models.MessageSyncLog
	externalMsgID := "wx_protocol:guid-new-account:1009001"
	if err := db.Where("external_msg_id = ? AND sync_status = ?", externalMsgID, enums.MessageSyncStatusSuccess).First(&syncLog).Error; err != nil {
		t.Fatalf("expected successful receive sync log: %v", err)
	}
}

func TestWxWorkProtocolReceivesCustomerMessageWithConfiguredKnowledgeRecordsSuccessSyncLog(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		TenantID:    101,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-configured-knowledge",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	store := &models.Store{
		TenantID:    101,
		StoreCode:   "configured-knowledge-store",
		Name:        "已配置知识库门店",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	knowledgeBase := &models.KnowledgeBase{
		TenantID:    101,
		StoreID:     store.ID,
		DatasetID:   "dataset-configured-knowledge",
		Name:        "门店知识库",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(knowledgeBase).Error; err != nil {
		t.Fatalf("create knowledge base: %v", err)
	}
	if err := db.Model(store).Update("knowledge_base_id", knowledgeBase.ID).Error; err != nil {
		t.Fatalf("bind knowledge base to store: %v", err)
	}
	store.KnowledgeBaseID = knowledgeBase.ID
	binding := createWxWorkProtocolTestBinding(t, db, store, "configured-knowledge")
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "guid-configured-knowledge",
		ChannelID:           channel.ID,
		EmployeeUserID:      "employee-configured",
		EmployeeName:        "已配置员工号",
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		KnowledgeBaseID:     knowledgeBase.ID,
		AIReplyEnabled:      true,
		HealthStatus:        "online",
		Status:              enums.StatusOk,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	customer := &models.Customer{
		TenantID:    101,
		Name:        "知识库门店客户",
		Avatar:      "https://example.com/customer-avatar.jpg",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := db.Create(&models.CustomerIdentity{
		TenantID:       101,
		CustomerID:     customer.ID,
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     "wxwork_protocol:external-configured-customer",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create customer identity: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1009002",
		Sender:      "external-configured-customer",
		SenderName:  "知识库门店客户",
		Receiver:    "employee-configured",
		RoomID:      "0",
		ContentType: wxProtocolMsgText,
		MsgType:     wxProtocolMsgText,
		Content:     "请问早餐几点开始？",
		SendTime:    now.Unix(),
	}, `{"id":"1009002","sender":"external-configured-customer","receiver":"employee-configured","roomid":"0","content":"请问早餐几点开始？","msg_type":2}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	externalMsgID := "wx_protocol:guid-configured-knowledge:1009002"
	var messageRef models.WxWorkKFMessageRef
	if err := db.Where("wx_msg_id = ?", externalMsgID).First(&messageRef).Error; err != nil {
		t.Fatalf("expected inbound message reference: %v", err)
	}
	var syncLog models.MessageSyncLog
	if err := db.Where("external_msg_id = ? AND sync_status = ?", externalMsgID, enums.MessageSyncStatusSuccess).First(&syncLog).Error; err != nil {
		t.Fatalf("expected successful receive sync log: %v", err)
	}
	if syncLog.ConversationID != messageRef.ConversationID || syncLog.MessageID != messageRef.MessageID {
		t.Fatalf("expected sync log and message reference to identify the same message, log=%+v ref=%+v", syncLog, messageRef)
	}
	if syncLog.Direction != enums.MessageSyncDirectionWecomToAgentDesk ||
		syncLog.Source != "wxwork_protocol" ||
		syncLog.Target != "agentdesk" ||
		syncLog.ErrorMessage != "message received" {
		t.Fatalf("unexpected successful receive sync log: %+v", syncLog)
	}
	state := ConversationRouteService.GetByConversationID(messageRef.ConversationID)
	if state == nil ||
		state.WxWorkInstanceID != instance.ID ||
		state.StoreID != store.ID ||
		state.KnowledgeBaseID != knowledgeBase.ID {
		t.Fatalf("expected configured store route state, got %+v", state)
	}
}

func TestWxWorkProtocolReferencedRecallMarksOriginalMessageRecalled(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          32,
		TenantID:    101,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-recall-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("external-recall-user")
	conversation, err := ConversationService.Create(external, channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID:       101,
		Guid:           "guid-recall",
		ChannelID:      channel.ID,
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	originalWxMsgID := "wx_protocol:guid-recall:1003262"
	message, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, originalWxMsgID, enums.IMMessageTypeText, "你好", "", external, "incoming-1003262")
	if err != nil {
		t.Fatalf("send original customer message: %v", err)
	}
	if err := db.Create(&models.WxWorkKFMessageRef{
		TenantID:       101,
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		WxMsgID:        originalWxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionIn),
		OpenKfID:       "wx_protocol:guid-recall:single",
		ExternalUserID: external.ExternalID,
		SendStatus:     string(enums.WxWorkKFMessageSendStatusReceived),
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create original message ref: %v", err)
	}
	var beforeCount int64
	if err := db.Model(&models.Message{}).Count(&beforeCount).Error; err != nil {
		t.Fatalf("count messages before recall: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err = svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1003267",
		Sender:      "external-recall-user",
		Receiver:    "employee-1",
		ContentType: wxProtocolMsgSystemAlt,
		MsgType:     wxProtocolMsgSystemAlt,
		Content:     "该消息已被撤回",
		ReferID:     json.RawMessage(`"1003262"`),
		SendTime:    now.Add(time.Second).Unix(),
	}, `{"id":"1003267","referid":"1003262","msg_type":1011,"content_type":1011,"content":"该消息已被撤回"}`)
	if err != nil {
		t.Fatalf("handle recall callback: %v", err)
	}

	updated := MessageService.Get(message.ID)
	if updated == nil || updated.SendStatus != enums.IMMessageStatusRecalled || updated.RecalledAt == nil {
		t.Fatalf("expected original message recalled, got %+v", updated)
	}
	ref := WxWorkKFMessageRefService.GetByWxMsgID(originalWxMsgID)
	if ref == nil || ref.SendStatus != string(enums.WxWorkKFMessageSendStatusRecalled) {
		t.Fatalf("expected original ref status recalled, got %+v", ref)
	}
	var afterCount int64
	if err := db.Model(&models.Message{}).Count(&afterCount).Error; err != nil {
		t.Fatalf("count messages after recall: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("expected recall callback not to create message, before=%d after=%d", beforeCount, afterCount)
	}
	var syncLog models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-recall:1003267").First(&syncLog).Error; err != nil {
		t.Fatalf("expected recall sync log: %v", err)
	}
	if syncLog.SyncStatus != enums.MessageSyncStatusSuccess || !strings.Contains(syncLog.ErrorMessage, "recall applied") {
		t.Fatalf("unexpected recall sync log: %+v", syncLog)
	}
}

func TestWxWorkProtocolEmployeeOutgoingEchoRepairsLegacyRef(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          31,
		TenantID:    101,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	store := &models.Store{
		TenantID:    101,
		StoreCode:   "employee-echo-store",
		Name:        "员工回复测试门店",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	binding := createWxWorkProtocolTestBinding(t, db, store, "employee-echo")
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "guid-1",
		ChannelID:           channel.ID,
		EmployeeUserID:      "employee-1",
		EmployeeName:        "回复测试员工",
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		HealthStatus:        "online",
		Status:              enums.StatusOk,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("external-user-1")
	conversation, _, err := ConversationService.CreateStoreScopedWithRuntimeProfileWithoutWelcome(external, channel.ID, *aiAgent, StoreConversationScope{
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.WxWorkKFConversation{
		TenantID:       101,
		ConversationID: conversation.ID,
		ChannelID:      channel.ID,
		OpenKfID:       "wx_protocol:guid-1:single",
		ExternalUserID: "external-user-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation mapping: %v", err)
	}
	wxMsgID := "wx_protocol:guid-1:wx-msg-1"
	if err := db.Create(&models.WxWorkKFMessageRef{
		TenantID:       101,
		ConversationID: conversation.ID,
		MessageID:      0,
		WxMsgID:        wxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		OpenKfID:       "wx_protocol:guid-1:single",
		ExternalUserID: "external-user-1",
		SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create legacy ref: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err = svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "wx-msg-1",
		Sender:      "employee-1",
		Receiver:    "external-user-1",
		RoomID:      "0",
		ContentType: 0,
		MsgType:     wxProtocolMsgText,
		Content:     "我在企微回复",
		SendTime:    now.Unix(),
	}, `{"id":"wx-msg-1","content":"我在企微回复"}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	ref := WxWorkKFMessageRefService.GetByWxMsgID(wxMsgID)
	if ref == nil || ref.MessageID <= 0 || ref.ConversationID != conversation.ID {
		t.Fatalf("expected legacy ref to be repaired with local message id, got %+v", ref)
	}
	message := MessageService.Get(ref.MessageID)
	if message == nil || message.SenderType != enums.IMSenderTypeAgent || message.Content != "我在企微回复" {
		t.Fatalf("expected repaired local agent message, got %+v", message)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("message_id = ?", ref.MessageID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected repaired echo to avoid outbound outbox, got %d", outboxCount)
	}
}

func TestWxWorkProtocolEmployeeOutgoingFirstMessageCreatesConversation(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	store := &models.Store{
		TenantID:  101,
		StoreCode: "outgoing-first-store",
		Name:      "企微首发门店",
		Status:    enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	channel := &models.Channel{
		TenantID:    101,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-outgoing-first",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	binding := createWxWorkProtocolTestBinding(t, db, store, "outgoing-first")
	instance := &models.WxWorkProtocolInstance{
		TenantID:            101,
		Guid:                "guid-outgoing-first",
		ChannelID:           channel.ID,
		EmployeeUserID:      "employee-first",
		StoreID:             store.ID,
		StoreStaffBindingID: binding.ID,
		HealthStatus:        "online",
		Status:              enums.StatusOk,
		AuditFields:         models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	err := WxWorkProtocolService.handleChatMessage(instance, request.WxProtocolChatMsg{
		Seq:         "100",
		ID:          "outgoing-first-message",
		Sender:      instance.EmployeeUserID,
		Receiver:    "external-first-customer",
		RoomID:      "0",
		MsgType:     wxProtocolMsgText,
		ContentType: wxProtocolMsgText,
		Content:     "您好，我先联系您",
		SendTime:    now.Unix(),
	}, `{"id":"outgoing-first-message","content":"您好，我先联系您"}`)
	if err != nil {
		t.Fatalf("handle outgoing-first message: %v", err)
	}

	mapping := WxWorkProtocolService.findProtocolConversationMapping(instance, request.WxProtocolChatMsg{
		Sender:   instance.EmployeeUserID,
		Receiver: "external-first-customer",
	}, "external-first-customer")
	if mapping == nil || mapping.ConversationID <= 0 {
		t.Fatalf("outgoing-first message did not create conversation mapping: %+v", mapping)
	}
	var messages []models.Message
	if err := db.Where("conversation_id = ? AND sender_type = ?", mapping.ConversationID, enums.IMSenderTypeAgent).Find(&messages).Error; err != nil {
		t.Fatalf("find external agent message: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "您好，我先联系您" {
		t.Fatalf("unexpected external agent messages: %+v", messages)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("message_id = ?", messages[0].ID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("employee native echo must not create outbound loop, got %d outbox rows", outboxCount)
	}
}

func TestWxWorkProtocolMessageSequenceDetectsGapAndKeepsHighestCheckpoint(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		TenantID:     101,
		Guid:         "guid-sequence",
		HealthStatus: "online",
		Status:       enums.StatusOk,
		AuditFields:  models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	for _, sequence := range []string{"100", "103", "102", "103"} {
		if err := WxWorkProtocolService.advanceMessageSyncSequence(instance.ID, sequence); err != nil {
			t.Fatalf("advance sequence %s: %v", sequence, err)
		}
	}
	current := WxWorkProtocolInstanceService.Get(instance.ID)
	if current == nil {
		t.Fatal("sequence instance missing")
	}
	if current.MessageSyncSeq != "103" || current.MessageGapFromSeq != "101" || current.MessageGapToSeq != "102" || current.MessageGapDetectedAt == nil {
		t.Fatalf("unexpected sequence checkpoint: %+v", current)
	}
}

func TestPrepareOutboundMiniProgramMediaKeepsExistingCoverCredentials(t *testing.T) {
	svc := &wxWorkProtocolService{}
	message := &models.Message{Payload: `{"username":"gh_7370f8f46fc0@app","file_id":"cover-file-id","aes_key":"cover-aes-key","md5":"cover-md5","size":20810,"appicon":"http://example.com/icon.png"}`}
	if err := svc.prepareOutboundMiniProgramMedia(nil, &models.WxWorkProtocolInstance{Guid: "guid-1"}, message); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(message.Payload, "cover-file-id") {
		t.Fatalf("expected payload to keep original cover credentials, got %s", message.Payload)
	}
}

func TestPrepareOutboundMiniProgramMediaRequiresMiniProgramUsername(t *testing.T) {
	svc := &wxWorkProtocolService{}
	message := &models.Message{Payload: `{"conversation_id":"S:7881302995969629","file_id":"cover-file-id","aes_key":"cover-aes-key","md5":"cover-md5","size":20810}`}
	err := svc.prepareOutboundMiniProgramMedia(nil, &models.WxWorkProtocolInstance{Guid: "guid-1"}, message)
	if err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("expected username validation error, got %v", err)
	}
}

func TestWxWorkProtocolMiniProgramMessageIsStructuredCard(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1001564",
		MsgType:     wxProtocolMsgWeApp,
		ContentType: 78,
		Username:    "gh_7370f8f46fc0@app",
		AppID:       "wx37bef9195b47f085",
		AppName:     "自由家安心宿",
		AppIcon:     "http://mmbiz.qpic.cn/sz_mmbiz_png/example/640?wx_fmt=png",
		Title:       "e秒安心住",
		PagePath:    "pages/home/home.html",
		ThumbWidth:  360,
		ThumbHeight: 288,
		CDN: request.WxProtocolMediaPayload{
			FileID: "306c0201020465",
			AesKey: "6676686A7463676E75797576797A776E",
			MD5:    "c9e083a08b8f6ee8fd36072e138b29cb",
			Size:   20810,
		},
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgWeApp {
		t.Fatalf("expected inferred mini program msg_type=%d, got %d", wxProtocolMsgWeApp, got)
	}

	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeMiniProgram {
		t.Fatalf("expected mini program message type, got %s", got)
	}
	content, payload, err := svc.buildInboundMessageContent(nil, enums.IMMessageTypeMiniProgram, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != msg.Title {
		t.Fatalf("expected mini program title content, got %q", content)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("invalid payload json: %v", err)
	}
	for key, want := range map[string]string{
		"appid":     msg.AppID,
		"appname":   msg.AppName,
		"appicon":   msg.AppIcon,
		"title":     msg.Title,
		"page_path": msg.PagePath,
		"username":  msg.Username,
	} {
		if got := body[key]; got != want {
			t.Fatalf("expected payload %s=%q, got %#v in %#v", key, want, got, body)
		}
	}
	if got := body["msg_type"]; got != float64(wxProtocolMsgWeApp) {
		t.Fatalf("expected payload msg_type=%d, got %#v", wxProtocolMsgWeApp, got)
	}
}

func TestNormalizeStoreRoomAtList(t *testing.T) {
	got := normalizeStoreRoomAtList([]string{" staff-1 ", "", "staff-2", "staff-1", "0"})
	if len(got) != 2 || got[0] != "staff-1" || got[1] != "staff-2" {
		t.Fatalf("unexpected normalized at list: %#v", got)
	}
}
