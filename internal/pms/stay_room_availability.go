package pms

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	StayRoomAvailabilityAvailable   = "available"
	StayRoomAvailabilityUnavailable = "unavailable"
	StayRoomAvailabilityPartial     = "partial"
)

// StayRoomAvailabilityRequest describes a read-only availability assessment.
// It does not reserve, assign, or modify a room in HPMS.
type StayRoomAvailabilityRequest struct {
	RoomTypeID            string
	StartDate             string
	EndDate               string
	ExcludeReserveOrderID string
	ExcludeReceptOrderID  string
}

type StayRoomCandidate struct {
	HomeID         string `json:"homeId"`
	HomeName       string `json:"homeName"`
	RoomTypeID     string `json:"roomTypeId"`
	RoomTypeName   string `json:"roomTypeName"`
	BuildName      string `json:"buildName,omitempty"`
	FloorName      string `json:"floorName,omitempty"`
	HomeStatus     string `json:"homeStatus,omitempty"`
	HomeStatusName string `json:"homeStatusName,omitempty"`
	ControlStatus  string `json:"controlStatus,omitempty"`
	ReadyNow       bool   `json:"readyNow"`
}

type StayRoomAvailabilityResult struct {
	Status           string              `json:"status"`
	StartDate        string              `json:"startDate"`
	EndDate          string              `json:"endDate"`
	SnapshotDate     string              `json:"snapshotDate,omitempty"`
	RoomTypeID       string              `json:"roomTypeId,omitempty"`
	RoomTypeName     string              `json:"roomTypeName,omitempty"`
	CandidateCount   int                 `json:"candidateCount"`
	ReadyNowCount    int                 `json:"readyNowCount"`
	CoverageComplete bool                `json:"coverageComplete"`
	Candidates       []StayRoomCandidate `json:"candidates"`
	Reason           string              `json:"reason,omitempty"`
}

// AssessStayRoomAvailability combines the documented real-time room cards with
// every returned reservation/check-in interval. The HPMS endpoint only exposes
// today's room snapshot and future orders within 30 days, so wider ranges stay
// partial instead of being presented as confirmed availability.
func AssessStayRoomAvailability(data any, request StayRoomAvailabilityRequest) (StayRoomAvailabilityResult, error) {
	startDate, start, err := normalizeStayRoomDate(request.StartDate)
	if err != nil {
		return StayRoomAvailabilityResult{}, fmt.Errorf("入住日期无效")
	}
	endDate, end, err := normalizeStayRoomDate(request.EndDate)
	if err != nil || !end.After(start) {
		return StayRoomAvailabilityResult{}, fmt.Errorf("离店日期无效")
	}
	root, ok := data.(map[string]any)
	if !ok || root == nil {
		return StayRoomAvailabilityResult{}, fmt.Errorf("PMS 实时房态结构不正确")
	}
	groups, ok := root["list"].([]any)
	if !ok {
		return StayRoomAvailabilityResult{}, fmt.Errorf("PMS 实时房态缺少房间列表")
	}

	result := StayRoomAvailabilityResult{
		StartDate:        startDate,
		EndDate:          endDate,
		RoomTypeID:       strings.TrimSpace(request.RoomTypeID),
		CoverageComplete: true,
		Candidates:       make([]StayRoomCandidate, 0),
	}
	snapshotText := firstString(root, "date")
	if snapshotText != "" {
		if normalized, snapshot, snapshotErr := normalizeStayRoomDate(snapshotText); snapshotErr == nil {
			result.SnapshotDate = normalized
			if start.Before(snapshot) {
				start = snapshot
			}
			if end.After(snapshot.AddDate(0, 0, 30)) {
				result.CoverageComplete = false
				result.Reason = "实时房态只确认未来30天内的具体房间占用"
			}
		} else {
			result.CoverageComplete = false
			result.Reason = "实时房态未返回有效查询日期"
		}
	} else {
		result.CoverageComplete = false
		result.Reason = "实时房态未返回查询日期"
	}

	targetFound := result.RoomTypeID == ""
	for _, groupValue := range groups {
		group, ok := groupValue.(map[string]any)
		if !ok {
			continue
		}
		roomTypeID := firstString(group, "roomId", "roomTypeId", "productId")
		roomTypeName := firstString(group, "roomName", "roomTypeName", "productName")
		if result.RoomTypeID != "" && roomTypeID != result.RoomTypeID {
			continue
		}
		targetFound = true
		if result.RoomTypeName == "" {
			result.RoomTypeName = roomTypeName
		}
		cards, _ := group["homeCardList"].([]any)
		for _, cardValue := range cards {
			card, ok := cardValue.(map[string]any)
			if !ok || stayRoomIsControlled(card) {
				continue
			}
			available, complete := stayRoomIntervalsAvailable(card, request, start, end)
			if !complete {
				result.CoverageComplete = false
				if result.Reason == "" {
					result.Reason = "部分房间的订单占用时间不完整"
				}
			}
			if !available {
				continue
			}
			candidate := StayRoomCandidate{
				HomeID:         firstString(card, "homeId"),
				HomeName:       firstString(card, "homeName"),
				RoomTypeID:     firstNonEmptyStayRoomText(firstString(card, "roomId", "roomTypeId"), roomTypeID),
				RoomTypeName:   firstNonEmptyStayRoomText(firstString(card, "roomName", "roomTypeName"), roomTypeName),
				BuildName:      firstString(card, "buildName"),
				FloorName:      firstString(card, "floorName"),
				HomeStatus:     firstString(card, "homeStatus"),
				HomeStatusName: firstString(card, "homeStatusName"),
				ControlStatus:  firstString(card, "controlStatus"),
			}
			candidate.ReadyNow = candidate.HomeStatus == "0017002" &&
				(candidate.ControlStatus == "" || candidate.ControlStatus == "0074001")
			if candidate.ReadyNow {
				result.ReadyNowCount++
			}
			result.Candidates = append(result.Candidates, candidate)
		}
	}

	sort.SliceStable(result.Candidates, func(i, j int) bool {
		if result.Candidates[i].RoomTypeName != result.Candidates[j].RoomTypeName {
			return result.Candidates[i].RoomTypeName < result.Candidates[j].RoomTypeName
		}
		return result.Candidates[i].HomeName < result.Candidates[j].HomeName
	})
	result.CandidateCount = len(result.Candidates)
	if !targetFound {
		result.CoverageComplete = true
		result.Status = StayRoomAvailabilityUnavailable
		result.Reason = "实时房态未返回目标房型"
		return result, nil
	}
	if !result.CoverageComplete {
		result.Status = StayRoomAvailabilityPartial
	} else if result.CandidateCount > 0 {
		result.Status = StayRoomAvailabilityAvailable
	} else {
		result.Status = StayRoomAvailabilityUnavailable
		if result.Reason == "" {
			result.Reason = "没有找到整个入住区间均无订单冲突且未锁房、未维修的具体房间"
		}
	}
	return result, nil
}

func stayRoomIsControlled(card map[string]any) bool {
	control := firstString(card, "controlStatus")
	status := firstString(card, "homeStatus")
	return control == "0074002" || control == "0074003" || status == "0074002" || status == "0074003"
}

func stayRoomIntervalsAvailable(card map[string]any, request StayRoomAvailabilityRequest, start, end time.Time) (bool, bool) {
	complete := true
	excludedOwnOccupancy := false
	seen := make(map[string]struct{})
	for _, field := range []string{"reserveOrderInfoList", "checkInOrderInfoList"} {
		items, _ := card[field].([]any)
		for _, itemValue := range items {
			item, ok := itemValue.(map[string]any)
			if !ok {
				return false, false
			}
			reserveOrderID := firstString(item, "reserveOrderId")
			receptOrderID := firstString(item, "receptOrderId")
			if (strings.TrimSpace(request.ExcludeReserveOrderID) != "" && reserveOrderID == strings.TrimSpace(request.ExcludeReserveOrderID)) ||
				(strings.TrimSpace(request.ExcludeReceptOrderID) != "" && receptOrderID == strings.TrimSpace(request.ExcludeReceptOrderID)) {
				excludedOwnOccupancy = true
				continue
			}
			checkInText := firstString(item, "checkInTime", "checkInBusinessDate")
			checkOutText := firstString(item, "checkOutTime", "checkOutBusinessDate")
			key := strings.Join([]string{reserveOrderID, receptOrderID, checkInText, checkOutText}, "|")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			_, checkIn, checkInErr := normalizeStayRoomDate(checkInText)
			_, checkOut, checkOutErr := normalizeStayRoomDate(checkOutText)
			if checkInErr != nil || checkOutErr != nil || !checkOut.After(checkIn) {
				return false, false
			}
			if checkIn.Before(end) && checkOut.After(start) {
				return false, complete
			}
		}
	}
	status := firstString(card, "homeStatus")
	if (status == "0017003" || status == "0017004") && len(seen) == 0 && !excludedOwnOccupancy {
		return false, false
	}
	return true, complete
}

func normalizeStayRoomDate(value string) (string, time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		value = value[:10]
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", time.Time{}, err
	}
	return parsed.Format("2006-01-02"), parsed, nil
}

func firstNonEmptyStayRoomText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
