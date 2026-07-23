package enums

type StoreCustomerTagReconcileStrategy string

const (
	StoreCustomerTagReconcileStrategyPreserveSource StoreCustomerTagReconcileStrategy = "preserve_source"
	StoreCustomerTagReconcileStrategyPreserveTarget StoreCustomerTagReconcileStrategy = "preserve_target"
	StoreCustomerTagReconcileStrategyClearRebuild   StoreCustomerTagReconcileStrategy = "clear_rebuild"
)

var StoreCustomerTagReconcileStrategyValues = []StoreCustomerTagReconcileStrategy{
	StoreCustomerTagReconcileStrategyPreserveSource,
	StoreCustomerTagReconcileStrategyPreserveTarget,
	StoreCustomerTagReconcileStrategyClearRebuild,
}

var storeCustomerTagReconcileStrategyLabelMap = map[StoreCustomerTagReconcileStrategy]string{
	StoreCustomerTagReconcileStrategyPreserveSource: "保留来源门店标签",
	StoreCustomerTagReconcileStrategyPreserveTarget: "保留目标门店标签",
	StoreCustomerTagReconcileStrategyClearRebuild:   "清空目标标签后重建",
}

func GetStoreCustomerTagReconcileStrategyLabel(strategy StoreCustomerTagReconcileStrategy) string {
	return storeCustomerTagReconcileStrategyLabelMap[strategy]
}

func IsValidStoreCustomerTagReconcileStrategy(strategy StoreCustomerTagReconcileStrategy) bool {
	for _, value := range StoreCustomerTagReconcileStrategyValues {
		if value == strategy {
			return true
		}
	}
	return false
}
