package sandbox

import (
	"strings"
	"unicode"
)

var demoExchanges = [...]struct {
	scene    string
	question string
	reply    string
}{
	{"A", "酒店有停车场吗？", "酒店提供免费停车服务，设有地上地下停车场，推荐您从昭潭路进入。"},
	{"B", "能帮我升个房吗？", "可以的，你是会员，可以为您升级大床房"},
	{"C", "有点吵，可以换个安静点的吗？", "可以为您从1208房换到1606房，1606房相对安静。"},
	{"D", "空调还没修好，都二十多分钟了。", "很抱歉空调问题影响了您的入住。我们为您提供50元补偿、免费升房或延迟退房至14:00三种补救方案。"},
	{"E", "我是什么会员？生日有什么福利？", "您当前是钻石会员，已入住16次。生日可享50元券礼遇，有效期30天。"},
	{"F", "你们家的枕头好舒服，同款在哪里买？", "您喜欢的是丽斯严选零压力护颈椎枕头，售价180.18元。给您发商品资料，您可以先看看。"},
}

// MatchDemoQuestion matches the whole question, never a keyword or substring.
// Only input whitespace and common punctuation are ignored; replies are unchanged.
func MatchDemoQuestion(text string) (scene, reply string) {
	key := normalizeDemoQuestion(text)
	for _, item := range demoExchanges {
		if key == normalizeDemoQuestion(item.question) {
			return item.scene, item.reply
		}
	}
	return "", ""
}

func DemoReply(scene string) string {
	for _, item := range demoExchanges {
		if item.scene == scene {
			return item.reply
		}
	}
	return ""
}

func normalizeDemoQuestion(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("，。！？,.!?", r) {
			return -1
		}
		return r
	}, text)
}
