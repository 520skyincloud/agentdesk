package runtime

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestSummarizeMemoryItemsDoesNotPersistRoomNumberAsStableFact(t *testing.T) {
	stable, openIssues, _, _ := summarizeMemoryItems([]models.Message{
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我在109，厕所太滑摔倒了"},
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我的车牌皖A12345，开发票需要抬头"},
	})
	if strings.Contains(stable, "109") || strings.Contains(stable, "房号") || strings.Contains(stable, "我在") {
		t.Fatalf("room number should not be persisted as stable fact: %q", stable)
	}
	if !strings.Contains(stable, "车牌") || !strings.Contains(stable, "发票") {
		t.Fatalf("expected non-room stable facts to remain, got %q", stable)
	}
	if !strings.Contains(openIssues, "摔倒") {
		t.Fatalf("expected safety issue to remain in open issues, got %q", openIssues)
	}
}
