package services

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	conversationHistoryCursorVersion = 1
	conversationHistoryMaxSegments   = 100
)

var ConversationHistoryService = newConversationHistoryService()

type conversationHistoryService struct{}

func newConversationHistoryService() *conversationHistoryService {
	return &conversationHistoryService{}
}

type ConversationHistorySegment struct {
	Index                     int
	ConversationID            int64
	SessionNo                 int
	StoreID                   int64
	StoreStaffBindingID       int64
	WxWorkInstanceID          int64
	ChannelID                 int64
	StartReason               string
	StoreStaffDisplayName     string
	WxWorkEmployeeDisplayName string
	StartedAt                 time.Time
	EndedAt                   *time.Time
	Status                    enums.Status
	InheritedHistory          bool
	CurrentConversation       bool
}

type ConversationHistoryMessage struct {
	Message          models.Message
	SegmentIndex     int
	InheritedHistory bool
}

type conversationHistoryCursor struct {
	Version         int    `json:"v"`
	Fingerprint     string `json:"f"`
	SegmentIndex    int    `json:"s"`
	BeforeMessageID int64  `json:"b"`
}

func (s *conversationHistoryService) ListSegments(conversation *models.Conversation) ([]ConversationHistorySegment, error) {
	if conversation == nil || conversation.ID <= 0 || conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	conversationIDs, err := s.resolveConversationLineage(conversation)
	if err != nil {
		return nil, err
	}
	return s.listSegmentsForConversationIDs(conversation, conversationIDs)
}

// ListCurrentSegments returns every protocol-account segment of the current
// physical conversation without exposing predecessor conversations.
func (s *conversationHistoryService) ListCurrentSegments(conversation *models.Conversation) ([]ConversationHistorySegment, error) {
	if conversation == nil || conversation.ID <= 0 || conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在")
	}
	return s.listSegmentsForConversationIDs(conversation, []int64{conversation.ID})
}

// CanViewLineage requires every predecessor conversation to remain inside the
// operator's current tenant/team/store scope. A feature permission never
// widens the data scope.
func (s *conversationHistoryService) CanViewLineage(conversation *models.Conversation, operator *dto.AuthPrincipal) (bool, error) {
	if conversation == nil || operator == nil {
		return false, nil
	}
	conversationIDs, err := s.resolveConversationLineage(conversation)
	if err != nil {
		return false, err
	}
	for _, conversationID := range conversationIDs {
		if !AgentTeamScopeService.CanViewConversation(operator, conversationID) {
			return false, nil
		}
	}
	return true, nil
}

func (s *conversationHistoryService) listSegmentsForConversationIDs(
	conversation *models.Conversation,
	conversationIDs []int64,
) ([]ConversationHistorySegment, error) {
	segments := make([]ConversationHistorySegment, 0, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		item := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, conversation.TenantID)
		if item == nil || item.StoreID != conversation.StoreID || item.CustomerID != conversation.CustomerID {
			return nil, errorsx.BusinessError(1, "会话继承历史范围不一致，请联系管理员修复")
		}
		sessions := repositories.ConversationChannelSessionRepository.FindByConversation(sqls.DB(), conversation.TenantID, item.ID)
		if len(sessions) == 0 {
			startedAt := item.CreatedAt
			if startedAt.IsZero() {
				startedAt = item.LastActiveAt
			}
			sessions = []models.ConversationChannelSession{{
				TenantID: item.TenantID, ConversationID: item.ID, SessionNo: 1,
				StoreID: item.StoreID, StoreStaffBindingID: item.StoreStaffBindingID,
				ChannelID: item.ChannelID, StartReason: "historical_backfill",
				StartedAt: startedAt, Status: enums.StatusDisabled,
			}}
		}
		slices.SortFunc(sessions, func(left, right models.ConversationChannelSession) int {
			return left.SessionNo - right.SessionNo
		})
		for i := range sessions {
			session := &sessions[i]
			if session.SessionNo <= 0 {
				continue
			}
			segments = append(segments, ConversationHistorySegment{
				Index: len(segments), ConversationID: item.ID, SessionNo: session.SessionNo,
				StoreID: item.StoreID, StoreStaffBindingID: item.StoreStaffBindingID,
				WxWorkInstanceID: session.WxWorkInstanceID, ChannelID: session.ChannelID,
				StartReason: session.StartReason, StoreStaffDisplayName: session.StoreStaffDisplayName,
				WxWorkEmployeeDisplayName: session.WxWorkEmployeeDisplayName,
				StartedAt:                 session.StartedAt, EndedAt: session.EndedAt, Status: session.Status,
				InheritedHistory: item.ID != conversation.ID, CurrentConversation: item.ID == conversation.ID,
			})
			if len(segments) > conversationHistoryMaxSegments {
				return nil, errorsx.BusinessError(1, "会话继承历史过长，请联系管理员检查")
			}
		}
	}
	return segments, nil
}

func (s *conversationHistoryService) ListMessages(
	conversation *models.Conversation,
	cursorText string,
	limit int,
	senderType, messageType string,
) ([]ConversationHistoryMessage, string, bool, error) {
	segments, err := s.ListSegments(conversation)
	if err != nil {
		return nil, "", false, err
	}
	if len(segments) == 0 {
		return []ConversationHistoryMessage{}, "", false, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	fingerprint := conversationHistoryFingerprint(conversation, segments, senderType, messageType)
	cursor := conversationHistoryCursor{
		Version: conversationHistoryCursorVersion, Fingerprint: fingerprint,
		SegmentIndex: len(segments) - 1,
	}
	if strings.TrimSpace(cursorText) != "" {
		parsed, parseErr := decodeConversationHistoryCursor(cursorText)
		if parseErr != nil || parsed.Version != conversationHistoryCursorVersion || parsed.Fingerprint != fingerprint ||
			parsed.SegmentIndex < 0 || parsed.SegmentIndex >= len(segments) || parsed.BeforeMessageID < 0 {
			return nil, "", false, errorsx.InvalidParam("消息历史游标已失效，请刷新会话")
		}
		cursor = parsed
	}

	newestFirst := make([]ConversationHistoryMessage, 0, limit+1)
	beforeMessageID := cursor.BeforeMessageID
	for segmentIndex := cursor.SegmentIndex; segmentIndex >= 0 && len(newestFirst) < limit+1; segmentIndex-- {
		segment := segments[segmentIndex]
		rows, queryErr := repositories.MessageRepository.FindHistorySegmentBefore(
			sqls.DB(), conversation.TenantID, segment.ConversationID, segment.SessionNo,
			beforeMessageID, limit+1-len(newestFirst), senderType, messageType,
		)
		if queryErr != nil {
			return nil, "", false, queryErr
		}
		for i := range rows {
			newestFirst = append(newestFirst, ConversationHistoryMessage{
				Message: rows[i], SegmentIndex: segmentIndex, InheritedHistory: segment.InheritedHistory,
			})
		}
		beforeMessageID = 0
	}

	hasMore := len(newestFirst) > limit
	if hasMore {
		newestFirst = newestFirst[:limit]
	}
	var nextCursor string
	if hasMore && len(newestFirst) > 0 {
		oldestReturned := newestFirst[len(newestFirst)-1]
		nextCursor, err = encodeConversationHistoryCursor(conversationHistoryCursor{
			Version: conversationHistoryCursorVersion, Fingerprint: fingerprint,
			SegmentIndex: oldestReturned.SegmentIndex, BeforeMessageID: oldestReturned.Message.ID,
		})
		if err != nil {
			return nil, "", false, err
		}
	}
	slices.Reverse(newestFirst)
	return newestFirst, nextCursor, hasMore, nil
}

func (s *conversationHistoryService) resolveConversationLineage(conversation *models.Conversation) ([]int64, error) {
	links, err := repositories.ConversationContinuityLinkRepository.FindPredecessorChain(
		sqls.DB(), conversation.TenantID, conversation.ID, conversationHistoryMaxSegments,
	)
	if err != nil {
		return nil, errorsx.BusinessError(1, "会话继承历史不是有效的线性关系，请联系管理员修复")
	}
	conversationIDs := make([]int64, 0, len(links)+1)
	seen := map[int64]struct{}{conversation.ID: {}}
	for i := len(links) - 1; i >= 0; i-- {
		link := links[i]
		if link.StoreID != conversation.StoreID || link.CustomerID != conversation.CustomerID {
			return nil, errorsx.BusinessError(1, "会话继承历史跨越门店或客户，请联系管理员修复")
		}
		if _, exists := seen[link.PredecessorConversationID]; exists {
			return nil, errorsx.BusinessError(1, "会话继承历史存在循环，请联系管理员修复")
		}
		seen[link.PredecessorConversationID] = struct{}{}
		conversationIDs = append(conversationIDs, link.PredecessorConversationID)
	}
	conversationIDs = append(conversationIDs, conversation.ID)
	return conversationIDs, nil
}

func conversationHistoryFingerprint(conversation *models.Conversation, segments []ConversationHistorySegment, senderType, messageType string) string {
	var value strings.Builder
	_, _ = fmt.Fprintf(&value, "%d:%d:%d:%d|%s|%s", conversation.TenantID, conversation.StoreID, conversation.CustomerID, conversation.ID, senderType, messageType)
	for i := range segments {
		_, _ = fmt.Fprintf(&value, "|%d:%d:%d", segments[i].Index, segments[i].ConversationID, segments[i].SessionNo)
	}
	sum := sha256.Sum256([]byte(value.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

func encodeConversationHistoryCursor(cursor conversationHistoryCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeConversationHistoryCursor(value string) (conversationHistoryCursor, error) {
	cursor := conversationHistoryCursor{}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != strings.TrimSpace(value) {
		return cursor, errorsx.InvalidParam("消息历史游标无效")
	}
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return conversationHistoryCursor{}, errorsx.InvalidParam("消息历史游标无效")
	}
	return cursor, nil
}
