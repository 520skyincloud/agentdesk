package replyruntime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	ManualResumeContractV2     = "v2"
	ManualResumeContractLegacy = "legacy"

	// ManualResumeDeferredKnowledgeOutput marks a knowledge task that did not
	// produce customer-visible text and is therefore eligible to run again after
	// the manual waiting window ends.
	ManualResumeDeferredKnowledgeOutput = "deferred_knowledge_handoff"
)

// IsStableManualResumeClientMessageID identifies Commit IDs whose logical
// ownership is bound to a manual-resume notice, Task set, or resource set.
// These IDs allow a retry to treat the already persisted message as the
// authoritative payload while repairing only its external delivery.
func IsStableManualResumeClientMessageID(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "ai_manual_resume_") {
		return false
	}
	return strings.Contains(value, "_notice_") ||
		strings.Contains(value, "_task_") ||
		strings.Contains(value, "_resource_")
}

var stableOwnedManualResumeClientMessageIDPattern = regexp.MustCompile(`^ai_manual_resume_([0-9a-f]{48})_(?:task|resource)_[0-9a-f]{16}_([1-9][0-9]*)$`)

// IsStableOwnedManualResumeClientMessageID identifies customer-visible
// manual-resume Commit messages whose ID carries Task or resource ownership.
// Notices and legacy/text-only IDs are intentionally excluded because they do
// not prove that the deferred business work was committed.
func IsStableOwnedManualResumeClientMessageID(value string) bool {
	return stableOwnedManualResumeClientMessageIDPattern.MatchString(strings.TrimSpace(value))
}

// StableOwnedManualResumeClientMessageIDMatches binds the stable Commit ID to
// the exact manual-resume request and physical source message.
func StableOwnedManualResumeClientMessageIDMatches(value string, requestID string, sourceMessageID int64) bool {
	if sourceMessageID <= 0 || strings.TrimSpace(requestID) == "" {
		return false
	}
	matches := stableOwnedManualResumeClientMessageIDPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 3 {
		return false
	}
	parsedSourceID, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil || parsedSourceID != sourceMessageID {
		return false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	return matches[1] == fmt.Sprintf("%x", sum[:24])
}

type ManualResumeSource struct {
	Ref         string `json:"ref,omitempty"`
	MessageID   int64  `json:"messageId,omitempty"`
	MessageType string `json:"messageType,omitempty"`
	Text        string `json:"text,omitempty"`
}

type ManualResumeEntity struct {
	Text string `json:"text,omitempty"`
	Type string `json:"type,omitempty"`
}

type ManualResumeTaskPlan struct {
	TaskID             string               `json:"taskId,omitempty"`
	Intent             string               `json:"intent,omitempty"`
	SubIntent          string               `json:"subIntent,omitempty"`
	Objective          string               `json:"objective,omitempty"`
	RelationToPrevious string               `json:"relationToPrevious,omitempty"`
	ResolutionState    string               `json:"resolutionState,omitempty"`
	Entities           []ManualResumeEntity `json:"entities,omitempty"`
	Text               string               `json:"text,omitempty"`
	OriginalText       string               `json:"originalText,omitempty"`
	ResolvedText       string               `json:"resolvedText,omitempty"`
	SourceRefs         []string             `json:"sourceRefs,omitempty"`
	NeedsKnowledge     bool                 `json:"needsKnowledge,omitempty"`
	NeedsResource      bool                 `json:"needsResource,omitempty"`
	NeedsTool          bool                 `json:"needsTool,omitempty"`
	NeedsHumanRoute    bool                 `json:"needsHumanRoute,omitempty"`
	OutputKind         string               `json:"outputKind,omitempty"`
	ReplyRequired      bool                 `json:"replyRequired,omitempty"`
	Output             string               `json:"output,omitempty"`
	ResourceAction     string               `json:"resourceAction,omitempty"`
	MissingAspects     []string             `json:"missingAspects,omitempty"`
}

type ManualResumeSnapshot struct {
	RunLogID         int64
	ContractMode     string
	SourcesValidated bool
	FrozenTasks      []ManualResumeTaskPlan
	Sources          []ManualResumeSource
	NewSources       []ManualResumeSource
}

type manualResumeSnapshotContextKey struct{}

func WithManualResumeSnapshot(ctx context.Context, snapshot ManualResumeSnapshot) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, manualResumeSnapshotContextKey{}, cloneManualResumeSnapshot(snapshot))
}

func ManualResumeSnapshotFromContext(ctx context.Context) (ManualResumeSnapshot, bool) {
	if ctx == nil {
		return ManualResumeSnapshot{}, false
	}
	snapshot, ok := ctx.Value(manualResumeSnapshotContextKey{}).(ManualResumeSnapshot)
	if !ok || len(snapshot.FrozenTasks) == 0 {
		return ManualResumeSnapshot{}, false
	}
	return cloneManualResumeSnapshot(snapshot), true
}

func cloneManualResumeSnapshot(snapshot ManualResumeSnapshot) ManualResumeSnapshot {
	ret := snapshot
	ret.FrozenTasks = append([]ManualResumeTaskPlan(nil), snapshot.FrozenTasks...)
	for index := range ret.FrozenTasks {
		ret.FrozenTasks[index].Entities = append([]ManualResumeEntity(nil), ret.FrozenTasks[index].Entities...)
		ret.FrozenTasks[index].SourceRefs = append([]string(nil), ret.FrozenTasks[index].SourceRefs...)
		ret.FrozenTasks[index].MissingAspects = append([]string(nil), ret.FrozenTasks[index].MissingAspects...)
	}
	ret.Sources = append([]ManualResumeSource(nil), snapshot.Sources...)
	ret.NewSources = append([]ManualResumeSource(nil), snapshot.NewSources...)
	return ret
}
