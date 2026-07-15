package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	knowledgeResourceProviderFastGPT = "fastgpt_cloud"
	maxKnowledgeResourceImageBytes   = 12 << 20
	maxKnowledgeResourceImagePixels  = 64 * 1024 * 1024
)

var KnowledgeResourceService = newKnowledgeResourceService()

type knowledgeResourceService struct {
	httpClient *http.Client
}

type KnowledgeResourceSourceRef struct {
	KnowledgeBaseID int64
	SourceRecordID  string
}

type RuntimeKnowledgeResource struct {
	GroupID         int64
	ItemID          int64
	KnowledgeBaseID int64
	SourceRecordID  string
	AssetID         string
	Title           string
	Description     string
	SortNo          int
}

type downloadedKnowledgeResourceImage struct {
	SourceURL   string
	Title       string
	Description string
	SortNo      int
	Data        []byte
	Filename    string
	Checksum    string
}

func newKnowledgeResourceService() *knowledgeResourceService {
	return &knowledgeResourceService{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *knowledgeResourceService) SyncFastGPTResources(ctx context.Context, req request.SyncKnowledgeResourceGroupRequest, operator *dto.AuthPrincipal) (*models.KnowledgeResourceGroup, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if req.WxWorkInstanceID <= 0 || req.KnowledgeBaseID <= 0 || strings.TrimSpace(req.Query) == "" {
		return nil, errorsx.InvalidParam("请选择企微员工号、知识库并填写用于同步的检索问题")
	}
	tenantID, err := requireActiveTenantID(operator, "知识图片资源")
	if err != nil {
		return nil, err
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(req.WxWorkInstanceID, tenantID)
	if instance == nil || instance.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("企微员工号不存在或未启用")
	}
	if !s.canAccessWxWorkInstance(operator, instance.ID) {
		return nil, errorsx.Forbidden("无权限维护该企微员工号的知识图片资源")
	}
	if instance.KnowledgeBaseID != req.KnowledgeBaseID {
		return nil, errorsx.InvalidParam("只能同步当前企微员工号已绑定的知识库资源")
	}
	knowledgeBase := KnowledgeBaseService.GetInTenant(req.KnowledgeBaseID, tenantID)
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("知识库不存在或未启用")
	}
	if knowledgeBase.StoreID > 0 && knowledgeBase.StoreID != instance.StoreID {
		return nil, errorsx.InvalidParam("只能同步当前门店自己的知识图片资源")
	}
	if !KnowledgeBaseService.CanAccessKnowledgeBase(knowledgeBase.ID, operator) {
		return nil, errorsx.Forbidden("无权限维护该知识库图片资源")
	}
	if !isFastGPTKnowledgeBaseForResources(knowledgeBase) {
		return nil, errorsx.InvalidParam("只有 FastGPT 知识库支持同步图片资源")
	}
	allowedHosts := knowledgeResourceAllowedHosts(knowledgeBase.Remark)
	if len(allowedHosts) == 0 {
		return nil, errorsx.InvalidParam("知识库未配置 resourceAllowedHosts，无法同步外部图片资源")
	}

	source, err := rag.FetchFastGPTSyncSource(ctx, *knowledgeBase, strings.TrimSpace(req.Query))
	if err != nil {
		return nil, err
	}
	if expected := strings.TrimSpace(req.ExpectedSourceRecordID); expected != "" && expected != source.SourceRecordID {
		return nil, errorsx.InvalidParam("FastGPT 返回的来源记录与确认记录不一致")
	}
	if len(source.Resources) == 0 {
		return nil, errorsx.InvalidParam("该 FastGPT 知识记录没有可同步的图片资源")
	}

	images, err := s.downloadTrustedImages(ctx, allowedHosts, source.Resources)
	if err != nil {
		return nil, err
	}
	return s.persistSyncedResources(instance, knowledgeBase, source, images, operator)
}

func (s *knowledgeResourceService) FindPageByCnd(cnd *sqls.Cnd) ([]models.KnowledgeResourceGroup, *sqls.Paging) {
	return repositories.KnowledgeResourceGroupRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *knowledgeResourceService) ApplyAccessibleScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	tenantID := int64(0)
	if operator != nil {
		tenantID = operator.ActiveTenantID
	}
	if tenantID <= 0 {
		return cnd.Eq("tenant_id", -1)
	}
	cnd = cnd.Eq("tenant_id", tenantID)
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	if len(scope.StoreIDs) > 0 {
		return cnd.In("store_id", scope.StoreIDs)
	}
	return cnd.Eq("store_id", -1)
}

func (s *knowledgeResourceService) FindItems(groupID, tenantID int64) []models.KnowledgeResourceItem {
	if groupID <= 0 || tenantID <= 0 {
		return nil
	}
	return repositories.KnowledgeResourceItemRepository.FindByGroupIDInTenant(sqls.DB(), groupID, tenantID)
}

func (s *knowledgeResourceService) DeleteGroup(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID, err := requireActiveTenantID(operator, "知识图片资源")
	if err != nil {
		return err
	}
	group := repositories.KnowledgeResourceGroupRepository.GetInTenant(sqls.DB(), id, tenantID)
	if group == nil {
		return errorsx.InvalidParam("知识图片资源组不存在")
	}
	if !KnowledgeBaseService.CanAccessKnowledgeBase(group.KnowledgeBaseID, operator) {
		return errorsx.Forbidden("无权限删除该知识图片资源组")
	}
	if !s.canAccessStore(operator, group.StoreID) {
		return errorsx.Forbidden("无权限删除该门店的知识图片资源")
	}
	items := repositories.KnowledgeResourceItemRepository.FindByGroupIDInTenant(sqls.DB(), group.ID, tenantID)
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.KnowledgeResourceItemRepository.DeleteByGroupIDInTenant(tx.Tx, group.ID, tenantID); err != nil {
			return err
		}
		return repositories.KnowledgeResourceGroupRepository.DeleteInTenant(tx.Tx, group.ID, tenantID)
	}); err != nil {
		return err
	}
	s.cleanupUnreferencedAssets(assetIDsFromKnowledgeResourceItems(items), operator)
	return nil
}

func (s *knowledgeResourceService) canAccessStore(operator *dto.AuthPrincipal, storeID int64) bool {
	if operator == nil || storeID <= 0 {
		return false
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return true
	}
	for _, allowedStoreID := range scope.StoreIDs {
		if allowedStoreID == storeID {
			return true
		}
	}
	return false
}

func (s *knowledgeResourceService) ResolveForRuntime(wxWorkInstanceID, companyID, tenantID int64, sources []KnowledgeResourceSourceRef) []RuntimeKnowledgeResource {
	if wxWorkInstanceID <= 0 || tenantID <= 0 || len(sources) == 0 || sqls.DB() == nil {
		return nil
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(wxWorkInstanceID, tenantID)
	if instance == nil || instance.TenantID <= 0 || instance.StoreID <= 0 {
		return nil
	}
	storeID := instance.StoreID
	if companyID > 0 && companyID != instance.CompanyID {
		return nil
	}
	companyID = instance.CompanyID
	seenSource := map[string]bool{}
	ret := make([]RuntimeKnowledgeResource, 0)
	for _, source := range sources {
		if source.KnowledgeBaseID <= 0 || strings.TrimSpace(source.SourceRecordID) == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", source.KnowledgeBaseID, strings.TrimSpace(source.SourceRecordID))
		if seenSource[key] {
			continue
		}
		seenSource[key] = true
		group := repositories.KnowledgeResourceGroupRepository.Take(sqls.DB(),
			"tenant_id = ? AND company_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_provider = ? AND source_record_id = ? AND status = ?",
			instance.TenantID,
			companyID,
			storeID,
			source.KnowledgeBaseID,
			knowledgeResourceProviderFastGPT,
			strings.TrimSpace(source.SourceRecordID),
			enums.StatusOk,
		)
		if group == nil {
			group = repositories.KnowledgeResourceGroupRepository.Take(sqls.DB(),
				"tenant_id = ? AND knowledge_base_id = ? AND wx_work_instance_id = ? AND source_provider = ? AND source_record_id = ? AND status = ?",
				instance.TenantID,
				source.KnowledgeBaseID,
				wxWorkInstanceID,
				knowledgeResourceProviderFastGPT,
				strings.TrimSpace(source.SourceRecordID),
				enums.StatusOk,
			)
		}
		if group == nil {
			continue
		}
		for _, item := range repositories.KnowledgeResourceItemRepository.FindByGroupIDInTenant(sqls.DB(), group.ID, instance.TenantID) {
			if item.Status != enums.StatusOk || strings.TrimSpace(item.AssetID) == "" {
				continue
			}
			asset := AssetService.GetByAssetIDInTenant(item.AssetID, instance.TenantID)
			if asset == nil || asset.Status != enums.AssetStatusSuccess {
				continue
			}
			ret = append(ret, RuntimeKnowledgeResource{
				GroupID:         group.ID,
				ItemID:          item.ID,
				KnowledgeBaseID: group.KnowledgeBaseID,
				SourceRecordID:  group.SourceRecordID,
				AssetID:         item.AssetID,
				Title:           firstNonBlank(item.Title, asset.Filename),
				Description:     item.Description,
				SortNo:          item.SortNo,
			})
		}
	}
	sort.SliceStable(ret, func(i, j int) bool {
		if ret[i].GroupID == ret[j].GroupID {
			if ret[i].SortNo == ret[j].SortNo {
				return ret[i].ItemID < ret[j].ItemID
			}
			return ret[i].SortNo < ret[j].SortNo
		}
		return ret[i].GroupID < ret[j].GroupID
	})
	return ret
}

func (s *knowledgeResourceService) persistSyncedResources(instance *models.WxWorkProtocolInstance, knowledgeBase *models.KnowledgeBase, source rag.FastGPTSyncSource, images []downloadedKnowledgeResourceImage, operator *dto.AuthPrincipal) (*models.KnowledgeResourceGroup, error) {
	if instance == nil || knowledgeBase == nil || len(images) == 0 {
		return nil, errorsx.InvalidParam("没有可保存的知识图片资源")
	}
	if instance.TenantID <= 0 || instance.TenantID != knowledgeBase.TenantID {
		return nil, errorsx.InvalidParam("企微员工号与知识库不属于同一接入公司")
	}
	existing := repositories.KnowledgeResourceGroupRepository.Take(sqls.DB(),
		"tenant_id = ? AND company_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_provider = ? AND source_record_id = ?",
		instance.TenantID,
		instance.CompanyID,
		instance.StoreID,
		knowledgeBase.ID,
		knowledgeResourceProviderFastGPT,
		source.SourceRecordID,
	)
	oldItems := []models.KnowledgeResourceItem(nil)
	if existing != nil {
		oldItems = repositories.KnowledgeResourceItemRepository.FindByGroupIDInTenant(sqls.DB(), existing.ID, instance.TenantID)
	}
	reusable := make(map[string]models.KnowledgeResourceItem, len(oldItems))
	for _, item := range oldItems {
		if item.AssetID != "" && item.SourceURL != "" && item.SourceChecksum != "" {
			reusable[item.SourceURL+"|"+item.SourceChecksum] = item
		}
	}

	items := make([]models.KnowledgeResourceItem, 0, len(images))
	createdAssetIDs := make([]string, 0, len(images))
	for _, imageItem := range images {
		key := imageItem.SourceURL + "|" + imageItem.Checksum
		assetID := ""
		if oldItem, ok := reusable[key]; ok && AssetService.GetByAssetIDInTenant(oldItem.AssetID, instance.TenantID) != nil {
			assetID = oldItem.AssetID
		} else {
			asset, err := AssetService.UploadBytes(imageItem.Data, fmt.Sprintf("knowledge-resources/%d/%d", knowledgeBase.ID, instance.StoreID), imageItem.Filename, operator)
			if err != nil {
				s.cleanupUnreferencedAssets(createdAssetIDs, operator)
				return nil, err
			}
			assetID = asset.AssetID
			createdAssetIDs = append(createdAssetIDs, assetID)
		}
		items = append(items, models.KnowledgeResourceItem{
			TenantID:       instance.TenantID,
			AssetID:        assetID,
			SourceURL:      imageItem.SourceURL,
			SourceChecksum: imageItem.Checksum,
			Title:          imageItem.Title,
			Description:    imageItem.Description,
			SortNo:         imageItem.SortNo,
			Status:         enums.StatusOk,
			AuditFields:    utils.BuildAuditFields(operator),
		})
	}

	sourceHash := hashKnowledgeResourceSource(source, images)
	now := time.Now()
	group := existing
	err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if group == nil {
			group = &models.KnowledgeResourceGroup{
				TenantID:         instance.TenantID,
				CompanyID:        instance.CompanyID,
				StoreID:          instance.StoreID,
				IntentProfileID:  0,
				KnowledgeBaseID:  knowledgeBase.ID,
				WxWorkInstanceID: 0,
				SourceProvider:   knowledgeResourceProviderFastGPT,
				SourceRecordID:   source.SourceRecordID,
				Title:            source.Title,
				Description:      source.Description,
				SourceHash:       sourceHash,
				Status:           enums.StatusOk,
				AuditFields:      utils.BuildAuditFields(operator),
			}
			if err := repositories.KnowledgeResourceGroupRepository.Create(tx.Tx, group); err != nil {
				return err
			}
		} else {
			if err := repositories.KnowledgeResourceGroupRepository.UpdatesInTenant(tx.Tx, group.ID, instance.TenantID, map[string]any{
				"title":            source.Title,
				"description":      source.Description,
				"source_hash":      sourceHash,
				"status":           enums.StatusOk,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
			if err := repositories.KnowledgeResourceItemRepository.DeleteByGroupIDInTenant(tx.Tx, group.ID, instance.TenantID); err != nil {
				return err
			}
		}
		for index := range items {
			items[index].KnowledgeResourceGroupID = group.ID
			if err := repositories.KnowledgeResourceItemRepository.Create(tx.Tx, &items[index]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.cleanupUnreferencedAssets(createdAssetIDs, operator)
		return nil, err
	}
	if existing != nil {
		s.cleanupReplacedKnowledgeResourceAssets(oldItems, items, operator)
	}
	return repositories.KnowledgeResourceGroupRepository.GetInTenant(sqls.DB(), group.ID, instance.TenantID), nil
}

func (s *knowledgeResourceService) downloadTrustedImages(ctx context.Context, allowedHosts []string, resources []rag.FastGPTSyncResource) ([]downloadedKnowledgeResourceImage, error) {
	ret := make([]downloadedKnowledgeResourceImage, 0, len(resources))
	seen := map[string]bool{}
	for index, resource := range resources {
		sourceURL := strings.TrimSpace(resource.SourceURL)
		if sourceURL == "" || seen[sourceURL] {
			continue
		}
		seen[sourceURL] = true
		data, filename, checksum, err := s.downloadTrustedImage(ctx, sourceURL, allowedHosts)
		if err != nil {
			return nil, err
		}
		sortNo := resource.SortNo
		if sortNo <= 0 {
			sortNo = index + 1
		}
		ret = append(ret, downloadedKnowledgeResourceImage{
			SourceURL:   sourceURL,
			Title:       strings.TrimSpace(resource.Title),
			Description: strings.TrimSpace(resource.Description),
			SortNo:      sortNo,
			Data:        data,
			Filename:    filename,
			Checksum:    checksum,
		})
	}
	if len(ret) == 0 {
		return nil, errorsx.InvalidParam("没有可同步的图片资源")
	}
	sort.SliceStable(ret, func(i, j int) bool { return ret[i].SortNo < ret[j].SortNo })
	return ret, nil
}

func (s *knowledgeResourceService) downloadTrustedImage(ctx context.Context, rawURL string, allowedHosts []string) ([]byte, string, string, error) {
	parsed, err := validateTrustedKnowledgeResourceURL(rawURL, allowedHosts)
	if err != nil {
		return nil, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", "", fmt.Errorf("下载知识图片失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxKnowledgeResourceImageBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) == 0 || len(data) > maxKnowledgeResourceImageBytes {
		return nil, "", "", errorsx.InvalidParam("知识图片为空或超过 12MB 限制")
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mimeType == "" {
		mimeType = strings.ToLower(http.DetectContentType(data))
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" && mimeType != "image/gif" {
		return nil, "", "", errorsx.InvalidParam("知识资源只支持 JPEG、PNG 或 GIF 图片")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > maxKnowledgeResourceImagePixels {
		return nil, "", "", errorsx.InvalidParam("知识图片尺寸不合法")
	}
	sum := sha256.Sum256(data)
	return data, normalizeKnowledgeResourceFilename(parsed.Path, mimeType), hex.EncodeToString(sum[:]), nil
}

func validateTrustedKnowledgeResourceURL(rawURL string, allowedHosts []string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, errorsx.InvalidParam("知识图片 URL 不合法")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	allowed := false
	for _, item := range allowedHosts {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == host {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, errorsx.InvalidParam("知识图片域名不在知识库可信域名列表中")
	}
	if err := rejectPrivateKnowledgeResourceHost(host); err != nil {
		return nil, err
	}
	return parsed, nil
}

func rejectPrivateKnowledgeResourceHost(host string) error {
	if parsed, err := netip.ParseAddr(host); err == nil {
		if !parsed.IsGlobalUnicast() || parsed.IsPrivate() {
			return errorsx.InvalidParam("知识图片地址不能指向内网或本机")
		}
		return nil
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return errorsx.InvalidParam("知识图片域名无法解析")
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address)
		if !ok || !parsed.IsGlobalUnicast() || parsed.IsPrivate() {
			return errorsx.InvalidParam("知识图片域名解析到了内网或本机地址")
		}
	}
	return nil
}

func knowledgeResourceAllowedHosts(raw string) []string {
	config := struct {
		ResourceAllowedHosts []string `json:"resourceAllowedHosts"`
	}{}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &config) != nil {
		return nil
	}
	ret := make([]string, 0, len(config.ResourceAllowedHosts))
	seen := map[string]bool{}
	for _, host := range config.ResourceAllowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimSuffix(host, "/")
		if host == "" || strings.Contains(host, "/") || seen[host] {
			continue
		}
		seen[host] = true
		ret = append(ret, host)
	}
	return ret
}

func hashKnowledgeResourceSource(source rag.FastGPTSyncSource, images []downloadedKnowledgeResourceImage) string {
	parts := []string{strings.TrimSpace(source.SourceRecordID)}
	for _, item := range images {
		parts = append(parts, item.SourceURL+":"+item.Checksum)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func normalizeKnowledgeResourceFilename(rawPath string, mimeType string) string {
	filename := strings.TrimSpace(path.Base(rawPath))
	if filename == "" || filename == "." || filename == "/" {
		filename = "knowledge-image"
	}
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".gif") {
		return filename
	}
	switch mimeType {
	case "image/jpeg":
		return filename + ".jpg"
	case "image/png":
		return filename + ".png"
	default:
		return filename + ".gif"
	}
}

func (s *knowledgeResourceService) cleanupReplacedKnowledgeResourceAssets(previous []models.KnowledgeResourceItem, current []models.KnowledgeResourceItem, operator *dto.AuthPrincipal) {
	currentAssetIDs := map[string]bool{}
	for _, item := range current {
		currentAssetIDs[item.AssetID] = true
	}
	oldAssetIDs := make([]string, 0, len(previous))
	for _, item := range previous {
		if item.AssetID != "" && !currentAssetIDs[item.AssetID] {
			oldAssetIDs = append(oldAssetIDs, item.AssetID)
		}
	}
	s.cleanupUnreferencedAssets(oldAssetIDs, operator)
}

func (s *knowledgeResourceService) cleanupUnreferencedAssets(assetIDs []string, operator *dto.AuthPrincipal) {
	tenantID := int64(0)
	if operator != nil {
		tenantID = operator.ActiveTenantID
	}
	if tenantID <= 0 {
		return
	}
	seen := map[string]bool{}
	for _, assetID := range assetIDs {
		assetID = strings.TrimSpace(assetID)
		if assetID == "" || seen[assetID] {
			continue
		}
		seen[assetID] = true
		if len(repositories.KnowledgeResourceItemRepository.FindByAssetIDInTenant(sqls.DB(), assetID, tenantID)) > 0 {
			continue
		}
		if asset := AssetService.GetByAssetIDInTenant(assetID, tenantID); asset != nil {
			_ = AssetService.DeleteAsset(asset.ID, operator)
		}
	}
}

func assetIDsFromKnowledgeResourceItems(items []models.KnowledgeResourceItem) []string {
	ret := make([]string, 0, len(items))
	for _, item := range items {
		ret = append(ret, item.AssetID)
	}
	return ret
}

func isFastGPTKnowledgeBaseForResources(item *models.KnowledgeBase) bool {
	return item != nil && (item.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) || item.ChunkProvider == string(enums.KnowledgeChunkProviderFastGPT))
}

func (s *knowledgeResourceService) canAccessWxWorkInstance(operator *dto.AuthPrincipal, instanceID int64) bool {
	if operator == nil || instanceID <= 0 {
		return false
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return true
	}
	for _, allowedID := range scope.WxWorkInstanceIDs {
		if allowedID == instanceID {
			return true
		}
	}
	return false
}
