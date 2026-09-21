package pms

import (
	"encoding/json"
	"fmt"
	"time"
)

// CustomerQueryData projects a PMS query result at the customer-tool boundary.
// Internal services retain their original query data for subsequent operations.
func CustomerQueryData(action string, data any) (any, error) {
	if isMemberQuery(action) {
		return sanitizeMemberQueryData(action, data)
	}
	if data == nil {
		return nil, nil
	}
	switch action {
	case "reserve_order_detail", "reserve_order_by_phone", "recept_order_detail", "recept_order_by_phone":
		return customerOrderData(data)
	case "room_status":
		source, err := customerQueryObject(data)
		if err != nil {
			return nil, err
		}
		result := customerQueryFields(source, "date")
		if err := customerQueryArrayField(result, source, "list", customerRoomGroupData); err != nil {
			return nil, err
		}
		return result, nil
	case "inventory":
		return customerQueryArray(data, customerInventoryData)
	case "renew_candidates":
		source, err := customerQueryObject(data)
		if err != nil {
			return nil, err
		}
		result := customerQueryFields(source, "total")
		if err := customerQueryArrayField(result, source, "rows", customerRenewCandidateData); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("PMS 查询类型没有客户侧数据契约")
	}
}

func customerOrderData(data any) (any, error) {
	source, err := customerQueryObject(data)
	if err != nil {
		return nil, err
	}
	result := customerQueryFields(source,
		"reserveOrderId", "receptOrderId", "customerNo", "hotelName", "channelOrderNumber",
		"reserveStatus", "orderStatus", "orderType", "bookingTime", "createTime",
		"checkInTime", "checkOutTime", "checkInBusinessDate", "checkOutBusinessDate",
		"reserveDays", "numberNights", "stayNightCount", "checkInDays", "homeTotalNum",
		"roomId", "roomName", "homeId", "homeName", "payType", "payAmount",
		"roomFee", "originalRoomFee", "payableAmount", "receivedAmount", "waitPayAmount",
		"sellingAmounts", "originalSellingAmounts",
	)
	if err := customerQueryArrayField(result, source, "reserveProductList", customerOrderProductData); err != nil {
		return nil, err
	}
	if err := customerQueryArrayField(result, source, "receptOrderList", customerOrderData); err != nil {
		return nil, err
	}
	return result, nil
}

func customerOrderProductData(data any) (any, error) {
	source, err := customerQueryObject(data)
	if err != nil {
		return nil, err
	}
	result := customerQueryFields(source, "roomId", "roomName", "reserveHomeCount", "homePrice")
	if err := customerQueryArrayField(result, source, "productDetailPriceList", func(value any) (any, error) {
		item, err := customerQueryObject(value)
		if err != nil {
			return nil, err
		}
		return customerQueryFields(item,
			"productDetailId", "roomId", "roomName", "date", "reserveHomeCount",
			"consumeAmount", "originalAmount", "consumeAmountType", "currency",
		), nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func customerRoomGroupData(data any) (any, error) {
	source, err := customerQueryObject(data)
	if err != nil {
		return nil, err
	}
	result := customerQueryFields(source, "roomId", "roomName", "homeCount")
	if err := customerQueryArrayField(result, source, "homeCardList", func(value any) (any, error) {
		item, err := customerQueryObject(value)
		if err != nil {
			return nil, err
		}
		return customerQueryFields(item,
			"homeId", "homeName", "roomId", "roomName", "buildName", "floorName",
			"homeStatusId", "homeStatus", "homeStatusName", "controlStatus",
		), nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func customerInventoryData(data any) (any, error) {
	source, err := customerQueryObject(data)
	if err != nil {
		return nil, err
	}
	result := customerQueryFields(source,
		"productId", "productName", "roomTypeId", "roomTypeName", "roomId", "price",
		"currency", "consumeAmountType", "roomCount", "rowType",
	)
	if value, exists := source["bookings"]; exists {
		if value == nil {
			result["bookings"] = nil
			return result, nil
		}
		bookings, err := customerQueryObject(value)
		if err != nil {
			return nil, err
		}
		dates := make(map[string]any, len(bookings))
		for date, value := range bookings {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				continue
			}
			values, err := customerQueryObject(value)
			if err != nil {
				return nil, err
			}
			dates[date] = customerQueryFields(values,
				"sold", "available", "occupied", "maintenance", "oversold",
				"price", "currency", "consumeAmountType",
			)
		}
		result["bookings"] = dates
	}
	return result, nil
}

func customerRenewCandidateData(data any) (any, error) {
	source, err := customerQueryObject(data)
	if err != nil {
		return nil, err
	}
	result := customerQueryFields(source,
		"reserveOrderId", "receptOrderId", "reserveOrderNo", "homeId", "homeName",
		"roomId", "roomName", "checkInTime", "checkOutTime", "checkInBusinessDate",
		"reserveStatus", "receptOrderStatus", "orderType", "sameRoomType",
		"targetArrangeStatus", "needHomeConfirm",
	)
	if value, exists := source["allowedHomeHandleTypes"]; exists {
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("PMS 续住房间处理方式结构不正确")
		}
		handles := make([]any, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("PMS 续住房间处理方式结构不正确")
			}
			handles = append(handles, text)
		}
		result["allowedHomeHandleTypes"] = handles
	}
	return result, nil
}

func customerQueryObject(value any) (map[string]any, error) {
	source, ok := value.(map[string]any)
	if !ok || source == nil {
		return nil, fmt.Errorf("PMS 查询数据结构不正确")
	}
	return source, nil
}

func customerQueryFields(source map[string]any, fields ...string) map[string]any {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		value, exists := source[field]
		if !exists {
			continue
		}
		switch value.(type) {
		case nil, string, bool, json.Number:
			result[field] = value
		}
	}
	return result
}

func customerQueryArrayField(result, source map[string]any, field string, project func(any) (any, error)) error {
	value, exists := source[field]
	if !exists {
		return nil
	}
	items, err := customerQueryArray(value, project)
	if err != nil {
		return err
	}
	result[field] = items
	return nil
}

func customerQueryArray(data any, project func(any) (any, error)) (any, error) {
	if data == nil {
		return nil, nil
	}
	source, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("PMS 查询列表结构不正确")
	}
	result := make([]any, 0, len(source))
	for _, value := range source {
		item, err := project(value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}
