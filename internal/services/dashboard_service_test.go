package services

import (
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/i18nx"
	"testing"
)

func TestDashboardTextUsesEnglishLocale(t *testing.T) {
	t.Parallel()

	if got := dashboardText(i18nx.LocaleEnUS, "alert.pendingLongWait.title"); got != "Queued conversations are piling up" {
		t.Fatalf("dashboardText() = %q", got)
	}
	if got := dashboardText(i18nx.LocaleZhCN, "alert.pendingLongWait.title"); got != "待接入会话堆积" {
		t.Fatalf("dashboardText() = %q", got)
	}
}

func TestConversationStatusLabelUsesEnglishLocale(t *testing.T) {
	t.Parallel()

	if got := conversationStatusLabel(enums.IMConversationStatusPending, i18nx.LocaleEnUS); got != "Queued" {
		t.Fatalf("conversationStatusLabel() = %q", got)
	}
	if got := conversationStatusLabel(enums.IMConversationStatusPending, i18nx.LocaleZhCN); got != "待接入" {
		t.Fatalf("conversationStatusLabel() = %q", got)
	}
}

func TestDashboardQuickLinksUseCurrentTenantPages(t *testing.T) {
	t.Parallel()

	wantLinks := []string{
		"/dashboard/conversations",
		"/dashboard/agents",
		"/dashboard/knowledge",
		"/dashboard/settings",
	}
	links := buildDashboardQuickLinks(i18nx.LocaleZhCN)
	if len(links) != len(wantLinks) {
		t.Fatalf("buildDashboardQuickLinks() length = %d, want %d", len(links), len(wantLinks))
	}
	for i, want := range wantLinks {
		if links[i].Link != want {
			t.Fatalf("buildDashboardQuickLinks()[%d].Link = %q, want %q", i, links[i].Link, want)
		}
	}
	if links[len(links)-1].Title != "接入设置" {
		t.Fatalf("settings quick link title = %q", links[len(links)-1].Title)
	}
}
