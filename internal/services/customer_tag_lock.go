package services

import "sync"

const customerTagLockStripes = 128

var customerTagLocks [customerTagLockStripes]sync.Mutex

// SQLite has no row-level FOR UPDATE. The process lock complements database
// row locks and serializes writes for one Tenant/Store/customer identity.
func lockCustomerTags(tenantID, storeID, customerID int64) func() {
	key := uint64(tenantID)*1099511628211 ^ uint64(storeID)*16777619 ^ uint64(customerID)
	index := key % customerTagLockStripes
	customerTagLocks[index].Lock()
	return customerTagLocks[index].Unlock
}
