package builders

import (
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pms/sandbox"
)

func BuildPMSSandboxWorkspace(snapshot *sandbox.Snapshot, active bool) *response.PMSSandboxWorkspace {
	if snapshot == nil {
		return nil
	}
	result := &response.PMSSandboxWorkspace{Snapshot: *snapshot, Active: active}
	result.Resources = append([]sandbox.Resource{}, snapshot.Resources...)
	for i := range result.Resources {
		result.Resources[i].CardPayload = ""
	}
	result.Options = map[string][]response.PMSSandboxOption{
		"cleanStatus":     {{"clean", "净房"}, {"dirty", "脏房"}, {"maintenance", "维护中"}},
		"orderStatus":     {{"reserved", "已预订"}, {"checked_in", "已入住"}, {"checked_out", "已退房"}, {"cancelled", "已取消"}},
		"ruleCode":        {{"child_policy", "儿童政策"}, {"upgrade", "升房"}, {"room_change", "换房"}, {"late_checkout", "延迟退房"}, {"recovery_commitment", "服务补救"}},
		"ruleAction":      {{"information", "政策答复"}, {"upgrade", "升房"}, {"room_change", "换房"}, {"late_checkout", "延迟退房"}, {"commitment", "补偿承诺"}},
		"operationStatus": {{"pending", "待客户确认"}, {"succeeded", "办理成功"}, {"completed", "后台操作完成"}, {"cancelled", "已取消"}, {"expired", "已过期"}, {"superseded", "已被新方案替代"}, {"failed", "未完成"}},
		"deliveryStatus":  {{"sent", "已投递"}, {"pending", "待投递"}, {"sending", "投递中"}, {"failed", "投递失败"}, {"not_sent", "尚未投递"}, {"not_applicable", "无客户投递"}},
	}
	return result
}
