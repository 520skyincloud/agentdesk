package enums

const (
	KnowledgeRetrievalModeFastGPT = "fastgpt"
)

func IsKnowledgeRetrievalMode(value string) bool {
	switch value {
	case KnowledgeRetrievalModeFastGPT:
		return true
	default:
		return false
	}
}
