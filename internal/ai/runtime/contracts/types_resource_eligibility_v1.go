package contracts

const ResourceEligibilityV1SchemaVersion = SchemaResourceEligibilityV1

type ResourceEligibilityV1 struct {
	SchemaVersion string                      `json:"schemaVersion"`
	Items         []ResourceEligibilityItemV1 `json:"items"`
}

type ResourceEligibilityItemV1 struct {
	ResourceRef       string `json:"resourceRef"`
	TaskKey           string `json:"taskKey"`
	SourceEvidenceRef string `json:"sourceEvidenceRef,omitempty"`
	SourceRecordID    string `json:"sourceRecordId,omitempty"`
	ResourceType      string `json:"resourceType"`
	ResourcePurpose   string `json:"resourcePurpose"`
	TopicMatch        string `json:"topicMatch"`
	RequestMatch      string `json:"requestMatch"`
	AutoAttach        bool   `json:"autoAttach"`
	Decision          string `json:"decision"`
	ReasonCode        string `json:"reasonCode"`
}
