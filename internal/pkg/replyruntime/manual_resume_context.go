package replyruntime

import "context"

const (
	ManualResumeContractV2     = "v2"
	ManualResumeContractLegacy = "legacy"
)

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
