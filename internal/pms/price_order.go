package pms

import "fmt"

// PriceOrderData selects one stay's price product before computing a difference.
// It never sums unrelated rooms in a multi-room reservation.
func PriceOrderData(orderData, reserveData any) (any, error) {
	order, ok := orderData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("缺少用于核对房价的订单")
	}
	reserve, _ := reserveData.(map[string]any)
	if reserve == nil {
		reserve = order
	}
	if id := firstString(order, "reserveOrderId"); id != "" &&
		firstString(reserve, "reserveOrderId") != id {
		return nil, fmt.Errorf("房价明细不属于当前订单")
	}
	roomID, roomName := firstString(order, "roomId"), firstString(order, "roomName")
	if receptID := firstString(order, "receptOrderId"); receptID != "" {
		if rows, ok := reserve["receptOrderList"].([]any); ok {
			for _, value := range rows {
				row, _ := value.(map[string]any)
				if firstString(row, "receptOrderId") == receptID {
					if roomID == "" {
						roomID = firstString(row, "roomId")
					}
					if roomName == "" {
						roomName = firstString(row, "roomName")
					}
				}
			}
		}
	}
	products, _ := reserve["reserveProductList"].([]any)
	selected := make([]any, 0, 1)
	for _, value := range products {
		product, _ := value.(map[string]any)
		if product == nil {
			continue
		}
		matches := len(products) == 1 && roomID == "" && roomName == ""
		if roomID != "" {
			matches = firstString(product, "roomId") == roomID
		} else if roomName != "" {
			matches = firstString(product, "roomName") == roomName
		}
		if !matches {
			continue
		}
		if count := firstString(product, "reserveHomeCount"); count != "" && count != "1" {
			return nil, fmt.Errorf("同房型多间房的费用尚未分离，不能作为单间换房差价")
		}
		selected = append(selected, product)
	}
	if len(products) > 0 && len(selected) != 1 {
		return nil, fmt.Errorf("无法唯一定位当前房间的逐日价格")
	}
	result := make(map[string]any, len(order)+1)
	for key, value := range order {
		if key != "receptOrderList" && key != "reserveProductList" {
			result[key] = value
		}
	}
	if len(selected) == 1 {
		result["reserveProductList"] = selected
	}
	return result, nil
}
