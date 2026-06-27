package event_handlers

import (
	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/services"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mlogclub/simple/common/strs"
)

func init() {
	eventbus.
		Register[events.TicketAssignedEvent]().
		Subscribe(handleTicketAssignedNotify)
}

func handleTicketAssignedNotify(ctx context.Context, event events.TicketAssignedEvent) error {
	if event.TicketID <= 0 || event.ToUserID <= 0 {
		return nil
	}
	ticket := services.TicketService.Get(event.TicketID)
	if ticket == nil {
		return nil
	}
	content := buildTicketAssignedNotifyBody(ticket, event.ToUserID, event.Reason)
	return services.WxWorkNotifyService.SendTextToAssigneeOrDefault(event.ToUserID, "工单指派提醒", content)
}

func buildTicketAssignedNotifyBody(ticket *models.Ticket, assigneeID int64, reason string) string {
	if ticket == nil {
		return ""
	}
	lines := []string{
		fmt.Sprintf("工单号: %s", strs.DefaultIfBlank(ticket.TicketNo, fmt.Sprintf("#%d", ticket.ID))),
		fmt.Sprintf("工单标题: %s", strs.DefaultIfBlank(ticket.Title, "-")),
		fmt.Sprintf("当前状态: %s", enums.GetTicketStatusLabel(ticket.Status)),
		fmt.Sprintf("处理人: %s", resolveNotifyUserLabel(assigneeID)),
	}
	if strings.TrimSpace(reason) != "" {
		lines = append(lines, fmt.Sprintf("指派原因: %s", strings.TrimSpace(reason)))
	}
	lines = append(lines, fmt.Sprintf("时间: %s", time.Now().Format("2006-01-02 15:04:05")))
	return strings.Join(lines, "\n")
}
