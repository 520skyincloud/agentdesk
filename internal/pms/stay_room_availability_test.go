package pms

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssessStayRoomAvailabilityUsesEveryReturnedOrderInterval(t *testing.T) {
	data := stayRoomAvailabilityFixture(t, `{
		"date":"2026-09-23",
		"list":[{
			"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
				{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","homeStatusName":"空净","controlStatus":"0074001",
				 "reserveOrderInfoList":[{"reserveOrderId":"OLD","checkInTime":"2026-09-20 14:00:00","checkOutTime":"2026-09-23 12:00:00"}]},
				{"homeId":"H-1502","homeName":"1502","homeStatus":"0017002","homeStatusName":"空净","controlStatus":"0074001",
				 "reserveOrderInfoList":[{"reserveOrderId":"NEXT","checkInTime":"2026-09-24 14:00:00","checkOutTime":"2026-09-26 12:00:00"}]},
				{"homeId":"H-1503","homeName":"1503","homeStatus":"0017002","homeStatusName":"空净","controlStatus":"0074002"},
				{"homeId":"H-1504","homeName":"1504","homeStatus":"0017004","homeStatusName":"住净","controlStatus":"0074001",
				 "checkInOrderInfoList":[{"reserveOrderId":"RES-CURRENT","receptOrderId":"REC-CURRENT","checkInTime":"2026-09-22 14:00:00","checkOutTime":"2026-09-25 12:00:00"}]}
			]
		}]
	}`)

	got, err := AssessStayRoomAvailability(data, StayRoomAvailabilityRequest{
		RoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		ExcludeReserveOrderID: "RES-CURRENT", ExcludeReceptOrderID: "REC-CURRENT",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StayRoomAvailabilityAvailable || got.CandidateCount != 2 || got.ReadyNowCount != 1 || !got.CoverageComplete {
		t.Fatalf("unexpected availability: %#v", got)
	}
	if got.Candidates[0].HomeName != "1501" || got.Candidates[1].HomeName != "1504" {
		t.Fatalf("overlap or room control filtering failed: %#v", got.Candidates)
	}
}

func TestAssessStayRoomAvailabilityDoesNotTreatAggregateOrPartialCoverageAsConfirmed(t *testing.T) {
	t.Run("future range beyond room-status coverage stays partial", func(t *testing.T) {
		data := stayRoomAvailabilityFixture(t, `{
			"date":"2026-09-23",
			"list":[{"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
				{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","controlStatus":"0074001"}
			]}]
		}`)
		got, err := AssessStayRoomAvailability(data, StayRoomAvailabilityRequest{
			RoomTypeID: "ROOM-2", StartDate: "2026-10-22", EndDate: "2026-10-25",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StayRoomAvailabilityPartial || got.CoverageComplete || !strings.Contains(got.Reason, "未来30天") {
			t.Fatalf("future coverage was overstated: %#v", got)
		}
	})

	t.Run("malformed occupancy interval excludes that room", func(t *testing.T) {
		data := stayRoomAvailabilityFixture(t, `{
			"date":"2026-09-23",
			"list":[{"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
				{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","controlStatus":"0074001",
				 "reserveOrderInfoList":[{"reserveOrderId":"UNKNOWN","checkInTime":"","checkOutTime":""}]}
			]}]
		}`)
		got, err := AssessStayRoomAvailability(data, StayRoomAvailabilityRequest{
			RoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StayRoomAvailabilityPartial || got.CandidateCount != 0 || got.CoverageComplete {
			t.Fatalf("unknown occupancy interval became a candidate: %#v", got)
		}
	})
}

func TestAssessStayRoomAvailabilityReportsNoConflictFreeRoom(t *testing.T) {
	data := stayRoomAvailabilityFixture(t, `{
		"date":"2026-09-23",
		"list":[{"roomId":"ROOM-2","roomName":"高级大床房","homeCardList":[
			{"homeId":"H-1501","homeName":"1501","homeStatus":"0017002","controlStatus":"0074001",
			 "reserveOrderInfoList":[{"reserveOrderId":"NEXT","checkInTime":"2026-09-24 14:00:00","checkOutTime":"2026-09-26 12:00:00"}]}
		]}]
	}`)
	got, err := AssessStayRoomAvailability(data, StayRoomAvailabilityRequest{
		RoomTypeID: "ROOM-2", StartDate: "2026-09-23", EndDate: "2026-09-25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StayRoomAvailabilityUnavailable || got.CandidateCount != 0 || !got.CoverageComplete {
		t.Fatalf("overlapping room was reported as available: %#v", got)
	}
}

func stayRoomAvailabilityFixture(t *testing.T, payload string) any {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
