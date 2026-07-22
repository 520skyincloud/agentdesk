package services

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	customerTagRelationActive   = "active"
	customerTagRelationInactive = "inactive"
	customerTagSourceAI         = "ai"
	customerTagSourceManual     = "manual"
	maxActiveCustomerTags       = 20
)

var CustomerTagService = newCustomerTagService()

type customerTagService struct{}

type CustomerTagOperation struct {
	Op                 string
	TagID              int64
	Replaces           []int64
	Confidence         float64
	EvidenceMessageIDs []int64
}

type ReplyTagCandidate struct {
	TagID           int64
	SemanticKey     string
	Name            string
	ApplicableScene string
	ConflictGroup   string
	ManualProtected bool
	Source          string
	Confidence      float64
	LastMatchedAt   *time.Time
	SortNo          int
}

func newCustomerTagService() *customerTagService {
	return &customerTagService{}
}

func (s *customerTagService) ListForConversation(conversationID int64) []response.CustomerTagResponse {
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil || scope.Relation == nil {
		return nil
	}
	relations := repositories.CustomerTagRelationRepository.FindActiveByRelationID(sqls.DB(), scope.Relation.ID)
	ret := make([]response.CustomerTagResponse, 0, len(relations))
	for _, relation := range relations {
		tag := repositories.TagRepository.Get(sqls.DB(), relation.TagID)
		if tag == nil || tag.Status != enums.StatusOk {
			continue
		}
		ret = append(ret, response.CustomerTagResponse{
			ID: relation.ID, TagID: relation.TagID, Name: utils.RepairMojibakeText(tag.Name),
			Source: relation.Source, Confidence: relation.Confidence, EvidenceCount: relation.EvidenceCount,
			ManualProtected: relation.ManualProtected, UpdatedAt: utils.FormatTime(relation.UpdatedAt),
		})
	}
	return ret
}

func (s *customerTagService) ListAllowedTags(companyID int64) []models.Tag {
	cnd := sqls.NewCnd().
		Eq("status", enums.StatusOk).
		Eq("ai_enabled", true).
		Where("parent_id <> ?", 0).
		Eq("merged_into_tag_id", 0).
		Asc("sort_no").
		Asc("id")
	if companyID > 0 {
		cnd.Where("company_id = 0 OR company_id = ?", companyID)
	} else {
		cnd.Eq("company_id", 0)
	}
	return repositories.TagRepository.Find(sqls.DB(), cnd)
}

func (s *customerTagService) ListOptionsForConversation(conversationID int64, operator *dto.AuthPrincipal) ([]models.Tag, error) {
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return nil, err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return nil, err
	}
	return repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("status", enums.StatusOk).
		Eq("merged_into_tag_id", 0).
		Where("company_id = 0 OR company_id = ?", scope.CompanyID).
		Asc("sort_no").Asc("id")), nil
}

func (s *customerTagService) ListChangeLogsForConversation(conversationID int64, page, limit int, operator *dto.AuthPrincipal) ([]response.CustomerTagChangeLogResponse, *sqls.Paging, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return nil, nil, err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return nil, nil, err
	}
	if scope.Relation == nil {
		return []response.CustomerTagChangeLogResponse{}, &sqls.Paging{Page: page, Limit: limit}, nil
	}
	list, paging, err := repositories.CustomerTagChangeLogRepository.FindPageByRelationID(sqls.DB(), scope.Relation.ID, page, limit)
	if err != nil {
		return nil, nil, err
	}
	tagNames := make(map[int64]string)
	nameOf := func(tagID int64) string {
		if tagID <= 0 {
			return ""
		}
		if name, ok := tagNames[tagID]; ok {
			return name
		}
		name := ""
		if tag := repositories.TagRepository.Get(sqls.DB(), tagID); tag != nil {
			name = utils.RepairMojibakeText(tag.Name)
		}
		tagNames[tagID] = name
		return name
	}
	ret := make([]response.CustomerTagChangeLogResponse, 0, len(list))
	for i := range list {
		item := &list[i]
		evidence := make([]int64, 0)
		_ = json.Unmarshal([]byte(item.EvidenceMessageIDs), &evidence)
		if evidence == nil {
			evidence = make([]int64, 0)
		}
		ret = append(ret, response.CustomerTagChangeLogResponse{
			ID: item.ID, Action: item.Action,
			OldTagID: item.OldTagID, OldTagName: nameOf(item.OldTagID),
			NewTagID: item.NewTagID, NewTagName: nameOf(item.NewTagID),
			EvidenceMessageIDs: evidence, Source: item.Source, Confidence: item.Confidence,
			OperatorType: item.OperatorType, OperatorID: item.OperatorID,
			OperatorName: item.OperatorName, CreatedAt: utils.FormatTime(item.CreatedAt),
		})
	}
	return ret, paging, nil
}

func (s *customerTagService) SelectReplyTagCandidates(conversationID int64, orderedScenes []string, currentText string) ([]ReplyTagCandidate, error) {
	if len(orderedScenes) == 0 || strings.TrimSpace(currentText) == "" {
		return nil, nil
	}
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return nil, err
	}
	if scope.Relation == nil {
		return nil, nil
	}
	sceneOrder := make(map[string]int, len(orderedScenes))
	for _, scene := range orderedScenes {
		scene = strings.TrimSpace(scene)
		if _, valid := replyTagScenes[scene]; !valid || scene == "customer_profile" {
			continue
		}
		if _, exists := sceneOrder[scene]; !exists {
			sceneOrder[scene] = len(sceneOrder)
		}
	}
	if len(sceneOrder) == 0 {
		return nil, nil
	}
	tags, err := repositories.TagRepository.FindEffectiveLeavesByCompany(sqls.DB(), scope.CompanyID)
	if err != nil {
		return nil, err
	}
	tagByID := make(map[int64]*models.Tag, len(tags))
	mentionedTagIDs := make(map[int64]struct{})
	mentionedConflictGroups := make(map[string]struct{})
	for i := range tags {
		tag := &tags[i]
		tagByID[tag.ID] = tag
		if tagMentionedInCurrentText(currentText, tag) {
			mentionedTagIDs[tag.ID] = struct{}{}
			if group := strings.TrimSpace(tag.ConflictGroup); group != "" {
				mentionedConflictGroups[group] = struct{}{}
			}
		}
	}
	relations, err := repositories.CustomerTagRelationRepository.FindActiveByRelationIDWithError(sqls.DB(), scope.Relation.ID)
	if err != nil {
		return nil, err
	}
	candidates := make([]ReplyTagCandidate, 0, len(relations))
	for i := range relations {
		relation := &relations[i]
		tag := tagByID[relation.TagID]
		if tag == nil || !tag.ReplyEnabled {
			continue
		}
		if _, ok := sceneOrder[strings.TrimSpace(tag.ApplicableScene)]; !ok {
			continue
		}
		if _, mentioned := mentionedTagIDs[tag.ID]; mentioned {
			continue
		}
		if group := strings.TrimSpace(tag.ConflictGroup); group != "" {
			if _, contradicted := mentionedConflictGroups[group]; contradicted {
				continue
			}
		}
		candidates = append(candidates, ReplyTagCandidate{
			TagID: tag.ID, SemanticKey: tag.SemanticKey, Name: utils.RepairMojibakeText(tag.Name),
			ApplicableScene: tag.ApplicableScene, ConflictGroup: tag.ConflictGroup,
			ManualProtected: relation.ManualProtected, Source: relation.Source,
			Confidence: relation.Confidence, LastMatchedAt: relation.LastMatchedAt, SortNo: tag.SortNo,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if sceneOrder[left.ApplicableScene] != sceneOrder[right.ApplicableScene] {
			return sceneOrder[left.ApplicableScene] < sceneOrder[right.ApplicableScene]
		}
		if left.ManualProtected != right.ManualProtected {
			return left.ManualProtected
		}
		leftManual, rightManual := left.Source == customerTagSourceManual, right.Source == customerTagSourceManual
		if leftManual != rightManual {
			return leftManual
		}
		if left.Confidence != right.Confidence {
			return left.Confidence > right.Confidence
		}
		if !sameReplyTagMatchTime(left.LastMatchedAt, right.LastMatchedAt) {
			return replyTagMatchTimeAfter(left.LastMatchedAt, right.LastMatchedAt)
		}
		if left.SortNo != right.SortNo {
			return left.SortNo < right.SortNo
		}
		return left.TagID < right.TagID
	})
	ret := make([]ReplyTagCandidate, 0, 2)
	usedConflictGroups := make(map[string]struct{})
	for _, candidate := range candidates {
		group := strings.TrimSpace(candidate.ConflictGroup)
		if group != "" {
			if _, used := usedConflictGroups[group]; used {
				continue
			}
			usedConflictGroups[group] = struct{}{}
		}
		ret = append(ret, candidate)
		if len(ret) == 2 {
			break
		}
	}
	return ret, nil
}

func tagMentionedInCurrentText(text string, tag *models.Tag) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || tag == nil {
		return false
	}
	terms := append([]string{tag.Name}, strings.FieldsFunc(tag.Aliases, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n'
	})...)
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func sameReplyTagMatchTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func replyTagMatchTimeAfter(left, right *time.Time) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	return left.After(*right)
}

func (s *customerTagService) ManualAdd(req request.AddCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.resolveConversationScope(sqls.DB(), req.ConversationID, true)
	if err != nil {
		return err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return err
	}
	tag, err := s.validateTagForScope(sqls.DB(), req.TagID, scope.CompanyID, false)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(tx.Tx, scope.Relation.ID, tag.ID)
		conflicts := s.findActiveConflictingRelations(tx.Tx, scope.Relation.ID, tag)
		for _, conflict := range conflicts {
			if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, conflict.ID, map[string]any{
				"source": customerTagSourceManual, "relation_status": customerTagRelationInactive,
				"manual_protected": true, "inactivated_at": now, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		wasActive := current != nil && current.RelationStatus == customerTagRelationActive
		wasManualProtected := wasActive && current.ManualProtected && current.Source == customerTagSourceManual
		if !wasActive && repositories.CustomerTagRelationRepository.CountActiveByRelationID(tx.Tx, scope.Relation.ID) >= maxActiveCustomerTags {
			return errorsx.InvalidParam("每位客户最多保留 20 个有效标签")
		}
		if current != nil {
			if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, current.ID, map[string]any{
				"source": customerTagSourceManual, "relation_status": customerTagRelationActive,
				"confidence": 1, "manual_protected": true, "last_matched_at": now,
				"inactivated_at": nil, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		} else {
			item := &models.CustomerTagRelation{
				CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
				StoreCustomerRelationID: scope.Relation.ID, TagID: tag.ID,
				Source: customerTagSourceManual, RelationStatus: customerTagRelationActive,
				Confidence: 1, EvidenceCount: 1, FirstMatchedAt: &now, LastMatchedAt: &now,
				ManualProtected: true, AuditFields: utils.BuildAuditFields(operator),
			}
			if err := repositories.CustomerTagRelationRepository.Create(tx.Tx, item); err != nil {
				return err
			}
		}
		if len(conflicts) > 0 {
			for _, conflict := range conflicts {
				if err := s.writeChangeLog(tx.Tx, scope, 0, "replace", conflict.TagID, tag.ID, nil, customerTagSourceManual, 1, operator); err != nil {
					return err
				}
			}
			return nil
		}
		if wasManualProtected {
			return nil
		}
		if !wasActive || current.Source != customerTagSourceManual || !current.ManualProtected {
			return s.writeChangeLog(tx.Tx, scope, 0, "add", 0, tag.ID, nil, customerTagSourceManual, 1, operator)
		}
		return nil
	})
}

func (s *customerTagService) ManualRemove(req request.RemoveCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.resolveConversationScope(sqls.DB(), req.ConversationID, true)
	if err != nil {
		return err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return err
	}
	current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(sqls.DB(), scope.Relation.ID, req.TagID)
	if current == nil || current.RelationStatus != customerTagRelationActive {
		return nil
	}
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, current.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationInactive,
			"manual_protected": true, "inactivated_at": now, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.writeChangeLog(tx.Tx, scope, 0, "remove", current.TagID, 0, nil, customerTagSourceManual, 1, operator)
	})
}

func (s *customerTagService) ManualReplace(req request.ReplaceCustomerTagRequest, operator *dto.AuthPrincipal) error {
	if req.OldTagID == req.NewTagID {
		return nil
	}
	scope, err := s.resolveConversationScope(sqls.DB(), req.ConversationID, true)
	if err != nil {
		return err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return err
	}
	newTag, err := s.validateTagForScope(sqls.DB(), req.NewTagID, scope.CompanyID, false)
	if err != nil {
		return err
	}
	oldTag, err := s.validateTagForScope(sqls.DB(), req.OldTagID, scope.CompanyID, false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(oldTag.ConflictGroup) == "" || oldTag.ConflictGroup != newTag.ConflictGroup {
		return errorsx.InvalidParam("只能替换同一互斥组内的客户标签")
	}
	old := repositories.CustomerTagRelationRepository.GetByRelationAndTag(sqls.DB(), scope.Relation.ID, req.OldTagID)
	if old == nil || old.RelationStatus != customerTagRelationActive {
		return errorsx.InvalidParam("待替换的客户标签不存在")
	}
	return s.ManualAdd(request.AddCustomerTagRequest{ConversationID: req.ConversationID, TagID: req.NewTagID}, operator)
}

func (s *customerTagService) ApplyAI(conversationID int64, runID int64, operations []CustomerTagOperation) (bool, error) {
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, true)
	if err != nil {
		return false, err
	}
	changed := false
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		for _, operation := range operations {
			applied, applyErr := s.applyAIOperation(tx.Tx, scope, runID, operation)
			if applyErr != nil {
				return applyErr
			}
			changed = changed || applied
		}
		return nil
	})
	return changed, err
}

func (s *customerTagService) applyAIOperation(db *gorm.DB, scope *customerTagScope, runID int64, operation CustomerTagOperation) (bool, error) {
	tag, err := s.validateTagForScope(db, operation.TagID, scope.CompanyID, true)
	if err != nil {
		return false, err
	}
	now := time.Now()
	current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, tag.ID)
	switch operation.Op {
	case "add", "refresh":
		if current != nil && current.ManualProtected {
			return false, nil
		}
		if len(s.findActiveConflictingRelations(db, scope.Relation.ID, tag)) > 0 {
			return false, nil
		}
		if current != nil {
			if current.RelationStatus != customerTagRelationActive && repositories.CustomerTagRelationRepository.CountActiveByRelationID(db, scope.Relation.ID) >= maxActiveCustomerTags {
				return false, nil
			}
			if err := repositories.CustomerTagRelationRepository.Updates(db, current.ID, map[string]any{
				"source": customerTagSourceAI, "relation_status": customerTagRelationActive,
				"confidence": operation.Confidence, "evidence_count": current.EvidenceCount + 1,
				"last_matched_at": now, "last_evolution_run_id": runID,
				"inactivated_at": nil, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return false, err
			}
			if err := s.writeChangeLog(db, scope, runID, "refresh", tag.ID, tag.ID, operation.EvidenceMessageIDs, customerTagSourceAI, operation.Confidence, nil); err != nil {
				return false, err
			}
			return true, nil
		}
		if repositories.CustomerTagRelationRepository.CountActiveByRelationID(db, scope.Relation.ID) >= maxActiveCustomerTags {
			return false, nil
		}
		item := &models.CustomerTagRelation{
			CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
			StoreCustomerRelationID: scope.Relation.ID, TagID: tag.ID,
			Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
			Confidence: operation.Confidence, EvidenceCount: 1,
			FirstMatchedAt: &now, LastMatchedAt: &now, LastEvolutionRunID: runID,
			AuditFields: utils.BuildAuditFields(nil),
		}
		if err := repositories.CustomerTagRelationRepository.Create(db, item); err != nil {
			return false, err
		}
		if err := s.writeChangeLog(db, scope, runID, "add", 0, tag.ID, operation.EvidenceMessageIDs, customerTagSourceAI, operation.Confidence, nil); err != nil {
			return false, err
		}
		return true, nil
	case "remove":
		if current == nil || current.RelationStatus != customerTagRelationActive || current.ManualProtected {
			return false, nil
		}
		if err := repositories.CustomerTagRelationRepository.Updates(db, current.ID, map[string]any{
			"relation_status": customerTagRelationInactive, "inactivated_at": now,
			"last_evolution_run_id": runID, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}); err != nil {
			return false, err
		}
		if err := s.writeChangeLog(db, scope, runID, "remove", tag.ID, 0, operation.EvidenceMessageIDs, customerTagSourceAI, operation.Confidence, nil); err != nil {
			return false, err
		}
		return true, nil
	case "replace":
		if current != nil && current.ManualProtected {
			return false, nil
		}
		replaceIDs := make(map[int64]struct{}, len(operation.Replaces))
		for _, oldTagID := range operation.Replaces {
			if oldTagID > 0 {
				replaceIDs[oldTagID] = struct{}{}
			}
		}
		conflicts := s.findActiveConflictingRelations(db, scope.Relation.ID, tag)
		oldRelations := make([]*models.CustomerTagRelation, 0, len(conflicts))
		for _, oldRelation := range conflicts {
			if oldRelation.ManualProtected {
				return false, nil
			}
			if _, ok := replaceIDs[oldRelation.TagID]; !ok {
				return false, nil
			}
			oldRelations = append(oldRelations, oldRelation)
		}
		if len(oldRelations) == 0 {
			return false, nil
		}
		if len(replaceIDs) != len(oldRelations) {
			return false, nil
		}
		if current == nil {
			current = &models.CustomerTagRelation{
				CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
				StoreCustomerRelationID: scope.Relation.ID, TagID: tag.ID,
				Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
				Confidence: operation.Confidence, EvidenceCount: 1,
				FirstMatchedAt: &now, LastMatchedAt: &now, LastEvolutionRunID: runID,
				AuditFields: utils.BuildAuditFields(nil),
			}
			if err := repositories.CustomerTagRelationRepository.Create(db, current); err != nil {
				return false, err
			}
		} else {
			if err := repositories.CustomerTagRelationRepository.Updates(db, current.ID, map[string]any{
				"source": customerTagSourceAI, "relation_status": customerTagRelationActive,
				"confidence": operation.Confidence, "evidence_count": current.EvidenceCount + 1,
				"last_matched_at": now, "last_evolution_run_id": runID,
				"inactivated_at": nil, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return false, err
			}
		}
		for _, oldRelation := range oldRelations {
			if err := repositories.CustomerTagRelationRepository.Updates(db, oldRelation.ID, map[string]any{
				"relation_status": customerTagRelationInactive, "inactivated_at": now,
				"last_evolution_run_id": runID, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return false, err
			}
			if err := s.writeChangeLog(db, scope, runID, "replace", oldRelation.TagID, tag.ID, operation.EvidenceMessageIDs, customerTagSourceAI, operation.Confidence, nil); err != nil {
				return false, err
			}
		}
		return true, nil
	default:
		return false, errorsx.InvalidParam("客户标签操作不合法")
	}
}

type customerTagScope struct {
	Conversation *models.Conversation
	Route        *models.ConversationRouteState
	Relation     *models.StoreCustomerRelation
	CompanyID    int64
	StoreID      int64
}

func (s *customerTagService) resolveConversationScope(db *gorm.DB, conversationID int64, createRelation bool) (*customerTagScope, error) {
	conversation := repositories.ConversationRepository.Get(db, conversationID)
	if conversation == nil || conversation.CustomerID <= 0 {
		return nil, errorsx.InvalidParam("会话尚未关联有效客户")
	}
	route := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", conversationID)
	if route == nil || route.StoreID <= 0 {
		return nil, errorsx.InvalidParam("会话尚未绑定门店")
	}
	store := repositories.StoreRepository.Get(db, route.StoreID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	relation := repositories.StoreCustomerRelationRepository.Take(db, "customer_id = ? AND store_id = ?", conversation.CustomerID, route.StoreID)
	if relation == nil && createRelation {
		now := time.Now()
		relation = &models.StoreCustomerRelation{
			CustomerID: conversation.CustomerID, StoreID: route.StoreID,
			WxWorkInstanceID: route.WxWorkInstanceID, LastConversationID: conversation.ID,
			LastActiveAt: &now, VisitCount: 1, Status: enums.StatusOk,
			AuditFields: utils.BuildAuditFields(nil),
		}
		if err := repositories.StoreCustomerRelationRepository.CreateIfAbsent(db, relation); err != nil {
			return nil, err
		}
		relation = repositories.StoreCustomerRelationRepository.Take(db,
			"customer_id = ? AND store_id = ?", conversation.CustomerID, route.StoreID)
		if relation == nil {
			return nil, errorsx.BusinessError(2005, "客户门店关系创建失败")
		}
	}
	return &customerTagScope{
		Conversation: conversation, Route: route, Relation: relation,
		CompanyID: store.CompanyID, StoreID: route.StoreID,
	}, nil
}

func (s *customerTagService) validateTagForScope(db *gorm.DB, tagID, companyID int64, requireAI bool) (*models.Tag, error) {
	tag := repositories.TagRepository.Get(db, tagID)
	if tag == nil || tag.Status != enums.StatusOk || tag.ParentID == 0 || tag.MergedIntoTagID > 0 {
		return nil, errorsx.InvalidParam("标签不存在或不可用")
	}
	if tag.CompanyID > 0 && tag.CompanyID != companyID {
		return nil, errorsx.Forbidden("标签不属于当前公司")
	}
	if requireAI && !tag.AIEnabled {
		return nil, errorsx.InvalidParam("标签未开放给 AI 使用")
	}
	if runeCount := len([]rune(strings.TrimSpace(tag.Name))); runeCount < 1 || runeCount > 5 {
		return nil, errorsx.InvalidParam("标签名称必须为 1～5 个字")
	}
	return tag, nil
}

func (s *customerTagService) requireConversationAccess(scope *customerTagScope, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if scope == nil || scope.Conversation == nil || scope.StoreID <= 0 {
		return errorsx.InvalidParam("会话门店范围不存在")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return nil
	}
	managedScope := AgentTeamScopeService.Resolve(operator)
	if managedScope.Unrestricted || slices.Contains(managedScope.StoreIDs, scope.StoreID) {
		return nil
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) && AgentProfileService.CanServeConversation(operator.UserID, scope.Conversation.ID) {
		return nil
	}
	return errorsx.Forbidden("无权操作该门店客户标签")
}

func (s *customerTagService) requireStoreAccess(storeID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if storeID <= 0 {
		return errorsx.InvalidParam("门店范围不存在")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return nil
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted || slices.Contains(scope.StoreIDs, storeID) {
		return nil
	}
	return errorsx.Forbidden("无权操作该门店客户标签")
}

func (s *customerTagService) writeChangeLog(db *gorm.DB, scope *customerTagScope, runID int64, action string, oldTagID, newTagID int64, evidence []int64, source string, confidence float64, operator *dto.AuthPrincipal) error {
	operatorType := "system"
	operatorID := constants.SystemAuditUserID
	operatorName := constants.SystemAuditUserName
	if operator != nil {
		operatorType = "user"
		operatorID = operator.UserID
		operatorName = operator.Username
	}
	if evidence == nil {
		evidence = make([]int64, 0)
	}
	rawEvidence, _ := json.Marshal(evidence)
	return repositories.CustomerTagChangeLogRepository.Create(db, &models.CustomerTagChangeLog{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, ConversationID: scope.Conversation.ID,
		EvolutionRunID: runID, Action: action, OldTagID: oldTagID, NewTagID: newTagID,
		EvidenceMessageIDs: string(rawEvidence), Source: source, Confidence: confidence,
		OperatorType: operatorType, OperatorID: operatorID, OperatorName: operatorName, CreatedAt: time.Now(),
	})
}

func (s *customerTagService) findActiveConflictingRelations(db *gorm.DB, relationID int64, target *models.Tag) []*models.CustomerTagRelation {
	if target == nil || strings.TrimSpace(target.ConflictGroup) == "" {
		return nil
	}
	active := repositories.CustomerTagRelationRepository.FindActiveByRelationID(db, relationID)
	ret := make([]*models.CustomerTagRelation, 0, len(active))
	for i := range active {
		item := &active[i]
		if item.TagID == target.ID {
			continue
		}
		existingTag := repositories.TagRepository.Get(db, item.TagID)
		if tagsCanReplace(existingTag, target) {
			ret = append(ret, item)
		}
	}
	return ret
}

func tagsCanReplace(oldTag, newTag *models.Tag) bool {
	if oldTag == nil || newTag == nil {
		return false
	}
	if strings.TrimSpace(oldTag.SemanticKey) != "" && oldTag.SemanticKey == newTag.SemanticKey {
		return true
	}
	return strings.TrimSpace(oldTag.ConflictGroup) != "" && oldTag.ConflictGroup == newTag.ConflictGroup
}
