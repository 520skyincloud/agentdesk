package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
)

const legacyWxWorkProtocolPersonaPrompt = `你是酒店前台同事，说话简短、自然、像正常微信聊天。
不要用客服模板，不要加固定结尾，不要用“亲”“为您”“这边”“～”。
能确定就直接答；需要真实动作时先收集一个最关键字段或进入接待路由，没工具或路由结果前别表达动作已执行或后续有人处理。
互动要接住上下文，别总回“哈哈/收到”。闲聊、感谢、确认、表情和纠错都要顺着当前话题自然回应，结束类就收住。`

const neutralWxWorkProtocolPersonaPrompt = `你是线上酒店接待，说话简短、自然、像正常微信聊天。
不要用客服模板，不要加固定结尾，不要用“亲”“为您”“这边”“～”。
能确定就直接答；需要真实动作时先收集一个最关键字段或进入接待路由，没工具或路由结果前别表达动作已执行或后续有人处理。
互动要接住上下文，别总回“哈哈/收到”。闲聊、感谢、确认、表情和纠错都要顺着当前话题自然回应，结束类就收住。`

func init() {
	register(62, "migrate legacy wxwork reception persona to neutral wording", func() error {
		now := time.Now()
		return sqls.DB().Model(&models.WxWorkProtocolInstance{}).
			Where("persona_prompt = ?", legacyWxWorkProtocolPersonaPrompt).
			Updates(map[string]any{
				"persona_prompt":   neutralWxWorkProtocolPersonaPrompt,
				"front_desk_mode":  "unmanned",
				"updated_at":       now,
				"update_user_id":   constants.SystemAuditUserID,
				"update_user_name": constants.SystemAuditUserName,
			}).Error
	})
}
