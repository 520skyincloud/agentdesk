package services

import (
	"context"
	"strings"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TicketService = newTicketService()

func newTicketService() *ticketService {
	return &ticketService{}
}

type TicketDetailAggregate struct {
	Ticket     *models.Ticket
	Tags       []models.Tag
	Customer   *models.Customer
	Progresses []models.TicketProgress
	Users      map[int64]*models.User
}

type TicketSummaryAggregate struct {
	All        int64
	Pending    int64
	InProgress int64
	Done       int64
	Unassigned int64
	Mine       int64
	Stale      int64
}

type TicketListAggregate struct {
	List           []models.Ticket
	Paging         *sqls.Paging
	TagsByTicketID map[int64][]models.Tag
	Users          map[int64]*models.User
	Customers      map[int64]*models.Customer
}

type ticketService struct {
}

func normalizeTicketStaleHours(staleHours int) int {
	switch staleHours {
	case 24, 48, 168:
		return staleHours
	default:
		return 24
	}
}

func normalizeTicketCategory(category string) string {
	switch strings.TrimSpace(category) {
	case "delivery", "cleaning", "maintenance", "wake_up", "luggage", "human_decision":
		return strings.TrimSpace(category)
	default:
		return "general"
	}
}

func normalizeTicketPriority(priority string) string {
	switch strings.TrimSpace(priority) {
	case "low", "normal", "high", "urgent":
		return strings.TrimSpace(priority)
	default:
		return "normal"
	}
}

func buildTicketAssignmentProgressContent(fromUser *models.User, toUser *models.User, reason string) string {
	fromName := ticketAssignmentUserDisplayName(fromUser)
	if fromName == "" {
		fromName = "未分配"
	}
	toName := ticketAssignmentUserDisplayName(toUser)
	if toName == "" && toUser != nil {
		toName = toUser.Username
	}
	content := "指派处理人：" + fromName + " -> " + toName
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		content += "，原因：" + trimmedReason
	}
	return content
}

func ticketAssignmentUserDisplayName(user *models.User) string {
	if user == nil {
		return ""
	}
	if strings.TrimSpace(user.Nickname) != "" {
		return strings.TrimSpace(user.Nickname)
	}
	return strings.TrimSpace(user.Username)
}

func (s *ticketService) Get(id int64) *models.Ticket {
	return repositories.TicketRepository.Get(sqls.DB(), id)
}

func (s *ticketService) Take(where ...any) *models.Ticket {
	return repositories.TicketRepository.Take(sqls.DB(), where...)
}

func (s *ticketService) Find(cnd *sqls.Cnd) []models.Ticket {
	return repositories.TicketRepository.Find(sqls.DB(), cnd)
}

func (s *ticketService) FindOne(cnd *sqls.Cnd) *models.Ticket {
	return repositories.TicketRepository.FindOne(sqls.DB(), cnd)
}

func (s *ticketService) FindPageByParams(params *params.QueryParams) (list []models.Ticket, paging *sqls.Paging) {
	return repositories.TicketRepository.FindPageByParams(sqls.DB(), params)
}

func (s *ticketService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Ticket, paging *sqls.Paging) {
	return repositories.TicketRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *ticketService) FindPageAggregateByCnd(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (*TicketListAggregate, error) {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return nil, err
	}
	list, paging := repositories.TicketRepository.FindPageByCnd(sqls.DB(), cnd.Eq("tenant_id", tenantID))
	return s.buildTicketListAggregate(sqls.DB(), list, paging, tenantID), nil
}

func (s *ticketService) ApplyStaleFilter(cnd *sqls.Cnd, staleHours int) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	staleHour := normalizeTicketStaleHours(staleHours)
	return cnd.
		NotEq("status", enums.TicketStatusDone).
		Where("updated_at < ?", time.Now().Add(-time.Duration(staleHour)*time.Hour))
}

func (s *ticketService) Count(cnd *sqls.Cnd) int64 {
	return repositories.TicketRepository.Count(sqls.DB(), cnd)
}

func (s *ticketService) Create(t *models.Ticket) error {
	return repositories.TicketRepository.Create(sqls.DB(), t)
}

func (s *ticketService) Update(t *models.Ticket) error {
	return repositories.TicketRepository.Update(sqls.DB(), t)
}

func (s *ticketService) Updates(id int64, columns map[string]any) error {
	return repositories.TicketRepository.Updates(sqls.DB(), id, columns)
}

func (s *ticketService) UpdateColumn(id int64, name string, value any) error {
	return repositories.TicketRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *ticketService) Delete(id int64) {
	repositories.TicketRepository.Delete(sqls.DB(), id)
}

func (s *ticketService) GetTags(ticketID int64) []models.Tag {
	ticket := s.Get(ticketID)
	if ticket == nil {
		return nil
	}
	return s.GetTagsInTenant(ticketID, ticket.TenantID)
}

func (s *ticketService) GetTagsInTenant(ticketID, tenantID int64) []models.Tag {
	if ticketID <= 0 || tenantID <= 0 {
		return nil
	}
	relations := TicketTagService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("ticket_id", ticketID).Asc("id"))
	if len(relations) == 0 {
		return nil
	}
	tagIDs := make([]int64, 0, len(relations))
	for i := range relations {
		tagIDs = append(tagIDs, relations[i].TagID)
	}
	tags := repositories.TagRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("id", tagIDs))
	if len(tags) <= 1 {
		return tags
	}
	tagMap := make(map[int64]models.Tag, len(tags))
	for i := range tags {
		tagMap[tags[i].ID] = tags[i]
	}
	ordered := make([]models.Tag, 0, len(relations))
	for _, tagID := range tagIDs {
		if tag, ok := tagMap[tagID]; ok {
			ordered = append(ordered, tag)
		}
	}
	return ordered
}

func (s *ticketService) CreateTicket(req request.CreateTicketRequest, operator *dto.AuthPrincipal) (*models.Ticket, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID, err := s.resolveCreateTenantID(req.ConversationID, operator)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)
	if title == "" {
		return nil, errorsx.InvalidParam("工单标题不能为空")
	}
	if description == "" {
		return nil, errorsx.InvalidParam("工单描述不能为空")
	}
	source := enums.TicketSource(strings.TrimSpace(req.Source))
	if source == "" {
		source = enums.TicketSourceManual
	}
	if !enums.IsValidTicketSource(string(source)) {
		return nil, errorsx.InvalidParam("工单来源不合法")
	}
	if err := s.validateTicketRefs(req.CustomerID, req.ConversationID, req.CurrentAssigneeID, tenantID); err != nil {
		return nil, err
	}
	tagIDs, err := TicketTagService.ValidateTagIDs(req.TagIDs, tenantID)
	if err != nil {
		return nil, err
	}

	ticket := &models.Ticket{
		TenantID:          tenantID,
		Title:             title,
		Description:       description,
		Category:          normalizeTicketCategory(req.Category),
		Priority:          normalizeTicketPriority(req.Priority),
		RoomNo:            strings.TrimSpace(req.RoomNo),
		Source:            source,
		Channel:           strings.TrimSpace(req.Channel),
		CustomerID:        req.CustomerID,
		ConversationID:    req.ConversationID,
		Status:            enums.TicketStatusPending,
		CurrentAssigneeID: req.CurrentAssigneeID,
		AuditFields:       utils.BuildAuditFields(operator),
	}

	ticketNo, err := TicketNoSequenceService.Next(ticket.CreatedAt)
	if err != nil {
		return nil, err
	}

	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		ticket.TicketNo = ticketNo
		if err := repositories.TicketRepository.Create(ctx.Tx, ticket); err != nil {
			return err
		}
		if err := TicketTagService.ReplaceTicketTags(ctx.Tx, ticket.ID, tenantID, tagIDs, operator); err != nil {
			return err
		}
		return repositories.TicketProgressRepository.Create(ctx.Tx, &models.TicketProgress{
			TenantID:  tenantID,
			TicketID:  ticket.ID,
			Content:   "创建工单",
			AuthorID:  operator.UserID,
			CreatedAt: time.Now(),
		})
	}); err != nil {
		return nil, err
	}

	eventbus.PublishAsync(context.Background(), events.TicketCreatedEvent{
		TicketID:   ticket.ID,
		OperatorID: operator.UserID,
	})
	return repositories.TicketRepository.GetInTenant(sqls.DB(), ticket.ID, tenantID), nil
}

func (s *ticketService) CreateFromConversation(req request.CreateTicketFromConversationRequest, operator *dto.AuthPrincipal) (*models.Ticket, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	var conversation *models.Conversation
	if tenantID := AgentTeamScopeService.ActiveTenantID(operator); tenantID > 0 {
		conversation = repositories.ConversationRepository.GetInTenant(sqls.DB(), req.ConversationID, tenantID)
	} else if operator.UserID == 0 {
		conversation = repositories.ConversationRepository.Get(sqls.DB(), req.ConversationID)
	} else {
		return nil, errorsx.Forbidden("请先进入需要管理工单的接入公司")
	}
	if conversation == nil {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	if conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话缺少接入公司归属")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(ConversationService.BuildConversationSummary(conversation))
	}
	if title == "" {
		title = "会话工单"
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = strings.TrimSpace(conversation.LastMessageSummary)
	}
	if description == "" {
		description = title
	}
	scopedOperator := *operator
	scopedOperator.ActiveTenantID = conversation.TenantID
	return s.CreateTicket(request.CreateTicketRequest{
		Title:             title,
		Description:       description,
		Category:          req.Category,
		Priority:          req.Priority,
		RoomNo:            req.RoomNo,
		Source:            string(enums.TicketSourceConversation),
		Channel:           s.resolveConversationChannel(conversation),
		CustomerID:        conversation.CustomerID,
		ConversationID:    conversation.ID,
		TagIDs:            req.TagIDs,
		CurrentAssigneeID: req.CurrentAssigneeID,
	}, &scopedOperator)
}

func (s *ticketService) UpdateTicket(req request.UpdateTicketRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return err
	}
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)
	if title == "" {
		return errorsx.InvalidParam("工单标题不能为空")
	}
	if description == "" {
		return errorsx.InvalidParam("工单描述不能为空")
	}
	ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), req.TicketID, tenantID)
	if ticket == nil {
		return errorsx.InvalidParam("工单不存在")
	}
	if err := s.validateAssignee(req.CurrentAssigneeID, tenantID); err != nil {
		return err
	}
	tagIDs, err := TicketTagService.ValidateTagIDs(req.TagIDs, tenantID)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.TicketRepository.UpdatesInTenant(ctx.Tx, ticket.ID, tenantID, map[string]any{
			"title":               title,
			"description":         description,
			"category":            normalizeTicketCategory(req.Category),
			"priority":            normalizeTicketPriority(req.Priority),
			"room_no":             strings.TrimSpace(req.RoomNo),
			"current_assignee_id": req.CurrentAssigneeID,
			"updated_at":          now,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
		}); err != nil {
			return err
		}
		return TicketTagService.ReplaceTicketTags(ctx.Tx, ticket.ID, tenantID, tagIDs, operator)
	})
}

func (s *ticketService) LinkCustomer(ticketID int64, customerID int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return err
	}
	ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), ticketID, tenantID)
	if ticket == nil {
		return errorsx.InvalidParam("工单不存在")
	}
	if customerID <= 0 || repositories.CustomerRepository.GetInTenant(sqls.DB(), customerID, tenantID) == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	if ticket.ConversationID > 0 {
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), ticket.ConversationID, tenantID)
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if conversation.CustomerID > 0 && conversation.CustomerID != customerID {
			return errorsx.InvalidParam("会话与客户不匹配")
		}
	}
	now := time.Now()
	return repositories.TicketRepository.UpdatesInTenant(sqls.DB(), ticket.ID, tenantID, map[string]any{
		"customer_id":      customerID,
		"updated_at":       now,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}

func (s *ticketService) AssignTicket(req request.AssignTicketRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return err
	}
	var assignedEvent *events.TicketAssignedEvent
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		event, err := s.assignTicketTx(ctx.Tx, req, tenantID, operator)
		if err != nil {
			return err
		}
		assignedEvent = event
		return nil
	}); err != nil {
		return err
	}
	if assignedEvent != nil {
		eventbus.PublishAsync(context.Background(), *assignedEvent)
	}
	return nil
}

func (s *ticketService) ChangeStatus(req request.ChangeTicketStatusRequest, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return err
	}
	status := strings.TrimSpace(req.Status)
	if !enums.IsValidTicketStatus(status) {
		return errorsx.InvalidParam("工单状态不合法")
	}
	ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), req.TicketID, tenantID)
	if ticket == nil {
		return errorsx.InvalidParam("工单不存在")
	}
	now := time.Now()
	var handledAt *time.Time
	if enums.TicketStatus(status) == enums.TicketStatusDone {
		handledAt = &now
	}
	return repositories.TicketRepository.UpdatesInTenant(sqls.DB(), ticket.ID, tenantID, map[string]any{
		"status":           enums.TicketStatus(status),
		"handled_at":       handledAt,
		"updated_at":       now,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}

func (s *ticketService) AddProgress(req request.CreateTicketProgressRequest, operator *dto.AuthPrincipal) (*models.TicketProgress, error) {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errorsx.InvalidParam("处理进展不能为空")
	}
	ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), req.TicketID, tenantID)
	if ticket == nil {
		return nil, errorsx.InvalidParam("工单不存在")
	}
	now := time.Now()
	progress := &models.TicketProgress{
		TenantID:  tenantID,
		TicketID:  ticket.ID,
		Content:   content,
		AuthorID:  operator.UserID,
		CreatedAt: now,
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.TicketProgressRepository.Create(ctx.Tx, progress); err != nil {
			return err
		}
		return repositories.TicketRepository.UpdatesInTenant(ctx.Tx, ticket.ID, tenantID, map[string]any{
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		})
	}); err != nil {
		return nil, err
	}
	return progress, nil
}

func (s *ticketService) ListProgress(ticketID int64, operator *dto.AuthPrincipal) ([]models.TicketProgress, error) {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return nil, err
	}
	if repositories.TicketRepository.GetInTenant(sqls.DB(), ticketID, tenantID) == nil {
		return nil, errorsx.InvalidParam("工单不存在")
	}
	return repositories.TicketProgressRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("ticket_id", ticketID).
		Asc("id")), nil
}

func (s *ticketService) GetDetail(id int64, operator *dto.AuthPrincipal) (*TicketDetailAggregate, error) {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return nil, err
	}
	ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), id, tenantID)
	if ticket == nil {
		return nil, errorsx.InvalidParam("工单不存在")
	}
	aggregate := &TicketDetailAggregate{
		Ticket:     ticket,
		Tags:       s.GetTagsInTenant(id, tenantID),
		Progresses: repositories.TicketProgressRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("ticket_id", id).Asc("id")),
		Users:      make(map[int64]*models.User),
	}
	if ticket.CustomerID > 0 {
		aggregate.Customer = repositories.CustomerRepository.GetInTenant(sqls.DB(), ticket.CustomerID, tenantID)
	}
	userIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	addUserID := func(userID int64) {
		if userID <= 0 {
			return
		}
		if _, ok := seen[userID]; ok {
			return
		}
		seen[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}
	addUserID(ticket.CurrentAssigneeID)
	for i := range aggregate.Progresses {
		addUserID(aggregate.Progresses[i].AuthorID)
	}
	if len(userIDs) > 0 {
		users := repositories.UserRepository.FindByIdsInTenant(sqls.DB(), userIDs, tenantID)
		for i := range users {
			item := users[i]
			aggregate.Users[item.ID] = &item
		}
	}
	return aggregate, nil
}

func (s *ticketService) GetSummary(operator *dto.AuthPrincipal, staleHours ...int) *TicketSummaryAggregate {
	summary, err := s.GetSummaryForOperator(operator, staleHours...)
	if err != nil {
		return &TicketSummaryAggregate{}
	}
	return summary
}

func (s *ticketService) GetSummaryForOperator(operator *dto.AuthPrincipal, staleHours ...int) (*TicketSummaryAggregate, error) {
	tenantID, err := requireActiveTenantID(operator, "工单")
	if err != nil {
		return nil, err
	}
	staleHour := 0
	if len(staleHours) > 0 {
		staleHour = staleHours[0]
	}
	summary := &TicketSummaryAggregate{
		All:        s.Count(sqls.NewCnd().Eq("tenant_id", tenantID)),
		Pending:    s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.TicketStatusPending)),
		InProgress: s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.TicketStatusInProgress)),
		Done:       s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.TicketStatusDone)),
		Unassigned: s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("current_assignee_id", 0)),
		Stale:      s.Count(s.ApplyStaleFilter(sqls.NewCnd().Eq("tenant_id", tenantID), staleHour)),
	}
	summary.Mine = s.Count(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("current_assignee_id", operator.UserID))
	return summary, nil
}

func (s *ticketService) assignTicketTx(tx *gorm.DB, req request.AssignTicketRequest, tenantID int64, operator *dto.AuthPrincipal) (*events.TicketAssignedEvent, error) {
	ticket := repositories.TicketRepository.GetInTenant(tx, req.TicketID, tenantID)
	if ticket == nil {
		return nil, errorsx.InvalidParam("工单不存在")
	}
	if err := s.validateRequiredAssignee(req.ToUserID, tenantID); err != nil {
		return nil, err
	}
	toUser := repositories.UserRepository.GetInTenant(tx, req.ToUserID, tenantID)
	if toUser == nil || toUser.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("负责人不存在")
	}
	var fromUser *models.User
	if ticket.CurrentAssigneeID > 0 {
		fromUser = repositories.UserRepository.GetInTenant(tx, ticket.CurrentAssigneeID, tenantID)
	}
	now := time.Now()
	if err := repositories.TicketRepository.UpdatesInTenant(tx, ticket.ID, tenantID, map[string]any{
		"current_assignee_id": req.ToUserID,
		"updated_at":          now,
		"update_user_id":      operator.UserID,
		"update_user_name":    operator.Username,
	}); err != nil {
		return nil, err
	}
	if err := repositories.TicketProgressRepository.Create(tx, &models.TicketProgress{
		TenantID:  tenantID,
		TicketID:  ticket.ID,
		Content:   buildTicketAssignmentProgressContent(fromUser, toUser, req.Reason),
		AuthorID:  operator.UserID,
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	return &events.TicketAssignedEvent{
		TicketID:   ticket.ID,
		FromUserID: ticket.CurrentAssigneeID,
		ToUserID:   req.ToUserID,
		OperatorID: operator.UserID,
		Reason:     strings.TrimSpace(req.Reason),
	}, nil
}

func (s *ticketService) buildTicketListAggregate(db *gorm.DB, list []models.Ticket, paging *sqls.Paging, tenantID int64) *TicketListAggregate {
	aggregate := &TicketListAggregate{
		List:           list,
		Paging:         paging,
		TagsByTicketID: make(map[int64][]models.Tag),
		Users:          make(map[int64]*models.User),
		Customers:      make(map[int64]*models.Customer),
	}
	if len(list) == 0 {
		return aggregate
	}
	ticketIDs := make([]int64, 0, len(list))
	customerIDs := make([]int64, 0)
	userIDs := make([]int64, 0)
	ticketSeen := make(map[int64]struct{})
	customerSeen := make(map[int64]struct{})
	userSeen := make(map[int64]struct{})
	for i := range list {
		item := &list[i]
		if _, ok := ticketSeen[item.ID]; !ok {
			ticketSeen[item.ID] = struct{}{}
			ticketIDs = append(ticketIDs, item.ID)
		}
		if item.CustomerID > 0 {
			if _, ok := customerSeen[item.CustomerID]; !ok {
				customerSeen[item.CustomerID] = struct{}{}
				customerIDs = append(customerIDs, item.CustomerID)
			}
		}
		if item.CurrentAssigneeID > 0 {
			if _, ok := userSeen[item.CurrentAssigneeID]; !ok {
				userSeen[item.CurrentAssigneeID] = struct{}{}
				userIDs = append(userIDs, item.CurrentAssigneeID)
			}
		}
	}
	s.enrichTicketTags(db, aggregate, ticketIDs, tenantID)
	if len(userIDs) > 0 {
		users := repositories.UserRepository.FindByIdsInTenant(db, userIDs, tenantID)
		for i := range users {
			item := users[i]
			aggregate.Users[item.ID] = &item
		}
	}
	if len(customerIDs) > 0 {
		customers := repositories.CustomerRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("id", customerIDs))
		for i := range customers {
			item := customers[i]
			aggregate.Customers[item.ID] = &item
		}
	}
	return aggregate
}

func (s *ticketService) enrichTicketTags(db *gorm.DB, aggregate *TicketListAggregate, ticketIDs []int64, tenantID int64) {
	if len(ticketIDs) == 0 {
		return
	}
	ticketTags := repositories.TicketTagRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("ticket_id", ticketIDs).Asc("id"))
	if len(ticketTags) == 0 {
		return
	}
	tagIDs := make([]int64, 0)
	tagSeen := make(map[int64]struct{})
	ticketTagMap := make(map[int64][]int64, len(ticketIDs))
	for i := range ticketTags {
		relation := ticketTags[i]
		ticketTagMap[relation.TicketID] = append(ticketTagMap[relation.TicketID], relation.TagID)
		if _, ok := tagSeen[relation.TagID]; !ok {
			tagSeen[relation.TagID] = struct{}{}
			tagIDs = append(tagIDs, relation.TagID)
		}
	}
	tags := repositories.TagRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("id", tagIDs))
	tagMap := make(map[int64]models.Tag, len(tags))
	for i := range tags {
		tagMap[tags[i].ID] = tags[i]
	}
	for ticketID, orderedTagIDs := range ticketTagMap {
		orderedTags := make([]models.Tag, 0, len(orderedTagIDs))
		for _, tagID := range orderedTagIDs {
			if tag, ok := tagMap[tagID]; ok {
				orderedTags = append(orderedTags, tag)
			}
		}
		aggregate.TagsByTicketID[ticketID] = orderedTags
	}
}

func (s *ticketService) validateTicketRefs(customerID, conversationID, assigneeID, tenantID int64) error {
	if customerID > 0 && repositories.CustomerRepository.GetInTenant(sqls.DB(), customerID, tenantID) == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	if conversationID > 0 {
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if customerID > 0 && conversation.CustomerID != customerID {
			return errorsx.InvalidParam("会话与客户不匹配")
		}
	}
	return s.validateAssignee(assigneeID, tenantID)
}

func (s *ticketService) validateAssignee(userID, tenantID int64) error {
	if userID <= 0 {
		return nil
	}
	return s.validateRequiredAssignee(userID, tenantID)
}

func (s *ticketService) validateRequiredAssignee(userID, tenantID int64) error {
	if userID <= 0 {
		return errorsx.InvalidParam("负责人不存在")
	}
	user := repositories.UserRepository.GetInTenant(sqls.DB(), userID, tenantID)
	if user == nil || user.Status != enums.StatusOk {
		return errorsx.InvalidParam("负责人不存在")
	}
	return nil
}

func (s *ticketService) resolveConversationChannel(conversation *models.Conversation) string {
	if conversation == nil || conversation.ChannelID <= 0 {
		return ""
	}
	if channel := repositories.ChannelRepository.GetInTenant(sqls.DB(), conversation.ChannelID, conversation.TenantID); channel != nil {
		return channel.ChannelType
	}
	return ""
}

func (s *ticketService) resolveCreateTenantID(conversationID int64, operator *dto.AuthPrincipal) (int64, error) {
	if operator == nil {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	if tenantID := AgentTeamScopeService.ActiveTenantID(operator); tenantID > 0 {
		return tenantID, nil
	}
	if operator.UserID == 0 && conversationID > 0 {
		conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
		if conversation == nil {
			return 0, errorsx.InvalidParam("会话不存在")
		}
		if conversation.TenantID <= 0 {
			return 0, errorsx.InvalidParam("会话缺少接入公司归属")
		}
		return conversation.TenantID, nil
	}
	if operator.UserID <= 0 {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	return 0, errorsx.Forbidden("请先进入需要管理工单的接入公司")
}
