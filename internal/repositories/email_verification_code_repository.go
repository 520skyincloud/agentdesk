package repositories

import (
	"agent-desk/internal/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var EmailVerificationCodeRepository = newEmailVerificationCodeRepository()

type emailVerificationCodeRepository struct{}

func newEmailVerificationCodeRepository() *emailVerificationCodeRepository {
	return &emailVerificationCodeRepository{}
}

func (r *emailVerificationCodeRepository) Create(db *gorm.DB, item *models.EmailVerificationCode) error {
	return db.Create(item).Error
}

func (r *emailVerificationCodeRepository) Get(db *gorm.DB, id int64) *models.EmailVerificationCode {
	item := &models.EmailVerificationCode{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *emailVerificationCodeRepository) FindLatestOpen(db *gorm.DB, email, purpose, scopeHash string) *models.EmailVerificationCode {
	item := &models.EmailVerificationCode{}
	err := db.Where("email = ? AND purpose = ? AND scope_token_hash = ? AND consumed_at IS NULL", email, purpose, scopeHash).
		Order("id DESC").Take(item).Error
	if err != nil {
		return nil
	}
	return item
}

func (r *emailVerificationCodeRepository) FindVerifiedByTokenForUpdate(db *gorm.DB, tokenHash string) *models.EmailVerificationCode {
	item := &models.EmailVerificationCode{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("verification_token_hash = ? AND verified_at IS NOT NULL AND consumed_at IS NULL", tokenHash).
		Take(item).Error
	if err != nil {
		return nil
	}
	return item
}

func (r *emailVerificationCodeRepository) CountCreatedSince(db *gorm.DB, email, requestIP string, since time.Time) int64 {
	query := db.Model(&models.EmailVerificationCode{}).Where("created_at >= ?", since)
	if email != "" {
		query = query.Where("email = ?", email)
	}
	if requestIP != "" {
		query = query.Where("request_ip = ?", requestIP)
	}
	var count int64
	_ = query.Count(&count).Error
	return count
}

func (r *emailVerificationCodeRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.EmailVerificationCode{}).Where("id = ?", id).Updates(columns).Error
}
