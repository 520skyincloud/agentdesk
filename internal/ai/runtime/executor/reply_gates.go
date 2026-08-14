package executor

import (
	"os"
	"strconv"
	"strings"
)

// replyGate 是 P9 灰度开关（分层隔离计划 14.1/22）：每道已上线门禁都可独立关闭，
// 默认全部开启（生产安全默认），支持三种关闭粒度（按需配置其一）：
//
//	AI_REPLY_<GATE>=off                     全局关闭
//	AI_REPLY_<GATE>_EXCLUDE_TENANT_IDS=1,2  排除指定租户（豁免）
//	AI_REPLY_<GATE>_EXCLUDE_STORE_IDS=5     排除指定门店
//	AI_REPLY_<GATE>_EXCLUDE_BINDING_IDS=9   排除指定员工绑定
//
// 回滚顺序遵循文档 22：优先关 Recommendation/Evidence gate，保住 Fact 与 Resource 硬门禁。
type replyGate string

const (
	gateFactSourceBoundary  replyGate = "FACT_SOURCE_BOUNDARY_V2"
	gateResourceEligibility replyGate = "RESOURCE_ELIGIBILITY_V1"
	gateEvidenceQuality     replyGate = "EVIDENCE_QUALITY_GATE_V1"
	gatePromptLayer         replyGate = "PROMPT_LAYER_V2"
)

// gateEnabled 判定该门禁对当前请求是否生效：默认开；off 全关；排除清单命中则关。
func gateEnabled(gate replyGate, req RunInput) bool {
	prefix := "AI_REPLY_" + string(gate)
	value := strings.ToLower(strings.TrimSpace(os.Getenv(prefix)))
	if value == "off" || value == "false" || value == "0" {
		return false
	}
	return !gateExcluded(prefix+"_EXCLUDE_TENANT_IDS", req.Conversation.TenantID) &&
		!gateExcluded(prefix+"_EXCLUDE_STORE_IDS", req.Conversation.StoreID) &&
		!gateExcluded(prefix+"_EXCLUDE_BINDING_IDS", req.Conversation.StoreStaffBindingID)
}

func gateExcluded(envName string, value int64) bool {
	if value <= 0 {
		return false
	}
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return false
	}
	for _, item := range strings.Split(raw, ",") {
		parsed, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err == nil && parsed == value {
			return true
		}
	}
	return false
}
