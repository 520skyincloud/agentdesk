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
