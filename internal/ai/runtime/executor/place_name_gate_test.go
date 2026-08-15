package executor

import (
	"testing"
)

// 生产回放 1518/1521：客户口述“壹间公寓”，AI 复述并确认——名称层
// 受保护事实校验必须在任何子意图前拒绝。
func TestExtractAssertedPlaceNames(t *testing.T) {
	names := extractAssertedPlaceNames("填壹间公寓就行，地址可以写：合肥市工人文化宫昭潭路停车场入口右手边大楼。")
	joined := ""
	for _, name := range names {
		joined += name + "|"
	}
	if joined == "" || !contains(joined, "壹间公寓") {
		t.Fatalf("must extract injected place name, got %v", names)
	}
	// 真实权威名必须能互含通过。
	if !placeNameAuthorized("某某酒店", []string{"某某酒店管理公司"}) {
		t.Fatal("substring-both-ways authorization broken")
	}
	if placeNameAuthorized("壹间公寓", []string{"职工之家酒店"}) {
		t.Fatal("injected name must not authorize")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
