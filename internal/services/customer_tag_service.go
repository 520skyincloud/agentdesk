package services

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const customerTagSourceManual = "manual"

var CustomerTagService = &customerTagService{}

type customerTagService struct{}

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
	policy, err := repositories.StoreCustomerTagRuntimePolicyRepository.GetByStore(sqls.DB(), conversation.TenantID, route.StoreID)
	if err != nil {
		return nil, err
	}
	if policy == nil || policy.Status != enums.StatusOk || !policy.ReplyTagContextEnabled {
		return nil, nil
	}
	storeRelation := repositories.StoreCustomerRelationRepository.TakeByCustomerAndStoreInTenant(
		sqls.DB(), conversation.TenantID, conversation.CustomerID, route.StoreID,
	)
	if storeRelation == nil || storeRelation.Status != enums.StatusOk {
		return nil, nil
	}

	sceneOrder := make(map[string]int, len(orderedScenes))
	for _, scene := range orderedScenes {
		scene = strings.TrimSpace(scene)
		if scene == "" {
			continue
		}
		if _, exists := sceneOrder[scene]; !exists {
			sceneOrder[scene] = len(sceneOrder)
		}
	}
	if len(sceneOrder) == 0 {
		return nil, nil
	}
	relations, err := repositories.CustomerTagRelationRepository.FindActiveByStoreRelation(
		sqls.DB(), conversation.TenantID, route.StoreID, storeRelation.ID,
	)
	if err != nil {
		return nil, err
	}
	tags := make(map[int64]*models.Tag, len(relations))
	mentionedTagIDs := make(map[int64]struct{})
	mentionedConflictGroups := make(map[string]struct{})
	for _, relation := range relations {
		tag := repositories.TagRepository.GetInTenant(sqls.DB(), relation.TagID, conversation.TenantID)
		if tag == nil || tag.Status != enums.StatusOk || !tag.SystemDefined || tag.TemplateDefinitionID == nil {
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
