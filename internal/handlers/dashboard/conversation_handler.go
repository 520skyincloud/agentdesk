package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/i18nx"
	"agent-desk/internal/services"
	"strconv"
	"strings"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/web"
)

func ConversationAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "serviceMode"},
		params.QueryFilter{ParamName: "currentAssigneeId"},
	).Desc("last_message_at").Desc("id")
	cnd = services.AgentTeamScopeService.ApplyConversationFilter(cnd, operator)

	paging := params.GetPaging(ctx)

	if keyword, _ := params.Get(ctx, "keyword"); strs.IsNotBlank(keyword) {
		keywordLike := "%" + strings.TrimSpace(keyword) + "%"
		cnd.Where("customer_name LIKE ? OR last_message_summary LIKE ?", keywordLike, keywordLike)
	}

	// 标签搜索
	if tagID, _ := params.GetInt64(ctx, "tagId"); tagID > 0 {
		tenantID := services.AgentTeamScopeService.ActiveTenantID(operator)
		tagIDs := services.TagService.GetSelfAndDescendantIDsInTenant(tagID, tenantID)
		if len(tagIDs) == 0 {
			httpx.WriteJSON(ctx, &web.PageResult{
				Results: []response.ConversationResponse{},
				Page:    paging,
			})
			return
		}
		cnd = services.CustomerTagService.ApplyConversationFilter(cnd, tenantID, tagIDs)
	}
	if agentTeamID, _ := params.GetInt64(ctx, "agentTeamId"); agentTeamID > 0 {
		userIDs := services.AgentProfileService.GetUserIDsByTeamIDInTenant(agentTeamID, services.AgentTeamScopeService.ActiveTenantID(operator))
		if len(userIDs) == 0 {
			httpx.WriteJSON(ctx, &web.PageResult{
				Results: []response.ConversationResponse{},
				Page:    paging,
			})
			return
		}
		cnd.In("current_assignee_id", userIDs)
	}

	list, paging := services.ConversationService.FindPageByCnd(cnd)
	customerTags := services.CustomerTagService.ListForConversations(list)
	results := make([]response.ConversationResponse, 0, len(list))
	for _, item := range list {
		result := builders.BuildConversationWithLocale(&item, i18nx.Locale(ctx))
		result.CustomerTags = customerTags[item.ID]
		results = append(results, result)
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func ConversationAnyConversations(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	filterValue, _ := params.Get(ctx, "filter")
	keyword, _ := params.Get(ctx, "keyword")
	wxWorkInstanceID, _ := params.GetInt64(ctx, "wxWorkInstanceId")
	paging := params.GetPaging(ctx)

	list, paging, err := services.ConversationService.ListConversations(
		operator,
		request.AgentConversationFilter(strings.TrimSpace(filterValue)),
		keyword,
		wxWorkInstanceID,
		paging,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	results := make([]response.ConversationResponse, 0, len(list))
	customerTags := services.CustomerTagService.ListForConversations(list)
	for _, item := range list {
		result := builders.BuildConversationWithLocale(&item, i18nx.Locale(ctx))
		result.CustomerTags = customerTags[item.ID]
		results = append(results, result)
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func ConversationGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	if !services.AgentTeamScopeService.CanViewConversation(operator, id) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("会话不存在"))
		return
	}
	item := services.ConversationService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("会话不存在"))
		return
	}

	canViewRelated := services.AuthService.HasPermission(ctx, constants.PermissionConversationRelatedView.Code)
	canViewHistory := false
	if canViewRelated {
		canViewHistory, err = services.ConversationHistoryService.CanViewLineage(item, operator)
		if err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
	}
	historySegments, err := services.ConversationHistoryService.ListCurrentSegments(item)
	if canViewHistory {
		historySegments, err = services.ConversationHistoryService.ListSegments(item)
	}
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	detail := response.ConversationDetailResponse{
		ConversationResponse: builders.BuildConversationWithLocale(item, i18nx.Locale(ctx)),
		TakeoverState:        services.ConversationTakeoverService.ResolveState(item, operator),
		Participants:         builders.BuildParticipantResponses(id, item.TenantID),
		ChannelSessions:      builders.BuildConversationChannelSessions(services.ConversationChannelSessionService.ListInTenant(id, item.TenantID)),
		HistorySegments:      builders.BuildConversationHistorySegments(historySegments),
	}
	if canViewRelated {
		detail.ContinuityLinks = builders.BuildConversationContinuityLinks(services.ConversationService.ListConversationContinuityLinks(item, operator))
		related := services.ConversationService.ListRelatedStoreConversations(item, operator)
		detail.RelatedConversations = make([]response.ConversationResponse, 0, len(related))
		for i := range related {
			detail.RelatedConversations = append(detail.RelatedConversations, builders.BuildConversationWithLocale(&related[i], i18nx.Locale(ctx)))
		}
	}
	detail.CustomerTags = services.CustomerTagService.ListForConversation(id)
	httpx.WriteJSON(ctx, detail)
}

func ConversationPostInherit(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationInherit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.InheritStoreConversationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ConversationService.InheritStoreConversation(req, operator, httpx.GetRequestID(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildConversationWithLocale(item, i18nx.Locale(ctx)))
}

func ConversationPostInheritPreview(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationInherit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.PreviewStoreConversationInheritanceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ConversationService.PreviewStoreConversationInheritance(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ConversationPostInheritBatch(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationInherit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BatchInheritStoreConversationsRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ConversationService.BatchInheritStoreConversations(req, operator, httpx.GetRequestID(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ConversationAnyMessage_list(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	var (
		conversationID, _ = params.GetInt64(ctx, "conversationId")
		senderType, _     = params.Get(ctx, "senderType")
		messageType, _    = params.Get(ctx, "messageType")
		cursorText, _     = params.Get(ctx, "cursor")
		includeHistory, _ = params.Get(ctx, "includeHistory")
		limit, _          = params.GetInt(ctx, "limit")
	)
	if !services.AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("会话不存在"))
		return
	}

	if (includeHistory == "1" || strings.EqualFold(includeHistory, "true")) &&
		services.AuthService.HasPermission(ctx, constants.PermissionConversationRelatedView.Code) {
		conversation := services.ConversationService.Get(conversationID)
		if conversation == nil {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("会话不存在"))
			return
		}
		canViewHistory, scopeErr := services.ConversationHistoryService.CanViewLineage(conversation, operator)
		if scopeErr != nil {
			httpx.WriteJSON(ctx, scopeErr)
			return
		}
		if canViewHistory {
			list, nextCursor, hasMore, historyErr := services.ConversationHistoryService.ListMessages(
				conversation, cursorText, limit, senderType, messageType,
			)
			if historyErr != nil {
				httpx.WriteJSON(ctx, historyErr)
				return
			}
			results := builders.BuildConversationHistoryMessagesWithLocale(list, i18nx.Locale(ctx))
			httpx.WriteJSON(ctx, httpx.CursorData(results, nextCursor, hasMore))
			return
		}
	}

	cursor, _ := strconv.ParseInt(strings.TrimSpace(cursorText), 10, 64)
	list, nextCursor, hasMore := services.MessageService.FindByConversationIDCursor(
		conversationID, cursor, limit, senderType, messageType,
	)
	results := builders.BuildMessagesWithLocale(list, i18nx.Locale(ctx))
	httpx.WriteJSON(ctx, httpx.CursorData(results, strconv.FormatInt(nextCursor, 10), hasMore))
}

func ConversationPostAssign(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationAssign)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.AssignConversationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.AssignConversation(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostTakeover_request(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.RequestConversationTakeoverRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err := services.ConversationTakeoverService.Request(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostTakeover_direct(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationAssign)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.RequestConversationTakeoverRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationTakeoverService.DirectTakeover(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostTakeover_review(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationAssign)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReviewConversationTakeoverRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationTakeoverService.Review(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostResume_ai(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ResumeConversationAIRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationTakeoverService.ResumeAI(req.ConversationID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostTransfer(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationTransfer)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.TransferConversationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.TransferConversation(req.ConversationID, req.ToUserID, req.Reason, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostSet_auto_handoff_enabled(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SetConversationAutoHandoffEnabledRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkCustomerHandoffSettingService.SetForConversation(req.ConversationID, req.AutoHandoffEnabled, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostClose(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationClose)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CloseConversationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.CloseConversation(req.ConversationID, req.CloseReason, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostLink_customer(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationLinkCustomer)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.LinkConversationCustomerRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.LinkConversationCustomer(req.ConversationID, req.CustomerID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostSend_message(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.SendConversationMessageRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.EnsureAgentCanReply(req.ConversationID, "关闭AI回复后网页端直接回复", operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.MessageService.SendAgentMessageWithRequestID(req.ConversationID, 0, req.ClientMsgID, req.MessageType, req.Content, req.Payload, operator, httpx.GetRequestID(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildMessageWithLocale(item, i18nx.Locale(ctx)))
}

func ConversationPostSend_arrival_binding_card(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SendArrivalBindingCardRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ArrivalBindingTicketService.SendBindingCardForConversation(
		req.ConversationID,
		operator,
		httpx.GetRequestID(ctx),
	); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostRecall_message(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.RecallConversationMessageRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.MessageService.RecallAgentMessage(req.MessageID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildMessageWithLocale(item, i18nx.Locale(ctx)))
}

func ConversationPostRead(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.ReadConversationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationService.MarkAgentConversationReadToMessage(req.ConversationID, req.MessageID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostUpload_image(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	rawConv := strings.TrimSpace(params.FormValue(ctx, "conversationId"))
	if rawConv == "" {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("conversationId不能为空"))
		return
	}
	conversationID, err := strconv.ParseInt(rawConv, 10, 64)
	if err != nil || conversationID <= 0 {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("conversationId不能为空"))
		return
	}
	if err := services.ConversationService.EnsureAgentCanReply(conversationID, "关闭AI回复后网页端上传图片", operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	header, err := ctx.FormFile("file")
	if err != nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("请选择上传图片"))
		return
	}
	if !strings.HasPrefix(strings.ToLower(header.Header.Get("Content-Type")), "image/") {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("仅支持上传图片文件"))
		return
	}

	item, err := services.AssetService.UploadFileInTenant(header, "images", operator.ActiveTenantID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildAsset(item))
}

func ConversationPostUpload_attachment(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	rawConv := strings.TrimSpace(params.FormValue(ctx, "conversationId"))
	if rawConv == "" {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("conversationId不能为空"))
		return
	}
	conversationID, err := strconv.ParseInt(rawConv, 10, 64)
	if err != nil || conversationID <= 0 {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("conversationId不能为空"))
		return
	}
	if err := services.ConversationService.EnsureAgentCanReply(conversationID, "关闭AI回复后网页端上传附件", operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	header, err := ctx.FormFile("file")
	if err != nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("请选择上传附件"))
		return
	}
	item, err := services.AssetService.UploadFileInTenant(header, "attachments", operator.ActiveTenantID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildAsset(item))
}

func ConversationGetCustomer_tag_options(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionCustomerTagManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	conversationID, _ := params.GetInt64(ctx, "conversationId")
	list, err := services.CustomerTagService.ListOptionsForConversation(conversationID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTagResponses(list))
}

func ConversationAnyCustomer_tag_change_log(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionCustomerTagManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	conversationID, _ := params.GetInt64(ctx, "conversationId")
	paging := params.GetPaging(ctx)
	list, resultPaging, err := services.CustomerTagService.ListChangeLogsForConversation(
		conversationID, paging.Page, paging.Limit, operator,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: list, Page: resultPaging})
}

func ConversationPostCustomer_tag_add(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionCustomerTagManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.AddCustomerTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.CustomerTagService.ManualAdd(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostCustomer_tag_remove(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionCustomerTagManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.RemoveCustomerTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.CustomerTagService.ManualRemove(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationPostCustomer_tag_replace(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionCustomerTagManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReplaceCustomerTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.CustomerTagService.ManualReplace(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
