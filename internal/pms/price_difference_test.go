package pms

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssessPriceDifferenceUsesSameDatesAndExactMoney(t *testing.T) {
	order := customerQueryFixture(t, `{
		"checkInTime":"2026-09-21T14:00:00+08:00",
		"checkOutTime":"2026-09-23T12:00:00+08:00",
		"reserveProductList":[{
			"roomName":"标准大床房",
			"productDetailPriceList":[
				{"date":"2026-09-21","consumeAmount":"100.10","consumeAmountType":"ROOM_FEE","currency":"CNY"},
				{"date":"2026-09-22","consumeAmount":"120.20","consumeAmountType":"ROOM_FEE","currency":"CNY"}
			]
		}]
	}`)
	inventory := customerQueryFixture(t, `[{
		"productId":"ROOM-TYPE-2",
		"roomTypeName":"豪华大床房",
		"bookings":{
			"2026-09-21":{"available":2,"price":"130.30","consumeAmountType":"ROOM_FEE","currency":"CNY"},
			"2026-09-22":{"available":1,"price":"140.40","consumeAmountType":"ROOM_FEE","currency":"CNY"}
		}
	}]`)

	assessment, err := AssessPriceDifference(order, inventory, "ROOM-TYPE-2", "2026-09-21", "2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != PriceDifferenceExact || assessment.Availability != PriceAvailabilityAvailable {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
	if assessment.CurrentTotal != "220.30" || assessment.TargetTotal != "270.70" ||
		assessment.Difference != "50.40" || assessment.DifferenceType != "additional_charge" {
		t.Fatalf("money comparison used the wrong basis: %+v", assessment)
	}
}

func TestAssessPriceDifferenceDoesNotSpreadOnePriceAcrossMultipleNights(t *testing.T) {
	order := customerQueryFixture(t, `{
		"reserveProductList":[{
			"roomName":"标准大床房",
			"productDetailPriceList":[
				{"date":"2026-09-21","consumeAmount":"100","consumeAmountType":"ROOM_FEE","currency":"CNY"},
				{"date":"2026-09-22","consumeAmount":"100","consumeAmountType":"ROOM_FEE","currency":"CNY"}
			]
		}]
	}`)
	inventory := customerQueryFixture(t, `[{
		"productId":"ROOM-TYPE-2",
		"price":"130",
		"currency":"CNY",
		"consumeAmountType":"ROOM_FEE",
		"roomCount":3
	}]`)

	assessment, err := AssessPriceDifference(order, inventory, "ROOM-TYPE-2", "2026-09-21", "2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != PriceDifferenceQuoteOnly || len(assessment.Missing) == 0 ||
		!strings.Contains(assessment.Reason, "不完整") {
		t.Fatalf("multi-night root price must not be treated as exact: %+v", assessment)
	}
	if assessment.Difference != "" {
		t.Fatalf("quote-only assessment must not expose a final difference: %+v", assessment)
	}
}

func TestAssessPriceDifferenceDoesNotTreatEmptyPriceAsFree(t *testing.T) {
	order := customerQueryFixture(t, `{
		"reserveProductList":[{
			"roomName":"标准大床房",
			"productDetailPriceList":[
				{"date":"2026-09-21","consumeAmount":"100","consumeAmountType":"ROOM_FEE","currency":"CNY"}
			]
		}]
	}`)
	inventory := customerQueryFixture(t, `[{
		"productId":"ROOM-TYPE-2",
		"price":null,
		"currency":"CNY",
		"consumeAmountType":"ROOM_FEE",
		"bookings":{"2026-09-21":{"available":1}}
	}]`)

	assessment, err := AssessPriceDifference(order, inventory, "ROOM-TYPE-2", "2026-09-21", "2026-09-22")
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != PriceDifferenceQuoteOnly || assessment.Difference != "" {
		t.Fatalf("empty target price must remain unknown: %+v", assessment)
	}
}

func TestAssessPriceDifferenceMarksUnavailableTargetWithoutQuotingIt(t *testing.T) {
	order := customerQueryFixture(t, `{
		"reserveProductList":[{
			"roomName":"标准大床房",
			"productDetailPriceList":[
				{"date":"2026-09-21","consumeAmount":"100","consumeAmountType":"ROOM_FEE","currency":"CNY"}
			]
		}]
	}`)
	inventory := customerQueryFixture(t, `[{
		"productId":"ROOM-TYPE-2",
		"bookings":{"2026-09-21":{"available":0,"price":"130","consumeAmountType":"ROOM_FEE","currency":"CNY"}}
	}]`)

	assessment, err := AssessPriceDifference(order, inventory, "ROOM-TYPE-2", "2026-09-21", "2026-09-22")
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != PriceDifferenceUnavailable || assessment.Availability != PriceAvailabilityUnavailable {
		t.Fatalf("unavailable target must be explicit: %+v", assessment)
	}
	if assessment.Difference != "" {
		t.Fatalf("unavailable target must not be presented as a payable difference: %+v", assessment)
	}
}

func TestCustomerInventoryDataPreservesOptionalPriceFacts(t *testing.T) {
	source := customerQueryFixture(t, `[{
		"productId":"ROOM-TYPE-2",
		"price":"130.00",
		"currency":"CNY",
		"consumeAmountType":"ROOM_FEE",
		"bookings":{"2026-09-21":{"available":1,"price":"130.00","currency":"CNY","consumeAmountType":"ROOM_FEE"}}
	}]`)
	projected, err := CustomerQueryData("inventory", source)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(projected)
	for _, wanted := range []string{`"price":"130.00"`, `"currency":"CNY"`, `"consumeAmountType":"ROOM_FEE"`} {
		if !strings.Contains(string(raw), wanted) {
			t.Fatalf("optional price fact was dropped: %s in %s", wanted, raw)
		}
	}
}
