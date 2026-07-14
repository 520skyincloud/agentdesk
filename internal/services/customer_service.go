package services

import (
	"crypto/md5"
	"encoding/hex"
	"log/slog"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var CustomerService = newCustomerService()

func newCustomerService() *customerService {
	return &customerService{}
}

type customerService struct {
}

type CustomerPresentationData struct {
	CompaniesByID              map[int64]*models.Company
	StoreRelationsByCustomerID map[int64][]models.StoreCustomerRelation
	StoresByID                 map[int64]*models.Store
	WxWorkInstancesByID        map[int64]*models.WxWorkProtocolInstance
}

func (s *customerService) Get(id int64) *models.Customer {
	return repositories.CustomerRepository.Get(sqls.DB(), id)
}

func (s *customerService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.Customer {
	return s.GetByTenantID(id, customerTenantID(operator))
}

func (s *customerService) GetByTenantID(id, tenantID int64) *models.Customer {
	return repositories.CustomerRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *customerService) Take(where ...interface{}) *models.Customer {
	return repositories.CustomerRepository.Take(sqls.DB(), where...)
}

func (s *customerService) Find(cnd *sqls.Cnd) []models.Customer {
	return repositories.CustomerRepository.Find(sqls.DB(), cnd)
}

func (s *customerService) FindOne(cnd *sqls.Cnd) *models.Customer {
	return repositories.CustomerRepository.FindOne(sqls.DB(), cnd)
}

func (s *customerService) FindPageByParams(params *params.QueryParams) (list []models.Customer, paging *sqls.Paging) {
	return repositories.CustomerRepository.FindPageByParams(sqls.DB(), params)
}

func (s *customerService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Customer, paging *sqls.Paging) {
	return repositories.CustomerRepository.FindPageByCnd(sqls.DB(), cnd)
}

// ListCustomers 客户分页列表（连联系方式表，支持按非主联系方式检索）。
func (s *customerService) ListCustomers(req request.CustomerListRequest, operator *dto.AuthPrincipal) (list []models.Customer, paging *sqls.Paging) {
	tenantID := customerTenantID(operator)
	if tenantID <= 0 {
		return []models.Customer{}, &sqls.Paging{Page: req.GetPage(), Limit: req.GetLimit(), Total: 0}
	}
	if err := s.newCustomerListQuery(req, tenantID).Distinct("c.*").Offset(req.Offset()).Order("c.id DESC").Limit(req.GetLimit()).Scan(&list).Error; err != nil {
		slog.Error("customer list scan failed", slog.Any("error", err))
	}

	var total int64
	if err := s.newCustomerListQuery(req, tenantID).Distinct("c.id").Count(&total).Error; err != nil {
		slog.Error("customer list count failed", slog.Any("error", err))
	}

	paging = &sqls.Paging{
		Page:  req.GetPage(),
		Limit: req.GetLimit(),
		Total: total,
	}
	return
}

func (s *customerService) newCustomerListQuery(req request.CustomerListRequest, tenantID int64) *gorm.DB {
	deleted := int(enums.StatusDeleted)
	tx := sqls.DB().
		Table("t_customer AS c").
		Joins("LEFT JOIN t_customer_contact AS cc ON cc.customer_id = c.id AND cc.tenant_id = c.tenant_id AND cc.status <> ?", deleted).
		Joins("LEFT JOIN t_company AS co ON co.id = c.company_id AND co.tenant_id = c.tenant_id")

	tx.Where("c.tenant_id = ? AND c.status <> ?", tenantID, enums.StatusDeleted)

	if req.Status != nil {
		tx.Where("c.status = ?", *req.Status)
	}
	if req.Gender != nil {
		tx.Where("c.gender = ?", *req.Gender)
	}
	if req.CompanyID != nil && *req.CompanyID > 0 {
		tx.Where("c.company_id = ?", *req.CompanyID)
	}
	if kw := strings.TrimSpace(req.Keyword); strs.IsNotBlank(kw) {
		pat := "%" + kw + "%"
		tx.Where(`(
c.name LIKE ? OR
c.primary_mobile LIKE ? OR
c.primary_email LIKE ? OR
cc.contact_value LIKE ? OR
co.name LIKE ?
)`, pat, pat, pat, pat, pat)
	}
	return tx
}

func (s *customerService) Count(cnd *sqls.Cnd) int64 {
	return repositories.CustomerRepository.Count(sqls.DB(), cnd)
}

func (s *customerService) CountByCompanyIDs(companyIDs []int64, operator *dto.AuthPrincipal) map[int64]int64 {
	return repositories.CustomerRepository.CountByCompanyIDsInTenant(sqls.DB(), companyIDs, customerTenantID(operator), int(enums.StatusDeleted))
}

func (s *customerService) LoadPresentationData(customers []models.Customer, includeStoreRelations bool) CustomerPresentationData {
	data := CustomerPresentationData{
		CompaniesByID:              map[int64]*models.Company{},
		StoreRelationsByCustomerID: map[int64][]models.StoreCustomerRelation{},
		StoresByID:                 map[int64]*models.Store{},
		WxWorkInstancesByID:        map[int64]*models.WxWorkProtocolInstance{},
	}
	if len(customers) == 0 {
		return data
	}
	companyIDs := make([]int64, 0, len(customers))
	customerIDs := make([]int64, 0, len(customers))
	tenantIDs := make([]int64, 0, len(customers))
	companyTenantByID := make(map[int64]int64, len(customers))
	customerTenantByID := make(map[int64]int64, len(customers))
	for i := range customers {
		companyIDs = appendPositive(companyIDs, customers[i].CompanyID)
		customerIDs = appendPositive(customerIDs, customers[i].ID)
		tenantIDs = appendPositive(tenantIDs, customers[i].TenantID)
		recordExpectedTenant(companyTenantByID, customers[i].CompanyID, customers[i].TenantID)
		recordExpectedTenant(customerTenantByID, customers[i].ID, customers[i].TenantID)
	}
	tenantIDs = uniquePositive(tenantIDs)
	companyIDs = uniquePositive(companyIDs)
	if len(companyIDs) > 0 {
		companies := repositories.CompanyRepository.Find(sqls.DB(), sqls.NewCnd().In("id", companyIDs).In("tenant_id", tenantIDs))
		for i := range companies {
			if companyTenantByID[companies[i].ID] == companies[i].TenantID {
				data.CompaniesByID[companies[i].ID] = &companies[i]
			}
		}
	}
	if !includeStoreRelations || len(customerIDs) == 0 {
		return data
	}
	relations := repositories.StoreCustomerRelationRepository.Find(sqls.DB(), sqls.NewCnd().
		In("customer_id", uniquePositive(customerIDs)).
		In("tenant_id", tenantIDs).
		NotEq("status", enums.StatusDeleted).
		Desc("last_active_at").
		Desc("id"))
	storeIDs := make([]int64, 0, len(relations))
	instanceIDs := make([]int64, 0, len(relations))
	storeTenantByID := make(map[int64]int64, len(relations))
	instanceTenantByID := make(map[int64]int64, len(relations))
	for i := range relations {
		relation := relations[i]
		if customerTenantByID[relation.CustomerID] != relation.TenantID {
			continue
		}
		data.StoreRelationsByCustomerID[relation.CustomerID] = append(data.StoreRelationsByCustomerID[relation.CustomerID], relation)
		storeIDs = appendPositive(storeIDs, relation.StoreID)
		instanceIDs = appendPositive(instanceIDs, relation.WxWorkInstanceID)
		recordExpectedTenant(storeTenantByID, relation.StoreID, relation.TenantID)
		recordExpectedTenant(instanceTenantByID, relation.WxWorkInstanceID, relation.TenantID)
	}
	if storeIDs = uniquePositive(storeIDs); len(storeIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().In("id", storeIDs).In("tenant_id", tenantIDs))
		for i := range stores {
			if storeTenantByID[stores[i].ID] == stores[i].TenantID {
				data.StoresByID[stores[i].ID] = &stores[i]
			}
		}
	}
	if instanceIDs = uniquePositive(instanceIDs); len(instanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().In("id", instanceIDs).In("tenant_id", tenantIDs))
		for i := range instances {
			if instanceTenantByID[instances[i].ID] == instances[i].TenantID {
				data.WxWorkInstancesByID[instances[i].ID] = &instances[i]
			}
		}
	}
	return data
}

func recordExpectedTenant(expected map[int64]int64, id, tenantID int64) {
	if id <= 0 || tenantID <= 0 {
		return
	}
	current, exists := expected[id]
	if !exists {
		expected[id] = tenantID
		return
	}
	if current != tenantID {
		expected[id] = 0
	}
}

func (s *customerService) EnsureExternalCustomer(ctx *sqls.TxContext, tenantID int64, externalUser openidentity.ExternalUser) (int64, error) {
	if ctx == nil || ctx.Tx == nil {
		return 0, errorsx.InvalidParam("事务上下文不能为空")
	}
	if tenantID <= 0 {
		return 0, errorsx.InvalidParam("接入渠道缺少租户归属")
	}
	externalSource := externalUser.ExternalSource
	externalID := strings.TrimSpace(externalUser.ExternalID)
	if strings.TrimSpace(string(externalSource)) == "" || externalID == "" {
		return 0, errorsx.Unauthorized("外部用户标识不能为空")
	}
	now := time.Now()
	if identity := repositories.CustomerIdentityRepository.GetByInTenant(ctx.Tx, tenantID, externalSource, externalID); identity != nil {
		updates := map[string]any{
			"last_active_at": now,
			"updated_at":     now,
		}
		if strs.IsNotBlank(externalUser.ExternalName) {
			updates["name"] = externalUser.ExternalName
		}
		if err := repositories.CustomerRepository.UpdatesInTenant(ctx.Tx, identity.CustomerID, tenantID, updates); err != nil {
			return 0, err
		}

		ctx.RegisterCallback(func() {
			if strs.IsNotBlank(externalUser.ExternalName) {
				if err := s.syncConversationCustomerName(sqls.DB(), identity.CustomerID, externalUser.ExternalName, nil, now); err != nil {
					slog.Error("sync conversation customer name failed",
						"customerId", identity.CustomerID,
						"customerName", externalUser.ExternalName,
						"error", err,
					)
				}
			}
		})
		return identity.CustomerID, nil
	}

	customer := &models.Customer{
		TenantID:     tenantID,
		Name:         buildExternalCustomerName(externalUser),
		LastActiveAt: &now,
		Status:       enums.StatusOk,
		AuditFields:  utils.BuildAuditFields(nil),
	}
	if err := repositories.CustomerRepository.Create(ctx.Tx, customer); err != nil {
		return 0, err
	}
	if err := repositories.CustomerIdentityRepository.Create(ctx.Tx, &models.CustomerIdentity{
		TenantID:       tenantID,
		CustomerID:     customer.ID,
		ExternalSource: externalSource,
		ExternalID:     externalID,
		Status:         enums.StatusOk,
		AuditFields:    utils.BuildAuditFields(nil),
	}); err != nil {
		return 0, err
	}
	return customer.ID, nil
}

func buildExternalCustomerName(externalUser openidentity.ExternalUser) string {
	if strs.IsNotBlank(externalUser.ExternalName) {
		return externalUser.ExternalName
	}
	return "访客" + hashUUID(externalUser.ExternalID)
}

func hashUUID(uuid string) string {
	if uuid == "" {
		return "unknown"
	}

	h := md5.Sum([]byte(uuid))
	return hex.EncodeToString(h[:])[:8]
}

func (s *customerService) TouchStoreRelation(customerID, storeID, wxWorkInstanceID, conversationID int64, at time.Time) error {
	if customerID <= 0 || storeID <= 0 {
		return nil
	}
	customer := s.Get(customerID)
	if customer == nil || customer.TenantID <= 0 {
		return errorsx.InvalidParam("客户不存在或缺少租户归属")
	}
	existing := repositories.StoreCustomerRelationRepository.Take(sqls.DB(), "tenant_id = ? AND customer_id = ? AND store_id = ?", customer.TenantID, customerID, storeID)
	if existing == nil {
		return repositories.StoreCustomerRelationRepository.Create(sqls.DB(), &models.StoreCustomerRelation{
			TenantID:           customer.TenantID,
			CustomerID:         customerID,
			StoreID:            storeID,
			WxWorkInstanceID:   wxWorkInstanceID,
			LastConversationID: conversationID,
			LastActiveAt:       &at,
			VisitCount:         1,
			Status:             enums.StatusOk,
			AuditFields:        utils.BuildAuditFields(nil),
		})
	}
	return repositories.StoreCustomerRelationRepository.UpdatesInTenant(sqls.DB(), existing.ID, customer.TenantID, map[string]any{
		"wx_work_instance_id":  wxWorkInstanceID,
		"last_conversation_id": conversationID,
		"last_active_at":       at,
		"visit_count":          existing.VisitCount + 1,
		"updated_at":           at,
		"update_user_id":       int64(0),
		"update_user_name":     "system",
	})
}

func (s *customerService) ListStoreRelations(customerID int64) []models.StoreCustomerRelation {
	if customerID <= 0 {
		return nil
	}
	return repositories.StoreCustomerRelationRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("customer_id", customerID).
		NotEq("status", enums.StatusDeleted).
		Desc("last_active_at").
		Desc("id"))
}

func (s *customerService) CreateCustomer(req request.CreateCustomerRequest, operator *dto.AuthPrincipal) (*models.Customer, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := customerTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理客户的接入公司")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("客户名称不能为空")
	}

	if req.CompanyID > 0 {
		company := CompanyService.GetInTenant(req.CompanyID, operator)
		if company == nil {
			return nil, errorsx.InvalidParam("所属公司不存在")
		}
	}

	item := &models.Customer{
		TenantID:      tenantID,
		Name:          name,
		Gender:        enums.Gender(req.Gender),
		CompanyID:     req.CompanyID,
		PrimaryMobile: strings.TrimSpace(req.PrimaryMobile),
		PrimaryEmail:  strings.TrimSpace(req.PrimaryEmail),
		Status:        enums.StatusOk,
		Remark:        strings.TrimSpace(req.Remark),
		AuditFields:   utils.BuildAuditFields(operator),
	}

	if err := repositories.CustomerRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *customerService) UpdateCustomer(req request.UpdateCustomerRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(req.ID, operator)
	if item == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errorsx.InvalidParam("客户名称不能为空")
	}

	if req.CompanyID > 0 {
		company := CompanyService.GetInTenant(req.CompanyID, operator)
		if company == nil {
			return errorsx.InvalidParam("所属公司不存在")
		}
	}

	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		if err := repositories.CustomerRepository.UpdatesInTenant(ctx.Tx, req.ID, item.TenantID, map[string]any{
			"name":             name,
			"gender":           req.Gender,
			"company_id":       req.CompanyID,
			"primary_mobile":   strings.TrimSpace(req.PrimaryMobile),
			"primary_email":    strings.TrimSpace(req.PrimaryEmail),
			"remark":           strings.TrimSpace(req.Remark),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		return s.syncConversationCustomerName(ctx.Tx, req.ID, name, operator, now)
	})
}

func (s *customerService) DeleteCustomer(id int64, operator dto.AuthPrincipal) error {
	item := s.GetInTenant(id, &operator)
	if item == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	return repositories.CustomerRepository.UpdatesInTenant(sqls.DB(), id, item.TenantID, map[string]any{
		"status":           enums.StatusDeleted,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *customerService) syncConversationCustomerName(db *gorm.DB, customerID int64, name string, operator *dto.AuthPrincipal, now time.Time) error {
	if customerID <= 0 {
		return nil
	}
	updates := map[string]any{
		"customer_name": name,
		"updated_at":    now,
	}
	if operator != nil {
		updates["update_user_id"] = operator.UserID
		updates["update_user_name"] = operator.Username
	}
	return repositories.ConversationRepository.UpdatesByCustomerID(db, customerID, updates)
}

func (s *customerService) UpdateStatus(id int64, status int, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(id, operator)
	if item == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return errorsx.InvalidParam("状态值不合法")
	}
	return repositories.CustomerRepository.UpdatesInTenant(sqls.DB(), id, item.TenantID, map[string]any{
		"status":           status,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

// SaveCustomerProfile 单事务保存客户主信息与联系方式全量（新建或更新）。
func (s *customerService) SaveCustomerProfile(req request.SaveCustomerProfileRequest, operator *dto.AuthPrincipal) (*models.Customer, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := customerTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理客户的接入公司")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("客户名称不能为空")
	}
	if req.CompanyID > 0 {
		if CompanyService.GetInTenant(req.CompanyID, operator) == nil {
			return nil, errorsx.InvalidParam("所属公司不存在")
		}
	}
	createMode := req.ID == nil || *req.ID <= 0

	var out *models.Customer
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var customerID int64
		if createMode {
			c := &models.Customer{
				TenantID:      tenantID,
				Name:          name,
				Gender:        enums.Gender(req.Gender),
				CompanyID:     req.CompanyID,
				PrimaryMobile: "",
				PrimaryEmail:  "",
				Status:        enums.StatusOk,
				Remark:        strings.TrimSpace(req.Remark),
				AuditFields:   utils.BuildAuditFields(operator),
			}
			if err := repositories.CustomerRepository.Create(ctx.Tx, c); err != nil {
				return err
			}
			customerID = c.ID
			out = c
		} else {
			customerID = *req.ID
			cur := repositories.CustomerRepository.GetInTenant(ctx.Tx, customerID, tenantID)
			if cur == nil {
				return errorsx.InvalidParam("客户不存在")
			}
			now := time.Now()
			if err := repositories.CustomerRepository.UpdatesInTenant(ctx.Tx, customerID, tenantID, map[string]any{
				"name":             name,
				"gender":           req.Gender,
				"company_id":       req.CompanyID,
				"remark":           strings.TrimSpace(req.Remark),
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
			if err := s.syncConversationCustomerName(ctx.Tx, customerID, name, operator, now); err != nil {
				return err
			}
			out = repositories.CustomerRepository.GetInTenant(ctx.Tx, customerID, tenantID)
		}
		return CustomerContactService.ReplaceAllForCustomerInTx(ctx, customerID, tenantID, req.Contacts, operator)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func customerTenantID(operator *dto.AuthPrincipal) int64 {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return 0
	}
	return operator.ActiveTenantID
}
