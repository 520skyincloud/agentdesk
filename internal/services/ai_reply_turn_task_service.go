package services

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

const aiReplyTurnTaskBatchLimit = 6

var AIReplyTurnTaskService = newAIReplyTurnTaskService()

type aiReplyTurnTaskService struct{}

type AIReplyTurnTaskInput struct {
	TenantID        int64
	TurnID          int64
	SourceMessageID int64
	SequenceNo      int
	OccurrenceIndex int
	TaskType        enums.AIReplyTurnTaskType
	Intent          string
	SubIntent       string
	RequestMode     string
	RelationType    string
	RelatedTaskID   int64
	ResourceAction  string
	QuestionText    string
	// 多模态契约 3.2/22.11：QuestionUnit 持久来源绑定与摘要；临时 SourceRef 不入库。
	AnalysisRevision      int
	SourceSpanStart       int
	SourceSpanEnd         int
	SourceBindingsJSON    string
	SourceSetFingerprint  string
	CanonicalQuestionHash string
	CapabilityCode        string
	CapabilityRoute       string
	CapabilityFingerprint string
	AnswerGroupKey        string
}

type AIReplyTurnTaskKnowledgeUpdate struct {
	TaskKey    string
	Status     enums.AIReplyTurnTaskKnowledgeStatus
	HitCount   int
	ResultCode string
	// RetrieveLogID/QueryFingerprint 绑定 Task↔RetrieveLog 审计链（契约 4.17）。
	RetrieveLogID   int64
	QueryFingerprint string
}

func newAIReplyTurnTaskService() *aiReplyTurnTaskService {
	return &aiReplyTurnTaskService{}
}

func (s *aiReplyTurnTaskService) Enabled() bool {
	return sqls.DB() != nil && sqls.DB().Migrator().HasTable(&models.AIReplyTurnTask{})
}

func (s *aiReplyTurnTaskService) StableTaskKey(input AIReplyTurnTaskInput) string {
	// 契约 3.2.3：稳定键不再包含 OccurrenceIndex。优先使用 QuestionUnit 的
	// canonical hash + 来源集合 fingerprint；旧输入缺这些字段时回退到语义
	// fingerprint（同样不带 occurrence）。
	canonicalHash := strings.TrimSpace(input.CanonicalQuestionHash)
	sourceSetFingerprint := strings.TrimSpace(input.SourceSetFingerprint)
	if input.TenantID > 0 && input.TurnID > 0 && input.SourceMessageID > 0 && input.AnalysisRevision > 0 &&
		canonicalHash != "" && sourceSetFingerprint != "" {
		taskType := strings.TrimSpace(string(input.TaskType))
		if taskType == "" {
			taskType = string(enums.AIReplyTurnTaskTypeText)
		}
		raw := fmt.Sprintf(
			"%d/%d/%d/%d/%s/%s/%s",
			input.TenantID, input.TurnID, input.SourceMessageID, input.AnalysisRevision,
			sourceSetFingerprint, taskType, canonicalHash,
		)
		sum := sha256.Sum256([]byte(raw))
		return "turn_task_" + hex.EncodeToString(sum[:16])
	}
	fingerprint := s.SemanticQuestionFingerprint(input)
	if fingerprint == "" {
		fingerprint = canonicalHash
	}
	if input.TenantID > 0 && input.TurnID > 0 && fingerprint != "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s/1", input.TenantID, input.TurnID, fingerprint)))
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
		return "turn_task_" + strings.ToLower(encoded[:26])
	}
	taskType := strings.TrimSpace(string(input.TaskType))
	if taskType == "" {
		taskType = string(enums.AIReplyTurnTaskTypeText)
	}
	sequenceNo := input.SequenceNo
	if sequenceNo <= 0 {
		sequenceNo = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", input.SourceMessageID, sequenceNo, taskType)))
	return "turn_task_" + hex.EncodeToString(sum[:12])
}

func (s *aiReplyTurnTaskService) SemanticQuestionFingerprint(input AIReplyTurnTaskInput) string {
	questionFingerprint := s.QuestionFingerprint(input.QuestionText)
	parts := []string{
		normalizeTaskFingerprintPart(input.Intent),
		normalizeTaskFingerprintPart(input.SubIntent),
		questionFingerprint,
		normalizeTaskFingerprintPart(input.RequestMode),
	}
	if strings.Join(parts, "") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func (s *aiReplyTurnTaskService) QuestionFingerprint(text string) string {
	text = norm.NFKC.String(strings.ToLower(strings.TrimSpace(text)))
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRightFunc(text, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (s *aiReplyTurnTaskService) EnsureTasksDB(db *gorm.DB, turn *models.AIReplyTurn, inputs []AIReplyTurnTaskInput) ([]models.AIReplyTurnTask, error) {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 {
		return nil, errorsx.InvalidParam("AI 回复逐题任务缺少轮次范围")
	}
	if len(inputs) == 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil, nil
	}
	now := time.Now()
	ret := make([]models.AIReplyTurnTask, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	existingTasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, turn.TenantID, turn.ID)
	existingBySourceSequence := make(map[string]*models.AIReplyTurnTask, len(existingTasks))
	for index := range existingTasks {
		existing := &existingTasks[index]
		identity := aiReplyTurnTaskSourceIdentity(existing.SourceMessageID, existing.SequenceNo, existing.TaskType)
		existingBySourceSequence[identity] = existing
	}
	for index, input := range inputs {
		if input.SourceMessageID <= 0 {
			return nil, errorsx.InvalidParam("AI 回复逐题任务缺少来源消息")
		}
		source := repositories.MessageRepository.GetInTenant(db, input.SourceMessageID, turn.TenantID)
		if source == nil || source.ConversationID != turn.ConversationID || source.SessionNo != turn.SessionNo ||
			source.AIReplyTurnID != turn.ID || source.SenderType != enums.IMSenderTypeCustomer {
			return nil, errorsx.InvalidParam("AI 回复逐题任务来源消息范围不一致")
		}
		if input.SequenceNo <= 0 {
			input.SequenceNo = index + 1
		}
		if input.TaskType == "" {
			input.TaskType = enums.AIReplyTurnTaskTypeText
		}
		input.TenantID = turn.TenantID
		input.TurnID = turn.ID
		fingerprint := s.SemanticQuestionFingerprint(input)
		// 契约 3.2.2：canonical hash 非空时写入 CanonicalQuestionHash，
		// QuestionFingerprint 作为兼容副本。
		canonicalHash := strings.TrimSpace(input.CanonicalQuestionHash)
		if canonicalHash == "" && fingerprint != "" {
			canonicalHash = fingerprint
		}
		identity := aiReplyTurnTaskSourceIdentity(input.SourceMessageID, input.SequenceNo, input.TaskType)
		existingIdentity := existingBySourceSequence[identity]
		if existingIdentity != nil {
			fingerprint = existingIdentity.QuestionFingerprint
			if canonicalHash == "" {
				canonicalHash = existingIdentity.CanonicalQuestionHash
			}
		}
		taskKey := ""
		if existingIdentity != nil {
			taskKey = existingIdentity.TaskKey
		} else {
			taskKey = s.StableTaskKey(input)
			// 稳定键不含 occurrence：同轮次语义/来源相同的第二个 QuestionUnit
			// 会得到相同 key。为保证物理行唯一，用 sequence 做 salt 生成
			// 去重副本行，随后由 canonical 去重收敛为 covered。
			for {
				owner := s.taskKeyOwner(db, turn, taskKey)
				if owner == nil || (owner.SourceMessageID == input.SourceMessageID && owner.SequenceNo == input.SequenceNo) {
					break
				}
				sum := sha256.Sum256([]byte(taskKey + "\x1f" + fmt.Sprintf("%d", input.SequenceNo)))
				taskKey = "turn_task_" + hex.EncodeToString(sum[:16])
			}
		}
		if _, exists := seen[taskKey]; exists {
			continue
		}
		seen[taskKey] = struct{}{}
		if fingerprint == "" {
			fingerprint = s.QuestionFingerprint(input.QuestionText)
		}
		knowledgeStatus := enums.AIReplyTurnTaskKnowledgeStatusNone
		stage := enums.AIReplyTurnTaskStageGenerate
		status := enums.AIReplyTurnTaskStatusPending
		if input.TaskType == enums.AIReplyTurnTaskTypeKnowledge {
			knowledgeStatus = enums.AIReplyTurnTaskKnowledgeStatusPending
			stage = enums.AIReplyTurnTaskStageKnowledge
		} else if input.TaskType == enums.AIReplyTurnTaskTypeResource {
			stage = enums.AIReplyTurnTaskStageCommit
		} else if input.TaskType == enums.AIReplyTurnTaskTypeHuman {
			stage = enums.AIReplyTurnTaskStageHandoff
		}
		item := &models.AIReplyTurnTask{
			TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
			TurnID: turn.ID, IntroducedVersion: turn.Version, SourceMessageID: input.SourceMessageID,
			TaskKey: taskKey, SequenceNo: input.SequenceNo, TaskType: input.TaskType,
			Intent: limitText(strings.TrimSpace(input.Intent), 80), SubIntent: limitText(strings.TrimSpace(input.SubIntent), 120),
			ResourceAction: limitText(strings.TrimSpace(input.ResourceAction), 80), QuestionFingerprint: fingerprint,
			RelationType: limitText(strings.TrimSpace(input.RelationType), 24), RelatedTaskID: input.RelatedTaskID,
			AnalysisRevision:      input.AnalysisRevision,
			SourceSpanStart:       input.SourceSpanStart,
			SourceSpanEnd:         input.SourceSpanEnd,
			SourceBindingsJSON:    strings.TrimSpace(input.SourceBindingsJSON),
			SourceSetFingerprint:  limitText(strings.TrimSpace(input.SourceSetFingerprint), 64),
			CanonicalQuestionHash: limitText(canonicalHash, 64),
			RequestMode:           limitText(strings.TrimSpace(input.RequestMode), 24),
			CapabilityCode:        limitText(strings.TrimSpace(input.CapabilityCode), 120),
			CapabilityRoute:       limitText(strings.TrimSpace(input.CapabilityRoute), 40),
			CapabilityFingerprint: limitText(strings.TrimSpace(input.CapabilityFingerprint), 64),
			AnswerGroupKey:        limitText(strings.TrimSpace(input.AnswerGroupKey), 128),
			Stage:                 stage, Status: status, KnowledgeStatus: knowledgeStatus,
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserName: "ai_reply_task",
				UpdatedAt: now, UpdateUserName: "ai_reply_task",
			},
		}
		created, err := repositories.AIReplyTurnTaskRepository.CreateIfAbsent(db, item)
		if err != nil {
			return nil, err
		}
		if !created {
			existing, getErr := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, turn.TenantID, turn.ID, taskKey)
			if getErr != nil {
				return nil, getErr
			}
			if existing == nil {
				return nil, fmt.Errorf("AI reply task conflict without existing row")
			}
			item = existing
			if !aiReplyTurnTaskTerminal(existing.Status) {
				updates := map[string]any{
					"introduced_version":      turn.Version,
					"intent":                  limitText(strings.TrimSpace(input.Intent), 80),
					"sub_intent":              limitText(strings.TrimSpace(input.SubIntent), 120),
					"resource_action":         limitText(strings.TrimSpace(input.ResourceAction), 80),
					"question_fingerprint":    fingerprint,
					"canonical_question_hash": limitText(canonicalHash, 64),
					"relation_type":           limitText(strings.TrimSpace(input.RelationType), 24),
					"related_task_id":         input.RelatedTaskID,
					"updated_at":              now,
					"update_user_name":        "ai_reply_task",
				}
				if input.AnalysisRevision > 0 {
					updates["analysis_revision"] = input.AnalysisRevision
					updates["source_span_start"] = input.SourceSpanStart
					updates["source_span_end"] = input.SourceSpanEnd
					updates["source_set_fingerprint"] = limitText(strings.TrimSpace(input.SourceSetFingerprint), 64)
					if strings.TrimSpace(input.SourceBindingsJSON) != "" {
						updates["source_bindings_json"] = strings.TrimSpace(input.SourceBindingsJSON)
					}
				}
				if strings.TrimSpace(input.CapabilityRoute) != "" {
					updates["capability_code"] = limitText(strings.TrimSpace(input.CapabilityCode), 120)
					updates["capability_route"] = limitText(strings.TrimSpace(input.CapabilityRoute), 40)
					updates["capability_fingerprint"] = limitText(strings.TrimSpace(input.CapabilityFingerprint), 64)
				}
				if strings.TrimSpace(input.AnswerGroupKey) != "" {
					updates["answer_group_key"] = limitText(strings.TrimSpace(input.AnswerGroupKey), 128)
				}
				if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, existing.ID, existing.TenantID, updates); err != nil {
					return nil, err
				}
				item.IntroducedVersion = turn.Version
				item.Intent = limitText(strings.TrimSpace(input.Intent), 80)
				item.SubIntent = limitText(strings.TrimSpace(input.SubIntent), 120)
				item.ResourceAction = limitText(strings.TrimSpace(input.ResourceAction), 80)
				item.QuestionFingerprint = fingerprint
				item.RelationType = limitText(strings.TrimSpace(input.RelationType), 24)
				item.RelatedTaskID = input.RelatedTaskID
			}
		}
		if created && fingerprint != "" && status != enums.AIReplyTurnTaskStatusHandoffPending {
			canonical := s.findCanonicalDuplicate(db, turn, item)
			if canonical != nil {
				covered, err := s.markCoveredByExisting(db, canonical, item, now)
				if err != nil {
					return nil, err
				}
				item = covered
			}
		}
		ret = append(ret, *item)
	}
	sortAIReplyTurnTasks(ret)
	return ret, nil
}

// markCoveredByExisting 把 current 任务标记为被 canonical 任务覆盖。
func (s *aiReplyTurnTaskService) markCoveredByExisting(db *gorm.DB, canonical, current *models.AIReplyTurnTask, now time.Time) (*models.AIReplyTurnTask, error) {
	if canonical == nil || current == nil || canonical.ID <= 0 || canonical.ID == current.ID {
		return current, nil
	}
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, current.ID, current.TenantID, map[string]any{
		"stage":              enums.AIReplyTurnTaskStageComplete,
		"status":             enums.AIReplyTurnTaskStatusCovered,
		"covered_by_task_id": canonical.ID,
		"result_code":        "covered_by_existing_task",
		"completed_at":       now,
		"updated_at":         now,
		"update_user_name":   "ai_reply_task",
	}); err != nil {
		return nil, err
	}
	current.Stage = enums.AIReplyTurnTaskStageComplete
	current.Status = enums.AIReplyTurnTaskStatusCovered
	current.CoveredByTaskID = canonical.ID
	current.ResultCode = "covered_by_existing_task"
	current.CompletedAt = &now
	return current, nil
}

func aiReplyTurnTaskSourceIdentity(sourceMessageID int64, sequenceNo int, taskType enums.AIReplyTurnTaskType) string {
	return fmt.Sprintf("%d:%d:%s", sourceMessageID, sequenceNo, strings.TrimSpace(string(taskType)))
}

func normalizeTaskFingerprintPart(value string) string {
	value = norm.NFKC.String(strings.ToLower(strings.TrimSpace(value)))
	return strings.Join(strings.Fields(value), " ")
}

func (s *aiReplyTurnTaskService) findCanonicalDuplicate(db *gorm.DB, turn *models.AIReplyTurn, current *models.AIReplyTurnTask) *models.AIReplyTurnTask {
	// 契约 22.11：不再跳过同 SourceMessageID；同源精确重复（canonical hash 相同）
	// 也必须收敛到既有 canonical Task。
	if current == nil || current.QuestionFingerprint == "" {
		return nil
	}
	items := repositories.AIReplyTurnTaskRepository.FindByFingerprintInTurn(
		db, turn.TenantID, turn.ID, current.QuestionFingerprint, current.TaskType,
	)
	canonicalHash := strings.TrimSpace(current.CanonicalQuestionHash)
	for index := range items {
		candidate := &items[index]
		if candidate.ID == current.ID ||
			candidate.Status == enums.AIReplyTurnTaskStatusSuperseded || candidate.Status == enums.AIReplyTurnTaskStatusSkipped ||
			candidate.Status == enums.AIReplyTurnTaskStatusFailed {
			continue
		}
		// 同 canonical hash 时同源重复也覆盖；hash 缺失时保守只合并跨源重复。
		if candidate.SourceMessageID == current.SourceMessageID &&
			(canonicalHash == "" || strings.TrimSpace(candidate.CanonicalQuestionHash) != canonicalHash) {
			continue
		}
		return candidate
	}
	return nil
}

func (s *aiReplyTurnTaskService) ClaimBatchDB(db *gorm.DB, turn *models.AIReplyTurn, jobID int64) ([]models.AIReplyTurnTask, bool, error) {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 || jobID <= 0 {
		return nil, false, errorsx.InvalidParam("AI 回复逐题任务缺少领取范围")
	}
	allTasks := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, turn.TenantID, turn.ID)
	claimed := make([]models.AIReplyTurnTask, 0, aiReplyTurnTaskBatchLimit)
	hasMore := false
	now := time.Now()
	for index := range allTasks {
		task := allTasks[index]
		if task.Status == enums.AIReplyTurnTaskStatusHandoffPending || aiReplyTurnTaskTerminal(task.Status) {
			continue
		}
		if task.Status == enums.AIReplyTurnTaskStatusRunning && task.ClaimedByJobID == jobID {
			claimed = append(claimed, task)
			continue
		}
		if task.Status != enums.AIReplyTurnTaskStatusPending && task.Status != enums.AIReplyTurnTaskStatusReady {
			continue
		}
		if task.NextRetryAt != nil && task.NextRetryAt.After(now) {
			continue
		}
		if len(claimed) >= aiReplyTurnTaskBatchLimit {
			hasMore = true
			continue
		}
		locked, err := repositories.AIReplyTurnTaskRepository.GetForUpdateInTenant(db, task.ID, turn.TenantID)
		if err != nil {
			return nil, false, err
		}
		if locked == nil || (locked.Status != enums.AIReplyTurnTaskStatusPending && locked.Status != enums.AIReplyTurnTaskStatusReady) {
			continue
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, locked.ID, locked.TenantID, map[string]any{
			"status":            enums.AIReplyTurnTaskStatusRunning,
			"claimed_by_job_id": jobID,
			"claimed_version":   turn.Version,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_task_claim",
		}); err != nil {
			return nil, false, err
		}
		locked.Status = enums.AIReplyTurnTaskStatusRunning
		locked.ClaimedByJobID = jobID
		locked.ClaimedVersion = turn.Version
		claimed = append(claimed, *locked)
	}
	sortAIReplyTurnTasks(claimed)
	return claimed, hasMore, nil
}

func (s *aiReplyTurnTaskService) MarkKnowledgeResultsDB(db *gorm.DB, tenantID, turnID, jobID int64, updates []AIReplyTurnTaskKnowledgeUpdate) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || jobID <= 0 {
		return errorsx.InvalidParam("AI 回复知识任务缺少范围")
	}
	now := time.Now()
	for _, update := range updates {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, strings.TrimSpace(update.TaskKey))
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) || task.ClaimedByJobID != jobID {
			continue
		}
		status := enums.AIReplyTurnTaskStatusRunning
		stage := enums.AIReplyTurnTaskStageGenerate
		claimedByJobID := task.ClaimedByJobID
		claimedVersion := task.ClaimedVersion
		var nextRetryAt *time.Time
		if update.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			status = enums.AIReplyTurnTaskStatusPending
			stage = enums.AIReplyTurnTaskStageKnowledge
			claimedByJobID = 0
			claimedVersion = 0
			nextAttempt := task.AttemptCount + 1
			if nextAttempt >= aiReplyJobMaxAttempts {
				status = enums.AIReplyTurnTaskStatusHandoffPending
				stage = enums.AIReplyTurnTaskStageHandoff
			} else {
				nextRetryAt = aiReplyTaskRetryAt(now, nextAttempt)
			}
		}
		updatesMap := map[string]any{
			"knowledge_status":    update.Status,
			"knowledge_hit_count": max(update.HitCount, 0),
			"stage":               stage,
			"status":              status,
			"claimed_by_job_id":   claimedByJobID,
			"claimed_version":     claimedVersion,
			"attempt_count":       gorm.Expr("attempt_count + 1"),
			"result_code":         controlledResultCode(update.ResultCode, string(update.Status)),
			"next_retry_at":       nextRetryAt,
			"updated_at":          now,
			"update_user_name":    "ai_reply_knowledge",
		}
		// 契约 4.17：只在真实取得日志时写入，不覆盖重试前已绑定的 checkpoint。
		if update.RetrieveLogID > 0 {
			updatesMap["knowledge_retrieve_log_id"] = update.RetrieveLogID
		}
		if strings.TrimSpace(update.QueryFingerprint) != "" {
			updatesMap["knowledge_query_fingerprint"] = limitText(strings.TrimSpace(update.QueryFingerprint), 64)
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, updatesMap); err != nil {
			return err
		}
	}
	return nil
}

func aiReplyTaskRetryAt(now time.Time, attempt int) *time.Time {
	if attempt <= 0 {
		attempt = 1
	}
	delayIndex := attempt - 1
	if delayIndex >= len(aiReplyJobRetryDelays) {
		delayIndex = len(aiReplyJobRetryDelays) - 1
	}
	next := now.Add(aiReplyJobRetryDelays[delayIndex])
	return &next
}

func (s *aiReplyTurnTaskService) MarkCommittedMessagesDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	jobID int64,
	taskMessageIDs map[string]int64,
	delivered bool,
	now time.Time,
) error {
	if db == nil || turn == nil || jobID <= 0 || len(taskMessageIDs) == 0 {
		return nil
	}
	status := enums.AIReplyTurnTaskStatusCommitted
	stage := enums.AIReplyTurnTaskStageDelivery
	if delivered {
		status = enums.AIReplyTurnTaskStatusDelivered
		stage = enums.AIReplyTurnTaskStageComplete
	}
	for taskKey, messageID := range taskMessageIDs {
		if strings.TrimSpace(taskKey) == "" || messageID <= 0 {
			continue
		}
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, turn.TenantID, turn.ID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) || task.ClaimedByJobID != jobID {
			return ErrAIReplyTurnStale
		}
		updates := map[string]any{
			"stage":                stage,
			"status":               status,
			"result_code":          "committed",
			"committed_message_id": messageID,
			"updated_at":           now,
			"update_user_name":     "ai_reply_commit",
		}
		if delivered {
			updates["completed_at"] = now
			updates["result_code"] = "delivered"
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, updates); err != nil {
			return err
		}
	}
	return nil
}

func (s *aiReplyTurnTaskService) MarkSuppressedActionsDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	jobID int64,
	items []AIReplyTurnActionSuppression,
	now time.Time,
) error {
	if db == nil || turn == nil || jobID <= 0 || len(items) == 0 {
		return nil
	}
	for _, item := range items {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, turn.TenantID, turn.ID, strings.TrimSpace(item.TaskKey))
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) || task.ClaimedByJobID != jobID || item.CoveredByMessageID <= 0 {
			return ErrAIReplyTurnStale
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":                enums.AIReplyTurnTaskStageComplete,
			"status":               enums.AIReplyTurnTaskStatusCovered,
			"result_code":          controlledResultCode(item.ResultCode, "resource_action_suppressed"),
			"committed_message_id": item.CoveredByMessageID,
			"claimed_by_job_id":    0,
			"claimed_version":      0,
			"completed_at":         now,
			"next_retry_at":        nil,
			"updated_at":           now,
			"update_user_name":     "ai_reply_action",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *aiReplyTurnTaskService) MarkDeliveredByMessageDB(db *gorm.DB, message *models.Message, deliveredAt time.Time) error {
	if db == nil || message == nil || message.ID <= 0 || message.AIReplyTurnID <= 0 {
		return nil
	}
	if !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND committed_message_id = ? AND status = ?",
			message.TenantID, message.AIReplyTurnID, message.ID, enums.AIReplyTurnTaskStatusCommitted).
		Updates(map[string]any{
			"stage":            enums.AIReplyTurnTaskStageComplete,
			"status":           enums.AIReplyTurnTaskStatusDelivered,
			"result_code":      "delivered",
			"completed_at":     deliveredAt,
			"updated_at":       deliveredAt,
			"update_user_name": "ai_reply_delivery",
		}).Error
}

func (s *aiReplyTurnTaskService) HasUnfinished(tenantID, turnID int64) bool {
	if !s.Enabled() {
		return false
	}
	return s.HasUnfinishedDB(sqls.DB(), tenantID, turnID)
}

func (s *aiReplyTurnTaskService) HasUnfinishedDB(db *gorm.DB, tenantID, turnID int64) bool {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return false
	}
	return repositories.AIReplyTurnTaskRepository.CountUnfinishedByTurnInTenant(db, tenantID, turnID) > 0
}

func (s *aiReplyTurnTaskService) HasRunnable(tenantID, turnID int64) bool {
	if !s.Enabled() {
		return false
	}
	return repositories.AIReplyTurnTaskRepository.CountRunnableByTurnInTenant(sqls.DB(), tenantID, turnID) > 0
}

func (s *aiReplyTurnTaskService) NextRetryAt(tenantID, turnID int64) *time.Time {
	if !s.Enabled() {
		return nil
	}
	return repositories.AIReplyTurnTaskRepository.NextRetryAtByTurnInTenant(sqls.DB(), tenantID, turnID, time.Now())
}

func (s *aiReplyTurnTaskService) HasFailureHandoffs(tenantID, turnID int64) bool {
	if !s.Enabled() {
		return false
	}
	return repositories.AIReplyTurnTaskRepository.CountFailureHandoffsByTurnInTenant(sqls.DB(), tenantID, turnID) > 0
}

func (s *aiReplyTurnTaskService) MarkPendingHandoffsDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status = ?", tenantID, turnID, enums.AIReplyTurnTaskStatusHandoffPending).
		Updates(map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusHandoff,
			"result_code":       controlledResultCode(resultCode, "human_handoff"),
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      now,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff",
		}).Error
}

func (s *aiReplyTurnTaskService) FinalizeAIHandoffDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	if err := s.MarkPendingHandoffsDB(db, tenantID, turnID, resultCode, now); err != nil {
		return err
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status IN ?", tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
			enums.AIReplyTurnTaskStatusRunning,
		}).
		Updates(map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusSuperseded,
			"result_code":       "ai_failure_handoff_superseded",
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      now,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff",
		}).Error
}

func (s *aiReplyTurnTaskService) FinalizeRequestedHandoffDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	if err := db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND task_type = ? AND status IN ?", tenantID, turnID,
			enums.AIReplyTurnTaskTypeHuman,
			[]enums.AIReplyTurnTaskStatus{
				enums.AIReplyTurnTaskStatusPending,
				enums.AIReplyTurnTaskStatusReady,
				enums.AIReplyTurnTaskStatusRunning,
				enums.AIReplyTurnTaskStatusHandoffPending,
			}).
		Updates(map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusHandoff,
			"result_code":       controlledResultCode(resultCode, "human_handoff"),
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      now,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff",
		}).Error; err != nil {
		return err
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status IN ?", tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
			enums.AIReplyTurnTaskStatusRunning,
			enums.AIReplyTurnTaskStatusHandoffPending,
		}).
		Updates(map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusSuperseded,
			"result_code":       "human_handoff_superseded",
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      now,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff",
		}).Error
}

func (s *aiReplyTurnTaskService) MarkHandoffPendingDB(db *gorm.DB, tenantID, turnID, jobID int64, taskKeys []string, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || len(taskKeys) == 0 {
		return nil
	}
	for _, taskKey := range uniqueTaskKeys(taskKeys) {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) || (jobID > 0 && task.ClaimedByJobID != 0 && task.ClaimedByJobID != jobID) {
			continue
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":            enums.AIReplyTurnTaskStageHandoff,
			"status":           enums.AIReplyTurnTaskStatusHandoffPending,
			"result_code":      controlledResultCode(resultCode, "human_handoff_pending"),
			"next_retry_at":    nil,
			"updated_at":       now,
			"update_user_name": "ai_reply_handoff",
		}); err != nil {
			return err
		}
	}
	return nil
}

// MarkUnfinishedHandoffPendingDB is the terminal failure boundary for a turn.
// A runtime error may not contain task keys (for example a prepare or protocol
// failure before the reply plan is returned), so the turn ledger itself is the
// source of truth for the tasks that still need human handling.
func (s *aiReplyTurnTaskService) MarkUnfinishedHandoffPendingDB(
	db *gorm.DB,
	tenantID, turnID, jobID int64,
	resultCode string,
	now time.Time,
) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	query := db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status IN ?", tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
			enums.AIReplyTurnTaskStatusRunning,
		})
	if jobID > 0 {
		query = query.Where("claimed_by_job_id = 0 OR claimed_by_job_id = ?", jobID)
	}
	return query.Updates(map[string]any{
		"stage":             enums.AIReplyTurnTaskStageHandoff,
		"status":            enums.AIReplyTurnTaskStatusHandoffPending,
		"result_code":       controlledResultCode(resultCode, "human_handoff_pending"),
		"claimed_by_job_id": 0,
		"claimed_version":   0,
		"next_retry_at":     nil,
		"updated_at":        now,
		"update_user_name":  "ai_reply_handoff",
	}).Error
}

func (s *aiReplyTurnTaskService) MarkTurnHandoffDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return nil
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesByTurnInTenant(db, tenantID, turnID, map[string]any{
		"stage":            enums.AIReplyTurnTaskStageComplete,
		"status":           enums.AIReplyTurnTaskStatusHandoff,
		"result_code":      controlledResultCode(resultCode, "human_handoff"),
		"completed_at":     now,
		"next_retry_at":    nil,
		"updated_at":       now,
		"update_user_name": "ai_reply_handoff",
	})
}

// CancelHandoffTransactionDB 契约 16.3：客户取消转人工时事务闭合整个 Handoff。
// 覆盖：payload.taskKeys 中的 handoff_pending/handoff 任务（新 V2 负载）、
// 同来源消息的派生 pending/running 任务（superseded），不再只按单个
// OriginMessageID + handoff_pending 做局部清理。
func (s *aiReplyTurnTaskService) CancelHandoffTransactionDB(
	db *gorm.DB,
	tenantID, conversationID, originMessageID int64,
	taskKeys []string,
	now time.Time,
) error {
	if db == nil || tenantID <= 0 || conversationID <= 0 || originMessageID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	msg := repositories.MessageRepository.GetInTenant(db, originMessageID, tenantID)
	if msg == nil || msg.AIReplyTurnID <= 0 {
		return nil
	}
	turnID := msg.AIReplyTurnID
	// 0) V2 负载绑定的任务集合：无论 handoff_pending 还是 handoff，都按取消
	//    语义标记 skipped（不进入 CancelHandoffDecisionDB 的“回退可执行”路径）。
	for _, taskKey := range uniqueTaskKeys(taskKeys) {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) {
			continue
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusSkipped,
			"result_code":       "human_handoff_cancelled",
			"claimed_by_job_id": 0, "claimed_version": 0,
			"completed_at": now, "next_retry_at": nil,
			"updated_at": now, "update_user_name": "ai_reply_handoff_cancel",
		}); err != nil {
			return err
		}
	}
	// 1) 同轮剩余 handoff_pending（含 V2 负载未列出、由同一确认产生的）。
	if err := db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status = ?", tenantID, turnID, enums.AIReplyTurnTaskStatusHandoffPending).
		Updates(map[string]any{
			"stage": enums.AIReplyTurnTaskStageComplete, "status": enums.AIReplyTurnTaskStatusSkipped,
			"result_code": "human_handoff_cancelled", "claimed_by_job_id": 0, "claimed_version": 0,
			"completed_at": now, "next_retry_at": nil, "updated_at": now, "update_user_name": "ai_reply_handoff_cancel",
		}).Error; err != nil {
		return err
	}
	// 2) 已进入 handoff 的原始人工任务：取消语义是“不进入人工池”，任务不再
	//    代表待办；标记 skipped 保留审计。
	if err := db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status = ?", tenantID, turnID, enums.AIReplyTurnTaskStatusHandoff).
		Updates(map[string]any{
			"status": enums.AIReplyTurnTaskStatusSkipped, "result_code": "human_handoff_cancelled",
			"updated_at": now, "update_user_name": "ai_reply_handoff_cancel",
		}).Error; err != nil {
		return err
	}
	// 3) 同来源消息派生的 pending/running 任务：superseded，Worker 不再运行。
	if err := db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND source_message_id = ? AND status IN ?",
			tenantID, turnID, originMessageID,
			[]enums.AIReplyTurnTaskStatus{enums.AIReplyTurnTaskStatusPending, enums.AIReplyTurnTaskStatusReady, enums.AIReplyTurnTaskStatusRunning},
		).
		Updates(map[string]any{
			"stage": enums.AIReplyTurnTaskStageComplete, "status": enums.AIReplyTurnTaskStatusSuperseded,
			"result_code": "superseded_by_handoff_cancel", "claimed_by_job_id": 0, "claimed_version": 0,
			"completed_at": now, "next_retry_at": nil, "updated_at": now, "update_user_name": "ai_reply_handoff_cancel",
		}).Error; err != nil {
		return err
	}
	return nil
}

// CancelHandoffPendingBySourceMessageDB 在客户取消转人工后，把该来源消息触发的
// handoff_pending 任务标记为 skipped，闭合确认链路：否则任务仍挂在 handoff_pending，
// 后续 job 会为同一个诉求反复触发转人工确认，造成死循环。
func (s *aiReplyTurnTaskService) CancelHandoffPendingBySourceMessageDB(db *gorm.DB, tenantID, sourceMessageID int64, now time.Time) error {
	if db == nil || tenantID <= 0 || sourceMessageID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	msg := repositories.MessageRepository.GetInTenant(db, sourceMessageID, tenantID)
	if msg == nil || msg.AIReplyTurnID <= 0 {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND source_message_id = ? AND status = ?",
			tenantID, msg.AIReplyTurnID, sourceMessageID, enums.AIReplyTurnTaskStatusHandoffPending).
		Updates(map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusSkipped,
			"result_code":       "human_handoff_cancelled",
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      now,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff_cancel",
		}).Error
}

func (s *aiReplyTurnTaskService) MarkTaskKeysHandoffDB(db *gorm.DB, tenantID, turnID int64, taskKeys []string, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || len(taskKeys) == 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	for _, taskKey := range uniqueTaskKeys(taskKeys) {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || aiReplyTurnTaskTerminal(task.Status) {
			continue
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":            enums.AIReplyTurnTaskStageComplete,
			"status":           enums.AIReplyTurnTaskStatusHandoff,
			"result_code":      controlledResultCode(resultCode, "human_handoff"),
			"completed_at":     now,
			"next_retry_at":    nil,
			"updated_at":       now,
			"update_user_name": "ai_reply_handoff",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *aiReplyTurnTaskService) ReleaseJobClaimsDB(db *gorm.DB, tenantID, turnID, jobID int64, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || jobID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND claimed_by_job_id = ? AND status = ?", tenantID, turnID, jobID, enums.AIReplyTurnTaskStatusRunning).
		Updates(map[string]any{
			"stage":             gorm.Expr("CASE WHEN knowledge_status = ? THEN ? ELSE stage END", enums.AIReplyTurnTaskKnowledgeStatusPending, enums.AIReplyTurnTaskStageKnowledge),
			"status":            enums.AIReplyTurnTaskStatusPending,
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"updated_at":        now,
			"update_user_name":  "ai_reply_task_release",
		}).Error
}

func (s *aiReplyTurnTaskService) SupersedeTurnDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesByTurnInTenant(db, tenantID, turnID, map[string]any{
		"stage":             enums.AIReplyTurnTaskStageComplete,
		"status":            enums.AIReplyTurnTaskStatusSuperseded,
		"result_code":       controlledResultCode(resultCode, "turn_superseded"),
		"claimed_by_job_id": 0,
		"claimed_version":   0,
		"completed_at":      now,
		"next_retry_at":     nil,
		"updated_at":        now,
		"update_user_name":  "ai_reply_turn",
	})
}

func aiReplyTurnTaskTerminal(status enums.AIReplyTurnTaskStatus) bool {
	switch status {
	case enums.AIReplyTurnTaskStatusDelivered, enums.AIReplyTurnTaskStatusCovered,
		enums.AIReplyTurnTaskStatusHandoff, enums.AIReplyTurnTaskStatusSkipped,
		enums.AIReplyTurnTaskStatusSuperseded, enums.AIReplyTurnTaskStatusFailed:
		return true
	default:
		return false
	}
}

func aiReplyTurnTaskActionable(status enums.AIReplyTurnTaskStatus) bool {
	switch status {
	case enums.AIReplyTurnTaskStatusPending, enums.AIReplyTurnTaskStatusReady, enums.AIReplyTurnTaskStatusRunning:
		return true
	default:
		return false
	}
}

func uniqueTaskKeys(taskKeys []string) []string {
	ret := make([]string, 0, len(taskKeys))
	seen := make(map[string]struct{}, len(taskKeys))
	for _, taskKey := range taskKeys {
		taskKey = strings.TrimSpace(taskKey)
		if taskKey == "" {
			continue
		}
		if _, exists := seen[taskKey]; exists {
			continue
		}
		seen[taskKey] = struct{}{}
		ret = append(ret, taskKey)
	}
	return ret
}

func sortAIReplyTurnTasks(tasks []models.AIReplyTurnTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].SequenceNo == tasks[j].SequenceNo {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].SequenceNo < tasks[j].SequenceNo
	})
}

// MarkTechnicalFailureDB 记录 Task 技术失败。契约 22.11：技术失败绝不改写为
// handoff_pending，只做退避重试或终态 failed，人工兜底由业务侧单独决定。
func (s *aiReplyTurnTaskService) MarkTechnicalFailureDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	taskKey string,
	failureClass string,
	maxAttempts int,
	now time.Time,
) error {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 {
		return errorsx.InvalidParam("AI 回复任务技术失败缺少范围")
	}
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return errorsx.InvalidParam("AI 回复任务技术失败缺少任务键")
	}
	task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, turn.TenantID, turn.ID, taskKey)
	if err != nil {
		return err
	}
	if task == nil || aiReplyTurnTaskTerminal(task.Status) {
		return nil
	}
	if maxAttempts <= 0 {
		maxAttempts = aiReplyJobMaxAttempts
	}
	nextAttempt := task.AttemptCount + 1
	updates := map[string]any{
		"failure_class":     limitText(strings.TrimSpace(failureClass), 40),
		"attempt_count":     gorm.Expr("attempt_count + 1"),
		"claimed_by_job_id": 0,
		"claimed_version":   0,
		"next_retry_at":     nil,
		"updated_at":        now,
		"update_user_name":  "ai_reply_task_failure",
	}
	if nextAttempt >= maxAttempts {
		updates["stage"] = enums.AIReplyTurnTaskStageComplete
		updates["status"] = enums.AIReplyTurnTaskStatusFailed
		updates["result_code"] = controlledResultCode("technical_failure", "technical_failure")
		updates["completed_at"] = now
	} else {
		updates["stage"] = enums.AIReplyTurnTaskStageIntent
		updates["status"] = enums.AIReplyTurnTaskStatusPending
		nextRetry := aiReplyTaskRetryAt(now, nextAttempt)
		updates["next_retry_at"] = nextRetry
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, updates)
}

// AttachKnowledgeCheckpointDB 持久化知识阶段检查点：一次成功检索的
// retrieveLogID 与 evidence fingerprint，阶段恢复时按 fingerprint 复用。
func (s *aiReplyTurnTaskService) AttachKnowledgeCheckpointDB(
	db *gorm.DB,
	tenantID, turnID int64,
	taskKey string,
	retrieveLogID int64,
	evidenceFingerprint string,
	now time.Time,
) error {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return errorsx.InvalidParam("AI 回复知识检查点缺少范围")
	}
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return errorsx.InvalidParam("AI 回复知识检查点缺少任务键")
	}
	task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
	if err != nil {
		return err
	}
	if task == nil || aiReplyTurnTaskTerminal(task.Status) {
		return nil
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
		"knowledge_retrieve_log_id": retrieveLogID,
		"evidence_fingerprint":      limitText(strings.TrimSpace(evidenceFingerprint), 64),
		"updated_at":                now,
		"update_user_name":          "ai_reply_knowledge_checkpoint",
	})
}

// CancelHandoffDecisionDB 事务化关闭误判的人工兜底：把 handoff_pending 任务
// 回退为可执行 pending，而不是留下半开的人工接管状态。
func (s *aiReplyTurnTaskService) CancelHandoffDecisionDB(
	db *gorm.DB,
	tenantID, turnID int64,
	taskKeys []string,
	resultCode string,
	now time.Time,
) error {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return errorsx.InvalidParam("AI 回复取消人工兜底缺少范围")
	}
	keys := uniqueTaskKeys(taskKeys)
	if len(keys) == 0 {
		return nil
	}
	for _, taskKey := range keys {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || task.Status != enums.AIReplyTurnTaskStatusHandoffPending {
			continue
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":             enums.AIReplyTurnTaskStageGenerate,
			"status":            enums.AIReplyTurnTaskStatusPending,
			"result_code":       controlledResultCode(resultCode, "handoff_cancelled"),
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"completed_at":      nil,
			"next_retry_at":     nil,
			"updated_at":        now,
			"update_user_name":  "ai_reply_handoff_cancel",
		}); err != nil {
			return err
		}
	}
	return nil
}

// taskKeyOwner 返回占用该 TaskKey 的既有任务（可能为 nil）。
func (s *aiReplyTurnTaskService) taskKeyOwner(db *gorm.DB, turn *models.AIReplyTurn, taskKey string) *models.AIReplyTurnTask {
	if taskKey == "" {
		return nil
	}
	return repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, taskKey)
}
