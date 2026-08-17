package executor

import (
	"strings"
	"unicode/utf8"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/repositories"
	"github.com/mlogclub/simple/sqls"
)

// authoritativeStoreAddress 返回当前门店的权威地址（hydrate 后实例 → Store 表）。
// 这是 protected store fact（文档 5.4）：只能来自 Store 表，
// 不得来自知识库、客户陈述、客户图片 OCR、历史 AI 回复或模型推断。
// validateStoreNameAssertions 任何 part 声明的场所名必须匹配权威门店名。
// 命中客户注入名（如“壹间公寓”）直接 rejected，不进入地址子意图分支。
func validateStoreNameAssertions(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	authoritative := authoritativeStoreNames(input.Req)
	if len(authoritative) == 0 {
		return nil
	}
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		for _, name := range extractAssertedPlaceNames(content) {
			if placeNameAuthorized(name, authoritative) {
				continue
			}
			issues = append(issues, validationIssue(
				"protected_fact_source_violation",
				"$.parts",
				"reply asserts an unauthorized store place name: "+name,
			))
			break
		}
	}
	return issues
}

// authoritativeStoreNames 契约 4.9/13.1-7：门店名称是受保护事实。权威集合
// 来自门店记录（Name/BrandName/NavigationName）；客户口述的“XX公寓/酒店”
// 不得被确认或复述（生产 1518/1521 注入场景）。
func authoritativeStoreNames(req RunInput) []string {
	instance := findRuntimeWxWorkInstance(req)
	if instance == nil || instance.StoreID <= 0 {
		return nil
	}
	store := repositories.StoreRepository.Get(sqls.DB(), instance.StoreID)
	if store == nil || store.TenantID != req.Conversation.TenantID {
		return nil
	}
	names := make([]string, 0, 3)
	for _, name := range []string{store.Name, store.BrandName, store.NavigationName} {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// extractAssertedPlaceNames 抓取回复中声明的场所名（…公寓/酒店/宾馆/大厦/旅店/民宿）。
// 使用字节索引扫描，回溯时按 UTF-8 前导字节恢复 rune 边界。
func extractAssertedPlaceNames(content string) []string {
	suffixes := []string{"公寓", "酒店", "宾馆", "大厦", "旅店", "民宿"}
	found := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, suffix := range suffixes {
		from := 0
		for from <= len(content) {
			idx := strings.Index(content[from:], suffix)
			if idx < 0 {
				break
			}
			endByte := from + idx + len(suffix)
			startByte := endByte
			maxBack := endByte - 36 // ≈12 个 CJK rune
			if maxBack < from {
				maxBack = from
			}
			for startByte > maxBack {
				r, size := decodeLastRune(content[maxBack:startByte])
				if isPlaceNameBoundary(r) {
					break
				}
				startByte -= size
			}
			name := strings.TrimSpace(content[startByte:endByte])
			if utf8.RuneCountInString(name) >= 3 && !genericStoreCategoryPhrase(name) {
				if _, dup := seen[name]; !dup {
					seen[name] = struct{}{}
					found = append(found, name)
				}
			}
			from = endByte
		}
	}
	return found
}

func genericStoreCategoryPhrase(name string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(strings.TrimSpace(name))
	for _, prefix := range []string{"我们", "你们", "咱们", "这个", "这家", "那家", "本", "该", "当前"} {
		compact = strings.TrimPrefix(compact, prefix)
	}
	switch compact {
	case "酒店", "公寓", "宾馆", "大厦", "旅店", "民宿":
		return true
	default:
		return false
	}
}

// decodeLastRune 返回 s 中最后一个 rune 及其字节长度。
func decodeLastRune(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	return utf8.DecodeLastRuneInString(s)
}

// isPlaceNameBoundary 判定场所名左边界：标点、空白或引导词。
func isPlaceNameBoundary(r rune) bool {
	switch r {
	case '，', ',', '。', '；', ';', '：', ':', '？', '?', '！', '!', ' ', '\n', '\t', '“', '”', '"', '\'', '（', '）', '(', ')', '、', '的', '是', '叫', '在', '填', '去', '到', '住', '订', '找':
		return true
	}
	return false
}

// placeNameAuthorized 场所名与权威名集合比对（互为包含视为一致）。
func placeNameAuthorized(name string, authoritative []string) bool {
	for _, allowed := range authoritative {
		if strings.Contains(allowed, name) || strings.Contains(name, allowed) {
			return true
		}
	}
	return false
}

func authoritativeStoreAddress(req RunInput) string {
	instance := findRuntimeWxWorkInstance(req)
	if instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.StoreAddress)
}

// validateReplyFactSourceBoundary 是 FactSourceBoundary 的 Phase1 落地（文档 7/15.2）：
// 对「受保护门店地址」做确定性值比对。
//
// 规则：地址类任务（reply 中 part 的 task 对应地址子意图）若声明了地址值，
// 该值必须与权威门店地址一致（领域规范化后比较，允许格式差异）；
// 出现任何与权威值不一致的地址断言（如客户 OCR 里的“壹间公寓高新社区”）直接 rejected。
// 这是业务事实错误，不可走协议修复。
func validateReplyFactSourceBoundary(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := validateStoreNameAssertions(input)
	if len(issues) > 0 {
		return issues
	}
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(input.Plan.Tasks))
	for _, task := range input.Plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	issues = append(issues, make([]contracts.ValidationIssueV1, 0)...)
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		addressTask := false
		for _, taskKey := range part.TaskKeys {
			if task, ok := planByTask[taskKey]; ok && isAddressTextSubIntent(task.SubIntent) {
				addressTask = true
				break
			}
		}
		if !addressTask {
			continue
		}
		// 找到本轮证据中的权威地址值（S* store_fact）。
		authoritative := ""
		for _, item := range input.Evidence.Items {
			if item.SourceType == "store_fact" && strings.Contains(item.Title, "门店地址") {
				authoritative = strings.TrimSpace(item.Content)
				break
			}
		}
		if authoritative == "" {
			// 权威地址未配置：地址类任务不得下确定性地址断言（只能说明未配置或追问）。
			if assertedAddressAssertion(content) {
				issues = append(issues, validationIssue(
					"protected_fact_source_violation",
					"$.parts",
					"address task asserts a concrete address while the authoritative store address is unconfigured",
				))
			}
			continue
		}
		// 正文包含具体地址断言但与权威值不一致 → 拒绝（壹间公寓场景）。
		if asserted := assertedAddressAssertion(content); asserted {
			if !addressMatchesAuthoritative(content, authoritative) {
				issues = append(issues, validationIssue(
					"protected_fact_source_violation",
					"$.parts",
					"address assertion does not match the authoritative store address",
				))
			}
		}
	}
	return issues
}

// assertedAddressAssertion 粗判正文是否包含具体地址断言：
// 中文地址特征 = “路/街/道/号/层/楼/座/栋/室”等地标 + 数字（阿拉伯或中文数字），
// 或出现“社区/公寓/大厦/广场/小区”类场所词（如“壹间公寓高新社区”，常无阿拉伯数字）。
func assertedAddressAssertion(content string) bool {
	compact := strings.TrimSpace(content)
	if compact == "" {
		return false
	}
	hasLandmark := strings.ContainsAny(compact, "路街道号层楼座栋室")
	hasPlace := strings.ContainsAny(compact, "社区公寓大厦广场小区")
	hasDigits := strings.ContainsAny(compact, "0123456789一二三四五六七八九十百千壹贰叁肆伍陆柒捌玖")
	return (hasLandmark && hasDigits) || hasPlace
}

// addressMatchesAuthoritative 判断正文中的地址断言是否与权威地址一致。
// 关键信号是「路/街/道 + 门牌号」：权威地址中的道路+门牌核心（如“路392”）
// 必须出现在正文中；“12-15层/12至15层”这类楼层格式差异不影响判定。
// 权威地址没有道路+门牌信号时，退化为规范化全文包含比较。
func addressMatchesAuthoritative(content, authoritative string) bool {
	normContent := normalizeAddressForCompare(content)
	normAuth := normalizeAddressForCompare(authoritative)
	if normAuth == "" {
		return true
	}
	if key, ok := roadHouseNumberKey(normAuth); ok {
		return strings.Contains(normContent, key)
	}
	return strings.Contains(normContent, normAuth) || strings.Contains(normAuth, normContent)
}

// roadHouseNumberKey 从规范化地址中提取「道路字 + 紧随的门牌数字」关键信号。
// 例：“合肥市包河区水阳江路392号职工之家1215整层” -> “路392”。
func roadHouseNumberKey(normAuth string) (string, bool) {
	runes := []rune(normAuth)
	for i, r := range runes {
		if r != '路' && r != '街' && r != '道' {
			continue
		}
		digits := 0
		for j := i + 1; j < len(runes) && digits < 6; j++ {
			if runes[j] >= '0' && runes[j] <= '9' {
				digits++
				continue
			}
			break
		}
		if digits > 0 {
			return string(runes[i : i+1+digits]), true
		}
	}
	return "", false
}

// normalizeAddressForCompare 保留汉字与数字，丢弃其余字符；
// 同时把“至/到/-”统一删掉，使“12至15层”与“12-15层”等价。
func normalizeAddressForCompare(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff: // CJK 统一汉字
			switch r {
			case '至', '到':
				// 连接词归一：丢弃
			default:
				b.WriteRune(r)
			}
		default:
			// 标点、字母、空白丢弃
		}
	}
	return b.String()
}
