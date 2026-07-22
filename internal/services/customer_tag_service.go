package services

import (
	"encoding/json"
	"slices"
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

func (s *customerTagService) ManualAdd(req request.AddCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.resolveConversationScope(sqls.DB(), req.ConversationID, true)
	if err != nil {
		return err
	}
	if err := s.requireStoreAccess(scope.StoreID, operator); err != nil {
		return err
	}
	tag, err := s.validateTagForScope(sqls.DB(), req.TagID, scope.CompanyID, false)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		current := repositories.CustomerTagRelationRepository.GetByRelationAndTag(tx.Tx, scope.Relation.ID, tag.ID)
		if current != nil {
			if current.RelationStatus == customerTagRelationActive && current.ManualProtected {
				return nil
			}
			if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, current.ID, map[string]any{
				"source": customerTagSourceManual, "relation_status": customerTagRelationActive,
				"confidence": 1, "manual_protected": true, "last_matched_at": now,
				"inactivated_at": nil, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
			return s.writeChangeLog(tx.Tx, scope, 0, "add", 0, tag.ID, nil, customerTagSourceManual, 1, operator)
		}
		if repositories.CustomerTagRelationRepository.CountActiveByRelationID(tx.Tx, scope.Relation.ID) >= maxActiveCustomerTags {
			return errorsx.InvalidParam("每位客户最多保留 20 个有效标签")
		}
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
		return s.writeChangeLog(tx.Tx, scope, 0, "add", 0, tag.ID, nil, customerTagSourceManual, 1, operator)
	})
}

func (s *customerTagService) ManualRemove(req request.RemoveCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.resolveConversationScope(sqls.DB(), req.ConversationID, true)
	if err != nil {
		return err
	}
	if err := s.requireStoreAccess(scope.StoreID, operator); err != nil {
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
	if err := s.requireStoreAccess(scope.StoreID, operator); err != nil {
		return err
	}
	if _, err := s.validateTagForScope(sqls.DB(), req.NewTagID, scope.CompanyID, false); err != nil {
		return err
	}
	old := repositories.CustomerTagRelationRepository.GetByRelationAndTag(sqls.DB(), scope.Relation.ID, req.OldTagID)
	if old == nil || old.RelationStatus != customerTagRelationActive {
		return errorsx.InvalidParam("待替换的客户标签不存在")
	}
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, old.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationInactive,
			"manual_protected": true, "inactivated_at": now, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		next := repositories.CustomerTagRelationRepository.GetByRelationAndTag(tx.Tx, scope.Relation.ID, req.NewTagID)
		if next == nil {
			next = &models.CustomerTagRelation{
				CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
				StoreCustomerRelationID: scope.Relation.ID, TagID: req.NewTagID,
				Source: customerTagSourceManual, RelationStatus: customerTagRelationActive,
				Confidence: 1, EvidenceCount: 1, FirstMatchedAt: &now, LastMatchedAt: &now,
				ManualProtected: true, AuditFields: utils.BuildAuditFields(operator),
			}
			if err := repositories.CustomerTagRelationRepository.Create(tx.Tx, next); err != nil {
				return err
			}
		} else if err := repositories.CustomerTagRelationRepository.Updates(tx.Tx, next.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationActive,
			"confidence": 1, "manual_protected": true, "last_matched_at": now,
			"inactivated_at": nil, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.writeChangeLog(tx.Tx, scope, 0, "replace", req.OldTagID, req.NewTagID, nil, customerTagSourceManual, 1, operator)
	})
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
		if current != nil {
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
		oldRelations := make([]*models.CustomerTagRelation, 0, len(operation.Replaces))
		for _, oldTagID := range operation.Replaces {
			oldTag, oldErr := s.validateTagForScope(db, oldTagID, scope.CompanyID, true)
			if oldErr != nil || !tagsCanReplace(oldTag, tag) {
				continue
			}
			oldRelation := repositories.CustomerTagRelationRepository.GetByRelationAndTag(db, scope.Relation.ID, oldTagID)
			if oldRelation == nil || oldRelation.RelationStatus != customerTagRelationActive || oldRelation.ManualProtected {
				continue
			}
			oldRelations = append(oldRelations, oldRelation)
		}
		if len(oldRelations) == 0 {
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
	if tag == nil || tag.Status != enums.StatusOk || tag.MergedIntoTagID > 0 {
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

func (s *customerTagService) requireStoreAccess(storeID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		return nil
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if !slices.Contains(scope.StoreIDs, storeID) {
		return errorsx.Forbidden("无权操作该门店客户标签")
	}
	return nil
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
	rawEvidence, _ := json.Marshal(evidence)
	return repositories.CustomerTagChangeLogRepository.Create(db, &models.CustomerTagChangeLog{
		CompanyID: scope.CompanyID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, ConversationID: scope.Conversation.ID,
		EvolutionRunID: runID, Action: action, OldTagID: oldTagID, NewTagID: newTagID,
		EvidenceMessageIDs: string(rawEvidence), Source: source, Confidence: confidence,
		OperatorType: operatorType, OperatorID: operatorID, OperatorName: operatorName, CreatedAt: time.Now(),
	})
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
