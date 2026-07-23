package services

import (
	"sort"
	"sync"
)

const customerTagLockStripes = 128

var customerTagLocks [customerTagLockStripes]sync.Mutex

// SQLite has no row-level FOR UPDATE. The process lock complements database
// row locks and serializes writes for one Tenant/Store/customer identity.
func lockCustomerTags(tenantID, storeID, customerID int64) func() {
	return lockCustomerTagStores(tenantID, customerID, storeID)
}

func lockCustomerTagStores(tenantID, customerID int64, storeIDs ...int64) func() {
	indexSet := make(map[int]struct{}, len(storeIDs))
	for _, storeID := range storeIDs {
		if storeID <= 0 {
			continue
		}
		key := uint64(tenantID)*1099511628211 ^ uint64(storeID)*16777619 ^ uint64(customerID)
		indexSet[int(key%customerTagLockStripes)] = struct{}{}
	}
	indexes := make([]int, 0, len(indexSet))
	for index := range indexSet {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		customerTagLocks[index].Lock()
	}
	return func() {
		for i := len(indexes) - 1; i >= 0; i-- {
			customerTagLocks[indexes[i]].Unlock()
		}
	}
}
