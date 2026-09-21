package pms

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

const (
	PriceDifferenceExact        = "exact"
	PriceDifferenceQuoteOnly    = "quote_only"
	PriceDifferenceInsufficient = "insufficient_data"
	PriceDifferenceUnavailable  = "unavailable"

	PriceAvailabilityAvailable   = "available"
	PriceAvailabilityUnavailable = "unavailable"
	PriceAvailabilityUnknown     = "unknown"
)

// PriceDifferenceAssessment is a conservative, read-only comparison between
// the current order's daily room charges and a target room type's inventory
// prices. It never treats a missing price as zero or a missing availability
// value as available.
type PriceDifferenceAssessment struct {
	Status             string            `json:"status"`
	Availability       string            `json:"availability"`
	Reason             string            `json:"reason,omitempty"`
	Missing            []string          `json:"missing,omitempty"`
	RoomTypeID         string            `json:"roomTypeId"`
	StartDate          string            `json:"startDate,omitempty"`
	EndDate            string            `json:"endDate,omitempty"`
	Dates              []string          `json:"dates,omitempty"`
	Currency           string            `json:"currency,omitempty"`
	CurrentDailyAmount map[string]string `json:"currentDailyAmount,omitempty"`
	TargetDailyAmount  map[string]string `json:"targetDailyAmount,omitempty"`
	CurrentTotal       string            `json:"currentTotal,omitempty"`
	TargetTotal        string            `json:"targetTotal,omitempty"`
	Difference         string            `json:"difference,omitempty"`
	DifferenceType     string            `json:"differenceType,omitempty"`
}

type moneyValue struct {
	value    *big.Rat
	currency string
	basis    string
	scale    int
}

// AssessPriceDifference compares already-projected customer-facing PMS
// results. Exact output requires one current and one target amount for every
// night, the same currency, and the same explicit pricing basis.
func AssessPriceDifference(orderData, inventoryData any, targetRoomTypeID, startDate, endDate string) (PriceDifferenceAssessment, error) {
	targetRoomTypeID = strings.TrimSpace(targetRoomTypeID)
	if targetRoomTypeID == "" {
		return PriceDifferenceAssessment{
			Status:       PriceDifferenceInsufficient,
			Availability: PriceAvailabilityUnknown,
			Reason:       "目标房型 ID 缺失，不能猜测房型",
			Missing:      []string{"roomTypeId"},
		}, nil
	}
	dates, normalizedStart, normalizedEnd, err := stayDates(startDate, endDate)
	if err != nil {
		return PriceDifferenceAssessment{
			Status:       PriceDifferenceInsufficient,
			Availability: PriceAvailabilityUnknown,
			RoomTypeID:   targetRoomTypeID,
			Reason:       err.Error(),
			Missing:      []string{"valid date range"},
		}, nil
	}

	current, currentMissing, err := extractCurrentDailyAmounts(orderData, dates)
	if err != nil {
		return PriceDifferenceAssessment{}, err
	}
	target, targetAvailability, targetMissing, err := extractTargetDailyAmounts(inventoryData, targetRoomTypeID, dates)
	if err != nil {
		return PriceDifferenceAssessment{}, err
	}

	assessment := PriceDifferenceAssessment{
		Status:             PriceDifferenceInsufficient,
		Availability:       targetAvailability,
		RoomTypeID:         targetRoomTypeID,
		StartDate:          normalizedStart,
		EndDate:            normalizedEnd,
		Dates:              dates,
		CurrentDailyAmount: current.amounts,
		TargetDailyAmount:  target.amounts,
	}
	assessment.Missing = uniqueStrings(append(currentMissing, targetMissing...))
	if targetAvailability == PriceAvailabilityUnavailable {
		assessment.Status = PriceDifferenceUnavailable
		assessment.Reason = "目标房型在至少一个入住日不可售"
		return assessment, nil
	}

	if len(assessment.Missing) > 0 {
		assessment.Status = PriceDifferenceQuoteOnly
		assessment.Reason = "当前返回的每日价格或计价口径不完整，不能确认最终差价"
		return assessment, nil
	}
	if current.currency == "" || target.currency == "" {
		assessment.Status = PriceDifferenceQuoteOnly
		assessment.Reason = "订单或目标房型缺少币种"
		assessment.Missing = uniqueStrings(append(assessment.Missing, "currency"))
		return assessment, nil
	}
	if current.currency != target.currency {
		assessment.Status = PriceDifferenceQuoteOnly
		assessment.Reason = "订单与目标房型币种不一致"
		assessment.Missing = uniqueStrings(append(assessment.Missing, "same currency"))
		return assessment, nil
	}
	if current.basis == "" || target.basis == "" || current.basis != target.basis {
		assessment.Status = PriceDifferenceQuoteOnly
		assessment.Reason = "订单与目标房型缺少相同的计价口径"
		assessment.Missing = uniqueStrings(append(assessment.Missing, "same pricing basis"))
		return assessment, nil
	}

	currentTotal := new(big.Rat)
	targetTotal := new(big.Rat)
	for _, date := range dates {
		currentTotal.Add(currentTotal, current.byDate[date].value)
		targetTotal.Add(targetTotal, target.byDate[date].value)
	}
	assessment.Status = PriceDifferenceExact
	assessment.Currency = current.currency
	assessment.CurrentTotal = formatMoney(currentTotal, max(current.scale, target.scale))
	assessment.TargetTotal = formatMoney(targetTotal, max(current.scale, target.scale))
	difference := new(big.Rat).Sub(targetTotal, currentTotal)
	assessment.Difference = formatMoney(difference, max(current.scale, target.scale))
	switch difference.Sign() {
	case 1:
		assessment.DifferenceType = "additional_charge"
	case -1:
		assessment.DifferenceType = "reduction"
	default:
		assessment.DifferenceType = "no_difference"
	}
	return assessment, nil
}

type dailyAmounts struct {
	byDate   map[string]moneyValue
	amounts  map[string]string
	currency string
	basis    string
	scale    int
}

func extractCurrentDailyAmounts(orderData any, dates []string) (dailyAmounts, []string, error) {
	result := dailyAmounts{byDate: make(map[string]moneyValue), amounts: make(map[string]string)}
	missing := make([]string, 0)
	source, ok := orderData.(map[string]any)
	if !ok || source == nil {
		return result, []string{"order daily price"}, nil
	}

	var collect func(any)
	collect = func(value any) {
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		if prices, exists := object["productDetailPriceList"]; exists {
			items, ok := prices.([]any)
			if !ok {
				return
			}
			for _, item := range items {
				price, ok := item.(map[string]any)
				if !ok {
					continue
				}
				date := normalizeDate(textValue(price["date"]))
				if date == "" {
					continue
				}
				amount, parsed := parseMoney(price["consumeAmount"])
				if !parsed {
					continue
				}
				currency := strings.TrimSpace(textValue(price["currency"]))
				basis := strings.TrimSpace(textValue(price["consumeAmountType"]))
				current := moneyValue{value: amount, currency: currency, basis: basis, scale: moneyScale(price["consumeAmount"])}
				if existing, exists := result.byDate[date]; exists {
					existing.value.Add(existing.value, current.value)
					if existing.currency == "" {
						existing.currency = current.currency
					}
					if existing.basis == "" {
						existing.basis = current.basis
					}
					existing.scale = max(existing.scale, current.scale)
					result.byDate[date] = existing
				} else {
					result.byDate[date] = current
				}
			}
		}
		for _, key := range []string{"reserveProductList", "receptOrderList"} {
			if items, ok := object[key].([]any); ok {
				for _, item := range items {
					collect(item)
				}
			}
		}
	}
	collect(source)

	for _, date := range dates {
		value, ok := result.byDate[date]
		if !ok {
			missing = append(missing, "current price "+date)
			continue
		}
		result.amounts[date] = formatMoney(value.value, value.scale)
		if result.currency == "" {
			result.currency = value.currency
		} else if result.currency != value.currency {
			missing = append(missing, "same current currency")
		}
		if result.basis == "" {
			result.basis = value.basis
		} else if result.basis != value.basis {
			missing = append(missing, "same current pricing basis")
		}
		result.scale = max(result.scale, value.scale)
	}
	return result, uniqueStrings(missing), nil
}

func extractTargetDailyAmounts(inventoryData any, targetRoomTypeID string, dates []string) (dailyAmounts, string, []string, error) {
	result := dailyAmounts{byDate: make(map[string]moneyValue), amounts: make(map[string]string)}
	source, ok := inventoryData.([]any)
	if !ok {
		return result, PriceAvailabilityUnknown, []string{"target inventory"}, nil
	}
	var row map[string]any
	for _, value := range source {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"roomTypeId", "productId"} {
			if textValue(item[key]) == targetRoomTypeID {
				row = item
				break
			}
		}
		if row != nil {
			break
		}
	}
	if row == nil {
		return result, PriceAvailabilityUnknown, []string{"target room type"}, nil
	}

	availability := PriceAvailabilityUnknown
	if bookings, ok := row["bookings"].(map[string]any); ok {
		availability = PriceAvailabilityAvailable
		for _, date := range dates {
			day, ok := bookings[date].(map[string]any)
			if !ok {
				return result, availability, []string{"target inventory " + date}, nil
			}
			if available, exists := day["available"]; exists {
				if count, ok := parseMoney(available); ok && count.Sign() <= 0 {
					availability = PriceAvailabilityUnavailable
				}
			} else {
				availability = PriceAvailabilityUnknown
			}
			if value, ok := moneyValueFromMap(day); ok {
				result.byDate[date] = value
			}
		}
	} else if len(dates) == 1 {
		// The documented inventory response exposes a single current `price`.
		// It can only be applied to a one-night query; never spread it across
		// an arbitrary multi-night range.
		if value, ok := moneyValueFromMap(row); ok {
			result.byDate[dates[0]] = value
		}
	}

	missing := make([]string, 0)
	for _, date := range dates {
		value, ok := result.byDate[date]
		if !ok {
			missing = append(missing, "target price "+date)
			continue
		}
		result.amounts[date] = formatMoney(value.value, value.scale)
		if result.currency == "" {
			result.currency = value.currency
		} else if result.currency != value.currency {
			missing = append(missing, "same target currency")
		}
		if result.basis == "" {
			result.basis = value.basis
		} else if result.basis != value.basis {
			missing = append(missing, "same target pricing basis")
		}
		result.scale = max(result.scale, value.scale)
	}
	return result, availability, uniqueStrings(missing), nil
}

func moneyValueFromMap(source map[string]any) (moneyValue, bool) {
	amount, ok := parseMoney(source["price"])
	if !ok {
		return moneyValue{}, false
	}
	return moneyValue{
		value:    amount,
		currency: strings.TrimSpace(textValue(source["currency"])),
		basis:    strings.TrimSpace(textValue(source["consumeAmountType"])),
		scale:    moneyScale(source["price"]),
	}, true
}

func stayDates(startDate, endDate string) ([]string, string, string, error) {
	start := normalizeDate(startDate)
	end := normalizeDate(endDate)
	if start == "" || end == "" {
		return nil, start, end, fmt.Errorf("入住和离店日期不完整，不能确认差价")
	}
	startTime, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, start, end, fmt.Errorf("入住日期格式不正确")
	}
	endTime, err := time.Parse("2006-01-02", end)
	if err != nil || !endTime.After(startTime) {
		return nil, start, end, fmt.Errorf("离店日期必须晚于入住日期")
	}
	dates := make([]string, 0, int(endTime.Sub(startTime)/(24*time.Hour)))
	for current := startTime; current.Before(endTime); current = current.AddDate(0, 0, 1) {
		dates = append(dates, current.Format("2006-01-02"))
	}
	return dates, start, end, nil
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		value = value[:10]
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.Format("2006-01-02")
	}
	return ""
}

func parseMoney(value any) (*big.Rat, bool) {
	text := strings.TrimSpace(textValue(value))
	if text == "" || strings.EqualFold(text, "null") {
		return nil, false
	}
	ratio := new(big.Rat)
	if _, ok := ratio.SetString(text); !ok {
		return nil, false
	}
	return ratio, true
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%v", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	default:
		return ""
	}
}

func moneyScale(value any) int {
	text := strings.TrimSpace(textValue(value))
	if index := strings.IndexByte(text, '.'); index >= 0 {
		return len(text) - index - 1
	}
	return 0
}

func formatMoney(value *big.Rat, scale int) string {
	if value == nil {
		return ""
	}
	if scale < 2 {
		scale = 2
	}
	return value.FloatString(scale)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
