package services

import (
	"encoding/json"
	"fmt"
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
	maxActiveCustomerTags       = 6
)

var CustomerTagService = &customerTagService{}

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

type customerTagScope struct {
	Conversation *models.Conversation
	Route        *models.ConversationRouteState
	Relation     *models.StoreCustomerRelation
	TenantID     int64
	StoreID      int64
	ProfileID    int64
}

func (s *customerTagService) ApplyConversationFilter(cnd *sqls.Cnd, tenantID int64, tagIDs []int64) *sqls.Cnd {
	return repositories.CustomerTagRelationRepository.ApplyConversationFilter(cnd, tenantID, uniquePositive(tagIDs))
}

func (s *customerTagService) ListForConversation(conversationID int64) []response.CustomerTagResponse {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil {
		return nil
	}
	return s.ListForConversations([]models.Conversation{*conversation})[conversationID]
}

func (s *customerTagService) ListForConversations(conversations []models.Conversation) map[int64][]response.CustomerTagResponse {
	ret := make(map[int64][]response.CustomerTagResponse, len(conversations))
	byTenant := make(map[int64][]models.Conversation)
	for i := range conversations {
		item := conversations[i]
		if item.ID > 0 && item.TenantID > 0 && item.CustomerID > 0 {
			byTenant[item.TenantID] = append(byTenant[item.TenantID], item)
		}
	}
	for tenantID, tenantConversations := range byTenant {
		s.fillCustomerTagsInTenant(ret, tenantID, tenantConversations)
	}
	return ret
}

func (s *customerTagService) fillCustomerTagsInTenant(ret map[int64][]response.CustomerTagResponse, tenantID int64, conversations []models.Conversation) {
	conversationIDs := make([]int64, 0, len(conversations))
	customerIDs := make([]int64, 0, len(conversations))
	conversationByID := make(map[int64]models.Conversation, len(conversations))
	for i := range conversations {
		conversationIDs = append(conversationIDs, conversations[i].ID)
		customerIDs = append(customerIDs, conversations[i].CustomerID)
		conversationByID[conversations[i].ID] = conversations[i]
	}
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("conversation_id", uniquePositive(conversationIDs)).
		Where("store_id > ?", 0))
	storeIDs := make([]int64, 0, len(routes))
	routeByConversationID := make(map[int64]models.ConversationRouteState, len(routes))
	for i := range routes {
		conversation, ok := conversationByID[routes[i].ConversationID]
		if !ok || conversation.TenantID != routes[i].TenantID {
			continue
		}
		routeByConversationID[routes[i].ConversationID] = routes[i]
		storeIDs = append(storeIDs, routes[i].StoreID)
	}
	if len(routeByConversationID) == 0 {
		return
	}
	relations := repositories.StoreCustomerRelationRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("customer_id", uniquePositive(customerIDs)).
		In("store_id", uniquePositive(storeIDs)).
		Eq("status", enums.StatusOk))
	type relationKey struct {
		customerID int64
		storeID    int64
	}
	relationByKey := make(map[relationKey]models.StoreCustomerRelation, len(relations))
	relationIDs := make([]int64, 0, len(relations))
	for i := range relations {
		relationByKey[relationKey{customerID: relations[i].CustomerID, storeID: relations[i].StoreID}] = relations[i]
		relationIDs = append(relationIDs, relations[i].ID)
	}
	if len(relationIDs) == 0 {
		return
	}
	tagRelations, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelations(sqls.DB(), tenantID, relationIDs)
	if err != nil || len(tagRelations) == 0 {
		return
	}
	tagIDs := make([]int64, 0, len(tagRelations))
	relationsByStoreRelationID := make(map[int64][]models.CustomerTagRelation)
	for i := range tagRelations {
		tagIDs = append(tagIDs, tagRelations[i].TagID)
		relationsByStoreRelationID[tagRelations[i].StoreCustomerRelationID] = append(relationsByStoreRelationID[tagRelations[i].StoreCustomerRelationID], tagRelations[i])
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), tenantID)
	if tenant == nil || tenant.IntentProfileID <= 0 {
		return
	}
	tags := repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("intent_profile_id", tenant.IntentProfileID).
		Eq("system_defined", true).
		Where("template_definition_id IS NOT NULL").
		Eq("status", enums.StatusOk).
		In("id", uniquePositive(tagIDs)))
	tagByID := make(map[int64]*models.Tag, len(tags))
	for i := range tags {
		tagByID[tags[i].ID] = &tags[i]
	}
	for conversationID, conversation := range conversationByID {
		route, ok := routeByConversationID[conversationID]
		if !ok {
			continue
		}
		storeRelation, ok := relationByKey[relationKey{customerID: conversation.CustomerID, storeID: route.StoreID}]
		if !ok {
			continue
		}
		ret[conversationID] = buildCustomerTagResponses(relationsByStoreRelationID[storeRelation.ID], tagByID)
	}
}

func (s *customerTagService) ListByStoreRelations(tenantID int64, storeRelations []models.StoreCustomerRelation) map[int64][]response.CustomerTagResponse {
	ret := make(map[int64][]response.CustomerTagResponse, len(storeRelations))
	tenant := repositories.TenantRepository.Get(sqls.DB(), tenantID)
	if tenant == nil || tenant.IntentProfileID <= 0 {
		return ret
	}
	relationIDs := make([]int64, 0, len(storeRelations))
	for i := range storeRelations {
		if storeRelations[i].TenantID == tenantID && storeRelations[i].ID > 0 {
			relationIDs = append(relationIDs, storeRelations[i].ID)
		}
	}
	relations, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelations(sqls.DB(), tenantID, uniquePositive(relationIDs))
	if err != nil || len(relations) == 0 {
		return ret
	}
	tagIDs := make([]int64, 0, len(relations))
	relationsByID := make(map[int64][]models.CustomerTagRelation)
	for i := range relations {
		tagIDs = append(tagIDs, relations[i].TagID)
		relationsByID[relations[i].StoreCustomerRelationID] = append(relationsByID[relations[i].StoreCustomerRelationID], relations[i])
	}
	tags := repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("intent_profile_id", tenant.IntentProfileID).
		Eq("system_defined", true).
		Where("template_definition_id IS NOT NULL").
		Eq("status", enums.StatusOk).
		In("id", uniquePositive(tagIDs)))
	tagByID := make(map[int64]*models.Tag, len(tags))
	for i := range tags {
		tagByID[tags[i].ID] = &tags[i]
	}
	for relationID, items := range relationsByID {
		ret[relationID] = buildCustomerTagResponses(items, tagByID)
	}
	return ret
}

func buildCustomerTagResponses(relations []models.CustomerTagRelation, tagByID map[int64]*models.Tag) []response.CustomerTagResponse {
	ret := make([]response.CustomerTagResponse, 0, len(relations))
	for i := range relations {
		tag := tagByID[relations[i].TagID]
		if tag == nil || tag.Status != enums.StatusOk {
			continue
		}
		name := strings.TrimSpace(tag.DisplayAlias)
		if name == "" {
			name = tag.Name
		}
		ret = append(ret, response.CustomerTagResponse{
			ID: relations[i].ID, TagID: tag.ID,
			Name: utils.RepairMojibakeText(name), StandardName: utils.RepairMojibakeText(tag.Name),
			SemanticKey: tag.SemanticKey, ConflictGroup: tag.ConflictGroup,
			Source: relations[i].Source, Confidence: relations[i].Confidence,
			EvidenceCount: relations[i].EvidenceCount, ManualProtected: relations[i].ManualProtected,
			UpdatedAt: utils.FormatTime(relations[i].UpdatedAt),
		})
	}
	return ret
}

func (s *customerTagService) ListOptionsForConversation(conversationID int64, operator *dto.AuthPrincipal) ([]models.Tag, error) {
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return nil, err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return nil, err
	}
	all := repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", scope.TenantID).
		Eq("intent_profile_id", scope.ProfileID).
		Eq("system_defined", true).
		Where("template_definition_id IS NOT NULL").
		Asc("sort_no").Asc("id"))
	parentIDs := make(map[int64]struct{}, len(all))
	for i := range all {
		if all[i].ParentID > 0 {
			parentIDs[all[i].ParentID] = struct{}{}
		}
	}
	ret := make([]models.Tag, 0, len(all))
	for i := range all {
		if all[i].ParentID == 0 {
			continue
		}
		if _, isCategory := parentIDs[all[i].ID]; isCategory {
			continue
		}
		ret = append(ret, all[i])
	}
	return ret, nil
}

func (s *customerTagService) listAllowedAITags(db *gorm.DB, scope *customerTagScope) ([]models.Tag, error) {
	if scope == nil || scope.TenantID <= 0 || scope.ProfileID <= 0 {
		return nil, errorsx.InvalidParam("客户标签行业范围不存在")
	}
	all, err := repositories.TagRepository.FindByProfileInTenant(db, scope.TenantID, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	parentIDs := make(map[int64]struct{}, len(all))
	for i := range all {
		if all[i].ParentID > 0 {
			parentIDs[all[i].ParentID] = struct{}{}
		}
	}
	ret := make([]models.Tag, 0, len(all))
	for i := range all {
		if all[i].Status != enums.StatusOk || all[i].ParentID == 0 || !all[i].AIEnabled ||
			!all[i].SystemDefined || all[i].TemplateDefinitionID == nil || strings.TrimSpace(all[i].SemanticKey) == "" {
			continue
		}
		if _, isCategory := parentIDs[all[i].ID]; isCategory {
			continue
		}
		ret = append(ret, all[i])
	}
	return ret, nil
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
	list, total, err := repositories.CustomerTagChangeLogRepository.FindPageByStoreRelation(
		sqls.DB(), scope.TenantID, scope.StoreID, scope.Relation.ID, page, limit,
	)
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
		if tag := repositories.TagRepository.GetInTenant(sqls.DB(), tagID, scope.TenantID); tag != nil {
			name = strings.TrimSpace(tag.DisplayAlias)
			if name == "" {
				name = tag.Name
			}
			name = utils.RepairMojibakeText(name)
		}
		tagNames[tagID] = name
		return name
	}
	ret := make([]response.CustomerTagChangeLogResponse, 0, len(list))
	for i := range list {
		evidence := make([]int64, 0)
		_ = json.Unmarshal([]byte(list[i].EvidenceMessageIDs), &evidence)
		if evidence == nil {
			evidence = make([]int64, 0)
		}
		ret = append(ret, response.CustomerTagChangeLogResponse{
			ID: list[i].ID, Action: list[i].Action,
			OldTagID: list[i].OldTagID, OldTagName: nameOf(list[i].OldTagID),
			NewTagID: list[i].NewTagID, NewTagName: nameOf(list[i].NewTagID),
			EvidenceMessageIDs: evidence, Source: list[i].Source, Confidence: list[i].Confidence,
			OperatorType: list[i].OperatorType, OperatorID: list[i].OperatorID,
			OperatorName: list[i].OperatorName, CreatedAt: utils.FormatTime(list[i].CreatedAt),
		})
	}
	return ret, &sqls.Paging{Page: page, Limit: limit, Total: total}, nil
}

func (s *customerTagService) ReconcileStoreRelationTags(
	req request.ReconcileStoreCustomerRelationTagsRequest,
	operator *dto.AuthPrincipal,
) (*models.StoreCustomerTagDecision, error) {
	if !req.Confirmed {
		return nil, errorsx.InvalidParam("请确认门店客户标签处理方案")
	}
	if req.SourceStoreRelationID <= 0 || req.TargetStoreRelationID <= 0 {
		return nil, errorsx.InvalidParam("请选择来源门店和目标门店")
	}
	if req.SourceStoreRelationID == req.TargetStoreRelationID {
		return nil, errorsx.InvalidParam("来源门店和目标门店不能相同")
	}
	if !enums.IsValidStoreCustomerTagReconcileStrategy(req.Strategy) {
		return nil, errorsx.InvalidParam("门店客户标签处理方案不合法")
	}
	if err := s.requireStoreRelationTagReconcileAccess(operator); err != nil {
		return nil, err
	}

	tenantID := operator.ActiveTenantID
	source, err := repositories.StoreCustomerRelationRepository.GetInTenant(sqls.DB(), req.SourceStoreRelationID, tenantID)
	if err != nil {
		return nil, err
	}
	target, err := repositories.StoreCustomerRelationRepository.GetInTenant(sqls.DB(), req.TargetStoreRelationID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateStoreRelationTagReconcilePair(source, target, tenantID); err != nil {
		return nil, err
	}

	unlock := lockCustomerTagStores(tenantID, source.CustomerID, source.StoreID, target.StoreID)
	defer unlock()

	var decision *models.StoreCustomerTagDecision
	targetChanged := false
	eventConversationID := int64(0)
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedSource, lockedTarget, err := lockStoreRelationTagReconcilePair(
			ctx.Tx,
			tenantID,
			req.SourceStoreRelationID,
			req.TargetStoreRelationID,
		)
		if err != nil {
			return err
		}
		if err := validateStoreRelationTagReconcilePair(lockedSource, lockedTarget, tenantID); err != nil {
			return err
		}
		sourceStore := repositories.StoreRepository.GetInTenant(ctx.Tx, lockedSource.StoreID, tenantID)
		targetStore := repositories.StoreRepository.GetInTenant(ctx.Tx, lockedTarget.StoreID, tenantID)
		if sourceStore == nil || targetStore == nil ||
			sourceStore.Status != enums.StatusOk || targetStore.Status != enums.StatusOk {
			return errorsx.InvalidParam("来源门店或目标门店不存在或已停用")
		}
		tenant := repositories.TenantRepository.Get(ctx.Tx, tenantID)
		if tenant == nil || tenant.Status != enums.StatusOk || tenant.IntentProfileID <= 0 {
			return errorsx.InvalidParam("接入公司尚未绑定有效行业")
		}

		sourceActive, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelationForUpdate(
			ctx.Tx, tenantID, lockedSource.StoreID, lockedSource.ID,
		)
		if err != nil {
			return err
		}
		targetAll, err := repositories.CustomerTagRelationRepository.FindByStoreRelationForUpdate(
			ctx.Tx, tenantID, lockedTarget.StoreID, lockedTarget.ID,
		)
		if err != nil {
			return err
		}
		sourceTagIDs := activeCustomerTagIDs(sourceActive)
		targetBeforeTagIDs := activeCustomerTagIDs(targetAll)
		targetScope := buildStoreRelationTagReconcileScope(ctx.Tx, tenant, lockedTarget)
		now := time.Now()

		switch req.Strategy {
		case enums.StoreCustomerTagReconcileStrategyPreserveSource:
			if err := s.validateStoreRelationTagSourceSet(ctx.Tx, targetScope, sourceActive); err != nil {
				return err
			}
			sourceSet := customerTagIDSet(sourceTagIDs)
			for i := range targetAll {
				item := &targetAll[i]
				if _, keep := sourceSet[item.TagID]; keep {
					continue
				}
				wasActive := item.RelationStatus == customerTagRelationActive
				needsUpdate := wasActive ||
					item.Source != customerTagSourceManual ||
					!item.ManualProtected ||
					item.InactivatedAt == nil
				if !needsUpdate {
					continue
				}
				columns := map[string]any{
					"source":           customerTagSourceManual,
					"relation_status":  customerTagRelationInactive,
					"manual_protected": true,
					"updated_at":       now,
					"update_user_id":   operator.UserID,
					"update_user_name": operator.Username,
				}
				if wasActive || item.InactivatedAt == nil {
					columns["inactivated_at"] = now
				}
				if err := repositories.CustomerTagRelationRepository.UpdatesInScope(
					ctx.Tx, item.ID, tenantID, lockedTarget.StoreID, lockedTarget.ID, columns,
				); err != nil {
					return err
				}
				targetChanged = true
				if wasActive {
					if err := s.writeChangeLog(ctx.Tx, targetScope, 0, "remove", item.TagID, 0, nil, customerTagSourceManual, 1, operator); err != nil {
						return err
					}
				}
			}
			for _, tagID := range sourceTagIDs {
				applied, err := s.manualAddDB(ctx.Tx, targetScope, tagID, operator)
				if err != nil {
					return err
				}
				targetChanged = targetChanged || applied
			}
		case enums.StoreCustomerTagReconcileStrategyPreserveTarget:
			// The explicit decision is still persisted below even though the
			// target relation remains unchanged.
		case enums.StoreCustomerTagReconcileStrategyClearRebuild:
			for i := range targetAll {
				item := &targetAll[i]
				wasActive := item.RelationStatus == customerTagRelationActive
				needsUpdate := wasActive || item.ManualProtected || item.InactivatedAt == nil
				if !needsUpdate {
					continue
				}
				columns := map[string]any{
					"relation_status":  customerTagRelationInactive,
					"manual_protected": false,
					"updated_at":       now,
					"update_user_id":   operator.UserID,
					"update_user_name": operator.Username,
				}
				if wasActive || item.InactivatedAt == nil {
					columns["inactivated_at"] = now
				}
				if err := repositories.CustomerTagRelationRepository.UpdatesInScope(
					ctx.Tx, item.ID, tenantID, lockedTarget.StoreID, lockedTarget.ID, columns,
				); err != nil {
					return err
				}
				targetChanged = true
				if wasActive {
					if err := s.writeChangeLog(ctx.Tx, targetScope, 0, "remove", item.TagID, 0, nil, customerTagSourceManual, 1, operator); err != nil {
						return err
					}
				}
			}
		}

		targetAfter, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelationForUpdate(
			ctx.Tx, tenantID, lockedTarget.StoreID, lockedTarget.ID,
		)
		if err != nil {
			return err
		}
		targetAfterTagIDs := activeCustomerTagIDs(targetAfter)
		if err := validateStoreRelationTagReconcileResult(req.Strategy, sourceTagIDs, targetBeforeTagIDs, targetAfterTagIDs); err != nil {
			return err
		}
		decision = &models.StoreCustomerTagDecision{
			TenantID:               tenantID,
			CustomerID:             lockedSource.CustomerID,
			SourceStoreID:          lockedSource.StoreID,
			SourceStoreRelationID:  lockedSource.ID,
			TargetStoreID:          lockedTarget.StoreID,
			TargetStoreRelationID:  lockedTarget.ID,
			Strategy:               req.Strategy,
			SourceTagIDsJSON:       marshalCustomerTagIDs(sourceTagIDs),
			TargetBeforeTagIDsJSON: marshalCustomerTagIDs(targetBeforeTagIDs),
			TargetAfterTagIDsJSON:  marshalCustomerTagIDs(targetAfterTagIDs),
			OperatorID:             operator.UserID,
			OperatorName:           operator.Username,
			CreatedAt:              now,
		}
		if err := repositories.StoreCustomerTagDecisionRepository.Create(ctx.Tx, decision); err != nil {
			return err
		}
		if err := s.writeChangeLog(
			ctx.Tx,
			targetScope,
			0,
			storeRelationTagReconcileAction(req.Strategy),
			0,
			0,
			nil,
			customerTagSourceManual,
			1,
			operator,
		); err != nil {
			return err
		}
		eventConversationID = lockedTarget.LastConversationID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if targetChanged && eventConversationID > 0 {
		conversation := repositories.ConversationRepository.Get(sqls.DB(), eventConversationID)
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(
			sqls.DB(), eventConversationID, decision.TenantID,
		)
		if conversation != nil && route != nil &&
			conversation.TenantID == decision.TenantID &&
			conversation.CustomerID == decision.CustomerID &&
			route.StoreID == decision.TargetStoreID {
			WsService.PublishCustomerTagChanged(conversation, decision.TargetStoreID, decision.TargetStoreRelationID, time.Now())
		}
	}
	return decision, nil
}

func (s *customerTagService) ManualAdd(req request.AddCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.prepareManualScope(req.ConversationID, operator)
	if err != nil {
		return err
	}
	unlock := lockCustomerTags(scope.TenantID, scope.StoreID, scope.Conversation.CustomerID)
	defer unlock()
	changed := false
	eventRelationID := scope.RelationID()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedScope, err := s.resolveConversationScope(ctx.Tx, req.ConversationID, true)
		if err != nil {
			return err
		}
		if err := s.lockScopeRelation(ctx.Tx, lockedScope); err != nil {
			return err
		}
		eventRelationID = lockedScope.Relation.ID
		changed, err = s.manualAddDB(ctx.Tx, lockedScope, req.TagID, operator)
		return err
	})
	if err == nil && changed {
		WsService.PublishCustomerTagChanged(scope.Conversation, scope.StoreID, eventRelationID, time.Now())
	}
	return err
}

func (s *customerTagService) ManualRemove(req request.RemoveCustomerTagRequest, operator *dto.AuthPrincipal) error {
	scope, err := s.prepareManualScope(req.ConversationID, operator)
	if err != nil {
		return err
	}
	unlock := lockCustomerTags(scope.TenantID, scope.StoreID, scope.Conversation.CustomerID)
	defer unlock()
	changed := false
	eventRelationID := scope.RelationID()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedScope, err := s.resolveConversationScope(ctx.Tx, req.ConversationID, false)
		if err != nil {
			return err
		}
		if lockedScope.Relation == nil {
			return nil
		}
		if err := s.lockScopeRelation(ctx.Tx, lockedScope); err != nil {
			return err
		}
		eventRelationID = lockedScope.Relation.ID
		current, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTagForUpdate(
			ctx.Tx, lockedScope.TenantID, lockedScope.StoreID, lockedScope.Relation.ID, req.TagID,
		)
		if err != nil {
			return err
		}
		if current == nil || current.RelationStatus != customerTagRelationActive {
			return nil
		}
		now := time.Now()
		if err := repositories.CustomerTagRelationRepository.UpdatesInScope(ctx.Tx, current.ID, lockedScope.TenantID, lockedScope.StoreID, lockedScope.Relation.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationInactive,
			"manual_protected": true, "inactivated_at": now, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		changed = true
		return s.writeChangeLog(ctx.Tx, lockedScope, 0, "remove", current.TagID, 0, nil, customerTagSourceManual, 1, operator)
	})
	if err == nil && changed {
		WsService.PublishCustomerTagChanged(scope.Conversation, scope.StoreID, eventRelationID, time.Now())
	}
	return err
}

func (s *customerTagService) ManualReplace(req request.ReplaceCustomerTagRequest, operator *dto.AuthPrincipal) error {
	if req.OldTagID == req.NewTagID {
		return errorsx.InvalidParam("新旧客户标签不能相同")
	}
	scope, err := s.prepareManualScope(req.ConversationID, operator)
	if err != nil {
		return err
	}
	unlock := lockCustomerTags(scope.TenantID, scope.StoreID, scope.Conversation.CustomerID)
	defer unlock()
	changed := false
	eventRelationID := scope.RelationID()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedScope, err := s.resolveConversationScope(ctx.Tx, req.ConversationID, true)
		if err != nil {
			return err
		}
		if err := s.lockScopeRelation(ctx.Tx, lockedScope); err != nil {
			return err
		}
		eventRelationID = lockedScope.Relation.ID
		oldTag, err := s.validateTagForScope(ctx.Tx, req.OldTagID, lockedScope, false)
		if err != nil {
			return err
		}
		newTag, err := s.validateTagForScope(ctx.Tx, req.NewTagID, lockedScope, false)
		if err != nil {
			return err
		}
		if strings.TrimSpace(oldTag.ConflictGroup) == "" || oldTag.ConflictGroup != newTag.ConflictGroup {
			return errorsx.InvalidParam("只能替换同一互斥组内的客户标签")
		}
		old, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTagForUpdate(
			ctx.Tx, lockedScope.TenantID, lockedScope.StoreID, lockedScope.Relation.ID, req.OldTagID,
		)
		if err != nil {
			return err
		}
		if old == nil || old.RelationStatus != customerTagRelationActive {
			return errorsx.InvalidParam("待替换的客户标签不存在")
		}
		changed, err = s.manualAddDB(ctx.Tx, lockedScope, req.NewTagID, operator)
		return err
	})
	if err == nil && changed {
		WsService.PublishCustomerTagChanged(scope.Conversation, scope.StoreID, eventRelationID, time.Now())
	}
	return err
}

func (s *customerTagService) prepareManualScope(conversationID int64, operator *dto.AuthPrincipal) (*customerTagScope, error) {
	if conversationID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return nil, err
	}
	if err := s.requireConversationAccess(scope, operator); err != nil {
		return nil, err
	}
	return scope, nil
}

func (scope *customerTagScope) RelationID() int64 {
	if scope == nil || scope.Relation == nil {
		return 0
	}
	return scope.Relation.ID
}

func (s *customerTagService) manualAddDB(db *gorm.DB, scope *customerTagScope, tagID int64, operator *dto.AuthPrincipal) (bool, error) {
	tag, err := s.validateTagForScope(db, tagID, scope, false)
	if err != nil {
		return false, err
	}
	current, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTagForUpdate(
		db, scope.TenantID, scope.StoreID, scope.Relation.ID, tag.ID,
	)
	if err != nil {
		return false, err
	}
	active, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelationForUpdate(db, scope.TenantID, scope.StoreID, scope.Relation.ID)
	if err != nil {
		return false, err
	}
	conflicts := s.findActiveConflictingRelations(db, active, tag, scope.TenantID)
	wasActive := current != nil && current.RelationStatus == customerTagRelationActive
	wasProtectedManual := wasActive && current.ManualProtected && current.Source == customerTagSourceManual
	if wasProtectedManual && len(conflicts) == 0 {
		return false, nil
	}
	now := time.Now()
	for _, conflict := range conflicts {
		if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, conflict.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationInactive,
			"manual_protected": true, "inactivated_at": now, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return false, err
		}
	}
	if !wasActive {
		count, err := repositories.CustomerTagRelationRepository.CountActiveByStoreRelation(db, scope.TenantID, scope.StoreID, scope.Relation.ID)
		if err != nil {
			return false, err
		}
		if count >= maxActiveCustomerTags {
			return false, errorsx.InvalidParam("每个门店客户最多保留6个有效标签")
		}
	}
	if current != nil {
		if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, current.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
			"source": customerTagSourceManual, "relation_status": customerTagRelationActive,
			"confidence": 1, "manual_protected": true, "last_matched_at": now,
			"inactivated_at": nil, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return false, err
		}
	} else {
		item := &models.CustomerTagRelation{
			TenantID: scope.TenantID, StoreID: scope.StoreID,
			CustomerID: scope.Conversation.CustomerID, StoreCustomerRelationID: scope.Relation.ID,
			TagID: tag.ID, Source: customerTagSourceManual, RelationStatus: customerTagRelationActive,
			Confidence: 1, EvidenceCount: 1, FirstMatchedAt: &now, LastMatchedAt: &now,
			ManualProtected: true, AuditFields: utils.BuildAuditFields(operator),
		}
		if err := repositories.CustomerTagRelationRepository.Create(db, item); err != nil {
			return false, err
		}
	}
	if len(conflicts) > 0 {
		for _, conflict := range conflicts {
			if err := s.writeChangeLog(db, scope, 0, "replace", conflict.TagID, tag.ID, nil, customerTagSourceManual, 1, operator); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if err := s.writeChangeLog(db, scope, 0, "add", 0, tag.ID, nil, customerTagSourceManual, 1, operator); err != nil {
		return false, err
	}
	return true, nil
}

func (s *customerTagService) ApplyAI(conversationID, runID int64, operations []CustomerTagOperation) (bool, error) {
	if len(operations) == 0 {
		return false, nil
	}
	scope, err := s.resolveConversationScope(sqls.DB(), conversationID, false)
	if err != nil {
		return false, err
	}
	unlock := lockCustomerTags(scope.TenantID, scope.StoreID, scope.Conversation.CustomerID)
	defer unlock()
	changed := false
	eventRelationID := scope.RelationID()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedScope, err := s.resolveConversationScope(ctx.Tx, conversationID, true)
		if err != nil {
			return err
		}
		if err := s.lockScopeRelation(ctx.Tx, lockedScope); err != nil {
			return err
		}
		eventRelationID = lockedScope.Relation.ID
		changed, err = s.applyAIOperationsDB(ctx.Tx, lockedScope, runID, operations)
		return err
	})
	if err == nil && changed {
		WsService.PublishCustomerTagChanged(scope.Conversation, scope.StoreID, eventRelationID, time.Now())
	}
	return changed, err
}

// applyAIOperationsDB is shared by the public mutation and the evolution
// checkpoint transaction. The caller must already hold the Store customer
// relation lock and the in-process customer-tag mutex.
func (s *customerTagService) applyAIOperationsDB(
	db *gorm.DB,
	scope *customerTagScope,
	runID int64,
	operations []CustomerTagOperation,
) (bool, error) {
	changed := false
	for i := range operations {
		applied, err := s.applyAIOperation(db, scope, runID, operations[i])
		if err != nil {
			return false, err
		}
		changed = changed || applied
	}
	return changed, nil
}

func (s *customerTagService) applyAIOperation(db *gorm.DB, scope *customerTagScope, runID int64, operation CustomerTagOperation) (bool, error) {
	operation.Op = strings.ToLower(strings.TrimSpace(operation.Op))
	if operation.Confidence < 0 || operation.Confidence > 1 {
		return false, errorsx.InvalidParam("客户标签置信度不合法")
	}
	tag, err := s.validateTagForScope(db, operation.TagID, scope, true)
	if err != nil {
		return false, err
	}
	current, err := repositories.CustomerTagRelationRepository.GetByStoreRelationAndTagForUpdate(
		db, scope.TenantID, scope.StoreID, scope.Relation.ID, tag.ID,
	)
	if err != nil {
		return false, err
	}
	active, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelationForUpdate(db, scope.TenantID, scope.StoreID, scope.Relation.ID)
	if err != nil {
		return false, err
	}
	now := time.Now()
	switch operation.Op {
	case "add", "refresh":
		if current != nil && current.ManualProtected {
			return false, nil
		}
		if len(s.findActiveConflictingRelations(db, active, tag, scope.TenantID)) > 0 {
			return false, nil
		}
		if current != nil {
			if current.RelationStatus != customerTagRelationActive {
				count, err := repositories.CustomerTagRelationRepository.CountActiveByStoreRelation(db, scope.TenantID, scope.StoreID, scope.Relation.ID)
				if err != nil {
					return false, err
				}
				if count >= maxActiveCustomerTags {
					return false, nil
				}
			}
			if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, current.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
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
		count, err := repositories.CustomerTagRelationRepository.CountActiveByStoreRelation(db, scope.TenantID, scope.StoreID, scope.Relation.ID)
		if err != nil {
			return false, err
		}
		if count >= maxActiveCustomerTags {
			return false, nil
		}
		item := &models.CustomerTagRelation{
			TenantID: scope.TenantID, StoreID: scope.StoreID,
			CustomerID: scope.Conversation.CustomerID, StoreCustomerRelationID: scope.Relation.ID,
			TagID: tag.ID, Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
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
		if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, current.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
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
		conflicts := s.findActiveConflictingRelations(db, active, tag, scope.TenantID)
		if len(conflicts) == 0 || len(replaceIDs) != len(conflicts) {
			return false, nil
		}
		for _, conflict := range conflicts {
			if conflict.ManualProtected {
				return false, nil
			}
			if _, ok := replaceIDs[conflict.TagID]; !ok {
				return false, nil
			}
		}
		if current == nil {
			current = &models.CustomerTagRelation{
				TenantID: scope.TenantID, StoreID: scope.StoreID,
				CustomerID: scope.Conversation.CustomerID, StoreCustomerRelationID: scope.Relation.ID,
				TagID: tag.ID, Source: customerTagSourceAI, RelationStatus: customerTagRelationActive,
				Confidence: operation.Confidence, EvidenceCount: 1,
				FirstMatchedAt: &now, LastMatchedAt: &now, LastEvolutionRunID: runID,
				AuditFields: utils.BuildAuditFields(nil),
			}
			if err := repositories.CustomerTagRelationRepository.Create(db, current); err != nil {
				return false, err
			}
		} else {
			if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, current.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
				"source": customerTagSourceAI, "relation_status": customerTagRelationActive,
				"confidence": operation.Confidence, "evidence_count": current.EvidenceCount + 1,
				"last_matched_at": now, "last_evolution_run_id": runID,
				"inactivated_at": nil, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return false, err
			}
		}
		for _, conflict := range conflicts {
			if err := repositories.CustomerTagRelationRepository.UpdatesInScope(db, conflict.ID, scope.TenantID, scope.StoreID, scope.Relation.ID, map[string]any{
				"relation_status": customerTagRelationInactive, "inactivated_at": now,
				"last_evolution_run_id": runID, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return false, err
			}
			if err := s.writeChangeLog(db, scope, runID, "replace", conflict.TagID, tag.ID, operation.EvidenceMessageIDs, customerTagSourceAI, operation.Confidence, nil); err != nil {
				return false, err
			}
		}
		return true, nil
	default:
		return false, errorsx.InvalidParam("客户标签操作不合法")
	}
}

func (s *customerTagService) resolveConversationScope(db *gorm.DB, conversationID int64, createRelation bool) (*customerTagScope, error) {
	conversation := repositories.ConversationRepository.Get(db, conversationID)
	if conversation == nil || conversation.TenantID <= 0 || conversation.CustomerID <= 0 {
		return nil, errorsx.InvalidParam("会话尚未关联有效客户")
	}
	if repositories.CustomerRepository.GetInTenant(db, conversation.CustomerID, conversation.TenantID) == nil {
		return nil, errorsx.InvalidParam("会话客户不属于当前接入公司")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, conversation.TenantID)
	if route == nil || route.StoreID <= 0 {
		return nil, errorsx.InvalidParam("会话尚未绑定门店")
	}
	store := repositories.StoreRepository.GetInTenant(db, route.StoreID, conversation.TenantID)
	if store == nil || store.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("门店不存在或已停用")
	}
	tenant := repositories.TenantRepository.Get(db, conversation.TenantID)
	if tenant == nil || tenant.IntentProfileID <= 0 {
		return nil, errorsx.InvalidParam("接入公司尚未绑定有效行业")
	}
	relation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		db, conversation.TenantID, conversation.CustomerID, route.StoreID,
	)
	if relation == nil && createRelation {
		now := time.Now()
		candidate := &models.StoreCustomerRelation{
			TenantID: conversation.TenantID, CustomerID: conversation.CustomerID, StoreID: route.StoreID,
			WxWorkInstanceID: route.WxWorkInstanceID, LastConversationID: conversation.ID,
			LastActiveAt: &now, VisitCount: 1, Status: enums.StatusOk,
			AuditFields: utils.BuildAuditFields(nil),
		}
		if err := repositories.StoreCustomerRelationRepository.CreateIfAbsent(db, candidate); err != nil {
			return nil, err
		}
		relation = repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
			db, conversation.TenantID, conversation.CustomerID, route.StoreID,
		)
		if relation == nil {
			return nil, errorsx.BusinessError(5, "客户门店关系创建失败")
		}
	}
	if relation != nil && (relation.TenantID != conversation.TenantID || relation.StoreID != route.StoreID || relation.CustomerID != conversation.CustomerID) {
		return nil, errorsx.Forbidden("客户门店关系范围不一致")
	}
	return &customerTagScope{
		Conversation: conversation, Route: route, Relation: relation,
		TenantID: conversation.TenantID, StoreID: route.StoreID, ProfileID: tenant.IntentProfileID,
	}, nil
}

func (s *customerTagService) lockScopeRelation(db *gorm.DB, scope *customerTagScope) error {
	if scope == nil || scope.Relation == nil {
		return errorsx.InvalidParam("客户门店关系不存在")
	}
	locked, err := repositories.StoreCustomerRelationRepository.GetForUpdateInTenant(db, scope.Relation.ID, scope.TenantID)
	if err != nil {
		return err
	}
	if locked == nil || locked.StoreID != scope.StoreID || locked.CustomerID != scope.Conversation.CustomerID || locked.Status != enums.StatusOk {
		return errorsx.InvalidParam("客户门店关系不存在或已停用")
	}
	scope.Relation = locked
	return nil
}

func (s *customerTagService) validateTagForScope(db *gorm.DB, tagID int64, scope *customerTagScope, requireAI bool) (*models.Tag, error) {
	if scope == nil || scope.TenantID <= 0 || tagID <= 0 {
		return nil, errorsx.InvalidParam("标签不存在或不可用")
	}
	tag := repositories.TagRepository.GetInTenant(db, tagID, scope.TenantID)
	profileID := scope.ProfileID
	if tag == nil || tag.Status != enums.StatusOk || tag.ParentID == 0 || !tag.SystemDefined || tag.TemplateDefinitionID == nil || tag.IntentProfileID != profileID {
		return nil, errorsx.InvalidParam("标签不存在或不可用")
	}
	if repositories.TagRepository.Count(db, sqls.NewCnd().
		Eq("tenant_id", scope.TenantID).
		Eq("intent_profile_id", profileID).
		Eq("parent_id", tag.ID)) > 0 {
		return nil, errorsx.InvalidParam("标签分类不能直接用于客户画像")
	}
	if requireAI && !tag.AIEnabled {
		return nil, errorsx.InvalidParam("标签未开放给AI使用")
	}
	if strings.TrimSpace(tag.SemanticKey) == "" {
		return nil, errorsx.InvalidParam("标签缺少稳定语义标识")
	}
	return tag, nil
}

func (s *customerTagService) requireConversationAccess(scope *customerTagScope, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if scope == nil || scope.Conversation == nil || scope.TenantID <= 0 || scope.StoreID <= 0 {
		return errorsx.InvalidParam("会话门店范围不存在")
	}
	if operator.ActiveTenantID != scope.TenantID {
		return errorsx.Forbidden("无权操作其他接入公司的客户标签")
	}
	if !AgentTeamScopeService.CanViewConversation(operator, scope.Conversation.ID) {
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
	if evidence == nil {
		evidence = make([]int64, 0)
	}
	evidence = uniquePositive(evidence)
	rawEvidence, _ := json.Marshal(evidence)
	return repositories.CustomerTagChangeLogRepository.Create(db, &models.CustomerTagChangeLog{
		TenantID: scope.TenantID, StoreID: scope.StoreID, CustomerID: scope.Conversation.CustomerID,
		StoreCustomerRelationID: scope.Relation.ID, ConversationID: scope.Conversation.ID,
		EvolutionRunID: runID, Action: action, OldTagID: oldTagID, NewTagID: newTagID,
		EvidenceMessageIDs: string(rawEvidence), Source: source, Confidence: confidence,
		OperatorType: operatorType, OperatorID: operatorID, OperatorName: operatorName, CreatedAt: time.Now(),
	})
}

func (s *customerTagService) findActiveConflictingRelations(db *gorm.DB, active []models.CustomerTagRelation, target *models.Tag, tenantID int64) []*models.CustomerTagRelation {
	if target == nil || strings.TrimSpace(target.ConflictGroup) == "" {
		return nil
	}
	tagIDs := make([]int64, 0, len(active))
	for i := range active {
		if active[i].TagID != target.ID {
			tagIDs = append(tagIDs, active[i].TagID)
		}
	}
	tags := repositories.TagRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("id", uniquePositive(tagIDs)))
	tagByID := make(map[int64]*models.Tag, len(tags))
	for i := range tags {
		tagByID[tags[i].ID] = &tags[i]
	}
	ret := make([]*models.CustomerTagRelation, 0)
	for i := range active {
		item := &active[i]
		if item.TagID != target.ID && tagsCanReplace(tagByID[item.TagID], target) {
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

func (s *customerTagService) requireStoreRelationTagReconcileAccess(operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if operator.ActiveTenantID <= 0 {
		return errorsx.Forbidden("请先选择接入公司")
	}
	if operator.IsPlatformAccount {
		return nil
	}
	if operator.TenantID > 0 && operator.TenantID != operator.ActiveTenantID {
		return errorsx.Forbidden("不能处理其他接入公司的客户标签")
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin) {
		return errorsx.Forbidden("仅公司主管可确认门店客户标签处理方案")
	}
	return nil
}

func validateStoreRelationTagReconcilePair(source, target *models.StoreCustomerRelation, tenantID int64) error {
	if source == nil || target == nil ||
		source.Status != enums.StatusOk || target.Status != enums.StatusOk {
		return errorsx.InvalidParam("来源或目标门店客户关系不存在或已停用")
	}
	if source.TenantID != tenantID || target.TenantID != tenantID {
		return errorsx.Forbidden("不能跨接入公司处理客户标签")
	}
	if source.ID == target.ID || source.StoreID == target.StoreID {
		return errorsx.InvalidParam("来源门店和目标门店必须不同")
	}
	if source.CustomerID <= 0 || source.CustomerID != target.CustomerID {
		return errorsx.InvalidParam("来源和目标必须属于同一客户")
	}
	return nil
}

func lockStoreRelationTagReconcilePair(
	db *gorm.DB,
	tenantID, sourceRelationID, targetRelationID int64,
) (*models.StoreCustomerRelation, *models.StoreCustomerRelation, error) {
	ids := []int64{sourceRelationID, targetRelationID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	locked := make(map[int64]*models.StoreCustomerRelation, len(ids))
	for _, id := range ids {
		item, err := repositories.StoreCustomerRelationRepository.GetForUpdateInTenant(db, id, tenantID)
		if err != nil {
			return nil, nil, err
		}
		locked[id] = item
	}
	return locked[sourceRelationID], locked[targetRelationID], nil
}

func buildStoreRelationTagReconcileScope(
	db *gorm.DB,
	tenant *models.Tenant,
	relation *models.StoreCustomerRelation,
) *customerTagScope {
	conversation := &models.Conversation{
		TenantID:   relation.TenantID,
		CustomerID: relation.CustomerID,
	}
	if relation.LastConversationID > 0 {
		if current := repositories.ConversationRepository.Get(db, relation.LastConversationID); current != nil &&
			current.TenantID == relation.TenantID && current.CustomerID == relation.CustomerID {
			route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(
				db, current.ID, relation.TenantID,
			)
			if route != nil && route.StoreID == relation.StoreID {
				conversation = current
			}
		}
	}
	return &customerTagScope{
		Conversation: conversation,
		Relation:     relation,
		TenantID:     relation.TenantID,
		StoreID:      relation.StoreID,
		ProfileID:    tenant.IntentProfileID,
	}
}

func (s *customerTagService) validateStoreRelationTagSourceSet(
	db *gorm.DB,
	scope *customerTagScope,
	relations []models.CustomerTagRelation,
) error {
	if len(relations) > maxActiveCustomerTags {
		return errorsx.InvalidParam("来源门店客户存在超过6个有效标签，必须先修复后再处理")
	}
	semanticKeys := make(map[string]int64, len(relations))
	conflictGroups := make(map[string]int64, len(relations))
	for i := range relations {
		relation := relations[i]
		if relation.RelationStatus != customerTagRelationActive ||
			relation.TenantID != scope.TenantID ||
			relation.CustomerID != scope.Conversation.CustomerID {
			return errorsx.InvalidParam("来源门店客户标签范围不一致，必须先修复后再处理")
		}
		tag, err := s.validateTagForScope(db, relation.TagID, scope, false)
		if err != nil {
			return errorsx.InvalidParam("来源门店客户包含已失效或跨行业标签，必须先修复后再处理")
		}
		semanticKey := strings.TrimSpace(tag.SemanticKey)
		if existingID, exists := semanticKeys[semanticKey]; exists && existingID != tag.ID {
			return errorsx.InvalidParam("来源门店客户存在重复语义标签，必须先修复后再处理")
		}
		semanticKeys[semanticKey] = tag.ID
		conflictGroup := strings.TrimSpace(tag.ConflictGroup)
		if conflictGroup == "" {
			continue
		}
		if existingID, exists := conflictGroups[conflictGroup]; exists && existingID != tag.ID {
			return errorsx.InvalidParam("来源门店客户存在互斥标签，必须先修复后再处理")
		}
		conflictGroups[conflictGroup] = tag.ID
	}
	return nil
}

func activeCustomerTagIDs(relations []models.CustomerTagRelation) []int64 {
	ret := make([]int64, 0, len(relations))
	for i := range relations {
		if relations[i].RelationStatus == customerTagRelationActive && relations[i].TagID > 0 {
			ret = append(ret, relations[i].TagID)
		}
	}
	ret = uniquePositive(ret)
	sort.Slice(ret, func(i, j int) bool { return ret[i] < ret[j] })
	return ret
}

func customerTagIDSet(values []int64) map[int64]struct{} {
	ret := make(map[int64]struct{}, len(values))
	for _, value := range values {
		ret[value] = struct{}{}
	}
	return ret
}

func marshalCustomerTagIDs(values []int64) string {
	if values == nil {
		values = make([]int64, 0)
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func validateStoreRelationTagReconcileResult(
	strategy enums.StoreCustomerTagReconcileStrategy,
	sourceTagIDs, targetBeforeTagIDs, targetAfterTagIDs []int64,
) error {
	expected := make([]int64, 0)
	switch strategy {
	case enums.StoreCustomerTagReconcileStrategyPreserveSource:
		expected = sourceTagIDs
	case enums.StoreCustomerTagReconcileStrategyPreserveTarget:
		expected = targetBeforeTagIDs
	case enums.StoreCustomerTagReconcileStrategyClearRebuild:
	default:
		return errorsx.InvalidParam("门店客户标签处理方案不合法")
	}
	if !slices.Equal(expected, targetAfterTagIDs) {
		return errorsx.BusinessError(6, "门店客户标签处理结果校验失败")
	}
	return nil
}

func storeRelationTagReconcileAction(strategy enums.StoreCustomerTagReconcileStrategy) string {
	switch strategy {
	case enums.StoreCustomerTagReconcileStrategyPreserveSource:
		return "reconcile_preserve_source"
	case enums.StoreCustomerTagReconcileStrategyPreserveTarget:
		return "reconcile_preserve_target"
	case enums.StoreCustomerTagReconcileStrategyClearRebuild:
		return "reconcile_clear_rebuild"
	default:
		return "reconcile"
	}
}

func (s *customerTagService) SelectReplyTagCandidates(conversationID int64, orderedScenes []string, currentText string) ([]ReplyTagCandidate, error) {
	if conversationID <= 0 || len(orderedScenes) == 0 || strings.TrimSpace(currentText) == "" {
		return nil, nil
	}
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 || conversation.CustomerID <= 0 {
		return nil, fmt.Errorf("reply tag context conversation scope is incomplete")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	if route == nil || route.StoreID <= 0 {
		return nil, fmt.Errorf("reply tag context store scope is incomplete")
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), conversation.TenantID)
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), route.StoreID, conversation.TenantID)
	tenantPolicy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(sqls.DB(), conversation.TenantID)
	if tenant == nil || tenant.Status != enums.StatusOk || tenant.IntentProfileID <= 0 ||
		store == nil || store.Status != enums.StatusOk ||
		tenantPolicy == nil || tenantPolicy.Status != enums.StatusOk || tenantPolicy.IntentProfileID != tenant.IntentProfileID {
		return nil, nil
	}
	policy, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStore(sqls.DB(), conversation.TenantID, route.StoreID)
	if err != nil {
		return nil, err
	}
	if policy == nil || policy.Status != enums.StatusOk || !policy.ReplyTagContextEnabled {
		return nil, nil
	}
	storeRelation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(sqls.DB(), conversation.TenantID, conversation.CustomerID, route.StoreID)
	if storeRelation == nil || storeRelation.Status != enums.StatusOk {
		return nil, nil
	}
	sceneOrder := make(map[string]int, len(orderedScenes))
	for _, scene := range orderedScenes {
		scene = strings.TrimSpace(scene)
		if scene != "" {
			if _, exists := sceneOrder[scene]; !exists {
				sceneOrder[scene] = len(sceneOrder)
			}
		}
	}
	if len(sceneOrder) == 0 {
		return nil, nil
	}
	relations, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelation(sqls.DB(), conversation.TenantID, route.StoreID, storeRelation.ID)
	if err != nil {
		return nil, err
	}
	tags := make(map[int64]*models.Tag, len(relations))
	mentionedTagIDs := make(map[int64]struct{})
	mentionedConflictGroups := make(map[string]struct{})
	for _, relation := range relations {
		tag := repositories.TagRepository.GetInTenant(sqls.DB(), relation.TagID, conversation.TenantID)
		if tag == nil || tag.Status != enums.StatusOk || tag.IntentProfileID != tenantPolicy.IntentProfileID || !tag.SystemDefined || tag.TemplateDefinitionID == nil {
			continue
		}
		tags[tag.ID] = tag
		if tagMentionedInCurrentText(currentText, tag) {
			mentionedTagIDs[tag.ID] = struct{}{}
			if group := strings.TrimSpace(tag.ConflictGroup); group != "" {
				mentionedConflictGroups[group] = struct{}{}
			}
		}
	}
	candidates := make([]ReplyTagCandidate, 0, len(relations))
	for _, relation := range relations {
		tag := tags[relation.TagID]
		if tag == nil || !tag.ReplyEnabled {
			continue
		}
		scene := strings.TrimSpace(tag.ApplicableScene)
		if _, ok := sceneOrder[scene]; !ok {
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
			ApplicableScene: scene, ConflictGroup: tag.ConflictGroup,
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
