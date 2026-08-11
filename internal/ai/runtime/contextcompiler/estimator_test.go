package contextcompiler

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestConservativeEstimatorFormula(t *testing.T) {
	estimator := ConservativeEstimator{}
	if got := estimator.CountText("", "停车ABC"); got != 3 {
		t.Fatalf("CountText()=%d want=3", got)
	}
	messages := []*schema.Message{schema.UserMessage("停车ABC"), schema.AssistantMessage("可以", nil)}
	if got := estimator.CountMessages("", messages); got != 37 {
		t.Fatalf("CountMessages()=%d want=37", got)
	}
}
