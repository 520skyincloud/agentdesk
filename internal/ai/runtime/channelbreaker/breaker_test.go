package channelbreaker

import (
	"testing"
	"time"
)

func TestBreakerOpensAfterThresholdAndResetsOnSuccess(t *testing.T) {
	Reset()
	now := time.Now()
	stage, model := "vision", "qwen3-vl-plus"

	// 前 4 次失败不熔断。
	for i := 0; i < FailureThreshold-1; i++ {
		RecordFailure(stage, model, now)
		if open, _ := IsOpen(stage, model, now); open {
			t.Fatalf("breaker opened too early at failure %d", i+1)
		}
	}
	// 第 5 次失败熔断。
	RecordFailure(stage, model, now)
	if open, _ := IsOpen(stage, model, now); !open {
		t.Fatalf("breaker should be open after %d failures", FailureThreshold)
	}

	// 成功一次后复位。
	RecordSuccess(stage, model)
	if open, _ := IsOpen(stage, model, now); open {
		t.Fatalf("breaker should reset after success")
	}
}

func TestBreakerReopensAfterWindow(t *testing.T) {
	Reset()
	now := time.Now()
	stage, model := "intent_detect", "deepseek-v4-flash"
	for i := 0; i < FailureThreshold; i++ {
		RecordFailure(stage, model, now)
	}
	if open, _ := IsOpen(stage, model, now); !open {
		t.Fatalf("breaker should be open")
	}
	// 窗口过后自动复位。
	later := now.Add(OpenDuration + time.Second)
	if open, _ := IsOpen(stage, model, later); open {
		t.Fatalf("breaker should reset after open window")
	}
}

func TestKeyEmptyForMissingPart(t *testing.T) {
	if Key("", "model") != "" || Key("stage", "") != "" {
		t.Fatalf("key should be empty when stage or model is missing")
	}
}
