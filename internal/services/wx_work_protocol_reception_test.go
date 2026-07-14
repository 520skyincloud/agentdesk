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

func TestBuildRuntimeAIAgentUsesNeutralReceptionIdentity(t *testing.T) {
	agent := WxWorkProtocolInstanceService.BuildRuntimeAIAgent(&models.WxWorkProtocolInstance{
		PersonaPrompt: DefaultWxWorkProtocolPersonaPrompt,
		FrontDeskMode: wxWorkFrontDeskModeUnmanned,
	})
	if strings.Contains(agent.SystemPrompt, "你是酒店前台同事") {
		t.Fatalf("legacy front desk identity leaked: %s", agent.SystemPrompt)
	}
	if !strings.Contains(agent.SystemPrompt, "你是线上酒店接待") {
		t.Fatalf("neutral identity missing: %s", agent.SystemPrompt)
	}
}
