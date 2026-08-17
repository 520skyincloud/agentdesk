package runtime

import (
	"strings"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	svc "agent-desk/internal/services"
)

var AIReplyService = newAIReplyService()

func init() {
	svc.TriggerAIReplySyncHook = AIReplyService.TriggerReplySync
}

func newAIReplyService() *aiReplyService {
	return &aiReplyService{
		eligibility: newReplyEligibility(),
		executor:    newRuntimeReplyExecutor(),
		interrupts:  newReplyInterruptService(),
		commit:      newReplyCommitService(),
		runlog:      newReplyRunLogService(),
		memory:      newConversationMemoryService(),
	}
}

type aiReplyService struct {
	eligibility *replyEligibility
	executor    *runtimeReplyExecutor
	interrupts  *replyInterruptService
	commit      replyCommitter
	runlog      *replyRunLogService
	memory      *conversationMemoryService
}

type replyCommitter interface {
	HasStructuredVariableReply(trace *aiReplyTraceData) bool
	CommitAIReplyBatch(input replyCommitInput) ([]models.Message, error)
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
