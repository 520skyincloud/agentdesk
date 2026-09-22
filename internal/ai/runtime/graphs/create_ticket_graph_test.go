package graphs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/compose"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type ticketGraphMemoryCheckpointStore struct {
	items map[string][]byte
}

func (s *ticketGraphMemoryCheckpointStore) Get(_ context.Context, checkPointID string) ([]byte, bool, error) {
	item, ok := s.items[checkPointID]
	return item, ok, nil
}

func (s *ticketGraphMemoryCheckpointStore) Set(_ context.Context, checkPointID string, data []byte) error {
	s.items[checkPointID] = append([]byte(nil), data...)
	return nil
}

func TestCreateTicketConfirmationCreatesOnceAndResolvedInterruptBlocksDuplicate(t *testing.T) {
	tests := []struct {
		name         string
		requestText  string
		confirmation string
	}{
		{name: "direct registration follow-up", requestText: "需要，帮我登记", confirmation: "确认"},
		{name: "natural maintenance ticket follow-up", requestText: "可以，麻烦建个维修工单", confirmation: "可以，就按这个建"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupCreateTicketGraphTestDB(t)
			conversation := models.Conversation{LastMessageSummary: "1304房空调不制冷；客户回复：" + tt.requestText}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatalf("create conversation: %v", err)
			}

			graph := compose.NewGraph[string, string]()
			createTicket := NewCreateTicketGraph(conversation, models.AIAgent{Name: "AI客服"})
			if err := graph.AddLambdaNode("create_ticket", compose.InvokableLambda(createTicket.Run)); err != nil {
				t.Fatalf("add ticket node: %v", err)
			}
			if err := graph.AddEdge(compose.START, "create_ticket"); err != nil {
				t.Fatalf("add start edge: %v", err)
			}
			if err := graph.AddEdge("create_ticket", compose.END); err != nil {
				t.Fatalf("add end edge: %v", err)
			}
			checkPointID := "ticket-confirm-" + strings.ReplaceAll(tt.name, " ", "-")
			runner, err := graph.Compile(context.Background(),
				compose.WithCheckPointStore(&ticketGraphMemoryCheckpointStore{items: make(map[string][]byte)}),
				compose.WithGraphName("ticket_confirmation"),
			)
			if err != nil {
				t.Fatalf("compile ticket graph: %v", err)
			}

			arguments := `{"title":"空调维修","description":"1304房空调不制冷"}`
			_, err = runner.Invoke(context.Background(), arguments, compose.WithCheckPointID(checkPointID))
			interruptInfo, interrupted := compose.ExtractInterruptInfo(err)
			if !interrupted || interruptInfo == nil || len(interruptInfo.InterruptContexts) != 1 {
				t.Fatalf("ticket creation must request confirmation first: info=%#v err=%v", interruptInfo, err)
			}
			interrupt := interruptInfo.InterruptContexts[0]
			now := time.Now()
			pending := &models.ConversationInterrupt{
				ConversationID: conversation.ID,
				CheckPointID:   checkPointID,
				InterruptID:    interrupt.ID,
				InterruptType:  InterruptTypeTicketCreationConfirmation,
				Status:         "pending",
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := services.ConversationInterruptService.CreateOrUpdatePending(pending); err != nil {
				t.Fatalf("save pending ticket confirmation: %v", err)
			}

			resumeContext := compose.ResumeWithData(context.Background(), interrupt.ID, tt.confirmation)
			output, err := runner.Invoke(resumeContext, "", compose.WithCheckPointID(checkPointID))
			if err != nil || !strings.Contains(output, `"action":"ticket_created"`) {
				t.Fatalf("explicit confirmation did not create the ticket: output=%q err=%v", output, err)
			}
			pending = services.ConversationInterruptService.FindLatestPendingByConversationID(conversation.ID)
			if pending == nil {
				t.Fatal("pending confirmation disappeared before production marked it resolved")
			}
			if err := services.ConversationInterruptService.MarkResolved(pending.ID, 1001); err != nil {
				t.Fatalf("mark ticket confirmation resolved: %v", err)
			}

			if duplicateTarget := services.ConversationInterruptService.FindLatestPendingByConversationID(conversation.ID); duplicateTarget != nil {
				t.Fatalf("a repeated confirmation could re-enter the resolved ticket graph: %#v", duplicateTarget)
			}
			var ticketCount int64
			if err := db.Model(&models.Ticket{}).Where("conversation_id = ?", conversation.ID).Count(&ticketCount).Error; err != nil {
				t.Fatalf("count tickets: %v", err)
			}
			if ticketCount != 1 {
				t.Fatalf("confirmation must create exactly one ticket, got %d", ticketCount)
			}
		})
	}
}

func setupCreateTicketGraphTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.ConversationInterrupt{},
		&models.Ticket{},
		&models.TicketTag{},
		&models.TicketProgress{},
		&models.TicketNoSequence{},
	); err != nil {
		t.Fatalf("auto migrate ticket graph tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		eventbus.WaitAsync[events.TicketCreatedEvent]()
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
