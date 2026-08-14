package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/actions"
)

// buildDisabledActionInstruction 把动作目录里“未开放/未接入”的能力显式告诉 Generate 模型，
// 避免模型不知道某能力已被关闭，就自行编造“帮你查 / 帮你看 / 帮你确认”。
//
// 判断依据是 KindExternal（依赖外部系统、尚未接入执行器），而不是关键词穷举：
// 当前只有“查房态 / 查会员等级”两个 external 动作，且都未接入。
func buildDisabledActionInstruction() string {
	names := make([]string, 0, 2)
	for _, def := range actions.List() {
		if def.Kind == actions.KindExternal {
			names = append(names, def.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "当前系统未开放的能力（禁止承诺替客户查询或操作，也不要说“帮你查 / 帮你看 / 帮你确认”）：" +
		strings.Join(names, "、") +
		"。遇到这类诉求：要动作就按人工路由二次确认，要信息就如实说明当前暂不支持。"
}
