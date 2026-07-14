package services_test

import (
	"testing"

	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

func TestSystemTicketCreationInheritsConversationTenant(t *testing.T) {
	setupTicketTestDB(t)
	customerID := createTestCustomerInTenant(t, "system-ticket-customer", 303)
	conversationID := createTestConversationInTenant(t, customerID, "system-ticket-conversation", 303)
	system := &dto.AuthPrincipal{UserID: 0, Username: "system", Nickname: "system"}

	direct, err := services.TicketService.CreateTicket(request.CreateTicketRequest{
		Title:          "system service ticket",
		Description:    "system service ticket description",
		CustomerID:     customerID,
		ConversationID: conversationID,
	}, system)
	if err != nil {
		t.Fatalf("create system service ticket: %v", err)
	}
	fromConversation, err := services.TicketService.CreateFromConversation(request.CreateTicketFromConversationRequest{
		ConversationID: conversationID,
		Title:          "AI confirmed ticket",
		Description:    "AI confirmed ticket description",
	}, system)
	if err != nil {
		t.Fatalf("create AI conversation ticket: %v", err)
	}
	for _, item := range []struct {
		name string
		id   int64
	}{
		{name: "direct", id: direct.ID},
		{name: "conversation", id: fromConversation.ID},
	} {
		ticket := repositories.TicketRepository.GetInTenant(sqls.DB(), item.id, 303)
		if ticket == nil || ticket.TenantID != 303 || ticket.ConversationID != conversationID {
			t.Fatalf("%s system ticket tenant mismatch: %+v", item.name, ticket)
		}
		progresses := repositories.TicketProgressRepository.Find(sqls.DB(), sqls.NewCnd().Eq("ticket_id", item.id))
		if len(progresses) != 1 || progresses[0].TenantID != 303 {
			t.Fatalf("%s system ticket progress tenant mismatch: %+v", item.name, progresses)
		}
	}
}

func TestTicketAndTagRuntimeTenantIsolation(t *testing.T) {
	setupTicketTestDB(t)

	adminA := createTestOperatorInTenant(t, "tenant-a-admin", 101)
	adminB := createTestOperatorInTenant(t, "tenant-b-admin", 202)
	assigneeA := createTestOperatorInTenant(t, "tenant-a-assignee", 101)
	assigneeB := createTestOperatorInTenant(t, "tenant-b-assignee", 202)
	customerA := createTestCustomerInTenant(t, "tenant-a-customer", 101)
	customerB := createTestCustomerInTenant(t, "tenant-b-customer", 202)
	conversationA := createTestConversationInTenant(t, customerA, "tenant-a-conversation", 101)
	conversationB := createTestConversationInTenant(t, customerB, "tenant-b-conversation", 202)
	tagA := createTestTagInTenant(t, "tenant-a-tag", 101)
	tagB := createTestTagInTenant(t, "tenant-b-tag", 202)

	ticketA, err := services.TicketService.CreateTicket(request.CreateTicketRequest{
		Title:             "A tenant ticket",
		Description:       "A tenant ticket description",
		CustomerID:        customerA,
		ConversationID:    conversationA,
		CurrentAssigneeID: assigneeA.UserID,
		TagIDs:            []int64{tagA},
	}, adminA)
	if err != nil {
		t.Fatalf("create tenant A ticket: %v", err)
	}
	ticketB, err := services.TicketService.CreateTicket(request.CreateTicketRequest{
		Title:             "B tenant ticket",
		Description:       "B tenant ticket description",
		CustomerID:        customerB,
		ConversationID:    conversationB,
		CurrentAssigneeID: assigneeB.UserID,
		TagIDs:            []int64{tagB},
	}, adminB)
	if err != nil {
		t.Fatalf("create tenant B ticket: %v", err)
	}
	if ticketA.TenantID != 101 || ticketB.TenantID != 202 {
		t.Fatalf("ticket tenant inheritance mismatch: A=%d B=%d", ticketA.TenantID, ticketB.TenantID)
	}
	assertTicketChildrenTenant(t, ticketA.ID, 101)
	assertTicketChildrenTenant(t, ticketB.ID, 202)

	aggregateA, err := services.TicketService.FindPageAggregateByCnd(sqls.NewCnd().Page(1, 20), adminA)
	if err != nil {
		t.Fatalf("list tenant A tickets: %v", err)
	}
	if len(aggregateA.List) != 1 || aggregateA.List[0].ID != ticketA.ID {
		t.Fatalf("tenant A list leaked tickets: %+v", aggregateA.List)
	}
	if len(aggregateA.TagsByTicketID[ticketA.ID]) != 1 || aggregateA.TagsByTicketID[ticketA.ID][0].ID != tagA {
		t.Fatalf("tenant A aggregate tags mismatch: %+v", aggregateA.TagsByTicketID)
	}
	if aggregateA.Customers[customerA] == nil || aggregateA.Users[assigneeA.UserID] == nil {
		t.Fatalf("tenant A aggregate enrichment missing: customers=%+v users=%+v", aggregateA.Customers, aggregateA.Users)
	}
	if _, err := services.TicketService.GetDetail(ticketB.ID, adminA); err == nil {
		t.Fatal("tenant A must not read tenant B ticket detail")
	}
	if _, err := services.TicketService.ListProgress(ticketB.ID, adminA); err == nil {
		t.Fatal("tenant A must not read tenant B ticket progress")
	}

	if err := services.TicketService.UpdateTicket(request.UpdateTicketRequest{
		TicketID:          ticketB.ID,
		Title:             "cross-tenant update",
		Description:       "cross-tenant update description",
		CurrentAssigneeID: assigneeA.UserID,
	}, adminA); err == nil {
		t.Fatal("tenant A must not update tenant B ticket")
	}
	if err := services.TicketService.LinkCustomer(ticketB.ID, customerA, adminA); err == nil {
		t.Fatal("tenant A must not link tenant B ticket")
	}
	if err := services.TicketService.AssignTicket(request.AssignTicketRequest{TicketID: ticketB.ID, ToUserID: assigneeA.UserID}, adminA); err == nil {
		t.Fatal("tenant A must not assign tenant B ticket")
	}
	if err := services.TicketService.ChangeStatus(request.ChangeTicketStatusRequest{TicketID: ticketB.ID, Status: string(enums.TicketStatusDone)}, adminA); err == nil {
		t.Fatal("tenant A must not change tenant B ticket status")
	}
	if _, err := services.TicketService.AddProgress(request.CreateTicketProgressRequest{TicketID: ticketB.ID, Content: "cross-tenant progress"}, adminA); err == nil {
		t.Fatal("tenant A must not add tenant B ticket progress")
	}

	crossTenantRequests := []request.CreateTicketRequest{
		{Title: "cross customer", Description: "cross customer", CustomerID: customerB},
		{Title: "cross conversation", Description: "cross conversation", ConversationID: conversationB},
		{Title: "cross assignee", Description: "cross assignee", CurrentAssigneeID: assigneeB.UserID},
		{Title: "cross tag", Description: "cross tag", TagIDs: []int64{tagB}},
	}
	for _, req := range crossTenantRequests {
		if _, err := services.TicketService.CreateTicket(req, adminA); err == nil {
			t.Fatalf("tenant A create accepted cross-tenant references: %+v", req)
		}
	}

	currentB := repositories.TicketRepository.GetInTenant(sqls.DB(), ticketB.ID, 202)
	if currentB == nil || currentB.Title != ticketB.Title || currentB.Status != enums.TicketStatusPending || currentB.CurrentAssigneeID != assigneeB.UserID {
		t.Fatalf("tenant B ticket changed after rejected operations: %+v", currentB)
	}
	progressesB := repositories.TicketProgressRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", 202).Eq("ticket_id", ticketB.ID))
	if len(progressesB) != 1 {
		t.Fatalf("tenant B progress changed after rejected operations: %+v", progressesB)
	}

	viewA, err := services.TicketViewService.Save(request.SaveTicketViewRequest{Name: "A view", Filters: map[string]any{"status": "pending"}}, adminA)
	if err != nil {
		t.Fatalf("save tenant A view: %v", err)
	}
	viewB, err := services.TicketViewService.Save(request.SaveTicketViewRequest{Name: "B view", Filters: map[string]any{"status": "pending"}}, adminB)
	if err != nil {
		t.Fatalf("save tenant B view: %v", err)
	}
	viewsA, err := services.TicketViewService.ListForOperator(adminA)
	if err != nil || len(viewsA) != 1 || viewsA[0].ID != viewA.ID || viewsA[0].TenantID != 101 {
		t.Fatalf("tenant A views leaked: views=%+v err=%v", viewsA, err)
	}
	if _, err := services.TicketViewService.Save(request.SaveTicketViewRequest{ID: viewB.ID, Name: "cross view update", Filters: map[string]any{}}, adminA); err == nil {
		t.Fatal("tenant A must not update tenant B view")
	}
	if err := services.TicketViewService.Delete(viewB.ID, adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B view")
	}

	tagsA, _, err := services.TagService.FindPageForOperator(sqls.NewCnd().Page(1, 20), adminA)
	if err != nil || len(tagsA) != 1 || tagsA[0].ID != tagA {
		t.Fatalf("tenant A tag list leaked: tags=%+v err=%v", tagsA, err)
	}
	if _, err := services.TagService.GetForOperator(tagB, adminA); err == nil {
		t.Fatal("tenant A must not read tenant B tag")
	}
	if _, err := services.TagService.CreateTag(request.CreateTagRequest{ParentID: tagB, Name: "cross parent"}, adminA); err == nil {
		t.Fatal("tenant A must not create below tenant B tag")
	}
	if err := services.TagService.UpdateTag(request.UpdateTagRequest{ID: tagB, CreateTagRequest: request.CreateTagRequest{Name: "cross update"}}, adminA); err == nil {
		t.Fatal("tenant A must not update tenant B tag")
	}
	if err := services.TagService.UpdateStatus(tagB, int(enums.StatusDisabled), adminA); err == nil {
		t.Fatal("tenant A must not change tenant B tag status")
	}
	if err := services.TagService.UpdateSort([]int64{tagB}, adminA); err == nil {
		t.Fatal("tenant A must not reorder tenant B tag")
	}
	if err := services.TagService.DeleteTag(tagB, adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B tag")
	}

	if err := services.ConversationTagService.AddTag(request.AddConversationTagRequest{ConversationID: conversationB, TagID: tagB}, adminB); err != nil {
		t.Fatalf("add tenant B conversation tag: %v", err)
	}
	if err := services.ConversationTagService.AddTag(request.AddConversationTagRequest{ConversationID: conversationA, TagID: tagA}, adminA); err != nil {
		t.Fatalf("add tenant A conversation tag: %v", err)
	}
	if err := services.ConversationTagService.AddTag(request.AddConversationTagRequest{ConversationID: conversationA, TagID: tagB}, adminA); err == nil {
		t.Fatal("tenant A must not add tenant B tag to its conversation")
	}
	if err := services.ConversationTagService.RemoveTag(request.RemoveConversationTagRequest{ConversationID: conversationB, TagID: tagB}, adminA); err == nil {
		t.Fatal("tenant A must not remove tenant B conversation tag")
	}
	relationB := repositories.ConversationTagRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("tenant_id", 202).Eq("conversation_id", conversationB).Eq("tag_id", tagB))
	if relationB == nil {
		t.Fatal("tenant B conversation tag was removed by tenant A")
	}

	if summaryA := services.TicketService.GetSummary(adminA); summaryA.All != 1 || summaryA.Mine != 0 {
		t.Fatalf("tenant A summary leaked: %+v", summaryA)
	}
	if summaryB := services.TicketService.GetSummary(adminB); summaryB.All != 1 || summaryB.Mine != 0 {
		t.Fatalf("tenant B summary leaked: %+v", summaryB)
	}
}

func assertTicketChildrenTenant(t *testing.T, ticketID, tenantID int64) {
	t.Helper()
	progresses := repositories.TicketProgressRepository.Find(sqls.DB(), sqls.NewCnd().Eq("ticket_id", ticketID))
	if len(progresses) == 0 {
		t.Fatal("expected ticket progress")
	}
	for i := range progresses {
		if progresses[i].TenantID != tenantID {
			t.Fatalf("ticket progress tenant mismatch: %+v", progresses[i])
		}
	}
	relations := repositories.TicketTagRepository.Find(sqls.DB(), sqls.NewCnd().Eq("ticket_id", ticketID))
	if len(relations) == 0 {
		t.Fatal("expected ticket tag relation")
	}
	for i := range relations {
		if relations[i].TenantID != tenantID {
			t.Fatalf("ticket tag tenant mismatch: %+v", relations[i])
		}
	}
}
