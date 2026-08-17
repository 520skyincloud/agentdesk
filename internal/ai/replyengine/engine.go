package replyengine

import (
	"encoding/json"
	"strings"
)

func IsExplicitHandoffRequest(text string) bool {
	compact := NormalizeIntentText(text)
	return ContainsAny(compact,
		"转人工", "找人工", "人工客服", "真人客服", "真人处理", "人工处理", "让人处理", "找个人", "找你们人",
		"投诉", "差评", "举报", "曝光", "起诉", "律师", "12315", "安全", "报警", "隐私")
}

func LooksLikeMediaFollowUp(text string) bool {
	compact := NormalizeIntentText(text)
	if compact == "" || IsExplicitHandoffRequest(compact) {
		return false
	}
	if ContainsAny(compact, "wifi", "wi-fi", "无线", "网络", "发票", "专票", "普票", "停车", "早餐", "电话", "定位", "小程序", "入住", "退房") {
		return false
	}
	return ContainsAny(compact,
		"这是啥", "这是什么", "这个是啥", "这啥", "这是干嘛", "这是干什么", "干嘛的", "干什么的", "啥意思", "什么意思", "啥菜", "什么菜", "吃得怎么样", "怎么样", "看起来怎么样", "你看", "看一下", "帮我看", "图里", "图片里", "照片里", "刚发的", "上面", "这个", "那张",
		"看到了吗", "看到了没", "看见了吗", "看见没", "收到了吗", "收到没")
}

func MediaUnderstandingFromPayload(raw string) (mediaText string, mediaSummary string, status string) {
	if strings.TrimSpace(raw) == "" {
		return "", "", ""
	}
	var payload struct {
		MediaText    string `json:"mediaText"`
		MediaSummary string `json:"mediaSummary"`
		MediaStatus  string `json:"mediaUnderstandingStatus"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", "", ""
	}
	return strings.TrimSpace(payload.MediaText), strings.TrimSpace(payload.MediaSummary), strings.TrimSpace(payload.MediaStatus)
}

// MediaResponseExpectationFromPayload reads the structured media routing hint
// written atomically with message_analysis.v2. The boolean is false for legacy
// payloads so callers can keep a narrow compatibility path without treating
// keyword heuristics as the current protocol.
func MediaResponseExpectationFromPayload(raw string) (mode string, basis string, confidence float64, ok bool) {
	if strings.TrimSpace(raw) == "" {
		return "", "", 0, false
	}
	var payload struct {
		ResponseExpectation *struct {
			Mode       string  `json:"mode"`
			Basis      string  `json:"basis"`
			Confidence float64 `json:"confidence"`
		} `json:"responseExpectation"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.ResponseExpectation == nil {
		return "", "", 0, false
	}
	mode = strings.TrimSpace(payload.ResponseExpectation.Mode)
	basis = strings.TrimSpace(payload.ResponseExpectation.Basis)
	if mode != "none" && mode != "reply" && mode != "uncertain" {
		return "", "", 0, false
	}
	return mode, basis, payload.ResponseExpectation.Confidence, true
}

func MediaResponseExpectationTriggersAI(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "reply", "uncertain":
		return true
	default:
		return false
	}
}

func MediaUnderstandingExplicitlyNoIntent(text string) bool {
	return containsMediaIntentAny(text, []string{
		"无清晰文字报错或明确服务诉求",
		"无清晰文字",
		"无明确服务诉求",
		"无明确诉求",
		"无服务诉求",
		"没有清晰文字",
		"没有明确服务诉求",
		"没有明确诉求",
		"没有报错",
		"未见报错",
		"不含报错",
		"无报错",
	})
}

func MediaUnderstandingHasActionableIntent(text string) bool {
	text = normalizeMediaIntentText(text)
	if text == "" || MediaUnderstandingExplicitlyNoIntent(text) {
		return false
	}
	if strings.Contains(text, "?") || strings.Contains(text, "？") {
		return true
	}
	return containsMediaIntentAny(text, []string{
		"怎么弄", "怎么办", "怎么处理", "怎么回事", "怎么解决", "怎么打开", "怎么登录", "怎么支付", "怎么入住", "怎么退房",
		"打不开", "进不去", "无法", "不能", "失败", "报错", "错误", "异常", "超时", "卡住", "无权限", "未登录", "二维码", "验证码",
		"需要", "帮忙", "求助", "投诉", "差评", "退款", "赔偿", "坏了", "漏水", "脏", "没电", "没网", "连不上",
		"在哪里", "在哪", "哪里", "定位", "地址", "导航", "电话", "小程序", "入住码", "停车", "洗衣房", "wifi", "wi-fi", "网络", "投屏", "发票", "早餐",
	})
}

func normalizeMediaIntentText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), ""))
}

func containsMediaIntentAny(text string, needles []string) bool {
	text = normalizeMediaIntentText(text)
	for _, needle := range needles {
		needle = normalizeMediaIntentText(needle)
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func NormalizeIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "？", "", "?", "", "！", "", "!", "")
	return replacer.Replace(text)
}

func ContainsAny(text string, values ...string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(text, strings.ToLower(value)) {
			return true
		}
	}
	return false
}
