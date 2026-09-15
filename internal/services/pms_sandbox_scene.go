package services

import (
	"context"
	"encoding/json"
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
		// The demo has one customer-facing answer for the product question.
		// The actual product card is still sent separately by Commit.
		name, price := pillowCardSummary(state.Resource.CardPayload)
		result.Reply = fmt.Sprintf("您喜欢的是%s%s。给您发商品资料，您可以先看看。", name, price)
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
		if hasTopic(input.Topics, "birthday") &&
			(hasTopic(input.Topics, "benefits") || hasTopic(input.Topics, "membership") || hasTopic(input.Topics, "member")) &&
			hasOnlyTopics(input.Topics, "birthday", "benefits", "membership", "member") {
			// Keep the showcase question as a single, deterministic answer.
			result.Reply = "您当前是钻石会员，已入住 16 次。生日可享 100 元礼遇，有效期 30 天。"
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
		if hasTopic(topics, "parking") && hasOnlyTopics(topics, "parking") {
			result.Reply = "酒店提供免费停车服务，设有地上地下停车场，推荐您从昭潭路进入。"
			result.Completed = true
			return result, nil
		}
		if hasTopic(topics, "breakfast") && hasTopic(topics, "child_policy") &&
			hasOnlyTopics(topics, "breakfast", "child_policy") {
			// Keep the showcase question as one reply instead of concatenating
			// separate topic sentences.
			result.Reply = "您的订单包含 2 份早餐，供应时间为 07:00–10:00。1.2 米以下儿童免费用餐。"
			result.Completed = true
			return result, nil
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
	switch scene {
	case "B":
		result.Reply = "可以为您查询升级房型，请以当前订单可用房型和差价为准。"
		result.Completed = true
		return result, nil
	case "C":
		result.Reply = "可以为您查询可换房间，请以当前房态和房间安排为准。"
		result.Completed = true
		return result, nil
	case "D":
		result.Reply = "已了解您的服务需求，我先按现有服务信息为您处理；具体安排以实际房态和服务规则为准。"
		result.Completed = true
		return result, nil
	}
	return nil, errors.New("不支持的测试 PMS 场景")
}

func pillowCardSummary(payload string) (string, string) {
	var raw map[string]any
	if json.Unmarshal([]byte(payload), &raw) != nil {
		return "这款枕头", ""
	}
	content, _ := raw["content"].(map[string]any)
	name, _ := content["product_title"].(string)
	if name == "" {
		name = "这款枕头"
	}
	price, _ := content["product_price"].(string)
	if price != "" {
		return name, "，售价 " + price + " 元"
	}
	return name, ""
}

func hasTopic(topics []string, value string) bool {
	for _, topic := range topics {
		if topic == value {
			return true
		}
	}
	return false
}

func hasOnlyTopics(topics []string, allowed ...string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, topic := range allowed {
		allowedSet[topic] = struct{}{}
	}
	for _, topic := range topics {
		if _, ok := allowedSet[topic]; !ok {
			return false
		}
	}
	return true
}
