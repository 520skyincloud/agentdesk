package services

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/pkg/enums"
)

const (
	pillowProductID    = "10001004185008"
	pillowProductAppID = "wxca8d4b8e8feedc2a"
	pillowProductTitle = "丽斯严选零压力护颈椎枕头"
	pillowProductLead  = "这是我们酒店同款，您可以先看看，需要的话再下单就好。"
)

//go:embed resources/pillow_product.json
var pillowProductPayload string

var WxWorkProtocolShopProductResourceService = &wxWorkProtocolShopProductResourceService{}

type wxWorkProtocolShopProductResourceService struct{}

type WxWorkShopProductResource struct {
	Lead        string
	Content     string
	Payload     string
	MessageType enums.IMMessageType
}

func (s *wxWorkProtocolShopProductResourceService) BuildPillowProductMessage(customerText string) (*WxWorkShopProductResource, error) {
	if isPillowRoomServiceRequest(customerText) {
		return nil, fmt.Errorf("当前消息是客房枕头服务请求，不能发送商品卡")
	}
	if !isPillowProductPurchaseRequest(customerText) {
		return nil, fmt.Errorf("当前消息没有明确的枕头购买意图，不能发送商品卡")
	}
	payload := strings.TrimSpace(pillowProductPayload)
	content, err := validatePillowProductPayload(payload)
	if err != nil {
		return nil, err
	}
	return &WxWorkShopProductResource{
		Lead:        pillowProductLead,
		Content:     content.ProductTitle,
		Payload:     payload,
		MessageType: enums.IMMessageTypeShopProduct,
	}, nil
}

func validatePillowProductPayload(payload string) (pillowProductContent, error) {
	body := struct {
		Content pillowProductContent `json:"content"`
	}{}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return pillowProductContent{}, fmt.Errorf("枕头商品 payload 不是有效 JSON: %w", err)
	}
	content := body.Content
	for field, value := range map[string]string{
		"product_appid":        content.ProductAppID,
		"product_page_path":    content.ProductPagePath,
		"product_id":           content.ProductID,
		"product_cover_url":    content.ProductCoverURL,
		"product_title":        content.ProductTitle,
		"shop_info.url":        content.ShopInfo.URL,
		"shop_info.extra_info": content.ShopInfo.ExtraInfo,
	} {
		if strings.TrimSpace(value) == "" {
			return pillowProductContent{}, fmt.Errorf("枕头商品 payload 缺少协议字段 %s", field)
		}
	}
	if content.ProductID != pillowProductID || content.ProductAppID != pillowProductAppID || content.ProductTitle != pillowProductTitle {
		return pillowProductContent{}, fmt.Errorf("枕头商品 payload 与受控商品不一致")
	}
	return content, nil
}

type pillowProductContent struct {
	ProductAppID    string `json:"product_appid"`
	ProductPagePath string `json:"product_page_path"`
	ProductID       string `json:"product_id"`
	ProductCoverURL string `json:"product_cover_url"`
	ProductTitle    string `json:"product_title"`
	ShopInfo        struct {
		URL       string `json:"url"`
		ExtraInfo string `json:"extra_info"`
	} `json:"shop_info"`
}

func isPillowProductPurchaseRequest(text string) bool {
	compact := compactShopProductIntentText(text)
	if compact == "" || isPillowRoomServiceRequest(compact) {
		return false
	}
	return containsAnyShopProductText(compact, []string{
		"同款怎么买", "怎么买", "哪里买", "在哪买", "购买", "购买链接", "链接", "下单", "多少钱", "价格",
		"卖吗", "能买吗", "可以买", "想买", "我要买", "怎么卖",
	})
}

func isPillowRoomServiceRequest(text string) bool {
	compact := compactShopProductIntentText(text)
	if !strings.Contains(compact, "枕头") {
		return false
	}
	return containsAnyShopProductText(compact, []string{
		"送个枕头", "送一个枕头", "送两个枕头", "送枕头到", "枕头送到", "送来枕头",
		"拿个枕头", "拿一个枕头", "拿来枕头", "换个枕头", "换一个枕头", "更换枕头",
		"加个枕头", "加一个枕头", "加两个枕头", "补个枕头", "补一个枕头", "多要个枕头",
		"枕头脏", "枕头坏", "枕头破", "枕头不舒服", "枕头不合适", "枕头太高", "枕头太低",
	})
}

func compactShopProductIntentText(text string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "，", "", ",", "", "。", "", "！", "", "!", "", "？", "", "?", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

func containsAnyShopProductText(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}
