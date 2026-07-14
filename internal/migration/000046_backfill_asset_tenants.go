package migration

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"golang.org/x/net/html"
	"gorm.io/gorm"
)

func init() {
	register(46, "backfill asset tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillAssetTenants(ctx.Tx)
		})
	})
}

type assetMessageReference struct {
	MessageID int64
	TenantID  int64
}

func backfillAssetTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before asset tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	userTenants, err := loadConversationDomainTenantIDs(tx, &models.User{})
	if err != nil {
		return err
	}
	messageReferences, err := loadAssetMessageReferences(tx)
	if err != nil {
		return err
	}

	var assets []models.Asset
	if err := tx.Order("id ASC").Find(&assets).Error; err != nil {
		return err
	}
	knownAssetIDs := make(map[string]int64, len(assets))
	for i := range assets {
		knownAssetIDs[strings.TrimSpace(assets[i].AssetID)] = assets[i].ID
	}
	for assetID, refs := range messageReferences {
		if _, ok := knownAssetIDs[assetID]; !ok {
			return fmt.Errorf("message %d references missing asset %q", refs[0].MessageID, assetID)
		}
	}

	for i := range assets {
		item := &assets[i]
		resolver := newConversationDomainTenantResolver("asset", item.ID, item.TenantID, validTenantIDs)
		if item.CreateUserID > 0 {
			tenantID, ok := userTenants[item.CreateUserID]
			if !ok {
				return fmt.Errorf("asset %d references missing creator user %d", item.ID, item.CreateUserID)
			}
			if tenantID > 0 {
				if err := resolver.merge("creator", item.CreateUserID, tenantID); err != nil {
					return err
				}
			}
		}
		for _, ref := range messageReferences[strings.TrimSpace(item.AssetID)] {
			if ref.TenantID <= 0 {
				return fmt.Errorf("asset %d message %d has no tenant", item.ID, ref.MessageID)
			}
			if err := resolver.merge("message", ref.MessageID, ref.TenantID); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenant.ID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.Asset{}, "asset", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func loadAssetMessageReferences(tx *gorm.DB) (map[string][]assetMessageReference, error) {
	var messages []models.Message
	if err := tx.Order("id ASC").Find(&messages).Error; err != nil {
		return nil, err
	}
	ret := make(map[string][]assetMessageReference)
	for i := range messages {
		assetIDs, err := messageAssetIDs(messages[i])
		if err != nil {
			return nil, fmt.Errorf("message %d asset reference: %w", messages[i].ID, err)
		}
		for _, assetID := range assetIDs {
			ret[assetID] = append(ret[assetID], assetMessageReference{
				MessageID: messages[i].ID,
				TenantID:  messages[i].TenantID,
			})
		}
	}
	return ret, nil
}

func messageAssetIDs(message models.Message) ([]string, error) {
	switch message.MessageType {
	case enums.IMMessageTypeHTML:
		return htmlMessageAssetIDs(message.Content)
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeVideo, enums.IMMessageTypeAttachment, enums.IMMessageTypeGIF:
		if strings.TrimSpace(message.Payload) == "" {
			return nil, nil
		}
		var payload struct {
			AssetID string `json:"assetId"`
		}
		if err := json.Unmarshal([]byte(message.Payload), &payload); err != nil {
			return nil, err
		}
		if assetID := strings.TrimSpace(payload.AssetID); assetID != "" {
			return []string{assetID}, nil
		}
	}
	return nil, nil
}

func htmlMessageAssetIDs(content string) ([]string, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	doc, err := html.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	ret := make([]string, 0)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "img" {
			for _, attr := range node.Attr {
				if attr.Key != "data-asset-id" {
					continue
				}
				assetID := strings.TrimSpace(attr.Val)
				if assetID != "" {
					if _, ok := seen[assetID]; !ok {
						seen[assetID] = struct{}{}
						ret = append(ret, assetID)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return ret, nil
}
