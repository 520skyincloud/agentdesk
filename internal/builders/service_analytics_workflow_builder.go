package builders

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
)

func BuildQualitySamplingBatch(aggregate *services.QualitySamplingAggregate) *response.QualitySamplingBatchResponse {
	if aggregate == nil {
		return nil
	}
	ret := &response.QualitySamplingBatchResponse{
		ID: aggregate.Batch.ID, Name: aggregate.Batch.Name, CriteriaJSON: aggregate.Batch.CriteriaJSON,
		Seed: aggregate.Batch.Seed, SampleSize: aggregate.Batch.SampleSize, Status: string(aggregate.Batch.Status),
		CreatedBy: aggregate.Batch.CreatedBy, CreatedAt: utils.FormatTime(aggregate.Batch.CreatedAt),
		CompletedAt: utils.FormatTimePtr(aggregate.Batch.CompletedAt),
		Items:       make([]response.QualitySamplingItemResponse, 0, len(aggregate.Items)),
	}
	for _, item := range aggregate.Items {
		ret.Items = append(ret.Items, response.QualitySamplingItemResponse{
			AssignmentID: item.AssignmentID, ConversationID: item.ConversationID,
			SessionNo: item.SessionNo, AgentID: item.AgentID, InspectionID: item.InspectionID,
		})
	}
	return ret
}

func BuildReportViewPreset(item models.ReportViewPreset) response.ReportViewPresetResponse {
	return response.ReportViewPresetResponse{
		ID: item.ID, PageCode: item.PageCode, Name: item.Name, FiltersJSON: item.FiltersJSON,
		ColumnsJSON: item.ColumnsJSON, SortJSON: item.SortJSON, IsDefault: item.IsDefault,
	}
}

func BuildAgentPresence(item *models.AgentPresenceSession) *response.AgentPresenceResponse {
	if item == nil {
		return &response.AgentPresenceResponse{Status: "offline"}
	}
	return &response.AgentPresenceResponse{
		Status: string(item.Status), BreakReason: item.BreakReason,
		StartedAt: utils.FormatTime(item.StartedAt), LastSeenAt: utils.FormatTime(item.LastSeenAt),
	}
}

func BuildConversationEvaluation(item models.ConversationEvaluation) response.ConversationEvaluationResponse {
	tags := []string{}
	_ = json.Unmarshal([]byte(item.TagCodesJSON), &tags)
	return response.ConversationEvaluationResponse{
		ID: item.ID, ConversationID: item.ConversationID, SessionNo: item.SessionNo,
		AssignmentID: item.AssignmentID, CustomerID: item.CustomerID, Status: string(item.Status),
		InviteChannel: item.InviteChannel, InvitedAt: utils.FormatTime(item.InvitedAt),
		ExpiresAt: utils.FormatTime(item.ExpiresAt), SubmittedAt: utils.FormatTimePtr(item.SubmittedAt),
		Rating: item.Rating, TagCodes: tags, Comment: item.Comment,
	}
}

func BuildConversationEvaluationInvite(item *services.ConversationEvaluationInvite) *response.ConversationEvaluationInviteResponse {
	if item == nil {
		return nil
	}
	return &response.ConversationEvaluationInviteResponse{
		Evaluation: BuildConversationEvaluation(item.Evaluation),
		Path:       item.Path,
	}
}

func BuildPublicConversationEvaluation(item *services.PublicConversationEvaluation) *response.PublicConversationEvaluationResponse {
	if item == nil {
		return nil
	}
	return &response.PublicConversationEvaluationResponse{
		Status: string(item.Evaluation.Status), CompanyName: item.CompanyName,
		ExpiresAt:   utils.FormatTime(item.Evaluation.ExpiresAt),
		SubmittedAt: utils.FormatTimePtr(item.Evaluation.SubmittedAt), Rating: item.Evaluation.Rating,
	}
}
