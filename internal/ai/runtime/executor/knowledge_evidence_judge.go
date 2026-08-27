package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"
)

const (
	knowledgeEvidenceJudgeSchemaVersion = "knowledge_evidence_judge.v2"

	knowledgeEvidenceDecisionDirectSingle   = "direct_single"
	knowledgeEvidenceDecisionDirectCombined = "direct_combined"
	knowledgeEvidenceDecisionPartial        = "partial"
	knowledgeEvidenceDecisionInsufficient   = "insufficient"

	knowledgeEvidenceLayerStore   = "store"
	knowledgeEvidenceLayerGeneral = "general"

	knowledgeEvidenceJudgeMaxTimeout      = 15 * time.Second
	knowledgeEvidenceJudgeMaxOutputTokens = 4096
)

type knowledgeEvidenceJudge interface {
	JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

type knowledgeEvidenceJudgeTask struct {
	TaskID        string
	Query         string
	Objective     string
	Entities      []knowledgeEvidenceJudgeEntity
	SourceContext []knowledgeEvidenceJudgeSourceMessage
	Candidates    []knowledgeEvidenceJudgeCandidate
}

type knowledgeEvidenceJudgeEntity struct {
	Text string
	Type string
}

type knowledgeEvidenceJudgeSourceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type knowledgeEvidenceJudgeCandidate struct {
	CandidateID string
	Layer       string
	Hit         rag.RetrieveResult
}

type knowledgeEvidenceJudgeOutcome struct {
	Applied    bool
	Selections map[string]map[string]knowledgeEvidenceLayerSelection
	Trace      callbacks.KnowledgeEvidenceJudgeTraceData
}

type knowledgeEvidenceLayerSelection struct {
	Decision             string
	SelectedCandidateIDs []string
	SupportedFacts       []knowledgeEvidenceFact
	MissingAspects       []string
}

type knowledgeEvidenceFact struct {
	FactID         string   `json:"factId"`
	Aspect         string   `json:"aspect"`
	Statement      string   `json:"statement"`
	CriticalValues []string `json:"criticalValues"`
}

type modelKnowledgeEvidenceJudge struct{}

type knowledgeEvidenceJudgePrompt struct {
	SchemaVersion string                             `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgePromptTask `json:"tasks"`
}

type knowledgeEvidenceJudgePromptTask struct {
	TaskID        string                                  `json:"taskId"`
	Question      string                                  `json:"question"`
	SourceContext []knowledgeEvidenceJudgeSourceMessage   `json:"sourceContext,omitempty"`
	Candidates    []knowledgeEvidenceJudgePromptCandidate `json:"candidates"`
}

type knowledgeEvidenceJudgePromptCandidate struct {
	CandidateID string  `json:"candidateId"`
	Layer       string  `json:"layer"`
	FAQQuestion string  `json:"faqQuestion,omitempty"`
	FAQAnswer   string  `json:"faqAnswer,omitempty"`
	Title       string  `json:"title,omitempty"`
	RawContent  string  `json:"rawContent"`
	Score       float32 `json:"score"`
}

type knowledgeEvidenceJudgeResponse struct {
	SchemaVersion string                               `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgeResponseTask `json:"tasks"`
}

type knowledgeEvidenceJudgeResponseTask struct {
	TaskID string                                `json:"taskId"`
	Layers []knowledgeEvidenceJudgeResponseLayer `json:"layers"`
}

type knowledgeEvidenceJudgeResponseLayer struct {
	Layer                string                  `json:"layer"`
	Decision             string                  `json:"decision"`
	SelectedCandidateIDs []string                `json:"selectedCandidateIds"`
	SupportedFacts       []knowledgeEvidenceFact `json:"supportedFacts"`
	MissingAspects       []string                `json:"missingAspects"`
}

func (modelKnowledgeEvidenceJudge) JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	prompt := buildKnowledgeEvidenceJudgePrompt(tasks)
	fingerprint := fingerprintKnowledgeEvidenceJudgePrompt(prompt)
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{
		SchemaVersion:        knowledgeEvidenceJudgeSchemaVersion,
		Status:               "fallback",
		Reason:               "judge was not completed; unselected retrieval will be withheld and existing handoff routing will be used",
		CandidateFingerprint: fingerprint,
		TaskCount:            len(prompt.Tasks),
		CandidateCount:       countKnowledgeEvidenceJudgeCandidates(prompt),
	}
	if len(prompt.Tasks) == 0 {
		trace.Status = "skipped"
		trace.Reason = "no retrieved candidate required evidence judging"
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, services.StoreAIModelUsageKnowledgeJudgeLLM, 0)
	if err != nil || resolved == nil {
		trace.Reason = "knowledge judge model is unavailable; unselected retrieval will be withheld and existing handoff routing will be used"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}
	config := normalizeKnowledgeEvidenceJudgeConfig(resolved.Config, len(prompt.Tasks))
	trace.Model = config.ModelName
	if strings.TrimSpace(config.ModelName) == "" || strings.TrimSpace(string(config.Provider)) == "" {
		trace.Reason = "knowledge judge model configuration is incomplete; unselected retrieval will be withheld and existing handoff routing will be used"
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	systemPrompt := knowledgeEvidenceJudgeSystemPrompt()
	userPrompt, err := json.Marshal(prompt)
	if err != nil {
		trace.Reason = "knowledge judge prompt could not be encoded; unselected retrieval will be withheld and existing handoff routing will be used"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	callCtx := knowledgeEvidenceJudgeUsageContext(ctx, req, resolved)
	callCtx, capture := usagex.WithCapture(callCtx)
	callCtx, cancel := context.WithTimeout(callCtx, time.Duration(config.TimeoutMS)*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	result, callErr := ai.LLM.ChatWithConfig(callCtx, config, systemPrompt, string(userPrompt))
	trace.LatencyMs = time.Since(startedAt).Milliseconds()
	recordKnowledgeEvidenceJudgeUsage(callCtx, req, config, result, lastKnowledgeEvidenceJudgeReceipt(capture), fingerprint, trace.LatencyMs, callErr)
	if callErr != nil {
		trace.Reason = "knowledge judge model call failed; unselected retrieval will be withheld and existing handoff routing will be used"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(callErr)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	selections, parseErr := parseKnowledgeEvidenceJudgeResponse(result.Content, tasks)
	if parseErr != nil {
		trace.Reason = "knowledge judge returned an invalid protocol response; unselected retrieval will be withheld and existing handoff routing will be used"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(parseErr)
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}
	repaired := repairHighConfidenceInsufficientKnowledgeSelections(tasks, selections)
	trace.Status = "completed"
	trace.Reason = "knowledge evidence was selected once per task and layer before deterministic store priority"
	if repaired > 0 {
		trace.Reason += fmt.Sprintf("; repaired %d high-confidence false-insufficient selection(s) from grounded affirmative enumeration", repaired)
	}
	return knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: selections,
		Trace:      trace,
	}
}

func buildKnowledgeEvidenceJudgePrompt(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgePrompt {
	prompt := knowledgeEvidenceJudgePrompt{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion}
	prompt.Tasks = make([]knowledgeEvidenceJudgePromptTask, 0, len(tasks))
	for _, task := range tasks {
		item := knowledgeEvidenceJudgePromptTask{
			TaskID:        strings.TrimSpace(task.TaskID),
			Question:      strings.TrimSpace(task.Query),
			SourceContext: append([]knowledgeEvidenceJudgeSourceMessage(nil), task.SourceContext...),
		}
		item.Candidates = make([]knowledgeEvidenceJudgePromptCandidate, 0, len(task.Candidates))
		for _, candidate := range task.Candidates {
			title := strings.TrimSpace(candidate.Hit.Title)
			if title == "" {
				title = strings.TrimSpace(candidate.Hit.DocumentTitle)
			}
			faqQuestion, faqAnswer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			rawContent := strings.TrimSpace(candidate.Hit.Content)
			if faqQuestion != "" && faqAnswer != "" {
				rawContent = "问题：" + faqQuestion + "\n答案：" + faqAnswer
			}
			item.Candidates = append(item.Candidates, knowledgeEvidenceJudgePromptCandidate{
				CandidateID: strings.TrimSpace(candidate.CandidateID),
				Layer:       strings.TrimSpace(candidate.Layer),
				FAQQuestion: faqQuestion,
				FAQAnswer:   faqAnswer,
				Title:       title,
				RawContent:  rawContent,
				Score:       candidate.Hit.Score,
			})
		}
		prompt.Tasks = append(prompt.Tasks, item)
	}
	return prompt
}

func splitKnowledgeEvidenceFAQ(hit rag.RetrieveResult) (string, string) {
	return splitKnowledgeEvidenceFAQForQuery(hit, strings.TrimSpace(hit.FaqQuestion))
}

type knowledgeEvidenceFAQUnit struct {
	Question string
	Answer   string
}

func splitKnowledgeEvidenceFAQForQuery(hit rag.RetrieveResult, query string) (string, string) {
	raw := strings.TrimSpace(hit.Content)
	units := parseKnowledgeEvidenceFAQUnits(raw)
	if len(units) > 0 {
		if preferred := strings.TrimSpace(hit.FaqQuestion); preferred != "" {
			if index := bestKnowledgeEvidenceFAQUnitIndex(units, preferred); index >= 0 && knowledgeEvidenceFAQQuestionMatchScore(units[index].Question, preferred) >= 0.82 {
				return units[index].Question, units[index].Answer
			}
		}
		if index := bestKnowledgeEvidenceFAQUnitIndex(units, query); index >= 0 {
			return units[index].Question, units[index].Answer
		}
		return units[0].Question, units[0].Answer
	}
	question := normalizeKnowledgeEvidenceFAQQuestion(strings.TrimSpace(hit.FaqQuestion))
	if question != "" {
		return question, trimKnowledgeEvidenceFAQMetadata(raw)
	}
	return "", ""
}

func knowledgeEvidenceHitForQuery(hit rag.RetrieveResult, query string) rag.RetrieveResult {
	question, answer := splitKnowledgeEvidenceFAQForQuery(hit, query)
	if question == "" || answer == "" {
		return hit
	}
	hit.FaqQuestion = question
	hit.Content = "问题：" + question + "\n答案：" + answer
	return hit
}

func parseQuestionAnswerContent(raw string) (string, string, bool) {
	units := parseKnowledgeEvidenceFAQUnits(raw)
	if len(units) == 0 {
		return "", "", false
	}
	return units[0].Question, units[0].Answer, true
}

var knowledgeEvidenceFAQQuestionMarkerPattern = regexp.MustCompile(`(?m)(?:^|\n)[ \t]*(?:问题|问|Q|q)[ \t]*[:：][ \t]*`)
var knowledgeEvidenceFAQAnswerMarkerPattern = regexp.MustCompile(`(?m)(?:^|\n)?[ \t]*(?:答案|答|A|a)[ \t]*[:：][ \t]*`)
var knowledgeEvidenceFAQMetadataLinePattern = regexp.MustCompile(`(?im)(?:^|\n)[ \t]*(?:#{1,6}[ \t]*)?(?:相似问题|相似问法|训练问题|训练问法|扩展问题|扩展问法|召回问题|命中问题|训练元数据|关键词|标签|metadata)[ \t]*[:：]`)

func trimKnowledgeEvidenceFAQMetadata(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}
	if index := knowledgeEvidenceFAQMetadataLinePattern.FindStringIndex(raw); index != nil {
		raw = raw[:index[0]]
	}
	return strings.TrimSpace(raw)
}

func normalizeKnowledgeEvidenceFAQQuestion(raw string) string {
	question := trimKnowledgeEvidenceFAQMetadata(raw)
	for {
		match := knowledgeEvidenceFAQQuestionMarkerPattern.FindStringIndex(question)
		if match == nil || match[0] != 0 {
			break
		}
		question = strings.TrimSpace(question[match[1]:])
	}
	return question
}

func parseKnowledgeEvidenceFAQUnits(raw string) []knowledgeEvidenceFAQUnit {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	questionMarkers := knowledgeEvidenceFAQQuestionMarkerPattern.FindAllStringIndex(raw, -1)
	units := make([]knowledgeEvidenceFAQUnit, 0, len(questionMarkers))
	for index, marker := range questionMarkers {
		blockEnd := len(raw)
		if index+1 < len(questionMarkers) {
			blockEnd = questionMarkers[index+1][0]
		}
		block := raw[marker[1]:blockEnd]
		answerMarker := knowledgeEvidenceFAQAnswerMarkerPattern.FindStringIndex(block)
		if answerMarker == nil {
			continue
		}
		question := normalizeKnowledgeEvidenceFAQQuestion(block[:answerMarker[0]])
		answer := trimKnowledgeEvidenceFAQMetadata(block[answerMarker[1]:])
		if question != "" && answer != "" {
			units = append(units, knowledgeEvidenceFAQUnit{Question: question, Answer: answer})
		}
	}
	for index := 0; index+1 < len(units); index++ {
		units[index].Answer = trimKnowledgeEvidenceFAQTrailingHeading(units[index].Answer, units[index+1].Question)
	}
	return units
}

func trimKnowledgeEvidenceFAQTrailingHeading(answer string, nextQuestion string) string {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		trimmedHeading := strings.TrimSpace(strings.TrimLeft(last, "#-* "))
		if strings.HasPrefix(last, "#") || knowledgeEvidenceFAQQuestionMatchScore(trimmedHeading, nextQuestion) >= 0.72 {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func bestKnowledgeEvidenceFAQUnitIndex(units []knowledgeEvidenceFAQUnit, query string) int {
	query = strings.TrimSpace(query)
	if len(units) == 0 || query == "" {
		return -1
	}
	bestIndex := -1
	bestScore := 0.0
	for index, unit := range units {
		score := knowledgeEvidenceFAQQuestionMatchScore(unit.Question, query)
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	return bestIndex
}

func knowledgeEvidenceFAQQuestionMatchScore(question string, query string) float64 {
	question = normalizeKnowledgeEvidenceQuestionForMatch(question)
	query = normalizeKnowledgeEvidenceQuestionForMatch(query)
	if question == "" || query == "" {
		return 0
	}
	if question == query {
		return 1
	}
	questionLen := len([]rune(question))
	queryLen := len([]rune(query))
	shorter, longer := questionLen, queryLen
	if shorter > longer {
		shorter, longer = longer, shorter
	}
	if shorter >= 4 && (strings.Contains(question, query) || strings.Contains(query, question)) {
		return 0.9 * float64(shorter) / float64(longer)
	}
	return knowledgeEvidenceTextNGramSimilarity(question, query)
}

func normalizeKnowledgeEvidenceQuestionForMatch(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	for _, prefix := range []string{"你们酒店的", "你们酒店", "咱们酒店的", "咱们酒店", "本酒店的", "本酒店", "酒店的", "酒店", "门店的", "门店"} {
		if remainder := strings.TrimPrefix(compact, prefix); remainder != compact && len([]rune(remainder)) >= 4 {
			compact = remainder
			break
		}
	}
	for _, phrase := range []string{"应该怎么填写", "应该怎么填", "要怎么填写", "要怎么填", "如何填写", "如何填", "怎么填写", "怎么填", "填写哪些", "填写什么", "填哪些", "填什么"} {
		compact = strings.ReplaceAll(compact, phrase, "填写内容")
	}
	return compact
}

func knowledgeEvidenceJudgeSystemPrompt() string {
	return strings.TrimSpace(`你是酒店客服知识证据裁判。你不回答客户，不决定是否转人工，只为每个客户任务在每个知识层选择足以回答的证据。

每个 task 会提供当前原子问题、紧邻会话 sourceContext，以及带 layer 的候选。sourceContext 只用于理解“这几个、上面那种、都”等指代，不能当作酒店事实来源。

事实维度完整性检查是每个 task、每个 layer 的必做步骤：
1. 先把客户当前原子问题拆成内部事实维度清单。例如同一句同时询问是否存在、数量、费用、时间、位置、方法、范围或条件时，每个维度都必须单独列入检查；这个内部清单不要作为额外字段输出。
2. 对当前 layer 提供的全部候选逐条检查，每条候选的 faqQuestion、faqAnswer 和 rawContent 都要核对它能支持清单中的哪些维度，不能在看到第一条相关候选后提前停止。
3. 不同候选分别补齐不同事实维度，且属于同一门店、同一对象和同一适用范围时，必须判 direct_combined，并选中所有补齐答案所必需的同层候选。
4. 只有检查完当前 layer 的全部候选，仍有清单维度没有任何候选能够补齐时，才允许判 partial，并且 missingAspects 只能写这些真实缺失的维度。只要同层还有候选能补齐 missingAspects，就不得判 partial。

必须分别裁决 store 和 general 两层，每层只能输出一种 decision：
- direct_single：单条候选的完整语义足以回答当前问题，只选择这一条。
- direct_combined：同一层内至少两条候选指向同一门店、同一实体和同一适用范围，合在一起足以回答当前问题，只选择必要的候选。
- partial：同一层内已确认一部分有用事实，但仍缺少当前问题要求的一个或多个事实维度。只选择支持已确认事实的必要候选。
- insufficient：该层没有足够证据，selectedCandidateIds 必须为空。

每层还必须输出 supportedFacts 和 missingAspects：
- supportedFacts 只能写 selectedCandidateIds 原文明示或完整 FAQ 问答明确确认的原子事实。每条必须包含 factId、aspect、statement、criticalValues。
- factId 在同一个 task 的同一知识层内必须唯一；aspect 只能是 existence、quantity、price、time、location、method、scope、condition、other。
- statement 必须是可直接给后续回复使用的完整事实句，不要写推理过程。criticalValues 只列回复不可丢失的数量、金额、时间、电话、地址、房型名或其他关键原文；没有则输出空数组。
- missingAspects 只写客户当前问题仍然缺失的事实维度或条件，使用简短中文短语。
- direct_single/direct_combined 必须至少有一条 supportedFacts，且 missingAspects 为空；唯一例外是选中单条“转接/转人工”流程指令时，supportedFacts 和 missingAspects 都必须为空。
- partial 必须同时包含至少一条 supportedFacts 和一条 missingAspects。
- insufficient 的 selectedCandidateIds 和 supportedFacts 必须为空；missingAspects 可以用简短短语说明当前层缺少什么，没有必要时输出空数组。

严禁跨 store/general 拼接证据，也不能把不同门店、不同房型对象、不同时间条件或互相矛盾的内容组合。检索分数和候选顺序不能替代语义判断。

FAQ 必须把 faqQuestion 和 faqAnswer 作为一个完整问答来理解。答案出现“是的、可以、不需要、没有”等省略表达时，可以结合 FAQ 问题还原其中已经被明确确认的对象、数量、条件和结论；不得补出 FAQ 问答没有确认的事实。rawContent 只用于核对原文。

肯定枚举中的精确成员属于明确存在性证据。例如“部分房型配备办公桌，如合柴、麦田和艺林”已经明确支持“麦田房型有办公桌”；不能因为总述使用“部分房型”就把枚举内成员判为 insufficient。只有成员名称、所问设施或能力、肯定关系都在同一条 FAQ 原文中明确出现时才能使用，不能把相似名称、条件性描述或其他事实维度当成枚举成员。

完整答案单元规则：FAQ 中与客户当前问题直接相关的事实、适用条件、操作建议、选择或比较方法，都是彼此独立的答案单元，必须分别进入 supportedFacts，不能只保留结论而省略后续怎么做。操作建议和选择方法通常使用 method 或 condition；其中会改变客户下一步行动、且回复时不可丢失的关键动作原文或短语，也必须进入对应事实的 criticalValues。与当前问题无关的延伸内容不得为了凑齐答案而加入。

对每个 selectedCandidateIds 对应的 faqAnswer，必须按句号、分号、逗号等语义边界逐个检查独立事实子句。一个答案同时包含否定/能力边界与办理方法、时间与地点、数量与费用等多个子句时，每个与当前问题有关的子句都必须由独立 supportedFacts 覆盖，不能只保留后半句的方法而漏掉前半句边界。否定对象、数量、金额、时间、电话、地址等不可遗漏的原文字面值必须进入对应 fact 的 criticalValues。

例如 FAQ 问题“问下房间的两瓶矿泉水是免费的吗？”、答案“是的，房间内的矿泉水都是免费的”，完整语义已经确认“房间内有两瓶矿泉水，并且免费”。它足以回答“房间里有几瓶矿泉水”，应判 direct_single；不能因为数量只写在 faqQuestion 中就丢掉这个已被肯定回答确认的事实。这个规则同样适用于其他 FAQ 中被肯定或否定答案确认的对象、数量与条件。

候选答案如果只是“转接”，它是流程指令，不是酒店事实。只有 FAQ 问题与当前任务语义直接匹配时，才可以把该候选作为 direct_single 单条流程指令选择，此时 supportedFacts 和 missingAspects 都输出空数组；绝不能把 FAQ 问题文字当作已经确认的事实，也不能让“转接”候选参与 direct_combined。

事实维度必须严格隔离：确认“有外卖机器人”只支持 existence，不能生成“能送到房间”的 scope 或 method；确认地点名称只支持 existence/location，不能生成距离、步行时间或路线；确认有充电桩不能推导所有车位都能充电。客户询问了这些未被证据确认的维度时，应判 partial 并把对应维度写入 missingAspects。

同层组合示例：客户问“既有沙发又有办公桌的房型有哪些”，一条候选列出有沙发的房型，另一条候选列出有办公桌的房型，两条属于同一门店和房型范围时，可以判 direct_combined，让后续生成阶段计算交集。只知道沙发或只知道办公桌时应判 partial，保留已确认事实，同时明确缺少另一项设施事实。

否定答案也可以完整回答问题。例如“早餐几点”对应“酒店不提供早餐”可以判 direct_single。必须区分能力/存在性与故障/执行请求，例如“有空调吗”不能选择“空调不制冷需要处理”。

严格输出 JSON，不要 Markdown、解释或额外字段。必须原样返回每个 taskId；对输入实际包含的每个 layer 恰好返回一次。输出格式：
{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内的矿泉水免费。","criticalValues":["免费"]}],"missingAspects":[]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":[],"missingAspects":["没有可用于回答当前问题的通用知识证据"]}]}]}`)
}

func parseKnowledgeEvidenceJudgeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	normalized, err := normalizeKnowledgeEvidenceJudgeResponseJSON(raw)
	if err != nil {
		return nil, err
	}
	parsed := knowledgeEvidenceJudgeResponse{}
	decoder := json.NewDecoder(strings.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode knowledge judge response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("knowledge judge response contains trailing content")
	}
	if parsed.SchemaVersion != knowledgeEvidenceJudgeSchemaVersion {
		return nil, fmt.Errorf("unexpected knowledge judge schema version %q", parsed.SchemaVersion)
	}
	expected := make(map[string]map[string]map[string]struct{}, len(tasks))
	expectedTasks := make(map[string]knowledgeEvidenceJudgeTask, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return nil, fmt.Errorf("knowledge judge task id is empty")
		}
		layerCandidates := make(map[string]map[string]struct{}, 2)
		for _, candidate := range task.Candidates {
			candidateID := strings.TrimSpace(candidate.CandidateID)
			if candidateID == "" {
				return nil, fmt.Errorf("knowledge judge candidate id is empty for task %s", taskID)
			}
			layer := strings.TrimSpace(candidate.Layer)
			if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
				return nil, fmt.Errorf("invalid knowledge judge layer %q for candidate %s", layer, candidateID)
			}
			if layerCandidates[layer] == nil {
				layerCandidates[layer] = make(map[string]struct{})
			}
			if _, exists := layerCandidates[layer][candidateID]; exists {
				return nil, fmt.Errorf("duplicate expected candidate id %s", candidateID)
			}
			layerCandidates[layer][candidateID] = struct{}{}
		}
		expected[taskID] = layerCandidates
		expectedTasks[taskID] = task
	}

	ret := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	for taskID, expectedLayers := range expected {
		ret[taskID] = defaultKnowledgeEvidenceLayerSelections(expectedLayers)
	}
	seenTasks := make(map[string]bool, len(parsed.Tasks))
	invalidTasks := make(map[string]bool)
	for _, task := range parsed.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		expectedLayers, ok := expected[taskID]
		if !ok {
			continue
		}
		if seenTasks[taskID] {
			ret[taskID] = defaultKnowledgeEvidenceLayerSelections(expectedLayers)
			invalidTasks[taskID] = true
			continue
		}
		seenTasks[taskID] = true
		if invalidTasks[taskID] {
			continue
		}
		selections := ret[taskID]
		seenLayers := make(map[string]bool, len(task.Layers))
		invalidLayers := make(map[string]bool)
		for _, layerResult := range task.Layers {
			layer := strings.TrimSpace(layerResult.Layer)
			expectedCandidates, ok := expectedLayers[layer]
			if !ok {
				continue
			}
			if seenLayers[layer] {
				selections[layer] = insufficientKnowledgeEvidenceLayerSelection()
				invalidLayers[layer] = true
				continue
			}
			seenLayers[layer] = true
			if invalidLayers[layer] {
				continue
			}
			selections[layer] = normalizeParsedKnowledgeEvidenceLayerSelection(
				taskID,
				layer,
				layerResult,
				expectedCandidates,
				expectedTasks[taskID],
			)
		}
		ret[taskID] = selections
	}
	return ret, nil
}

func defaultKnowledgeEvidenceLayerSelections(expectedLayers map[string]map[string]struct{}) map[string]knowledgeEvidenceLayerSelection {
	selections := make(map[string]knowledgeEvidenceLayerSelection, len(expectedLayers))
	for layer := range expectedLayers {
		selections[layer] = insufficientKnowledgeEvidenceLayerSelection()
	}
	return selections
}

func insufficientKnowledgeEvidenceLayerSelection() knowledgeEvidenceLayerSelection {
	return knowledgeEvidenceLayerSelection{Decision: knowledgeEvidenceDecisionInsufficient}
}

func normalizeParsedKnowledgeEvidenceLayerSelection(
	taskID string,
	layer string,
	layerResult knowledgeEvidenceJudgeResponseLayer,
	expectedCandidates map[string]struct{},
	expectedTask knowledgeEvidenceJudgeTask,
) knowledgeEvidenceLayerSelection {
	insufficient := insufficientKnowledgeEvidenceLayerSelection()
	decision := strings.TrimSpace(layerResult.Decision)
	switch decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined, knowledgeEvidenceDecisionPartial, knowledgeEvidenceDecisionInsufficient:
	default:
		return insufficient
	}

	selectedIDs := make([]string, 0, len(layerResult.SelectedCandidateIDs))
	seenSelected := make(map[string]struct{}, len(layerResult.SelectedCandidateIDs))
	for _, rawCandidateID := range layerResult.SelectedCandidateIDs {
		candidateID := strings.TrimSpace(rawCandidateID)
		if _, ok := expectedCandidates[candidateID]; !ok {
			return insufficient
		}
		if _, exists := seenSelected[candidateID]; exists {
			return insufficient
		}
		seenSelected[candidateID] = struct{}{}
		selectedIDs = append(selectedIDs, candidateID)
	}
	supportedFacts, err := normalizeKnowledgeEvidenceFacts(taskID, layer, layerResult.SupportedFacts, make(map[string]struct{}))
	if err != nil {
		return insufficient
	}
	if len(selectedIDs) > 0 {
		supportedFacts = groundedKnowledgeEvidenceFacts(expectedTask, layer, selectedIDs, supportedFacts)
		supportedFacts = enrichKnowledgeEvidenceFactsFromSelectedFAQs(expectedTask, layer, selectedIDs, supportedFacts)
	}
	supportedFacts = filterKnowledgeEvidenceFactsForTask(expectedTask, supportedFacts)
	missingAspects, err := normalizeKnowledgeEvidenceMissingAspects(taskID, layer, layerResult.MissingAspects)
	if err != nil {
		return insufficient
	}
	if decision == knowledgeEvidenceDecisionDirectCombined && len(selectedIDs) == 1 {
		decision = knowledgeEvidenceDecisionDirectSingle
	}
	selectedHandoff := selectedKnowledgeEvidenceIsHandoffDirective(expectedTask, layer, selectedIDs)
	switch decision {
	case knowledgeEvidenceDecisionInsufficient:
		if len(selectedIDs) != 0 || len(supportedFacts) != 0 {
			return insufficient
		}
	case knowledgeEvidenceDecisionDirectSingle:
		if len(selectedIDs) != 1 || len(missingAspects) != 0 || (!selectedHandoff && len(supportedFacts) == 0) || (selectedHandoff && len(supportedFacts) != 0) {
			return insufficient
		}
	case knowledgeEvidenceDecisionDirectCombined:
		if len(selectedIDs) < 2 || selectedHandoff || len(supportedFacts) == 0 || len(missingAspects) != 0 {
			return insufficient
		}
	case knowledgeEvidenceDecisionPartial:
		if len(selectedIDs) == 0 || selectedHandoff || len(supportedFacts) == 0 || len(missingAspects) == 0 {
			return insufficient
		}
	}
	if !selectedHandoff {
		missingRequired := missingRequiredKnowledgeEvidenceAspects(expectedTask, supportedFacts)
		if len(missingRequired) > 0 {
			missingAspects = appendKnowledgeEvidenceMissingAspects(missingAspects, missingRequired)
			if len(selectedIDs) == 0 || len(supportedFacts) == 0 {
				return insufficient
			}
			decision = knowledgeEvidenceDecisionPartial
		}
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             decision,
		SelectedCandidateIDs: selectedIDs,
		SupportedFacts:       supportedFacts,
		MissingAspects:       missingAspects,
	}
}

func groundedKnowledgeEvidenceFacts(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	evidenceUnits := make([][]string, 0, len(selectedCandidateIDs))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		parts := []string{answer}
		if knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
			parts = append(parts, strings.TrimSpace(question+" "+answer))
		}
		if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
			parts = []string{candidate.Hit.Content}
		}
		unit := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				unit = append(unit, part)
			}
		}
		if len(unit) > 0 {
			evidenceUnits = append(evidenceUnits, unit)
		}
	}
	if len(evidenceUnits) == 0 {
		return nil
	}

	grounded := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, fact := range facts {
		for _, evidenceUnit := range evidenceUnits {
			if knowledgeEvidenceFactGroundedByText(fact, evidenceUnit) {
				grounded = append(grounded, fact)
				break
			}
		}
	}
	return grounded
}

func knowledgeEvidenceFactGroundedByText(fact knowledgeEvidenceFact, evidenceParts []string) bool {
	statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
	if statement == "" {
		return false
	}
	for _, value := range fact.CriticalValues {
		normalizedValue := normalizeRuntimeKnowledgeQuery(value)
		if normalizedValue == "" || !knowledgeEvidencePartsContainValue(evidenceParts, normalizedValue) {
			return false
		}
	}

	bestSimilarity := 0.0
	polarityMatched := false
	statementNegative := knowledgeEvidenceTextHasNegativeBoundary(statement)
	for _, part := range evidenceParts {
		for _, unit := range append([]string{part}, splitKnowledgeEvidenceAnswerClauses(part)...) {
			normalizedUnit := normalizeRuntimeKnowledgeQuery(unit)
			if normalizedUnit == "" {
				continue
			}
			if strings.Contains(normalizedUnit, statement) || strings.Contains(statement, normalizedUnit) {
				if statementNegative == knowledgeEvidenceTextHasNegativeBoundary(normalizedUnit) {
					return true
				}
				continue
			}
			similarity := knowledgeEvidenceTextNGramSimilarity(statement, normalizedUnit)
			if similarity > bestSimilarity {
				bestSimilarity = similarity
				polarityMatched = statementNegative == knowledgeEvidenceTextHasNegativeBoundary(normalizedUnit)
			}
		}
	}
	minimumSimilarity := 0.46
	if len(fact.CriticalValues) > 0 {
		minimumSimilarity = 0.28
	}
	return polarityMatched && bestSimilarity >= minimumSimilarity
}

func knowledgeEvidencePartsContainValue(parts []string, normalizedValue string) bool {
	for _, part := range parts {
		if strings.Contains(normalizeRuntimeKnowledgeQuery(part), normalizedValue) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerConfirmsQuestion(answer string) bool {
	answer = strings.TrimSpace(answer)
	for _, prefix := range []string{"是的", "对的", "没错", "有的"} {
		if !strings.HasPrefix(answer, prefix) {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(answer, prefix))
		if remainder == "" || strings.ContainsRune("，,。.!！；;：:", []rune(remainder)[0]) {
			return true
		}
	}
	return false
}

func enrichKnowledgeEvidenceFactsFromSelectedFAQs(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	facts []knowledgeEvidenceFact,
) []knowledgeEvidenceFact {
	required := requiredKnowledgeEvidenceAspects(task)
	if len(required) == 0 || len(selectedCandidateIDs) == 0 {
		return facts
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, facts)
	}
	return facts
}

func enrichKnowledgeEvidenceFactsFromFAQUnit(task knowledgeEvidenceJudgeTask, question string, answer string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if !knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
		return facts
	}
	statement := affirmativeKnowledgeEvidenceQuestionStatement(question)
	if statement == "" {
		return facts
	}
	seenFactIDs := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	for _, aspect := range requiredKnowledgeEvidenceAspects(task) {
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			continue
		}
		criticalValues := confirmedKnowledgeEvidenceQuestionCriticalValues(aspect, question, answer)
		if len(criticalValues) == 0 {
			continue
		}
		fact := knowledgeEvidenceFact{
			FactID:         nextKnowledgeEvidenceFactID(task.TaskID, seenFactIDs),
			Aspect:         aspect,
			Statement:      statement,
			CriticalValues: criticalValues,
		}
		if !knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			continue
		}
		seenFactIDs[fact.FactID] = struct{}{}
		facts = append(facts, fact)
	}
	return facts
}

func affirmativeKnowledgeEvidenceQuestionStatement(question string) string {
	statement := strings.TrimSpace(question)
	statement = strings.Trim(statement, " 。！!？?")
	for _, prefix := range []string{"请问一下", "请问", "问一下", "问下", "想问一下", "想问"} {
		statement = strings.TrimSpace(strings.TrimPrefix(statement, prefix))
	}
	statement = strings.TrimSuffix(statement, "吗")
	statement = strings.TrimSuffix(statement, "嘛")
	statement = strings.TrimSuffix(statement, "么")
	statement = strings.ReplaceAll(statement, "是不是", "是")
	statement = strings.ReplaceAll(statement, "是否", "")
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return ""
	}
	return statement + "。"
}

func confirmedKnowledgeEvidenceQuestionCriticalValues(aspect string, question string, answer string) []string {
	combined := strings.TrimSpace(question + " " + answer)
	values := make([]string, 0, 2)
	switch aspect {
	case "quantity":
		for _, match := range knowledgeEvidenceStrictQuantityPattern.FindAllString(question, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	case "price":
		compact := normalizeRuntimeKnowledgeQuery(combined)
		for _, value := range []string{"免费", "收费"} {
			if strings.Contains(compact, value) {
				values = appendIfMissing(values, value)
			}
		}
		for _, match := range knowledgeEvidencePriceValuePattern.FindAllString(combined, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	case "time":
		for _, match := range knowledgeEvidenceAnswerTimePattern.FindAllString(combined, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	}
	return values
}

func filterKnowledgeEvidenceFactsForTask(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 {
		return nil
	}
	required := requiredKnowledgeEvidenceAspects(task)
	ret := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, rawFact := range facts {
		fact := narrowKnowledgeEvidenceFactToTask(task, rawFact, required)
		if knowledgeEvidenceFactIsMarketingFiller(fact.Statement) {
			continue
		}
		if len(required) == 0 {
			if fact.Aspect != "other" {
				ret = append(ret, fact)
			}
			continue
		}
		keep := false
		for _, aspect := range required {
			if knowledgeEvidenceFactSupportsAspect(fact, aspect) {
				keep = true
				break
			}
		}
		if !keep && fact.Aspect == "existence" && knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) && knowledgeEvidenceNegativeFactAnswersTask(task, fact) {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "scope") && fact.Aspect == "existence" {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "method") && fact.Aspect == "existence" && knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "price") && (knowledgeEvidenceQueryAsksComparison(task.Query) || knowledgeEvidenceQueryAsksPriceBoundary(task.Query)) {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if (fact.Aspect == "condition" || fact.Aspect == "scope") && containsAny(compact, []string{"平台", "权益", "不同", "调整"}) {
				keep = true
			}
			if fact.Aspect == "condition" && containsAny(compact, []string{"情况", "为准", "而定", "取决于"}) {
				keep = true
			}
			if fact.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择", "联系"}) {
				keep = true
			}
		}
		if keep {
			ret = append(ret, fact)
		}
	}
	return ret
}

func narrowKnowledgeEvidenceFactToTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, required []string) knowledgeEvidenceFact {
	if len(required) != 1 || required[0] != "existence" || fact.Aspect != "existence" {
		return fact
	}
	clauses := splitKnowledgeEvidenceAnswerClauses(fact.Statement)
	if len(clauses) < 2 {
		return fact
	}
	for _, clause := range clauses {
		candidate := fact
		candidate.Statement = strings.TrimSpace(clause) + "。"
		candidate.CriticalValues = filterKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, candidate.Statement)
		if knowledgeEvidenceFactSupportsAspect(candidate, "existence") {
			return candidate
		}
	}
	return fact
}

func filterKnowledgeEvidenceCriticalValuesForStatement(values []string, statement string) []string {
	ret := make([]string, 0, len(values))
	compact := normalizeRuntimeKnowledgeQuery(statement)
	for _, value := range values {
		if strings.Contains(compact, normalizeRuntimeKnowledgeQuery(value)) {
			ret = appendIfMissing(ret, value)
		}
	}
	return ret
}

func requiredKnowledgeEvidenceAspects(task knowledgeEvidenceJudgeTask) []string {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	ret := make([]string, 0, 3)
	appendAspect := func(aspect string) {
		if aspect != "" && !knowledgeEvidenceContainsString(ret, aspect) {
			ret = append(ret, aspect)
		}
	}
	switch semanticGateNormalizeObjective(task.Objective) {
	case "availability":
		appendAspect("existence")
	case "quantity":
		appendAspect("quantity")
	case "price":
		appendAspect("price")
	case "time":
		appendAspect("time")
	case "location":
		appendAspect("location")
	case "method":
		if strings.Contains(query, "怎么填") {
			appendAspect("location")
		} else {
			appendAspect("method")
		}
	}
	if containsAny(query, []string{"几瓶", "几个", "几间", "几台", "几条", "几套", "几双", "几把", "几包", "几盒", "几袋", "几件", "几支", "几只", "几辆", "几杯", "几桶", "几卷", "多少瓶", "多少个", "多少台", "多少条", "多少套", "多少双", "多少把", "多少包", "多少盒", "多少袋", "多少件", "多少支", "多少只", "多少辆", "多少杯", "多少桶", "多少卷", "数量"}) {
		appendAspect("quantity")
	}
	if containsAny(query, []string{"免费", "收费", "多少钱", "价格", "费用", "钱", "价"}) {
		appendAspect("price")
	}
	if containsAny(query, []string{"几点", "多久", "什么时候", "何时", "时间"}) {
		appendAspect("time")
	}
	if containsAny(query, []string{"在哪", "哪里", "地址", "位置", "楼层", "怎么填"}) {
		appendAspect("location")
	}
	if !strings.Contains(query, "怎么填") && containsAny(query, []string{"怎么", "如何", "怎样", "办理", "操作", "打开"}) {
		appendAspect("method")
	}
	if containsAny(query, []string{"有没有", "是否有", "有吗", "配备", "提供吗"}) {
		appendAspect("existence")
	}
	if containsAny(query, []string{"送到", "哪些", "都有", "全部", "范围"}) {
		appendAspect("scope")
	}
	return ret
}

func knowledgeEvidenceFactSupportsAspect(fact knowledgeEvidenceFact, aspect string) bool {
	compact := normalizeRuntimeKnowledgeQuery(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
	switch aspect {
	case "quantity":
		return fact.Aspect == "quantity" && knowledgeEvidenceStrictQuantityPattern.MatchString(compact)
	case "price":
		return fact.Aspect == "price" && (containsAny(compact, []string{"免费", "收费", "价格", "费用", "金额"}) || knowledgeEvidencePriceValuePattern.MatchString(compact))
	case "time":
		return fact.Aspect == "time" && (knowledgeEvidenceAnswerTimePattern.MatchString(compact) || containsAny(compact, []string{"时间", "工作日", "分钟", "小时", "天", "点"}))
	case "location":
		return fact.Aspect == "location" && knowledgeEvidenceTextHasLocationCue(compact)
	case "method":
		return fact.Aspect == "method" && knowledgeEvidenceTextHasMethodCue(compact)
	case "scope":
		return fact.Aspect == "scope" && containsAny(compact, []string{"范围", "送到", "全部", "所有", "都", "仅限", "适用"})
	case "condition":
		return fact.Aspect == "condition" && containsAny(compact, []string{"如果", "条件", "取决于", "为准", "而定", "具体情况"})
	case "existence":
		return fact.Aspect == "existence" && containsAny(compact, []string{"有", "没有", "提供", "配备", "不提供", "无", "不含"})
	default:
		return fact.Aspect == aspect
	}
}

func knowledgeEvidenceFactsCoverRequiredAspect(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, aspect string) bool {
	for _, fact := range facts {
		if knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			return true
		}
		if fact.Aspect == "existence" && knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) && knowledgeEvidenceNegativeFactAnswersTask(task, fact) {
			return true
		}
	}
	if aspect == "price" && (knowledgeEvidenceQueryAsksComparison(task.Query) || knowledgeEvidenceQueryAsksPriceBoundary(task.Query)) {
		for _, fact := range facts {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if fact.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择"}) {
				return true
			}
			if (fact.Aspect == "condition" || fact.Aspect == "scope") && containsAny(compact, []string{"平台", "权益", "不同", "调整"}) {
				return true
			}
			if fact.Aspect == "condition" && containsAny(compact, []string{"情况", "为准", "而定", "取决于"}) {
				return true
			}
		}
	}
	return false
}

func knowledgeEvidenceTextHasLocationCue(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return knowledgeEvidenceLocationAnchor(text) != "" || containsAny(compact, []string{
		"地址", "位于", "楼", "层", "对面", "入口", "房间号", "门牌号",
		"洗衣房", "大厅", "前台旁", "旁边", "附近", "电梯口", "楼下", "楼上",
	})
}

func knowledgeEvidenceTextHasMethodCue(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{
		"通过", "使用", "扫码", "点击", "输入", "操作", "办理", "联系", "回复", "选择", "对比", "比较", "申请", "下载", "登记",
		"刷脸", "扫脸", "扫人脸", "人脸开门", "房卡开门", "密码开门", "入住机办理", "小程序办理", "开门",
	})
}

func knowledgeEvidenceNegativeFactAnswersTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact) bool {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
	if query == "" || statement == "" {
		return false
	}
	for _, entity := range task.Entities {
		value := normalizeRuntimeKnowledgeQuery(entity.Text)
		if len([]rune(value)) >= 2 && strings.Contains(query, value) && strings.Contains(statement, value) {
			return true
		}
	}
	statementRunes := []rune(statement)
	for index := 0; index+2 <= len(statementRunes); index++ {
		token := string(statementRunes[index : index+2])
		if containsAny(token, []string{"酒店", "没有", "暂不", "提供", "不提"}) {
			continue
		}
		if strings.Contains(query, token) {
			return true
		}
	}
	return knowledgeEvidenceTextNGramSimilarity(query, statement) >= 0.22
}

func missingRequiredKnowledgeEvidenceAspects(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []string {
	ret := make([]string, 0, 2)
	for _, aspect := range requiredKnowledgeEvidenceAspects(task) {
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			continue
		}
		ret = append(ret, knowledgeEvidenceAspectLabel(aspect))
	}
	return ret
}

func knowledgeEvidenceAspectLabel(aspect string) string {
	switch aspect {
	case "existence":
		return "是否存在"
	case "quantity":
		return "数量"
	case "price":
		return "费用"
	case "time":
		return "时间"
	case "location":
		return "位置"
	case "method":
		return "办理方式"
	case "scope":
		return "适用范围"
	case "condition":
		return "适用条件"
	default:
		return aspect
	}
}

func appendKnowledgeEvidenceMissingAspects(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !knowledgeEvidenceMissingAspectCovered(ret, value) {
			ret = append(ret, value)
		}
	}
	return ret
}

func knowledgeEvidenceMissingAspectCovered(existing []string, value string) bool {
	if knowledgeEvidenceContainsString(existing, value) {
		return true
	}
	markers := []string{value}
	switch value {
	case "适用范围":
		markers = []string{"范围", "送到"}
	case "数量":
		markers = []string{"数量", "几瓶", "几个"}
	case "费用":
		markers = []string{"费用", "收费", "免费", "价格"}
	}
	for _, item := range existing {
		compact := normalizeRuntimeKnowledgeQuery(item)
		if containsAny(compact, markers) {
			return true
		}
	}
	return false
}

func requiredKnowledgeEvidenceAspect(required []string, aspect string) bool {
	return knowledgeEvidenceContainsString(required, aspect)
}

func knowledgeEvidenceContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func knowledgeEvidenceQueryAsksComparison(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{"一样", "不同", "区别", "对比", "比较", "哪个", "哪家"})
}

func knowledgeEvidenceQueryAsksPriceBoundary(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{"优惠", "折扣", "优惠价", "老客户", "会员价"})
}

func knowledgeEvidenceFactIsMarketingFiller(statement string) bool {
	compact := normalizeRuntimeKnowledgeQuery(statement)
	if compact == "" {
		return true
	}
	for _, phrase := range []string{"帮您解放双手", "解放双手", "祝您入住愉快", "祝您旅途愉快", "宾至如归", "更舒适的体验", "更便捷的体验"} {
		if strings.Contains(compact, normalizeRuntimeKnowledgeQuery(phrase)) {
			return true
		}
	}
	return false
}

func repairHighConfidenceInsufficientKnowledgeSelections(tasks []knowledgeEvidenceJudgeTask, selections map[string]map[string]knowledgeEvidenceLayerSelection) int {
	repaired := 0
	for _, task := range tasks {
		requiredEntities := normalizedKnowledgeEvidenceEntities(task.Entities)
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selection, ok := selections[task.TaskID][layer]
			if !ok || (selection.Decision != knowledgeEvidenceDecisionInsufficient && selection.Decision != knowledgeEvidenceDecisionPartial) {
				continue
			}
			if semanticGateNormalizeObjective(task.Objective) == "availability" && len(requiredEntities) >= 2 {
				if repairedSelection, ok := highConfidenceKnowledgeConsensusSelection(task, layer, requiredEntities); ok {
					selections[task.TaskID][layer] = repairedSelection
					repaired++
					continue
				}
			}
			if repairedSelection, ok := highConfidenceDirectFAQSelection(task, layer); ok {
				selections[task.TaskID][layer] = repairedSelection
				repaired++
			}
		}
	}
	return repaired
}

func deterministicKnowledgeEvidenceJudgeFallbackSelections(tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, int, int) {
	selections := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	handoffs := 0
	for _, task := range tasks {
		layers := make(map[string]map[string]struct{}, 2)
		for _, candidate := range task.Candidates {
			layer := strings.TrimSpace(candidate.Layer)
			if layer == knowledgeEvidenceLayerStore || layer == knowledgeEvidenceLayerGeneral {
				if layers[layer] == nil {
					layers[layer] = make(map[string]struct{})
				}
				layers[layer][strings.TrimSpace(candidate.CandidateID)] = struct{}{}
			}
		}
		taskSelections := defaultKnowledgeEvidenceLayerSelections(layers)
		for layer := range layers {
			if selection, ok := deterministicKnowledgeEvidenceHandoffSelection(task, layer); ok {
				taskSelections[layer] = selection
				handoffs++
			}
		}
		selections[task.TaskID] = taskSelections
	}
	repairHighConfidenceInsufficientKnowledgeSelections(tasks, selections)
	groundedAnswers := 0
	for _, task := range tasks {
		taskSelections := selections[task.TaskID]
		if knowledgeEvidenceTaskHasLayerCandidates(task, knowledgeEvidenceLayerStore) && !selectionHasCompleteEvidence(taskSelections[knowledgeEvidenceLayerStore]) {
			// When the store layer returned candidates but none can be proven safe
			// locally, do not let a general fallback silently override it.
			taskSelections[knowledgeEvidenceLayerGeneral] = insufficientKnowledgeEvidenceLayerSelection()
		}
		for _, selection := range taskSelections {
			if selectionHasCompleteEvidence(selection) && len(selection.SupportedFacts) > 0 {
				groundedAnswers++
			}
		}
	}
	return selections, groundedAnswers, handoffs
}

func knowledgeEvidenceTaskHasLayerCandidates(task knowledgeEvidenceJudgeTask, layer string) bool {
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) == strings.TrimSpace(layer) {
			return true
		}
	}
	return false
}

func deterministicKnowledgeEvidenceHandoffSelection(task knowledgeEvidenceJudgeTask, layer string) (knowledgeEvidenceLayerSelection, bool) {
	var best *knowledgeEvidenceJudgeCandidate
	for index := range task.Candidates {
		candidate := &task.Candidates[index]
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if best == nil || candidate.Hit.Score > best.Hit.Score {
			best = candidate
		}
	}
	if best == nil {
		return knowledgeEvidenceLayerSelection{}, false
	}
	_, answer := splitKnowledgeEvidenceFAQForQuery(best.Hit, task.Query)
	if !isKnowledgeHandoffDirectiveContent(answer) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{best.CandidateID},
	}, true
}

func highConfidenceDirectFAQSelection(task knowledgeEvidenceJudgeTask, layer string) (knowledgeEvidenceLayerSelection, bool) {
	const minimumScore = float32(0.85)
	const minimumQuestionMatch = 0.82
	type matchedFAQ struct {
		candidate knowledgeEvidenceJudgeCandidate
		question  string
		answer    string
		match     float64
	}
	matches := make([]matchedFAQ, 0, 2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) || candidate.Hit.Score < minimumScore {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		questionMatch := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query)
		if question == "" || answer == "" || isKnowledgeHandoffDirectiveContent(answer) || questionMatch < minimumQuestionMatch {
			continue
		}
		matches = append(matches, matchedFAQ{candidate: candidate, question: question, answer: answer, match: questionMatch})
	}
	if len(matches) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	best := matches[0]
	for _, match := range matches[1:] {
		if match.match > best.match+0.02 || (match.match >= best.match-0.02 && match.candidate.Hit.Score > best.candidate.Hit.Score) {
			best = match
		}
	}
	if knowledgeEvidenceDirectFAQHasConflict(task, layer, best.candidate.CandidateID, best.answer, best.match) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	facts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, best.answer)
	facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, best.question, best.answer, facts)
	facts = filterKnowledgeEvidenceFactsForTask(task, facts)
	facts = groundedKnowledgeEvidenceFacts(task, layer, []string{best.candidate.CandidateID}, facts)
	if len(facts) == 0 || len(missingRequiredKnowledgeEvidenceAspects(task, facts)) > 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{best.candidate.CandidateID},
		SupportedFacts:       facts,
	}, true
}

func knowledgeEvidenceDirectFAQHasConflict(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateID string, selectedAnswer string, selectedQuestionMatch float64) bool {
	for _, candidate := range task.Candidates {
		if candidate.CandidateID == selectedCandidateID || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) || candidate.Hit.Score < 0.7 {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		questionMatch := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query)
		if questionMatch < 0.78 || questionMatch+0.08 < selectedQuestionMatch {
			continue
		}
		if isKnowledgeHandoffDirectiveContent(answer) || knowledgeEvidenceFAQAnswersConflict(selectedAnswer, answer) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswersConflict(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if knowledgeEvidenceTextHasNegativeBoundary(left) != knowledgeEvidenceTextHasNegativeBoundary(right) {
		return true
	}
	leftNumbers := knowledgeEvidenceAnswerNumberPattern.FindAllString(normalizeRuntimeKnowledgeQuery(left), -1)
	rightNumbers := knowledgeEvidenceAnswerNumberPattern.FindAllString(normalizeRuntimeKnowledgeQuery(right), -1)
	return len(leftNumbers) > 0 && len(rightNumbers) > 0 && strings.Join(leftNumbers, "|") != strings.Join(rightNumbers, "|")
}

func deterministicKnowledgeEvidenceFactsFromFAQ(taskID string, answer string) []knowledgeEvidenceFact {
	clauses := splitKnowledgeEvidenceAnswerClauses(answer)
	facts := make([]knowledgeEvidenceFact, 0, len(clauses))
	seen := make(map[string]struct{}, len(clauses))
	for _, clause := range clauses {
		if !knowledgeEvidenceAnswerClauseIsGroundedFact(clause) {
			continue
		}
		aspect, criticalValue := knowledgeEvidenceAnswerClauseAspect(clause)
		criticalValues := knowledgeEvidenceAnswerClauseCriticalValues(clause, criticalValue)
		if aspect == "method" {
			for _, cue := range []string{"通过", "使用", "扫码", "点击", "输入", "操作", "办理", "联系", "回复", "选择", "对比", "比较", "申请", "下载", "登记", "刷脸", "扫脸", "扫人脸", "开门"} {
				if strings.Contains(clause, cue) {
					criticalValues = appendIfMissing(criticalValues, cue)
				}
			}
			for _, channel := range []string{"小程序", "入住机", "短信链接", "二维码", "房卡", "人脸", "电话", "微信", "支付宝", "银行卡", "APP", "app"} {
				if strings.Contains(clause, channel) {
					criticalValues = appendIfMissing(criticalValues, channel)
				}
			}
		}
		factID := nextKnowledgeEvidenceFactID(taskID, seen)
		seen[factID] = struct{}{}
		statement := strings.TrimSpace(clause)
		if !strings.HasSuffix(statement, "。") && !strings.HasSuffix(statement, "！") && !strings.HasSuffix(statement, "？") {
			statement += "。"
		}
		facts = append(facts, knowledgeEvidenceFact{FactID: factID, Aspect: aspect, Statement: statement, CriticalValues: criticalValues})
	}
	return facts
}

func normalizedKnowledgeEvidenceEntities(entities []knowledgeEvidenceJudgeEntity) []string {
	ret := make([]string, 0, len(entities))
	seen := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		value := normalizeRuntimeKnowledgeQuery(entity.Text)
		if len([]rune(value)) < 2 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func highConfidenceKnowledgeConsensusSelection(task knowledgeEvidenceJudgeTask, layer string, requiredEntities []string) (knowledgeEvidenceLayerSelection, bool) {
	const minimumScore = float32(0.85)
	var best *knowledgeEvidenceJudgeCandidate
	bestTarget := ""
	bestPredicate := ""
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) || candidate.Hit.Score < minimumScore {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
			continue
		}
		answer = strings.TrimSpace(answer)
		target, predicate, ok := knowledgeEvidenceAffirmativeEnumerationMatch(answer, requiredEntities)
		if !ok {
			continue
		}
		copy := candidate
		if best == nil || candidate.Hit.Score > best.Hit.Score {
			best = &copy
			bestTarget = target
			bestPredicate = predicate
		}
	}
	if best == nil || knowledgeEvidenceEnumerationHasConflict(task, layer, bestTarget, bestPredicate, minimumScore) {
		return knowledgeEvidenceLayerSelection{}, false
	}

	criticalValues := make([]string, 0, 2)
	targetText := bestTarget
	predicateText := bestPredicate
	for _, entity := range task.Entities {
		value := normalizeRuntimeKnowledgeQuery(entity.Text)
		if value == bestTarget {
			targetText = strings.TrimSpace(entity.Text)
		}
		if value == bestPredicate {
			predicateText = strings.TrimSpace(entity.Text)
		}
	}
	criticalValues = appendIfMissing(criticalValues, targetText)
	criticalValues = appendIfMissing(criticalValues, predicateText)
	return knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		SelectedCandidateIDs: []string{best.CandidateID},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID:         strings.TrimSpace(task.TaskID) + "F1",
			Aspect:         "existence",
			Statement:      targetText + "有" + predicateText + "。",
			CriticalValues: criticalValues,
		}},
	}, true
}

func knowledgeEvidenceAffirmativeEnumerationMatch(answer string, entities []string) (string, string, bool) {
	compact := normalizeRuntimeKnowledgeQuery(answer)
	if knowledgeEvidenceTextHasNegativeBoundary(compact) || containsAny(compact, []string{"可能", "也许", "不一定", "为准", "取决于", "视情况", "具体情况"}) {
		return "", "", false
	}
	prefix, members, ok := splitKnowledgeEvidenceEnumeration(answer)
	if !ok {
		return "", "", false
	}
	for _, target := range entities {
		if !knowledgeEvidenceEnumerationContainsMember(members, target) {
			continue
		}
		for _, predicate := range entities {
			if predicate != target && strings.Contains(prefix, predicate) {
				return target, predicate, true
			}
		}
	}
	return "", "", false
}

func splitKnowledgeEvidenceEnumeration(answer string) (string, []string, bool) {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(answer)))
	markerIndex := -1
	markerLength := 0
	for _, marker := range []string{"例如", "比如", "包括", "包含", "分别是", "分别为", "如"} {
		if index := strings.Index(compact, marker); index >= 0 && (markerIndex < 0 || index < markerIndex) {
			if marker == "如" && strings.HasPrefix(compact[index:], "如果") {
				continue
			}
			markerIndex = index
			markerLength = len(marker)
		}
	}
	if markerIndex < 0 {
		return "", nil, false
	}
	prefix := normalizeRuntimeKnowledgeQuery(compact[:markerIndex])
	tail := compact[markerIndex+markerLength:]
	if index := strings.IndexAny(tail, "。；;！？!?"); index >= 0 {
		tail = tail[:index]
	}
	tail = strings.NewReplacer("以及", "、", "或者", "、", "和", "、", "及", "、", "与", "、", "，", "、", ",", "、", "/", "、").Replace(tail)
	return prefix, strings.Split(tail, "、"), true
}

func knowledgeEvidenceEnumerationContainsMember(members []string, entity string) bool {
	for _, member := range members {
		member = strings.TrimSuffix(normalizeRuntimeKnowledgeQuery(member), "等")
		for _, suffix := range []string{"房型", "客房"} {
			member = strings.TrimSuffix(member, suffix)
		}
		if member == entity {
			return true
		}
	}
	return false
}

func knowledgeEvidenceEnumerationHasConflict(task knowledgeEvidenceJudgeTask, layer string, target string, predicate string, minimumScore float32) bool {
	for _, candidate := range task.Candidates {
		if candidate.Layer != layer || candidate.Hit.Score < minimumScore {
			continue
		}
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		compact := normalizeRuntimeKnowledgeQuery(answer)
		if strings.Contains(compact, target) && strings.Contains(compact, predicate) && knowledgeEvidenceTextHasNegativeBoundary(compact) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceTextContainsAll(text string, required []string) bool {
	text = normalizeRuntimeKnowledgeQuery(text)
	if text == "" {
		return false
	}
	for _, value := range required {
		if value == "" || !strings.Contains(text, value) {
			return false
		}
	}
	return true
}

func normalizeKnowledgeEvidenceJudgeResponseJSON(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", fmt.Errorf("knowledge judge response is empty")
	}

	if strings.HasPrefix(normalized, `"`) {
		var unwrapped string
		decoder := json.NewDecoder(strings.NewReader(normalized))
		if err := decoder.Decode(&unwrapped); err != nil {
			return "", fmt.Errorf("unwrap string-encoded knowledge judge response: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return "", fmt.Errorf("string-encoded knowledge judge response contains trailing content")
		}
		normalized = strings.TrimSpace(unwrapped)
		if normalized == "" {
			return "", fmt.Errorf("string-encoded knowledge judge response is empty")
		}
	}

	unwrapped, err := unwrapKnowledgeEvidenceJudgeJSONFence(normalized)
	if err != nil {
		return "", err
	}
	return unwrapped, nil
}

func unwrapKnowledgeEvidenceJudgeJSONFence(raw string) (string, error) {
	if !strings.HasPrefix(raw, "```") {
		return raw, nil
	}
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return "", fmt.Errorf("knowledge judge response contains an incomplete JSON code block")
	}
	header := strings.TrimSpace(lines[0])
	if header != "```" && !strings.EqualFold(header, "```json") {
		return "", fmt.Errorf("knowledge judge response contains an unsupported code block")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return "", fmt.Errorf("knowledge judge JSON code block contains trailing content")
	}
	body := strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	if body == "" {
		return "", fmt.Errorf("knowledge judge JSON code block is empty")
	}
	return body, nil
}

func normalizeKnowledgeEvidenceFacts(taskID string, layer string, facts []knowledgeEvidenceFact, seenFactIDs map[string]struct{}) ([]knowledgeEvidenceFact, error) {
	ret := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, fact := range facts {
		factID := strings.TrimSpace(fact.FactID)
		aspect := strings.TrimSpace(fact.Aspect)
		statement := strings.TrimSpace(fact.Statement)
		if factID == "" || statement == "" || !isKnowledgeEvidenceFactAspect(aspect) {
			return nil, fmt.Errorf("invalid supported fact for task %s layer %s", taskID, layer)
		}
		if _, exists := seenFactIDs[factID]; exists {
			return nil, fmt.Errorf("duplicate supported fact id %s for task %s", factID, taskID)
		}
		seenFactIDs[factID] = struct{}{}
		criticalValues := make([]string, 0, len(fact.CriticalValues))
		seenValues := make(map[string]struct{}, len(fact.CriticalValues))
		for _, rawValue := range fact.CriticalValues {
			value := strings.TrimSpace(rawValue)
			if value == "" {
				return nil, fmt.Errorf("empty critical value for fact %s", factID)
			}
			if _, exists := seenValues[value]; exists {
				return nil, fmt.Errorf("duplicate critical value %q for fact %s", value, factID)
			}
			seenValues[value] = struct{}{}
			criticalValues = append(criticalValues, value)
		}
		ret = append(ret, knowledgeEvidenceFact{
			FactID:         factID,
			Aspect:         aspect,
			Statement:      statement,
			CriticalValues: criticalValues,
		})
	}
	return ret, nil
}

func normalizeKnowledgeEvidenceMissingAspects(taskID string, layer string, aspects []string) ([]string, error) {
	ret := make([]string, 0, len(aspects))
	seen := make(map[string]struct{}, len(aspects))
	for _, rawAspect := range aspects {
		aspect := strings.TrimSpace(rawAspect)
		if aspect == "" {
			return nil, fmt.Errorf("empty missing aspect for task %s layer %s", taskID, layer)
		}
		if _, exists := seen[aspect]; exists {
			return nil, fmt.Errorf("duplicate missing aspect %q for task %s layer %s", aspect, taskID, layer)
		}
		seen[aspect] = struct{}{}
		ret = append(ret, aspect)
	}
	return ret, nil
}

func isKnowledgeEvidenceFactAspect(aspect string) bool {
	switch aspect {
	case "existence", "quantity", "price", "time", "location", "method", "scope", "condition", "other":
		return true
	default:
		return false
	}
}

func reconcileSelectedFAQGuidanceFacts(
	taskID string,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	return reconcileSelectedFAQGuidanceFactsForQuery(taskID, "", layer, selection, candidates)
}

func reconcileSelectedFAQGuidanceFactsForQuery(
	taskID string,
	query string,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	return reconcileSelectedFAQGuidanceFactsForTask(
		knowledgeEvidenceJudgeTask{TaskID: taskID, Query: query},
		layer,
		selection,
		candidates,
	)
}

func reconcileSelectedFAQGuidanceFactsForTask(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle && selection.Decision != knowledgeEvidenceDecisionDirectCombined {
		return selection
	}
	taskID := task.TaskID
	seenFactIDs := make(map[string]struct{}, len(selection.SupportedFacts))
	for _, fact := range selection.SupportedFacts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	for _, candidateID := range selection.SelectedCandidateIDs {
		candidate, ok := candidates[strings.TrimSpace(candidateID)]
		if !ok || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(answer) {
			if !knowledgeEvidenceAnswerClauseIsGroundedFact(clause) {
				continue
			}
			aspect, criticalValue := knowledgeEvidenceAnswerClauseAspect(clause)
			criticalValues := knowledgeEvidenceAnswerClauseCriticalValues(clause, criticalValue)
			if factIndex := knowledgeEvidenceAnswerClauseFactIndex(clause, criticalValue, criticalValues, selection.SupportedFacts); factIndex >= 0 {
				selection.SupportedFacts[factIndex].CriticalValues = appendKnowledgeEvidenceCriticalValues(
					selection.SupportedFacts[factIndex].CriticalValues,
					criticalValues,
				)
				continue
			}
			factID := nextKnowledgeEvidenceFactID(taskID, seenFactIDs)
			seenFactIDs[factID] = struct{}{}
			statement := strings.TrimSpace(clause)
			if !strings.HasSuffix(statement, "。") && !strings.HasSuffix(statement, "！") && !strings.HasSuffix(statement, "？") {
				statement += "。"
			}
			selection.SupportedFacts = append(selection.SupportedFacts, knowledgeEvidenceFact{
				FactID:         factID,
				Aspect:         aspect,
				Statement:      statement,
				CriticalValues: criticalValues,
			})
		}
	}
	selection.SupportedFacts = filterKnowledgeEvidenceFactsForTask(task, selection.SupportedFacts)
	return selection
}

func knowledgeEvidenceAnswerClauseIsGroundedFact(clause string) bool {
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if len([]rune(compact)) < 3 {
		return false
	}
	if knowledgeEvidenceFactIsMarketingFiller(clause) {
		return false
	}
	for _, filler := range []string{"好的", "好哒", "收到", "谢谢", "谢谢您", "感谢", "不客气", "您好", "你好", "抱歉", "不好意思", "没问题", "希望能帮到您", "祝您愉快"} {
		if compact == normalizeRuntimeKnowledgeQuery(filler) {
			return false
		}
	}
	compactLength := len([]rune(compact))
	for _, prefix := range []string{"感谢", "谢谢", "祝您", "很高兴为您", "希望能帮到"} {
		if strings.HasPrefix(compact, prefix) && compactLength <= 16 {
			return false
		}
	}
	for _, prefix := range []string{"另外", "同时", "此外", "然后", "以及", "并且"} {
		if compact == prefix {
			return false
		}
	}
	return true
}

func knowledgeEvidenceAnswerClauseAspect(clause string) (string, string) {
	if aspect, criticalValue := knowledgeEvidenceGuidanceRequirement(clause); criticalValue != "" {
		return aspect, criticalValue
	}
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if knowledgeEvidenceNegativeBoundaryAnchor(clause) != "" {
		return "existence", ""
	}
	if knowledgeEvidenceAnswerTimePattern.MatchString(clause) || strings.Contains(compact, "时间") || strings.Contains(compact, "几点") {
		return "time", ""
	}
	if containsAny(compact, []string{"免费", "收费", "价格", "费用", "金额"}) {
		return "price", ""
	}
	if knowledgeEvidenceStrictQuantityPattern.MatchString(compact) {
		return "quantity", ""
	}
	if knowledgeEvidenceTextHasLocationCue(clause) {
		return "location", ""
	}
	if knowledgeEvidenceTextHasMethodCue(clause) {
		return "method", ""
	}
	if containsAny(compact, []string{"不同平台", "平台权益", "实时调整", "自动调整"}) {
		return "condition", ""
	}
	if containsAny(compact, []string{"仅限", "适用", "范围", "全部", "均可"}) {
		return "scope", ""
	}
	if containsAny(compact, []string{"如果", "需要", "取决于", "为准", "而定"}) {
		return "condition", ""
	}
	return "other", ""
}

func knowledgeEvidenceAnswerClauseFactIndex(clause string, criticalValue string, criticalValues []string, facts []knowledgeEvidenceFact) int {
	if criticalValue != "" {
		if index := knowledgeEvidenceGuidanceFactIndex(clause, criticalValue, criticalValues, facts); index >= 0 {
			return index
		}
	}
	clauseText := normalizeRuntimeKnowledgeQuery(clause)
	clauseNegative := knowledgeEvidenceTextHasNegativeBoundary(clauseText)
	bestIndex := -1
	bestSimilarity := 0.0
	for index := range facts {
		statement := normalizeRuntimeKnowledgeQuery(facts[index].Statement)
		if statement == "" || clauseNegative != knowledgeEvidenceTextHasNegativeBoundary(statement) {
			continue
		}
		if strings.Contains(statement, clauseText) || strings.Contains(clauseText, statement) {
			return index
		}
		similarity := knowledgeEvidenceTextNGramSimilarity(clauseText, statement)
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIndex = index
		}
	}
	if bestSimilarity >= 0.58 {
		return bestIndex
	}
	return -1
}

func knowledgeEvidenceAnswerClauseCriticalValues(clause string, criticalValue string) []string {
	values := make([]string, 0, 4)
	if criticalValue != "" {
		values = appendKnowledgeEvidenceCriticalValues(values, knowledgeEvidenceGuidanceCriticalValues(clause, criticalValue))
	}
	if anchor := knowledgeEvidenceNegativeBoundaryAnchor(clause); anchor != "" {
		values = appendIfMissing(values, anchor)
	}
	for _, match := range knowledgeEvidenceGuidanceNumberPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	timeMatches := knowledgeEvidenceAnswerTimePattern.FindAllString(clause, -1)
	for _, match := range timeMatches {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceAnswerNumberPattern.FindAllString(clause, -1) {
		if knowledgeEvidenceNumberIsPartOfTime(match, timeMatches) {
			continue
		}
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceAnswerChineseQuantityPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	if anchor := knowledgeEvidenceLocationAnchor(clause); anchor != "" {
		values = appendIfMissing(values, anchor)
	}
	compact := normalizeRuntimeKnowledgeQuery(clause)
	for _, value := range []string{"免费", "收费"} {
		if strings.Contains(compact, value) {
			values = appendIfMissing(values, value)
		}
	}
	return values
}

func knowledgeEvidenceNumberIsPartOfTime(number string, timeMatches []string) bool {
	number = strings.TrimSpace(number)
	if number == "" {
		return false
	}
	for _, timeMatch := range timeMatches {
		if strings.Contains(strings.TrimSpace(timeMatch), number) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceTextHasNegativeBoundary(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{"并不是", "并非", "没有", "不是", "不能", "不会", "无法", "不可", "不含", "不提供", "不支持", "不需要", "无需", "未配备", "暂不"})
}

func knowledgeEvidenceNegativeBoundaryAnchor(clause string) string {
	for _, marker := range []string{"并不是", "不提供", "不支持", "不需要", "未配备", "没有", "并非", "不是", "不能", "不会", "无法", "不可", "不含", "无需", "暂不"} {
		index := strings.Index(clause, marker)
		if index < 0 {
			continue
		}
		anchor := strings.TrimSpace(clause[index+len(marker):])
		for _, connector := range []string{"但是", "不过", "而是", "但"} {
			if connectorIndex := strings.Index(anchor, connector); connectorIndex >= 0 {
				anchor = strings.TrimSpace(anchor[:connectorIndex])
			}
		}
		anchor = strings.Trim(anchor, " ，,。；;！!？?：:")
		runes := []rune(anchor)
		if len(runes) > 20 {
			anchor = string(runes[:20])
		}
		if len([]rune(normalizeRuntimeKnowledgeQuery(anchor))) >= 2 {
			return anchor
		}
	}
	return ""
}

func knowledgeEvidenceLocationAnchor(clause string) string {
	for _, marker := range []string{"地址为", "地址是", "地址：", "地址:", "位于"} {
		index := strings.Index(clause, marker)
		if index < 0 {
			continue
		}
		anchor := strings.Trim(strings.TrimSpace(clause[index+len(marker):]), " ，,。；;！!？?")
		length := len([]rune(normalizeRuntimeKnowledgeQuery(anchor)))
		if length >= 3 && length <= 48 {
			return anchor
		}
	}
	return ""
}

func splitKnowledgeEvidenceAnswerClauses(answer string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(answer), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '!', '？', '?', '；', ';', '，', ',':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(normalizeRuntimeKnowledgeQuery(part))) < 3 {
			continue
		}
		ret = append(ret, part)
	}
	return ret
}

func knowledgeEvidenceGuidanceRequirement(clause string) (string, string) {
	compact := normalizeRuntimeKnowledgeQuery(clause)
	for _, cue := range []string{"对比", "比较", "联系", "回复", "选择"} {
		if strings.Contains(compact, cue) {
			return "method", cue
		}
	}
	if strings.Contains(compact, "建议") {
		return "method", "建议"
	}
	for _, cue := range []string{"取决于", "具体情况", "当天情况", "实际情况"} {
		if strings.Contains(compact, cue) {
			return "condition", cue
		}
	}
	if strings.Contains(compact, "为准") {
		return "condition", "为准"
	}
	if strings.Contains(compact, "而定") {
		return "condition", "而定"
	}
	return "", ""
}

func knowledgeEvidenceGuidanceFactIndex(clause string, criticalValue string, criticalValues []string, facts []knowledgeEvidenceFact) int {
	clauseText := normalizeRuntimeKnowledgeQuery(clause)
	for index := range facts {
		statement := normalizeRuntimeKnowledgeQuery(facts[index].Statement)
		if statement != "" && (strings.Contains(statement, clauseText) || strings.Contains(clauseText, statement)) {
			return index
		}
	}
	if criticalValue == "对比" || criticalValue == "比较" {
		for index := range facts {
			if knowledgeEvidenceGuidanceCueCovered(facts[index].Statement, criticalValue) {
				return index
			}
			for _, value := range facts[index].CriticalValues {
				if knowledgeEvidenceGuidanceCueCovered(value, criticalValue) {
					return index
				}
			}
		}
	}
	for _, literal := range criticalValues[1:] {
		for index := range facts {
			if strings.Contains(normalizeRuntimeKnowledgeQuery(facts[index].Statement), normalizeRuntimeKnowledgeQuery(literal)) || stringSliceContains(facts[index].CriticalValues, literal) {
				return index
			}
		}
	}
	return -1
}

var knowledgeEvidenceGuidanceNumberPattern = regexp.MustCompile(`[0-9][0-9-]{5,}[0-9]`)
var knowledgeEvidenceGuidanceQuotedPattern = regexp.MustCompile(`[“\"]([^”\"]{1,20})[”\"]`)
var knowledgeEvidenceAnswerTimePattern = regexp.MustCompile(`[0-9]{1,2}:[0-9]{2}(?:\s*(?:-|~|至|到)\s*[0-9]{1,2}:[0-9]{2})?`)
var knowledgeEvidenceAnswerNumberPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:\s*(?:-|~|至|到)\s*[0-9]+(?:\.[0-9]+)?)?(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|分|秒|元|块|折|层|楼|号|公里|米|工作日)?`)
var knowledgeEvidenceAnswerChineseQuantityPattern = regexp.MustCompile(`[零〇一二三四五六七八九十百千万两]+(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|层|楼|号|公里|米|工作日)`)
var knowledgeEvidenceStrictQuantityPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)?|[零〇一二三四五六七八九十百千万两]+)(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|公里|米|工作日)`)
var knowledgeEvidencePriceValuePattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:元|块|折)`)

func knowledgeEvidenceGuidanceCriticalValues(clause string, criticalValue string) []string {
	values := []string{criticalValue}
	for _, match := range knowledgeEvidenceGuidanceNumberPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceGuidanceQuotedPattern.FindAllStringSubmatch(clause, -1) {
		if len(match) > 1 {
			values = appendIfMissing(values, strings.TrimSpace(match[1]))
		}
	}
	return values
}

func appendKnowledgeEvidenceCriticalValues(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func knowledgeEvidenceGuidanceCueCovered(text string, criticalValue string) bool {
	criticalValue = normalizeRuntimeKnowledgeQuery(criticalValue)
	if criticalValue == "对比" || criticalValue == "比较" {
		return strings.Contains(text, "对比") || strings.Contains(text, "比较")
	}
	return criticalValue != "" && strings.Contains(text, criticalValue)
}

func nextKnowledgeEvidenceFactID(taskID string, seen map[string]struct{}) string {
	prefix := strings.TrimSpace(taskID) + "F"
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s%d", prefix, index)
		if _, exists := seen[candidate]; !exists {
			return candidate
		}
	}
}

func selectedKnowledgeEvidenceIsHandoffDirective(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) == 0 {
		return false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[candidateID] = struct{}{}
	}
	for _, candidate := range task.Candidates {
		if candidate.Layer != layer {
			continue
		}
		if _, ok := selected[candidate.CandidateID]; ok {
			_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if isKnowledgeHandoffDirectiveContent(answer) {
				return true
			}
		}
	}
	return false
}

func normalizeKnowledgeEvidenceJudgeConfig(config models.AIConfig, taskCount int) models.AIConfig {
	if taskCount < 1 {
		taskCount = 1
	}
	requiredTimeout := 4*time.Second + time.Duration(taskCount)*time.Second
	if requiredTimeout > knowledgeEvidenceJudgeMaxTimeout {
		requiredTimeout = knowledgeEvidenceJudgeMaxTimeout
	}
	configuredTimeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if config.TimeoutMS <= 0 {
		configuredTimeout = knowledgeEvidenceJudgeMaxTimeout
	} else if configuredTimeout < requiredTimeout {
		configuredTimeout = requiredTimeout
	} else if configuredTimeout > knowledgeEvidenceJudgeMaxTimeout {
		configuredTimeout = knowledgeEvidenceJudgeMaxTimeout
	}
	config.TimeoutMS = int(configuredTimeout / time.Millisecond)

	requiredOutputTokens := 512 + taskCount*256
	if requiredOutputTokens < 1024 {
		requiredOutputTokens = 1024
	}
	if requiredOutputTokens > knowledgeEvidenceJudgeMaxOutputTokens {
		requiredOutputTokens = knowledgeEvidenceJudgeMaxOutputTokens
	}
	if config.MaxOutputTokens <= 0 {
		config.MaxOutputTokens = requiredOutputTokens
	} else if config.MaxOutputTokens < requiredOutputTokens {
		config.MaxOutputTokens = requiredOutputTokens
	} else if config.MaxOutputTokens > knowledgeEvidenceJudgeMaxOutputTokens {
		config.MaxOutputTokens = knowledgeEvidenceJudgeMaxOutputTokens
	}
	config.MaxRetryCount = 0
	return config
}

func knowledgeEvidenceJudgeUsageContext(ctx context.Context, req RunInput, resolved *services.ResolvedAIConfig) context.Context {
	scope := usagex.ScopeFromContext(ctx)
	runtimeScope := resolveRuntimeIntentScope(req)
	if scope.CompanyID <= 0 {
		scope.CompanyID = runtimeScope.CompanyID
	}
	if scope.StoreID <= 0 {
		scope.StoreID = runtimeScope.StoreID
	}
	if scope.WxWorkInstanceID <= 0 {
		scope.WxWorkInstanceID = runtimeScope.WxWorkInstanceID
	}
	if scope.ConversationID <= 0 {
		scope.ConversationID = req.Conversation.ID
	}
	if scope.MessageID <= 0 {
		scope.MessageID = req.UserMessage.ID
	}
	if strings.TrimSpace(scope.RequestID) == "" {
		scope.RequestID = strings.TrimSpace(req.UserMessage.RequestID)
	}
	if resolved != nil {
		scope.CredentialRevision = resolved.CredentialRevision
		scope.ModelSource = resolved.Source
	}
	return usagex.WithScope(ctx, scope)
}

func recordKnowledgeEvidenceJudgeUsage(ctx context.Context, req RunInput, config models.AIConfig, result *ai.ChatCompletionResult, receipt *usagex.Receipt, fingerprint string, latencyMS int64, callErr error) {
	status := "completed"
	errorClass := ""
	if callErr != nil {
		status = "failed"
		errorClass = "model_call_failed"
	}
	record := ai.ModelUsageRecord{
		Stage:            "knowledge_evidence_judge",
		OperationType:    "batch_select",
		Config:           config,
		LatencyMS:        latencyMS,
		Status:           status,
		ErrorClass:       errorClass,
		Receipt:          receipt,
		ExternalEventKey: knowledgeEvidenceJudgeUsageEventKey(req, fingerprint),
	}
	if result != nil {
		record.PromptTokens = int64(result.PromptTokens)
		record.CompletionTokens = int64(result.CompletionTokens)
	}
	ai.RecordModelUsage(ctx, record)
}

func knowledgeEvidenceJudgeUsageEventKey(req RunInput, fingerprint string) string {
	requestID := strings.TrimSpace(req.UserMessage.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("conversation-%d-message-%d", req.Conversation.ID, req.UserMessage.ID)
	}
	return requestID + ":knowledge_evidence_judge:" + fingerprint
}

func lastKnowledgeEvidenceJudgeReceipt(capture *usagex.Capture) *usagex.Receipt {
	if capture == nil {
		return nil
	}
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func fingerprintKnowledgeEvidenceJudgePrompt(prompt knowledgeEvidenceJudgePrompt) string {
	raw, _ := json.Marshal(prompt)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

func countKnowledgeEvidenceJudgeCandidates(prompt knowledgeEvidenceJudgePrompt) int {
	count := 0
	for _, task := range prompt.Tasks {
		count += len(task.Candidates)
	}
	return count
}

func compactKnowledgeEvidenceJudgeError(err error) string {
	if err == nil {
		return ""
	}
	return preview(strings.Join(strings.Fields(err.Error()), " "), 240)
}
