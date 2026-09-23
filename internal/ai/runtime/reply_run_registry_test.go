package runtime

import (
	"context"
	"testing"
)

func TestActiveAIReplyRunRegistryCancelsOnlyOlderConversationRun(t *testing.T) {
	registry := newActiveAIReplyRunRegistry()
	firstCtx, firstCancel := context.WithCancel(context.Background())
	first, accepted := registry.begin(88, 100, firstCancel)
	if !accepted || first == nil {
		t.Fatal("first run must be accepted")
	}

	secondCtx, secondCancel := context.WithCancel(context.Background())
	second, accepted := registry.begin(88, 101, secondCancel)
	if !accepted || second == nil {
		t.Fatal("newer run must replace the older run")
	}
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("newer message must cancel the older model run immediately")
	}

	registry.finish(first)
	select {
	case <-secondCtx.Done():
		t.Fatal("older goroutine cleanup must not cancel or remove the newer run")
	default:
	}

	registry.cancelOlder(88, 102)
	select {
	case <-secondCtx.Done():
	default:
		t.Fatal("a newly committed customer message must cancel the active older run before reply routing")
	}

	_, thirdCancel := context.WithCancel(context.Background())
	third, accepted := registry.begin(88, 103, thirdCancel)
	if !accepted || third == nil {
		t.Fatal("conversation must accept a new run after the previous run was cancelled")
	}

	staleCtx, staleCancel := context.WithCancel(context.Background())
	if stale, ok := registry.begin(88, 99, staleCancel); ok || stale != nil {
		t.Fatal("out-of-order older trigger must not replace the active newer run")
	}
	select {
	case <-staleCtx.Done():
	default:
		t.Fatal("rejected stale run must be cancelled")
	}

	registry.finish(second)
	registry.finish(third)
	if current := registry.byConversation[88]; current != nil {
		t.Fatalf("latest run cleanup must clear the registry, got %#v", current)
	}
}
