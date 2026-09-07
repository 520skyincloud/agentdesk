package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDirectHandoffSuccessTextLabelsOnlyProvidedSubjects(t *testing.T) {
	if got := directHandoffSuccessText(nil); got != DirectHandoffSuccessMessage {
		t.Fatalf("legacy success text changed: %q", got)
	}
	got := directHandoffSuccessText([]string{"能换纸币吗", "能换纸币吗", ""})
	if got != "关于“能换纸币吗”，帮您转接到同事了" {
		t.Fatalf("wrong subject or duplicate: %q", got)
	}
	if got := directHandoffSuccessText([]string{"replyParts: taskId"}); got != DirectHandoffSuccessMessage {
		t.Fatalf("internal protocol leaked: %q", got)
	}
}

func TestHandoffRoomNumberPayloadRetainsNoticeSubjects(t *testing.T) {
	payload := handoffConfirmationPayload{Reason: "送用品", AwaitingField: "room_number", NoticeSubjects: []string{"送条毛巾"}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded handoffConfirmationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.RoomNumber = "1315"
	if got := directHandoffSuccessText(decoded.NoticeSubjects); !strings.Contains(got, "送条毛巾") {
		t.Fatalf("lost subject while collecting room: %q", got)
	}
}
