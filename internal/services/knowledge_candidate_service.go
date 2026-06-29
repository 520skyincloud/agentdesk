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
	if !s.canReviewCandidate(item, operator) {
		return errorsx.Forbidden("无权限维护该门店知识进化候选")
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

func (s *knowledgeCandidateService) BatchApprove(ids []int64, operator *dto.AuthPrincipal) error {
	return s.batchReview(ids, enums.KnowledgeCandidateStatusApproved, operator)
}

func (s *knowledgeCandidateService) BatchReject(ids []int64, operator *dto.AuthPrincipal) error {
	return s.batchReview(ids, enums.KnowledgeCandidateStatusRejected, operator)
}

func (s *knowledgeCandidateService) QualityCheck(ids []int64) (*response.KnowledgeCandidateQualityCheckResponse, error) {
	ids = uniquePositiveInt64s(ids)
	if len(ids) == 0 {
		return nil, errorsx.InvalidParam("请选择待归档问答")
	}
	ret := &response.KnowledgeCandidateQualityCheckResponse{
		Reports: make([]response.KnowledgeCandidateQualityReport, 0, len(ids)),
	}
	for _, id := range ids {
		item := s.Get(id)
		if item == nil {
			return nil, errorsx.InvalidParam("待归档问答不存在")
		}
		report := s.qualityCheckOne(item)
		ret.Reports = append(ret.Reports, report)
		switch report.Decision {
		case "approve":
			ret.ApproveIDs = append(ret.ApproveIDs, item.ID)
		case "reject":
			ret.RejectIDs = append(ret.RejectIDs, item.ID)
		default:
			ret.ReviewIDs = append(ret.ReviewIDs, item.ID)
		}
	}
	return ret, nil
}

func (s *knowledgeCandidateService) AnalyzeConversation(conversationID int64, operator *dto.AuthPrincipal) (*response.KnowledgeCandidateAnalyzeResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	route := ConversationRouteService.GetByConversationID(conversationID)
	if route == nil || route.StoreID <= 0 || route.KnowledgeBaseID <= 0 {
		return &response.KnowledgeCandidateAnalyzeResponse{Skipped: true, Reason: "会话未绑定门店或知识库，不能归档"}, nil
	}
	messages := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("conversation_id", conversationID).
		In("message_type", []string{string(enums.IMMessageTypeText), string(enums.IMMessageTypeHTML), string(enums.IMMessageTypeVoice)}).
		Desc("id").Limit(40))
	if len(messages) == 0 {
		return &response.KnowledgeCandidateAnalyzeResponse{Skipped: true, Reason: "没有可分析的文本消息"}, nil
	}
	var answerMsg *models.Message
	for i := range messages {
		msg := &messages[i]
		if msg.SenderType == enums.IMSenderTypeAgent && strings.TrimSpace(msg.Content) != "" {
			answerMsg = msg
			break
		}
	}
	if answerMsg == nil {
		return &response.KnowledgeCandidateAnalyzeResponse{Skipped: true, Reason: "没有人工客服的语言回答"}, nil
	}
	var questionMsg *models.Message
	for i := range messages {
		msg := &messages[i]
		if msg.ID >= answerMsg.ID || msg.SenderType != enums.IMSenderTypeCustomer || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		questionMsg = msg
		break
	}
	if questionMsg == nil {
		return &response.KnowledgeCandidateAnalyzeResponse{Skipped: true, Reason: "没有找到对应客户问题"}, nil
	}
	joined := strings.ToLower(questionMsg.Content + "\n" + answerMsg.Content)
	if containsAny(joined, []string{"送水", "拖鞋", "维修", "打扫", "赔偿", "退款", "投诉", "报警", "身份证", "手机号"}) {
		return &response.KnowledgeCandidateAnalyzeResponse{Skipped: true, Reason: "该对话更像行动/人工决策，不自动进入知识库"}, nil
	}
	candidate, err := s.UpsertCandidate(route.StoreID, route.KnowledgeBaseID, conversationID, []int64{questionMsg.ID, answerMsg.ID}, enums.KnowledgeCandidateSourceStoreWecom, questionMsg.Content, answerMsg.Content, "人工会话分析生成", answerMsg.Content, 0.72, operator.Username)
	if err != nil {
		return nil, err
	}
	return &response.KnowledgeCandidateAnalyzeResponse{Created: true, Candidate: buildAnalyzeKnowledgeCandidateResponse(candidate)}, nil
}

func buildAnalyzeKnowledgeCandidateResponse(item *models.KnowledgeCandidate) response.KnowledgeCandidateResponse {
	if item == nil {
		return response.KnowledgeCandidateResponse{}
	}
	return response.KnowledgeCandidateResponse{
		ID:              item.ID,
		StoreID:         item.StoreID,
		KnowledgeBaseID: item.KnowledgeBaseID,
		ConversationID:  item.ConversationID,
		MessageIDs:      item.MessageIDs,
		Source:          string(item.Source),
		SourceName:      enums.GetKnowledgeCandidateSourceLabel(item.Source),
		Question:        item.Question,
		Answer:          item.Answer,
		Summary:         item.Summary,
		EvidenceText:    item.EvidenceText,
		Frequency:       item.Frequency,
		SimilarityKey:   item.SimilarityKey,
		Status:          string(item.Status),
		StatusName:      enums.GetKnowledgeCandidateStatusLabel(item.Status),
		Confidence:      item.Confidence,
		CreatedBy:       item.CreatedBy,
		ReviewUserID:    item.ReviewUserID,
		ReviewUserName:  item.ReviewUserName,
		ReviewedAt:      item.ReviewedAt,
		ExportedAt:      item.ExportedAt,
		ImportedAt:      item.ImportedAt,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func (s *knowledgeCandidateService) qualityCheckOne(item *models.KnowledgeCandidate) response.KnowledgeCandidateQualityReport {
	reasons := make([]string, 0, 4)
	decision := "approve"
	question := strings.TrimSpace(item.Question)
	answer := strings.TrimSpace(item.Answer)
	joined := strings.ToLower(question + "\n" + answer + "\n" + item.Summary + "\n" + item.EvidenceText)
	if question == "" || answer == "" {
		decision = "reject"
		reasons = append(reasons, "缺少问题或答案，不能形成问答对")
	}
	if item.StoreID <= 0 || item.KnowledgeBaseID <= 0 {
		decision = worseKnowledgeDecision(decision, "review")
		reasons = append(reasons, "缺少门店或知识库归属，需要先绑定")
	}
	if containsAny(joined, []string{"送水", "拖鞋", "打扫", "维修", "叫醒", "行李", "已经安排", "已安排", "登记好了", "工单"}) {
		decision = worseKnowledgeDecision(decision, "review")
		reasons = append(reasons, "更像行动/工单类问题，不应直接沉淀成知识库 FAQ")
	}
	if containsAny(joined, []string{"退款", "赔偿", "赔付", "投诉", "报警", "身份证", "手机号", "隐私", "房卡丢", "安全"}) {
		decision = worseKnowledgeDecision(decision, "review")
		reasons = append(reasons, "涉及人工决策、隐私或安全，必须人工复核")
	}
	if item.Confidence > 0 && item.Confidence < 0.55 {
		decision = worseKnowledgeDecision(decision, "review")
		reasons = append(reasons, "置信度偏低，需要人工确认")
	}
	if item.Frequency >= 3 {
		reasons = append(reasons, "出现频次较高，建议优先审核")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "符合语言问答归档条件")
	}
	return response.KnowledgeCandidateQualityReport{
		ID:              item.ID,
		Decision:        decision,
		DecisionName:    knowledgeDecisionName(decision),
		Reasons:         reasons,
		Question:        item.Question,
		Answer:          item.Answer,
		Frequency:       item.Frequency,
		StoreID:         item.StoreID,
		KnowledgeBaseID: item.KnowledgeBaseID,
	}
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func worseKnowledgeDecision(current, next string) string {
	rank := map[string]int{"approve": 0, "review": 1, "reject": 2}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func knowledgeDecisionName(decision string) string {
	switch decision {
	case "approve":
		return "建议通过"
	case "reject":
		return "建议驳回"
	default:
		return "建议复核"
	}
}

func (s *knowledgeCandidateService) MarkImported(id int64, operator *dto.AuthPrincipal) error {
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("待归档问答不存在")
	}
	if !s.canReviewCandidate(item, operator) {
		return errorsx.Forbidden("无权限维护该门店知识进化候选")
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

func (s *knowledgeCandidateService) batchReview(ids []int64, status enums.KnowledgeCandidateStatus, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	ids = uniquePositiveInt64s(ids)
	if len(ids) == 0 {
		return errorsx.InvalidParam("请选择待归档问答")
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, id := range ids {
			item := repositories.KnowledgeCandidateRepository.Get(ctx.Tx, id)
			if item == nil {
				return errorsx.InvalidParam("待归档问答不存在")
			}
			if !s.canReviewCandidate(item, operator) {
				return errorsx.Forbidden("无权限维护该门店知识进化候选")
			}
			if err := repositories.KnowledgeCandidateRepository.Updates(ctx.Tx, item.ID, map[string]any{
				"status":           status,
				"review_user_id":   operator.UserID,
				"review_user_name": operator.Username,
				"reviewed_at":      now,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *knowledgeCandidateService) canReviewCandidate(item *models.KnowledgeCandidate, operator *dto.AuthPrincipal) bool {
	if item == nil || operator == nil {
		return false
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return true
	}
	for _, id := range scope.KnowledgeBaseIDs {
		if id == item.KnowledgeBaseID {
			return true
		}
	}
	return false
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
	messages := MessageService.Find(sqls.NewCnd().Eq("conversation_id", conversationID).Asc("seq_no").Limit(60))
	extraction := buildKnowledgeCandidateExtraction(messages)
	if !extraction.Eligible {
		return nil, nil
	}
	return s.UpsertCandidate(state.StoreID, state.KnowledgeBaseID, conversationID, extraction.MessageIDs, source, extraction.Question, extraction.Answer, extraction.Summary, extraction.Evidence, extraction.Confidence, string(source))
}

type knowledgeCandidateExtraction struct {
	Eligible   bool
	Question   string
	Answer     string
	Summary    string
	Evidence   string
	MessageIDs []int64
	Confidence float64
}

func buildKnowledgeCandidateExtraction(messages []models.Message) knowledgeCandidateExtraction {
	ret := knowledgeCandidateExtraction{MessageIDs: make([]int64, 0, len(messages)), Confidence: 0.72}
	var lastCustomerQuestion models.Message
	var answerLines []string
	var evidence strings.Builder
	for _, item := range messages {
		content := strings.TrimSpace(stripHTMLForKnowledgeCandidate(item.Content))
		if content == "" || !knowledgeCandidateMessageTypeAllowed(item.MessageType) {
			continue
		}
		ret.MessageIDs = append(ret.MessageIDs, item.ID)
		evidence.WriteString(fmt.Sprintf("%s: %s\n", item.SenderType, content))
		switch item.SenderType {
		case enums.IMSenderTypeCustomer:
			if isKnowledgeCandidateQuestion(content) {
				lastCustomerQuestion = item
			}
		case enums.IMSenderTypeAgent:
			if lastCustomerQuestion.ID > 0 && isKnowledgeCandidateAnswer(content) {
				answerLines = append(answerLines, content)
			}
		}
	}
	question := strings.TrimSpace(stripHTMLForKnowledgeCandidate(lastCustomerQuestion.Content))
	answer := strings.TrimSpace(strings.Join(answerLines, "\n"))
	if question == "" || answer == "" {
		return ret
	}
	combined := strings.TrimSpace(question + "\n" + answer)
	if isActionOnlyKnowledgeCandidate(combined) || isHumanDecisionKnowledgeCandidate(combined) || isLowValueKnowledgeCandidate(question, answer) {
		return ret
	}
	ret.Eligible = true
	ret.Question = limitText(question, 300)
	ret.Answer = limitText(answer, 1200)
	ret.Summary = "人工语言回答解决了当前门店知识库未覆盖的问题，待审核后可沉淀为门店 FAQ。"
	ret.Evidence = strings.TrimSpace(evidence.String())
	return ret
}

func knowledgeCandidateMessageTypeAllowed(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeHTML:
		return true
	default:
		return false
	}
}

func stripHTMLForKnowledgeCandidate(value string) string {
	value = strings.ReplaceAll(value, "<br>", "\n")
	value = strings.ReplaceAll(value, "<br/>", "\n")
	value = strings.ReplaceAll(value, "<br />", "\n")
	re := regexp.MustCompile(`<[^>]+>`)
	return strings.TrimSpace(re.ReplaceAllString(value, ""))
}

func isKnowledgeCandidateQuestion(value string) bool {
	value = strings.TrimSpace(value)
	if len([]rune(value)) < 4 {
		return false
	}
	if containsKnowledgeCandidateAny(value, []string{"吗", "么", "什么", "怎么", "几点", "多久", "哪里", "在哪", "能不能", "可以", "有没有", "多少钱", "收费", "停车", "早餐", "发票", "押金", "退房", "入住", "路线", "地址"}) {
		return true
	}
	return strings.Contains(value, "？") || strings.Contains(value, "?")
}

func isKnowledgeCandidateAnswer(value string) bool {
	value = strings.TrimSpace(value)
	if len([]rune(value)) < 8 {
		return false
	}
	if containsKnowledgeCandidateAny(value, []string{"我帮您", "马上", "已经", "安排", "派单", "工单", "同事过去", "稍等", "收到", "好的", "嗯", "哦"}) && len([]rune(value)) < 40 {
		return false
	}
	return !containsKnowledgeCandidateAny(value, []string{"请转人工", "联系人工", "我不知道", "不清楚", "看不清", "无法查看"})
}

func isActionOnlyKnowledgeCandidate(value string) bool {
	return containsKnowledgeCandidateAny(value, []string{"送水", "拖鞋", "毛巾", "浴巾", "牙刷", "纸巾", "加被", "打扫", "保洁", "维修", "报修", "马桶", "空调", "漏水", "行李", "叫醒", "派单", "工单", "安排同事", "同事过去", "马上送", "给您送"})
}

func isHumanDecisionKnowledgeCandidate(value string) bool {
	return containsKnowledgeCandidateAny(value, []string{"退款", "赔偿", "免单", "投诉", "差评", "安全", "报警", "隐私", "身份证", "订单异常", "价格争议", "升级处理"})
}

func isLowValueKnowledgeCandidate(question string, answer string) bool {
	combined := strings.TrimSpace(question + answer)
	if len([]rune(combined)) < 20 {
		return true
	}
	return containsKnowledgeCandidateAny(combined, []string{"你好", "在吗", "谢谢", "不用了", "没事了", "好的", "嗯嗯"}) && len([]rune(combined)) < 50
}

func containsKnowledgeCandidateAny(value string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
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
