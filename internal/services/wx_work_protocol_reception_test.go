package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
)

func TestAppendWxWorkReceptionContextUnmanned(t *testing.T) {
	text := appendWxWorkReceptionContext("基础人设", &models.WxWorkProtocolInstance{
		FrontDeskMode: wxWorkFrontDeskModeUnmanned,
	}, time.Date(2026, 7, 14, 10, 0, 0, 0, time.Local))
	if !strings.Contains(text, "无人化酒店") || !strings.Contains(text, "不得无依据引导客人去前台") {
		t.Fatalf("unexpected unmanned context: %s", text)
	}
}

func TestAppendWxWorkReceptionContextStaffedDoesNotPromiseAction(t *testing.T) {
	text := appendWxWorkReceptionContext("基础人设", &models.WxWorkProtocolInstance{
		FrontDeskMode: wxWorkFrontDeskModeStaffed,
	}, time.Date(2026, 7, 14, 10, 0, 0, 0, time.Local))
	if !strings.Contains(text, "有前台酒店") || !strings.Contains(text, "不代表前台已经接单") {
		t.Fatalf("unexpected staffed context: %s", text)
	}
}

func TestAppendWxWorkReceptionContextScheduledUsesCurrentWindow(t *testing.T) {
	active := appendWxWorkReceptionContext("基础人设", &models.WxWorkProtocolInstance{
		FrontDeskMode:  wxWorkFrontDeskModeScheduled,
		FrontDeskHours: "08:00-22:00",
	}, time.Date(2026, 7, 14, 10, 0, 0, 0, time.Local))
	if !strings.Contains(active, "当前处于该时段") {
		t.Fatalf("unexpected active scheduled context: %s", active)
	}

	inactive := appendWxWorkReceptionContext("基础人设", &models.WxWorkProtocolInstance{
		FrontDeskMode:  wxWorkFrontDeskModeScheduled,
		FrontDeskHours: "08:00-22:00",
	}, time.Date(2026, 7, 14, 23, 0, 0, 0, time.Local))
	if !strings.Contains(inactive, "当前不在该时段") {
		t.Fatalf("unexpected inactive scheduled context: %s", inactive)
	}
}

func TestBuildRuntimeAIAgentUsesDefaultXiaoQiPersona(t *testing.T) {
	agent := WxWorkProtocolInstanceService.BuildRuntimeAIAgent(&models.WxWorkProtocolInstance{
		FrontDeskMode: wxWorkFrontDeskModeUnmanned,
	})
	for _, expected := range []string{
		"酒店前台同事小七",
		"礼貌、温和、有耐心",
		"您、为您、这边、呀、啦、～",
		"没有真实工具、资源提交或接待结果时",
		"不得承诺已经通知、安排、处理或稍后完成",
	} {
		if !strings.Contains(agent.SystemPrompt, expected) {
			t.Fatalf("default persona missing %q: %s", expected, agent.SystemPrompt)
		}
	}
}

func TestNormalizeWxWorkPersonaPromptFallsBackToDefault(t *testing.T) {
	if got := normalizeWxWorkPersonaPrompt(" \n\t "); got != DefaultWxWorkProtocolPersonaPrompt {
		t.Fatalf("blank persona must fall back to the default, got %q", got)
	}
}

func TestMergeWxWorkPersonaIntoSystemPromptKeepsCustomPersona(t *testing.T) {
	custom := "客人问早餐时优先给出明确时间。"
	got := mergeWxWorkPersonaIntoSystemPrompt(DefaultWxWorkProtocolPersonaPrompt, custom)
	if !strings.Contains(got, DefaultWxWorkProtocolPersonaPrompt) || !strings.Contains(got, custom) {
		t.Fatalf("custom persona must extend the default prompt, got %q", got)
	}
	if strings.Count(got, custom) != 1 {
		t.Fatalf("custom persona must only be merged once, got %q", got)
	}
	if again := mergeWxWorkPersonaIntoSystemPrompt(got, custom); again != got {
		t.Fatalf("merging the same custom persona must remain idempotent, got %q", again)
	}
}
