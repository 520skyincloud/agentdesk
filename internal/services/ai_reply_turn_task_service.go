package services

import (
	"encoding/json"

	"agent-desk/internal/ai/runtime/contracts"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	QuestionUnitKey string
	SequenceNo      int
	OccurrenceIndex int
	TaskType        enums.AIReplyTurnTaskType
	Intent          string
	SubIntent       string
	RequestMode     string
	RelationType    string
	RelatedTaskID   int64
	// RelatedTaskKey is an ephemeral relation reference from the normalized
	// QuestionUnit. It is resolved to RelatedTaskID only inside the turn
	// transaction and is never part of the stable task identity.
	RelatedTaskKey string
	ResourceAction string
	QuestionText   string
	// 多模态契约 3.2/22.11：QuestionUnit 持久来源绑定与摘要；临时 SourceRef 不入库。
	AnalysisRevision        int
	AnswerRequirementsJSON  string
	SourceSpanStart         int
	SourceSpanEnd           int
	SourceBindingsJSON      string
	ObservationBindingsJSON string
	SourceSetFingerprint    string
	CanonicalQuestionHash   string
	CapabilityCode          string
	CapabilityRoute         string
	CapabilityFingerprint   string
	AnswerGroupKey          string
}

type AIReplyTurnTaskKnowledgeUpdate struct {
	TaskKey    string
	Status     enums.AIReplyTurnTaskKnowledgeStatus
	HitCount   int
	ResultCode string
	// RetrieveLogID/QueryFingerprint 绑定 Task↔RetrieveLog 审计链（契约 4.17）。
	RetrieveLogID    int64
	QueryFingerprint string
}

func newAIReplyTurnTaskService() *aiReplyTurnTaskService {
	return &aiReplyTurnTaskService{}
}

func (s *aiReplyTurnTaskService) Enabled() bool {
	return sqls.DB() != nil && sqls.DB().Migrator().HasTable(&models.AIReplyTurnTask{})
}

func (s *aiReplyTurnTaskService) StableTaskKey(input AIReplyTurnTaskInput) string {
	// 契约 3.2.3：Task 是来源片段的持久处理标签，身份只能由租户、轮次和
	// 不可变来源集合决定。Intent/SubIntent/TaskType/模型改写均是可修订分析结果，
	// 绝不能在协议修复或重试时制造第二个物理 Task。
	sourceSetFingerprint := strings.TrimSpace(input.SourceSetFingerprint)
	if input.TenantID > 0 && input.TurnID > 0 && input.SourceMessageID > 0 &&
		sourceSetFingerprint != "" {
		raw := fmt.Sprintf(
			"%d/%d/%d/%s",
			input.TenantID, input.TurnID, input.SourceMessageID,
			sourceSetFingerprint,
		)
		sum := sha256.Sum256([]byte(raw))
		return "turn_task_" + hex.EncodeToString(sum[:16])
	}
	if input.TenantID > 0 && input.TurnID > 0 && input.SourceMessageID > 0 {
		sum := sha256.Sum256([]byte(fmt.Sprintf(
			"%d/%d/%d/%d/%d",
			input.TenantID,
			input.TurnID,
			input.SourceMessageID,
			input.SourceSpanStart,
			input.SourceSpanEnd,
		)))
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
		return "turn_task_" + strings.ToLower(encoded[:26])
	}
	sequenceNo := input.SequenceNo
	if sequenceNo <= 0 {
		sequenceNo = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", input.SourceMessageID, sequenceNo)))
	return "turn_task_" + hex.EncodeToString(sum[:12])
}

func (s *aiReplyTurnTaskService) SemanticQuestionFingerprint(input AIReplyTurnTaskInput) string {
	if canonical := strings.TrimSpace(input.CanonicalQuestionHash); canonical != "" {
		return canonical
	}
	return s.QuestionFingerprint(input.QuestionText)
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
	existingByTaskKey := make(map[string]*models.AIReplyTurnTask, len(existingTasks))
	resolvedTaskKeys := make([]string, len(inputs))
	for index := range existingTasks {
		existing := &existingTasks[index]
		existingByTaskKey[existing.TaskKey] = existing
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
		normalizeAIReplyTurnTaskSourceIdentity(&input)
		normalizedObservationBindings, observationErr := normalizeAIReplyTurnTaskObservationBindings(db, turn, input.ObservationBindingsJSON)
		if observationErr != nil {
			return nil, observationErr
		}
		input.ObservationBindingsJSON = normalizedObservationBindings
		fingerprint := s.SemanticQuestionFingerprint(input)
		// 契约 3.2.2：canonical hash 非空时写入 CanonicalQuestionHash，
		// QuestionFingerprint 作为兼容副本。
		canonicalHash := strings.TrimSpace(input.CanonicalQuestionHash)
		if canonicalHash == "" && fingerprint != "" {
			canonicalHash = fingerprint
		}
		taskKey := s.StableTaskKey(input)
		resolvedTaskKeys[index] = taskKey
		normalizedRequirements, requirementsErr := normalizeAnswerRequirementsForTask(input.AnswerRequirementsJSON, taskKey, input)
		if requirementsErr != nil {
			return nil, requirementsErr
		}
		input.AnswerRequirementsJSON = normalizedRequirements
		existingIdentity := existingByTaskKey[taskKey]
		if existingIdentity != nil {
			if canonicalHash == "" {
				canonicalHash = existingIdentity.CanonicalQuestionHash
			}
		}
		if owner := s.taskKeyOwner(db, turn, taskKey); owner != nil &&
			(owner.SourceMessageID != input.SourceMessageID || owner.SourceSpanStart != input.SourceSpanStart ||
				owner.SourceSpanEnd != input.SourceSpanEnd ||
				strings.TrimSpace(owner.SourceSetFingerprint) != strings.TrimSpace(input.SourceSetFingerprint)) {
			return nil, fmt.Errorf("AI reply task stable key collision: %s", taskKey)
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
			TaskKey: taskKey, QuestionUnitKey: limitText(strings.TrimSpace(input.QuestionUnitKey), 128),
			SequenceNo: input.SequenceNo, TaskType: input.TaskType,
			Intent: limitText(strings.TrimSpace(input.Intent), 80), SubIntent: limitText(strings.TrimSpace(input.SubIntent), 120),
			ResourceAction: limitText(strings.TrimSpace(input.ResourceAction), 80), QuestionFingerprint: fingerprint,
			RelationType: limitText(strings.TrimSpace(input.RelationType), 24), RelatedTaskID: input.RelatedTaskID,
			AnalysisRevision:        input.AnalysisRevision,
			SourceSpanStart:         input.SourceSpanStart,
			SourceSpanEnd:           input.SourceSpanEnd,
			SourceBindingsJSON:      strings.TrimSpace(input.SourceBindingsJSON),
			ObservationBindingsJSON: strings.TrimSpace(input.ObservationBindingsJSON),
			SourceSetFingerprint:    limitText(strings.TrimSpace(input.SourceSetFingerprint), 64),
			CanonicalQuestionHash:   limitText(canonicalHash, 64),
			RequestMode:             limitText(strings.TrimSpace(input.RequestMode), 24),
			AnswerRequirementsJSON:  strings.TrimSpace(input.AnswerRequirementsJSON),
			CapabilityCode:          limitText(strings.TrimSpace(input.CapabilityCode), 120),
			CapabilityRoute:         limitText(strings.TrimSpace(input.CapabilityRoute), 40),
			CapabilityFingerprint:   limitText(strings.TrimSpace(input.CapabilityFingerprint), 64),
			AnswerGroupKey:          limitText(strings.TrimSpace(input.AnswerGroupKey), 128),
			Stage:                   stage, Status: status, KnowledgeStatus: knowledgeStatus,
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
			if aiReplyTurnTaskAnalysisRevisable(existing.Status) {
				analysisChanged := aiReplyTurnTaskAnalysisChanged(existing, input, fingerprint, canonicalHash)
				updates := map[string]any{
					"introduced_version":        turn.Version,
					"source_message_id":         input.SourceMessageID,
					"question_unit_key":         limitText(strings.TrimSpace(input.QuestionUnitKey), 128),
					"sequence_no":               input.SequenceNo,
					"task_type":                 input.TaskType,
					"intent":                    limitText(strings.TrimSpace(input.Intent), 80),
					"sub_intent":                limitText(strings.TrimSpace(input.SubIntent), 120),
					"resource_action":           limitText(strings.TrimSpace(input.ResourceAction), 80),
					"request_mode":              limitText(strings.TrimSpace(input.RequestMode), 24),
					"question_fingerprint":      fingerprint,
					"canonical_question_hash":   limitText(canonicalHash, 64),
					"relation_type":             limitText(strings.TrimSpace(input.RelationType), 24),
					"related_task_id":           input.RelatedTaskID,
					"analysis_revision":         input.AnalysisRevision,
					"source_span_start":         input.SourceSpanStart,
					"source_span_end":           input.SourceSpanEnd,
					"source_bindings_json":      strings.TrimSpace(input.SourceBindingsJSON),
					"observation_bindings_json": strings.TrimSpace(input.ObservationBindingsJSON),
					"source_set_fingerprint":    limitText(strings.TrimSpace(input.SourceSetFingerprint), 64),
					"answer_requirements_json":  strings.TrimSpace(input.AnswerRequirementsJSON),
					"capability_code":           limitText(strings.TrimSpace(input.CapabilityCode), 120),
					"capability_route":          limitText(strings.TrimSpace(input.CapabilityRoute), 40),
					"capability_fingerprint":    limitText(strings.TrimSpace(input.CapabilityFingerprint), 64),
					"answer_group_key":          limitText(strings.TrimSpace(input.AnswerGroupKey), 128),
					"updated_at":                now,
					"update_user_name":          "ai_reply_task",
				}
				if analysisChanged {
					updates["stage"] = stage
					updates["status"] = status
					updates["knowledge_status"] = knowledgeStatus
					updates["claimed_by_job_id"] = int64(0)
					updates["claimed_version"] = 0
					updates["covered_by_task_id"] = int64(0)
					updates["attempt_count"] = 0
					updates["knowledge_hit_count"] = 0
					updates["knowledge_retrieve_log_id"] = int64(0)
					updates["knowledge_query_fingerprint"] = ""
					updates["requirement_state_json"] = ""
					updates["evidence_fingerprint"] = ""
					updates["failure_class"] = ""
					updates["result_code"] = ""
					updates["committed_message_id"] = int64(0)
					updates["next_retry_at"] = nil
					updates["completed_at"] = nil
				}
				if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, existing.ID, existing.TenantID, updates); err != nil {
					return nil, err
				}
				item.IntroducedVersion = turn.Version
				item.SourceMessageID = input.SourceMessageID
				item.QuestionUnitKey = limitText(strings.TrimSpace(input.QuestionUnitKey), 128)
				item.SequenceNo = input.SequenceNo
				item.TaskType = input.TaskType
				item.Intent = limitText(strings.TrimSpace(input.Intent), 80)
				item.SubIntent = limitText(strings.TrimSpace(input.SubIntent), 120)
				item.ResourceAction = limitText(strings.TrimSpace(input.ResourceAction), 80)
				item.RequestMode = limitText(strings.TrimSpace(input.RequestMode), 24)
				item.QuestionFingerprint = fingerprint
				item.RelationType = limitText(strings.TrimSpace(input.RelationType), 24)
				item.RelatedTaskID = input.RelatedTaskID
				item.AnalysisRevision = input.AnalysisRevision
				item.SourceSpanStart = input.SourceSpanStart
				item.SourceSpanEnd = input.SourceSpanEnd
				item.SourceBindingsJSON = strings.TrimSpace(input.SourceBindingsJSON)
				item.ObservationBindingsJSON = strings.TrimSpace(input.ObservationBindingsJSON)
				item.SourceSetFingerprint = limitText(strings.TrimSpace(input.SourceSetFingerprint), 64)
				item.CanonicalQuestionHash = limitText(canonicalHash, 64)
				item.AnswerRequirementsJSON = strings.TrimSpace(input.AnswerRequirementsJSON)
				item.CapabilityCode = limitText(strings.TrimSpace(input.CapabilityCode), 120)
				item.CapabilityRoute = limitText(strings.TrimSpace(input.CapabilityRoute), 40)
				item.CapabilityFingerprint = limitText(strings.TrimSpace(input.CapabilityFingerprint), 64)
				item.AnswerGroupKey = limitText(strings.TrimSpace(input.AnswerGroupKey), 128)
				if analysisChanged {
					item.Stage = stage
					item.Status = status
					item.KnowledgeStatus = knowledgeStatus
					item.ClaimedByJobID = 0
					item.ClaimedVersion = 0
					item.CoveredByTaskID = 0
					item.AttemptCount = 0
					item.KnowledgeHitCount = 0
					item.KnowledgeRetrieveLogID = 0
					item.KnowledgeQueryFingerprint = ""
					item.RequirementStateJSON = ""
					item.EvidenceFingerprint = ""
					item.FailureClass = ""
					item.ResultCode = ""
					item.CommittedMessageID = 0
					item.NextRetryAt = nil
					item.CompletedAt = nil
				}
			}
		}
		existingByTaskKey[taskKey] = item
		if created && fingerprint != "" && status != enums.AIReplyTurnTaskStatusHandoffPending {
			canonical := s.findCanonicalDuplicate(db, turn, item)
			if canonical != nil {
				covered, err := s.markDependentOnCanonical(db, canonical, item, now)
				if err != nil {
					return nil, err
				}
				item = covered
			}
		}
		ret = append(ret, *item)
	}
	// Resolve ephemeral parent references only after every input has been
	// created/loaded, so Q1 -> Q2 works even when the parent is created in this
	// same call. The relation is an indexed ID inside the same tenant/turn;
	// unknown or cross-scope references are cleared rather than failing the
	// customer's whole reply.
	for index := range inputs {
		input := inputs[index]
		taskKey := resolvedTaskKeys[index]
		if taskKey == "" {
			continue
		}
		child := existingByTaskKey[taskKey]
		if child == nil {
			continue
		}
		parentKey := strings.TrimSpace(input.RelatedTaskKey)
		parentID := int64(0)
		if parentKey != "" && parentKey != taskKey {
			parent := existingByTaskKey[parentKey]
			if parent == nil {
				parent, _ = repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, turn.TenantID, turn.ID, parentKey)
			}
			if parent != nil && parent.ID != child.ID && parent.TenantID == turn.TenantID &&
				parent.TurnID == turn.ID && parent.ConversationID == turn.ConversationID && parent.SessionNo == turn.SessionNo {
				parentID = parent.ID
			} else {
				slog.Warn("AI reply task parent reference rejected", "turn_id", turn.ID, "task_key", taskKey, "reason", "parent_scope_or_identity_invalid")
			}
		}
		if child.RelatedTaskID == parentID && (parentID > 0 || strings.TrimSpace(input.RelationType) != "new_topic") {
			continue
		}
		updates := map[string]any{"related_task_id": parentID, "updated_at": now, "update_user_name": "ai_reply_task"}
		if parentID > 0 && strings.TrimSpace(input.RelationType) == "" {
			updates["relation_type"] = "follow_up"
			child.RelationType = "follow_up"
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, child.ID, turn.TenantID, updates); err != nil {
			return nil, err
		}
		child.RelatedTaskID = parentID
	}
	sortAIReplyTurnTasks(ret)
	for index := range ret {
		if item := existingByTaskKey[ret[index].TaskKey]; item != nil {
			ret[index].RelatedTaskID = item.RelatedTaskID
			ret[index].RelationType = item.RelationType
		}
	}
	return ret, nil
}

func normalizeAIReplyTurnTaskSourceIdentity(input *AIReplyTurnTaskInput) {
	if input == nil || input.SourceMessageID <= 0 {
		return
	}
	if input.SourceSpanStart < 0 {
		input.SourceSpanStart = 0
	}
	if input.SourceSpanEnd < input.SourceSpanStart {
		input.SourceSpanEnd = input.SourceSpanStart
	}
	if strings.TrimSpace(input.SourceBindingsJSON) == "" {
		raw, _ := json.Marshal([]map[string]any{{
			"messageId": input.SourceMessageID,
			"spanStart": input.SourceSpanStart,
			"spanEnd":   input.SourceSpanEnd,
		}})
		input.SourceBindingsJSON = string(raw)
	}
	if strings.TrimSpace(input.SourceSetFingerprint) == "" {
		sum := sha256.Sum256([]byte(input.SourceBindingsJSON))
		input.SourceSetFingerprint = hex.EncodeToString(sum[:])
	}
}

type aiReplyTurnTaskObservationBinding struct {
	MessageID      int64 `json:"messageId"`
	SourceRevision int   `json:"sourceRevision"`
}

func normalizeAIReplyTurnTaskObservationBindings(db *gorm.DB, turn *models.AIReplyTurn, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var bindings []aiReplyTurnTaskObservationBinding
	if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
		return "", fmt.Errorf("AI reply task observation bindings are invalid: %w", err)
	}
	seen := make(map[string]struct{}, len(bindings))
	normalized := make([]aiReplyTurnTaskObservationBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.MessageID <= 0 || binding.SourceRevision <= 0 {
			return "", errorsx.InvalidParam("AI 回复逐题任务媒体观察绑定无效")
		}
		message := repositories.MessageRepository.GetInTenant(db, binding.MessageID, turn.TenantID)
		if message == nil || message.ConversationID != turn.ConversationID || message.SessionNo != turn.SessionNo ||
			message.AIReplyTurnID != turn.ID || message.SenderType != enums.IMSenderTypeCustomer {
			return "", errorsx.InvalidParam("AI 回复逐题任务媒体观察范围不一致")
		}
		key := fmt.Sprintf("%d/%d", binding.MessageID, binding.SourceRevision)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, binding)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].MessageID == normalized[j].MessageID {
			return normalized[i].SourceRevision < normalized[j].SourceRevision
		}
		return normalized[i].MessageID < normalized[j].MessageID
	})
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode AI reply task observation bindings: %w", err)
	}
	return string(encoded), nil
}

func normalizeAnswerRequirementsForTask(raw, taskKey string, input AIReplyTurnTaskInput) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	set, err := contracts.DecodeAnswerRequirementSetV1([]byte(raw))
	if err != nil {
		return "", fmt.Errorf("AI reply task %s answer requirements are invalid: %w", taskKey, err)
	}
	if err := contracts.ValidateAnswerRequirementBindingV1(
		set, taskKey, input.SourceMessageID, input.SourceSpanStart, input.SourceSpanEnd,
	); err != nil {
		return "", fmt.Errorf("AI reply task %s answer requirements do not match source: %w", taskKey, err)
	}
	canonical, err := contracts.MarshalAnswerRequirementSetV1(set)
	if err != nil {
		return "", fmt.Errorf("AI reply task %s answer requirements cannot be encoded: %w", taskKey, err)
	}
	return string(canonical), nil
}

func aiReplyTurnTaskAnalysisChanged(existing *models.AIReplyTurnTask, input AIReplyTurnTaskInput, fingerprint, canonicalHash string) bool {
	if existing == nil {
		return true
	}
	return existing.TaskType != input.TaskType ||
		existing.Intent != limitText(strings.TrimSpace(input.Intent), 80) ||
		existing.SubIntent != limitText(strings.TrimSpace(input.SubIntent), 120) ||
		existing.ResourceAction != limitText(strings.TrimSpace(input.ResourceAction), 80) ||
		existing.RequestMode != limitText(strings.TrimSpace(input.RequestMode), 24) ||
		existing.RelationType != limitText(strings.TrimSpace(input.RelationType), 24) ||
		existing.RelatedTaskID != input.RelatedTaskID ||
		existing.QuestionFingerprint != strings.TrimSpace(fingerprint) ||
		existing.CanonicalQuestionHash != limitText(strings.TrimSpace(canonicalHash), 64) ||
		existing.ObservationBindingsJSON != strings.TrimSpace(input.ObservationBindingsJSON) ||
		existing.AnswerRequirementsJSON != strings.TrimSpace(input.AnswerRequirementsJSON) ||
		existing.CapabilityCode != limitText(strings.TrimSpace(input.CapabilityCode), 120) ||
		existing.CapabilityRoute != limitText(strings.TrimSpace(input.CapabilityRoute), 40) ||
		existing.CapabilityFingerprint != limitText(strings.TrimSpace(input.CapabilityFingerprint), 64) ||
		existing.AnswerGroupKey != limitText(strings.TrimSpace(input.AnswerGroupKey), 128)
}

// markDependentOnCanonical records exact-duplicate ownership without claiming
// that the duplicate is already answered. A real committed message closes the
// dependency immediately; otherwise the duplicate waits for the canonical
// task's durable transition.
func (s *aiReplyTurnTaskService) markDependentOnCanonical(db *gorm.DB, canonical, current *models.AIReplyTurnTask, now time.Time) (*models.AIReplyTurnTask, error) {
	if canonical == nil || current == nil || canonical.ID <= 0 || canonical.ID == current.ID {
		return current, nil
	}
	canonical = s.resolveCoverageCanonicalDB(db, canonical)
	if canonical == nil || canonical.ID <= 0 || canonical.ID == current.ID {
		return current, nil
	}
	switch canonical.Status {
	case enums.AIReplyTurnTaskStatusCommitted, enums.AIReplyTurnTaskStatusDelivered, enums.AIReplyTurnTaskStatusCovered:
		if canonical.CommittedMessageID <= 0 {
			// Historical rows may contain evidence-free covered states. They are
			// not valid coverage and must not suppress a new task.
			return current, nil
		}
		if err := s.markCoverageDependentCoveredDB(db, canonical, current, canonical.CommittedMessageID, "covered_by_committed_task", now); err != nil {
			return nil, err
		}
		return current, nil
	case enums.AIReplyTurnTaskStatusHandoff:
		if err := s.markCoverageDependentHandoffDB(db, canonical, current, now); err != nil {
			return nil, err
		}
		return current, nil
	case enums.AIReplyTurnTaskStatusFailed, enums.AIReplyTurnTaskStatusSkipped, enums.AIReplyTurnTaskStatusSuperseded:
		return current, nil
	}
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, current.ID, current.TenantID, map[string]any{
		"status":             enums.AIReplyTurnTaskStatusWaitingCoverage,
		"covered_by_task_id": canonical.ID,
		"result_code":        "waiting_for_canonical_task",
		"completed_at":       nil,
		"claimed_by_job_id":  0,
		"claimed_version":    0,
		"next_retry_at":      nil,
		"updated_at":         now,
		"update_user_name":   "ai_reply_task",
	}); err != nil {
		return nil, err
	}
	current.Status = enums.AIReplyTurnTaskStatusWaitingCoverage
	current.CoveredByTaskID = canonical.ID
	current.ResultCode = "waiting_for_canonical_task"
	current.CompletedAt = nil
	current.ClaimedByJobID = 0
	current.ClaimedVersion = 0
	return current, nil
}

func (s *aiReplyTurnTaskService) resolveCoverageCanonicalDB(db *gorm.DB, task *models.AIReplyTurnTask) *models.AIReplyTurnTask {
	current := task
	seen := map[int64]struct{}{}
	for current != nil && current.Status == enums.AIReplyTurnTaskStatusCovered && current.CommittedMessageID <= 0 && current.CoveredByTaskID > 0 {
		if _, exists := seen[current.ID]; exists {
			return nil
		}
		seen[current.ID] = struct{}{}
		next, err := repositories.AIReplyTurnTaskRepository.GetForUpdateInTenant(db, current.CoveredByTaskID, current.TenantID)
		if err != nil || next == nil || next.TurnID != current.TurnID {
			return nil
		}
		current = next
	}
	return current
}

func (s *aiReplyTurnTaskService) markCoverageDependentCoveredDB(
	db *gorm.DB,
	canonical, dependent *models.AIReplyTurnTask,
	messageID int64,
	resultCode string,
	now time.Time,
) error {
	if canonical == nil || dependent == nil || messageID <= 0 {
		return nil
	}
	if err := s.ensureRequirementCoveredByMessage(db, dependent, messageID, resultCode, now); err != nil {
		return err
	}
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, dependent.ID, dependent.TenantID, map[string]any{
		"stage":                enums.AIReplyTurnTaskStageComplete,
		"status":               enums.AIReplyTurnTaskStatusCovered,
		"covered_by_task_id":   canonical.ID,
		"result_code":          controlledResultCode(resultCode, "covered_by_committed_task"),
		"committed_message_id": messageID,
		"claimed_by_job_id":    0,
		"claimed_version":      0,
		"completed_at":         now,
		"next_retry_at":        nil,
		"updated_at":           now,
		"update_user_name":     "ai_reply_task_coverage",
	}); err != nil {
		return err
	}
	dependent.Stage = enums.AIReplyTurnTaskStageComplete
	dependent.Status = enums.AIReplyTurnTaskStatusCovered
	dependent.CoveredByTaskID = canonical.ID
	dependent.ResultCode = controlledResultCode(resultCode, "covered_by_committed_task")
	dependent.CommittedMessageID = messageID
	dependent.CompletedAt = &now
	return nil
}

func (s *aiReplyTurnTaskService) markCoverageDependentHandoffDB(
	db *gorm.DB,
	canonical, dependent *models.AIReplyTurnTask,
	now time.Time,
) error {
	if canonical == nil || dependent == nil {
		return nil
	}
	if err := s.attachRequirementTaskOutcomeDB(db, dependent, "handoff", fmt.Sprintf("task:%d", canonical.ID), "covered_by_canonical_handoff", now); err != nil {
		return err
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, dependent.ID, dependent.TenantID, map[string]any{
		"stage":              enums.AIReplyTurnTaskStageComplete,
		"status":             enums.AIReplyTurnTaskStatusHandoff,
		"covered_by_task_id": canonical.ID,
		"result_code":        "covered_by_canonical_handoff",
		"claimed_by_job_id":  0,
		"claimed_version":    0,
		"completed_at":       now,
		"next_retry_at":      nil,
		"updated_at":         now,
		"update_user_name":   "ai_reply_task_coverage",
	})
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
		// Surface-identical deictic questions are not duplicates when they point
		// at different media evidence. "这个是什么" for image A and image B must
		// remain two independently answerable Tasks. Observation bindings are
		// normalized before persistence, so exact JSON equality is a stable
		// dependency-identity comparison.
		if strings.TrimSpace(candidate.ObservationBindingsJSON) != strings.TrimSpace(current.ObservationBindingsJSON) {
			continue
		}
		if strings.TrimSpace(candidate.ResourceAction) != strings.TrimSpace(current.ResourceAction) ||
			strings.TrimSpace(candidate.RelationType) != strings.TrimSpace(current.RelationType) {
			continue
		}
		candidateRequirements := strings.TrimSpace(candidate.AnswerRequirementsJSON)
		currentRequirements := strings.TrimSpace(current.AnswerRequirementsJSON)
		if candidateRequirements != "" && currentRequirements != "" && candidateRequirements != currentRequirements {
			continue
		}
		candidateCanonicalHash := strings.TrimSpace(candidate.CanonicalQuestionHash)
		if canonicalHash != "" && candidateCanonicalHash != "" && candidateCanonicalHash != canonicalHash {
			continue
		}
		// 同 canonical hash 时同源重复也覆盖；hash 缺失时保守只合并跨源重复。
		if candidate.SourceMessageID == current.SourceMessageID &&
			(canonicalHash == "" || candidateCanonicalHash != canonicalHash) {
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
		var completedAt *time.Time
		failureClass := ""
		if update.Status == enums.AIReplyTurnTaskKnowledgeStatusFailed {
			// FastGPT Gateway owns transport retries. Re-running the whole Task or Job
			// would multiply external calls and can later turn a technical outage into
			// an unrelated human handoff. Persist one explicit terminal outcome instead.
			status = enums.AIReplyTurnTaskStatusFailed
			stage = enums.AIReplyTurnTaskStageComplete
			claimedByJobID = 0
			claimedVersion = 0
			completedAt = &now
			failureClass = "knowledge"
			if err := s.attachRequirementTaskOutcomeDB(
				db, task, "failed_terminal", "", controlledResultCode(update.ResultCode, "knowledge_failed"), now,
			); err != nil {
				return err
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
			"failure_class":       failureClass,
			"next_retry_at":       nil,
			"completed_at":        completedAt,
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
		if status == enums.AIReplyTurnTaskStatusFailed {
			failedTask := *task
			failedTask.Stage = stage
			failedTask.Status = status
			failedTask.FailureClass = failureClass
			failedTask.ResultCode = controlledResultCode(update.ResultCode, "knowledge_failed")
			failedTask.CompletedAt = completedAt
			if err := s.resolveCoverageDependentsDB(db, &failedTask, now); err != nil {
				return err
			}
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
		// 契约 3.2.4：Task completed 前所有 Required Requirement 必须由
		// 真实 RetrieveLog/Message 证据进入终态，禁止凭状态字段伪造 answered。
		if err := s.EnsureRequirementTerminalForTask(db, task, messageID, now); err != nil {
			return err
		}
		updates := map[string]any{
			"stage":                stage,
			"status":               status,
			"result_code":          aiReplyTaskCommitResultCode(task, delivered),
			"committed_message_id": messageID,
			"updated_at":           now,
			"update_user_name":     "ai_reply_commit",
		}
		if delivered {
			updates["completed_at"] = now
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, updates); err != nil {
			return err
		}
		committedTask := *task
		committedTask.Stage = stage
		committedTask.Status = status
		committedTask.CommittedMessageID = messageID
		if err := s.resolveCoverageDependentsDB(db, &committedTask, now); err != nil {
			return err
		}
	}
	return nil
}

func aiReplyTaskCommitResultCode(task *models.AIReplyTurnTask, delivered bool) string {
	base := "committed"
	if delivered {
		base = "delivered"
	}
	if task == nil || task.TaskType != enums.AIReplyTurnTaskTypeKnowledge {
		return base
	}
	switch task.KnowledgeStatus {
	case enums.AIReplyTurnTaskKnowledgeStatusNoHit:
		return base + "_no_hit"
	case enums.AIReplyTurnTaskKnowledgeStatusNoContext:
		return base + "_no_context"
	default:
		return base
	}
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
		if err := s.ensureRequirementCoveredByMessage(db, task, item.CoveredByMessageID, item.ResultCode, now); err != nil {
			return err
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
		coveredTask := *task
		coveredTask.Stage = enums.AIReplyTurnTaskStageComplete
		coveredTask.Status = enums.AIReplyTurnTaskStatusCovered
		coveredTask.CommittedMessageID = item.CoveredByMessageID
		if err := s.resolveCoverageDependentsDB(db, &coveredTask, now); err != nil {
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
			"stage":  enums.AIReplyTurnTaskStageComplete,
			"status": enums.AIReplyTurnTaskStatusDelivered,
			"result_code": gorm.Expr(
				"CASE WHEN knowledge_status = ? THEN ? WHEN knowledge_status = ? THEN ? ELSE ? END",
				enums.AIReplyTurnTaskKnowledgeStatusNoContext, "delivered_no_context",
				enums.AIReplyTurnTaskKnowledgeStatusNoHit, "delivered_no_hit", "delivered",
			),
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

// HasWorkPending reports whether AI-side work is still active or waiting for a
// canonical duplicate to produce durable evidence. Committed tasks belong to
// delivery, and handoff_pending tasks belong to the handoff state machine, so
// neither is counted here.
func (s *aiReplyTurnTaskService) HasWorkPending(tenantID, turnID int64) bool {
	if !s.Enabled() {
		return false
	}
	return repositories.AIReplyTurnTaskRepository.CountWorkPendingByTurnInTenant(sqls.DB(), tenantID, turnID) > 0
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

func (s *aiReplyTurnTaskService) HasTerminalFailures(tenantID, turnID int64) bool {
	if !s.Enabled() {
		return false
	}
	return repositories.AIReplyTurnTaskRepository.CountFailedByTurnInTenant(sqls.DB(), tenantID, turnID) > 0
}

func (s *aiReplyTurnTaskService) MarkPendingHandoffsDB(db *gorm.DB, tenantID, turnID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || !db.Migrator().HasTable(&models.AIReplyTurnTask{}) {
		return nil
	}
	candidates := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, tenantID, turnID)
	if err := db.Model(&models.AIReplyTurnTask{}).
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
		}).Error; err != nil {
		return err
	}
	for index := range candidates {
		if candidates[index].Status != enums.AIReplyTurnTaskStatusHandoffPending {
			continue
		}
		candidates[index].Status = enums.AIReplyTurnTaskStatusHandoff
		candidates[index].Stage = enums.AIReplyTurnTaskStageComplete
		if err := s.resolveCoverageDependentsDB(db, &candidates[index], now); err != nil {
			return err
		}
	}
	return nil
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
			enums.AIReplyTurnTaskStatusWaitingCoverage,
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
				enums.AIReplyTurnTaskStatusWaitingCoverage,
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
			enums.AIReplyTurnTaskStatusWaitingCoverage,
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
			[]enums.AIReplyTurnTaskStatus{
				enums.AIReplyTurnTaskStatusPending,
				enums.AIReplyTurnTaskStatusReady,
				enums.AIReplyTurnTaskStatusRunning,
				enums.AIReplyTurnTaskStatusWaitingCoverage,
			},
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
		handoffTask := *task
		handoffTask.Stage = enums.AIReplyTurnTaskStageComplete
		handoffTask.Status = enums.AIReplyTurnTaskStatusHandoff
		if err := s.resolveCoverageDependentsDB(db, &handoffTask, now); err != nil {
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

// ReleaseTaskKeysDB releases only the claimed tasks that were not selected for
// the current ReplyPlanV4 batch. Their knowledge checkpoint remains intact, so
// the continuation job resumes at Generate instead of querying FastGPT again.
func (s *aiReplyTurnTaskService) ReleaseTaskKeysDB(
	db *gorm.DB,
	tenantID, turnID, jobID int64,
	taskKeys []string,
	now time.Time,
) error {
	return s.releaseTaskKeysDB(db, tenantID, turnID, jobID, taskKeys, "deferred_to_next_reply_batch", nil, now)
}

// DeferTaskKeysDB releases an exact claimed subset with a durable retry time.
// It is used for external prerequisites such as media analysis: the Task is
// unfinished but must not be reclaimed in a tight continuation loop.
func (s *aiReplyTurnTaskService) DeferTaskKeysDB(
	db *gorm.DB,
	tenantID, turnID, jobID int64,
	taskKeys []string,
	resultCode string,
	nextRetryAt time.Time,
	now time.Time,
) error {
	if nextRetryAt.Before(now) {
		nextRetryAt = now
	}
	return s.releaseTaskKeysDB(db, tenantID, turnID, jobID, taskKeys, resultCode, &nextRetryAt, now)
}

func (s *aiReplyTurnTaskService) releaseTaskKeysDB(
	db *gorm.DB,
	tenantID, turnID, jobID int64,
	taskKeys []string,
	resultCode string,
	nextRetryAt *time.Time,
	now time.Time,
) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || jobID <= 0 || len(taskKeys) == 0 {
		return nil
	}
	resultCode = controlledResultCode(resultCode, "deferred_to_next_reply_batch")
	for _, taskKey := range uniqueTaskKeys(taskKeys) {
		task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
		if err != nil {
			return err
		}
		if task == nil || task.Status != enums.AIReplyTurnTaskStatusRunning || task.ClaimedByJobID != jobID {
			continue
		}
		stage := task.Stage
		if task.KnowledgeStatus == enums.AIReplyTurnTaskKnowledgeStatusPending {
			stage = enums.AIReplyTurnTaskStageKnowledge
		} else if stage == "" || stage == enums.AIReplyTurnTaskStageComplete {
			stage = enums.AIReplyTurnTaskStageGenerate
		}
		updates := map[string]any{
			"stage": stage, "status": enums.AIReplyTurnTaskStatusPending,
			"claimed_by_job_id": 0, "claimed_version": 0,
			"next_retry_at": nextRetryAt, "result_code": resultCode,
			"updated_at": now, "update_user_name": "ai_reply_task_batch",
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, tenantID, updates); err != nil {
			return err
		}
	}
	return nil
}

// MarkClaimedPolicySkippedDB closes the exact Task batch selected by a Runtime
// policy decision. A skipped Job must not release these claims back to pending:
// doing so re-enqueues the same no-reply work on every cron tick.
func (s *aiReplyTurnTaskService) MarkClaimedPolicySkippedDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	jobID int64,
	taskKeys []string,
	resultCode string,
	now time.Time,
) error {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 || jobID <= 0 {
		return errorsx.InvalidParam("AI 回复跳过任务缺少领取范围")
	}
	resultCode = controlledResultCode(resultCode, "policy_skipped")
	targets := make(map[string]struct{}, len(taskKeys))
	for _, taskKey := range uniqueTaskKeys(taskKeys) {
		targets[taskKey] = struct{}{}
	}
	items := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, turn.TenantID, turn.ID)
	for index := range items {
		task := &items[index]
		if task.Status != enums.AIReplyTurnTaskStatusRunning || task.ClaimedByJobID != jobID {
			continue
		}
		if len(targets) > 0 {
			if _, selected := targets[strings.TrimSpace(task.TaskKey)]; !selected {
				continue
			}
		}
		if err := s.attachRequirementTaskOutcomeDB(
			db, task, "skipped_policy", fmt.Sprintf("job:%d", jobID), resultCode, now,
		); err != nil {
			return err
		}
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
			"stage":             enums.AIReplyTurnTaskStageComplete,
			"status":            enums.AIReplyTurnTaskStatusSkipped,
			"result_code":       resultCode,
			"failure_class":     "",
			"claimed_by_job_id": 0,
			"claimed_version":   0,
			"next_retry_at":     nil,
			"completed_at":      now,
			"updated_at":        now,
			"update_user_name":  "ai_reply_policy",
		}); err != nil {
			return err
		}
		skipped := *task
		skipped.Stage = enums.AIReplyTurnTaskStageComplete
		skipped.Status = enums.AIReplyTurnTaskStatusSkipped
		skipped.ResultCode = resultCode
		skipped.ClaimedByJobID = 0
		skipped.ClaimedVersion = 0
		skipped.CompletedAt = &now
		if err := s.resolveCoverageDependentsDB(db, &skipped, now); err != nil {
			return err
		}
	}
	return nil
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

func aiReplyTurnTaskAnalysisRevisable(status enums.AIReplyTurnTaskStatus) bool {
	switch status {
	case enums.AIReplyTurnTaskStatusPending, enums.AIReplyTurnTaskStatusReady,
		enums.AIReplyTurnTaskStatusRunning, enums.AIReplyTurnTaskStatusWaitingCoverage:
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
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, updates); err != nil {
		return err
	}
	if nextAttempt >= maxAttempts {
		failedTask := *task
		failedTask.Stage = enums.AIReplyTurnTaskStageComplete
		failedTask.Status = enums.AIReplyTurnTaskStatusFailed
		failedTask.ResultCode = "technical_failure"
		return s.resolveCoverageDependentsDB(db, &failedTask, now)
	}
	return nil
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

// AttachRequirementOutcomeDB 契约 10.10：逐 Requirement 写入 Outcome
// （CAS：只允许 pending/空 → 终态）。Task 全部 Required Requirement 到终态
// 才可 completed 的不变量由 EnsureRequirementTerminalForTask 强制。
func (s *aiReplyTurnTaskService) AttachRequirementOutcomeDB(
	db *gorm.DB, tenantID, turnID int64, taskKey string,
	states []contracts.RequirementStateItemV1, now time.Time,
) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || taskKey == "" || len(states) == 0 {
		return nil
	}
	task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(db, tenantID, turnID, taskKey)
	if err != nil {
		return err
	}
	if task == nil || aiReplyTurnTaskTerminal(task.Status) {
		return nil
	}
	if strings.TrimSpace(task.AnswerRequirementsJSON) == "" {
		return nil
	}
	requirements, err := contracts.DecodeAnswerRequirementSetV1([]byte(task.AnswerRequirementsJSON))
	if err != nil {
		return fmt.Errorf("AI reply task %s answer requirements are invalid: %w", task.TaskKey, err)
	}
	if err := contracts.ValidateAnswerRequirementBindingV1(
		requirements, task.TaskKey, task.SourceMessageID, task.SourceSpanStart, task.SourceSpanEnd,
	); err != nil {
		return fmt.Errorf("AI reply task %s answer requirements do not match task: %w", task.TaskKey, err)
	}
	allowedKeys := make(map[string]int, len(requirements.Requirements))
	for _, requirement := range requirements.Requirements {
		allowedKeys[requirement.Key] = requirement.Sequence
	}
	set := contracts.RequirementStateSetV1{SchemaVersion: contracts.RequirementStateSetV1SchemaVersion, States: []contracts.RequirementStateItemV1{}}
	if strings.TrimSpace(task.RequirementStateJSON) != "" {
		set, err = contracts.DecodeRequirementStateSetV1([]byte(task.RequirementStateJSON))
		if err != nil {
			return fmt.Errorf("AI reply task %s requirement state is invalid: %w", task.TaskKey, err)
		}
	}
	byKey := make(map[string]contracts.RequirementStateItemV1, len(set.States))
	for _, state := range set.States {
		if _, exists := allowedKeys[state.Key]; !exists {
			return fmt.Errorf("AI reply task %s requirement state references unknown key %s", task.TaskKey, state.Key)
		}
		byKey[state.Key] = state
	}
	for _, state := range states {
		if _, exists := allowedKeys[state.Key]; !exists {
			return fmt.Errorf("AI reply task %s requirement outcome references unknown key %s", task.TaskKey, state.Key)
		}
		if existing, ok := byKey[state.Key]; ok && contracts.RequirementOutcomeTerminal(existing.Outcome) {
			continue // 终态不可改写
		}
		byKey[state.Key] = state
	}
	merged := contracts.RequirementStateSetV1{
		SchemaVersion: contracts.RequirementStateSetV1SchemaVersion,
		States:        make([]contracts.RequirementStateItemV1, 0, len(byKey)),
	}
	for _, requirement := range requirements.Requirements {
		if state, exists := byKey[requirement.Key]; exists {
			merged.States = append(merged.States, state)
		}
	}
	raw, err := contracts.MarshalRequirementStateSetV1(merged)
	if err != nil {
		return fmt.Errorf("AI reply task %s requirement state cannot be encoded: %w", task.TaskKey, err)
	}
	return repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, task.ID, task.TenantID, map[string]any{
		"requirement_state_json": string(raw), "updated_at": now, "update_user_name": "ai_reply_requirement",
	})
}

// EnsureRequirementTerminalForTask 契约 3.2.4/10.10：提交前校验所有 Required
// Requirement 已有可追溯终态。缺失状态只能由本事务已创建的 Message 和本
// Task 的终态 RetrieveLog 补齐；技术失败、pending 或缺证据一律拒绝提交。
func (s *aiReplyTurnTaskService) EnsureRequirementTerminalForTask(db *gorm.DB, task *models.AIReplyTurnTask, messageID int64, now time.Time) error {
	if task == nil || strings.TrimSpace(task.AnswerRequirementsJSON) == "" {
		return nil
	}
	if db == nil || messageID <= 0 {
		return fmt.Errorf("AI reply task %s requirement completion lacks committed message evidence", task.TaskKey)
	}
	requirements, err := contracts.DecodeAnswerRequirementSetV1([]byte(task.AnswerRequirementsJSON))
	if err != nil {
		return fmt.Errorf("AI reply task %s answer requirements are invalid: %w", task.TaskKey, err)
	}
	if err := contracts.ValidateAnswerRequirementBindingV1(
		requirements, task.TaskKey, task.SourceMessageID, task.SourceSpanStart, task.SourceSpanEnd,
	); err != nil {
		return fmt.Errorf("AI reply task %s answer requirements do not match task: %w", task.TaskKey, err)
	}
	if len(requirements.Requirements) == 0 {
		return nil
	}
	stateSet := contracts.RequirementStateSetV1{SchemaVersion: contracts.RequirementStateSetV1SchemaVersion, States: []contracts.RequirementStateItemV1{}}
	if strings.TrimSpace(task.RequirementStateJSON) != "" {
		stateSet, err = contracts.DecodeRequirementStateSetV1([]byte(task.RequirementStateJSON))
		if err != nil {
			return fmt.Errorf("AI reply task %s requirement state is invalid: %w", task.TaskKey, err)
		}
	}
	outcomeByKey := make(map[string]string, len(stateSet.States))
	for _, state := range stateSet.States {
		outcomeByKey[state.Key] = state.Outcome
	}
	missing := false
	for _, requirement := range requirements.Requirements {
		if !requirement.Required {
			continue
		}
		if outcome, ok := outcomeByKey[requirement.Key]; ok && contracts.RequirementOutcomeTerminal(outcome) {
			continue
		}
		missing = true
		break
	}
	if !missing {
		return nil
	}
	outcome, evidenceRef, err := s.requirementCommitEvidence(db, task, messageID)
	if err != nil {
		return err
	}
	states := make([]contracts.RequirementStateItemV1, 0, len(requirements.Requirements))
	for _, requirement := range requirements.Requirements {
		if existing, ok := outcomeByKey[requirement.Key]; ok && contracts.RequirementOutcomeTerminal(existing) {
			continue
		}
		if !requirement.Required {
			continue
		}
		states = append(states, contracts.RequirementStateItemV1{
			Key: requirement.Key, Outcome: outcome, EvidenceRef: evidenceRef,
		})
	}
	if len(states) == 0 {
		return nil
	}
	return s.AttachRequirementOutcomeDB(db, task.TenantID, task.TurnID, task.TaskKey, states, now)
}

func (s *aiReplyTurnTaskService) requirementCommitEvidence(db *gorm.DB, task *models.AIReplyTurnTask, messageID int64) (string, string, error) {
	message := repositories.MessageRepository.GetInTenant(db, messageID, task.TenantID)
	if message == nil || message.ConversationID != task.ConversationID || message.AIReplyTurnID != task.TurnID || message.SenderType != enums.IMSenderTypeAI {
		return "", "", fmt.Errorf("AI reply task %s committed message evidence is invalid", task.TaskKey)
	}
	messageRef := fmt.Sprintf("message:%d", message.ID)
	if task.TaskType != enums.AIReplyTurnTaskTypeKnowledge {
		return "answered", messageRef, nil
	}
	if task.KnowledgeStatus == enums.AIReplyTurnTaskKnowledgeStatusFailed ||
		task.KnowledgeStatus == enums.AIReplyTurnTaskKnowledgeStatusPending ||
		task.KnowledgeStatus == enums.AIReplyTurnTaskKnowledgeStatusNone {
		return "", "", fmt.Errorf("AI reply knowledge task %s cannot commit with status %s", task.TaskKey, task.KnowledgeStatus)
	}
	if task.KnowledgeRetrieveLogID <= 0 {
		return "", "", fmt.Errorf("AI reply knowledge task %s lacks retrieve log evidence", task.TaskKey)
	}
	retrieveLog := repositories.KnowledgeRetrieveLogRepository.GetInTenant(db, task.KnowledgeRetrieveLogID, task.TenantID)
	if retrieveLog == nil || retrieveLog.TurnID != task.TurnID || retrieveLog.TaskID != task.ID ||
		strings.TrimSpace(retrieveLog.TaskKey) != strings.TrimSpace(task.TaskKey) {
		return "", "", fmt.Errorf("AI reply knowledge task %s retrieve log scope is invalid", task.TaskKey)
	}
	retrieveRef := fmt.Sprintf("retrieve_log:%d;%s", retrieveLog.ID, messageRef)
	switch task.KnowledgeStatus {
	case enums.AIReplyTurnTaskKnowledgeStatusHit:
		if retrieveLog.ExecutionStatus != "succeeded" || retrieveLog.HitCount <= 0 || retrieveLog.UsedChunkCount <= 0 {
			return "", "", fmt.Errorf("AI reply knowledge task %s lacks succeeded hit evidence", task.TaskKey)
		}
		return "answered", retrieveRef, nil
	case enums.AIReplyTurnTaskKnowledgeStatusNoHit:
		if retrieveLog.ExecutionStatus != "no_hit" || retrieveLog.HitCount != 0 {
			return "", "", fmt.Errorf("AI reply knowledge task %s no-hit evidence is invalid", task.TaskKey)
		}
		return "no_hit", retrieveRef, nil
	case enums.AIReplyTurnTaskKnowledgeStatusNoContext:
		if retrieveLog.ExecutionStatus != "succeeded" || retrieveLog.HitCount <= 0 {
			return "", "", fmt.Errorf("AI reply knowledge task %s no-context evidence is invalid", task.TaskKey)
		}
		return "no_context", retrieveRef, nil
	default:
		return "", "", fmt.Errorf("AI reply knowledge task %s has unsupported status %s", task.TaskKey, task.KnowledgeStatus)
	}
}

func (s *aiReplyTurnTaskService) ensureRequirementCoveredByMessage(db *gorm.DB, task *models.AIReplyTurnTask, messageID int64, resultCode string, now time.Time) error {
	if task == nil || strings.TrimSpace(task.AnswerRequirementsJSON) == "" {
		return nil
	}
	message := repositories.MessageRepository.GetInTenant(db, messageID, task.TenantID)
	if message == nil || message.ConversationID != task.ConversationID || message.SessionNo != task.SessionNo ||
		message.AIReplyTurnID != task.TurnID || message.SenderType != enums.IMSenderTypeAI {
		return fmt.Errorf("AI reply task %s coverage message evidence is invalid", task.TaskKey)
	}
	requirements, err := contracts.DecodeAnswerRequirementSetV1([]byte(task.AnswerRequirementsJSON))
	if err != nil {
		return fmt.Errorf("AI reply task %s answer requirements are invalid: %w", task.TaskKey, err)
	}
	if err := contracts.ValidateAnswerRequirementBindingV1(
		requirements, task.TaskKey, task.SourceMessageID, task.SourceSpanStart, task.SourceSpanEnd,
	); err != nil {
		return fmt.Errorf("AI reply task %s answer requirements do not match task: %w", task.TaskKey, err)
	}
	states := make([]contracts.RequirementStateItemV1, 0, len(requirements.Requirements))
	for _, requirement := range requirements.Requirements {
		if !requirement.Required {
			continue
		}
		states = append(states, contracts.RequirementStateItemV1{
			Key: requirement.Key, Outcome: "covered",
			EvidenceRef: fmt.Sprintf("message:%d", message.ID), ErrorCode: strings.TrimSpace(resultCode),
		})
	}
	return s.AttachRequirementOutcomeDB(db, task.TenantID, task.TurnID, task.TaskKey, states, now)
}

func (s *aiReplyTurnTaskService) attachRequirementTaskOutcomeDB(
	db *gorm.DB,
	task *models.AIReplyTurnTask,
	outcome, evidenceRef, resultCode string,
	now time.Time,
) error {
	if task == nil || strings.TrimSpace(task.AnswerRequirementsJSON) == "" {
		return nil
	}
	requirements, err := contracts.DecodeAnswerRequirementSetV1([]byte(task.AnswerRequirementsJSON))
	if err != nil {
		return fmt.Errorf("AI reply task %s answer requirements are invalid: %w", task.TaskKey, err)
	}
	if err := contracts.ValidateAnswerRequirementBindingV1(
		requirements, task.TaskKey, task.SourceMessageID, task.SourceSpanStart, task.SourceSpanEnd,
	); err != nil {
		return fmt.Errorf("AI reply task %s answer requirements do not match task: %w", task.TaskKey, err)
	}
	states := make([]contracts.RequirementStateItemV1, 0, len(requirements.Requirements))
	for _, requirement := range requirements.Requirements {
		if !requirement.Required {
			continue
		}
		states = append(states, contracts.RequirementStateItemV1{
			Key: requirement.Key, Outcome: strings.TrimSpace(outcome),
			EvidenceRef: strings.TrimSpace(evidenceRef), ErrorCode: strings.TrimSpace(resultCode),
		})
	}
	return s.AttachRequirementOutcomeDB(db, task.TenantID, task.TurnID, task.TaskKey, states, now)
}

// resolveCoverageDependentsDB closes the duplicate dependency from the
// canonical task's durable outcome. It never infers completion from an
// in-memory model result.
func (s *aiReplyTurnTaskService) resolveCoverageDependentsDB(
	db *gorm.DB,
	canonical *models.AIReplyTurnTask,
	now time.Time,
) error {
	if db == nil || canonical == nil || canonical.ID <= 0 || canonical.TenantID <= 0 || canonical.TurnID <= 0 {
		return nil
	}
	dependents, err := repositories.AIReplyTurnTaskRepository.FindCoverageDependentsForUpdateInTenant(
		db, canonical.TenantID, canonical.TurnID, canonical.ID,
	)
	if err != nil {
		return err
	}
	for index := range dependents {
		dependent := &dependents[index]
		switch canonical.Status {
		case enums.AIReplyTurnTaskStatusCommitted, enums.AIReplyTurnTaskStatusDelivered, enums.AIReplyTurnTaskStatusCovered:
			if canonical.CommittedMessageID <= 0 {
				continue
			}
			if err := s.markCoverageDependentCoveredDB(
				db, canonical, dependent, canonical.CommittedMessageID, "covered_by_committed_task", now,
			); err != nil {
				return err
			}
		case enums.AIReplyTurnTaskStatusHandoff:
			if err := s.markCoverageDependentHandoffDB(db, canonical, dependent, now); err != nil {
				return err
			}
		case enums.AIReplyTurnTaskStatusFailed:
			if err := s.attachRequirementTaskOutcomeDB(
				db, dependent, "failed_terminal", fmt.Sprintf("task:%d", canonical.ID),
				"covered_by_canonical_failure", now,
			); err != nil {
				return err
			}
			if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, dependent.ID, dependent.TenantID, map[string]any{
				"stage":              enums.AIReplyTurnTaskStageComplete,
				"status":             enums.AIReplyTurnTaskStatusFailed,
				"covered_by_task_id": canonical.ID,
				"result_code":        "covered_by_canonical_failure",
				"failure_class":      canonical.FailureClass,
				"claimed_by_job_id":  0,
				"claimed_version":    0,
				"completed_at":       now,
				"next_retry_at":      nil,
				"updated_at":         now,
				"update_user_name":   "ai_reply_task_coverage",
			}); err != nil {
				return err
			}
		case enums.AIReplyTurnTaskStatusSkipped, enums.AIReplyTurnTaskStatusSuperseded:
			outcome := "skipped_policy"
			if canonical.Status == enums.AIReplyTurnTaskStatusSuperseded {
				outcome = "superseded"
			}
			if err := s.attachRequirementTaskOutcomeDB(
				db, dependent, outcome, fmt.Sprintf("task:%d", canonical.ID), "covered_by_canonical_"+string(canonical.Status), now,
			); err != nil {
				return err
			}
			if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, dependent.ID, dependent.TenantID, map[string]any{
				"stage":             enums.AIReplyTurnTaskStageComplete,
				"status":            canonical.Status,
				"result_code":       "covered_by_canonical_" + string(canonical.Status),
				"claimed_by_job_id": 0,
				"claimed_version":   0,
				"completed_at":      now,
				"next_retry_at":     nil,
				"updated_at":        now,
				"update_user_name":  "ai_reply_task_coverage",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecordResolvedCoverageDB 契约 10.7：与 Task 创建同事务写持久覆盖标签。
func (s *aiReplyTurnTaskService) RecordResolvedCoverageDB(
	db *gorm.DB, job *models.JobRef, turn *models.AIReplyTurn, tasks []models.AIReplyTurnTask,
	intentCoverage []contracts.ResolvedCoverageItemV1, now time.Time,
) error {
	if db == nil || job == nil || job.ID <= 0 || turn == nil || (len(tasks) == 0 && len(intentCoverage) == 0) {
		return nil
	}
	coverage := contracts.ResolvedTurnCoverageV1{
		SchemaVersion: contracts.ResolvedTurnCoverageV1SchemaVersion,
		TurnID:        turn.ID, TurnVersion: turn.Version, Items: []contracts.ResolvedCoverageItemV1{},
	}
	current := repositories.AIReplyJobRepository.GetInTenant(db, job.ID, job.TenantID)
	if current != nil && strings.TrimSpace(current.ResolvedCoverageJSON) != "" {
		previous, decodeErr := contracts.DecodeResolvedTurnCoverageV1([]byte(current.ResolvedCoverageJSON))
		if decodeErr == nil && previous.TurnID == turn.ID && previous.TurnVersion <= turn.Version {
			coverage.Items = append(coverage.Items, previous.Items...)
		} else {
			slog.Warn("AI reply resolved coverage history ignored",
				"tenant_id", job.TenantID, "job_id", job.ID, "turn_id", turn.ID, "error", decodeErr)
		}
	}
	byIdentity := make(map[string]contracts.ResolvedCoverageItemV1, len(coverage.Items)+len(tasks)+len(intentCoverage))
	for _, item := range coverage.Items {
		byIdentity[resolvedCoverageIdentity(item)] = item
	}
	for _, item := range intentCoverage {
		if item.MessageID <= 0 {
			continue
		}
		byIdentity[resolvedCoverageIdentity(item)] = item
	}
	for _, task := range tasks {
		hash := task.CanonicalQuestionHash
		if hash == "" {
			hash = task.QuestionFingerprint
		}
		status := "scheduled"
		reason := ""
		switch task.Status {
		case enums.AIReplyTurnTaskStatusWaitingCoverage:
			status = "waiting"
			reason = "waiting_for_canonical_task"
		case enums.AIReplyTurnTaskStatusCovered, enums.AIReplyTurnTaskStatusDelivered, enums.AIReplyTurnTaskStatusCommitted:
			status = "covered"
			reason = string(task.Status)
		case enums.AIReplyTurnTaskStatusHandoff:
			status = "routed"
			reason = string(task.Status)
		case enums.AIReplyTurnTaskStatusHandoffPending:
			status = "handoff_pending"
			reason = string(task.Status)
		case enums.AIReplyTurnTaskStatusSkipped:
			status = "skipped"
			reason = string(task.Status)
		case enums.AIReplyTurnTaskStatusSuperseded:
			status = "superseded"
			reason = string(task.Status)
		case enums.AIReplyTurnTaskStatusFailed:
			status = "failed"
			reason = string(task.Status)
		}
		item := contracts.ResolvedCoverageItemV1{
			MessageID: task.SourceMessageID, CanonicalHash: hash, TaskID: task.ID, TaskKey: task.TaskKey,
			Status: status, CoveredByTaskID: task.CoveredByTaskID, ReasonCode: reason,
		}
		byIdentity[resolvedCoverageIdentity(item)] = item
	}
	coverage.Items = coverage.Items[:0]
	for _, item := range byIdentity {
		coverage.Items = append(coverage.Items, item)
	}
	sort.SliceStable(coverage.Items, func(i, j int) bool {
		if coverage.Items[i].MessageID != coverage.Items[j].MessageID {
			return coverage.Items[i].MessageID < coverage.Items[j].MessageID
		}
		if coverage.Items[i].TaskID != coverage.Items[j].TaskID {
			return coverage.Items[i].TaskID < coverage.Items[j].TaskID
		}
		return coverage.Items[i].TaskKey < coverage.Items[j].TaskKey
	})
	raw, err := contracts.MarshalResolvedTurnCoverageV1(coverage)
	if err != nil {
		return fmt.Errorf("encode resolved_turn_coverage.v1: %w", err)
	}
	sum := sha256.Sum256(raw)
	return db.Model(&models.AIReplyJob{}).Where("id = ? AND tenant_id = ?", job.ID, job.TenantID).Updates(map[string]any{
		"resolved_coverage_json":        string(raw),
		"resolved_coverage_fingerprint": hex.EncodeToString(sum[:]),
		"updated_at":                    now, "update_user_name": "ai_reply_coverage",
	}).Error
}

func resolvedCoverageIdentity(item contracts.ResolvedCoverageItemV1) string {
	if item.TaskID > 0 || strings.TrimSpace(item.TaskKey) != "" {
		return fmt.Sprintf("task:%d:%d:%s", item.MessageID, item.TaskID, strings.TrimSpace(item.TaskKey))
	}
	return fmt.Sprintf("message:%d", item.MessageID)
}

// AIReplyTurnTaskGroupBinding 是文档 §16.5 的绑定输入。
type AIReplyTurnTaskGroupBinding struct {
	TaskKey        string
	AnswerGroupKey string
}

// BindAnswerGroupsDB 按文档 §8.1 的事务与 CAS 条件，在 Generate 前把
// BuildReplyPlanV4 产出的真实 groupKey 持久化到已 claim 的 Task。
// 只更新 answer_group_key，不改 status/committed/covered 等完成证据。
func (s *aiReplyTurnTaskService) BindAnswerGroupsDB(
	db *gorm.DB,
	turn *models.AIReplyTurn,
	jobID int64,
	bindings []AIReplyTurnTaskGroupBinding,
	now time.Time,
) error {
	if db == nil || turn == nil || turn.ID <= 0 || turn.TenantID <= 0 || jobID <= 0 {
		return errorsx.InvalidParam("AI 回复分组绑定缺少范围")
	}
	if len(bindings) == 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		locked, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, turn.ID, turn.TenantID)
		if err != nil {
			return err
		}
		if locked == nil || locked.ConversationID != turn.ConversationID ||
			locked.SessionNo != turn.SessionNo || locked.Version != turn.Version {
			return errorsx.InvalidParam("AI 回复分组绑定轮次已过期")
		}
		updated := 0
		for _, binding := range bindings {
			taskKey := strings.TrimSpace(binding.TaskKey)
			groupKey := strings.TrimSpace(binding.AnswerGroupKey)
			if taskKey == "" || groupKey == "" || len(groupKey) > 128 {
				return errorsx.InvalidParam("AI 回复分组绑定 key 无效")
			}
			task, err := repositories.AIReplyTurnTaskRepository.GetForUpdateByKeyInTenant(ctx.Tx, turn.TenantID, turn.ID, taskKey)
			if err != nil {
				return err
			}
			if task == nil || task.ClaimedByJobID != jobID || task.ClaimedVersion != locked.Version {
				return errorsx.InvalidParam("AI 回复分组绑定任务未由当前 Job 领取")
			}
			if aiReplyTurnTaskTerminal(task.Status) {
				continue // 已完成任务不得被改写
			}
			if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(ctx.Tx, task.ID, task.TenantID, map[string]any{
				"answer_group_key": groupKey,
				"updated_at":       now,
				"update_user_name": "ai_reply_group_bind",
			}); err != nil {
				return err
			}
			updated++
		}
		if updated == 0 {
			return errorsx.InvalidParam("AI 回复分组绑定未更新任何任务")
		}
		return nil
	})
}
