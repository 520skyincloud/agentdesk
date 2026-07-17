package dashboard

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ServiceAnalyticsGetExport(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionServiceAnalyticsExport)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	aggregate, err := services.ServiceAnalyticsService.GetOverview(serviceAnalyticsQueryFromContext(ctx), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", `attachment; filename="service-analytics.csv"`)
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(ctx.Writer)
	_ = writer.Write([]string{"类型", "维度", "指标", "值"})
	writeAnalyticsMetric := func(section, dimension, metric string, value any) {
		_ = writer.Write([]string{section, dimension, metric, fmt.Sprint(value)})
	}
	writeAnalyticsMetric("范围", "", "开始时间", aggregate.StartAt.Format(time.DateTime))
	writeAnalyticsMetric("范围", "", "结束时间", aggregate.EndAt.Format(time.DateTime))
	writeAnalyticsMetric("服务总览", "", "会话轮次", aggregate.Summary.SessionCount)
	writeAnalyticsMetric("服务总览", "", "独立客户", aggregate.Summary.UniqueCustomerCount)
	writeAnalyticsMetric("服务总览", "", "消息总量", aggregate.Summary.TotalMessageCount)
	writeAnalyticsMetric("服务总览", "", "进入人工池", aggregate.Summary.HumanQueueCount)
	writeAnalyticsMetric("服务总览", "", "成功分配", aggregate.Summary.AssignedCount)
	writeAnalyticsMetric("服务总览", "", "人工已回复", aggregate.Summary.HumanRepliedCount)
	writeAnalyticsMetric("服务总览", "", "人工未回复", aggregate.Summary.UnansweredCount)
	writeAnalyticsMetric("响应效率", "", "平均排队秒数", aggregate.Summary.AverageQueueSeconds)
	writeAnalyticsMetric("响应效率", "", "排队P50秒数", aggregate.Summary.P50QueueSeconds)
	writeAnalyticsMetric("响应效率", "", "排队P90秒数", aggregate.Summary.P90QueueSeconds)
	writeAnalyticsMetric("响应效率", "", "平均人工首响秒数", aggregate.Summary.AverageFirstReplySeconds)
	writeAnalyticsMetric("响应效率", "", "人工首响P50秒数", aggregate.Summary.P50FirstReplySeconds)
	writeAnalyticsMetric("响应效率", "", "人工首响P90秒数", aggregate.Summary.P90FirstReplySeconds)
	writeAnalyticsMetric("响应效率", "", "平均连续响应秒数", aggregate.Summary.AverageResponseSeconds)
	writeAnalyticsMetric("响应效率", "", "连续响应P50秒数", aggregate.Summary.P50ResponseSeconds)
	writeAnalyticsMetric("响应效率", "", "连续响应P90秒数", aggregate.Summary.P90ResponseSeconds)
	writeAnalyticsMetric("响应效率", "", "平均客户总等待秒数", aggregate.Summary.AverageHumanWaitSeconds)
	writeAnalyticsMetric("响应效率", "", "客户总等待P50秒数", aggregate.Summary.P50HumanWaitSeconds)
	writeAnalyticsMetric("响应效率", "", "客户总等待P90秒数", aggregate.Summary.P90HumanWaitSeconds)
	writeAnalyticsMetric("响应效率", "", "排队SLA达标率", aggregate.Summary.QueueSLARate)
	writeAnalyticsMetric("响应效率", "", "首响SLA达标率", aggregate.Summary.FirstReplySLARate)
	writeAnalyticsMetric("质检与满意度", "", "可质检分段", aggregate.Summary.QualityInspectableCount)
	writeAnalyticsMetric("质检与满意度", "", "已完成质检", aggregate.Summary.QualityInspectionCount)
	writeAnalyticsMetric("质检与满意度", "", "质检通过率", aggregate.Summary.QualityPassRate)
	writeAnalyticsMetric("质检与满意度", "", "评价邀请", aggregate.Summary.EvaluationInviteCount)
	writeAnalyticsMetric("质检与满意度", "", "评价提交", aggregate.Summary.EvaluationSubmittedCount)
	writeAnalyticsMetric("质检与满意度", "", "客户满意率", aggregate.Summary.SatisfactionRate)
	writeAnalyticsMetric("派单质量", "", "决策总数", aggregate.Dispatch.DecisionCount)
	writeAnalyticsMetric("派单质量", "", "自动派单", aggregate.Dispatch.AutoCount)
	writeAnalyticsMetric("派单质量", "", "人工派单", aggregate.Dispatch.ManualCount)
	writeAnalyticsMetric("派单质量", "", "降级", aggregate.Dispatch.FallbackCount)
	writeAnalyticsMetric("派单质量", "", "失败", aggregate.Dispatch.FailedCount)
	writeAnalyticsMetric("派单质量", "", "过期", aggregate.Dispatch.StaleCount)
	writeAnalyticsMetric("派单质量", "", "人工覆盖", aggregate.Dispatch.OverrideCount)
	for _, agent := range aggregate.Agents {
		dimension := agent.AgentName
		writeAnalyticsMetric("客服表现", dimension, "分配数", agent.AssignedCount)
		writeAnalyticsMetric("客服表现", dimension, "已回复分段", agent.RepliedCount)
		writeAnalyticsMetric("客服表现", dimension, "人工消息", agent.HumanMessageCount)
		writeAnalyticsMetric("客服表现", dimension, "平均首响秒数", agent.AverageFirstReplySeconds)
		writeAnalyticsMetric("客服表现", dimension, "首响P50秒数", agent.P50FirstReplySeconds)
		writeAnalyticsMetric("客服表现", dimension, "首响P90秒数", agent.P90FirstReplySeconds)
		writeAnalyticsMetric("客服表现", dimension, "平均响应秒数", agent.AverageResponseSeconds)
		writeAnalyticsMetric("客服表现", dimension, "响应P50秒数", agent.P50ResponseSeconds)
		writeAnalyticsMetric("客服表现", dimension, "响应P90秒数", agent.P90ResponseSeconds)
		writeAnalyticsMetric("客服表现", dimension, "服务秒数", agent.ServiceSeconds)
		writeAnalyticsMetric("客服表现", dimension, "在线秒数", agent.OnlineSeconds)
		writeAnalyticsMetric("客服表现", dimension, "空闲秒数", agent.IdleSeconds)
		writeAnalyticsMetric("客服表现", dimension, "忙碌秒数", agent.BusySeconds)
		writeAnalyticsMetric("客服表现", dimension, "休息秒数", agent.BreakSeconds)
		writeAnalyticsMetric("客服表现", dimension, "质检通过率", agent.QualityPassRate)
		writeAnalyticsMetric("客服表现", dimension, "客户满意率", agent.SatisfactionRate)
	}
	for _, source := range aggregate.Sources {
		dimension := strings.TrimSpace(source.StoreName + " / " + source.WxWorkEmployeeName)
		writeAnalyticsMetric("来源分析", dimension, "会话轮次", source.SessionCount)
		writeAnalyticsMetric("来源分析", dimension, "消息总量", source.MessageCount)
		writeAnalyticsMetric("来源分析", dimension, "进入人工池", source.HumanQueueCount)
		writeAnalyticsMetric("来源分析", dimension, "人工已回复", source.HumanRepliedCount)
		writeAnalyticsMetric("来源分析", dimension, "平均首响秒数", source.AverageFirstReply)
		writeAnalyticsMetric("来源分析", dimension, "质检覆盖率", source.QualityCoverageRate)
		writeAnalyticsMetric("来源分析", dimension, "质检通过率", source.QualityPassRate)
		writeAnalyticsMetric("来源分析", dimension, "质检均分", source.AverageQualityScore)
		writeAnalyticsMetric("来源分析", dimension, "评价参与率", source.EvaluationParticipationRate)
		writeAnalyticsMetric("来源分析", dimension, "客户满意率", source.SatisfactionRate)
	}
	writer.Flush()
}

func ServiceSessionPostAnnotate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordAnnotate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateServiceSessionAnnotationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ServiceAnalyticsService.UpdateSessionAnnotation(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceSession(item))
}

func ServiceSessionGetExport(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordExport)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	query := serviceSessionQueryFromContext(ctx)
	items, err := services.ServiceAnalyticsService.ExportSessions(query, operator, 10000)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Content-Type", "text/csv; charset=utf-8")
	ctx.Header("Content-Disposition", `attachment; filename="conversation-records.csv"`)
	ctx.Status(http.StatusOK)
	_, _ = ctx.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(ctx.Writer)
	_ = writer.Write([]string{
		"服务轮次ID", "会话ID", "轮次", "客户", "开始时间", "结束时间", "渠道", "门店", "企微员工号", "客服组", "客服",
		"客户消息", "AI消息", "人工消息", "排队秒数", "人工首响秒数", "解决状态", "咨询分类", "标签ID", "服务小记", "数据质量",
	})
	for i := range items {
		item := &items[i]
		built := builders.BuildServiceSession(item)
		tagIDs := make([]string, 0, len(built.TagIDs))
		for _, tagID := range built.TagIDs {
			tagIDs = append(tagIDs, strconv.FormatInt(tagID, 10))
		}
		_ = writer.Write([]string{
			strconv.FormatInt(item.ID, 10), strconv.FormatInt(item.ConversationID, 10), strconv.Itoa(item.SessionNo), built.CustomerName,
			item.StartedAt.Format(time.DateTime), formatCSVTime(item.EndedAt), built.ChannelName, built.StoreName, built.WxWorkEmployeeName,
			built.AssignedTeamName, built.AssignedAgentName, strconv.Itoa(item.CustomerMessageCount), strconv.Itoa(item.AIMessageCount),
			strconv.Itoa(item.HumanMessageCount), strconv.FormatInt(item.QueueSeconds, 10), strconv.FormatInt(item.FirstResponseSeconds, 10),
			item.ResolutionCode, item.CategoryCode, strings.Join(tagIDs, ","), item.SessionSummary, string(item.DataQuality),
		})
	}
	writer.Flush()
}

func QualitySamplingPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualitySamplingCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateQualitySamplingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QualityInspectionService.CreateSamplingBatch(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQualitySamplingBatch(item))
}

func QualitySamplingAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "name", Op: params.Like},
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "createdBy"},
		params.QueryFilter{ParamName: "startAt", ColumnName: "created_at", Op: params.Gte},
	).Desc("created_at").Desc("id")
	if value := params.GetTime(ctx, "endAt"); value != nil {
		cnd.Lte("created_at", endOfRequestedDay(*value, params.FormValue(ctx, "endAt")))
	}
	items, paging, err := services.QualityInspectionService.ListSamplingBatches(cnd, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := make([]response.QualitySamplingBatchResponse, 0, len(items))
	for i := range items {
		results = append(results, *builders.BuildQualitySamplingBatch(&items[i]))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func QualitySamplingGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QualityInspectionService.GetSamplingBatch(id, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQualitySamplingBatch(item))
}

func ReportViewPresetAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionReportViewPresetManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	items, err := services.ReportViewPresetService.List(params.FormValue(ctx, "pageCode"), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret := make([]any, 0, len(items))
	for _, item := range items {
		ret = append(ret, builders.BuildReportViewPreset(item))
	}
	httpx.WriteJSON(ctx, ret)
}

func ReportViewPresetPostSave(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionReportViewPresetManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SaveReportViewPresetRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ReportViewPresetService.Save(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReportViewPreset(*item))
}

func ReportViewPresetPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionReportViewPresetManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReportViewPresetService.Delete(params.FormValueInt64Default(ctx, "id", 0), operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func AgentPresenceGetCurrent(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentPresenceUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.AgentPresenceService.GetCurrent(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildAgentPresence(item))
}

func AgentPresencePostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentPresenceUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateAgentPresenceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.AgentPresenceService.SetStatus(operator, enums.AgentPresenceStatus(req.Status), req.BreakReason, time.Now())
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildAgentPresence(item))
}

func ConversationEvaluationPostInvite(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationEvaluationInvite)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.InviteConversationEvaluationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ConversationEvaluationService.Invite(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildConversationEvaluationInvite(item))
}

func ConversationEvaluationAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationEvaluationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "conversationId"},
		params.QueryFilter{ParamName: "assignmentId"},
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "rating"},
		params.QueryFilter{ParamName: "startAt", ColumnName: "invited_at", Op: params.Gte},
	).Desc("invited_at").Desc("id")
	if value := params.GetTime(ctx, "endAt"); value != nil {
		cnd.Lte("invited_at", endOfRequestedDay(*value, params.FormValue(ctx, "endAt")))
	}
	items, paging, err := services.ConversationEvaluationService.List(
		cnd,
		params.FormValueInt64Default(ctx, "teamId", 0),
		params.FormValueInt64Default(ctx, "agentId", 0),
		operator,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := make([]response.ConversationEvaluationResponse, 0, len(items))
	for _, item := range items {
		results = append(results, builders.BuildConversationEvaluation(item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func serviceAnalyticsQueryFromContext(ctx *gin.Context) services.ServiceAnalyticsQuery {
	query := services.ServiceAnalyticsQuery{
		TeamID: params.FormValueInt64Default(ctx, "teamId", 0), SquadID: params.FormValueInt64Default(ctx, "squadId", 0),
		AgentID: params.FormValueInt64Default(ctx, "agentId", 0), StoreID: params.FormValueInt64Default(ctx, "storeId", 0),
		WxWorkInstanceID: params.FormValueInt64Default(ctx, "wxWorkInstanceId", 0), DataQuality: params.FormValue(ctx, "dataQuality"),
	}
	if value := params.GetTime(ctx, "startAt"); value != nil {
		query.StartAt = *value
	}
	if value := params.GetTime(ctx, "endAt"); value != nil {
		query.EndAt = endOfRequestedDay(*value, params.FormValue(ctx, "endAt"))
	}
	return query
}

func formatCSVTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.DateTime)
}
