package repositories

import "gorm.io/gorm"

var TenantIntegrityAuditRepository = newTenantIntegrityAuditRepository()

func newTenantIntegrityAuditRepository() *tenantIntegrityAuditRepository {
	return &tenantIntegrityAuditRepository{}
}

type tenantIntegrityAuditRepository struct{}

type TenantIntegrityQuery struct {
	Table  string
	Alias  string
	Joins  []string
	Where  string
	Args   []any
	IDExpr string
}

type TenantIntegrityQueryResult struct {
	Count     int64
	SampleIDs []int64
}

type TenantIntegrityCandidateEvidenceRow struct {
	ID             int64
	TenantID       int64
	ConversationID int64
	MessageIDs     string
}

type TenantIntegrityMessageEvidenceRow struct {
	ID             int64
	TenantID       int64
	ConversationID int64
}

func (r *tenantIntegrityAuditRepository) HasTable(db *gorm.DB, table string) bool {
	return db.Migrator().HasTable(table)
}

func (r *tenantIntegrityAuditRepository) HasColumn(db *gorm.DB, table, column string) bool {
	return db.Migrator().HasColumn(table, column)
}

func (r *tenantIntegrityAuditRepository) Query(
	db *gorm.DB,
	query TenantIntegrityQuery,
	sampleLimit int,
) (TenantIntegrityQueryResult, error) {
	build := func() *gorm.DB {
		ret := db.Table(query.Table + " AS " + query.Alias)
		for _, join := range query.Joins {
			ret = ret.Joins(join)
		}
		if query.Where != "" {
			ret = ret.Where(query.Where, query.Args...)
		}
		return ret
	}

	result := TenantIntegrityQueryResult{SampleIDs: []int64{}}
	if err := build().Count(&result.Count).Error; err != nil {
		return result, err
	}
	if result.Count == 0 || sampleLimit <= 0 {
		return result, nil
	}

	if err := build().
		Select(query.IDExpr).
		Order(query.IDExpr + " ASC").
		Limit(sampleLimit).
		Scan(&result.SampleIDs).Error; err != nil {
		return TenantIntegrityQueryResult{}, err
	}
	return result, nil
}

func (r *tenantIntegrityAuditRepository) FindCandidateEvidenceRows(db *gorm.DB, table string) ([]TenantIntegrityCandidateEvidenceRow, error) {
	rows := make([]TenantIntegrityCandidateEvidenceRow, 0)
	err := db.Table(table).
		Select("id, tenant_id, conversation_id, message_ids").
		Where("message_ids <> ''").
		Order("id ASC").
		Scan(&rows).Error
	return rows, err
}

func (r *tenantIntegrityAuditRepository) FindMessageEvidenceRows(db *gorm.DB, table string, ids []int64) ([]TenantIntegrityMessageEvidenceRow, error) {
	rows := make([]TenantIntegrityMessageEvidenceRow, 0, len(ids))
	if len(ids) == 0 {
		return rows, nil
	}
	err := db.Table(table).
		Select("id, tenant_id, conversation_id").
		Where("id IN ?", ids).
		Scan(&rows).Error
	return rows, err
}
