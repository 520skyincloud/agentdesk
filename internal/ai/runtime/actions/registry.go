// Package actions 是回复动作目录的代码注册表。
//
// 它把“系统能替客户干的事”集中登记：内置动作（转人工、发定位等）由代码初始化，
// 外部动作（查房态、查会员）只登记元数据与开关、默认关闭，待真实接入后才写入执行器。
// 后台只允许开关与排序，不允许新增/删除动作，也不允许打开尚未接入的外部动作。
//
// 本包保持零业务依赖：执行器由上层通过 RegisterExecutor 注入，避免与 services 形成循环依赖。
package actions

import (
	"errors"
	"sort"
	"strings"
)

// Kind 表示动作的实现来源。
type Kind string

const (
	KindBuiltin  Kind = "builtin"  // 系统内部可执行。
	KindExternal Kind = "external" // 依赖外部系统，未接入前不可执行。
	KindTool     Kind = "tool"     // 已接入的工具调用。
)

// ErrActionNotProvisioned 表示动作已在目录登记，但外部系统尚未接入。
var ErrActionNotProvisioned = errors.New("action not provisioned yet")

// Input 是一次动作执行的输入。
type Input struct {
	ConversationID  int64
	TenantID        int64
	StoreID         int64
	AIAgentID       int64
	OriginMessageID int64
	RequestID       string
	Parameters      map[string]any
}

// Output 是一次动作执行的结果。
type Output struct {
	// Handled 为 true 表示动作已落地，回复由动作自身（或后续 Commit）负责。
	Handled bool
	// ReplyText 是可选的动作自身产出文案（例如二次确认询问）。
	ReplyText string
	// NeedConfirmation 为 true 表示动作需要客户二次确认，运行时进入确认链路。
	NeedConfirmation bool
}

// Executor 执行一个动作。
type Executor interface {
	Run(input Input) (Output, error)
}

// Definition 是一个动作的完整定义。
type Definition struct {
	Code                string
	Name                string
	Kind                Kind
	Description         string
	InputSchema         string
	RequireConfirmation bool
	ExecutorRef         string
	DefaultEnabled      bool
}

type registry struct {
	definitions map[string]Definition
	executors   map[string]Executor
	order       []string
}

var global = newRegistry()

func newRegistry() *registry {
	return &registry{
		definitions: map[string]Definition{},
		executors:   map[string]Executor{},
	}
}

// Register 登记一个动作。重复 code 会 panic，用于在启动阶段尽早暴露定义冲突。
func Register(def Definition) {
	def.Code = strings.TrimSpace(def.Code)
	if def.Code == "" {
		panic("reply action code must not be empty")
	}
	if def.Kind == "" {
		def.Kind = KindBuiltin
	}
	if _, exists := global.definitions[def.Code]; exists {
		panic("reply action code already registered: " + def.Code)
	}
	global.definitions[def.Code] = def
	global.order = append(global.order, def.Code)
}

// RegisterExecutor 给已登记动作绑定执行器。
func RegisterExecutor(code string, executor Executor) {
	code = strings.TrimSpace(code)
	if code == "" || executor == nil {
		panic("reply action executor registration requires code and executor")
	}
	if _, exists := global.definitions[code]; !exists {
		panic("cannot register executor for unknown reply action: " + code)
	}
	global.executors[code] = executor
}

// List 返回按 code 排序的动作定义快照。
func List() []Definition {
	ret := make([]Definition, 0, len(global.definitions))
	for _, code := range global.order {
		if def, ok := global.definitions[code]; ok {
			ret = append(ret, def)
		}
	}
	sort.SliceStable(ret, func(i, j int) bool { return ret[i].Code < ret[j].Code })
	return ret
}

// Get 按 code 取动作定义。
func Get(code string) (Definition, bool) {
	def, ok := global.definitions[strings.TrimSpace(code)]
	return def, ok
}

// GetDefinitionKind 按 code 取动作类型。
func GetDefinitionKind(code string) Kind {
	def, ok := Get(code)
	if !ok {
		return ""
	}
	return def.Kind
}

// Provisioned 判断动作是否已接入（有执行器）。
func Provisioned(code string) bool {
	_, ok := global.executors[strings.TrimSpace(code)]
	return ok
}

// Resolve 按 code 取可执行动作；未接入时返回 ErrActionNotProvisioned。
func Resolve(code string) (Definition, Executor, error) {
	def, ok := Get(code)
	if !ok {
		return Definition{}, nil, errors.New("unknown reply action: " + code)
	}
	executor, ok := global.executors[def.Code]
	if !ok || executor == nil {
		return def, nil, ErrActionNotProvisioned
	}
	return def, executor, nil
}
