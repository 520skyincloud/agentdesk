package services

import (
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var CustomerContactService = newCustomerContactService()

func newCustomerContactService() *customerContactService {
	return &customerContactService{}
}

type customerContactService struct {
}

func (s *customerContactService) Get(id int64) *models.CustomerContact {
	return repositories.CustomerContactRepository.Get(sqls.DB(), id)
}

func (s *customerContactService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.CustomerContact {
	return repositories.CustomerContactRepository.GetInTenant(sqls.DB(), id, customerTenantID(operator))
}

func (s *customerContactService) Take(where ...interface{}) *models.CustomerContact {
	return repositories.CustomerContactRepository.Take(sqls.DB(), where...)
}

func (s *customerContactService) Find(cnd *sqls.Cnd) []models.CustomerContact {
	return repositories.CustomerContactRepository.Find(sqls.DB(), cnd)
}

func (s *customerContactService) FindOne(cnd *sqls.Cnd) *models.CustomerContact {
	return repositories.CustomerContactRepository.FindOne(sqls.DB(), cnd)
}

func (s *customerContactService) FindPageByParams(params *params.QueryParams) (list []models.CustomerContact, paging *sqls.Paging) {
	return repositories.CustomerContactRepository.FindPageByParams(sqls.DB(), params)
}

func (s *customerContactService) FindPageByCnd(cnd *sqls.Cnd) (list []models.CustomerContact, paging *sqls.Paging) {
	return repositories.CustomerContactRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *customerContactService) Count(cnd *sqls.Cnd) int64 {
	return repositories.CustomerContactRepository.Count(sqls.DB(), cnd)
}

func (s *customerContactService) Create(t *models.CustomerContact) error {
	return repositories.CustomerContactRepository.Create(sqls.DB(), t)
}

func (s *customerContactService) Update(t *models.CustomerContact) error {
	return repositories.CustomerContactRepository.Update(sqls.DB(), t)
}

func (s *customerContactService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.CustomerContactRepository.Updates(sqls.DB(), id, columns)
}

func (s *customerContactService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.CustomerContactRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *customerContactService) Delete(id int64) {
	repositories.CustomerContactRepository.Delete(sqls.DB(), id)
}

// FindActiveByCustomerID 返回某客户下未删除的联系方式列表。
func (s *customerContactService) FindActiveByCustomerID(customerID int64, operator *dto.AuthPrincipal) []models.CustomerContact {
	tenantID := customerTenantID(operator)
	if customerID <= 0 || tenantID <= 0 {
		return nil
	}
	cnd := sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id")
	return repositories.CustomerContactRepository.Find(sqls.DB(), cnd)
}

func normalizeContactSource(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "manual"
	}
	return v
}

func (s *customerContactService) hasDuplicateContact(
	db *gorm.DB,
	customerID int64,
	tenantID int64,
	contactType enums.ContactType,
	contactValue string,
	excludeID int64,
) bool {
	cnd := sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("contact_type = ?", contactType).
		Where("contact_value = ?", contactValue).
		Where("status <> ?", enums.StatusDeleted)
	if excludeID > 0 {
		cnd = cnd.Where("id <> ?", excludeID)
	}
	return repositories.CustomerContactRepository.FindOne(db, cnd) != nil
}

// findSoftDeletedContactByNaturalKey 按 uk_customer_contact 业务键查找已软删行；复活时用 UPDATE 代替 INSERT，避免唯一索引冲突。
func (s *customerContactService) findSoftDeletedContactByNaturalKey(
	db *gorm.DB,
	customerID int64,
	tenantID int64,
	contactType enums.ContactType,
	contactValue string,
) *models.CustomerContact {
	cnd := sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("contact_type = ?", contactType).
		Where("contact_value = ?", contactValue).
		Where("status = ?", enums.StatusDeleted)
	return repositories.CustomerContactRepository.FindOne(db, cnd)
}

// syncCustomerPrimaryFromContacts 根据当前主联系方式更新客户表冗余字段（列表检索用）。
func (s *customerContactService) syncCustomerPrimaryFromContacts(db *gorm.DB, customerID, tenantID int64) error {
	if customerID <= 0 || tenantID <= 0 {
		return nil
	}
	if repositories.CustomerRepository.GetInTenant(db, customerID, tenantID) == nil {
		return nil
	}
	cnd := sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("is_primary = ?", true).
		Where("status <> ?", enums.StatusDeleted)
	primary := repositories.CustomerContactRepository.FindOne(db, cnd)
	pm, pe := "", ""
	if primary != nil {
		val := strings.TrimSpace(primary.ContactValue)
		switch primary.ContactType {
		case enums.ContactTypeEmail:
			pe = val
		default:
			pm = val
		}
	}
	return repositories.CustomerRepository.UpdatesInTenant(db, customerID, tenantID, map[string]any{
		"primary_mobile": pm,
		"primary_email":  pe,
		"updated_at":     time.Now(),
	})
}

// ReplaceAllForCustomerInTx 在事务内全量替换客户联系方式（软删未出现在 payload 中的记录），并同步客户主联系方式冗余字段。
func (s *customerContactService) ReplaceAllForCustomerInTx(
	ctx *sqls.TxContext,
	customerID int64,
	tenantID int64,
	raw []request.CustomerProfileContactItem,
	operator *dto.AuthPrincipal,
) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if tenantID <= 0 || tenantID != customerTenantID(operator) || repositories.CustomerRepository.GetInTenant(ctx.Tx, customerID, tenantID) == nil {
		return errorsx.InvalidParam("客户不存在")
	}
	type line struct {
		id      *int64
		ct      enums.ContactType
		val     string
		remark  string
		primary bool
	}
	var items []line
	for _, r := range raw {
		ct := strings.TrimSpace(r.ContactType)
		val := strings.TrimSpace(r.ContactValue)
		if val == "" {
			continue
		}
		if !enums.IsValidContactType(ct) {
			return errorsx.InvalidParam("联系方式类型不合法")
		}
		items = append(items, line{
			id:      r.ID,
			ct:      enums.ContactType(ct),
			val:     val,
			remark:  strings.TrimSpace(r.Remark),
			primary: r.IsPrimary,
		})
	}
	if len(items) > 0 {
		primaryCount := 0
		for i := range items {
			if items[i].primary {
				primaryCount++
			}
		}
		if primaryCount == 0 {
			items[0].primary = true
		} else if primaryCount > 1 {
			return errorsx.InvalidParam("仅能指定一条主联系方式")
		}
	}

	existing := repositories.CustomerContactRepository.Find(ctx.Tx, sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))

	wantIDs := map[int64]struct{}{}
	for i := range items {
		if items[i].id != nil && *items[i].id > 0 {
			wantIDs[*items[i].id] = struct{}{}
		}
	}
	now := time.Now()
	for _, ex := range existing {
		if _, ok := wantIDs[ex.ID]; !ok {
			if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, ex.ID, tenantID, map[string]any{
				"status":           enums.StatusDeleted,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		return !items[i].primary && items[j].primary
	})

	for _, it := range items {
		if it.id != nil && *it.id > 0 {
			row := repositories.CustomerContactRepository.GetInTenant(ctx.Tx, *it.id, tenantID)
			if row == nil || row.CustomerID != customerID || row.Status == enums.StatusDeleted {
				return errorsx.InvalidParam("联系方式不存在")
			}
			if s.hasDuplicateContact(ctx.Tx, customerID, tenantID, it.ct, it.val, *it.id) {
				return errorsx.InvalidParam("该联系方式已存在")
			}
			if it.primary {
				if err := s.clearPrimaryExcept(ctx.Tx, customerID, tenantID, *it.id); err != nil {
					return err
				}
			}
			if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, *it.id, tenantID, map[string]any{
				"contact_type":     it.ct,
				"contact_value":    it.val,
				"is_primary":       it.primary,
				"remark":           it.remark,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
			continue
		}
		if s.hasDuplicateContact(ctx.Tx, customerID, tenantID, it.ct, it.val, 0) {
			return errorsx.InvalidParam("该联系方式已存在")
		}
		if deleted := s.findSoftDeletedContactByNaturalKey(ctx.Tx, customerID, tenantID, it.ct, it.val); deleted != nil {
			if it.primary {
				if err := s.clearPrimaryExcept(ctx.Tx, customerID, tenantID, deleted.ID); err != nil {
					return err
				}
			}
			if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, deleted.ID, tenantID, map[string]any{
				"status":           enums.StatusOk,
				"contact_type":     it.ct,
				"contact_value":    it.val,
				"is_primary":       it.primary,
				"is_verified":      false,
				"verified_at":      nil,
				"remark":           it.remark,
				"source":           normalizeContactSource("manual"),
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
			continue
		}
		if it.primary {
			if err := s.clearPrimaryExcept(ctx.Tx, customerID, tenantID, 0); err != nil {
				return err
			}
		}
		item := &models.CustomerContact{
			TenantID:     tenantID,
			CustomerID:   customerID,
			ContactType:  it.ct,
			ContactValue: it.val,
			IsPrimary:    it.primary,
			IsVerified:   false,
			Source:       normalizeContactSource("manual"),
			Status:       enums.StatusOk,
			Remark:       it.remark,
			AuditFields:  utils.BuildAuditFields(operator),
		}
		if err := repositories.CustomerContactRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
	}
	return s.syncCustomerPrimaryFromContacts(ctx.Tx, customerID, tenantID)
}

func (s *customerContactService) clearPrimaryExcept(db *gorm.DB, customerID, tenantID, exceptID int64) error {
	cnd := sqls.NewCnd().
		Where("customer_id = ?", customerID).
		Where("tenant_id = ?", tenantID).
		Where("is_primary = ?", true)
	if exceptID > 0 {
		cnd = cnd.Where("id <> ?", exceptID)
	}
	list := repositories.CustomerContactRepository.Find(db, cnd)
	for i := range list {
		if err := repositories.CustomerContactRepository.UpdateColumnInTenant(db, list[i].ID, tenantID, "is_primary", false); err != nil {
			return err
		}
	}
	return nil
}

func (s *customerContactService) validateContactStatus(status int) error {
	if !enums.IsValidStatus(status) {
		return errorsx.InvalidParam("状态值不合法")
	}
	if status == int(enums.StatusDeleted) {
		return errorsx.InvalidParam("状态值不合法")
	}
	return nil
}

// CreateCustomerContact 创建联系方式；主联系方式在同一客户下唯一。
func (s *customerContactService) CreateCustomerContact(req request.CreateCustomerContactRequest, operator *dto.AuthPrincipal) (*models.CustomerContact, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := customerTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理客户的接入公司")
	}
	if req.CustomerID <= 0 {
		return nil, errorsx.InvalidParam("客户不存在")
	}
	if CustomerService.GetInTenant(req.CustomerID, operator) == nil {
		return nil, errorsx.InvalidParam("客户不存在")
	}
	ct := strings.TrimSpace(req.ContactType)
	if !enums.IsValidContactType(ct) {
		return nil, errorsx.InvalidParam("联系方式类型不合法")
	}
	val := strings.TrimSpace(req.ContactValue)
	if val == "" {
		return nil, errorsx.InvalidParam("联系方式不能为空")
	}
	if err := s.validateContactStatus(req.Status); err != nil {
		return nil, err
	}
	status := enums.Status(req.Status)
	if status == 0 {
		status = enums.StatusOk
	}

	var created *models.CustomerContact
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if s.hasDuplicateContact(ctx.Tx, req.CustomerID, tenantID, enums.ContactType(ct), val, 0) {
			return errorsx.InvalidParam("该联系方式已存在")
		}
		now := time.Now()
		if deleted := s.findSoftDeletedContactByNaturalKey(ctx.Tx, req.CustomerID, tenantID, enums.ContactType(ct), val); deleted != nil {
			if req.IsPrimary {
				if err := s.clearPrimaryExcept(ctx.Tx, req.CustomerID, tenantID, deleted.ID); err != nil {
					return err
				}
			}
			var verifiedAt *time.Time
			if req.IsVerified {
				verifiedAt = &now
			}
			if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, deleted.ID, tenantID, map[string]any{
				"status":           status,
				"contact_type":     enums.ContactType(ct),
				"contact_value":    val,
				"is_primary":       req.IsPrimary,
				"is_verified":      req.IsVerified,
				"verified_at":      verifiedAt,
				"source":           normalizeContactSource(req.Source),
				"remark":           strings.TrimSpace(req.Remark),
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       now,
			}); err != nil {
				return err
			}
			created = repositories.CustomerContactRepository.GetInTenant(ctx.Tx, deleted.ID, tenantID)
			return s.syncCustomerPrimaryFromContacts(ctx.Tx, req.CustomerID, tenantID)
		}
		if req.IsPrimary {
			if err := s.clearPrimaryExcept(ctx.Tx, req.CustomerID, tenantID, 0); err != nil {
				return err
			}
		}
		var verifiedAt *time.Time
		if req.IsVerified {
			verifiedAt = &now
		}
		item := &models.CustomerContact{
			TenantID:     tenantID,
			CustomerID:   req.CustomerID,
			ContactType:  enums.ContactType(ct),
			ContactValue: val,
			IsPrimary:    req.IsPrimary,
			IsVerified:   req.IsVerified,
			VerifiedAt:   verifiedAt,
			Source:       normalizeContactSource(req.Source),
			Status:       status,
			Remark:       strings.TrimSpace(req.Remark),
			AuditFields:  utils.BuildAuditFields(operator),
		}
		if err := repositories.CustomerContactRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		created = item
		if err := s.syncCustomerPrimaryFromContacts(ctx.Tx, req.CustomerID, tenantID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateCustomerContact 更新联系方式。
func (s *customerContactService) UpdateCustomerContact(req request.UpdateCustomerContactRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if req.ID <= 0 {
		return errorsx.InvalidParam("联系方式不存在")
	}
	current := s.GetInTenant(req.ID, operator)
	if current == nil {
		return errorsx.InvalidParam("联系方式不存在")
	}
	tenantID := current.TenantID
	ct := strings.TrimSpace(req.ContactType)
	if !enums.IsValidContactType(ct) {
		return errorsx.InvalidParam("联系方式类型不合法")
	}
	val := strings.TrimSpace(req.ContactValue)
	if val == "" {
		return errorsx.InvalidParam("联系方式不能为空")
	}
	if err := s.validateContactStatus(req.Status); err != nil {
		return err
	}

	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if s.hasDuplicateContact(ctx.Tx, current.CustomerID, tenantID, enums.ContactType(ct), val, req.ID) {
			return errorsx.InvalidParam("该联系方式已存在")
		}
		if req.IsPrimary {
			if err := s.clearPrimaryExcept(ctx.Tx, current.CustomerID, tenantID, req.ID); err != nil {
				return err
			}
		}
		now := time.Now()
		verifiedAt := current.VerifiedAt
		if req.IsVerified {
			if verifiedAt == nil {
				verifiedAt = &now
			}
		} else {
			verifiedAt = nil
		}
		if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, req.ID, tenantID, map[string]any{
			"contact_type":     enums.ContactType(ct),
			"contact_value":    val,
			"is_primary":       req.IsPrimary,
			"is_verified":      req.IsVerified,
			"verified_at":      verifiedAt,
			"source":           normalizeContactSource(req.Source),
			"status":           req.Status,
			"remark":           strings.TrimSpace(req.Remark),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		return s.syncCustomerPrimaryFromContacts(ctx.Tx, current.CustomerID, tenantID)
	})
}

// DeleteCustomerContact 软删除联系方式并同步客户主联系方式冗余字段。
func (s *customerContactService) DeleteCustomerContact(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if id <= 0 {
		return errorsx.InvalidParam("联系方式不存在")
	}
	current := s.GetInTenant(id, operator)
	if current == nil {
		return errorsx.InvalidParam("联系方式不存在")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		if err := repositories.CustomerContactRepository.UpdatesInTenant(ctx.Tx, id, current.TenantID, map[string]any{
			"status":           enums.StatusDeleted,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return err
		}
		return s.syncCustomerPrimaryFromContacts(ctx.Tx, current.CustomerID, current.TenantID)
	})
}
