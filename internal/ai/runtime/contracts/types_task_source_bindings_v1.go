package contracts

const TaskSourceBindingsV1SchemaVersion = "task_source_bindings.v1"

// TaskSourceBindingsV1 persists authoritative message-analysis evidence without
// storing a second copy of the customer text.
type TaskSourceBindingsV1 struct {
	SchemaVersion    string                    `json:"schemaVersion"`
	PrimaryMessageID int64                     `json:"primaryMessageId"`
	Bindings         []TaskSourceBindingItemV1 `json:"bindings"`
}

type TaskSourceBindingItemV1 struct {
	MessageID             int64   `json:"messageId"`
	AnalysisRevision      int     `json:"analysisRevision"`
	Start                 int     `json:"start"`
	End                   int     `json:"end"`
	ObservationMessageIDs []int64 `json:"observationMessageIds"`
}
