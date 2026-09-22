package executor

import (
	"context"
	"sync"
	"testing"
	"time"
)

type runtimePMSBarrierInvoker struct {
	mu      sync.Mutex
	block   map[string]bool
	started chan string
	release <-chan struct{}
	results map[string]pmsReadStepResult
}

func (i *runtimePMSBarrierInvoker) Invoke(ctx context.Context, action string, _ map[string]string) pmsReadStepResult {
	if i.block[action] {
		i.started <- action
		select {
		case <-ctx.Done():
			return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "cancelled"}
		case <-i.release:
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if result, ok := i.results[action]; ok {
		return result
	}
	return pmsReadStepResult{Status: pmsReadStepUnavailable, Message: "missing fake result"}
}

func TestExecuteRuntimePMSReadPlanRunsIndependentOrderLookupsConcurrently(t *testing.T) {
	release := make(chan struct{})
	invoker := &runtimePMSBarrierInvoker{
		block:   map[string]bool{"reserve_order_by_phone": true, "recept_order_by_phone": true},
		started: make(chan string, 2),
		release: release,
		results: map[string]pmsReadStepResult{
			"reserve_order_by_phone": {Status: pmsReadStepOK, Data: map[string]any{"reserveOrderId": "RES-1"}},
			"recept_order_by_phone":  {Status: pmsReadStepOK, Data: map[string]any{"receptOrderId": "REC-1"}},
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		input := pmsReadPlanInput{Scenario: pmsReadScenarioOrder, Phone: "13800138000"}
		_, _ = executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
	}()

	waitForRuntimePMSConcurrentStarts(t, invoker.started, "reserve_order_by_phone", "recept_order_by_phone")
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent order lookup plan did not finish")
	}
}

func TestExecuteRuntimePMSReadPlanRunsResolvedInventoryAndMemberConcurrently(t *testing.T) {
	release := make(chan struct{})
	invoker := &runtimePMSBarrierInvoker{
		block:   map[string]bool{"inventory": true, "member_benefits_by_phone": true},
		started: make(chan string, 2),
		release: release,
		results: map[string]pmsReadStepResult{
			"recept_order_detail": {Status: pmsReadStepOK, Data: map[string]any{
				"receptOrderId": "REC-1", "checkInBusinessDate": "2026-09-22", "checkOutBusinessDate": "2026-09-24",
			}},
			"inventory": {Status: pmsReadStepOK, Data: []any{map[string]any{
				"roomTypeId": "ROOM-2", "roomTypeName": "豪华大床房", "bookings": map[string]any{
					"2026-09-22": map[string]any{"available": "2"},
					"2026-09-23": map[string]any{"available": "1"},
				},
			}}},
			"member_benefits_by_phone": {Status: pmsReadStepOK, Data: map[string]any{"member": map[string]any{"gradeName": "金卡"}}},
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		input := pmsReadPlanInput{
			Scenario: pmsReadScenarioRoomUpgrade, Phone: "13800138000", ReceptOrderID: "REC-1",
			TargetRoomTypeID: "ROOM-2", StartDate: "2026-09-22", EndDate: "2026-09-24",
		}
		_, _ = executeRuntimePMSReadPlan(context.Background(), buildPMSReadPlan(input), input, invoker)
	}()

	waitForRuntimePMSConcurrentStarts(t, invoker.started, "inventory", "member_benefits_by_phone")
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent inventory/member plan did not finish")
	}
}

func waitForRuntimePMSConcurrentStarts(t *testing.T, started <-chan string, expected ...string) {
	t.Helper()
	want := make(map[string]struct{}, len(expected))
	for _, action := range expected {
		want[action] = struct{}{}
	}
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for len(want) > 0 {
		select {
		case action := <-started:
			delete(want, action)
		case <-timer.C:
			t.Fatalf("independent PMS reads did not start concurrently; still waiting for %v", want)
		}
	}
}
