package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var StoreService = newStoreService()

func newStoreService() *storeService {
	return &storeService{}
}

type storeService struct{}

func (s *storeService) Get(id int64) *models.Store {
	if id <= 0 {
		return nil
	}
	return repositories.StoreRepository.Get(sqls.DB(), id)
}

func (s *storeService) Take(where ...any) *models.Store {
	return repositories.StoreRepository.Take(sqls.DB(), where...)
}
