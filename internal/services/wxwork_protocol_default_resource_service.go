package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

var WxWorkProtocolDefaultResourceService = &wxWorkProtocolDefaultResourceService{}

type wxWorkProtocolDefaultResourceService struct{}

func (s *wxWorkProtocolDefaultResourceService) BindInboundLocation(instanceID int64, message *models.Message) {
	if instanceID <= 0 || message == nil || message.MessageType != enums.IMMessageTypeLocation {
		return
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(message.Payload)), &payload); err != nil {
		return
	}
	longitude := strings.TrimSpace(fmt.Sprint(payload["longitude"]))
	latitude := strings.TrimSpace(fmt.Sprint(payload["latitude"]))
	if longitude == "" || longitude == "0" || latitude == "" || latitude == "0" {
		return
	}
	title := utils.RepairMojibakeText(firstNonBlank(
		strings.TrimSpace(fmt.Sprint(payload["title"])),
		strings.TrimSpace(message.Content),
	))
	address := utils.RepairMojibakeText(strings.TrimSpace(fmt.Sprint(payload["address"])))
	updates := map[string]any{
		"store_longitude":    longitude,
		"store_latitude":     latitude,
		"store_map_provider": "wxwork_inbound_location",
		"updated_at":         time.Now(),
		"update_user_id":     int64(0),
		"update_user_name":   "wxwork_location_bind",
	}
	if address != "" && address != "<nil>" {
		updates["store_address"] = address
	}
	if title != "" && title != "<nil>" {
		updates["store_navigation_name"] = title
	}
	if err := repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instanceID, updates); err != nil {
		slog.Warn("bind wxwork protocol inbound location failed", "instance_id", instanceID, "message_id", message.ID, "error", err)
	}
}

func (s *wxWorkProtocolDefaultResourceService) HandleCustomerIntent(conversation *models.Conversation, customerMessage *models.Message) bool {
	if conversation == nil || customerMessage == nil || customerMessage.SenderType != enums.IMSenderTypeCustomer {
		return false
	}
	if customerMessage.MessageType != enums.IMMessageTypeText && customerMessage.MessageType != enums.IMMessageTypeHTML && customerMessage.MessageType != enums.IMMessageTypeVoice {
		return false
	}
	text := defaultResourceIntentText(customerMessage)
	if text == "" {
		return false
	}
	route := ConversationRouteService.GetByConversationID(conversation.ID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return false
	}
	instance := WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
	if instance == nil || instance.Status != enums.StatusOk {
		return false
	}
	requestID := tracex.NormalizeRequestID(customerMessage.RequestID)
	if handled, err := s.consumePendingServiceTask(conversation, text, requestID); err != nil {
		slog.Warn("consume pending service task failed", "conversation_id", conversation.ID, "error", err)
		return false
	} else if handled {
		return true
	}
	if isPositiveConfirmation(text) {
		if handled, err := s.consumePendingLocation(conversation, instance, requestID); err != nil {
			slog.Warn("consume pending location failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			return false
		} else if handled {
			return true
		}
	}
	if wantsDirectStoreLocation(text) {
		if err := s.sendDefaultLocation(conversation, instance, requestID); err != nil {
			slog.Warn("send default store location failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			return false
		}
		return true
	}
	if wantsDefaultMiniProgram(text) {
		if err := s.sendDefaultMiniProgram(conversation, instance, requestID); err != nil {
			slog.Warn("send default mini program failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			return false
		}
		if wantsCheckInMiniProgram(text) {
			_, _ = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_checkin_tip_"+strs.UUID(), enums.IMMessageTypeText, "我发你小程序，进去选自助入住。", "", systemOperator(), requestID)
		}
		return true
	}
	if wantsServiceTask(text) {
		if err := s.handleServiceTask(conversation, text, requestID); err != nil {
			slog.Warn("handle service task intent failed", "conversation_id", conversation.ID, "error", err)
			return false
		}
		return true
	}
	return false
}

func (s *wxWorkProtocolDefaultResourceService) SendNewFriendWelcome(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) {
	if conversation == nil || instance == nil || instance.Status != enums.StatusOk {
		return
	}
	requestID = tracex.NormalizeRequestID(requestID)
	if message := strings.TrimSpace(instance.WelcomeMessage); message != "" {
		_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_welcome_text_"+strs.UUID(), enums.IMMessageTypeText, utils.RepairMojibakeText(message), "", systemOperator(), requestID)
		if err != nil {
			slog.Warn("send wxwork welcome text failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
		}
	}
	if instance.WelcomeSendMiniProgram && strings.TrimSpace(instance.DefaultMiniProgramPayload) != "" {
		if err := s.sendDefaultMiniProgram(conversation, instance, requestID); err != nil {
			slog.Warn("send wxwork welcome mini program failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
		}
	}
	if instance.WelcomeAskLocation && strings.TrimSpace(instance.StoreLongitude) != "" && strings.TrimSpace(instance.StoreLatitude) != "" {
		if err := s.sendDefaultLocation(conversation, instance, requestID); err != nil {
			slog.Warn("send wxwork welcome location failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
		}
	}
}

func (s *wxWorkProtocolDefaultResourceService) askBeforeSendingLocation(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	storeName := firstNonBlank(
		utils.RepairMojibakeText(strings.TrimSpace(instance.StoreNavigationName)),
		utils.RepairMojibakeText(strings.TrimSpace(instance.StoreAddress)),
		"酒店",
	)
	if err := ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionSendLocation, "", time.Now().Add(5*time.Minute)); err != nil {
		return err
	}
	content := fmt.Sprintf("要我把%s定位发您吗？", storeName)
	_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_location_confirm_"+strs.UUID(), enums.IMMessageTypeText, content, "", systemOperator(), requestID)
	return err
}

func (s *wxWorkProtocolDefaultResourceService) consumePendingLocation(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) (bool, error) {
	_, ok, err := ConversationRouteService.ConsumePendingAction(conversation.ID, enums.ConversationPendingActionSendLocation, time.Now())
	if err != nil || !ok {
		return false, err
	}
	if err := s.sendDefaultLocation(conversation, instance, requestID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *wxWorkProtocolDefaultResourceService) sendDefaultLocation(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	longitude := strings.TrimSpace(instance.StoreLongitude)
	latitude := strings.TrimSpace(instance.StoreLatitude)
	if longitude == "" || latitude == "" {
		return fmt.Errorf("员工号未绑定门店坐标")
	}
	lng, err := strconv.ParseFloat(longitude, 64)
	if err != nil || lng == 0 {
		return fmt.Errorf("员工号门店经度无效")
	}
	lat, err := strconv.ParseFloat(latitude, 64)
	if err != nil || lat == 0 {
		return fmt.Errorf("员工号门店纬度无效")
	}
	title := firstNonBlank(utils.RepairMojibakeText(strings.TrimSpace(instance.StoreNavigationName)), utils.RepairMojibakeText(strings.TrimSpace(instance.StoreAddress)), "门店位置")
	address := firstNonBlank(utils.RepairMojibakeText(strings.TrimSpace(instance.StoreAddress)), title)
	payload, _ := json.Marshal(map[string]any{
		"longitude": lng,
		"latitude":  lat,
		"address":   address,
		"title":     title,
		"zoom":      15,
	})
	_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_default_location_"+strs.UUID(), enums.IMMessageTypeLocation, title, string(payload), systemOperator(), requestID)
	return err
}

func (s *wxWorkProtocolDefaultResourceService) sendDefaultMiniProgram(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	payload := strings.TrimSpace(instance.DefaultMiniProgramPayload)
	if payload == "" {
		return fmt.Errorf("员工号未绑定默认小程序")
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return fmt.Errorf("默认小程序 payload 不是有效 JSON: %w", err)
	}
	delete(body, "protocol_msg_id")
	delete(body, "send_result")
	delete(body, "conversation_id")
	body = repairMapStringValues(body)
	injectMiniProgramStoreParams(body, instance)
	payloadBytes, _ := json.Marshal(body)
	content := firstNonBlank(strings.TrimSpace(fmt.Sprint(body["title"])), strings.TrimSpace(fmt.Sprint(body["appname"])), "小程序")
	_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_default_weapp_"+strs.UUID(), enums.IMMessageTypeMiniProgram, content, string(payloadBytes), systemOperator(), requestID)
	return err
}

func injectMiniProgramStoreParams(body map[string]any, instance *models.WxWorkProtocolInstance) {
	if body == nil || instance == nil {
		return
	}
	storeID := instance.StoreID
	storeName := ""
	storeCode := ""
	if storeID > 0 && sqls.DB() != nil {
		if store := StoreService.Get(storeID); store != nil {
			storeCode = strings.TrimSpace(store.StoreCode)
			if storeName == "" {
				storeName = utils.RepairMojibakeText(strings.TrimSpace(store.Name))
			}
		}
	}
	params := map[string]string{}
	if storeID > 0 {
		params["storeId"] = strconv.FormatInt(storeID, 10)
	}
	if storeCode != "" {
		params["storeCode"] = storeCode
	}
	if storeName != "" {
		params["storeName"] = storeName
	}
	if len(params) == 0 {
		return
	}
	pathKey, pagePath := miniProgramPagePath(body)
	if pagePath == "" {
		pagePath = "pages/index/index"
		pathKey = "page_path"
	}
	body[pathKey] = appendMiniProgramQuery(pagePath, params)
	if pathKey != "page_path" {
		body["page_path"] = body[pathKey]
	}
}

func miniProgramPagePath(body map[string]any) (string, string) {
	for _, key := range []string{"page_path", "pagePath", "path"} {
		value := strings.TrimSpace(fmt.Sprint(body[key]))
		if value != "" && value != "<nil>" {
			return key, value
		}
	}
	return "", ""
}

func appendMiniProgramQuery(pagePath string, params map[string]string) string {
	pagePath = strings.TrimSpace(pagePath)
	if pagePath == "" {
		pagePath = "pages/index/index"
	}
	base := pagePath
	rawQuery := ""
	if idx := strings.Index(pagePath, "?"); idx >= 0 {
		base = pagePath[:idx]
		rawQuery = pagePath[idx+1:]
	}
	values, _ := url.ParseQuery(rawQuery)
	for key, value := range params {
		value = strings.TrimSpace(value)
		if value != "" {
			values.Set(key, value)
		}
	}
	encoded := values.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

type serviceTaskDraft struct {
	Kind      string `json:"kind"`
	Category  string `json:"category"`
	Priority  string `json:"priority"`
	RoomNo    string `json:"roomNo"`
	RawText   string `json:"rawText"`
	CreatedAt string `json:"createdAt"`
}

func (s *wxWorkProtocolDefaultResourceService) handleServiceTask(conversation *models.Conversation, text string, requestID string) error {
	draft := buildServiceTaskDraft(text)
	if draft.Kind == "" {
		draft.Kind = "服务需求"
	}
	if draft.RoomNo == "" {
		payload, _ := json.Marshal(draft)
		if err := ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionServiceTask, string(payload), time.Now().Add(10*time.Minute)); err != nil {
			return err
		}
		_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_service_task_ask_room_"+strs.UUID(), enums.IMMessageTypeText, "房间号发我一下，我好登记。", "", systemOperator(), requestID)
		return err
	}
	return s.createServiceTaskTicket(conversation, draft, requestID)
}

func (s *wxWorkProtocolDefaultResourceService) consumePendingServiceTask(conversation *models.Conversation, text string, requestID string) (bool, error) {
	payload, ok, err := ConversationRouteService.ConsumePendingAction(conversation.ID, enums.ConversationPendingActionServiceTask, time.Now())
	if err != nil || !ok {
		return false, err
	}
	draft := serviceTaskDraft{}
	_ = json.Unmarshal([]byte(payload), &draft)
	if room := extractRoomNo(text); room != "" {
		draft.RoomNo = room
	}
	if draft.RoomNo == "" {
		if err := ConversationRouteService.SetPendingAction(conversation.ID, enums.ConversationPendingActionServiceTask, payload, time.Now().Add(10*time.Minute)); err != nil {
			return true, err
		}
		_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_service_task_room_retry_"+strs.UUID(), enums.IMMessageTypeText, "我还差房间号，发我就行。", "", systemOperator(), requestID)
		return true, err
	}
	return true, s.createServiceTaskTicket(conversation, draft, requestID)
}

func (s *wxWorkProtocolDefaultResourceService) createServiceTaskTicket(conversation *models.Conversation, draft serviceTaskDraft, requestID string) error {
	title := strings.TrimSpace(draft.Kind)
	if title == "" {
		title = "服务需求"
	}
	description := fmt.Sprintf("客户房间：%s\n需求：%s\n原话：%s", draft.RoomNo, title, strings.TrimSpace(draft.RawText))
	ticket, err := TicketService.CreateTicket(request.CreateTicketRequest{
		Title:          title + " - " + draft.RoomNo,
		Description:    description,
		Category:       draft.Category,
		Priority:       draft.Priority,
		RoomNo:         draft.RoomNo,
		Source:         string(enums.TicketSourceAIService),
		Channel:        "wxwork_protocol",
		CustomerID:     conversation.CustomerID,
		ConversationID: conversation.ID,
	}, systemOperator())
	if err != nil {
		_, _ = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_service_task_failed_"+strs.UUID(), enums.IMMessageTypeText, "我这边登记没成功，先帮你转同事处理。", "", systemOperator(), requestID)
		if aiAgent := AIAgentService.Get(conversation.AIAgentID); aiAgent != nil {
			_, _ = ConversationHumanDispatchService.HandoffByAIWithRequestID(conversation.ID, *aiAgent, "服务工单创建失败", requestID)
		} else {
			_, _ = ConversationRouteService.EnterHQAgentDeskPending(conversation.ID, "服务工单创建失败", time.Now())
		}
		return err
	}
	content := fmt.Sprintf("登记好了，房间%s，%s。", draft.RoomNo, shortServiceTaskKind(title))
	if ticket != nil && ticket.TicketNo != "" {
		content = fmt.Sprintf("登记好了，房间%s，%s。", draft.RoomNo, shortServiceTaskKind(title))
	}
	_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_service_task_done_"+strs.UUID(), enums.IMMessageTypeText, content, "", systemOperator(), requestID)
	return err
}

func buildServiceTaskDraft(text string) serviceTaskDraft {
	category, priority := detectServiceTaskCategoryPriority(text)
	return serviceTaskDraft{
		Kind:      detectServiceTaskKind(text),
		Category:  category,
		Priority:  priority,
		RoomNo:    extractRoomNo(text),
		RawText:   strings.TrimSpace(text),
		CreatedAt: time.Now().Format(time.RFC3339),
	}
}

func detectServiceTaskCategoryPriority(text string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(text))
	priority := "normal"
	if containsAny(lower, []string{"漏水", "马桶", "停电", "打不开门", "门锁", "危险", "摔倒", "异味很重", "空调坏"}) {
		priority = "high"
	}
	switch {
	case containsAny(lower, []string{"拖鞋", "牙刷", "纸巾", "浴巾", "毛巾", "矿泉水", "送水", "瓶水"}):
		return "delivery", priority
	case containsAny(lower, []string{"打扫", "保洁", "卫生", "清理"}):
		return "cleaning", priority
	case containsAny(lower, []string{"维修", "漏水", "马桶", "空调", "电视", "门锁", "停电"}):
		return "maintenance", priority
	case containsAny(lower, []string{"叫醒"}):
		return "wake_up", priority
	case containsAny(lower, []string{"行李", "寄存"}):
		return "luggage", priority
	default:
		return "general", priority
	}
}

func detectServiceTaskKind(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	cases := []struct{ key, label string }{
		{"送水", "送水"}, {"矿泉水", "送水"}, {"瓶水", "送水"}, {"水", "送水"},
		{"拖鞋", "送拖鞋"}, {"牙刷", "送牙刷"}, {"纸巾", "送纸巾"}, {"浴巾", "送浴巾"}, {"毛巾", "送毛巾"},
		{"打扫", "打扫房间"}, {"保洁", "打扫房间"}, {"卫生", "打扫房间"},
		{"维修", "维修"}, {"漏水", "维修"}, {"马桶", "维修"}, {"空调", "维修"}, {"电视", "维修"},
		{"叫醒", "叫醒服务"}, {"行李", "行李协助"},
	}
	for _, item := range cases {
		if strings.Contains(lower, strings.ToLower(item.key)) {
			return item.label
		}
	}
	return ""
}

func shortServiceTaskKind(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return "需求已记录"
	}
	return kind + "已记录"
}

func extractRoomNo(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] < '0' || runes[i] > '9' {
			continue
		}
		j := i
		for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
			j++
		}
		if j-i >= 3 && j-i <= 5 {
			return string(runes[i:j])
		}
		i = j
	}
	return ""
}

func wantsServiceTask(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{"送水", "矿泉水", "拖鞋", "牙刷", "纸巾", "浴巾", "毛巾", "打扫", "保洁", "维修", "漏水", "马桶", "空调坏", "电视坏", "叫醒", "行李"}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func wantsDirectStoreLocation(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{
		"发定位", "发个定位", "定位发我", "定位发一下", "把定位发", "把位置发", "发个位置", "位置发我", "导航发我", "给我定位", "给我发定位",
		"酒店定位发我", "门店位置", "酒店定位", "酒店位置发", "店位置发", "酒店在哪里", "酒店在哪", "门店在哪里", "门店在哪", "你们在哪", "你们在哪里",
		"怎么去", "怎么走", "到店路线", "导航路线", "酒店地址", "门店地址", "地址发我", "位置在哪", "在哪儿", "在哪里啊", "在哪里呀",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func isPositiveConfirmation(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	trimmed := strings.Trim(text, " ，。,.!！?？~～\n\t")
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) > 12 {
		return false
	}
	phrases := []string{"可以", "行", "好", "好的", "好啊", "好呀", "好呢", "发", "发啊", "发吧", "发我", "给我发", "要", "要的", "嗯", "嗯嗯", "对", "对的", "是", "是的", "ok", "okay", "yes"}
	for _, phrase := range phrases {
		if trimmed == phrase {
			return true
		}
	}
	return false
}

func wantsLocationDiscussion(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	if wantsDirectStoreLocation(text) {
		return false
	}
	keywords := []string{"定位", "地址", "位置", "导航", "路线", "离我多远"}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func repairMapStringValues(values map[string]any) map[string]any {
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			values[key] = utils.RepairMojibakeText(strings.TrimSpace(typed))
		case map[string]any:
			values[key] = repairMapStringValues(typed)
		case []any:
			for i := range typed {
				if nested, ok := typed[i].(map[string]any); ok {
					typed[i] = repairMapStringValues(nested)
				} else if text, ok := typed[i].(string); ok {
					typed[i] = utils.RepairMojibakeText(strings.TrimSpace(text))
				}
			}
			values[key] = typed
		}
	}
	return values
}

func defaultResourceIntentText(message *models.Message) string {
	if message == nil {
		return ""
	}
	parts := []string{strings.TrimSpace(message.Content)}
	if raw := strings.TrimSpace(message.Payload); raw != "" {
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			for _, key := range []string{"mediaText", "mediaSummary", "text", "summary"} {
				value := strings.TrimSpace(fmt.Sprint(payload[key]))
				if value != "" && value != "<nil>" {
					parts = append(parts, value)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func wantsDefaultMiniProgram(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{"小程序", "安心宿", "自由家", "开发票", "发票入口", "订单", "续住", "退房", "入住码", "电子房卡", "办入住", "办理入住", "自助入住", "入住办理", "怎么入住", "入住入口", "查订单"}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func wantsCheckInMiniProgram(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{"办入住", "办理入住", "自助入住", "入住办理", "怎么入住", "入住入口", "入住码"}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func systemOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 0, Username: "system", Nickname: "system"}
}
