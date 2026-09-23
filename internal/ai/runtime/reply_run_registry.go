package runtime

import (
	"context"
	"sync"
)

type activeAIReplyRun struct {
	conversationID int64
	messageID      int64
	cancel         context.CancelFunc
}

type activeAIReplyRunRegistry struct {
	mu             sync.Mutex
	byConversation map[int64]*activeAIReplyRun
}

func newActiveAIReplyRunRegistry() *activeAIReplyRunRegistry {
	return &activeAIReplyRunRegistry{byConversation: make(map[int64]*activeAIReplyRun)}
}

func (r *activeAIReplyRunRegistry) begin(conversationID, messageID int64, cancel context.CancelFunc) (*activeAIReplyRun, bool) {
	if r == nil || conversationID <= 0 || messageID <= 0 || cancel == nil {
		return &activeAIReplyRun{conversationID: conversationID, messageID: messageID, cancel: cancel}, true
	}
	run := &activeAIReplyRun{conversationID: conversationID, messageID: messageID, cancel: cancel}
	r.mu.Lock()
	current := r.byConversation[conversationID]
	if current != nil && current.messageID >= messageID {
		r.mu.Unlock()
		cancel()
		return nil, false
	}
	r.byConversation[conversationID] = run
	r.mu.Unlock()
	if current != nil {
		current.cancel()
	}
	return run, true
}

func (r *activeAIReplyRunRegistry) finish(run *activeAIReplyRun) {
	if r == nil || run == nil || run.conversationID <= 0 {
		return
	}
	r.mu.Lock()
	if r.byConversation[run.conversationID] == run {
		delete(r.byConversation, run.conversationID)
	}
	r.mu.Unlock()
}

func (r *activeAIReplyRunRegistry) cancelOlder(conversationID, messageID int64) {
	if r == nil || conversationID <= 0 || messageID <= 0 {
		return
	}
	r.mu.Lock()
	current := r.byConversation[conversationID]
	if current == nil || current.messageID >= messageID {
		r.mu.Unlock()
		return
	}
	delete(r.byConversation, conversationID)
	r.mu.Unlock()
	current.cancel()
}
