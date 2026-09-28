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

// The executor owns intent selection; this service only prepares the bound resource.
func (s *wxWorkProtocolShopProductResourceService) BuildPillowProductMessage() (*WxWorkShopProductResource, error) {
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
