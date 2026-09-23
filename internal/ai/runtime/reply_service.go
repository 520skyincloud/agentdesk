package runtime

import (
	"strings"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	svc "agent-desk/internal/services"
)

var AIReplyService = newAIReplyService()

func init() {
	svc.TriggerAIReplyAsyncHook = AIReplyService.TriggerReplyAsync
	svc.TriggerAIReplySyncHook = AIReplyService.TriggerReplySync
	svc.TriggerStandaloneOneReplyAsyncHook = AIReplyService.TriggerStandaloneOneReplyAsync
	svc.CancelOlderAIReplyRunHook = AIReplyService.CancelOlderReplyRun
}

func (s *aiReplyService) CancelOlderReplyRun(conversationID int64, messageID int64) {
	if s == nil || s.activeRuns == nil {
		return
	}
	s.activeRuns.cancelOlder(conversationID, messageID)
}

func newAIReplyService() *aiReplyService {
	return &aiReplyService{
		eligibility: newReplyEligibility(),
		executor:    newRuntimeReplyExecutor(),
		interrupts:  newReplyInterruptService(),
		commit:      newReplyCommitService(),
		runlog:      newReplyRunLogService(),
		memory:      newConversationMemoryService(),
		activeRuns:  newActiveAIReplyRunRegistry(),
	}
}

type aiReplyService struct {
	eligibility *replyEligibility
	executor    *runtimeReplyExecutor
	interrupts  *replyInterruptService
	commit      *replyCommitService
	runlog      *replyRunLogService
	memory      *conversationMemoryService
	activeRuns  *activeAIReplyRunRegistry
}

func firstInvokedToolCode(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	if len(summary.InvokedToolCodes) > 0 {
		return strings.TrimSpace(summary.InvokedToolCodes[0])
	}
	return ""
}
