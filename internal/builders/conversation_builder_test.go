package builders

import (
	"testing"

	"agent-desk/internal/pkg/i18nx"
)

func TestLocalizeConversationSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		locale  string
		summary string
		want    string
	}{
		{
			name:    "image summary in english",
			locale:  i18nx.LocaleEnUS,
			summary: "[图片]",
			want:    "[Image]",
		},
		{
			name:    "attachment summary in english",
			locale:  i18nx.LocaleEnUS,
			summary: "[附件] spec.pdf",
			want:    "[Attachment] spec.pdf",
		},
		{
			name:    "recalled message in english",
			locale:  i18nx.LocaleEnUS,
			summary: "该消息已撤回",
			want:    "This message was recalled.",
		},
		{
			name:    "business text is not translated",
			locale:  i18nx.LocaleEnUS,
			summary: "客户反馈无法登录",
			want:    "客户反馈无法登录",
		},
		{
			name:    "chinese locale keeps existing summary",
			locale:  i18nx.LocaleZhCN,
			summary: "[图片]",
			want:    "[图片]",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := localizeConversationSummary(tt.locale, tt.summary); got != tt.want {
				t.Fatalf("localizeConversationSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalizeRenderableMessageContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		locale  string
		content string
		want    string
	}{
		{
			name:    "recalled message in english",
			locale:  i18nx.LocaleEnUS,
			content: "该消息已撤回",
			want:    "This message was recalled.",
		},
		{
			name:    "normal customer message is not translated",
			locale:  i18nx.LocaleEnUS,
			content: "客户反馈无法登录",
			want:    "客户反馈无法登录",
		},
		{
			name:    "chinese locale keeps content",
			locale:  i18nx.LocaleZhCN,
			content: "该消息已撤回",
			want:    "该消息已撤回",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := localizeRenderableMessageContent(tt.locale, tt.content); got != tt.want {
				t.Fatalf("localizeRenderableMessageContent() = %q, want %q", got, tt.want)
			}
		})
	}
}
