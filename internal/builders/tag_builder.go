package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"time"
)

func BuildTagResponse(item *models.Tag) response.TagResponse {
	if item == nil {
		return response.TagResponse{}
	}
	templateDefinitionID := int64(0)
	if item.TemplateDefinitionID != nil {
		templateDefinitionID = *item.TemplateDefinitionID
	}
	return response.TagResponse{
		ID: item.ID, IntentProfileID: item.IntentProfileID, TemplateDefinitionID: templateDefinitionID,
		ParentID: item.ParentID, Name: item.Name, DisplayAlias: item.DisplayAlias,
		SemanticKey: item.SemanticKey, ConflictGroup: item.ConflictGroup,
		ApplicableScene: item.ApplicableScene, AIEnabled: item.AIEnabled,
		ReplyEnabled: item.ReplyEnabled, SystemDefined: item.SystemDefined,
		Remark: item.Remark, SortNo: item.SortNo, Status: item.Status,
		CreatedAt: item.CreatedAt.Format(time.DateTime), UpdatedAt: item.UpdatedAt.Format(time.DateTime),
	}
}

func BuildTagResponses(list []models.Tag) []response.TagResponse {
	if len(list) == 0 {
		return nil
	}
	results := make([]response.TagResponse, 0, len(list))
	for i := range list {
		results = append(results, BuildTagResponse(&list[i]))
	}
	return results
}

func BuildTagTreeResponses(list []models.Tag) []*response.TagTreeResponse {
	if len(list) == 0 {
		return nil
	}

	nodeMap := make(map[int64]*response.TagTreeResponse, len(list))
	roots := make([]*response.TagTreeResponse, 0)

	for i := range list {
		item := &list[i]
		templateDefinitionID := int64(0)
		if item.TemplateDefinitionID != nil {
			templateDefinitionID = *item.TemplateDefinitionID
		}
		nodeMap[item.ID] = &response.TagTreeResponse{
			ID: item.ID, IntentProfileID: item.IntentProfileID, TemplateDefinitionID: templateDefinitionID,
			ParentID: item.ParentID, Name: item.Name, DisplayAlias: item.DisplayAlias,
			SemanticKey: item.SemanticKey, ConflictGroup: item.ConflictGroup,
			ApplicableScene: item.ApplicableScene, AIEnabled: item.AIEnabled,
			ReplyEnabled: item.ReplyEnabled, SystemDefined: item.SystemDefined,
			Remark: item.Remark, SortNo: item.SortNo, Status: item.Status,
			CreatedAt: item.CreatedAt.Format(time.DateTime), UpdatedAt: item.UpdatedAt.Format(time.DateTime),
			Children: make([]*response.TagTreeResponse, 0),
		}
	}

	for i := range list {
		item := &list[i]
		node := nodeMap[item.ID]
		if item.ParentID == 0 {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodeMap[item.ParentID]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}

	return roots
}
