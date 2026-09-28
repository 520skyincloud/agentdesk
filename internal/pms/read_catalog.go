package pms

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SearchOrders uses the same paged GET searches as HPMS. Keyword search can
// match other fields, so enforce exact phone ownership before projecting data.
func (c *Client) SearchOrders(ctx context.Context, phone string) (QueryResult, error) {
	if !mainlandMemberPhone.MatchString(phone) {
		return QueryResult{}, fmt.Errorf("订单搜索需要客户有效手机号")
	}
	rows := make([]any, 0)
	receptions := map[string]bool{}
	seen := map[string]bool{}
	result := QueryResult{Action: "orders_by_phone"}
	for _, action := range []string{"recept_order_search", "reserve_order_search"} {
		paged, err := c.readAllPages(ctx, action, map[string]string{"phone": phone})
		if err != nil {
			return QueryResult{}, err
		}
		result.Source, result.AsOf = paged.Source, paged.AsOf
		for _, value := range paged.Data.([]any) {
			row, ok := value.(map[string]any)
			if !ok {
				return QueryResult{}, fmt.Errorf("PMS 订单搜索返回格式不正确")
			}
			if firstString(row, "hotelId") != c.hotelID || !orderMatchesPhone(row, phone) {
				continue
			}
			reserveID, receptID := firstString(row, "reserveOrderId"), firstString(row, "receptOrderId")
			if action == "reserve_order_search" && receptions[reserveID] {
				continue
			}
			key := "reserve:" + reserveID
			if action == "recept_order_search" {
				if receptID == "" {
					return QueryResult{}, fmt.Errorf("PMS 搜索结果缺少接待单标识")
				}
				key = "recept:" + receptID
				receptions[reserveID] = true
			} else if reserveID == "" {
				return QueryResult{}, fmt.Errorf("PMS 搜索结果缺少预订单标识")
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			projected, err := customerOrderData(row)
			if err != nil {
				return QueryResult{}, err
			}
			rows = append(rows, projected)
		}
	}
	result.Data = map[string]any{"rows": rows, "total": json.Number(strconv.Itoa(len(rows))), "complete": true}
	return result, nil
}

func orderMatchesPhone(row map[string]any, phone string) bool {
	if firstString(row, "reservePhone") == phone {
		return true
	}
	if phones, ok := row["reservePhoneList"].([]any); ok {
		for _, value := range phones {
			if value == phone {
				return true
			}
		}
	}
	if guests, ok := row["receptCustomerList"].([]any); ok {
		for _, value := range guests {
			if guest, ok := value.(map[string]any); ok && firstString(guest, "phone") == phone {
				return true
			}
		}
	}
	return false
}

func (c *Client) readAllPages(ctx context.Context, action string, args map[string]string) (QueryResult, error) {
	const pageSize = 100
	var rows []any
	var result QueryResult
	for page := 1; page <= 10; page++ {
		query := make(map[string]string, len(args)+2)
		for key, value := range args {
			query[key] = value
		}
		query["pageNum"], query["pageSize"] = strconv.Itoa(page), strconv.Itoa(pageSize)
		var err error
		result, err = c.Query(ctx, action, query)
		if err != nil {
			return QueryResult{}, err
		}
		data, ok := result.Data.(map[string]any)
		if !ok {
			return QueryResult{}, fmt.Errorf("PMS 分页数据格式不正确")
		}
		items, ok := data["rows"].([]any)
		total, err := strconv.Atoi(firstString(data, "total"))
		if !ok || err != nil || total < 0 {
			return QueryResult{}, fmt.Errorf("PMS 分页数据不完整")
		}
		rows = append(rows, items...)
		if len(rows) >= total {
			result.Data = rows
			return result, nil
		}
		if len(items) == 0 {
			return QueryResult{}, fmt.Errorf("PMS 分页未返回完整结果")
		}
	}
	return QueryResult{}, fmt.Errorf("匹配记录较多，请补充查询范围")
}

func (c *Client) MemberProgram(ctx context.Context) (QueryResult, error) {
	catalog, err := c.readAllPages(ctx, "member_grade_catalog", nil)
	if err != nil {
		return QueryResult{}, err
	}
	var grades []any
	for _, value := range catalog.Data.([]any) {
		row, ok := value.(map[string]any)
		if !ok || strings.TrimSpace(firstString(row, "gradeId")) == "" {
			return QueryResult{}, fmt.Errorf("PMS 等级目录缺少真实等级标识")
		}
		if firstString(row, "status") != "0" {
			continue
		}
		grade, err := c.Query(ctx, "member_benefits_by_grade", map[string]string{"gradeId": firstString(row, "gradeId")})
		if err != nil {
			return QueryResult{}, err
		}
		grades = append(grades, grade.Data)
		catalog.AsOf = grade.AsOf
	}
	catalog.Action = "member_program"
	catalog.Data = map[string]any{"grades": grades}
	return catalog, nil
}

// Board prices are dated selling prices, not a booking-specific final quote.
// Merge only the price, preserving independently queried availability and
// leaving unknown currency / pricing basis unknown.
func MergeRoomPrices(inventory, prices any) any {
	items, _ := inventory.([]any)
	priceRows, _ := prices.([]any)
	byID := map[string]map[string]any{}
	for _, value := range priceRows {
		if row, ok := value.(map[string]any); ok {
			byID[firstString(row, "roomId", "roomTypeId", "productId")] = row
		}
	}
	out := make([]any, 0, len(items))
	for _, value := range items {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		copyRow := make(map[string]any, len(row))
		for key, value := range row {
			copyRow[key] = value
		}
		priceRow := byID[firstString(row, "roomId", "roomTypeId", "productId")]
		priceDays, _ := priceRow["bookings"].(map[string]any)
		days, _ := row["bookings"].(map[string]any)
		newDays := map[string]any{}
		for date, value := range days {
			day, _ := value.(map[string]any)
			newDay := map[string]any{}
			for key, value := range day {
				newDay[key] = value
			}
			if price, ok := priceDays[date].(map[string]any); ok {
				if amount, exists := price["price"]; exists {
					newDay["price"] = amount
				}
			}
			newDays[date] = newDay
		}
		copyRow["bookings"] = newDays
		out = append(out, copyRow)
	}
	return out
}
