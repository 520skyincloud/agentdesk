package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
)

func TestWsNotificationTopic(t *testing.T) {
	svc := newWsService()
	if got := svc.notificationTopic(123); got != "notification:123" {
		t.Fatalf("expected notification:123, got %q", got)
	}
}

func TestWsNotificationCreatedEventType(t *testing.T) {
	event := RealtimeNotificationCreatedEvent{
		Payload: RealtimeNotificationCreatedPayload{
			Notification: response.NotificationResponse{ID: 1},
		},
	}
	if got := event.EventType(); got != "notification.created" {
		t.Fatalf("expected notification.created, got %q", got)
	}
	if payload := event.EventPayload(); payload == nil {
		t.Fatalf("expected payload")
	}
}

func TestWsStoreTopicAuthorization(t *testing.T) {
	db := setupStoreModelCredentialTestDB(t)
	first := createStoreModelCredentialTestStore(t, db, "南七店")
	second := createStoreModelCredentialTestStore(t, db, "高铁南站店")
	if err := db.Create(&models.StoreStaffBinding{
		UserID: 77, CompanyID: first.CompanyID, StoreID: first.ID, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := newWsService()
	superAdmin := &ClientSession{
		Role:      realtimeRoleAdmin,
		Principal: &dto.AuthPrincipal{UserID: 1, Roles: []string{constants.RoleCodeSuperAdmin}},
	}
	if !svc.canSubscribeStore(superAdmin, 99) {
		t.Fatal("super admin should be allowed to subscribe to a store topic")
	}

	regularAdmin := &ClientSession{
		Role:      realtimeRoleAdmin,
		Principal: &dto.AuthPrincipal{UserID: 2, Roles: []string{constants.RoleCodeAdmin}},
	}
	if svc.canSubscribeStore(regularAdmin, 99) {
		t.Fatal("regular admin must not subscribe to store credential topics")
	}

	storeStaff := &ClientSession{
		Role: realtimeRoleAdmin,
		Principal: &dto.AuthPrincipal{
			UserID: 77, Roles: []string{constants.RoleCodeStoreStaff},
		},
	}
	if !svc.canSubscribeStore(storeStaff, first.ID) {
		t.Fatal("store staff should subscribe to the bound store topic")
	}
	if svc.canSubscribeStore(storeStaff, second.ID) {
		t.Fatal("store staff must not subscribe to another store topic")
	}
}

func TestWsStoreCredentialChangedPayloadContainsOnlySafeMetadata(t *testing.T) {
	svc := newWsService()
	event := svc.newEvent("store:7", RealtimeStoreModelCredentialChangedEvent{
		Payload: RealtimeStoreModelCredentialChangedPayload{
			StoreID: 7, CredentialRevision: 3, Status: "active",
			ChangedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.Local).Format(time.DateTime),
		},
	})
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if got == "" || !wsTestContainsAll(got, `"storeId":7`, `"credentialRevision":3`, `"status":"active"`) {
		t.Fatalf("unexpected credential event payload: %s", got)
	}
	if wsTestContainsAny(got, "apiKey", "baseUrl", "authorization", "encryptedKey") {
		t.Fatalf("credential event leaked sensitive fields: %s", got)
	}
}

func TestWsStoreProfileChangedEventType(t *testing.T) {
	event := RealtimeStoreModelProfileChangedEvent{
		Payload: RealtimeStoreModelProfileChangedPayload{
			StoreID: 8, ProfileRevision: 4, Status: "pending",
		},
	}
	if event.EventType() != "store_model_profile.changed" {
		t.Fatalf("unexpected profile event type: %s", event.EventType())
	}
}

func wsTestContainsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !wsTestContainsAny(value, fragment) {
			return false
		}
	}
	return true
}

func wsTestContainsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
