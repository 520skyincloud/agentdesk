package repositories

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var TenantRepository = newTenantRepository()

func newTenantRepository() *tenantRepository {
	return &tenantRepository{}
}

type tenantRepository struct {
}

type TenantOperationalStats struct {
	TenantID                 int64
	AgentCount               int64
	StoreCount               int64
	AgentTeamCount           int64
	LatestConversationActive *time.Time
	LatestUserLogin          *time.Time
}

type tenantAggregateTime struct {
	Time  time.Time
	Valid bool
}

func (t *tenantAggregateTime) Scan(value any) error {
	parsed, err := parseTenantAggregateTime(value)
	if err != nil {
		return err
	}
	if parsed == nil {
		t.Time = time.Time{}
		t.Valid = false
		return nil
	}
	t.Time = *parsed
	t.Valid = true
	return nil
}

func (t tenantAggregateTime) Value() (driver.Value, error) {
	if !t.Valid {
		return nil, nil
	}
	return t.Time, nil
}

func (r *tenantRepository) GetByTenantCode(db *gorm.DB, tenantCode string) *models.Tenant {
	return r.FindOne(db, sqls.NewCnd().Eq("tenant_code", tenantCode))
}

func (r *tenantRepository) GetByRegistration(db *gorm.DB, registrationType, registrationNo string) *models.Tenant {
	return r.FindOne(db, sqls.NewCnd().Eq("registration_type", registrationType).Eq("registration_no", registrationNo))
}

func (r *tenantRepository) Get(db *gorm.DB, id int64) *models.Tenant {
	ret := &models.Tenant{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) GetForUpdate(db *gorm.DB, id int64) (*models.Tenant, error) {
	ret := &models.Tenant{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *tenantRepository) Take(db *gorm.DB, where ...any) *models.Tenant {
	ret := &models.Tenant{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tenant) {
	cnd.Find(db, &list)
	return
}

func (r *tenantRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Tenant {
	ret := &models.Tenant{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Tenant, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *tenantRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tenant, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Tenant{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *tenantRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...any) (list []models.Tenant) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *tenantRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...any) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *tenantRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Tenant{})
}

func (r *tenantRepository) CountByIntentProfile(db *gorm.DB, intentProfileID int64) (int64, error) {
	if db == nil || intentProfileID <= 0 {
		return 0, nil
	}
	var count int64
	err := db.Model(&models.Tenant{}).
		Where("intent_profile_id = ? AND status <> ?", intentProfileID, enums.StatusDeleted).
		Count(&count).Error
	return count, err
}

func (r *tenantRepository) FindOperationalStats(db *gorm.DB, tenantIDs []int64) (map[int64]TenantOperationalStats, error) {
	stats := make(map[int64]TenantOperationalStats, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		stats[tenantID] = TenantOperationalStats{TenantID: tenantID}
	}
	if len(tenantIDs) == 0 {
		return stats, nil
	}

	type countRow struct {
		TenantID int64 `gorm:"column:tenant_id"`
		Count    int64 `gorm:"column:count"`
	}
	var agentRows []countRow
	if err := db.Model(&models.AgentProfile{}).
		Select("tenant_id, COUNT(*) AS count").
		Where("tenant_id IN ? AND status <> ?", tenantIDs, enums.StatusDeleted).
		Group("tenant_id").
		Scan(&agentRows).Error; err != nil {
		return nil, err
	}
	for _, row := range agentRows {
		item := stats[row.TenantID]
		item.AgentCount = row.Count
		stats[row.TenantID] = item
	}

	var storeRows []countRow
	if err := db.Model(&models.Store{}).
		Select("tenant_id, COUNT(*) AS count").
		Where("tenant_id IN ? AND status <> ?", tenantIDs, enums.StatusDeleted).
		Group("tenant_id").
		Scan(&storeRows).Error; err != nil {
		return nil, err
	}
	for _, row := range storeRows {
		item := stats[row.TenantID]
		item.StoreCount = row.Count
		stats[row.TenantID] = item
	}

	var teamRows []countRow
	if err := db.Model(&models.AgentTeam{}).
		Select("tenant_id, COUNT(*) AS count").
		Where("tenant_id IN ? AND status <> ?", tenantIDs, enums.StatusDeleted).
		Group("tenant_id").
		Scan(&teamRows).Error; err != nil {
		return nil, err
	}
	for _, row := range teamRows {
		item := stats[row.TenantID]
		item.AgentTeamCount = row.Count
		stats[row.TenantID] = item
	}

	type activityRow struct {
		TenantID int64               `gorm:"column:tenant_id"`
		ActiveAt tenantAggregateTime `gorm:"column:active_at"`
	}
	var conversationRows []activityRow
	if err := db.Model(&models.Conversation{}).
		Select("tenant_id, MAX(last_active_at) AS active_at").
		Where("tenant_id IN ?", tenantIDs).
		Group("tenant_id").
		Scan(&conversationRows).Error; err != nil {
		return nil, err
	}
	for _, row := range conversationRows {
		var activeAt *time.Time
		if row.ActiveAt.Valid {
			value := row.ActiveAt.Time
			activeAt = &value
		}
		item := stats[row.TenantID]
		item.LatestConversationActive = activeAt
		stats[row.TenantID] = item
	}

	var userRows []activityRow
	if err := db.Model(&models.User{}).
		Select("tenant_id, MAX(last_login_at) AS active_at").
		Where("tenant_id IN ? AND status <> ? AND deleted_at IS NULL", tenantIDs, enums.StatusDeleted).
		Group("tenant_id").
		Scan(&userRows).Error; err != nil {
		return nil, err
	}
	for _, row := range userRows {
		var activeAt *time.Time
		if row.ActiveAt.Valid {
			value := row.ActiveAt.Time
			activeAt = &value
		}
		item := stats[row.TenantID]
		item.LatestUserLogin = activeAt
		stats[row.TenantID] = item
	}

	return stats, nil
}

func parseTenantAggregateTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if parsed, ok := value.(time.Time); ok {
		return &parsed, nil
	}
	var text string
	switch current := value.(type) {
	case string:
		text = current
	case []byte:
		text = string(current)
	default:
		return nil, fmt.Errorf("unsupported tenant activity time type %T", value)
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		parsed, err := time.ParseInLocation(layout, text, time.Local)
		if err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid tenant activity time %q", text)
}

func (r *tenantRepository) Create(db *gorm.DB, t *models.Tenant) (err error) {
	err = db.Create(t).Error
	return
}

func (r *tenantRepository) Update(db *gorm.DB, t *models.Tenant) (err error) {
	err = db.Save(t).Error
	return
}

func (r *tenantRepository) Updates(db *gorm.DB, id int64, columns map[string]any) (err error) {
	err = db.Model(&models.Tenant{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *tenantRepository) UpdateColumn(db *gorm.DB, id int64, name string, value any) (err error) {
	err = db.Model(&models.Tenant{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *tenantRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Tenant{}, "id = ?", id)
}
