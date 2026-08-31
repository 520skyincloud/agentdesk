package executor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

const replyTagContextSchemaVersion = "reply_tag_context.v1"

const replyTagContextSchemaV1 = `{"type":"object","additionalProperties":false,"required":["schemaVersion","scenes","tags"],"properties":{"schemaVersion":{"const":"reply_tag_context.v1"},"scenes":{"type":"array","maxItems":3,"uniqueItems":true,"items":{"enum":["parking_service","invoice_service","arrival_service","checkout_service","room_selection","room_assignment","stay_service","pet_service","room_service"]}},"tags":{"type":"array","maxItems":2,"items":{"type":"object","additionalProperties":false,"required":["tagId","semanticKey","name"],"properties":{"tagId":{"type":"integer","minimum":1},"semanticKey":{"type":"string","minLength":1},"name":{"type":"string","minLength":1,"maxLength":5}}}}}}`

var replyTagContextScenes = map[string]struct{}{
	"parking_service": {}, "invoice_service": {}, "arrival_service": {},
	"checkout_service": {}, "room_selection": {}, "room_assignment": {},
	"stay_service": {}, "pet_service": {}, "room_service": {},
}

type replyTagContextV1 struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Scenes        []string                `json:"scenes"`
	Tags          []replyTagContextItemV1 `json:"tags"`
}

type replyTagContextItemV1 struct {
	TagID       int64  `json:"tagId"`
	SemanticKey string `json:"semanticKey"`
	Name        string `json:"name"`
}

func appendReplyTagContext(req RunInput, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData, answerabilityStatus string, collector *callbacks.RuntimeTraceCollector, messages *[]*schema.Message) {
	trace := callbacks.ReplyTagContextTraceData{SchemaVersion: replyTagContextSchemaVersion, Status: "skipped"}
	setTrace := func() {
		if collector != nil {
			collector.SetGenerateTagContext(trace)
		}
	}
	scenes, reason := selectReplyTagScenes(req.UserMessage.Content, intent, replyPlan)
	trace.Scenes = append([]string(nil), scenes...)
	if len(scenes) == 0 {
		trace.Reason = reason
		setTrace()
		return
	}
	if intent.NeedsKnowledge && strings.TrimSpace(answerabilityStatus) != answerabilityStatusHasContext {
		trace.Reason = "knowledge_not_answerable"
		setTrace()
		return
	}
	candidates, err := services.CustomerTagService.SelectReplyTagCandidates(req.Conversation.ID, scenes, currentTurnDisplayText(req.UserMessage.Content))
	if err != nil {
		trace.Status = "failed"
		trace.Reason = "candidate_query_failed"
		setTrace()
		slog.Warn("reply tag context candidate query failed", "conversation_id", req.Conversation.ID, "error", err)
		return
	}
	if len(candidates) == 0 {
		trace.Reason = "no_matching_tags"
		setTrace()
		return
	}
	contextValue := replyTagContextV1{
		SchemaVersion: replyTagContextSchemaVersion,
		Scenes:        append([]string(nil), scenes...),
		Tags:          make([]replyTagContextItemV1, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		contextValue.Tags = append(contextValue.Tags, replyTagContextItemV1{
			TagID: candidate.TagID, SemanticKey: candidate.SemanticKey, Name: candidate.Name,
		})
	}
	if err := validateReplyTagContextV1(contextValue); err != nil {
		trace.Status = "failed"
		trace.Reason = "schema_validation_failed"
		setTrace()
		slog.Warn("reply tag context validation failed", "conversation_id", req.Conversation.ID, "error", err)
		return
	}
	rendered := renderReplyTagContext(contextValue)
	if strings.TrimSpace(rendered) == "" {
		trace.Reason = "empty_render"
		setTrace()
		return
	}
	*messages = append(*messages, schema.SystemMessage(rendered))
	trace.Status = "applied"
	trace.TagIDs = make([]int64, 0, len(contextValue.Tags))
	for _, item := range contextValue.Tags {
		trace.TagIDs = append(trace.TagIDs, item.TagID)
	}
	trace.Count = len(contextValue.Tags)
	trace.RenderedChars = len([]rune(rendered))
	trace.Reason = "matched_persisted_customer_tags"
	setTrace()
}

func selectReplyTagScenes(currentText string, intent callbacks.IntentTraceData, replyPlan callbacks.ReplyPlanTraceData) ([]string, string) {
	currentText = strings.TrimSpace(currentTurnDisplayText(currentText))
	if currentText == "" {
		return nil, "no_text_generate"
	}
	if !intent.ShouldReply {
		return nil, "intent_no_reply"
	}
	if intent.PrimaryIntent == "interaction" {
		return nil, "interaction"
	}
	if intent.NeedsHumanRoute {
		return nil, "human_route"
	}
	if intent.PrimaryIntent == "hotel_variable" && !intent.NeedsKnowledge {
		return nil, "resource_only"
	}
	ret := make([]string, 0, 3)
	add := func(scene string) {
		if len(ret) >= 3 {
			return
		}
		if _, valid := replyTagContextScenes[scene]; !valid {
			return
		}
		for _, existing := range ret {
			if existing == scene {
				return
			}
		}
		ret = append(ret, scene)
	}
	textTaskCount := 0
	for _, task := range replyPlan.TaskPlans {
		if !replyTagTaskUsesGenerate(task) {
			continue
		}
		textTaskCount++
		text := task.Text
		if strings.TrimSpace(text) == "" && len(replyPlan.TaskPlans) == 1 {
			text = currentText
		}
		for _, scene := range replyTagScenesForTask(task.SubIntent, text) {
			add(scene)
		}
	}
	if len(replyPlan.TaskPlans) > 0 && textTaskCount == 0 {
		return nil, "resource_only"
	}
	if len(replyPlan.TaskPlans) == 0 {
		for _, task := range intent.IntentTasks {
			if task.NeedsResource || task.NeedsHumanRoute || task.Intent == "hotel_variable" || task.Intent == "human_complaint_risk" {
				continue
			}
			textTaskCount++
			for _, scene := range replyTagScenesForTask(task.SubIntent, task.Text) {
				add(scene)
			}
		}
	}
	if len(replyPlan.TaskPlans) == 0 && len(intent.IntentTasks) == 0 {
		for _, scene := range replyTagScenesForTask(intent.SubIntent, currentText) {
			add(scene)
		}
	}
	if len(ret) == 0 {
		return nil, "no_matching_scene"
	}
	return ret, ""
}

func replyTagTaskUsesGenerate(task callbacks.ReplyTaskPlanTraceData) bool {
	return replyTaskRequiresText(task)
}

func replyTagScenesForTask(subIntent, text string) []string {
	ret := make([]string, 0, 3)
	add := func(scene string) {
		for _, existing := range ret {
			if existing == scene {
				return
			}
		}
		ret = append(ret, scene)
	}
	subIntent = strings.ToLower(strings.TrimSpace(subIntent))
	text = strings.ToLower(strings.TrimSpace(text))
	switch subIntent {
	case "parking":
		add("parking_service")
	case "invoice", "store_info_invoice":
		add("invoice_service")
	case "checkin_process", "check_in", "checkin", "check_in_process":
		add("arrival_service")
	case "checkout_process", "check_out", "checkout", "check_out_process":
		add("checkout_service")
	}
	if containsReplyTagTerm(text, "停车", "车位", "停车场") {
		add("parking_service")
	}
	if containsReplyTagTerm(text, "发票", "开票", "专票", "普票") {
		add("invoice_service")
	}
	if containsReplyTagTerm(text, "办理入住", "入住流程", "怎么入住", "提前入住", "晚到", "早到") {
		add("arrival_service")
	}
	if containsReplyTagTerm(text, "退房", "延迟退房", "提前退房") {
		add("checkout_service")
	}
	if containsReplyTagTerm(text, "房型", "亲子房", "家庭房", "儿童房", "宠物房", "宠物友好房") {
		add("room_selection")
	}
	if containsReplyTagTerm(text, "安静", "喜静", "怕吵", "床型", "大床", "双床", "吸烟", "无烟", "楼层", "电梯", "有窗", "窗户") {
		add("room_assignment")
	}
	if containsReplyTagTerm(text, "连住", "续住", "续房") {
		add("stay_service")
	}
	if containsReplyTagTerm(text, "宠物", "带宠") {
		add("pet_service")
	}
	if containsReplyTagTerm(text, "枕头", "硬枕", "软枕", "送水", "矿泉水", "用品", "清洁", "打扫", "勿扰", "别打扰", "少打扰") {
		add("room_service")
	}
	return ret
}

func containsReplyTagTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func validateReplyTagContextV1(value replyTagContextV1) error {
	if !json.Valid([]byte(replyTagContextSchemaV1)) {
		return errors.New("reply tag context schema is invalid")
	}
	if value.SchemaVersion != replyTagContextSchemaVersion {
		return errors.New("invalid schema version")
	}
	if len(value.Scenes) > 3 || len(value.Tags) > 2 {
		return errors.New("reply tag context exceeds item limits")
	}
	seenScenes := make(map[string]struct{}, len(value.Scenes))
	for _, scene := range value.Scenes {
		if _, ok := replyTagContextScenes[scene]; !ok {
			return fmt.Errorf("invalid scene %q", scene)
		}
		if _, exists := seenScenes[scene]; exists {
			return fmt.Errorf("duplicate scene %q", scene)
		}
		seenScenes[scene] = struct{}{}
	}
	for _, item := range value.Tags {
		if item.TagID <= 0 || strings.TrimSpace(item.SemanticKey) == "" {
			return errors.New("invalid reply tag identity")
		}
		if count := len([]rune(strings.TrimSpace(item.Name))); count < 1 || count > 5 {
			return errors.New("invalid reply tag name")
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoded := replyTagContextV1{}
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := ensureReplyTagJSONEOF(decoder); err != nil {
		return err
	}
	if !reflect.DeepEqual(value, decoded) {
		return errors.New("reply tag context round-trip mismatch")
	}
	return nil
}

func ensureReplyTagJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("reply tag context contains trailing JSON")
		}
		return err
	}
	return nil
}

func renderReplyTagContext(value replyTagContextV1) string {
	if len(value.Tags) == 0 {
		return ""
	}
	names := make([]string, 0, len(value.Tags))
	for _, item := range value.Tags {
		names = append(names, strings.TrimSpace(item.Name))
	}
	return "低优先偏好：" + strings.Join(names, "、") + "；仅在与当前问题相关时自然参考，不得复述标签、提及来源或覆盖客户当前表达。"
}
