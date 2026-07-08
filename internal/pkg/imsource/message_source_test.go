package imsource

import (
	"testing"

	"agent-desk/internal/pkg/enums"
)

func TestDetectSendSource(t *testing.T) {
	tests := []struct {
		name      string
		sender    enums.IMSenderType
		requestID string
		clientID  string
		want      string
	}{
		{
			name:      "wxwork app echo request",
			sender:    enums.IMSenderTypeAgent,
			requestID: "wx_protocol_self_echo",
			clientID:  "wx_protocol:guid:msg",
			want:      SendSourceLocal,
		},
		{
			name:     "wxwork app echo client id",
			sender:   enums.IMSenderTypeAgent,
			clientID: "wx_protocol:guid:msg",
			want:     SendSourceLocal,
		},
		{
			name:     "dashboard agent",
			sender:   enums.IMSenderTypeAgent,
			clientID: "agent_123",
			want:     SendSourceWeb,
		},
		{
			name:     "ai has no agent send source",
			sender:   enums.IMSenderTypeAI,
			clientID: "ai_reply_1",
			want:     "",
		},
		{
			name:     "customer has no agent send source",
			sender:   enums.IMSenderTypeCustomer,
			clientID: "wx_protocol:guid:msg",
			want:     "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectSendSource(tt.sender, tt.requestID, tt.clientID); got != tt.want {
				t.Fatalf("DetectSendSource() = %q, want %q", got, tt.want)
			}
		})
	}
}
