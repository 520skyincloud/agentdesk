package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

func validateReplyMediaObservationUse(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	required := make(map[string]struct{})
	for _, task := range input.Plan.Tasks {
		if stringInSlice("must_use_media_observation", task.Constraints) {
			required[task.TaskKey] = struct{}{}
		}
	}
	if len(required) == 0 {
		return nil
	}

	issues := make([]contracts.ValidationIssueV1, 0)
	for partIndex, part := range input.Output.Parts {
		if !replyPartCoversAnyTask(part.TaskKeys, required) || !mediaObservationReplyIsEvasive(part.Content) {
			continue
		}
		issues = append(issues, validationIssue(
			"media_observation_not_used",
			fmt.Sprintf("$.parts[%d].content", partIndex),
			"ready media observation must be used: state a concrete visible feature or plausible candidate instead of saying it is unknown or asking whether the user means the image",
		))
	}
	return issues
}

func replyPartCoversAnyTask(taskKeys []string, required map[string]struct{}) bool {
	for _, taskKey := range taskKeys {
		if _, ok := required[strings.TrimSpace(taskKey)]; ok {
			return true
		}
	}
	return false
}

func mediaObservationReplyIsEvasive(content string) bool {
	compact := compactReplyText(content)
	if compact == "" {
		return true
	}
	return !mediaObservationHasConcreteDescription(compact)
}

func mediaObservationHasConcreteDescription(compact string) bool {
	for _, prefix := range []string{
		"能看到一个", "能看到一", "看起来像", "有点像", "这是一个", "显示的是", "画面里有", "画面里是",
		"画面中有", "画面中是", "照片里有", "照片里是", "照片中有", "照片中是", "图片中有", "图片中是",
		"图里有", "图里是", "图上有", "图上是", "图中有", "图中是", "文字是", "内容是", "内容有",
		"看着像", "看着是", "看着有", "大概是", "大概像", "大概有", "可能是", "疑似", "应该是",
		"这是个", "这是张", "这是只", "这是块", "这是份", "更像", "像是", "像一个", "像一", "像个", "写着",
		"这是", "看着", "大概", "图中",
	} {
		index := strings.Index(compact, prefix)
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace(compact[index+len(prefix):])
		if mediaObservationConcreteTail(tail) {
			return true
		}
	}
	return false
}

func mediaObservationConcreteTail(tail string) bool {
	tail = strings.TrimLeft(tail, "，,。.!！?？:：;；是有")
	if tail == "" {
		return false
	}
	for _, generic := range []string{
		"图片", "照片", "图", "你发的", "刚发的", "媒体", "文件", "内容", "图里的东西", "图片里的东西",
		"不知道", "不清楚", "不好说", "没法", "无法", "不能", "看不出来", "看不清", "很难判断", "难判断", "不太确定",
		"问题", "个问题", "你问的", "你说的", "这个", "那个", "啥", "什么", "我来", "我还", "需要", "先看看",
	} {
		if strings.HasPrefix(tail, generic) {
			return false
		}
	}
	return true
}
