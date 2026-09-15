package services

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pms/sandbox"
)

// ImportPMSSandboxResource derives card data exclusively from a scoped source
// message. Never accept caller-supplied recipient, credentials or page overrides.
func ImportPMSSandboxResource(storeID int64, resource *sandbox.Resource) error {
	if resource == nil {
		return errorsx.InvalidParam("缺少商品资源")
	}
	resource.CardPayload = ""
	resource.MessageType = ""
	if resource.SourceMessageID <= 0 {
		return nil
	}
	message := MessageService.Get(resource.SourceMessageID)
	if message == nil {
		return errorsx.InvalidParam("原商品卡片消息不存在")
	}
	route := ConversationRouteService.GetByConversationID(message.ConversationID)
	if route == nil || route.StoreID != storeID {
		return errorsx.Forbidden("原商品卡片不属于当前门店")
	}
	payload, err := SanitizePMSSandboxCard(string(message.MessageType), message.Payload)
	if err != nil {
		return err
	}
	if message.MessageType == enums.IMMessageTypeMiniProgram && route.WxWorkInstanceID > 0 {
		instance := WxWorkProtocolInstanceService.Get(route.WxWorkInstanceID)
		if instance != nil && samePMSSandboxMiniProgramTarget(payload, instance.DefaultMiniProgramPayload) {
			return errorsx.InvalidParam("该消息是入住小程序，不能绑定为枕头商品")
		}
	}
	resource.CardPayload = payload
	resource.MessageType = string(message.MessageType)
	return nil
}

// Fields follow the employee-account send_weapp/send_finder_product contracts.
// This sanitizer does not call or alter the protocol adapter.
func SanitizePMSSandboxCard(messageType, payload string) (string, error) {
	body := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil || body == nil {
		return "", errorsx.InvalidParam("原商品卡片内容不是有效对象")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return "", errorsx.InvalidParam("原商品卡片内容格式不完整")
	}
	if nested, ok := body["wxPayload"].(map[string]any); ok {
		body = nested
	}
	result := make(map[string]any)
	switch enums.IMMessageType(messageType) {
	case enums.IMMessageTypeMiniProgram:
		for _, key := range []string{"username", "appid", "appname", "appicon", "title", "page_path", "file_id", "size", "aes_key", "md5"} {
			if value, ok := body[key]; ok {
				result[key] = value
			}
		}
		for _, key := range []string{"username", "appid", "title", "page_path"} {
			if value, ok := result[key].(string); !ok || strings.TrimSpace(value) == "" {
				return "", errorsx.InvalidParam(fmt.Sprintf("原小程序缺少协议字段 %s，暂不可用", key))
			}
		}
		if isEmptyProtocolValue(result["appicon"]) && (isEmptyProtocolValue(result["file_id"]) || isEmptyProtocolValue(result["aes_key"]) || isEmptyProtocolValue(result["md5"]) || isEmptyProtocolValue(result["size"])) {
			return "", errorsx.InvalidParam("原小程序缺少可发送封面，暂不可用")
		}
	case enums.IMMessageTypeShopProduct:
		source, ok := body["content"].(map[string]any)
		if !ok {
			return "", errorsx.InvalidParam("原商品卡片缺少 content 对象，暂不可用")
		}
		content := make(map[string]any)
		for _, key := range []string{"finder_live_id", "finder_username", "finder_object_id", "finder_nonce_id", "product_appid", "product_page_path", "product_id", "product_cover_url", "product_title", "product_desc", "product_price", "platform_headimg", "platform_nickname"} {
			value, exists := source[key]
			if !exists {
				return "", errorsx.InvalidParam(fmt.Sprintf("原商品卡片缺少协议字段 %s，暂不可用", key))
			}
			switch value.(type) {
			case string, json.Number:
				content[key] = fmt.Sprint(value)
			default:
				return "", errorsx.InvalidParam("原商品卡片字段类型不正确")
			}
		}
		for _, key := range []string{"product_appid", "product_page_path", "product_id", "product_title"} {
			if isEmptyProtocolValue(content[key]) {
				return "", errorsx.InvalidParam("原商品卡片缺少有效商品目标，暂不可用")
			}
		}
		shop, ok := source["shop_info"].(map[string]any)
		if !ok {
			return "", errorsx.InvalidParam("原商品卡片缺少店铺信息，暂不可用")
		}
		cleanShop := make(map[string]any)
		for _, key := range []string{"shop_id", "url", "extra_info"} {
			value, exists := shop[key]
			if !exists {
				return "", errorsx.InvalidParam("原商品卡片店铺信息不完整，暂不可用")
			}
			cleanShop[key] = fmt.Sprint(value)
		}
		content["shop_info"] = cleanShop
		result["content"] = content
	default:
		return "", errorsx.InvalidParam("该消息不是原小程序或微信小店商品卡片；文本口令不能作为卡片验收")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", errorsx.InvalidParam("原商品卡片无法编码")
	}
	return string(encoded), nil
}

func samePMSSandboxMiniProgramTarget(left, right string) bool {
	a, errA := wxProtocolRichPayload(left)
	b, errB := wxProtocolRichPayload(right)
	return errA == nil && errB == nil && !isEmptyProtocolValue(a["appid"]) &&
		fmt.Sprint(a["appid"]) == fmt.Sprint(b["appid"]) &&
		fmt.Sprint(a["username"]) == fmt.Sprint(b["username"])
}
