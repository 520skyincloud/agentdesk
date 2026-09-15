package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pms/sandbox"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sandboxFixture struct {
	t     *testing.T
	db    *gorm.DB
	svc   *pmsSandboxService
	now   time.Time
	scope sandbox.Scope
	snap  *sandbox.Snapshot
	seq   map[int64]int64
}

func newSandboxFixture(t *testing.T) *sandboxFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	err = db.AutoMigrate(&models.Store{}, &models.Customer{}, &models.Conversation{}, &models.ConversationRouteState{}, &models.Message{}, &models.ChannelMessageOutbox{}, &models.ServiceRecoveryCase{},
		&models.PMSOperation{}, &models.PMSSandboxStore{}, &models.PMSSandboxDataset{}, &models.PMSSandboxRoomType{}, &models.PMSSandboxRoom{}, &models.PMSSandboxOrder{}, &models.PMSSandboxGrade{}, &models.PMSSandboxMember{}, &models.PMSSandboxRule{}, &models.PMSSandboxResource{}, &models.PMSSandboxBinding{})
	if err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{Enabled: true, Provider: "sandbox", Environment: "test-2", SandboxEnabled: true, AllowWrite: true}})
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	f := &sandboxFixture{t: t, db: db, now: now, seq: map[int64]int64{}, scope: sandbox.Scope{StoreID: 1, ConversationID: 1, CustomerID: 1}}
	f.svc = &pmsSandboxService{now: func() time.Time { return f.now }}
	for _, item := range []any{
		&models.Store{ID: 1, StoreCode: "TEST-1", Name: "测试门店1"},
		&models.Store{ID: 2, StoreCode: "TEST-2", Name: "测试门店2"},
		&models.Customer{ID: 1, Name: "客户1"},
		&models.Customer{ID: 2, Name: "客户2"},
		&models.Conversation{ID: 1, CustomerID: 1, ChannelID: 1},
		&models.Conversation{ID: 2, CustomerID: 2, ChannelID: 1},
		&models.ConversationRouteState{ConversationID: 1, StoreID: 1},
		&models.ConversationRouteState{ConversationID: 2, StoreID: 1},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.snap, err = f.svc.Initialize(context.Background(), 1, 9, false)
	if err != nil {
		t.Fatal(err)
	}
	f.snap, err = f.svc.Bind(context.Background(), 1, 9, sandbox.BindingRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, CustomerID: 1, OrderID: f.snap.Orders[0].ID, MemberID: f.snap.Members[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "可以给我升房吗")
	t.Cleanup(func() {
		sqls.SetDB(nil)
		config.SetCurrent(nil)
		raw, _ := db.DB()
		_ = raw.Close()
	})
	return f
}

func (f *sandboxFixture) message(conversationID int64, sender enums.IMSenderType, text string) int64 {
	f.t.Helper()
	f.seq[conversationID]++
	created := f.now.Add(time.Duration(f.seq[conversationID]) * time.Second)
	item := &models.Message{ConversationID: conversationID, SenderType: sender, MessageType: enums.IMMessageTypeText, Content: text, SeqNo: f.seq[conversationID], ClientMsgID: fmt.Sprintf("sandbox-%d-%d", conversationID, f.seq[conversationID]), SendStatus: enums.IMMessageStatusSent, SentAt: &created}
	item.CreatedAt, item.UpdatedAt = created, created
	if err := f.db.Create(item).Error; err != nil {
		f.t.Fatal(err)
	}
	return item.ID
}

func (f *sandboxFixture) deliver(scope sandbox.Scope, op *sandbox.Operation, sent bool) int64 {
	f.t.Helper()
	messageID := f.message(scope.ConversationID, enums.IMSenderTypeAI, sandbox.CustomerReplyText(op.PreviewText))
	status := "pending"
	var sentAt *time.Time
	if sent {
		status = "sent"
		value := f.now.Add(time.Duration(f.seq[scope.ConversationID]) * time.Second)
		sentAt = &value
	}
	item := &models.ChannelMessageOutbox{ConversationID: scope.ConversationID, MessageID: messageID, ChannelType: "wxwork_protocol", SendStatus: status, SentAt: sentAt}
	if err := f.db.Create(item).Error; err != nil {
		f.t.Fatal(err)
	}
	if err := f.svc.MarkPreviewMessage(context.Background(), scope, op.ID, messageID); err != nil {
		f.t.Fatal(err)
	}
	return messageID
}

func (f *sandboxFixture) prepare(change sandbox.ChangeRequest) *sandbox.Operation {
	f.t.Helper()
	op, err := f.svc.Prepare(context.Background(), f.scope, change)
	if err != nil {
		f.t.Fatal(err)
	}
	return op
}

func TestPMSSandboxSixSceneReadPreviewAndCommit(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	result, err := f.svc.ExecuteScene(ctx, f.scope, sandbox.SceneInput{Scene: "A", Topics: []string{"breakfast", "child_policy"}})
	if err != nil || !result.Completed || result.Reply != "您的订单包含2份早餐，供应时间为07:00–10:00。1.2米以下儿童免费用餐。" {
		t.Fatalf("A incomplete: result=%+v err=%v", result, err)
	}
	result, err = f.svc.ExecuteScene(ctx, f.scope, sandbox.SceneInput{Scene: "A", Topics: []string{"breakfast", "child_policy", "checkout"}})
	if err != nil || !result.Completed || !strings.Contains(result.Reply, "12:00") {
		t.Fatalf("A multi-topic reply lost checkout: result=%+v err=%v", result, err)
	}
	result, err = f.svc.ExecuteScene(ctx, f.scope, sandbox.SceneInput{Scene: "E", Topics: []string{"birthday", "benefits"}})
	if err != nil || !result.Completed || result.Reply != "您当前是钻石会员，已入住 16 次。生日可享 100 元礼遇，有效期 30 天。" {
		t.Fatalf("E incomplete: %+v %v", result, err)
	}
	result, err = f.svc.ExecuteScene(ctx, f.scope, sandbox.SceneInput{Scene: "F"})
	if err != nil || result.Completed || result.Resource != nil {
		t.Fatalf("token alone must not count as card: %+v %v", result, err)
	}
	op := f.prepare(sandbox.ChangeRequest{Upgrade: true, LateCheckout: true, Recovery: true, Reason: "空调问题尚未解决"})
	if op.Plan.AddedCents != 0 || op.Plan.After.RoomID == op.Plan.Before.RoomID || op.Plan.After.CheckOut.Hour() != 14 || op.Plan.Commitment == "" {
		t.Fatalf("combined plan wrong: %+v", op.Plan)
	}
	before, _ := f.svc.Query(ctx, f.scope)
	if before.Order.Version != op.Plan.Before.Version {
		t.Fatal("preview must not execute")
	}
	f.deliver(f.scope, op, true)
	f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "确认办理")
	outcome, err := f.svc.Confirm(ctx, f.scope, op.ID)
	if err != nil || outcome.Status != "completed" || !strings.Contains(outcome.ResultText, "已核对并完成") {
		t.Fatalf("confirm failed: %+v %v", outcome, err)
	}
	after, _ := f.svc.Query(ctx, f.scope)
	if after.Order.Version != before.Order.Version+1 || after.Order.RoomNumber != op.Plan.After.RoomNumber || !after.Order.CheckOut.Equal(op.Plan.After.CheckOut) || after.Order.PayableCents != op.Plan.After.PayableCents {
		t.Fatalf("readback mismatch: %+v", after.Order)
	}
	repeated, err := f.svc.Confirm(ctx, f.scope, op.ID)
	if err != nil || repeated.ID != outcome.ID {
		t.Fatalf("repeated confirmation must reuse: %+v %v", repeated, err)
	}
	var recoveryCount int64
	f.db.Model(&models.ServiceRecoveryCase{}).Count(&recoveryCount)
	if recoveryCount != 1 {
		t.Fatalf("commitment must be recorded once: %d", recoveryCount)
	}
}

func TestPMSSandboxConfirmationSafety(t *testing.T) {
	for _, mode := range []string{"not_committed", "not_sent", "late_delivery", "historical", "intervening", "expired", "cancelled", "write_disabled"} {
		t.Run(mode, func(t *testing.T) {
			f := newSandboxFixture(t)
			op := f.prepare(sandbox.ChangeRequest{Upgrade: true})
			if mode != "not_committed" {
				f.deliver(f.scope, op, mode != "not_sent")
			}
			if mode == "intervening" {
				f.message(1, enums.IMSenderTypeCustomer, "先不用了，早餐在哪")
			}
			if mode != "historical" {
				f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "确认办理")
			}
			if mode == "late_delivery" {
				if err := f.db.Model(&models.ChannelMessageOutbox{}).Where("conversation_id = ?", 1).Update("sent_at", f.now.Add(time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
			}
			if mode == "expired" {
				f.now = f.now.Add(11 * time.Minute)
			}
			if mode == "cancelled" {
				if _, err := f.svc.Cancel(context.Background(), f.scope, op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "write_disabled" {
				cfg := config.Current()
				cfg.PMS.AllowWrite = false
				config.SetCurrent(&cfg)
			}
			if _, err := f.svc.Confirm(context.Background(), f.scope, op.ID); err == nil {
				t.Fatalf("%s confirmation should fail", mode)
			}
			state, _ := f.svc.Query(context.Background(), f.scope)
			if state.Order.Version != op.Plan.Before.Version {
				t.Fatalf("%s changed order", mode)
			}
		})
	}
}

func TestPMSSandboxScopePriceAndResetInvalidation(t *testing.T) {
	for _, mode := range []string{"store", "customer", "provider", "quote", "reset"} {
		t.Run(mode, func(t *testing.T) {
			f := newSandboxFixture(t)
			ctx := context.Background()
			op := f.prepare(sandbox.ChangeRequest{Upgrade: true})
			f.deliver(f.scope, op, true)
			f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "确认办理")
			switch mode {
			case "store":
				f.scope.StoreID = 2
			case "customer":
				f.scope.CustomerID = 2
			case "provider":
				if err := f.db.Model(&models.PMSOperation{}).Where("id = ?", op.ID).Update("provider", "hpms").Error; err != nil {
					t.Fatal(err)
				}
			case "quote":
				grade := f.snap.Grades[0]
				grade.FreeUpgradeMaxRank = 0
				if _, err := f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, Grade: &grade}); err != nil {
					t.Fatal(err)
				}
			case "reset":
				oldDataset := f.snap.Dataset.ID
				snap, err := f.svc.Reset(ctx, 1, 9, f.snap.Dataset.ID, f.snap.Dataset.Version)
				if err != nil || snap.Dataset.ID == oldDataset {
					t.Fatalf("reset failed: %+v %v", snap, err)
				}
				var messageCount int64
				f.db.Model(&models.Message{}).Count(&messageCount)
				if messageCount != 3 {
					t.Fatalf("reset removed messages: %d", messageCount)
				}
				var previous models.PMSOperation
				f.db.First(&previous, op.ID)
				if previous.Status != "superseded" {
					t.Fatalf("old preview not invalidated: %+v", previous)
				}
			}
			if _, err := f.svc.Confirm(ctx, f.scope, op.ID); err == nil {
				t.Fatalf("%s should be blocked", mode)
			}
		})
	}
}

func TestPMSSandboxConcurrentRoomAllocation(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	second := f.snap.Orders[0]
	second.ID, second.Version, second.RoomID, second.Number, second.GuestName = 0, 0, 0, "SECOND", "演示住客2"
	var err error
	f.snap, err = f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, Order: &second})
	if err != nil {
		t.Fatal(err)
	}
	secondOrderID := f.snap.Orders[len(f.snap.Orders)-1].ID
	f.snap, err = f.svc.Bind(ctx, 1, 9, sandbox.BindingRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, CustomerID: 2, OrderID: secondOrderID, MemberID: f.snap.Members[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	scope2 := sandbox.Scope{StoreID: 1, ConversationID: 2, CustomerID: 2, SourceMessageID: f.message(2, enums.IMSenderTypeCustomer, "帮我升房")}
	first := f.prepare(sandbox.ChangeRequest{Upgrade: true})
	other, err := f.svc.Prepare(ctx, scope2, sandbox.ChangeRequest{Upgrade: true})
	if err != nil || first.Plan.After.RoomID != other.Plan.After.RoomID {
		t.Fatalf("need conflicting plans: %+v %v", other, err)
	}
	f.deliver(f.scope, first, true)
	f.deliver(scope2, other, true)
	f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "确认办理")
	scope2.SourceMessageID = f.message(2, enums.IMSenderTypeCustomer, "确认办理")
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		scope sandbox.Scope
		id    int64
	}{{f.scope, first.ID}, {scope2, other.ID}} {
		wg.Add(1)
		go func(scope sandbox.Scope, id int64) {
			defer wg.Done()
			_, err := f.svc.Confirm(ctx, scope, id)
			results <- err
		}(item.scope, item.id)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("exactly one room claimant must succeed: %d", success)
	}
	var count int64
	f.db.Model(&models.PMSSandboxOrder{}).Where("room_id = ?", first.Plan.After.RoomID).Count(&count)
	if count != 1 {
		t.Fatalf("room assigned to %d orders", count)
	}
}

func TestPMSSandboxNoInventoryNoRightsAndStaleAdministration(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	old := f.snap
	grade := f.snap.Grades[0]
	grade.FreeUpgradeMaxRank = 0
	var err error
	f.snap, err = f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: old.Dataset.ID, Version: old.Dataset.Version, Grade: &grade})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: old.Dataset.ID, Version: old.Dataset.Version, Grade: &grade}); err == nil {
		t.Fatal("stale admin mutation should fail")
	}
	op := f.prepare(sandbox.ChangeRequest{Upgrade: true})
	if op.Plan.AddedCents <= 0 {
		t.Fatal("no free membership right must produce actual test price")
	}
	room := f.snap.Rooms[2]
	room.CleanStatus = "dirty"
	f.snap, err = f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, Room: &room})
	if err != nil {
		t.Fatal(err)
	}
	f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "我要新的升房方案")
	if _, err := f.svc.Prepare(ctx, f.scope, sandbox.ChangeRequest{Upgrade: true}); err == nil {
		t.Fatal("dirty inventory must not be assigned")
	}
	if _, err := f.svc.Reset(ctx, 1, 9, old.Dataset.ID, old.Dataset.Version); err == nil {
		t.Fatal("stale reset should be rejected")
	}
}

func TestPMSSandboxManualOrderConflictAndCrossDataset(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	copyOrder := f.snap.Orders[0]
	copyOrder.ID, copyOrder.Number, copyOrder.Version = 0, "OVERLAP", 0
	if _, err := f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, Order: &copyOrder}); err == nil {
		t.Fatal("backend editing must enforce room interval conflicts")
	}
	other, err := f.svc.Initialize(ctx, 2, 9, false)
	if err != nil {
		t.Fatal(err)
	}
	room := other.Rooms[0]
	if _, err := f.svc.Save(ctx, 1, 9, sandbox.SaveRequest{DatasetID: f.snap.Dataset.ID, Version: f.snap.Dataset.Version, Room: &room}); err == nil {
		t.Fatal("cross-store object must not update")
	}
	if _, err := f.svc.Bind(ctx, 2, 9, sandbox.BindingRequest{DatasetID: other.Dataset.ID, Version: other.Dataset.Version, CustomerID: 1, OrderID: other.Orders[0].ID}); err == nil {
		t.Fatal("binding customer from another store should fail")
	}
}

func TestPMSSandboxReadbackFailureRollsBackOrderAndAudit(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	op := f.prepare(sandbox.ChangeRequest{Upgrade: true, Recovery: true})
	f.deliver(f.scope, op, true)
	f.scope.SourceMessageID = f.message(1, enums.IMSenderTypeCustomer, "确认办理")
	const callback = "sandbox_test_corrupt_readback"
	if err := f.db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "pms_sandbox_orders" {
			tx.Session(&gorm.Session{NewDB: true}).Exec("UPDATE pms_sandbox_orders SET payable_cents = payable_cents + 1 WHERE id = ?", op.Plan.Before.ID)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.db.Callback().Update().Remove(callback)
	if _, err := f.svc.Confirm(ctx, f.scope, op.ID); err == nil || !strings.Contains(err.Error(), "回查") {
		t.Fatalf("mismatching readback must fail: %v", err)
	}
	var order models.PMSSandboxOrder
	f.db.First(&order, op.Plan.Before.ID)
	if order.Version != op.Plan.Before.Version || order.PayableCents != op.Plan.Before.PayableCents || order.RoomID != op.Plan.Before.RoomID {
		t.Fatalf("failed readback leaked updates: %+v", order)
	}
	var count int64
	f.db.Model(&models.ServiceRecoveryCase{}).Count(&count)
	if count != 0 {
		t.Fatal("failed operation must not leave compensation commitment")
	}
}

func TestPMSSandboxMergeOneSourceAndMissingBindings(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	upgrade := f.prepare(sandbox.ChangeRequest{Upgrade: true})
	combined := f.prepare(sandbox.ChangeRequest{LateCheckout: true, Recovery: true})
	if combined.ID != upgrade.ID || !combined.Plan.Request.Upgrade || !combined.Plan.Request.LateCheckout || !combined.Plan.Request.Recovery {
		t.Fatalf("same source must form one combined plan: %+v", combined)
	}
	unbound := sandbox.Scope{StoreID: 1, ConversationID: 2, CustomerID: 2}
	result, err := f.svc.ExecuteScene(ctx, unbound, sandbox.SceneInput{Scene: "A", Topics: []string{"breakfast"}})
	if err != nil || result.Completed || len(result.NeedsInput) == 0 || !strings.Contains(result.Reply, "订单号") {
		t.Fatalf("unbound query must ask required identity: %+v %v", result, err)
	}
	latest, err := f.svc.Latest(ctx, f.scope)
	if err != nil || latest.ID != combined.ID {
		t.Fatalf("latest result scope failed: %+v %v", latest, err)
	}
}

func TestPMSSandboxRequiresExplicitTestEnvironment(t *testing.T) {
	f := newSandboxFixture(t)
	cfg := config.Current()
	cfg.PMS.Environment = "production"
	config.SetCurrent(&cfg)
	if _, err := f.svc.Query(context.Background(), f.scope); err == nil {
		t.Fatal("production must not use sandbox")
	}
	if _, err := f.svc.Initialize(context.Background(), 1, 9, false); err == nil {
		t.Fatal("production must not seed sandbox")
	}
}

func TestPMSSandboxPrepareRequiresCustomerSourceButDashboardMayCancel(t *testing.T) {
	f := newSandboxFixture(t)
	ctx := context.Background()
	zeroSource := f.scope
	zeroSource.SourceMessageID = 0
	if _, err := f.svc.Prepare(ctx, zeroSource, sandbox.ChangeRequest{Upgrade: true}); err == nil {
		t.Fatal("a draft must not be created without an actual customer message")
	}
	aiSource := f.scope
	aiSource.SourceMessageID = f.message(1, enums.IMSenderTypeAI, "请确认办理")
	if _, err := f.svc.Prepare(ctx, aiSource, sandbox.ChangeRequest{Upgrade: true}); err == nil {
		t.Fatal("an AI message must not authorize a draft")
	}
	otherSource := f.scope
	otherSource.SourceMessageID = f.message(2, enums.IMSenderTypeCustomer, "帮我升房")
	if _, err := f.svc.Prepare(ctx, otherSource, sandbox.ChangeRequest{Upgrade: true}); err == nil {
		t.Fatal("another conversation must not authorize a draft")
	}
	op := f.prepare(sandbox.ChangeRequest{Upgrade: true})
	cancelled, err := f.svc.Cancel(ctx, zeroSource, op.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("permission-checked dashboard cancellation should work: %+v %v", cancelled, err)
	}
	state, err := f.svc.Query(ctx, f.scope)
	if err != nil || state.Order.Version != op.Plan.Before.Version || state.Order.RoomID != op.Plan.Before.RoomID {
		t.Fatalf("cancellation must not modify order: %+v %v", state, err)
	}
}
