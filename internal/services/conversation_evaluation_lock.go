package services

import "sync"

const conversationEvaluationLockStripes = 64

var conversationEvaluationLocks [conversationEvaluationLockStripes]sync.Mutex

// SQLite has no row-level FOR UPDATE. The process lock complements database
// row locks and serializes submissions for one evaluation token.
func lockConversationEvaluation(tokenHash string) func() {
	key := uint64(1469598103934665603)
	for i := 0; i < len(tokenHash); i++ {
		key ^= uint64(tokenHash[i])
		key *= 1099511628211
	}
	index := key % conversationEvaluationLockStripes
	conversationEvaluationLocks[index].Lock()
	return conversationEvaluationLocks[index].Unlock
}
