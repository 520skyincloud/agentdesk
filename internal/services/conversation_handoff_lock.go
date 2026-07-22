package services

import "sync"

const conversationHandoffLockStripes = 64

var conversationHandoffLocks [conversationHandoffLockStripes]sync.Mutex

// SQLite does not provide row-level FOR UPDATE semantics. A striped process
// lock complements database row locks and keeps local retries deterministic.
func lockConversationHandoff(conversationID int64) func() {
	index := uint64(conversationID) % conversationHandoffLockStripes
	conversationHandoffLocks[index].Lock()
	return conversationHandoffLocks[index].Unlock
}
