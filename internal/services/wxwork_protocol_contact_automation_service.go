package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const wxWorkContactAutomationBatchSize = 200

var WxWorkProtocolContactAutomationService = &wxWorkProtocolContactAutomationService{}

type wxWorkProtocolContactAutomationService struct {
	instanceLocks sync.Map
}

type wxWorkProtocolContactRecord struct {
	Seq         string `json:"seq"`
	UserID      string `json:"user_id"`
	UnionID     string `json:"unionid"`
	Name        string `json:"name"`
	CorpID      string `json:"corp_id"`
	Flag        int    `json:"flag"`
	AddTime     int64  `json:"add_time"`
	ApplyReason string `json:"apply_reason"`
}

type wxWorkProtocolContactSyncResponse struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Data         struct {
		LastSeq     string                        `json:"last_seq"`
		ContactList []wxWorkProtocolContactRecord `json:"contact_list"`
	} `json:"data"`
}

func (s *wxWorkProtocolContactAutomationService) Scan(limit int) int {
	if limit <= 0 {
		limit = 20
	}
	cnd := sqls.NewCnd()
	staticBindingIDs := repositories.ArrivalRepository.FindActiveStaticConnectionBindingIDs(sqls.DB())
	if len(staticBindingIDs) > 0 {
		cnd.Where(
			"(auto_accept_friend_request = ? OR welcome_enabled = ? OR store_staff_binding_id IN (?))",
			true,
			true,
			staticBindingIDs,
		)
	} else {
		cnd.Where("(auto_accept_friend_request = ? OR welcome_enabled = ?)", true, true)
	}
	items := repositories.WxWorkProtocolInstanceRepository.FindActivatedCurrent(
		sqls.DB(),
		cnd.Asc("id").Limit(limit),
	)
	handled := 0
	for i := range items {
		if err := s.withInstanceLock(items[i].ID, func() error {
			return s.scanInstance(&items[i])
		}); err != nil {
			s.recordResult(items[i].ID, err)
			slog.Warn("wxwork contact automation scan failed", "instance_id", items[i].ID, "error", err)
			continue
		}
		s.recordResult(items[i].ID, nil)
		handled++
	}
	return handled
}

// HandleFriendApply 由企微好友申请回调直接触发，定时扫描只负责漏回调补偿。
func (s *wxWorkProtocolContactAutomationService) HandleFriendApply(instanceID int64) error {
	return s.handleCallback(instanceID, func(instance *models.WxWorkProtocolInstance) error {
		if !instance.AutoAcceptFriendRequest {
			return nil
		}
		return s.autoAcceptPending(instance)
	})
}

// HandleFriendChange 在联系人增量变更回调后立即发送已配置的欢迎语。
func (s *wxWorkProtocolContactAutomationService) HandleFriendChange(instanceID int64) error {
	return s.handleCallback(instanceID, func(instance *models.WxWorkProtocolInstance) error {
		if !s.ShouldProcessNewContacts(instance) {
			return nil
		}
		return s.sendWelcomeForNewContacts(instance)
	})
}

func (s *wxWorkProtocolContactAutomationService) ShouldProcessNewContacts(
	instance *models.WxWorkProtocolInstance,
) bool {
	return isActivatedCurrentWxWorkProtocolInstance(instance) &&
		(instance.WelcomeEnabled && hasWxWorkWelcomeContent(instance) ||
			ArrivalBindingTicketService.HasActiveStaticConnection(instance.ID))
}

func (s *wxWorkProtocolContactAutomationService) handleCallback(instanceID int64, handler func(*models.WxWorkProtocolInstance) error) error {
	err := s.withInstanceLock(instanceID, func() error {
		instance := WxWorkProtocolInstanceService.Get(instanceID)
		if !isActivatedCurrentWxWorkProtocolInstance(instance) {
			return nil
		}
		return handler(instance)
	})
	s.recordResult(instanceID, err)
	return err
}

func (s *wxWorkProtocolContactAutomationService) withInstanceLock(instanceID int64, handler func() error) error {
	lockValue, _ := s.instanceLocks.LoadOrStore(instanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	return handler()
}

func (s *wxWorkProtocolContactAutomationService) scanInstance(instance *models.WxWorkProtocolInstance) error {
	if !isActivatedCurrentWxWorkProtocolInstance(instance) {
		return nil
	}
	if instance.AutoAcceptFriendRequest {
		if err := s.autoAcceptPending(instance); err != nil {
			return err
		}
	}
	if s.ShouldProcessNewContacts(instance) {
		if err := s.sendWelcomeForNewContacts(instance); err != nil {
			return err
		}
	}
	return nil
}

func (s *wxWorkProtocolContactAutomationService) autoAcceptPending(instance *models.WxWorkProtocolInstance) error {
	existingContacts, err := s.loadAllContactIDs(instance)
	if err != nil {
		return fmt.Errorf("同步现有联系人失败: %w", err)
	}
	result, err := s.sync(instance, "/contact/sync_apply_contact", instance.FriendRequestSyncSeq)
	if err != nil {
		return fmt.Errorf("同步好友申请失败: %w", err)
	}
	sortContactRecords(result.Data.ContactList)
	for _, item := range result.Data.ContactList {
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		if item.Flag == 3 || existingContacts[userID] {
			if err := s.advanceSeq(instance.ID, "friend_request_sync_seq", item.Seq); err != nil {
				return err
			}
			continue
		}
		if _, err := WxWorkProtocolService.AgreeContact(instance.ID, userID, item.CorpID); err != nil {
			return fmt.Errorf("同意客户 %s 的好友申请失败: %w", userID, err)
		}
		if err := s.advanceSeq(instance.ID, "friend_request_sync_seq", item.Seq); err != nil {
			return err
		}
		existingContacts[userID] = true
		if s.ShouldProcessNewContacts(instance) {
			if err := s.sendWelcome(instance, item, "wx_friend_accept_"+strings.TrimSpace(item.Seq)); err != nil {
				return fmt.Errorf("发送新好友欢迎语失败: %w", err)
			}
		}
	}
	if len(result.Data.ContactList) == 0 || strings.TrimSpace(result.Data.LastSeq) != "" {
		if err := s.advanceSeq(instance.ID, "friend_request_sync_seq", result.Data.LastSeq); err != nil {
			return err
		}
	}
	return nil
}

func (s *wxWorkProtocolContactAutomationService) sendWelcomeForNewContacts(instance *models.WxWorkProtocolInstance) error {
	result, err := s.sync(instance, "/contact/sync_contact", instance.ContactSyncSeq)
	if err != nil {
		return fmt.Errorf("同步新增联系人失败: %w", err)
	}
	// 第一次启用只建立增量基线，避免给账号里的历史联系人批量补发欢迎语。
	if isInitialContactSyncSeq(instance.ContactSyncSeq) {
		return s.advanceSeq(instance.ID, "contact_sync_seq", result.Data.LastSeq)
	}
	sortContactRecords(result.Data.ContactList)
	for _, item := range result.Data.ContactList {
		if item.Flag == 3 || strings.TrimSpace(item.UserID) == "" || item.AddTime <= 0 {
			if err := s.advanceSeq(instance.ID, "contact_sync_seq", item.Seq); err != nil {
				return err
			}
			continue
		}
		if err := s.sendWelcome(instance, item, "wx_contact_welcome_"+strings.TrimSpace(item.Seq)); err != nil {
			return err
		}
		if err := s.advanceSeq(instance.ID, "contact_sync_seq", item.Seq); err != nil {
			return err
		}
	}
	return s.advanceSeq(instance.ID, "contact_sync_seq", result.Data.LastSeq)
}

func (s *wxWorkProtocolContactAutomationService) sendWelcome(instance *models.WxWorkProtocolInstance, contact wxWorkProtocolContactRecord, requestID string) error {
	if instance == nil || !s.ShouldProcessNewContacts(instance) {
		return nil
	}
	msg := request.WxProtocolChatMsg{
		FromUsername: strings.TrimSpace(contact.UserID),
		ToUsername:   strings.TrimSpace(instance.EmployeeUserID),
		Sender:       strings.TrimSpace(contact.UserID),
		Receiver:     strings.TrimSpace(instance.EmployeeUserID),
		Desc:         strings.TrimSpace(contact.Name),
		SenderName:   strings.TrimSpace(contact.Name),
	}
	mapping := WxWorkProtocolService.findProtocolConversationMapping(instance, msg, contact.UserID)
	var conversation *models.Conversation
	conversationCreated := false
	if mapping != nil {
		conversation = repositories.ConversationRepository.GetInTenant(
			sqls.DB(),
			mapping.ConversationID,
			instance.TenantID,
		)
	}
	if conversation == nil {
		raw, _ := json.Marshal(contact)
		var err error
		conversation, conversationCreated, err = WxWorkProtocolService.ensureConversation(
			instance,
			msg,
			contact.UserID,
			string(raw),
		)
		if err != nil {
			return err
		}
	}
	if err := WxWorkProtocolService.ensureRouteState(conversation.ID, instance); err != nil {
		return err
	}
	return s.sendNewContactResources(
		conversation,
		instance,
		requestID,
		conversationCreated,
	)
}

func (s *wxWorkProtocolContactAutomationService) sendNewContactResources(
	conversation *models.Conversation,
	instance *models.WxWorkProtocolInstance,
	requestID string,
	conversationCreated bool,
) error {
	if !conversationCreated {
		return nil
	}
	var sendErrors []error
	if err := WxWorkProtocolDefaultResourceService.SendNewFriendWelcome(
		conversation,
		instance,
		requestID,
	); err != nil {
		sendErrors = append(sendErrors, err)
	}
	if err := ArrivalBindingTicketService.SendBindingCardForNewContact(
		conversation,
		instance,
		requestID,
	); err != nil {
		sendErrors = append(sendErrors, err)
	}
	return errors.Join(sendErrors...)
}

func (s *wxWorkProtocolContactAutomationService) loadAllContactIDs(instance *models.WxWorkProtocolInstance) (map[string]bool, error) {
	ret := map[string]bool{}
	seq := ""
	for page := 0; page < 50; page++ {
		result, err := s.sync(instance, "/contact/sync_contact", seq)
		if err != nil {
			return nil, err
		}
		for _, item := range result.Data.ContactList {
			if item.Flag != 3 && strings.TrimSpace(item.UserID) != "" {
				ret[strings.TrimSpace(item.UserID)] = true
			}
		}
		next := strings.TrimSpace(result.Data.LastSeq)
		if len(result.Data.ContactList) < wxWorkContactAutomationBatchSize || next == "" || next == seq {
			break
		}
		seq = next
	}
	return ret, nil
}

func (s *wxWorkProtocolContactAutomationService) sync(instance *models.WxWorkProtocolInstance, path string, seq string) (*wxWorkProtocolContactSyncResponse, error) {
	raw, err := WxWorkProtocolService.callInstanceAPI(instance.ID, path, map[string]any{
		"seq":   strings.TrimSpace(seq),
		"limit": wxWorkContactAutomationBatchSize,
	}, nil)
	if err != nil {
		return nil, err
	}
	ret := &wxWorkProtocolContactSyncResponse{}
	if err := json.Unmarshal([]byte(raw), ret); err != nil {
		return nil, fmt.Errorf("联系人同步响应不是有效 JSON: %w", err)
	}
	return ret, nil
}

func (s *wxWorkProtocolContactAutomationService) advanceSeq(instanceID int64, column string, seq string) error {
	seq = strings.TrimSpace(seq)
	if instanceID <= 0 || seq == "" {
		return nil
	}
	return repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instanceID, map[string]any{
		column:             seq,
		"updated_at":       time.Now(),
		"update_user_name": wxWorkProtocolSystemOperatorName,
	})
}

func (s *wxWorkProtocolContactAutomationService) recordResult(instanceID int64, scanErr error) {
	updates := map[string]any{
		"contact_automation_last_at":    time.Now(),
		"contact_automation_last_error": "",
		"updated_at":                    time.Now(),
		"update_user_name":              wxWorkProtocolSystemOperatorName,
	}
	if scanErr != nil {
		updates["contact_automation_last_error"] = limitText(scanErr.Error(), 500)
	}
	_ = repositories.WxWorkProtocolInstanceRepository.Updates(sqls.DB(), instanceID, updates)
}

func hasWxWorkWelcomeContent(instance *models.WxWorkProtocolInstance) bool {
	if instance == nil {
		return false
	}
	if runtimeInstance, err := StoreService.HydrateRuntimeInstanceDB(sqls.DB(), instance); err == nil {
		instance = runtimeInstance
	}
	return strings.TrimSpace(instance.WelcomeMessage) != "" ||
		strings.TrimSpace(instance.WelcomeImageAssetID) != "" ||
		(instance.WelcomeSendMiniProgram && strings.TrimSpace(instance.DefaultMiniProgramPayload) != "") ||
		(instance.WelcomeAskLocation && strings.TrimSpace(instance.StoreLongitude) != "" && strings.TrimSpace(instance.StoreLatitude) != "")
}

func isInitialContactSyncSeq(seq string) bool {
	seq = strings.TrimSpace(seq)
	return seq == "" || seq == "0"
}

func sortContactRecords(items []wxWorkProtocolContactRecord) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.TrimSpace(items[i].Seq) < strings.TrimSpace(items[j].Seq)
	})
}
