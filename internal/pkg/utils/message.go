package utils

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/mlogclub/simple/sqls"
	"golang.org/x/net/html"
)

type imMessageAssetPayload struct {
	AssetID      string              `json:"assetId"`
	Provider     enums.AssetProvider `json:"provider,omitempty"`
	StorageKey   string              `json:"storageKey,omitempty"`
	Filename     string              `json:"filename,omitempty"`
	FileSize     int64               `json:"fileSize,omitempty"`
	MimeType     string              `json:"mimeType,omitempty"`
	URL          string              `json:"url,omitempty"`
	MediaText    string              `json:"mediaText,omitempty"`
	MediaSummary string              `json:"mediaSummary,omitempty"`
	MediaStatus  string              `json:"mediaUnderstandingStatus,omitempty"`
}

func SanitizeMessageHTML(content string) string {
	policy := bluemonday.UGCPolicy()
	policy.AllowElements("img")
	policy.AllowAttrs("src", "alt", "title", "data-asset-id", "data-provider", "data-storage-key").OnElements("img")
	policy.AllowURLSchemes("http", "https")
	policy.AllowStandardURLs()
	policy.AllowElements("p", "br")
	return stripHTMLImageSrcIfBound(strings.TrimSpace(policy.Sanitize(content)))
}

func NormalizeMessageHTMLAssets(content string) (string, error) {
	return normalizeMessageHTMLAssets(content, 0)
}

func NormalizeMessageHTMLAssetsInTenant(content string, tenantID int64) (string, error) {
	if tenantID <= 0 {
		return "", fmt.Errorf("html message asset tenant is required")
	}
	return normalizeMessageHTMLAssets(content, tenantID)
}

func normalizeMessageHTMLAssets(content string, tenantID int64) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	doc, err := html.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return content, nil
	}
	var walkErr error
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil || walkErr != nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			asset, err := normalizeHTMLImageAsset(node, tenantID)
			if err != nil {
				walkErr = err
				return
			}
			if asset != nil {
				setHTMLAttr(node, "data-asset-id", strings.TrimSpace(asset.AssetID))
				removeHTMLAttr(node, "data-provider")
				removeHTMLAttr(node, "data-storage-key")
				removeHTMLAttr(node, "src")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if walkErr != nil {
		return "", walkErr
	}
	return renderHTMLFragment(doc), nil
}

func BuildHTMLSummary(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return strings.TrimSpace(content)
	}
	parts := make([]string, 0, 8)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			parts = append(parts, "[图片]")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func BuildRuntimeMessageText(messageType enums.IMMessageType, content string) string {
	content = strings.TrimSpace(content)
	switch messageType {
	case enums.IMMessageTypeHTML:
		return BuildHTMLSummary(content)
	case enums.IMMessageTypeImage:
		if content != "" {
			return "[图片] " + content
		}
		return "[图片]"
	case enums.IMMessageTypeVoice:
		if content != "" {
			return "[语音] " + content
		}
		return "[语音]"
	case enums.IMMessageTypeVideo:
		if content != "" {
			return "[视频] " + content
		}
		return "[视频]"
	case enums.IMMessageTypeAttachment:
		if content != "" {
			return "[附件] " + content
		}
		return "[附件]"
	case enums.IMMessageTypeGIF:
		if content != "" {
			return "[动画表情] " + content
		}
		return "[动画表情]"
	default:
		return content
	}
}

func BuildRuntimeMessageTextWithPayload(messageType enums.IMMessageType, content string, payload string) string {
	// ASR transcript is the canonical business input. Do not prefix it with
	// media labels or the stored audio filename: every semantic stage must see
	// the same text as an equivalent customer text message.
	if messageType == enums.IMMessageTypeVoice {
		mediaText, mediaSummary, mediaStatus := RuntimeMediaUnderstandingFromPayload(payload)
		if mediaStatus != "" && mediaStatus != "understood" && mediaStatus != "ready" {
			return ""
		}
		if mediaText != "" {
			return mediaText
		}
		if mediaSummary != "" {
			return mediaSummary
		}
		return ""
	}
	base := BuildRuntimeMessageText(messageType, content)
	mediaText, mediaSummary, mediaStatus := RuntimeMediaUnderstandingFromPayload(payload)
	if mediaText != "" {
		return strings.TrimSpace(base + "\n" + runtimeMediaUnderstandingLabel(messageType) + "：" + mediaText)
	}
	if mediaSummary != "" {
		return strings.TrimSpace(base + "\n" + runtimeMediaSummaryLabel(messageType) + "：" + mediaSummary)
	}
	if isRuntimeMediaMessageType(messageType) && mediaStatus != "" && mediaStatus != "understood" {
		return strings.TrimSpace(base + "\n媒体理解状态：" + mediaStatus)
	}
	return base
}

func runtimeMediaUnderstandingLabel(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "图片内容是"
	case enums.IMMessageTypeVoice:
		return "语音内容是"
	case enums.IMMessageTypeAttachment:
		return "文件内容是"
	case enums.IMMessageTypeVideo:
		return "视频理解结果是"
	case enums.IMMessageTypeGIF:
		return "动画表情理解结果是"
	default:
		return "媒体内容是"
	}
}

func runtimeMediaSummaryLabel(messageType enums.IMMessageType) string {
	switch messageType {
	case enums.IMMessageTypeImage:
		return "图片摘要是"
	case enums.IMMessageTypeVoice:
		return "语音摘要是"
	case enums.IMMessageTypeAttachment:
		return "文件摘要是"
	case enums.IMMessageTypeVideo:
		return "视频摘要是"
	case enums.IMMessageTypeGIF:
		return "动画表情摘要是"
	default:
		return "媒体摘要是"
	}
}

func RuntimeMediaUnderstandingFromPayload(raw string) (mediaText string, mediaSummary string, status string) {
	if strings.TrimSpace(raw) == "" {
		return "", "", ""
	}
	var payload struct {
		MediaText    string `json:"mediaText"`
		MediaSummary string `json:"mediaSummary"`
		MediaStatus  string `json:"mediaUnderstandingStatus"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", "", ""
	}
	return strings.TrimSpace(payload.MediaText), strings.TrimSpace(payload.MediaSummary), strings.TrimSpace(payload.MediaStatus)
}

func isRuntimeMediaMessageType(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}

func BuildRenderableMessage(item *models.Message) (content, payload string) {
	if item == nil {
		return "", ""
	}
	if item.RecalledAt != nil {
		return "该消息已撤回", ""
	}
	if item.SendStatus == enums.IMMessageStatusRecalled {
		return "该消息已撤回", ""
	}

	content = item.Content
	payload = item.Payload
	switch item.MessageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		payload = buildIMMessageAssetPayloadForResponse(item.Payload, item.TenantID)
	case enums.IMMessageTypeHTML:
		content = BuildMessageHTMLForResponse(item.Content, item.TenantID)
	}
	return content, payload
}

func BuildMessageHTMLForResponse(content string, tenantIDs ...int64) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return content
	}
	var walk func(*html.Node)
	tenantID := int64(0)
	if len(tenantIDs) > 0 {
		tenantID = tenantIDs[0]
	}
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			assetID := strings.TrimSpace(findHTMLAttr(node, "data-asset-id"))
			storageKey := strings.TrimSpace(findHTMLAttr(node, "data-storage-key"))
			boundAsset := assetID != "" || storageKey != "" || strings.TrimSpace(findHTMLAttr(node, "data-provider")) != ""
			if boundAsset {
				removeHTMLAttr(node, "src")
			}
			var asset *models.Asset
			if tenantID > 0 && assetID != "" {
				asset = repositories.AssetRepository.GetByAssetIDInTenant(sqls.DB(), assetID, tenantID)
			} else if tenantID > 0 && storageKey != "" {
				asset = repositories.AssetRepository.GetByStorageKeyInTenant(sqls.DB(), storageKey, tenantID)
			}
			if asset != nil {
				if accessURL, err := assetaccess.BuildRelativeURL(asset.AssetID, asset.TenantID, assetaccess.PurposeInline); err == nil {
					setHTMLAttr(node, "src", accessURL)
					setHTMLAttr(node, "data-asset-id", asset.AssetID)
				}
			}
			removeHTMLAttr(node, "data-provider")
			removeHTMLAttr(node, "data-storage-key")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return renderHTMLFragment(doc)
}

func stripHTMLImageSrcIfBound(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return content
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			assetID := strings.TrimSpace(findHTMLAttr(node, "data-asset-id"))
			provider := strings.TrimSpace(findHTMLAttr(node, "data-provider"))
			storageKey := strings.TrimSpace(findHTMLAttr(node, "data-storage-key"))
			if assetID != "" || provider != "" || storageKey != "" {
				removeHTMLAttr(node, "src")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return renderHTMLFragment(doc)
}

func renderHTMLFragment(doc *html.Node) string {
	if doc == nil {
		return ""
	}
	root := findHTMLRoot(doc)
	if root == nil {
		return ""
	}
	var buf bytes.Buffer
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&buf, child); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(buf.String())
}

func findHTMLRoot(doc *html.Node) *html.Node {
	var walk func(*html.Node) *html.Node
	walk = func(node *html.Node) *html.Node {
		if node == nil {
			return nil
		}
		if node.Type == html.ElementNode && node.Data == "div" {
			return node
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(doc)
}

func findHTMLAttr(node *html.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func setHTMLAttr(node *html.Node, key, value string) {
	if node == nil {
		return
	}
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
}

func removeHTMLAttr(node *html.Node, key string) {
	if node == nil {
		return
	}
	dst := node.Attr[:0]
	for _, attr := range node.Attr {
		if attr.Key != key {
			dst = append(dst, attr)
		}
	}
	node.Attr = dst
}

func buildIMMessageAssetPayloadForResponse(payload string, tenantIDs ...int64) string {
	assetPayload, err := parseIMMessageAssetPayload(payload)
	if err != nil || assetPayload == nil {
		return strings.TrimSpace(payload)
	}
	assetPayload = hydrateIMMessageAssetPayload(assetPayload, tenantIDs...)
	assetPayload.URL = ""
	tenantID := int64(0)
	if len(tenantIDs) > 0 {
		tenantID = tenantIDs[0]
	}
	if assetPayload.AssetID != "" && assetPayload.StorageKey != "" && tenantID > 0 {
		if accessURL, err := assetaccess.BuildRelativeURL(assetPayload.AssetID, tenantID, assetaccess.PurposeInline); err == nil {
			assetPayload.URL = accessURL
		}
	}
	assetPayload.StorageKey = ""
	data, err := json.Marshal(assetPayload)
	if err != nil {
		return strings.TrimSpace(payload)
	}
	return string(data)
}

func parseIMMessageAssetPayload(payload string) (*imMessageAssetPayload, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, nil
	}
	ret := &imMessageAssetPayload{}
	if err := json.Unmarshal([]byte(payload), ret); err != nil {
		return nil, err
	}
	ret.AssetID = strings.TrimSpace(ret.AssetID)
	ret.Provider = enums.AssetProvider(strings.TrimSpace(string(ret.Provider)))
	ret.StorageKey = strings.TrimSpace(ret.StorageKey)
	return ret, nil
}

func hydrateIMMessageAssetPayload(payload *imMessageAssetPayload, tenantIDs ...int64) *imMessageAssetPayload {
	if payload == nil {
		return nil
	}
	if payload.AssetID == "" {
		payload.StorageKey = ""
		payload.URL = ""
		return payload
	}
	if len(tenantIDs) == 0 || tenantIDs[0] <= 0 {
		payload.StorageKey = ""
		payload.URL = ""
		return payload
	}
	asset := repositories.AssetRepository.GetByAssetIDInTenant(sqls.DB(), payload.AssetID, tenantIDs[0])
	if asset == nil {
		payload.Provider = ""
		payload.StorageKey = ""
		payload.URL = ""
		return payload
	}
	payload.Provider = asset.Provider
	payload.StorageKey = strings.TrimSpace(asset.StorageKey)
	if payload.Filename == "" {
		payload.Filename = strings.TrimSpace(asset.Filename)
	}
	if payload.FileSize <= 0 {
		payload.FileSize = asset.FileSize
	}
	if payload.MimeType == "" {
		payload.MimeType = strings.TrimSpace(asset.MimeType)
	}
	return payload
}

func normalizeHTMLImageAsset(node *html.Node, tenantID int64) (*models.Asset, error) {
	if node == nil {
		return nil, nil
	}
	assetID := strings.TrimSpace(findHTMLAttr(node, "data-asset-id"))
	provider := enums.AssetProvider(strings.TrimSpace(findHTMLAttr(node, "data-provider")))
	storageKey := strings.TrimSpace(findHTMLAttr(node, "data-storage-key"))

	hasAssetID := assetID != ""
	hasProvider := provider != ""
	hasStorageKey := storageKey != ""
	if hasAssetID {
		var asset *models.Asset
		if tenantID > 0 {
			asset = repositories.AssetRepository.GetByAssetIDInTenant(sqls.DB(), assetID, tenantID)
		} else {
			asset = repositories.AssetRepository.GetByAssetID(sqls.DB(), assetID)
		}
		if asset == nil {
			return nil, fmt.Errorf("html message image asset not found")
		}
		if (hasProvider && asset.Provider != provider) || (hasStorageKey && strings.TrimSpace(asset.StorageKey) != storageKey) {
			return nil, fmt.Errorf("html message image asset attributes mismatch")
		}
		return asset, nil
	}
	if hasProvider || hasStorageKey {
		return nil, fmt.Errorf("html message image asset attributes are incomplete")
	}
	return nil, fmt.Errorf("html message image must include asset metadata")
}
