package factory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type failingToolCallingChatModel struct {
	model.ToolCallingChatModel
	calls atomic.Int32
}

func (m *failingToolCallingChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls.Add(1)
	return nil, errors.New("upstream unavailable")
}

func TestRetryingToolCallingChatModelUsesInitialCallPlusTwoRetries(t *testing.T) {
	delegate := &failingToolCallingChatModel{}
	wrapped := newRetryingToolCallingChatModel(delegate, 2)

	if _, err := wrapped.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err == nil {
		t.Fatal("expected upstream failure")
	}
	if got := delegate.calls.Load(); got != 3 {
		t.Fatalf("upstream calls=%d want 3", got)
	}
}
