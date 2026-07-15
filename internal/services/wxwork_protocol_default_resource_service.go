package services

import (
	"encoding/json"
	"errors"
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

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

var WxWorkProtocolDefaultResourceService = &wxWorkProtocolDefaultResourceService{}

type wxWorkProtocolDefaultResourceService struct{}

func (s *wxWorkProtocolDefaultResourceService) SendNewFriendWelcome(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	if conversation == nil || instance == nil || instance.Status != enums.StatusOk || !instance.WelcomeEnabled {
		return nil
	}
	var sendErrors []error
	requestID = tracex.NormalizeRequestID(requestID)
	if message := strings.TrimSpace(instance.WelcomeMessage); message != "" {
		_, err := MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_welcome_text_"+strs.UUID(), enums.IMMessageTypeText, utils.RepairMojibakeText(message), "", systemOperator(), requestID)
		if err != nil {
			slog.Warn("send wxwork welcome text failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			sendErrors = append(sendErrors, err)
		}
	}
	if assetID := strings.TrimSpace(instance.WelcomeImageAssetID); assetID != "" {
		if err := s.sendWelcomeImage(conversation, instance, assetID, requestID); err != nil {
			slog.Warn("send wxwork welcome image failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			sendErrors = append(sendErrors, err)
		}
	}
	if instance.WelcomeSendMiniProgram && strings.TrimSpace(instance.DefaultMiniProgramPayload) != "" {
		if err := s.sendDefaultMiniProgram(conversation, instance, requestID); err != nil {
			slog.Warn("send wxwork welcome mini program failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			sendErrors = append(sendErrors, err)
		}
	}
	if instance.WelcomeAskLocation && strings.TrimSpace(instance.StoreLongitude) != "" && strings.TrimSpace(instance.StoreLatitude) != "" {
		if err := s.sendDefaultLocation(conversation, instance, requestID); err != nil {
			slog.Warn("send wxwork welcome location failed", "conversation_id", conversation.ID, "instance_id", instance.ID, "error", err)
			sendErrors = append(sendErrors, err)
		}
	}
	return errors.Join(sendErrors...)
}

func (s *wxWorkProtocolDefaultResourceService) sendWelcomeImage(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, assetID string, requestID string) error {
	asset := AssetService.GetByAssetID(assetID)
	if asset == nil || asset.Status != enums.AssetStatusSuccess {
		return fmt.Errorf("欢迎语图片不存在或尚未上传完成")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.MimeType)), "image/") {
		return fmt.Errorf("欢迎语资源不是图片")
	}
	payload, err := buildIMMessageAssetPayload(asset)
	if err != nil {
		return err
	}
	_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_welcome_image_"+strs.UUID(), enums.IMMessageTypeImage, asset.Filename, payload, systemOperator(), requestID)
	return err
}

func (s *wxWorkProtocolDefaultResourceService) sendDefaultLocation(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	title, payload, err := s.BuildDefaultLocationMessage(instance)
	if err != nil {
		return err
	}
	_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_default_location_"+strs.UUID(), enums.IMMessageTypeLocation, title, payload, systemOperator(), requestID)
	return err
}

func (s *wxWorkProtocolDefaultResourceService) BuildDefaultLocationMessage(instance *models.WxWorkProtocolInstance) (string, string, error) {
	if instance == nil {
		return "", "", fmt.Errorf("员工号不存在")
	}
	longitude := strings.TrimSpace(instance.StoreLongitude)
	latitude := strings.TrimSpace(instance.StoreLatitude)
	if longitude == "" || latitude == "" {
		return "", "", fmt.Errorf("员工号未绑定门店坐标")
	}
	lng, err := strconv.ParseFloat(longitude, 64)
	if err != nil || lng == 0 {
		return "", "", fmt.Errorf("员工号门店经度无效")
	}
	lat, err := strconv.ParseFloat(latitude, 64)
	if err != nil || lat == 0 {
		return "", "", fmt.Errorf("员工号门店纬度无效")
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
	return title, string(payload), nil
}

func (s *wxWorkProtocolDefaultResourceService) sendDefaultMiniProgram(conversation *models.Conversation, instance *models.WxWorkProtocolInstance, requestID string) error {
	content, payload, err := s.BuildDefaultMiniProgramMessage(instance)
	if err != nil {
		return err
	}
	_, err = MessageService.SendAIMessageWithRequestID(conversation.ID, conversation.AIAgentID, "wx_default_weapp_"+strs.UUID(), enums.IMMessageTypeMiniProgram, content, payload, systemOperator(), requestID)
	return err
}

func (s *wxWorkProtocolDefaultResourceService) BuildDefaultMiniProgramMessage(instance *models.WxWorkProtocolInstance) (string, string, error) {
	if instance == nil {
		return "", "", fmt.Errorf("员工号不存在")
	}
	payload := strings.TrimSpace(instance.DefaultMiniProgramPayload)
	if payload == "" {
		return "", "", fmt.Errorf("员工号未绑定默认小程序")
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return "", "", fmt.Errorf("默认小程序 payload 不是有效 JSON: %w", err)
	}
	delete(body, "protocol_msg_id")
	delete(body, "send_result")
	delete(body, "conversation_id")
	body = repairMapStringValues(body)
	injectMiniProgramStoreParams(body, instance)
	deleteMiniProgramInternalStoreKeys(body)
	payloadBytes, _ := json.Marshal(body)
	content := firstNonBlank(strings.TrimSpace(fmt.Sprint(body["title"])), strings.TrimSpace(fmt.Sprint(body["appname"])), "小程序")
	return content, string(payloadBytes), nil
}

func (s *wxWorkProtocolDefaultResourceService) BuildDefaultPhoneMessage(instance *models.WxWorkProtocolInstance) (string, string, error) {
	if instance == nil {
		return "", "", fmt.Errorf("员工号不存在")
	}
	phone := utils.RepairMojibakeText(strings.TrimSpace(instance.StoreContactPhone))
	if phone == "" {
		return "", "", fmt.Errorf("员工号未配置联系电话")
	}
	return "酒店电话：" + phone, "", nil
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
	params := configuredMiniProgramStoreParams(body)
	if len(params) == 0 {
		if storeID > 0 {
			params["storeId"] = strconv.FormatInt(storeID, 10)
		}
		if storeCode != "" {
			params["storeCode"] = storeCode
		}
		if storeName != "" {
			params["storeName"] = storeName
		}
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

func configuredMiniProgramStoreParams(body map[string]any) map[string]string {
	params := map[string]string{}
	if body == nil {
		return params
	}
	if scene := strings.TrimSpace(fmt.Sprint(firstExistingMapValue(body, "store_scene", "storeScene", "wxacode_scene", "wxacodeScene"))); scene != "" && scene != "<nil>" {
		params["scene"] = scene
	}
	for key, value := range anyMapFromMiniProgramPayload(body, "store_query_params", "storeQueryParams", "store_params", "storeParams") {
		key = strings.TrimSpace(key)
		text := strings.TrimSpace(fmt.Sprint(value))
		if key != "" && text != "" && text != "<nil>" {
			params[key] = text
		}
	}
	return params
}

func deleteMiniProgramInternalStoreKeys(body map[string]any) {
	for _, key := range []string{"store_scene", "storeScene", "wxacode_scene", "wxacodeScene", "store_query_params", "storeQueryParams", "store_params", "storeParams"} {
		delete(body, key)
	}
}

func firstExistingMapValue(body map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := body[key]; ok {
			return value
		}
	}
	return ""
}

func anyMapFromMiniProgramPayload(body map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		value, ok := body[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			return typed
		case map[string]string:
			result := map[string]any{}
			for k, v := range typed {
				result[k] = v
			}
			return result
		case string:
			result := map[string]any{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(typed)), &result); err == nil {
				return result
			}
		}
	}
	return map[string]any{}
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
	} else if !isLikelyServiceTaskContinuation(text, draft) {
		return false, nil
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
		if aiAgent := AIAgentService.GetByTenantID(conversation.AIAgentID, conversation.TenantID); aiAgent != nil {
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
	if shouldLetReplyRuntimeHandle(text) {
		return false
	}
	if looksLikeQuestionNotServiceAction(text) && !hasExplicitServiceAction(text) {
		return false
	}
	keywords := []string{"送水", "送瓶水", "送两瓶水", "拿水", "补水", "要水", "需要水", "缺水", "送拖鞋", "拿拖鞋", "补拖鞋", "要拖鞋", "需要拖鞋", "缺拖鞋", "送牙刷", "拿牙刷", "补牙刷", "要牙刷", "需要牙刷", "送纸巾", "补纸巾", "要纸巾", "送浴巾", "补浴巾", "要浴巾", "送毛巾", "补毛巾", "要毛巾", "打扫", "保洁", "维修", "漏水", "马桶坏", "空调坏", "电视坏", "叫醒", "拿行李", "搬行李"}
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	if hasExplicitServiceAction(text) && containsAny(text, []string{"水", "拖鞋", "牙刷", "纸巾", "浴巾", "毛巾", "行李"}) {
		return true
	}
	return false
}

func hasExplicitServiceAction(text string) bool {
	return containsAny(text, []string{"送", "拿", "补", "要", "需要", "缺", "没有", "没给", "坏", "漏水", "打不开", "打扫", "保洁", "维修", "叫醒", "搬"})
}

func shouldLetReplyRuntimeHandle(text string) bool {
	return containsAny(text, []string{"在哪里拿", "在哪拿", "哪里拿", "去哪拿", "去哪里拿", "在哪里领", "在哪领", "哪里领", "怎么领", "怎么拿", "自取", "领取"})
}

func isLikelyServiceTaskContinuation(text string, draft serviceTaskDraft) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || shouldLetReplyRuntimeHandle(text) || looksLikeQuestionNotServiceAction(text) {
		return false
	}
	if wantsServiceTask(text) {
		return true
	}
	kind := strings.ToLower(strings.TrimSpace(draft.Kind + " " + draft.RawText))
	if kind == "" {
		return false
	}
	return containsAny(text, []string{"送", "拿", "补", "要", "需要", "缺", "没有", "没给", "再来", "再拿"}) && containsAny(text, serviceTaskItemKeywords(kind))
}

func serviceTaskItemKeywords(kind string) []string {
	items := []string{"水", "拖鞋", "牙刷", "纸巾", "浴巾", "毛巾", "打扫", "保洁", "维修", "马桶", "空调", "电视", "叫醒", "行李"}
	ret := make([]string, 0, len(items))
	for _, item := range items {
		if strings.Contains(kind, item) {
			ret = append(ret, item)
		}
	}
	return ret
}

func looksLikeQuestionNotServiceAction(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	return containsAny(text, []string{
		"有几", "几双", "几个", "多少", "多少钱", "要钱", "收费", "免费", "有没有", "有吗", "在哪", "哪里", "怎么", "能不能", "可以不", "可以吗",
		"停车", "停车场", "充电", "充电枪", "充电桩", "wifi", "发票", "早餐", "退房", "洗衣", "投屏",
	})
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

func systemOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 0, Username: "system", Nickname: "system"}
}
