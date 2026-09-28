package pms

import (
	"encoding/json"
	"testing"
)

func TestQueryBusinessFailureKeepsSafeReason(t *testing.T) {
	for _, tc := range []struct{ code, kind string }{
		{"1010005013", "empty"}, {"1010005035", "empty"},
		{"403", "denied"}, {"401", "denied"}, {"1010005016", "invalid_state"},
	} {
		failure := QueryErrorDetails(safeQueryBusinessError(map[string]any{
			"code": json.Number(tc.code), "msg": "raw private phone token",
		}))
		if failure.Kind != tc.kind || failure.Code != tc.code || failure.Message == "raw private phone token" {
			t.Fatalf("%s: %#v", tc.code, failure)
		}
	}
}

func TestStayRoomAvailabilityRejectsEndedIntervalsAfterClipping(t *testing.T) {
	for _, end := range []string{"2026-09-25", "2026-09-28"} {
		_, err := AssessStayRoomAvailability(map[string]any{
			"date": "2026-09-28", "list": []any{},
		}, StayRoomAvailabilityRequest{StartDate: "2026-09-23", EndDate: end})
		if err == nil || QueryErrorDetails(err).Kind != "invalid_state" {
			t.Fatalf("ended interval became available: %s %v", end, err)
		}
	}
}

func TestPriceOrderDataSelectsOnlyCurrentRoomProduct(t *testing.T) {
	order := map[string]any{"reserveOrderId": "reserve", "receptOrderId": "recept", "roomName": "儿童房"}
	for _, roomID := range []string{"", "child"} {
		order["roomId"] = roomID
		reserve := map[string]any{
			"reserveOrderId": "reserve",
			"reserveProductList": []any{
				map[string]any{"roomId": "child", "roomName": "儿童房", "reserveHomeCount": json.Number("1")},
				map[string]any{"roomId": "other", "roomName": "其他房", "reserveHomeCount": json.Number("2")},
			},
		}
		got, err := PriceOrderData(order, reserve)
		if err != nil {
			t.Fatal(err)
		}
		products := got.(map[string]any)["reserveProductList"].([]any)
		if len(products) != 1 || products[0].(map[string]any)["roomId"] != "child" {
			t.Fatalf("unrelated room price included: %#v", got)
		}
	}
}

func TestPriceOrderDataRejectsUnrelatedReservationAndMultiRoomAmounts(t *testing.T) {
	order := map[string]any{"reserveOrderId": "a", "roomId": "child"}
	for _, reserve := range []any{
		map[string]any{"reserveOrderId": "b"},
		map[string]any{"reserveOrderId": "a", "reserveProductList": []any{
			map[string]any{"roomId": "child", "reserveHomeCount": json.Number("2")},
		}},
	} {
		if _, err := PriceOrderData(order, reserve); err == nil {
			t.Fatal("unrelated or multiple-room price accepted")
		}
	}
}
