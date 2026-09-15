package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent-desk/internal/pms/sandbox"
)

func (s *pmsSandboxService) ExecuteScene(ctx context.Context, scope sandbox.Scope, input sandbox.SceneInput) (*sandbox.SceneResult, error) {
	if err := s.available(true); err != nil {
		return nil, err
	}
	scene := strings.ToUpper(strings.TrimSpace(input.Scene))
	switch scene {
	case "A", "B", "C", "D", "E", "F":
	default:
		return nil, errors.New("不支持的测试 PMS 场景")
	}
	var locateErr error
	if scene == "E" {
		locateErr = s.locateMember(ctx, scope, input.Phone)
	} else {
		locateErr = s.locate(ctx, scope, input.Phone, input.OrderNumber)
	}
	if locateErr != nil {
		return nil, locateErr
	}
	state, err := s.Query(ctx, scope)
	if err != nil {
		return nil, err
	}
	result := &sandbox.SceneResult{Scene: scene, State: state}
	if scene == "F" {
		if state.Resource == nil || state.Resource.CardPayload == "" || state.Resource.SourceMessageID <= 0 || state.Resource.MessageType == "" {
			result.Reply = "【测试 PMS】同款枕头商品卡片尚未完成绑定，暂时不能发送可打开的商品卡片。"
			return result, nil
		}
		result.Reply = "【测试 PMS】这是同款枕头商品卡片。"
		result.Resource, result.Completed = state.Resource, true
		return result, nil
	}
	if scene == "E" {
		if state.Member == nil {
			result.Reply = "【测试 PMS】请提供测试会员手机号，或先在测试 PMS 后台绑定测试会员。"
			result.NeedsInput = []string{"phone"}
			return result, nil
		}
		if state.Grade == nil {
			result.Reply = "【测试 PMS】当前测试会员未启用或权益已过期，暂不享有有效会员权益。"
			result.Completed = true
			return result, nil
		}
		parts := []string{fmt.Sprintf("【测试 PMS】您是%s，有效期至%s。", state.Grade.Name, state.Member.ValidUntil.Format("2006-01-02"))}
		if hasTopic(input.Topics, "birthday") {
			if len(state.Grade.BirthdayBenefits) == 0 {
				parts = append(parts, "当前未配置生日福利。")
			} else {
				parts = append(parts, "生日福利："+strings.Join(state.Grade.BirthdayBenefits, "；")+"。")
			}
		}
		if len(input.Topics) == 0 || !hasTopic(input.Topics, "birthday") || hasTopic(input.Topics, "benefits") || hasTopic(input.Topics, "membership") {
			if len(state.Grade.Benefits) == 0 {
				parts = append(parts, "当前未配置其他会员权益。")
			} else {
				parts = append(parts, "有效权益："+strings.Join(state.Grade.Benefits, "；")+"。")
			}
		}
		parts = append(parts, "以上是测试会员配置，不代表外部酒店权益。")
		result.Reply, result.Completed = strings.Join(parts, ""), true
		return result, nil
	}
	if state.Order == nil {
		result.Reply = "【测试 PMS】请提供测试订单号或测试手机号，或先在测试 PMS 后台绑定订单。"
		result.NeedsInput = []string{"order"}
		return result, nil
	}
	if scene == "A" {
		topics := input.Topics
		if len(topics) == 0 {
			topics = []string{"order"}
		}
		parts := []string{"【测试 PMS】"}
		for _, topic := range topics {
			switch topic {
			case "breakfast":
				if state.Order.IncludesBreakfast {
					parts = append(parts, "当前测试订单包含早餐。")
				} else {
					parts = append(parts, "当前测试订单不包含早餐。")
				}
			case "child_policy":
				policy := ""
				for _, rule := range state.Rules {
					if rule.Code == "child_policy" && rule.Enabled {
						policy = rule.Text
						break
					}
				}
				if policy == "" {
					parts = append(parts, "当前没有有效的测试儿童收费政策，不能确认收费。")
					result.NeedsInput = append(result.NeedsInput, "child_policy")
				} else {
					parts = append(parts, policy)
				}
			case "checkout":
				parts = append(parts, "当前订单退房时间为"+state.Order.CheckOut.Format("2006-01-02 15:04")+"。")
			case "checkin":
				parts = append(parts, "当前订单入住时间为"+state.Order.CheckIn.Format("2006-01-02 15:04")+"。")
			case "inventory":
				available := []string{}
				for _, inventory := range state.Availability {
					available = append(available, fmt.Sprintf("%s可用%d间", inventory.RoomType.Name, inventory.Available))
				}
				parts = append(parts, "当前订单入住区间内："+strings.Join(available, "，")+"；库存只是查询时状态，尚未锁房。")
			case "price":
				parts = append(parts, "测试订单应付金额为"+sandboxMoney(state.Order.PayableCents)+"，不代表已实际付款。")
			case "order":
				status := map[string]string{"reserved": "已预订", "checked_in": "已入住", "checked_out": "已退房", "cancelled": "已取消"}[state.Order.Status]
				room := state.Order.RoomNumber
				if room == "" {
					room = "尚未排房"
				}
				parts = append(parts, fmt.Sprintf("测试订单%s：%s，%s，入住%s，退房%s，应付%s，状态%s。", state.Order.Number, state.Order.RoomTypeName, room, state.Order.CheckIn.Format("2006-01-02 15:04"), state.Order.CheckOut.Format("2006-01-02 15:04"), sandboxMoney(state.Order.PayableCents), status))
			default:
				return nil, fmt.Errorf("不支持的测试订单查询方面：%s", topic)
			}
		}
		result.Reply, result.Completed = strings.Join(parts, ""), len(result.NeedsInput) == 0
		return result, nil
	}
	change := sandbox.ChangeRequest{OrderID: state.Order.ID}
	if input.Change != nil {
		change = *input.Change
		if change.OrderID == 0 {
			change.OrderID = state.Order.ID
		}
	}
	switch scene {
	case "B":
		change.Upgrade = true
	case "C":
		change.ChangeRoom = true
	case "D":
		if !change.Upgrade && !change.ChangeRoom && !change.LateCheckout {
			change.Recovery = true
		}
	}
	operation, err := s.Prepare(ctx, scope, change)
	if err != nil {
		return nil, err
	}
	result.Operation, result.Reply, result.Completed = operation, operation.PreviewText, true
	return result, nil
}

func hasTopic(topics []string, value string) bool {
	for _, topic := range topics {
		if topic == value {
			return true
		}
	}
	return false
}
