package main

import (
	"encoding/json"
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

const (
	expectedSimulationConversationCount = 36
	expectedSimulationMessageCount      = 135
	expectedSimulationAssignmentCount   = 21
	expectedSimulationNeedReplyCount    = 27

	simulationRemarkPrefix = "SIM_CONVERSATION:"
	simulationManualWindow = 24 * time.Hour
)

type simulationKind string

const (
	simulationKindAI         simulationKind = "ai_serving"
	simulationKindPending    simulationKind = "pending"
	simulationKindAssigned   simulationKind = "assigned"
	simulationKindProcessing simulationKind = "processing"
	simulationKindPriority   simulationKind = "priority"
	simulationKindUrgent     simulationKind = "urgent"
	simulationKindClosed     simulationKind = "closed"
)

type simulationTopic struct {
	Question         string
	AIReply          string
	Escalation       string
	AgentReply       string
	CustomerFollowUp string
	Resolution       string
}

type simulationLine struct {
	SenderType enums.IMSenderType
	Content    string
	SentAt     time.Time
}

type simulationScenario struct {
	Key            string
	Kind           simulationKind
	Topic          simulationTopic
	CustomerIndex  int
	StoreIndex     int
	TeamIndex      int
	AssigneeIndex  int
	StartedAt      time.Time
	HandoffAt      *time.Time
	AssignmentAt   *time.Time
	ManualExpireAt *time.Time
	ClosedAt       *time.Time
	HandoffReason  string
	Messages       []simulationLine
}

func (ctx *seedContext) upsertSimulationConversations() error {
	if len(ctx.customers) < 500 || len(ctx.stores) < 100 || len(ctx.wxInstances) < 100 || len(ctx.teams) < 3 || len(ctx.agents) < 12 {
		return fmt.Errorf("simulation prerequisites are incomplete")
	}
	if err := deleteSimulationConversations(ctx.db, ctx.marker); err != nil {
		return err
	}
	for _, scenario := range buildSimulationScenarios(ctx.now) {
		if err := ctx.createSimulationScenario(scenario); err != nil {
			return fmt.Errorf("create simulation scenario %s failed: %w", scenario.Key, err)
		}
	}
	return nil
}

func buildSimulationScenarios(now time.Time) []simulationScenario {
	storeStarts := []int{1, 35, 68}
	topics := simulationTopics()
	scenarios := make([]simulationScenario, 0, expectedSimulationConversationCount)
	for teamOffset, storeStart := range storeStarts {
		teamIndex := teamOffset + 1
		for offset := 0; offset < 12; offset++ {
			kind := simulationKindForOffset(offset)
			storeIndex := storeStart + offset
			assigneeIndex := 0
			if kind != simulationKindAI && kind != simulationKindPending {
				assigneeIndex = teamOffset*4 + ((offset - 5) % 4) + 1
			}
			scenario := newSimulationScenario(now, teamIndex, storeIndex, assigneeIndex, kind, topics[offset])
			scenario.Key = fmt.Sprintf("team-%d-%s-%02d", teamIndex, kind, offset+1)
			scenarios = append(scenarios, scenario)
		}
	}
	return scenarios
}

func simulationKindForOffset(offset int) simulationKind {
	switch {
	case offset < 2:
		return simulationKindAI
	case offset < 5:
		return simulationKindPending
	case offset < 7:
		return simulationKindAssigned
	case offset < 9:
		return simulationKindProcessing
	case offset == 9:
		return simulationKindPriority
	case offset == 10:
		return simulationKindUrgent
	default:
		return simulationKindClosed
	}
}

func newSimulationScenario(now time.Time, teamIndex, storeIndex, assigneeIndex int, kind simulationKind, topic simulationTopic) simulationScenario {
	scenario := simulationScenario{
		Kind:          kind,
		Topic:         topic,
		CustomerIndex: storeIndex,
		StoreIndex:    storeIndex,
		TeamIndex:     teamIndex,
		AssigneeIndex: assigneeIndex,
	}
	switch kind {
	case simulationKindAI:
		scenario.StartedAt = now.Add(-35 * time.Minute)
	case simulationKindPending:
		handoffAt := now.Add(-time.Duration(2+(storeIndex%3)*2) * time.Minute)
		expireAt := now.Add(simulationManualWindow)
		scenario.StartedAt = handoffAt.Add(-12 * time.Minute)
		scenario.HandoffAt = &handoffAt
		scenario.ManualExpireAt = &expireAt
		scenario.HandoffReason = topic.Escalation
	case simulationKindAssigned:
		handoffAt := now.Add(-10 * time.Minute)
		assignmentAt := now.Add(-time.Duration(3+(storeIndex%2)*2) * time.Minute)
		expireAt := now.Add(simulationManualWindow)
		scenario.StartedAt = now.Add(-24 * time.Minute)
		scenario.HandoffAt = &handoffAt
		scenario.AssignmentAt = &assignmentAt
		scenario.ManualExpireAt = &expireAt
		scenario.HandoffReason = topic.Escalation
	case simulationKindProcessing:
		handoffAt := now.Add(-25 * time.Minute)
		assignmentAt := now.Add(-20 * time.Minute)
		expireAt := now.Add(simulationManualWindow)
		scenario.StartedAt = now.Add(-38 * time.Minute)
		scenario.HandoffAt = &handoffAt
		scenario.AssignmentAt = &assignmentAt
		scenario.ManualExpireAt = &expireAt
		scenario.HandoffReason = topic.Escalation
	case simulationKindPriority:
		handoffAt := now.Add(-10 * time.Minute)
		assignmentAt := now.Add(-8 * time.Minute)
		expireAt := now.Add(simulationManualWindow)
		scenario.StartedAt = now.Add(-28 * time.Minute)
		scenario.HandoffAt = &handoffAt
		scenario.AssignmentAt = &assignmentAt
		scenario.ManualExpireAt = &expireAt
		scenario.HandoffReason = "客户持续催促，优先安排人工回复：" + topic.Escalation
	case simulationKindUrgent:
		handoffAt := now.Add(-25 * time.Minute)
		assignmentAt := now.Add(-20 * time.Minute)
		expireAt := now.Add(simulationManualWindow)
		scenario.StartedAt = now.Add(-42 * time.Minute)
		scenario.HandoffAt = &handoffAt
		scenario.AssignmentAt = &assignmentAt
		scenario.ManualExpireAt = &expireAt
		scenario.HandoffReason = "客户多次催促，需要紧急人工回复：" + topic.Escalation
	case simulationKindClosed:
		handoffAt := now.Add(-100 * time.Minute)
		assignmentAt := now.Add(-95 * time.Minute)
		closedAt := now.Add(-35 * time.Minute)
		scenario.StartedAt = now.Add(-2 * time.Hour)
		scenario.HandoffAt = &handoffAt
		scenario.AssignmentAt = &assignmentAt
		scenario.ClosedAt = &closedAt
		scenario.HandoffReason = topic.Escalation
	}
	scenario.Messages = buildSimulationDialogue(scenario, now)
	return scenario
}

func buildSimulationDialogue(scenario simulationScenario, now time.Time) []simulationLine {
	topic := scenario.Topic
	lines := []simulationLine{
		{SenderType: enums.IMSenderTypeCustomer, Content: topic.Question, SentAt: scenario.StartedAt.Add(time.Minute)},
		{SenderType: enums.IMSenderTypeAI, Content: topic.AIReply, SentAt: scenario.StartedAt.Add(2 * time.Minute)},
	}
	switch scenario.Kind {
	case simulationKindAI:
		return append(lines,
			simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: "好的，我明白了，谢谢。", SentAt: scenario.StartedAt.Add(10 * time.Minute)},
			simulationLine{SenderType: enums.IMSenderTypeAI, Content: "不客气，如有其他需要请随时告诉我。", SentAt: scenario.StartedAt.Add(11 * time.Minute)},
		)
	case simulationKindPending, simulationKindAssigned, simulationKindPriority, simulationKindUrgent:
		return append(lines, simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: topic.Escalation, SentAt: *scenario.HandoffAt})
	case simulationKindProcessing:
		return append(lines,
			simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: topic.Escalation, SentAt: *scenario.HandoffAt},
			simulationLine{SenderType: enums.IMSenderTypeAgent, Content: topic.AgentReply, SentAt: scenario.AssignmentAt.Add(time.Minute)},
			simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: topic.CustomerFollowUp, SentAt: now.Add(-4 * time.Minute)},
		)
	case simulationKindClosed:
		return append(lines,
			simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: topic.Escalation, SentAt: *scenario.HandoffAt},
			simulationLine{SenderType: enums.IMSenderTypeAgent, Content: topic.AgentReply, SentAt: scenario.AssignmentAt.Add(time.Minute)},
			simulationLine{SenderType: enums.IMSenderTypeCustomer, Content: "现在已经处理好了，辛苦了。", SentAt: scenario.ClosedAt.Add(-8 * time.Minute)},
			simulationLine{SenderType: enums.IMSenderTypeAgent, Content: topic.Resolution, SentAt: scenario.ClosedAt.Add(-5 * time.Minute)},
		)
	default:
		return lines
	}
}

func simulationTopics() []simulationTopic {
	return []simulationTopic{
		{
			Question:         "你好，房间里的 Wi-Fi 一直提示密码错误，正确密码是什么？",
			AIReply:          "酒店 Wi-Fi 通常使用房号和入住人手机号后四位验证，请确认连接的是房间内标注的网络。",
			Escalation:       "我按房号和手机号试了还是连不上，麻烦安排人工帮我看一下。",
			AgentReply:       "您好，我来协助排查。请告诉我房号和手机系统，我先确认该楼层网络状态。",
			CustomerFollowUp: "房号 0812，用的是 iPhone，现在仍然显示无法加入网络。",
			Resolution:       "已为您刷新房间网络认证，重新连接即可；本次问题已处理完成。",
		},
		{
			Question:         "明天早餐几点开始？我对花生过敏，餐厅能特别注意吗？",
			AIReply:          "早餐供应时间为 07:00-10:00，地点在酒店一层餐厅。过敏需求建议由工作人员进一步确认。",
			Escalation:       "请帮我联系餐厅确认一下花生过敏餐食，我需要人工回复。",
			AgentReply:       "您好，我正在联系餐厅登记过敏信息，请提供房号和用餐人数。",
			CustomerFollowUp: "房号 1206，两位用餐，其中一位严重花生过敏。",
			Resolution:       "餐厅已完成登记，明早报房号即可由工作人员引导取餐。",
		},
		{
			Question:         "房间空调开了半小时还是不制冷，室温一直是 28 度。",
			AIReply:          "请先确认空调模式为制冷、设定温度低于当前室温，并保持门窗关闭。",
			Escalation:       "这些都确认过了还是不制冷，请尽快安排工程人员上门。",
			AgentReply:       "收到，我现在联系工程值班人员上门检查，请问现在方便进入房间吗？",
			CustomerFollowUp: "现在方便，我十分钟后要出门，请尽量快一点。",
			Resolution:       "工程人员已完成检修并确认空调恢复正常，本次服务已结束。",
		},
		{
			Question:         "隔壁一直很吵，现在已经影响休息了，可以处理吗？",
			AIReply:          "非常抱歉影响您的休息。我可以记录噪音情况并协助联系值班人员处理。",
			Escalation:       "已经持续四十分钟了，请人工马上联系隔壁或者帮我换房。",
			AgentReply:       "您好，值班经理正在核实相邻房间情况，我也会同时确认可用安静房型。",
			CustomerFollowUp: "可以，最好安排同楼层的无烟房。",
			Resolution:       "已完成安静房间调整并送达新房卡，感谢您的理解。",
		},
		{
			Question:         "麻烦送两瓶矿泉水和两条浴巾到房间。",
			AIReply:          "可以为您记录客用品配送需求，请确认房号和期望送达时间。",
			Escalation:       "房号 0608，希望十分钟内送到，麻烦人工确认是否已经安排。",
			AgentReply:       "您好，客房服务已经接单，预计十分钟内送达。",
			CustomerFollowUp: "好的，我在房间等，到了请敲门。",
			Resolution:       "客用品已送达并与您确认签收，本次配送完成。",
		},
		{
			Question:         "我明天能延迟到下午两点退房吗？",
			AIReply:          "延迟退房需要根据次日房态确认，可能产生额外费用，建议由前台人工核实。",
			Escalation:       "请帮我转前台确认两点退房以及具体费用。",
			AgentReply:       "您好，我正在查询明日房态和会员权益，稍后给您准确答复。",
			CustomerFollowUp: "我是金卡会员，预订人姓李，麻烦一起核对权益。",
			Resolution:       "已为您免费延迟至 14:00 退房，前台系统已完成备注。",
		},
		{
			Question:         "退房后怎么开增值税专用发票？",
			AIReply:          "发票通常可在退房后由前台开具，需要提供抬头、税号及接收邮箱等信息。",
			Escalation:       "我需要专票并分开开住宿和餐饮，请让人工告诉我需要哪些资料。",
			AgentReply:       "您好，可以分别开具。请提供公司名称、税号、地址电话、开户行账号和接收邮箱。",
			CustomerFollowUp: "资料我稍后发过来，餐饮金额需要单独开一张。",
			Resolution:       "两张发票均已开具并发送至预留邮箱，请注意查收。",
		},
		{
			Question:         "今天下午房间没有打扫，现在还能安排清洁吗？",
			AIReply:          "可以为您记录补做清洁需求，具体上门时间需要客房部确认。",
			Escalation:       "我半小时后离开房间，请人工确认能否在六点前打扫完。",
			AgentReply:       "您好，客房部已收到需求，预计 17:20 上门并在 18:00 前完成。",
			CustomerFollowUp: "可以，我会把请即打扫牌挂在门外。",
			Resolution:       "房间清洁已完成，客用品也已补齐。",
		},
		{
			Question:         "自驾过来从哪个入口进停车场？住店客人收费吗？",
			AIReply:          "停车场入口通常位于酒店主入口侧方，住店停车优惠需要前台录入车牌后确认。",
			Escalation:       "导航把我带到后门了，请人工发一下准确入口并确认车牌登记。",
			AgentReply:       "您好，我马上发送停车场定位，请同时提供车牌号码。",
			CustomerFollowUp: "车牌是沪 A12345，我现在在酒店北侧路口。",
			Resolution:       "车牌已登记，停车场定位也已发送，离店前无需再次缴费。",
		},
		{
			Question:         "房门刷卡一直亮红灯，我现在进不了房间。",
			AIReply:          "请确认房卡没有与手机或磁性物品放在一起；若仍无法开门，需要工作人员现场处理。",
			Escalation:       "换了两张卡都打不开，我带着孩子在门口，请马上安排人工。",
			AgentReply:       "非常抱歉，我已通知值班人员携带备用卡上楼，请在房门口稍候。",
			CustomerFollowUp: "好的，我们在 1516 门口，请尽快。",
			Resolution:       "值班人员已协助开门并更换门卡，门锁测试正常。",
		},
		{
			Question:         "退房三天了，押金还没有退回，能帮我查一下吗？",
			AIReply:          "押金到账时间受支付渠道影响，通常需要 1-7 个工作日，具体状态需人工查询账务记录。",
			Escalation:       "已经超过银行说的时间了，请人工查询退款流水号。",
			AgentReply:       "您好，我正在核对前台结账记录和支付渠道流水，请稍等。",
			CustomerFollowUp: "订单尾号 4832，支付方式是微信。",
			Resolution:       "退款流水已核实并重新推送，预计一个工作日内到账。",
		},
		{
			Question:         "我退房后发现一副黑色耳机可能落在床头柜了。",
			AIReply:          "我可以记录遗失物品信息，失物招领需要客房部和前台人工共同核实。",
			Escalation:       "耳机很重要，请尽快帮我联系客房部查找并人工回复。",
			AgentReply:       "您好，已经登记房号、退房时间和物品特征，正在联系当班客房人员核查。",
			CustomerFollowUp: "耳机盒背面有一道白色划痕，找到后可以快递给我。",
			Resolution:       "耳机已找到并完成寄送登记，快递单号已发送给您。",
		},
	}
}

func (ctx *seedContext) createSimulationScenario(scenario simulationScenario) error {
	customer := ctx.customers[scenario.CustomerIndex-1]
	store := ctx.stores[scenario.StoreIndex-1]
	instance := ctx.wxInstances[scenario.StoreIndex-1]
	team := ctx.teams[scenario.TeamIndex-1]
	var assignee *models.User
	if scenario.AssigneeIndex > 0 {
		assignee = ctx.agents[scenario.AssigneeIndex-1]
	}

	status, routeStatus, routeTarget := simulationStatuses(scenario.Kind)
	currentAssigneeID := int64(0)
	if assignee != nil {
		currentAssigneeID = assignee.ID
	}
	currentTeamID := int64(0)
	if scenario.Kind != simulationKindAI {
		currentTeamID = team.ID
	}
	priority := 0
	if scenario.Kind == simulationKindPriority {
		priority = 2
	}
	if scenario.Kind == simulationKindUrgent {
		priority = 3
	}

	conversation := &models.Conversation{
		TenantID:          ctx.tenant.ID,
		ChannelID:         ctx.channel.ID,
		CustomerID:        customer.ID,
		CustomerName:      customer.Name,
		Status:            status,
		ServiceMode:       enums.IMConversationServiceModeAIFirst,
		Priority:          priority,
		CurrentAssigneeID: currentAssigneeID,
		CurrentTeamID:     currentTeamID,
		LastMessageAt:     scenario.StartedAt,
		LastActiveAt:      scenario.StartedAt,
		HandoffAt:         scenario.HandoffAt,
		HandoffReason:     scenario.HandoffReason,
		ClosedAt:          scenario.ClosedAt,
		ClosedBy:          currentAssigneeID,
		AuditFields:       simulationAuditFields(scenario.StartedAt),
	}
	if scenario.ClosedAt != nil {
		conversation.CloseReason = "仿真场景：客户问题已处理完成"
	}
	if err := ctx.db.Create(conversation).Error; err != nil {
		return err
	}

	lastCustomerAt := simulationLastCustomerMessageAt(scenario.Messages)
	route := &models.ConversationRouteState{
		TenantID:              ctx.tenant.ID,
		ConversationID:        conversation.ID,
		StoreID:               store.ID,
		KnowledgeBaseID:       instance.KnowledgeBaseID,
		WxWorkInstanceID:      instance.ID,
		RouteStatus:           routeStatus,
		RouteTarget:           routeTarget,
		SessionNo:             1,
		SessionStartedAt:      timePointer(scenario.StartedAt),
		LastManualHandoffAt:   scenario.HandoffAt,
		ManualExpireAt:        scenario.ManualExpireAt,
		LastCustomerMessageAt: lastCustomerAt,
		NeedHumanFollowUp:     simulationNeedsReply(scenario.Kind),
		HandoffReason:         scenario.HandoffReason,
		Remark:                fmt.Sprintf("%s %s%s %s", ctx.marker, simulationRemarkPrefix, scenario.Key, scenario.Kind),
		AuditFields:           simulationAuditFields(scenario.StartedAt),
	}
	if err := ctx.db.Create(route).Error; err != nil {
		return err
	}

	if err := ctx.createSimulationParticipants(conversation, customer, assignee, scenario); err != nil {
		return err
	}
	lastMessage, aiRounds, err := ctx.createSimulationMessages(conversation, assignee, scenario)
	if err != nil {
		return err
	}
	if lastMessage == nil {
		return fmt.Errorf("simulation scenario has no messages")
	}

	lastAt := lastMessage.CreatedAt
	updates := map[string]any{
		"last_message_id":       lastMessage.ID,
		"last_message_at":       lastAt,
		"last_active_at":        lastAt,
		"last_message_summary":  lastMessage.Content,
		"agent_unread_count":    simulationAgentUnreadCount(scenario.Kind),
		"customer_unread_count": 0,
		"ai_reply_rounds":       aiRounds,
		"updated_at":            lastAt,
		"update_user_id":        constants.SystemAuditUserID,
		"update_user_name":      constants.SystemAuditUserName,
	}
	if scenario.ClosedAt != nil {
		updates["updated_at"] = *scenario.ClosedAt
	}
	if err := ctx.db.Model(conversation).Updates(updates).Error; err != nil {
		return err
	}

	if err := ctx.createSimulationAssignment(conversation, assignee, scenario); err != nil {
		return err
	}
	if err := ctx.createSimulationEvents(conversation, assignee, scenario); err != nil {
		return err
	}
	return ctx.db.Model(&models.StoreCustomerRelation{}).
		Where("customer_id = ? AND store_id = ?", customer.ID, store.ID).
		Updates(map[string]any{
			"last_conversation_id": conversation.ID,
			"last_active_at":       lastAt,
			"updated_at":           lastAt,
		}).Error
}

func simulationStatuses(kind simulationKind) (enums.IMConversationStatus, enums.ConversationRouteStatus, string) {
	switch kind {
	case simulationKindAI:
		return enums.IMConversationStatusAIServing, enums.ConversationRouteStatusAIServing, "ai"
	case simulationKindPending:
		return enums.IMConversationStatusPending, enums.ConversationRouteStatusHQAgentDeskPending, "agentdesk_hq"
	case simulationKindClosed:
		return enums.IMConversationStatusClosed, enums.ConversationRouteStatusClosed, "closed"
	default:
		return enums.IMConversationStatusActive, enums.ConversationRouteStatusHQAgentDeskServing, "agentdesk_hq"
	}
}

func simulationNeedsReply(kind simulationKind) bool {
	return kind != simulationKindAI && kind != simulationKindClosed
}

func simulationAgentUnreadCount(kind simulationKind) int {
	if simulationNeedsReply(kind) {
		return 1
	}
	return 0
}

func simulationLastCustomerMessageAt(lines []simulationLine) *time.Time {
	var latest *time.Time
	for _, line := range lines {
		if line.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		current := line.SentAt
		latest = &current
	}
	return latest
}

func (ctx *seedContext) createSimulationParticipants(conversation *models.Conversation, customer *models.Customer, assignee *models.User, scenario simulationScenario) error {
	joinedAt := scenario.StartedAt
	if err := ctx.db.Create(&models.ConversationParticipant{
		TenantID:              ctx.tenant.ID,
		ConversationID:        conversation.ID,
		ParticipantType:       string(enums.IMParticipantTypeCustomer),
		ParticipantID:         customer.ID,
		ExternalParticipantID: fmt.Sprintf("test_customer_audit_customer_%03d", scenario.CustomerIndex),
		JoinedAt:              &joinedAt,
		Status:                enums.StatusOk,
		AuditFields:           simulationAuditFields(joinedAt),
	}).Error; err != nil {
		return err
	}
	if assignee == nil || scenario.AssignmentAt == nil {
		return nil
	}
	return ctx.db.Create(&models.ConversationParticipant{
		TenantID:        ctx.tenant.ID,
		ConversationID:  conversation.ID,
		ParticipantType: string(enums.IMParticipantTypeAgent),
		ParticipantID:   assignee.ID,
		JoinedAt:        scenario.AssignmentAt,
		LeftAt:          scenario.ClosedAt,
		Status:          enums.StatusOk,
		AuditFields:     simulationAuditFields(*scenario.AssignmentAt),
	}).Error
}

func (ctx *seedContext) createSimulationMessages(conversation *models.Conversation, assignee *models.User, scenario simulationScenario) (*models.Message, int, error) {
	payload, err := json.Marshal(map[string]any{
		"simulation": true,
		"batch":      ctx.batch,
		"scenario":   scenario.Key,
	})
	if err != nil {
		return nil, 0, err
	}
	var lastMessage *models.Message
	aiRounds := 0
	for index, line := range scenario.Messages {
		senderID, err := simulationSenderID(line.SenderType, assignee)
		if err != nil {
			return nil, 0, err
		}
		if line.SenderType == enums.IMSenderTypeAI {
			aiRounds++
		}
		sentAt := line.SentAt
		deliveredAt := sentAt.Add(time.Second)
		message := &models.Message{
			TenantID:       ctx.tenant.ID,
			ConversationID: conversation.ID,
			SessionNo:      1,
			RequestID:      fmt.Sprintf("simulation-%s-%02d", scenario.Key, index+1),
			ClientMsgID:    fmt.Sprintf("simulation-%s-%s-%02d", ctx.batch, scenario.Key, index+1),
			SenderType:     line.SenderType,
			SenderID:       senderID,
			MessageType:    enums.IMMessageTypeText,
			Content:        line.Content,
			Payload:        string(payload),
			SeqNo:          int64(index + 1),
			SendStatus:     enums.IMMessageStatusDelivered,
			SentAt:         &sentAt,
			DeliveredAt:    &deliveredAt,
			AuditFields:    simulationAuditFields(sentAt),
		}
		if err := ctx.db.Create(message).Error; err != nil {
			return nil, 0, err
		}
		lastMessage = message
	}
	return lastMessage, aiRounds, nil
}

func simulationSenderID(senderType enums.IMSenderType, assignee *models.User) (int64, error) {
	if senderType != enums.IMSenderTypeAgent {
		return 0, nil
	}
	if assignee == nil {
		return 0, fmt.Errorf("agent message is missing assignee")
	}
	return assignee.ID, nil
}

func (ctx *seedContext) createSimulationAssignment(conversation *models.Conversation, assignee *models.User, scenario simulationScenario) error {
	if assignee == nil || scenario.AssignmentAt == nil {
		return nil
	}
	status := enums.IMAssignmentStatusActive
	if scenario.ClosedAt != nil {
		status = enums.IMAssignmentStatusInactive
	}
	return ctx.db.Create(&models.ConversationAssignment{
		TenantID:       ctx.tenant.ID,
		ConversationID: conversation.ID,
		ToUserID:       assignee.ID,
		AssignType:     string(enums.IMAssignmentTypeAssign),
		Reason:         "仿真派单：" + scenario.HandoffReason,
		Status:         status,
		CreatedAt:      *scenario.AssignmentAt,
		FinishedAt:     scenario.ClosedAt,
		OperatorID:     ctx.leaders[scenario.TeamIndex-1].ID,
	}).Error
}

func (ctx *seedContext) createSimulationEvents(conversation *models.Conversation, assignee *models.User, scenario simulationScenario) error {
	events := []models.ConversationEventLog{
		{
			TenantID:       ctx.tenant.ID,
			ConversationID: conversation.ID,
			RequestID:      "simulation-" + scenario.Key + "-create",
			EventType:      enums.IMEventTypeCreate,
			OperatorType:   enums.IMSenderTypeCustomer,
			Content:        "仿真客户创建会话",
			CreatedAt:      scenario.StartedAt,
		},
	}
	if scenario.HandoffAt != nil {
		payload, _ := json.Marshal(map[string]any{"simulation": true, "scenario": scenario.Key, "teamId": conversation.CurrentTeamID})
		events = append(events, models.ConversationEventLog{
			TenantID:       ctx.tenant.ID,
			ConversationID: conversation.ID,
			RequestID:      "simulation-" + scenario.Key + "-handoff",
			EventType:      enums.IMEventTypeTransfer,
			OperatorType:   enums.IMSenderTypeAI,
			Content:        "仿真会话转人工：" + scenario.HandoffReason,
			Payload:        string(payload),
			CreatedAt:      *scenario.HandoffAt,
		})
	}
	if assignee != nil && scenario.AssignmentAt != nil {
		payload, _ := json.Marshal(map[string]any{"simulation": true, "scenario": scenario.Key, "toUserId": assignee.ID, "teamId": conversation.CurrentTeamID})
		events = append(events, models.ConversationEventLog{
			TenantID:       ctx.tenant.ID,
			ConversationID: conversation.ID,
			RequestID:      "simulation-" + scenario.Key + "-assign",
			EventType:      enums.IMEventTypeAssign,
			OperatorType:   enums.IMSenderTypeAgent,
			OperatorID:     ctx.leaders[scenario.TeamIndex-1].ID,
			Content:        "仿真会话已派发",
			Payload:        string(payload),
			CreatedAt:      *scenario.AssignmentAt,
		})
	}
	if scenario.ClosedAt != nil {
		events = append(events, models.ConversationEventLog{
			TenantID:       ctx.tenant.ID,
			ConversationID: conversation.ID,
			RequestID:      "simulation-" + scenario.Key + "-close",
			EventType:      enums.IMEventTypeClose,
			OperatorType:   enums.IMSenderTypeAgent,
			OperatorID:     conversation.CurrentAssigneeID,
			Content:        "仿真会话已解决并关闭",
			CreatedAt:      *scenario.ClosedAt,
		})
	}
	return ctx.db.Create(&events).Error
}

func simulationAuditFields(at time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt:      at,
		CreateUserID:   constants.SystemAuditUserID,
		CreateUserName: constants.SystemAuditUserName,
		UpdatedAt:      at,
		UpdateUserID:   constants.SystemAuditUserID,
		UpdateUserName: constants.SystemAuditUserName,
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func simulationConversationIDs(db *gorm.DB, batchMarker string) ([]int64, error) {
	ids := make([]int64, 0)
	err := db.Model(&models.ConversationRouteState{}).
		Where("remark LIKE ?", likeMarker(batchMarker)).
		Pluck("conversation_id", &ids).Error
	return ids, err
}

func deleteSimulationConversations(db *gorm.DB, batchMarker string) error {
	conversationIDs, err := simulationConversationIDs(db, batchMarker)
	if err != nil {
		return err
	}
	if len(conversationIDs) == 0 {
		return nil
	}

	ticketIDs := make([]int64, 0)
	if err := db.Model(&models.Ticket{}).Where("conversation_id IN ?", conversationIDs).Pluck("id", &ticketIDs).Error; err != nil {
		return err
	}
	if len(ticketIDs) > 0 {
		if err := db.Where("ticket_id IN ?", ticketIDs).Delete(&models.TicketTag{}).Error; err != nil {
			return err
		}
		if err := db.Where("ticket_id IN ?", ticketIDs).Delete(&models.TicketProgress{}).Error; err != nil {
			return err
		}
		if err := db.Where("biz_type = ? AND biz_id IN ?", "ticket", ticketIDs).Delete(&models.Notification{}).Error; err != nil {
			return err
		}
		if err := db.Where("id IN ?", ticketIDs).Delete(&models.Ticket{}).Error; err != nil {
			return err
		}
	}

	deleteSteps := []struct {
		model any
	}{
		{&models.KnowledgeCandidate{}},
		{&models.KnowledgeRetrieveLog{}},
		{&models.SkillRunLog{}},
		{&models.AgentRunLog{}},
		{&models.ConversationInterrupt{}},
		{&models.ConversationSessionSummary{}},
		{&models.MessageSyncLog{}},
		{&models.WxWorkKFMessageRef{}},
		{&models.ChannelMessageOutbox{}},
		{&models.WxWorkKFConversation{}},
		{&models.ConversationReadState{}},
		{&models.ConversationParticipant{}},
		{&models.ConversationAssignment{}},
		{&models.ConversationTag{}},
		{&models.ConversationEventLog{}},
		{&models.Message{}},
	}
	for _, step := range deleteSteps {
		if err := db.Where("conversation_id IN ?", conversationIDs).Delete(step.model).Error; err != nil {
			return err
		}
	}
	if err := db.Where("biz_type = ? AND biz_id IN ?", "conversation", conversationIDs).Delete(&models.Notification{}).Error; err != nil {
		return err
	}
	if err := db.Model(&models.StoreCustomerRelation{}).
		Where("last_conversation_id IN ?", conversationIDs).
		Update("last_conversation_id", 0).Error; err != nil {
		return err
	}
	if err := db.Where("conversation_id IN ?", conversationIDs).Delete(&models.ConversationRouteState{}).Error; err != nil {
		return err
	}
	return db.Where("id IN ?", conversationIDs).Delete(&models.Conversation{}).Error
}
