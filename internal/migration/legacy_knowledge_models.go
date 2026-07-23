package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

// Migration-only views of the retired local knowledge tables. They preserve
// historical upgrades without registering the tables in the active schema.
type legacyKnowledgeDocument struct {
	ID              int64
	TenantID        int64
	KnowledgeBaseID int64
	Title           string
	ContentType     string
	Content         string
	Status          enums.Status
	IndexStatus     string
	IndexedAt       *time.Time
	IndexError      string
	ContentHash     string
	models.AuditFields
}

func (legacyKnowledgeDocument) TableName() string { return "t_knowledge_document" }

type legacyKnowledgeFAQ struct {
	ID               int64
	TenantID         int64
	KnowledgeBaseID  int64
	Question         string
	Answer           string
	SimilarQuestions string
	Status           enums.Status
	IndexStatus      string
	IndexedAt        *time.Time
	IndexError       string
	Remark           string
	models.AuditFields
}

func (legacyKnowledgeFAQ) TableName() string { return "t_knowledge_faq" }

type legacyKnowledgeChunk struct {
	ID              int64
	TenantID        int64
	KnowledgeBaseID int64
	DocumentID      int64
	FaqID           int64
	ChunkNo         int
	ChunkType       string
	Title           string
	SectionPath     string
	Content         string
	TokenCount      int
	VectorID        string
	Status          enums.Status
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (legacyKnowledgeChunk) TableName() string { return "t_knowledge_chunk" }

const (
	legacyKnowledgeContentTypeMarkdown = "markdown"
	legacyKnowledgeIndexStatusPending  = "pending"
	legacyKnowledgeIndexStatusIndexed  = "indexed"
)
