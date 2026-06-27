package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var KnowledgeCandidateService = newKnowledgeCandidateService()

func newKnowledgeCandidateService() *knowledgeCandidateService {
	return &knowledgeCandidateService{}
}

type knowledgeCandidateService struct{}

func (s *knowledgeCandidateService) Get(id int64) *models.KnowledgeCandidate {
	if id <= 0 {
		return nil
	}
	return repositories.KnowledgeCandidateRepository.Get(sqls.DB(), id)
}

func (s *knowledgeCandidateService) FindPageByCnd(cnd *sqls.Cnd) ([]models.KnowledgeCandidate, *sqls.Paging) {
	return repositories.KnowledgeCandidateRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *knowledgeCandidateService) UpsertCandidate(storeID, knowledgeBaseID, conversationID int64, messageIDs []int64, source enums.KnowledgeCandidateSource, question, answer, summary, evidence string, confidence float64, createdBy string) (*models.KnowledgeCandidate, error) {
	question = strings.TrimSpace(question)
	answer = strings.TrimSpace(answer)
	if question == "" {
		return nil, errorsx.InvalidParam("问题不能为空")
	}
	key := buildKnowledgeCandidateSimilarityKey(storeID, knowledgeBaseID, question)
	now := time.Now()
	if existing := repositories.KnowledgeCandidateRepository.Take(sqls.DB(), "similarity_key = ? AND store_id = ? AND knowledge_base_id = ? AND status <> ?", key, storeID, knowledgeBaseID, enums.KnowledgeCandidateStatusRejected); existing != nil {
		updates := map[string]any{
			"frequency":        existing.Frequency + 1,
			"updated_at":       now,
			"update_user_name": "system",
		}
		if answer != "" && strings.TrimSpace(existing.Answer) == "" {
			updates["answer"] = answer
		}
		if evidence != "" {
			updates["evidence_text"] = mergeEvidence(existing.EvidenceText, evidence)
		}
		if err := repositories.KnowledgeCandidateRepository.Updates(sqls.DB(), existing.ID, updates); err != nil {
			return nil, err
		}
		return s.Get(existing.ID), nil
	}
	item := &models.KnowledgeCandidate{
		StoreID:         storeID,
		KnowledgeBaseID: knowledgeBaseID,
		ConversationID:  conversationID,
		MessageIDs:      utils.JoinInt64s(messageIDs),
		Source:          source,
		Question:        question,
		Answer:          answer,
		Summary:         strings.TrimSpace(summary),
		EvidenceText:    strings.TrimSpace(evidence),
		Frequency:       1,
		SimilarityKey:   key,
		Status:          enums.KnowledgeCandidateStatusPending,
		Confidence:      confidence,
		CreatedBy:       strings.TrimSpace(createdBy),
		AuditFields:     utils.BuildAuditFields(nil),
	}
	if err := repositories.KnowledgeCandidateRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *knowledgeCandidateService) Update(req request.UpdateKnowledgeCandidateRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.Get(req.ID)
	if item == nil {
		return errorsx.InvalidParam("待归档问答不存在")
	}
	status := item.Status
	if strings.TrimSpace(req.Status) != "" {
		status = enums.KnowledgeCandidateStatus(strings.TrimSpace(req.Status))
	}
	return repositories.KnowledgeCandidateRepository.Updates(sqls.DB(), item.ID, map[string]any{
		"question":         strings.TrimSpace(req.Question),
		"answer":           strings.TrimSpace(req.Answer),
		"summary":          strings.TrimSpace(req.Summary),
		"confidence":       req.Confidence,
		"status":           status,
		"similarity_key":   buildKnowledgeCandidateSimilarityKey(item.StoreID, item.KnowledgeBaseID, req.Question),
		"updated_at":       time.Now(),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}

func (s *knowledgeCandidateService) Approve(id int64, operator *dto.AuthPrincipal) error {
	return s.review(id, enums.KnowledgeCandidateStatusApproved, operator)
}

func (s *knowledgeCandidateService) Reject(id int64, operator *dto.AuthPrincipal) error {
	return s.review(id, enums.KnowledgeCandidateStatusRejected, operator)
}

func (s *knowledgeCandidateService) MarkImported(id int64, operator *dto.AuthPrincipal) error {
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("待归档问答不存在")
	}
	now := time.Now()
	updates := map[string]any{
		"status":           enums.KnowledgeCandidateStatusImported,
		"imported_at":      now,
		"updated_at":       now,
		"update_user_id":   int64(0),
		"update_user_name": "",
	}
	if operator != nil {
		updates["update_user_id"] = operator.UserID
		updates["update_user_name"] = operator.Username
	}
	return repositories.KnowledgeCandidateRepository.Updates(sqls.DB(), item.ID, updates)
}

func (s *knowledgeCandidateService) review(id int64, status enums.KnowledgeCandidateStatus, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("待归档问答不存在")
	}
	now := time.Now()
	return repositories.KnowledgeCandidateRepository.Updates(sqls.DB(), item.ID, map[string]any{
		"status":           status,
		"review_user_id":   operator.UserID,
		"review_user_name": operator.Username,
		"reviewed_at":      now,
		"updated_at":       now,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}

func (s *knowledgeCandidateService) ExportWeekly(req request.ExportKnowledgeCandidateWeeklyRequest, operator *dto.AuthPrincipal) (*response.KnowledgeCandidateExportResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	year, week := req.Year, req.Week
	if year <= 0 || week <= 0 {
		year, week = time.Now().ISOWeek()
	}
	status := enums.KnowledgeCandidateStatusApproved
	if strings.TrimSpace(req.Status) != "" {
		status = enums.KnowledgeCandidateStatus(strings.TrimSpace(req.Status))
	}
	cnd := sqls.NewCnd().Eq("status", status).Desc("frequency").Desc("id")
	if req.StoreID > 0 {
		cnd.Eq("store_id", req.StoreID)
	}
	list := repositories.KnowledgeCandidateRepository.Find(sqls.DB(), cnd)
	if len(list) == 0 {
		return &response.KnowledgeCandidateExportResponse{Count: 0}, nil
	}
	byStore := make(map[int64][]models.KnowledgeCandidate)
	for _, item := range list {
		byStore[item.StoreID] = append(byStore[item.StoreID], item)
	}
	var firstMarkdownPath, firstJSONLPath string
	total := 0
	for storeID, items := range byStore {
		storeCode := fmt.Sprintf("store-%d", storeID)
		if store := StoreService.Get(storeID); store != nil && strings.TrimSpace(store.StoreCode) != "" {
			storeCode = strings.TrimSpace(store.StoreCode)
		}
		dir := filepath.Join("exports", "knowledge-candidates", storeCode)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		base := fmt.Sprintf("%04d-W%02d", year, week)
		mdPath := filepath.Join(dir, base+".md")
		jsonlPath := filepath.Join(dir, base+".jsonl")
		if err := writeKnowledgeCandidateMarkdown(mdPath, storeCode, year, week, items); err != nil {
			return nil, err
		}
		if err := writeKnowledgeCandidateJSONL(jsonlPath, items); err != nil {
			return nil, err
		}
		if firstMarkdownPath == "" {
			firstMarkdownPath = mdPath
			firstJSONLPath = jsonlPath
		}
		total += len(items)
		now := time.Now()
		for _, item := range items {
			_ = repositories.KnowledgeCandidateRepository.Updates(sqls.DB(), item.ID, map[string]any{
				"status":           enums.KnowledgeCandidateStatusExported,
				"exported_at":      now,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			})
		}
	}
	return &response.KnowledgeCandidateExportResponse{
		MarkdownPath: firstMarkdownPath,
		JSONLPath:    firstJSONLPath,
		Count:        total,
	}, nil
}

func (s *knowledgeCandidateService) ExtractFromResolvedConversation(conversationID int64, source enums.KnowledgeCandidateSource) (*models.KnowledgeCandidate, error) {
	state := ConversationRouteService.GetByConversationID(conversationID)
	if state == nil {
		return nil, nil
	}
	messages := MessageService.Find(sqls.NewCnd().Eq("conversation_id", conversationID).Desc("seq_no").Limit(20))
	var question, answer string
	var messageIDs []int64
	var evidence strings.Builder
	for _, item := range messages {
		messageIDs = append(messageIDs, item.ID)
		line := fmt.Sprintf("%s: %s\n", item.SenderType, strings.TrimSpace(item.Content))
		evidence.WriteString(line)
		if question == "" && item.SenderType == enums.IMSenderTypeCustomer {
			question = item.Content
		}
		if answer == "" && item.SenderType == enums.IMSenderTypeAgent {
			answer = item.Content
		}
	}
	if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
		return nil, nil
	}
	return s.UpsertCandidate(state.StoreID, state.KnowledgeBaseID, conversationID, messageIDs, source, question, answer, "人工会话自动沉淀", evidence.String(), 0.6, string(source))
}

func buildKnowledgeCandidateSimilarityKey(storeID, knowledgeBaseID int64, question string) string {
	re := regexp.MustCompile(`[\s[:punct:]]+`)
	normalized := strings.ToLower(strings.TrimSpace(question))
	normalized = re.ReplaceAllString(normalized, "")
	if len([]rune(normalized)) > 80 {
		normalized = string([]rune(normalized)[:80])
	}
	return fmt.Sprintf("%d:%d:%s", storeID, knowledgeBaseID, normalized)
}

func mergeEvidence(oldValue, newValue string) string {
	oldValue = strings.TrimSpace(oldValue)
	newValue = strings.TrimSpace(newValue)
	if oldValue == "" {
		return newValue
	}
	if newValue == "" || strings.Contains(oldValue, newValue) {
		return oldValue
	}
	return oldValue + "\n---\n" + newValue
}

func writeKnowledgeCandidateMarkdown(path, storeCode string, year, week int, items []models.KnowledgeCandidate) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s %04d-W%02d 待归档问答\n\n", storeCode, year, week))
	for _, item := range items {
		b.WriteString(fmt.Sprintf("## %s\n\n", strings.TrimSpace(item.Question)))
		b.WriteString(fmt.Sprintf("- 门店ID：%d\n", item.StoreID))
		b.WriteString(fmt.Sprintf("- 知识库ID：%d\n", item.KnowledgeBaseID))
		b.WriteString(fmt.Sprintf("- 来源：%s\n", item.Source))
		b.WriteString(fmt.Sprintf("- 频次：%d\n", item.Frequency))
		b.WriteString(fmt.Sprintf("- 状态：%s\n", item.Status))
		b.WriteString(fmt.Sprintf("- 会话ID：%d\n\n", item.ConversationID))
		b.WriteString("**建议答案**\n\n")
		b.WriteString(strings.TrimSpace(item.Answer))
		b.WriteString("\n\n**证据**\n\n")
		b.WriteString("```text\n")
		b.WriteString(strings.TrimSpace(item.EvidenceText))
		b.WriteString("\n```\n\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

func writeKnowledgeCandidateJSONL(path string, items []models.KnowledgeCandidate) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := w.WriteString(string(line) + "\n"); err != nil {
			return err
		}
	}
	return nil
}
