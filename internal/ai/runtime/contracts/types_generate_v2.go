package contracts

// 交接文档 §5.2/§6.1：generate_task_input.v2 是 Generate 阶段的服务端权威
// 输入。groups 携带真实 groupKey（模型必须逐字回显）；groupRef（G1/G2/G3）
// 仅用于本批次理解，不得作为模型输出或 Validator 判定依据。

// GenerateTaskInputV2 是 v2 输入的完整形态。
type GenerateTaskInputV2 struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Groups        []GenerateTaskGroupV2     `json:"groups"`
	Tasks         []GenerateTaskInputTaskV2 `json:"tasks"`
}

// GenerateTaskGroupV2 是一个服务端确定的回复组。
type GenerateTaskGroupV2 struct {
	GroupRef   string   `json:"groupRef"`
	GroupKey   string   `json:"groupKey"`
	Sequence   int      `json:"sequence"`
	TaskKeys   []string `json:"taskKeys"`
	OutputMode string   `json:"outputMode"`
	Required   bool     `json:"required"`
}

// GenerateTaskInputTaskV2 是一个待回答任务及其组绑定。
type GenerateTaskInputTaskV2 struct {
	TaskKey         string `json:"taskKey"`
	GroupRef        string `json:"groupRef"`
	GroupKey        string `json:"groupKey"`
	Sequence        int    `json:"sequence"`
	CustomerRequest string `json:"customerRequest"`
}
