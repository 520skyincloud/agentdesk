package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/rag/vectordb"
	_ "agent-desk/internal/ai/runtime"
	"agent-desk/internal/bootstrap"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/logx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type scenario struct {
	ID                   string
	Category             string
	Name                 string
	Turns                []turn
	Rapid                bool
	RecordEachTurn       bool
	MustContainAny       []string
	MustContainAll       []string
	RequiredOutcomes     []outcomeRequirement
	Banned               []string
	ExpectedIntent       string
	ExpectedSubIntentAny []string
	NeedsKnowledge       *bool
	NeedsResource        *bool
	NeedsHumanRoute      *bool
	MaxReplyMessages     int
	LatencyWarningMs     int64
	LatencyLimitMs       int64
	Notes                string
}

type turn struct {
	Type                 enums.IMMessageType
	Content              string
	Payload              string
	WaitForAI            bool
	AfterDelay           time.Duration
	BackdateGap          time.Duration
	MustContainAny       []string
	MustContainAll       []string
	RequiredOutcomes     []outcomeRequirement
	Banned               []string
	ExpectedIntent       string
	ExpectedSubIntentAny []string
	NeedsKnowledge       *bool
	NeedsResource        *bool
	NeedsHumanRoute      *bool
	MaxReplyMessages     int
	LatencyWarningMs     int64
	LatencyLimitMs       int64
	Notes                string
}

type outcomeRequirement struct {
	Label                      string
	TextContainsAny            []string
	TextContainsAll            []string
	TextExcludesAny            []string
	ResourceTypesAll           []string
	DeferredHandoffContainsAny []string
	// FactSlot excludes resource delivery and answer-shape checks from the factual coverage denominator.
	FactSlot bool
}

type record struct {
	ScenarioID                string         `json:"scenarioId"`
	Category                  string         `json:"category"`
	Name                      string         `json:"name"`
	ConversationID            int64          `json:"conversationId"`
	Messages                  []string       `json:"messages"`
	MediaContext              []string       `json:"mediaContext,omitempty"`
	ReplyText                 string         `json:"replyText"`
	Status                    string         `json:"status"`
	FinalAction               string         `json:"finalAction"`
	LatencyMs                 int64          `json:"latencyMs"`
	RuntimeLatencyMs          int64          `json:"runtimeLatencyMs"`
	GenerateLatencyMs         int64          `json:"generateLatencyMs"`
	AIConfigID                int64          `json:"aiConfigId"`
	ModelSource               string         `json:"modelSource"`
	ConfiguredMaxOutputTokens int            `json:"configuredMaxOutputTokens,omitempty"`
	EffectiveMaxOutputTokens  int            `json:"effectiveMaxOutputTokens,omitempty"`
	PromptTokens              int            `json:"promptTokens"`
	CompletionTokens          int            `json:"completionTokens"`
	TotalTokens               int            `json:"totalTokens"`
	CachedTokens              int            `json:"cachedTokens"`
	Intent                    string         `json:"intent"`
	SubIntent                 string         `json:"subIntent"`
	ResourceAction            string         `json:"resourceAction"`
	ResourceActions           []string       `json:"resourceActions,omitempty"`
	CommitMessages            []commitRecord `json:"commitMessages,omitempty"`
	DeferredHandoff           bool           `json:"deferredHandoff,omitempty"`
	DeferredHandoffReason     string         `json:"deferredHandoffReason,omitempty"`
	KnowledgeHit              bool           `json:"knowledgeHit"`
	KnowledgeExpected         bool           `json:"knowledgeExpected"`
	ResourceExpected          bool           `json:"resourceExpected"`
	HumanExpected             bool           `json:"humanExpected"`
	RetrieverCount            int            `json:"retrieverCount"`
	ToolCount                 int            `json:"toolCount"`
	FactSlotsSatisfied        int            `json:"factSlotsSatisfied,omitempty"`
	FactSlotsExpected         int            `json:"factSlotsExpected,omitempty"`
	Score                     int            `json:"score"`
	Passed                    bool           `json:"passed"`
	Issues                    []string       `json:"issues,omitempty"`
	TraceSummary              map[string]any `json:"traceSummary,omitempty"`
	ErrorMessage              string         `json:"errorMessage,omitempty"`
}

type aiReplyTrace struct {
	Status                    string          `json:"status"`
	AIConfigID                int64           `json:"aiConfigId"`
	ModelSource               string          `json:"modelSource"`
	RuntimeLatencyMs          int64           `json:"runtimeLatencyMs"`
	ConfiguredMaxOutputTokens int             `json:"configuredMaxOutputTokens"`
	EffectiveMaxOutputTokens  int             `json:"effectiveMaxOutputTokens"`
	FinalAction               string          `json:"finalAction"`
	ReplySent                 bool            `json:"replySent"`
	ReplyMessageID            int64           `json:"replyMessageId"`
	Runtime                   json.RawMessage `json:"runtime"`
}

type runtimeTrace struct {
	Status string `json:"status"`
	Model  struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		Usage    struct {
			PromptTokens       int `json:"promptTokens"`
			CompletionTokens   int `json:"completionTokens"`
			TotalTokens        int `json:"totalTokens"`
			CachedPromptTokens int `json:"cachedPromptTokens"`
			ReasoningTokens    int `json:"reasoningTokens"`
		} `json:"usage"`
	} `json:"model"`
	Pipeline struct {
		Intent struct {
			PrimaryIntent    string   `json:"primaryIntent"`
			SubIntent        string   `json:"subIntent"`
			SecondaryIntents []string `json:"secondaryIntents"`
			NeedsKnowledge   bool     `json:"needsKnowledge"`
			NeedsResource    bool     `json:"needsResource"`
			NeedsHumanRoute  bool     `json:"needsHumanRoute"`
			ResourceAction   string   `json:"resourceAction"`
			ResourceActions  []string `json:"resourceActions"`
			Reason           string   `json:"reason"`
		} `json:"intent"`
		ToolKnowledge struct {
			KnowledgeTriggered bool `json:"knowledgeTriggered"`
			ToolTriggered      bool `json:"toolTriggered"`
		} `json:"toolKnowledge"`
		Generate struct {
			LatencyMs int64 `json:"latencyMs"`
		} `json:"generate"`
		EvidenceJudge struct {
			DeferredHandoff       bool   `json:"deferredHandoff"`
			DeferredHandoffReason string `json:"deferredHandoffReason"`
		} `json:"evidenceJudge"`
	} `json:"pipeline"`
	Retriever struct {
		Count int `json:"count"`
	} `json:"retriever"`
	Tools struct {
		Count int `json:"count"`
	} `json:"tools"`
	Output struct {
		CommitMessages []commitRecord `json:"commitMessages"`
	} `json:"output"`
}

type commitRecord struct {
	MessageID    int64  `json:"messageId,omitempty"`
	MessageType  string `json:"messageType"`
	ResourceType string `json:"resourceType,omitempty"`
	Content      string `json:"content"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type usageStageSummary struct {
	Stage                  string
	Provider               string
	Model                  string
	MetricSource           string
	Status                 string
	EventCount             int64
	PromptTokens           int64
	CompletionTokens       int64
	CachedPromptTokens     int64
	ReasoningTokens        int64
	RequestCount           int64
	RerankCount            int64
	EstimatedContextTokens int64
}

type usageSummary struct {
	DistinctRequests int64
	EventCount       int64
	Stages           []usageStageSummary
}

type runner struct {
	runID        string
	round        int
	instanceID   int64
	limit        int
	scenarioID   string
	suite        string
	waitTimeout  time.Duration
	outputDir    string
	cleanup      bool
	instance     *models.WxWorkProtocolInstance
	aiAgent      models.AIAgent
	records      []record
	conversation []int64
	customerIDs  []int64
	assetIDs     []int64
}

func main() {
	configPath := flag.String("config", "docker/agent-desk.yaml", "config file")
	instanceID := flag.Int64("wx-work-instance-id", 7, "WxWorkProtocolInstance id used for store, variables and knowledge scope")
	roundNo := flag.Int("round", 1, "self-optimization round number")
	limit := flag.Int("limit", 0, "optional scenario limit for smoke runs")
	scenarioID := flag.String("scenario", "", "optional exact scenario id for targeted smoke runs")
	suite := flag.String("suite", "", "optional scenario suite, e.g. balanced20, active-answer or continuous50-safe")
	waitTimeout := flag.Duration("wait-timeout", 180*time.Second, "max wait for each AI runlog")
	outputDir := flag.String("output-dir", "docs/generated", "report output directory")
	noCleanup := flag.Bool("no-cleanup", false, "keep temporary database records for debugging")
	flag.Parse()

	runID := fmt.Sprintf("rrt-%s-%s", time.Now().Format("20060102-150405"), strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	r := &runner{
		runID:       runID,
		round:       *roundNo,
		instanceID:  *instanceID,
		limit:       *limit,
		scenarioID:  strings.TrimSpace(*scenarioID),
		suite:       strings.TrimSpace(*suite),
		waitTimeout: *waitTimeout,
		outputDir:   *outputDir,
		cleanup:     !*noCleanup,
	}
	if err := initRuntime(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "init runtime: %v\n", err)
		os.Exit(1)
	}
	if err := r.prepare(); err != nil {
		fmt.Fprintf(os.Stderr, "prepare eval: %v\n", err)
		os.Exit(1)
	}

	startedAt := time.Now()
	health := r.checkHealth()
	err := r.run(context.Background())
	usage := r.collectUsageSummary()
	cleanupReport := map[string]int64{}
	if r.cleanup {
		cleanupReport = r.cleanupData()
	}
	reportPath, jsonlPath, writeErr := r.writeReports(startedAt, health, usage, cleanupReport, err)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval failed: %v\n", err)
	}
	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", writeErr)
		os.Exit(1)
	}
	fmt.Printf("runId=%s\nreport=%s\njsonl=%s\n", r.runID, reportPath, jsonlPath)
	if err != nil {
		os.Exit(1)
	}
}

func initRuntime(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	config.SetCurrent(cfg)
	logx.Init(logx.Config{Level: cfg.Logger.Level, Format: cfg.Logger.Format, AddSource: cfg.Logger.AddSource})
	if _, err := bootstrap.InitDB(cfg.DB); err != nil {
		return err
	}
	if err := bootstrap.InitMigrations(); err != nil {
		return err
	}
	return vectordb.Init(&cfg.VectorDB)
}

func (r *runner) prepare() error {
	r.instance = services.WxWorkProtocolInstanceService.Get(r.instanceID)
	if r.instance == nil || r.instance.Status != enums.StatusOk {
		return fmt.Errorf("wx work instance %d not found or disabled", r.instanceID)
	}
	if r.instance.StoreID <= 0 || r.instance.KnowledgeBaseID <= 0 {
		return fmt.Errorf("wx work instance %d lacks store/knowledge binding", r.instanceID)
	}
	r.aiAgent = services.WxWorkProtocolInstanceService.BuildRuntimeAIAgent(r.instance)
	return nil
}

func (r *runner) run(ctx context.Context) error {
	cases := buildScenarios(r.round)
	if r.scenarioID != "" {
		filtered := make([]scenario, 0, 1)
		for _, sc := range cases {
			if strings.EqualFold(sc.ID, r.scenarioID) {
				filtered = append(filtered, sc)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("scenario %q not found in round %d", r.scenarioID, r.round)
		}
		cases = filtered
	}
	if r.suite != "" {
		selected, err := selectScenarioSuite(cases, r.suite)
		if err != nil {
			return err
		}
		cases = selected
	}
	if r.limit > 0 && r.limit < len(cases) {
		cases = cases[:r.limit]
	}
	for idx := range cases {
		rec, err := r.runScenario(ctx, cases[idx])
		if cases[idx].RecordEachTurn {
			if err != nil {
				rec.ErrorMessage = err.Error()
				rec.Issues = append(rec.Issues, err.Error())
				rec.FactSlotsExpected = remainingFactSlots(cases[idx], r.records)
				rec.Score = 0
				rec.Passed = false
				r.records = append(r.records, rec)
			}
			continue
		}
		if err != nil {
			rec.ErrorMessage = err.Error()
			rec.Issues = append(rec.Issues, err.Error())
			if rec.FactSlotsExpected == 0 {
				rec.FactSlotsExpected = countFactSlots(cases[idx])
			}
			rec.Score = 0
			rec.Passed = false
		}
		r.records = append(r.records, rec)
		slog.Info("reply runtime eval case finished",
			"runId", r.runID,
			"scenario", cases[idx].ID,
			"score", rec.Score,
			"latency_ms", rec.LatencyMs,
			"intent", rec.Intent,
			"passed", rec.Passed,
		)
	}
	return nil
}

func (r *runner) runScenario(ctx context.Context, sc scenario) (record, error) {
	external := openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     r.runID + "-" + sc.ID,
		ExternalName:   "Runtime评测-" + sc.ID,
	}
	conversation, err := services.ConversationService.CreateWithRuntimeProfileWithoutWelcome(external, 0, r.aiAgent)
	if err != nil {
		return recordFromScenario(sc), err
	}
	r.conversation = append(r.conversation, conversation.ID)
	r.customerIDs = append(r.customerIDs, conversation.CustomerID)
	if err := r.bindRoute(conversation.ID); err != nil {
		return recordFromScenario(sc), err
	}

	rec := recordFromScenario(sc)
	rec.ConversationID = conversation.ID
	var lastRequestID string
	for idx, t := range sc.Turns {
		if t.Type == "" {
			t.Type = enums.IMMessageTypeText
		}
		if t.BackdateGap > 0 {
			if err := r.backdateConversation(conversation.ID, t.BackdateGap); err != nil {
				return rec, err
			}
		}
		requestID := fmt.Sprintf("%s-%s-%02d", r.runID, sc.ID, idx+1)
		clientMsgID := "client-" + requestID
		if isMedia(t.Type) {
			payload, err := r.prepareMediaPayload(t)
			if err != nil {
				return rec, err
			}
			t.Payload = payload
			rec.MediaContext = append(rec.MediaContext, strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(t.Type, t.Content, t.Payload)))
		}
		rec.Messages = append(rec.Messages, strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(t.Type, t.Content, t.Payload)))
		message, err := services.MessageService.SendCustomerMessageWithRequestID(conversation.ID, clientMsgID, t.Type, t.Content, t.Payload, external, requestID)
		if err != nil {
			return rec, err
		}
		if isMedia(t.Type) {
			messageServiceAlreadyTriggered := preparedMediaPayloadTriggersAI(t.Payload)
			if err := r.waitMediaUnderstandingAttempt(ctx, message.ID, 6*time.Second); err != nil {
				return rec, err
			}
			if err := r.applyPreparedMediaUnderstanding(message.ID, t.Payload); err != nil {
				return rec, err
			}
			if err := r.waitPreparedMediaUnderstanding(ctx, message.ID, 2*time.Second); err != nil {
				return rec, err
			}
			if t.WaitForAI && !messageServiceAlreadyTriggered {
				r.triggerPreparedMediaAI(conversation, message.ID)
			}
		}
		if t.WaitForAI {
			lastRequestID = message.RequestID
			log, waitErr := r.waitRunLog(ctx, lastRequestID)
			if waitErr != nil {
				return rec, waitErr
			}
			if sc.RecordEachTurn {
				turnScenario := scenarioFromTurn(sc, idx, t)
				turnRec := recordFromScenario(turnScenario)
				turnRec.ConversationID = conversation.ID
				turnRec.Messages = append([]string(nil), rec.Messages...)
				turnRec.MediaContext = append([]string(nil), rec.MediaContext...)
				turnRec = r.fillRecordFromRunLog(turnRec, turnScenario, log)
				turnRec.FactSlotsSatisfied, turnRec.FactSlotsExpected = factSlotStats(turnScenario, turnRec)
				turnRec.Score, turnRec.Issues = scoreRecord(turnScenario, turnRec)
				turnRec.Passed = turnRec.Score >= 80 && turnRec.Status != "error"
				r.records = append(r.records, turnRec)
			} else {
				rec = r.fillRecordFromRunLog(rec, sc, log)
			}
		}
		delay := t.AfterDelay
		if delay == 0 && sc.Rapid {
			delay = 30 * time.Millisecond
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	if lastRequestID == "" {
		rec.Status = "no_ai_expected"
		rec.Score = 100
		rec.Passed = true
		return rec, nil
	}
	if sc.RecordEachTurn {
		rec.Status = "completed"
		rec.Score = 100
		rec.Passed = true
		return rec, nil
	}
	rec.FactSlotsSatisfied, rec.FactSlotsExpected = factSlotStats(sc, rec)
	rec.Score, rec.Issues = scoreRecord(sc, rec)
	rec.Passed = rec.Score >= 80 && rec.Status != "error"
	return rec, nil
}

func scenarioFromTurn(sc scenario, index int, t turn) scenario {
	ret := scenario{
		ID:                   fmt.Sprintf("%s-T%02d", sc.ID, index+1),
		Category:             sc.Category,
		Name:                 strings.TrimSpace(t.Content),
		MustContainAny:       t.MustContainAny,
		MustContainAll:       t.MustContainAll,
		RequiredOutcomes:     t.RequiredOutcomes,
		Banned:               t.Banned,
		ExpectedIntent:       t.ExpectedIntent,
		ExpectedSubIntentAny: t.ExpectedSubIntentAny,
		NeedsKnowledge:       t.NeedsKnowledge,
		NeedsResource:        t.NeedsResource,
		NeedsHumanRoute:      t.NeedsHumanRoute,
		MaxReplyMessages:     t.MaxReplyMessages,
		LatencyWarningMs:     t.LatencyWarningMs,
		LatencyLimitMs:       t.LatencyLimitMs,
		Notes:                t.Notes,
	}
	if ret.Name == "" {
		ret.Name = sc.Name
	}
	return ret
}

func (r *runner) prepareMediaPayload(t turn) (string, error) {
	var data map[string]any
	if strings.TrimSpace(t.Payload) != "" {
		if err := json.Unmarshal([]byte(t.Payload), &data); err != nil {
			return "", err
		}
	}
	if data == nil {
		data = map[string]any{}
	}
	filename := strings.TrimSpace(stringFromMap(data, "filename"))
	if filename == "" {
		filename = strings.TrimSpace(t.Content)
	}
	if filename == "" {
		filename = "reply-runtime-media.txt"
	}
	mimeType := mediaMimeType(t.Type, filename)
	asset, err := services.AssetService.RegisterExternal(
		"reply_runtime_eval/"+r.runID,
		filename,
		int64(len(strings.TrimSpace(stringFromMap(data, "mediaText")))+128),
		mimeType,
		fmt.Sprintf("reply-runtime-eval/%s/%s/%d", r.runID, filename, time.Now().UnixNano()),
		nil,
	)
	if err != nil {
		return "", err
	}
	r.assetIDs = append(r.assetIDs, asset.ID)
	data["assetId"] = asset.AssetID
	data["provider"] = string(asset.Provider)
	data["storageKey"] = asset.StorageKey
	data["filename"] = asset.Filename
	data["fileSize"] = asset.FileSize
	data["mimeType"] = asset.MimeType
	if strings.TrimSpace(stringFromMap(data, "mediaUnderstandingStatus")) == "" {
		data["mediaUnderstandingStatus"] = "understood"
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func preparedMediaPayloadTriggersAI(payload string) bool {
	mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(payload)
	return strings.TrimSpace(status) == "understood" &&
		(strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "")
}

func (r *runner) triggerPreparedMediaAI(conversation *models.Conversation, messageID int64) {
	if conversation == nil || messageID <= 0 || services.TriggerAIReplyAsyncHook == nil {
		return
	}
	message := services.MessageService.Get(messageID)
	if message == nil {
		return
	}
	services.TriggerAIReplyAsyncHook(*conversation, *message)
}

func (r *runner) applyPreparedMediaUnderstanding(messageID int64, preparedPayload string) error {
	if messageID <= 0 {
		return nil
	}
	message := services.MessageService.Get(messageID)
	if message == nil {
		return nil
	}
	var base map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(message.Payload)), &base)
	if base == nil {
		base = map[string]any{}
	}
	var prepared map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(preparedPayload)), &prepared); err != nil {
		return err
	}
	for _, key := range []string{"mediaText", "mediaSummary", "mediaUnderstandingStatus"} {
		if value := strings.TrimSpace(stringFromMap(prepared, key)); value != "" {
			base[key] = value
		}
	}
	if strings.TrimSpace(stringFromMap(base, "mediaUnderstandingStatus")) == "" {
		base["mediaUnderstandingStatus"] = "understood"
	}
	payload, err := json.Marshal(base)
	if err != nil {
		return err
	}
	return services.MessageService.Updates(messageID, map[string]any{
		"payload":          string(payload),
		"updated_at":       time.Now(),
		"update_user_name": "reply-runtime-eval",
	})
}

func (r *runner) waitMediaUnderstandingAttempt(ctx context.Context, messageID int64, timeout time.Duration) error {
	if messageID <= 0 || timeout <= 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		message := services.MessageService.Get(messageID)
		if message == nil {
			return nil
		}
		_, _, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		status = strings.TrimSpace(status)
		if status == "understood" || status == "failed" || status == "empty" {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *runner) waitPreparedMediaUnderstanding(ctx context.Context, messageID int64, timeout time.Duration) error {
	if messageID <= 0 || timeout <= 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		message := services.MessageService.Get(messageID)
		if message == nil {
			return nil
		}
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(message.Payload)
		if strings.TrimSpace(status) == "understood" && (strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("prepared media understanding not visible for message %d", messageID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func stringFromMap(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func mediaMimeType(messageType enums.IMMessageType, filename string) string {
	lower := strings.ToLower(strings.TrimSpace(filename))
	switch messageType {
	case enums.IMMessageTypeImage:
		if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
			return "image/jpeg"
		}
		return "image/png"
	case enums.IMMessageTypeVoice:
		return "audio/amr"
	case enums.IMMessageTypeAttachment:
		if strings.HasSuffix(lower, ".pdf") {
			return "application/pdf"
		}
		if strings.HasSuffix(lower, ".csv") {
			return "text/csv"
		}
		if strings.HasSuffix(lower, ".json") {
			return "application/json"
		}
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

func recordFromScenario(sc scenario) record {
	return record{
		ScenarioID:        sc.ID,
		Category:          sc.Category,
		Name:              sc.Name,
		KnowledgeExpected: boolValue(sc.NeedsKnowledge),
		ResourceExpected:  boolValue(sc.NeedsResource),
		HumanExpected:     boolValue(sc.NeedsHumanRoute),
	}
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func (r *runner) bindRoute(conversationID int64) error {
	state, err := services.ConversationRouteService.Ensure(conversationID)
	if err != nil {
		return err
	}
	return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
		"store_id":            r.instance.StoreID,
		"knowledge_base_id":   r.instance.KnowledgeBaseID,
		"wx_work_instance_id": r.instance.ID,
		"route_status":        enums.ConversationRouteStatusAIServing,
		"route_target":        "ai",
		"session_no":          1,
		"updated_at":          time.Now(),
		"update_user_name":    "reply-runtime-eval",
	})
}

func (r *runner) backdateConversation(conversationID int64, gap time.Duration) error {
	when := time.Now().Add(-gap)
	if err := repositories.ConversationRepository.Updates(sqls.DB(), conversationID, map[string]any{
		"last_active_at": when,
		"updated_at":     when,
	}); err != nil {
		return err
	}
	if state := services.ConversationRouteService.GetByConversationID(conversationID); state != nil {
		return repositories.ConversationRouteStateRepository.Updates(sqls.DB(), state.ID, map[string]any{
			"last_customer_message_at": when,
			"updated_at":               when,
		})
	}
	return nil
}

func (r *runner) waitRunLog(ctx context.Context, requestID string) (*models.AgentRunLog, error) {
	deadline := time.Now().Add(r.waitTimeout)
	for {
		if item := services.AgentRunLogService.FindOne(sqls.NewCnd().Eq("request_id", requestID).Desc("id")); item != nil {
			return item, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting runlog for request_id=%s", requestID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (r *runner) fillRecordFromRunLog(rec record, sc scenario, logItem *models.AgentRunLog) record {
	rec.Status = strings.TrimSpace(logItem.FinalStatus)
	rec.FinalAction = strings.TrimSpace(logItem.FinalAction)
	rec.LatencyMs = logItem.LatencyMs
	rec.AIConfigID = logItem.AIConfigID
	rec.ReplyText = strings.TrimSpace(logItem.ReplyText)
	rec.ErrorMessage = strings.TrimSpace(logItem.ErrorMessage)

	var trace aiReplyTrace
	if err := json.Unmarshal([]byte(strings.TrimSpace(logItem.TraceData)), &trace); err == nil {
		rec.RuntimeLatencyMs = trace.RuntimeLatencyMs
		if trace.AIConfigID > 0 {
			rec.AIConfigID = trace.AIConfigID
		}
		rec.ModelSource = strings.TrimSpace(trace.ModelSource)
		rec.ConfiguredMaxOutputTokens = trace.ConfiguredMaxOutputTokens
		rec.EffectiveMaxOutputTokens = trace.EffectiveMaxOutputTokens
		if strings.TrimSpace(trace.FinalAction) != "" {
			rec.FinalAction = strings.TrimSpace(trace.FinalAction)
		}
		var rt runtimeTrace
		if len(trace.Runtime) > 0 && json.Unmarshal(trace.Runtime, &rt) == nil {
			rec.PromptTokens = rt.Model.Usage.PromptTokens
			rec.CompletionTokens = rt.Model.Usage.CompletionTokens
			rec.TotalTokens = rt.Model.Usage.TotalTokens
			rec.CachedTokens = rt.Model.Usage.CachedPromptTokens
			rec.Intent = strings.TrimSpace(rt.Pipeline.Intent.PrimaryIntent)
			rec.SubIntent = strings.TrimSpace(rt.Pipeline.Intent.SubIntent)
			rec.ResourceAction = strings.TrimSpace(rt.Pipeline.Intent.ResourceAction)
			rec.ResourceActions = append([]string(nil), rt.Pipeline.Intent.ResourceActions...)
			rec.GenerateLatencyMs = rt.Pipeline.Generate.LatencyMs
			rec.KnowledgeHit = rt.Retriever.Count > 0 || rt.Pipeline.ToolKnowledge.KnowledgeTriggered
			rec.RetrieverCount = rt.Retriever.Count
			rec.ToolCount = rt.Tools.Count
			rec.CommitMessages = append([]commitRecord(nil), rt.Output.CommitMessages...)
			rec.DeferredHandoff = rt.Pipeline.EvidenceJudge.DeferredHandoff
			rec.DeferredHandoffReason = strings.TrimSpace(rt.Pipeline.EvidenceJudge.DeferredHandoffReason)
			rec.TraceSummary = map[string]any{
				"model":                     strings.TrimSpace(rt.Model.Name),
				"provider":                  strings.TrimSpace(rt.Model.Provider),
				"configuredMaxOutputTokens": rec.ConfiguredMaxOutputTokens,
				"effectiveMaxOutputTokens":  rec.EffectiveMaxOutputTokens,
				"needsKnowledge":            rt.Pipeline.Intent.NeedsKnowledge,
				"needsResource":             rt.Pipeline.Intent.NeedsResource,
				"needsHumanRoute":           rt.Pipeline.Intent.NeedsHumanRoute,
				"intentReason":              strings.TrimSpace(rt.Pipeline.Intent.Reason),
				"secondaryIntents":          rt.Pipeline.Intent.SecondaryIntents,
				"resourceActions":           rt.Pipeline.Intent.ResourceActions,
				"commitMessages":            rt.Output.CommitMessages,
				"deferredHandoff":           rec.DeferredHandoff,
				"deferredHandoffReason":     rec.DeferredHandoffReason,
				"toolTriggered":             rt.Pipeline.ToolKnowledge.ToolTriggered,
				"knowledgeTriggered":        rt.Pipeline.ToolKnowledge.KnowledgeTriggered,
			}
		}
	}
	if rec.Intent == "" && sc.ExpectedIntent != "" {
		rec.Intent = "(missing trace)"
	}
	return rec
}

func scoreRecord(sc scenario, rec record) (int, []string) {
	score := 100
	issues := make([]string, 0)
	reply := strings.TrimSpace(rec.ReplyText)
	scoreText := scoreableCustomerText(rec)
	if rec.ErrorMessage != "" || rec.Status == "error" {
		score -= 60
		issues = append(issues, "runtime error: "+preview(rec.ErrorMessage, 120))
	}
	if reply == "" && rec.Status != "completed" && rec.FinalAction != "interrupted" {
		score -= 30
		issues = append(issues, "empty reply")
	}
	for _, needle := range sc.MustContainAll {
		if !containsLoose(scoreText, needle) {
			score -= 12
			issues = append(issues, "missing required text: "+needle)
		}
	}
	if len(sc.MustContainAny) > 0 && !containsAnyLoose(scoreText, sc.MustContainAny) {
		score -= 16
		issues = append(issues, "missing any required text: "+strings.Join(sc.MustContainAny, "/"))
	}
	for _, requirement := range sc.RequiredOutcomes {
		if requiredOutcomeSatisfied(rec, requirement) {
			continue
		}
		score -= 24
		label := strings.TrimSpace(requirement.Label)
		if label == "" {
			label = "unnamed outcome"
		}
		issues = append(issues, "missing required answer/action: "+label)
	}
	for _, banned := range append(defaultBannedPhrases(), sc.Banned...) {
		if containsLoose(scoreText, banned) {
			score -= 24
			issues = append(issues, "banned phrase: "+banned)
		}
	}
	if sc.ExpectedIntent != "" && rec.Intent != "" && rec.Intent != sc.ExpectedIntent {
		score -= 14
		issues = append(issues, fmt.Sprintf("intent mismatch: got %s want %s", rec.Intent, sc.ExpectedIntent))
	}
	if len(sc.ExpectedSubIntentAny) > 0 && rec.SubIntent != "" && !containsExact(sc.ExpectedSubIntentAny, rec.SubIntent) {
		score -= 6
		issues = append(issues, fmt.Sprintf("subIntent mismatch: got %s want one of %s", rec.SubIntent, strings.Join(sc.ExpectedSubIntentAny, "/")))
	}
	if sc.NeedsKnowledge != nil && rec.KnowledgeHit != *sc.NeedsKnowledge {
		score -= 10
		issues = append(issues, fmt.Sprintf("knowledge trigger mismatch: got %t want %t", rec.KnowledgeHit, *sc.NeedsKnowledge))
	}
	if sc.NeedsResource != nil {
		got := strings.TrimSpace(rec.ResourceAction) != "" || len(rec.ResourceActions) > 0 || hasCommittedStructuredResource(rec.CommitMessages)
		if got != *sc.NeedsResource {
			score -= 10
			issues = append(issues, fmt.Sprintf("resource trigger mismatch: got %t want %t", got, *sc.NeedsResource))
		}
		if *sc.NeedsResource {
			for _, resourceType := range expectedStructuredResourceTypes(rec) {
				if !hasCommittedResourceType(rec.CommitMessages, resourceType) {
					score -= 16
					issues = append(issues, "missing structured resource commit: "+resourceType)
				}
			}
		}
	}
	if sc.NeedsHumanRoute != nil {
		got := rec.FinalAction == "interrupted" || rec.FinalAction == "graph" || containsLoose(reply, "人工") || containsLoose(reply, "转人工") || containsLoose(reply, "同事接") || containsLoose(reply, "同事接手")
		if got != *sc.NeedsHumanRoute {
			score -= 10
			issues = append(issues, fmt.Sprintf("human route mismatch: got %t want %t", got, *sc.NeedsHumanRoute))
		}
	}
	if sc.MaxReplyMessages > 0 {
		if count := replyMessageCount(rec); count > sc.MaxReplyMessages {
			score -= 24
			issues = append(issues, fmt.Sprintf("too many reply messages: got %d want <= %d", count, sc.MaxReplyMessages))
		}
	}
	for index, message := range replyTextMessages(rec) {
		runeLen := len([]rune(message))
		if runeLen <= 220 {
			continue
		}
		score -= 8
		issues = append(issues, fmt.Sprintf("reply message %d too long: %d chars", index+1, runeLen))
	}
	latencyWarningMs := int64(8000)
	latencyLimitMs := int64(12000)
	if sc.LatencyWarningMs > 0 {
		latencyWarningMs = sc.LatencyWarningMs
	}
	if sc.LatencyLimitMs > 0 {
		latencyLimitMs = sc.LatencyLimitMs
	}
	if rec.LatencyMs > latencyLimitMs {
		score -= 18
		issues = append(issues, fmt.Sprintf("latency over %dms: %dms", latencyLimitMs, rec.LatencyMs))
	} else if rec.LatencyMs > latencyWarningMs {
		score -= 8
		issues = append(issues, fmt.Sprintf("latency over %dms: %dms", latencyWarningMs, rec.LatencyMs))
	}
	if score < 0 {
		score = 0
	}
	return score, issues
}

func replyMessageCount(rec record) int {
	count := 0
	for _, item := range rec.CommitMessages {
		if strings.TrimSpace(item.Status) != "sent" || strings.TrimSpace(item.ResourceType) != "" {
			continue
		}
		if strings.TrimSpace(item.Content) != "" {
			count++
		}
	}
	if count > 0 {
		return count
	}
	if strings.TrimSpace(rec.ReplyText) == "" {
		return 0
	}
	return strings.Count(rec.ReplyText, "<<NEXT_MESSAGE>>") + 1
}

func replyTextMessages(rec record) []string {
	messages := make([]string, 0, len(rec.CommitMessages))
	for _, item := range rec.CommitMessages {
		if strings.TrimSpace(item.Status) != "sent" || strings.TrimSpace(item.ResourceType) != "" {
			continue
		}
		if content := strings.TrimSpace(item.Content); content != "" {
			messages = append(messages, content)
		}
	}
	if len(messages) > 0 {
		return messages
	}
	for _, part := range strings.Split(rec.ReplyText, "<<NEXT_MESSAGE>>") {
		if part = strings.TrimSpace(part); part != "" {
			messages = append(messages, part)
		}
	}
	return messages
}

func expectedStructuredResourceTypes(rec record) []string {
	ret := make([]string, 0, len(rec.ResourceActions)+1)
	add := func(resourceType string) {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" {
			return
		}
		for _, existing := range ret {
			if existing == resourceType {
				return
			}
		}
		ret = append(ret, resourceType)
	}
	for _, action := range rec.ResourceActions {
		add(resourceTypeFromEvalAction(action))
	}
	add(resourceTypeFromEvalAction(rec.ResourceAction))
	return ret
}

func resourceTypeFromEvalAction(action string) string {
	switch strings.TrimSpace(action) {
	case "provide_location":
		return "location"
	case "send_miniprogram", "provide_mini_program":
		return "mini_program"
	case "provide_phone":
		return "phone"
	default:
		return ""
	}
}

func hasCommittedStructuredResource(items []commitRecord) bool {
	for _, item := range items {
		if strings.TrimSpace(item.Status) == "sent" && strings.TrimSpace(item.ResourceType) != "" {
			return true
		}
	}
	return false
}

func hasCommittedResourceType(items []commitRecord, resourceType string) bool {
	resourceType = strings.TrimSpace(resourceType)
	for _, item := range items {
		if strings.TrimSpace(item.Status) == "sent" && strings.TrimSpace(item.ResourceType) == resourceType {
			return true
		}
	}
	return false
}

func defaultBannedPhrases() []string {
	return []string{
		"已安排", "已经安排", "已通知", "已经通知", "已记录", "已经记录", "已提交", "已经提交",
		"马上送", "马上处理", "工单已创建",
		"帮你问", "帮您问", "我来问", "我去问", "问一下同事", "问下同事",
		"帮你查", "帮您查", "我查查", "我查一下", "我再查",
		"帮你确认", "帮您确认", "我来确认", "我去确认", "我需要确认", "我再确认",
		"帮你安排", "帮您安排", "帮你登记", "帮您登记", "帮你记下", "帮您记下", "我先记下", "我记下", "记下来", "我帮你记录", "我帮您记录",
		"帮你转告", "帮您转告", "我转告同事", "转告同事", "发到群里", "发群里",
		"反馈给同事", "我反馈", "我跟同事说", "我喊同事", "喊同事", "叫同事", "我让同事", "让同事给", "给你送一下", "送到房间", "让他们处理", "让他们联系",
		"我让系统通知", "让系统通知", "我让同事联系", "让同事联系你",
		"帮你处理网络", "帮您处理网络", "我去排查网络", "我帮你排查网络",
		"我发你", "我发您", "我这边发你", "我这边发您", "我这边直接发你", "我这边直接发您", "这边发你", "这边发您", "这边直接发你", "这边直接发您", "直接发你", "直接发您", "给你发", "给您发",
		"根据知识库", "系统显示", "作为AI", "我是机器人", "从历史来看", "按当前规则", "我先看看上一轮",
		"店助补充", "若不确定请先问同事", "请按门店实际填写",
		"[历史消息]", "[AI客服]", "[人工客服]", "[人工作答]",
		"replyParts", "taskId", "coveredFactIds",
	}
}

func containsLoose(text string, needle string) bool {
	return strings.Contains(normalizeText(text), normalizeText(needle))
}

func containsAnyLoose(text string, needles []string) bool {
	for _, needle := range needles {
		if containsLoose(text, needle) {
			return true
		}
	}
	return false
}

func containsAllLoose(text string, needles []string) bool {
	for _, needle := range needles {
		if !containsLoose(text, needle) {
			return false
		}
	}
	return true
}

func scoreableCustomerText(rec record) string {
	parts := make([]string, 0, len(rec.CommitMessages)+1)
	if strings.TrimSpace(rec.ReplyText) != "" {
		parts = append(parts, rec.ReplyText)
	}
	for _, item := range rec.CommitMessages {
		if strings.TrimSpace(item.Status) == "sent" && strings.TrimSpace(item.Content) != "" {
			parts = append(parts, item.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func requiredOutcomeSatisfied(rec record, requirement outcomeRequirement) bool {
	text := scoreableCustomerText(rec)
	if len(requirement.TextExcludesAny) > 0 && containsAnyLoose(text, requirement.TextExcludesAny) {
		return false
	}
	if len(requirement.TextContainsAll) > 0 && containsAllLoose(text, requirement.TextContainsAll) {
		return true
	}
	if len(requirement.TextContainsAny) > 0 && containsAnyLoose(text, requirement.TextContainsAny) {
		return true
	}
	if len(requirement.ResourceTypesAll) > 0 {
		for _, resourceType := range requirement.ResourceTypesAll {
			if !hasCommittedResourceType(rec.CommitMessages, resourceType) {
				return false
			}
		}
		return true
	}
	return rec.DeferredHandoff &&
		len(requirement.DeferredHandoffContainsAny) > 0 &&
		containsAnyLoose(rec.DeferredHandoffReason, requirement.DeferredHandoffContainsAny)
}

func factSlotStats(sc scenario, rec record) (satisfied int, expected int) {
	for _, requirement := range sc.RequiredOutcomes {
		if !requirement.FactSlot {
			continue
		}
		expected++
		if requiredOutcomeSatisfied(rec, requirement) {
			satisfied++
		}
	}
	return satisfied, expected
}

func countFactSlots(sc scenario) int {
	count := 0
	for _, requirement := range sc.RequiredOutcomes {
		if requirement.FactSlot {
			count++
		}
	}
	for _, item := range sc.Turns {
		for _, requirement := range item.RequiredOutcomes {
			if requirement.FactSlot {
				count++
			}
		}
	}
	return count
}

func remainingFactSlots(sc scenario, records []record) int {
	remaining := countFactSlots(sc)
	prefix := strings.ToUpper(strings.TrimSpace(sc.ID)) + "-T"
	for _, item := range records {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(item.ScenarioID)), prefix) {
			remaining -= item.FactSlotsExpected
		}
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}

func normalizeText(text string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "？", "", "?", "", "！", "", "!", "", "、", "", "：", "", ":", "", "；", "", ";", "", "“", "", "”", "", "\"", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

func isMedia(messageType enums.IMMessageType) bool {
	switch messageType {
	case enums.IMMessageTypeImage, enums.IMMessageTypeVoice, enums.IMMessageTypeAttachment, enums.IMMessageTypeVideo, enums.IMMessageTypeGIF:
		return true
	default:
		return false
	}
}

func buildScenarios(round int) []scenario {
	t := true
	f := false
	cases := make([]scenario, 0, 70)
	shorts := []scenario{
		short("S01", "WiFi密码多少", []string{"WiFi", "房间"}, "hotel_info", &t),
		short("S02", "房间网连不上", []string{"WiFi", "房间"}, "hotel_info", &t),
		short("S03", "发票怎么开", []string{"发票", "小程序"}, "hotel_info", &t),
		short("S04", "能开专票吗", []string{"专票", "发票"}, "hotel_info", &t),
		short("S05", "剃须刀有吗", []string{"剃须刀", "前台", "自取"}, "hotel_info", &t),
		short("S06", "早餐几点", []string{"早餐"}, "hotel_info", &t),
		short("S07", "停车收费吗", []string{"停车"}, "hotel_info", &t),
		short("S08", "酒店在哪里", []string{"地址", "定位", "酒店"}, "hotel_variable", &f).withResource(&t),
		short("S09", "电话给我一下", []string{"电话", "联系"}, "hotel_variable", &f).withResource(&t),
		short("S10", "入住小程序发我", []string{"小程序", "入住"}, "hotel_variable", &f).withResource(&t),
		short("S11", "几点可以入住", []string{"入住"}, "hotel_info", &t),
		short("S12", "几点退房", []string{"退房"}, "hotel_info", &t),
		short("S13", "洗衣房在哪", []string{"洗衣"}, "hotel_info", &t),
		short("S14", "电视怎么投屏", []string{"投屏", "电视", "WiFi"}, "hotel_info", &t),
		short("S15", "空调不制冷怎么办", []string{"房号", "同事", "处理"}, "service_request", &t),
		short("S16", "行李太多能帮我拿下楼吗", []string{"行李", "房号", "同事"}, "service_request", &t),
		short("S17", "我要投诉", []string{"人工", "同事"}, "human_complaint_risk", &f).withHuman(&t),
		short("S18", "为什么我朋友比我订得便宜", []string{"人工", "同事", "价格"}, "human_complaint_risk", &f).withHuman(&t),
		short("S19", "门锁坏了我有点害怕", []string{"安全", "同事", "房号"}, "human_complaint_risk", &f).withHuman(&t),
		short("S20", "谢谢啦", []string{"不客气"}, "social_confirm", &f),
		short("S21", "好的", []string{"好"}, "social_confirm", &f),
		short("S22", "😅", []string{"在", "发我"}, "social_confirm", &f),
		short("S23", "这是什么服务", []string{"具体", "哪项", "发我"}, "unknown_clarify", &f),
		short("S24", "帮我写一段代码", []string{"酒店", "处理不了", "服务范围"}, "unknown_clarify", &f),
		short("S25", "纸巾没了", []string{"纸巾", "抽纸", "房号", "洗衣房", "同事"}, "service_request", &t),
		short("S26", "能送两瓶水吗", []string{"水", "房号", "同事", "前台"}, "service_request", &t),
		short("S27", "我想延迟退房", []string{"退房", "房态", "时间"}, "hotel_info", &t),
		short("S28", "小程序打不开", []string{"小程序", "重试", "截图"}, "hotel_info", &t),
		short("S29", "前台在哪里", []string{"前台"}, "hotel_info", &t),
		short("S30", "我没给你发语音大哥", []string{"收到", "没事"}, "social_confirm", &f),
	}
	cases = append(cases, shorts...)

	cases = append(cases,
		rapid("C01", []string{"WiFi密码", "发票怎么开"}, []string{"WiFi", "发票"}, "hotel_info", &t),
		rapid("C02", []string{"早餐有吗", "停车免费吗", "剃须刀在哪"}, nil, "hotel_info", &t).withRequiredOutcomes(
			outcomeRequirement{Label: "早餐问题", TextContainsAny: []string{"早餐", "早饭"}},
			outcomeRequirement{Label: "停车问题", TextContainsAny: []string{"停车", "停车场", "车位"}},
			outcomeRequirement{Label: "剃须刀问题", TextContainsAny: []string{"剃须刀", "刮胡刀"}},
		),
		rapid("C03", []string{"我到楼下了", "入口怎么走", "定位也发我"}, []string{"入口", "定位"}, "hotel_variable", &t).withResource(&t),
		rapid("C04", []string{"电视投屏怎么弄", "WiFi也发下"}, []string{"投屏", "WiFi"}, "hotel_info", &t),
		rapid("C05", []string{"报销票咋开", "抬头等下发你"}, []string{"发票", "抬头"}, "hotel_info", &t),
		rapid("C06", []string{"空调显示E3", "房间太热了"}, []string{"房号", "同事"}, "service_request", &t),
		rapid("C07", []string{"我不满意", "帮我转人工"}, []string{"人工", "确认"}, "human_complaint_risk", &f).withHuman(&t),
		rapid("C08", []string{"我要入住", "小程序和定位都发我"}, []string{"小程序", "定位"}, "hotel_variable", &f).withResource(&t),
		rapid("C09", []string{"纸巾没了", "牙刷也没有", "住1208"}, []string{"纸巾", "抽纸", "牙刷", "1208", "洗衣房"}, "service_request", &t),
		rapid("C10", []string{"你们回复太慢了", "这个价格怎么不一样"}, []string{"人工", "同事", "价格"}, "human_complaint_risk", &f).withHuman(&t),
	)

	cases = append(cases,
		media("M01", enums.IMMessageTypeImage, "invoice.png", "图片是一张发票抬头截图，包含公司名称、税号和邮箱。", "这样能开专票吗", []string{"专票", "发票"}, "hotel_info", &t),
		media("M02", enums.IMMessageTypeImage, "wifi-error.png", "截图显示手机 WiFi 连接失败，提示无法加入网络。", "这个怎么弄", []string{"WiFi", "房间"}, "hotel_info", &t),
		media("M03", enums.IMMessageTypeImage, "tv-cast.jpg", "图片是电视投屏二维码页面，提示手机需连接同一 WiFi。", "怎么投", []string{"投屏", "WiFi"}, "hotel_info", &t),
		media("M04", enums.IMMessageTypeAttachment, "invoice-info.pdf", "文件是一份公司开票资料，包含抬头、税号、邮箱。", "资料够了吗", []string{"发票", "资料"}, "hotel_info", &t),
		media("M05", enums.IMMessageTypeAttachment, "order.pdf", "文件是一张订单确认单，显示入住人为张先生，入住日期为今天。", "这个能入住吗", []string{"入住", "证件", "小程序"}, "hotel_info", &t),
		media("M06", enums.IMMessageTypeVoice, "voice-1.amr", "WiFi 密码是多少，我住 1506。", "能直接告诉我吗", []string{"WiFi", "1506"}, "hotel_info", &t),
		media("M07", enums.IMMessageTypeVoice, "voice-2.amr", "帮我转人工，我要投诉房间太吵。", "听清了吗", []string{"人工", "投诉"}, "human_complaint_risk", &f).withHuman(&t),
		media("M08", enums.IMMessageTypeImage, "room.jpg", "图片为酒店房间桌面，有两瓶矿泉水和一张 WiFi 牌。", "这个水收费吗", []string{"水", "收费"}, "hotel_info", &t),
		media("M09", enums.IMMessageTypeImage, "selfie.jpg", "图片为客人自拍，无清晰文字、报错或明确酒店服务诉求。", "看到了吗", []string{"看到了", "发我"}, "social_confirm", &f),
		media("M10", enums.IMMessageTypeAttachment, "archive.zip", "文件理解失败：压缩包打不开。", "里面是发票资料，能开吗", []string{"打不开", "发票", "资料"}, "hotel_info", &t),
	)

	cases = append(cases,
		rapid("X01", []string{"我到附近了", "定位发我", "入住小程序也发一下", "停车在哪里"}, []string{"定位", "小程序", "停车"}, "hotel_variable", &t).withResource(&t),
		rapid("X02", []string{"WiFi连不上", "发票怎么开", "顺便给电话"}, []string{"WiFi", "发票", "电话"}, "hotel_variable", &t).withResource(&t),
		rapid("X03", []string{"空调坏了", "我住1302", "顺便问早餐几点"}, nil, "service_request", &t).withRequiredOutcomes(
			outcomeRequirement{Label: "空调故障处理", DeferredHandoffContainsAny: []string{"空调", "制冷"}},
			outcomeRequirement{Label: "早餐问题", TextContainsAny: []string{"早餐", "早饭"}},
		),
		rapid("X04", []string{"价格不一样我要人工", "停车还收费吗"}, []string{"人工", "停车"}, "human_complaint_risk", &t).withHuman(&t),
		rapid("X05", []string{"图片发票资料你看下", "如果不行我转人工", "我赶时间"}, []string{"发票", "人工"}, "human_complaint_risk", &t).withHuman(&t),
	)

	cases = append(cases, longScenario("L01"), longScenarioRoomExpiry("L02"), longScenarioStoreIsolation("L03"))
	cases = append(cases, activeAnswerScenarios()...)
	if round%2 == 0 {
		cases = append(cases, hundredTurnScenario())
	}
	if round%3 == 0 {
		cases = append(cases, returningCustomerScenario())
	}
	return cases
}

func selectScenarioSuite(cases []scenario, suite string) ([]scenario, error) {
	switch strings.ToLower(strings.TrimSpace(suite)) {
	case "active-answer", "active-answer-focused":
		return selectScenarioIDs(cases, []string{"AA01", "AA02", "AA03", "AA04", "AA05", "AA06", "AA07", "AA08"})
	case "continuous50", "continuous50-safe":
		return []scenario{continuous50SafeScenario()}, nil
	case "continuous30":
		return []scenario{continuous30Scenario()}, nil
	case "continuous20":
		return []scenario{continuous20Scenario()}, nil
	case "balanced20":
		return selectScenarioIDs(cases, []string{
			"S01", // WiFi
			"S03", // 发票
			"S07", // 停车
			"S08", // 定位变量
			"S10", // 小程序变量
			"S14", // 电视投屏
			"S15", // 空调服务请求
			"S17", // 明确投诉/人工
			"S20", // 感谢轻互动
			"S23", // 未知澄清
			"C02", // 连续酒店信息
			"C08", // 连续变量
			"C09", // 连续服务请求+房号
			"M01", // 图片发票追问
			"M03", // 图片电视投屏追问
			"M04", // 文件发票追问
			"M06", // 语音转文字上下文
			"X01", // 定位+小程序+停车混合
			"L01", // 长对话基础链路
			"L02", // 旧房号时效
		})
	default:
		return nil, fmt.Errorf("unknown scenario suite %q", suite)
	}
}

func activeAnswerScenarios() []scenario {
	t := true
	f := false
	robotCapabilityBanned := []string{"送到房间", "送上来", "送到门口", "送到房门", "直接送房间", "送进房间"}

	return []scenario{
		{
			ID:       "AA01",
			Category: "active-answer",
			Name:     "八问题长文字必须逐题完整回答",
			Turns: []turn{{
				Type:      enums.IMMessageTypeText,
				Content:   "请把这八个问题分别回答清楚：WiFi账号密码是什么、怎么办理入住、房门怎么开、房间矿泉水有几瓶且收费吗、有没有外卖机器人、外卖地址怎么填、停车收费且有没有充电桩、发票怎么申请以及多久能下载？",
				WaitForAI: true,
			}},
			RequiredOutcomes: []outcomeRequirement{
				textOutcome("WiFi账号", "alilys"),
				textOutcome("WiFi密码", "yzbh8888"),
				textOutcome("无传统前台", "没有传统前台", "无传统前台"),
				textOutcome("入住机", "入住机"),
				textOutcome("入住小程序", "小程序"),
				textOutcome("人脸开门", "扫脸", "刷脸", "人脸"),
				textOutcome("矿泉水数量", "两瓶", "2瓶"),
				textOutcome("矿泉水费用", "免费", "不收费"),
				robotExistenceOutcome(),
				textOutcome("南七店外卖地址", "丽斯未来酒店合肥南七店"),
				textOutcome("楼层房间号格式", "楼层房间号", "对应楼层房间号", "房间号"),
				textOutcome("停车费用", "停车免费", "免费停车", "停车是免费", "停车不收费"),
				textOutcome("停车场充电桩", "充电桩"),
				textOutcome("退房后申请发票", "退房后"),
				textOutcome("发票小程序", "自由家安心宿", "小程序"),
				textOutcome("发票下载时间", "1到3个工作日", "1至3个工作日", "1-3个工作日"),
			},
			Banned:           robotCapabilityBanned,
			ExpectedIntent:   "hotel_info",
			NeedsKnowledge:   &t,
			NeedsResource:    nil,
			NeedsHumanRoute:  &f,
			MaxReplyMessages: 3,
			LatencyWarningMs: 12000,
			LatencyLimitMs:   15000,
			Notes:            "八个原子业务问题均需有独立答案；机器人存在性不得外推配送范围。",
		},
		{
			ID:       "AA02",
			Category: "active-answer",
			Name:     "三问语音使用完整转写逐题回答",
			Turns: []turn{{
				Type: enums.IMMessageTypeVoice, Content: "active-answer-three.amr",
				Payload:   `{"filename":"active-answer-three.amr","mediaText":"我想一起问三个问题：房间里面有没有空调，矿泉水有几瓶而且是不是免费的，还有怎么办理入住？","mediaSummary":"客户一次询问多个酒店问题。","mediaUnderstandingStatus":"understood"}`,
				WaitForAI: true,
			}},
			RequiredOutcomes: []outcomeRequirement{
				textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"),
				textOutcome("矿泉水数量", "两瓶", "2瓶"),
				textOutcome("矿泉水费用", "免费", "不收费"),
				textOutcome("无传统前台", "没有传统前台", "无传统前台"),
				textOutcome("入住方式", "入住机", "小程序"),
			},
			ExpectedIntent:   "hotel_info",
			NeedsKnowledge:   &t,
			NeedsResource:    nil,
			NeedsHumanRoute:  &f,
			MaxReplyMessages: 3,
			Notes:            "mediaSummary 刻意不含具体问题，必须优先使用完整 mediaText。",
		},
		{
			ID:       "AA03",
			Category: "active-answer",
			Name:     "四问语音完整回答且不外推机器人能力",
			Turns: []turn{{
				Type: enums.IMMessageTypeVoice, Content: "active-answer-four.amr",
				Payload:   `{"filename":"active-answer-four.amr","mediaText":"再一起问四个：你们有没有外卖机器人，外卖地址应该怎么填，布草是不是一客一换，携程抖音美团的价格是不是一样？","mediaSummary":"客户语音询问四项酒店信息。","mediaUnderstandingStatus":"understood"}`,
				WaitForAI: true,
			}},
			RequiredOutcomes: []outcomeRequirement{
				robotExistenceOutcome(),
				textOutcome("南七店外卖地址", "丽斯未来酒店合肥南七店"),
				textOutcome("楼层房间号格式", "楼层房间号", "对应楼层房间号", "房间号"),
				textOutcome("布草一客一换", "一客一换"),
				textOutcome("平台权益不同", "平台权益", "权益不一样", "权益不同", "平台活动", "价格不一样", "不一定一样"),
				priceComparisonOutcome(),
			},
			Banned:           robotCapabilityBanned,
			ExpectedIntent:   "hotel_info",
			NeedsKnowledge:   &t,
			NeedsResource:    &f,
			NeedsHumanRoute:  &f,
			MaxReplyMessages: 3,
		},
		{
			ID:       "AA04",
			Category: "active-answer",
			Name:     "那麦田呢必须补全办公桌回指",
			Turns: []turn{
				{Type: enums.IMMessageTypeText, Content: "合柴房型有办公桌吗？", WaitForAI: true},
				{Type: enums.IMMessageTypeText, Content: "那麦田呢？", WaitForAI: true},
			},
			RequiredOutcomes: []outcomeRequirement{textAllOutcome("麦田办公桌", "麦田", "办公桌")},
			ExpectedIntent:   "hotel_info",
			NeedsKnowledge:   &t,
			NeedsResource:    &f,
			NeedsHumanRoute:  &f,
		},
		{
			ID:       "AA05",
			Category: "active-answer",
			Name:     "外卖地址再说一遍必须回指正确地址",
			Turns: []turn{
				{Type: enums.IMMessageTypeText, Content: "外卖地址怎么填？", WaitForAI: true},
				{Type: enums.IMMessageTypeText, Content: "外卖地址再说一遍，只要正确地址。", WaitForAI: true},
			},
			RequiredOutcomes: []outcomeRequirement{
				textOutcome("南七店名称", "丽斯未来酒店合肥南七店"),
				textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号"),
			},
			Banned:          []string{"哪个门店", "酒店名称或具体店名"},
			ExpectedIntent:  "hotel_info",
			NeedsKnowledge:  &t,
			NeedsResource:   &f,
			NeedsHumanRoute: &f,
		},
		{
			ID:       "AA06",
			Category: "active-answer",
			Name:     "那这两瓶是否免费必须保留数量与费用事实",
			Turns: []turn{
				{Type: enums.IMMessageTypeText, Content: "房间里有几瓶矿泉水？", WaitForAI: true},
				{Type: enums.IMMessageTypeText, Content: "那这两瓶是不是都免费？", WaitForAI: true},
			},
			RequiredOutcomes: []outcomeRequirement{
				textOutcome("矿泉水数量", "两瓶", "2瓶"),
				textOutcome("矿泉水费用", "免费", "不收费"),
			},
			ExpectedIntent:  "hotel_info",
			NeedsKnowledge:  &t,
			NeedsResource:   &f,
			NeedsHumanRoute: &f,
		},
		{
			ID:       "AA07",
			Category: "active-answer",
			Name:     "机器人存在性不得外推配送范围",
			Turns: []turn{{
				Type: enums.IMMessageTypeText, Content: "你们有外卖机器人吗？", WaitForAI: true,
			}},
			RequiredOutcomes: []outcomeRequirement{robotExistenceOutcome()},
			Banned:           robotCapabilityBanned,
			ExpectedIntent:   "hotel_info",
			NeedsKnowledge:   &t,
			NeedsResource:    &f,
			NeedsHumanRoute:  &f,
		},
		{
			ID:       "AA08",
			Category: "active-answer",
			Name:     "内部历史标签和生成协议不得泄漏",
			Turns: []turn{{
				Type: enums.IMMessageTypeText, Content: "哪些房型有办公桌？哪些房型同时还有沙发？请分别说清楚。", WaitForAI: true,
			}},
			RequiredOutcomes: []outcomeRequirement{
				textAllOutcome("麦田办公桌", "麦田", "办公桌"),
				textAllOutcome("合柴同时有沙发和办公桌", "合柴", "沙发", "办公桌"),
				textAllOutcome("艺林同时有沙发和办公桌", "艺林", "沙发", "办公桌"),
			},
			ExpectedIntent:  "hotel_info",
			NeedsKnowledge:  &t,
			NeedsResource:   &f,
			NeedsHumanRoute: &f,
			Notes:           "全局禁用词会拦截历史角色标签和 replyParts/taskId/coveredFactIds。",
		},
	}
}

func textOutcome(label string, containsAny ...string) outcomeRequirement {
	return outcomeRequirement{Label: label, TextContainsAny: containsAny, FactSlot: true}
}

func robotExistenceOutcome() outcomeRequirement {
	return textOutcome(
		"外卖机器人存在",
		"有外卖机器人",
		"有外卖机器人的",
		"配有外卖机器人",
		"提供外卖机器人",
	)
}

func priceComparisonOutcome() outcomeRequirement {
	return priceComparisonRequirement(true)
}

func priceComparisonCoverageOutcome() outcomeRequirement {
	return priceComparisonRequirement(false)
}

func priceComparisonRequirement(factSlot bool) outcomeRequirement {
	return outcomeRequirement{
		Label:           "建议比较价格",
		TextContainsAny: []string{"对比", "比较"},
		TextExcludesAny: negativePriceComparisonPhrases(),
		FactSlot:        factSlot,
	}
}

func negativePriceComparisonPhrases() []string {
	prefixes := []string{"无需", "不用", "不必", "不能", "不要", "没必要", "没有必要", "不需要"}
	verbs := []string{"对比", "比较"}
	ret := make([]string, 0, len(prefixes)*len(verbs))
	for _, prefix := range prefixes {
		for _, verb := range verbs {
			ret = append(ret, prefix+verb)
		}
	}
	return ret
}

func textAllOutcome(label string, containsAll ...string) outcomeRequirement {
	return outcomeRequirement{Label: label, TextContainsAll: containsAll, FactSlot: true}
}

func coverageOutcome(label string, containsAny ...string) outcomeRequirement {
	return outcomeRequirement{Label: label, TextContainsAny: containsAny}
}

func resourceOutcome(label string, resourceTypes ...string) outcomeRequirement {
	return outcomeRequirement{Label: label, ResourceTypesAll: resourceTypes}
}

func continuous20Scenario() scenario {
	t := true
	f := false
	turns := []turn{
		{
			Type: enums.IMMessageTypeText, Content: "你好", WaitForAI: true,
			MustContainAny: []string{"你好", "在"}, ExpectedIntent: "interaction", NeedsKnowledge: &f, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "WiFi密码多少", WaitForAI: true,
			MustContainAny: []string{"WiFi", "密码", "房间"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "定位发我，小程序也发一下，停车在哪", WaitForAI: true,
			MustContainAny: []string{"停车"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &t, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "发票怎么开", WaitForAI: true,
			MustContainAny: []string{"发票", "小程序"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeImage, Content: "meal.jpg", Payload: `{"filename":"meal.jpg","mediaText":"图片里是一份盖浇饭、饮料和水果，餐食比较完整。","mediaSummary":"图片里是一份盖浇饭、饮料和水果，餐食比较完整。","mediaUnderstandingStatus":"understood"}`, WaitForAI: false, AfterDelay: 50 * time.Millisecond,
		},
		{
			Type: enums.IMMessageTypeText, Content: "我吃得怎么样", WaitForAI: true,
			MustContainAny: []string{"不错", "挺", "丰盛", "可以"}, ExpectedIntent: "interaction", NeedsKnowledge: &f, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "电视怎么投屏", WaitForAI: true,
			MustContainAny: []string{"电视", "投屏", "WiFi"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "空调不制冷怎么办", WaitForAI: true,
			MustContainAny: []string{"空调", "房号", "资料"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeVoice, Content: "voice-wifi.amr", Payload: `{"filename":"voice-wifi.amr","mediaText":"早餐几点开始，停车免费吗？","mediaSummary":"客户语音询问早餐时间和停车是否免费。","mediaUnderstandingStatus":"understood"}`, WaitForAI: true,
			MustContainAny: []string{"早餐", "停车"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "电话多少", WaitForAI: true,
			MustContainAny: []string{"电话", "暂未配置", "联系"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &f, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "我要办理入住", WaitForAI: true,
			MustContainAny: []string{"入住", "小程序", "证件"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "办理入住的小程序发我", WaitForAI: true,
			MustContainAny: []string{"小程序", "入住"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &f, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeAttachment, Content: "invoice-info.pdf", Payload: `{"filename":"invoice-info.pdf","mediaText":"文件包含开票抬头、税号、邮箱和手机号。","mediaSummary":"文件包含开票抬头、税号、邮箱和手机号。","mediaUnderstandingStatus":"understood"}`, WaitForAI: false, AfterDelay: 50 * time.Millisecond,
		},
		{
			Type: enums.IMMessageTypeText, Content: "这个资料能开发票吗", WaitForAI: true,
			MustContainAny: []string{"发票", "资料"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "早餐和停车分别怎么弄", WaitForAI: true,
			MustContainAny: []string{"早餐", "停车"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "我住109，洗衣房在哪", WaitForAI: true,
			MustContainAny: []string{"洗衣", "109"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "过几天我又来了，电视打不开", WaitForAI: true, BackdateGap: 72 * time.Hour,
			MustContainAny: []string{"电视", "房号"}, Banned: []string{"109"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "定位再发一个", WaitForAI: true,
			MustContainAny: []string{"定位", "酒店"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &f, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "谢谢", WaitForAI: true,
			MustContainAny: []string{"不客气", "没事"}, ExpectedIntent: "interaction", NeedsKnowledge: &f, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "我没给你发语音大哥", WaitForAI: true,
			MustContainAny: []string{"收到", "没事", "不好意思", "抱歉"}, ExpectedIntent: "interaction", NeedsKnowledge: &f, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "浴帽和拖鞋在哪拿", WaitForAI: true,
			MustContainAny: []string{"浴帽", "拖鞋", "洗衣房", "1020"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		{
			Type: enums.IMMessageTypeText, Content: "发票多久能下载", WaitForAI: true,
			MustContainAny: []string{"发票", "1-3", "工作日", "下载"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
	}
	return scenario{
		ID:             "Q20",
		Category:       "continuous20",
		Name:           "单会话连续20轮：多意图、变量、媒体、旧上下文",
		Turns:          turns,
		Rapid:          false,
		RecordEachTurn: true,
	}
}

func continuous30Scenario() scenario {
	t := true
	f := false
	sc := continuous20Scenario()
	sc.ID = "Q30"
	sc.Category = "continuous30"
	sc.Name = "单会话连续30轮：多任务、变量、媒体、语音、文件、旧上下文"
	sc.Turns = append(sc.Turns,
		turn{
			Type: enums.IMMessageTypeText, Content: "电视投屏和WiFi密码一起说下", WaitForAI: true,
			MustContainAny: []string{"电视", "投屏", "WiFi", "密码"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "退房几点，晚退要收费吗", WaitForAI: true,
			MustContainAny: []string{"退房", "收费", "时间"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "小程序和电话都给我，顺便问下早餐在哪吃", WaitForAI: true,
			MustContainAny: []string{"早餐"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &t, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeImage, Content: "door-error.png", Payload: `{"filename":"door-error.png","mediaText":"图片显示门锁屏幕提示电量低，请联系工作人员或检查门锁。","mediaSummary":"门锁屏幕提示电量低。","mediaUnderstandingStatus":"understood"}`, WaitForAI: false, AfterDelay: 50 * time.Millisecond,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "这个门锁提示怎么处理", WaitForAI: true,
			MustContainAny: []string{"门锁", "房号", "资料", "联系"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeVoice, Content: "voice-mixed.amr", Payload: `{"filename":"voice-mixed.amr","mediaText":"我想问洗衣房在哪里，顺便把定位再发我一下。","mediaSummary":"客户语音询问洗衣房位置并要求发送定位。","mediaUnderstandingStatus":"understood"}`, WaitForAI: true,
			MustContainAny: []string{"洗衣", "定位"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &t, NeedsResource: &t, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeAttachment, Content: "receipt-material.pdf", Payload: `{"filename":"receipt-material.pdf","mediaText":"文件里有公司抬头、税号、邮箱、手机号和开票金额。","mediaSummary":"文件包含完整开票资料。","mediaUnderstandingStatus":"understood"}`, WaitForAI: false, AfterDelay: 50 * time.Millisecond,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "这份资料还缺什么才能开发票", WaitForAI: true,
			MustContainAny: []string{"发票", "资料"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "附近有吃的吗，停车入口也再说下", WaitForAI: true,
			MustContainAny: []string{"吃", "停车"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "我问的是停车不是早餐，你别串了", WaitForAI: true,
			MustContainAny: []string{"停车", "不好意思", "抱歉", "入口"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "那就只说停车入口怎么走", WaitForAI: true,
			MustContainAny: []string{"停车", "入口", "繁华大道", "九珑湾"}, ExpectedIntent: "hotel_info", NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		},
		turn{
			Type: enums.IMMessageTypeText, Content: "最后把定位再发我一下", WaitForAI: true,
			MustContainAny: []string{"定位", "酒店"}, ExpectedIntent: "hotel_variable", NeedsKnowledge: &f, NeedsResource: &t, NeedsHumanRoute: &f,
		},
	)
	return sc
}

func continuous50SafeScenario() scenario {
	t := true
	f := false
	knowledge := func(content string, requirements ...outcomeRequirement) turn {
		return turn{
			Type: enums.IMMessageTypeText, Content: content, WaitForAI: true,
			RequiredOutcomes: requirements, ExpectedIntent: "hotel_info",
			NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		}
	}
	interaction := func(content string, mustContainAny ...string) turn {
		return turn{
			Type: enums.IMMessageTypeText, Content: content, WaitForAI: true,
			MustContainAny: mustContainAny, ExpectedIntent: "interaction",
			NeedsKnowledge: &f, NeedsResource: &f, NeedsHumanRoute: &f,
		}
	}
	resource := func(content string, needsKnowledge bool, requirements ...outcomeRequirement) turn {
		return turn{
			Type: enums.IMMessageTypeText, Content: content, WaitForAI: true,
			RequiredOutcomes: requirements,
			NeedsKnowledge:   &needsKnowledge, NeedsResource: &t, NeedsHumanRoute: &f,
		}
	}
	voice := func(filename, mediaText, mediaSummary string, requirements ...outcomeRequirement) turn {
		payload, _ := json.Marshal(map[string]any{
			"filename": filename, "mediaText": mediaText, "mediaSummary": mediaSummary, "mediaUnderstandingStatus": "understood",
		})
		return turn{
			Type: enums.IMMessageTypeVoice, Content: filename, Payload: string(payload), WaitForAI: true,
			RequiredOutcomes: requirements, ExpectedIntent: "hotel_info",
			NeedsKnowledge: &t, NeedsResource: &f, NeedsHumanRoute: &f,
		}
	}
	withBanned := func(item turn, phrases ...string) turn {
		item.Banned = append([]string(nil), phrases...)
		return item
	}
	withMaxMessages := func(item turn, max int) turn {
		item.MaxReplyMessages = max
		return item
	}
	withLatencyBudget := func(item turn, warningMs, limitMs int64) turn {
		item.LatencyWarningMs = warningMs
		item.LatencyLimitMs = limitMs
		return item
	}
	allowResource := func(item turn) turn {
		item.NeedsResource = nil
		return item
	}
	robotCapabilityBanned := []string{"送到房间", "送上来", "送到门口", "送到房门", "直接送房间", "送进房间"}

	turns := []turn{
		// 01-10: basic hotel facts and complete fact slots.
		interaction("你好呀，小七，在吗？", "在", "你好", "您好"),
		knowledge("WiFi账号密码多少？", textOutcome("WiFi账号", "alilys"), textOutcome("WiFi密码", "yzbh8888")),
		knowledge("房间里有空调吗？", textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调")),
		resource("怎么办理入住？小程序也发我一下。", true,
			textOutcome("无传统前台", "没有传统前台", "无传统前台"), textOutcome("入住方式", "入住机", "小程序"), resourceOutcome("入住小程序资源", "mini_program")),
		knowledge("酒店房门怎么打开？", textOutcome("人脸开门", "扫脸", "刷脸", "人脸"), textOutcome("无需房卡", "无需房卡", "不用房卡")),
		knowledge("房间里有几瓶矿泉水，都是免费的吗？", textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费")),
		withBanned(knowledge("你们有外卖机器人吗？", robotExistenceOutcome()), robotCapabilityBanned...),
		knowledge("外卖地址应该怎么填？", textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")),
		knowledge("你们房间的布草是一客一换的吗？", textOutcome("布草一客一换", "一客一换")),
		knowledge("携程、抖音、美团的价格是一样的吗？", textOutcome("平台权益不同", "平台权益", "权益不一样", "权益不同", "平台活动", "价格不一样", "不一定一样"), priceComparisonCoverageOutcome()),

		// 11-20: resources, invoice, voice multi-question and room-type reference setup.
		knowledge("老客户优惠是什么情况？", textOutcome("当天情况", "当天情况", "当天房态"), textOutcome("门店管家电话", "18256022128")),
		resource("定位发我，停车场入口也一起说下。", true, textOutcome("停车场入口", "昭潭路", "工人文化宫西北门", "工人文化宫地面停车场"), resourceOutcome("酒店定位资源", "location")),
		knowledge("发票怎么开，多久能下载？", textOutcome("退房后申请", "退房后"), textOutcome("发票小程序", "自由家安心宿", "小程序"), textOutcome("下载时间", "1到3个工作日", "1至3个工作日", "1-3个工作日")),
		withMaxMessages(allowResource(voice("continuous-three.amr",
			"我想一起问三个问题：房间有没有空调，矿泉水有几瓶而且是不是免费的，还有怎么办理入住？",
			"客户一次询问多个酒店问题。",
			textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"), textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费"), textOutcome("入住方式", "入住机", "小程序"))), 3),
		knowledge("合柴、艺林、塔川、岭南这四种房型都有办公桌吗？", textOutcome("合柴", "合柴"), textOutcome("艺林", "艺林"), textOutcome("塔川", "塔川", "另外两种", "其他两个"), textOutcome("岭南", "岭南", "另外两种", "其他两个")),
		knowledge("那麦田呢？", textOutcome("麦田", "麦田"), textOutcome("办公桌回指", "办公桌", "桌子")),
		knowledge("酒店有早餐吗？", textOutcome("早餐政策", "不提供早餐", "暂不提供早餐", "没有早餐", "不含早餐")),
		knowledge("停车免费吗？停车场有充电桩吗？", textOutcome("停车费用", "停车免费", "免费停车", "停车是免费", "停车不收费", "不收费"), textOutcome("充电桩", "充电桩")),
		resource("入住小程序再发我一下。", false, resourceOutcome("入住小程序资源", "mini_program")),
		knowledge("外卖地址怎么填？", textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")),

		// 21-30: explicit repetition, text multi-question and five-question coverage.
		knowledge("外卖地址再说一遍，只要正确地址。", textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")),
		interaction("好的，谢谢你。", "不客气", "客气", "随时"),
		knowledge("房间里面有没有空调？", textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调")),
		withMaxMessages(allowResource(knowledge("再一起问三个：房间有空调吗、矿泉水有几瓶且免费吗、怎么办入住？",
			textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"), textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费"), textOutcome("入住方式", "入住机", "小程序"))), 3),
		resource("定位、小程序、停车入口三个一起给我。", true, textOutcome("停车入口", "昭潭路", "工人文化宫西北门", "工人文化宫地面停车场"), resourceOutcome("定位和小程序资源", "location", "mini_program")),
		knowledge("停车场入口怎么走？", textOutcome("停车入口", "昭潭路", "工人文化宫西北门", "工人文化宫地面停车场")),
		knowledge("房间里有几瓶矿泉水？", textOutcome("矿泉水数量", "两瓶", "2瓶")),
		knowledge("那这两瓶是不是都免费？", textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费")),
		withMaxMessages(allowResource(knowledge("我一次问五个：WiFi账号密码是什么、怎么办入住、房门怎么开、发票怎么开、停车收费吗？",
			textOutcome("WiFi账号", "alilys"), textOutcome("WiFi密码", "yzbh8888"), textOutcome("入住方式", "入住机", "小程序"), textOutcome("人脸开门", "扫脸", "刷脸", "人脸"), textOutcome("发票", "发票"), textOutcome("停车费用", "停车免费", "免费停车", "停车是免费", "停车不收费"))), 3),
		knowledge("刚才发票那条，确认是退房后申请，1到3个工作日下载，对吧？", textOutcome("退房后申请", "退房后"), textOutcome("下载时间", "1到3个工作日", "1至3个工作日", "1-3个工作日")),

		// 31-40: four-question voice, combined evidence and mixed resource turns.
		withBanned(withMaxMessages(voice("continuous-four.amr",
			"再一起问四个：你们有没有外卖机器人，外卖地址怎么填，布草是不是一客一换，携程抖音美团价格一样吗？",
			"客户语音询问四项酒店信息。",
			robotExistenceOutcome(), textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号"), textOutcome("布草一客一换", "一客一换"), textOutcome("平台权益不同", "平台权益", "权益不同", "权益不一样", "平台活动", "价格不一样", "不一定一样")), 3), robotCapabilityBanned...),
		knowledge("哪些房型既有沙发又有办公桌？", textOutcome("合柴", "合柴"), textOutcome("艺林", "艺林")),
		knowledge("哪些房型有办公桌？哪些房型同时还有沙发？", textOutcome("麦田办公桌", "麦田"), textOutcome("合柴交集", "合柴"), textOutcome("艺林交集", "艺林"), coverageOutcome("办公桌", "办公桌"), coverageOutcome("沙发", "沙发")),
		knowledge("麦田有办公桌吗？合柴和艺林都有沙发吗？", textOutcome("麦田办公桌", "麦田"), textOutcome("办公桌", "办公桌"), textOutcome("合柴", "合柴"), textOutcome("艺林", "艺林"), textOutcome("沙发", "沙发")),
		resource("请把酒店定位和入住小程序再发一次。", false, resourceOutcome("定位和小程序资源", "location", "mini_program")),
		withBanned(knowledge("你们有外卖机器人吗？外卖地址怎么填？", robotExistenceOutcome(), textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")), robotCapabilityBanned...),
		knowledge("房间有没有空调，矿泉水到底有几瓶？", textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"), textOutcome("矿泉水数量", "两瓶", "2瓶")),
		allowResource(knowledge("入住方式和开门方式分别说，不要混在一起。", textOutcome("入住方式", "入住机", "小程序"), textOutcome("人脸开门", "扫脸", "刷脸", "人脸"))),
		knowledge("谢谢，顺便再问下老客户优惠。", textOutcome("当天情况", "当天情况", "当天房态"), textOutcome("门店管家电话", "18256022128")),
		knowledge("停车场入口和充电桩位置一起说清楚。", textOutcome("停车入口", "昭潭路", "工人文化宫西北门", "工人文化宫地面停车场"), textOutcome("充电桩", "充电桩")),

		// 41-50: repeat stable facts, another voice run and final eight-question acceptance.
		knowledge("发票申请流程和下载时间再完整说一次。", textOutcome("退房后申请", "退房后"), textOutcome("发票小程序", "自由家安心宿", "小程序"), textOutcome("下载时间", "1到3个工作日", "1至3个工作日", "1-3个工作日")),
		knowledge("WiFi账号和密码再确认一下。", textOutcome("WiFi账号", "alilys"), textOutcome("WiFi密码", "yzbh8888")),
		knowledge("布草是不是一客一换？", textOutcome("布草一客一换", "一客一换")),
		knowledge("不同平台的房价为什么不一样？", textOutcome("平台权益不同", "平台权益", "权益不同", "权益不一样", "平台活动", "价格不一样", "不一定一样"), priceComparisonCoverageOutcome()),
		withMaxMessages(allowResource(voice("continuous-three-rewrite.amr",
			"麻烦分别告诉我，房间空调有没有，矿泉水配几瓶收不收费，入住要怎么操作。",
			"客户语音询问客房设施和入住。",
			textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"), textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费"), textOutcome("入住方式", "入住机", "小程序"))), 3),
		knowledge("外卖地址只说正确的酒店名和房间号格式。", textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")),
		knowledge("刚刚的外卖地址再复述一次。", textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号")),
		withBanned(withLatencyBudget(withMaxMessages(allowResource(knowledge("最后一次长问题，请分别回答八项：WiFi账号密码、入住方式、开门方式、矿泉水数量和费用、外卖机器人、外卖地址、停车和充电桩、发票流程。",
			textOutcome("WiFi账号", "alilys"), textOutcome("WiFi密码", "yzbh8888"), textOutcome("入住方式", "入住机", "小程序"), textOutcome("人脸开门", "扫脸", "刷脸", "人脸"), textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费"), robotExistenceOutcome(), textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号"), textOutcome("停车费用", "停车免费", "免费停车", "停车是免费", "停车不收费"), textOutcome("充电桩", "充电桩"), textOutcome("退房后申请", "退房后"), textOutcome("发票下载时间", "1到3个工作日", "1至3个工作日", "1-3个工作日"))), 3), 12000, 15000), robotCapabilityBanned...),
		withMaxMessages(allowResource(knowledge("最终确认：没有传统前台、能用入住机或小程序入住、扫脸开门、房间有空调、两瓶水免费、外卖地址写酒店名加楼层房间号，对吗？",
			textOutcome("无传统前台", "没有传统前台", "无传统前台"), textOutcome("入住方式", "入住机", "小程序"), textOutcome("人脸开门", "扫脸", "刷脸", "人脸"), textOutcome("空调", "有空调", "配有空调", "配了空调", "都有空调"), textOutcome("矿泉水数量", "两瓶", "2瓶"), textOutcome("矿泉水费用", "免费", "不收费"), textOutcome("南七店名称", "丽斯未来酒店合肥南七店"), textOutcome("房间号格式", "楼层房间号", "对应楼层房间号", "房间号"))), 3),
		interaction("谢谢，今天就问到这里。", "不客气", "客气", "好的", "好呀"),
	}

	return scenario{
		ID:             "Q50S",
		Category:       "continuous50-safe",
		Name:           "单会话连续50个AI轮次：逐题闭环、语音、回指、事实边界和协议安全",
		Turns:          turns,
		RecordEachTurn: true,
		Notes:          "连续运行三遍即形成150个AI轮次；每遍含117个事实槽位，资源投递和表达覆盖另行评分；全部问题保持AI可回答，不触发人工接管状态。",
	}
}

func selectScenarioIDs(cases []scenario, ids []string) ([]scenario, error) {
	byID := make(map[string]scenario, len(cases))
	for _, item := range cases {
		byID[strings.ToUpper(strings.TrimSpace(item.ID))] = item
	}
	selected := make([]scenario, 0, len(ids))
	missing := make([]string, 0)
	for _, id := range ids {
		item, ok := byID[strings.ToUpper(strings.TrimSpace(id))]
		if !ok {
			missing = append(missing, id)
			continue
		}
		selected = append(selected, item)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("scenario suite missing ids: %s", strings.Join(missing, ","))
	}
	return selected, nil
}

func short(id, msg string, must []string, intent string, knowledge *bool) scenario {
	return scenario{
		ID:              id,
		Category:        "short",
		Name:            msg,
		Turns:           []turn{{Type: enums.IMMessageTypeText, Content: msg, WaitForAI: true}},
		MustContainAny:  must,
		ExpectedIntent:  intent,
		NeedsKnowledge:  knowledge,
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func rapid(id string, messages []string, must []string, intent string, knowledge *bool) scenario {
	turns := make([]turn, 0, len(messages))
	for i, msg := range messages {
		turns = append(turns, turn{Type: enums.IMMessageTypeText, Content: msg, WaitForAI: i == len(messages)-1})
	}
	return scenario{
		ID:              id,
		Category:        "continuous",
		Name:            strings.Join(messages, " / "),
		Turns:           turns,
		Rapid:           true,
		MustContainAny:  must,
		ExpectedIntent:  intent,
		NeedsKnowledge:  knowledge,
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func media(id string, mediaType enums.IMMessageType, filename, mediaText, followUp string, must []string, intent string, knowledge *bool) scenario {
	payload, _ := json.Marshal(map[string]any{
		"filename":                 filename,
		"mediaText":                mediaText,
		"mediaSummary":             mediaText,
		"mediaUnderstandingStatus": "understood",
	})
	return scenario{
		ID:       id,
		Category: "media",
		Name:     filename + " / " + followUp,
		Turns: []turn{
			{Type: mediaType, Content: filename, Payload: string(payload), WaitForAI: false, AfterDelay: 50 * time.Millisecond},
			{Type: enums.IMMessageTypeText, Content: followUp, WaitForAI: true},
		},
		MustContainAny:  must,
		ExpectedIntent:  intent,
		NeedsKnowledge:  knowledge,
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func longScenario(id string) scenario {
	return scenario{
		ID:       id,
		Category: "long",
		Name:     "长对话：入住、WiFi、用品、感谢",
		Turns: []turn{
			{Type: enums.IMMessageTypeText, Content: "你好，我晚上入住", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "入住小程序发我", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "我住1508，WiFi密码多少", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "房间纸巾没了", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "谢谢", WaitForAI: true},
		},
		MustContainAny:  []string{"不客气", "有问题"},
		ExpectedIntent:  "social_confirm",
		NeedsKnowledge:  boolPtr(false),
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func longScenarioRoomExpiry(id string) scenario {
	return scenario{
		ID:       id,
		Category: "long",
		Name:     "长对话：旧房号不能污染新问题",
		Turns: []turn{
			{Type: enums.IMMessageTypeText, Content: "我住1201，电视怎么投屏", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "早餐有吗", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "空调不制冷", WaitForAI: true, BackdateGap: 13 * time.Hour},
		},
		MustContainAny:  []string{"房号", "空调", "同事"},
		Banned:          []string{"1201"},
		ExpectedIntent:  "service_request",
		NeedsKnowledge:  boolPtr(true),
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func longScenarioStoreIsolation(id string) scenario {
	return scenario{
		ID:       id,
		Category: "long",
		Name:     "长对话：当前门店变量优先",
		Turns: []turn{
			{Type: enums.IMMessageTypeText, Content: "上次住别的店电话是多少来着", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "算了，当前这个店定位发我", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "再把入住小程序发我", WaitForAI: true},
		},
		MustContainAny:  []string{"定位", "小程序", "当前"},
		ExpectedIntent:  "hotel_variable",
		NeedsKnowledge:  boolPtr(false),
		NeedsResource:   boolPtr(true),
		NeedsHumanRoute: boolPtr(false),
	}
}

func hundredTurnScenario() scenario {
	turns := make([]turn, 0, 100)
	seed := []string{"WiFi密码", "谢谢", "早餐几点", "停车在哪", "发票怎么开", "电视投屏", "纸巾没了", "小程序发我", "定位发我", "好的"}
	for i := 0; i < 100; i++ {
		turns = append(turns, turn{Type: enums.IMMessageTypeText, Content: fmt.Sprintf("%s，第%d次确认", seed[i%len(seed)], i+1), WaitForAI: true})
	}
	return scenario{
		ID:              "B100",
		Category:        "100-turn",
		Name:            "单会话 100-turn 暴力长测",
		Turns:           turns,
		MustContainAny:  []string{"好", "定位", "小程序", "发票", "WiFi"},
		ExpectedIntent:  "social_confirm",
		NeedsResource:   nil,
		NeedsKnowledge:  nil,
		NeedsHumanRoute: nil,
	}
}

func returningCustomerScenario() scenario {
	return scenario{
		ID:       "R01",
		Category: "returning",
		Name:     "隔几天回来：长期记忆和房号时效",
		Turns: []turn{
			{Type: enums.IMMessageTypeText, Content: "我住1606，想问下WiFi", WaitForAI: true},
			{Type: enums.IMMessageTypeText, Content: "三天后我又来了，空调不制冷", WaitForAI: true, BackdateGap: 72 * time.Hour},
		},
		MustContainAny:  []string{"房号", "空调", "同事"},
		Banned:          []string{"1606"},
		ExpectedIntent:  "service_request",
		NeedsKnowledge:  boolPtr(true),
		NeedsResource:   boolPtr(false),
		NeedsHumanRoute: boolPtr(false),
	}
}

func (sc scenario) withResource(v *bool) scenario {
	sc.NeedsResource = v
	return sc
}

func (sc scenario) withHuman(v *bool) scenario {
	sc.NeedsHumanRoute = v
	return sc
}

func (sc scenario) withRequiredOutcomes(requirements ...outcomeRequirement) scenario {
	sc.RequiredOutcomes = append([]outcomeRequirement(nil), requirements...)
	return sc
}

func boolPtr(v bool) *bool {
	return &v
}

func (r *runner) checkHealth() map[string]string {
	ret := map[string]string{}
	if db, err := sqls.DB().DB(); err == nil {
		if err := db.Ping(); err == nil {
			ret["mysql"] = "ok"
		} else {
			ret["mysql"] = err.Error()
		}
	}
	cfg := config.Current()
	qdrantHost := strings.TrimSpace(cfg.VectorDB.Host)
	if qdrantHost == "" {
		qdrantHost = "127.0.0.1"
	}
	scheme := "http"
	if cfg.VectorDB.UseTLS {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:6333/readyz", scheme, qdrantHost)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		ret["qdrant"] = err.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			ret["qdrant"] = "ok"
		} else {
			ret["qdrant"] = resp.Status
		}
	}
	ret["agent-desk"] = "checked outside via docker health"
	return ret
}

func (r *runner) cleanupData() map[string]int64 {
	db := sqls.DB()
	report := map[string]int64{}
	ids := uniqueInt64(r.conversation)
	customerIDs := uniqueInt64(r.customerIDs)
	if len(ids) > 0 {
		if db.Migrator().HasTable("t_ticket_progress") && db.Migrator().HasTable("t_ticket") {
			report["t_ticket_progress"] = execDelete(db, "t_ticket_progress", "ticket_id IN (SELECT id FROM t_ticket WHERE conversation_id IN ?)", ids)
		}
		deleteByConversation := []string{
			"t_agent_run_log",
			"t_channel_message_outbox",
			"t_message_sync_log",
			"t_conversation_interrupt",
			"t_conversation_session_summary",
			"t_ai_manual_resume_task",
			"t_conversation_route_state",
			"t_conversation_event_log",
			"t_conversation_read_state",
			"t_conversation_participant",
			"t_message",
			"t_conversation_assignment",
			"t_ticket",
		}
		for _, table := range deleteByConversation {
			report[table] = execDelete(db, table, "conversation_id IN ?", ids)
		}
		report["t_conversation"] = execDelete(db, "t_conversation", "id IN ?", ids)
	}
	if len(customerIDs) > 0 {
		for _, table := range []string{"t_store_customer_relation", "t_customer_identity", "t_customer_contact"} {
			report[table] += execDelete(db, table, "customer_id IN ?", customerIDs)
		}
		report["t_customer"] += execDelete(db, "t_customer", "id IN ?", customerIDs)
	}
	if len(r.assetIDs) > 0 {
		report["t_asset"] = execDelete(db, "t_asset", "id IN ?", uniqueInt64(r.assetIDs))
	}
	if db.Migrator().HasTable(&models.AIUsageEvent{}) {
		report["t_ai_usage_event"] = execDelete(db, "t_ai_usage_event", "request_id LIKE ?", r.runID+"%")
	}
	if db.Migrator().HasTable(&models.AIUsageGatewayCall{}) {
		report["t_ai_usage_gateway_call"] = execDelete(db, "t_ai_usage_gateway_call", "local_request_id LIKE ?", r.runID+"%")
	}
	report["residual_conversations"] = countResidual(db, "t_conversation", "id IN ?", ids)
	report["residual_messages"] = countResidual(db, "t_message", "conversation_id IN ?", ids)
	report["residual_runlogs"] = countResidual(db, "t_agent_run_log", "request_id LIKE ?", r.runID+"%")
	report["residual_customers"] = countResidual(db, "t_customer", "id IN ?", customerIDs)
	report["residual_assets"] = countResidual(db, "t_asset", "id IN ?", uniqueInt64(r.assetIDs))
	if db.Migrator().HasTable(&models.AIUsageEvent{}) {
		report["residual_usage_events"] = countResidual(db, "t_ai_usage_event", "request_id LIKE ?", r.runID+"%")
	}
	if db.Migrator().HasTable(&models.AIUsageGatewayCall{}) {
		report["residual_usage_gateway_calls"] = countResidual(db, "t_ai_usage_gateway_call", "local_request_id LIKE ?", r.runID+"%")
	}
	return report
}

func (r *runner) collectUsageSummary() usageSummary {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.AIUsageEvent{}) {
		return usageSummary{}
	}
	where := "request_id LIKE ?"
	arg := r.runID + "%"
	ret := usageSummary{}
	totals := struct {
		EventCount       int64 `gorm:"column:event_count"`
		DistinctRequests int64 `gorm:"column:distinct_requests"`
	}{}
	_ = db.Table("t_ai_usage_event").Where(where, arg).
		Select("COUNT(*) AS event_count, COUNT(DISTINCT request_id) AS distinct_requests").
		Scan(&totals).Error
	ret.EventCount = totals.EventCount
	ret.DistinctRequests = totals.DistinctRequests
	_ = db.Table("t_ai_usage_event").Where(where, arg).
		Select(`stage, provider, model, metric_source, status,
			COUNT(*) AS event_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(cached_prompt_tokens), 0) AS cached_prompt_tokens,
			COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
			COALESCE(SUM(request_count), 0) AS request_count,
			COALESCE(SUM(rerank_count), 0) AS rerank_count,
			COALESCE(SUM(estimated_context_tokens), 0) AS estimated_context_tokens`).
		Group("stage, provider, model, metric_source, status").
		Order("stage ASC, provider ASC, model ASC").
		Scan(&ret.Stages).Error
	return ret
}

func execDelete(db *gorm.DB, table string, where string, args ...any) int64 {
	if len(args) == 0 {
		return 0
	}
	tx := db.Table(table).Where(where, args...).Delete(nil)
	if tx.Error != nil {
		slog.Warn("cleanup delete failed", "table", table, "error", tx.Error)
		return 0
	}
	return tx.RowsAffected
}

func countResidual(db *gorm.DB, table string, where string, args ...any) int64 {
	if len(args) == 0 {
		return 0
	}
	var count int64
	tx := db.Table(table).Where(where, args...).Count(&count)
	if tx.Error != nil {
		slog.Warn("cleanup count failed", "table", table, "error", tx.Error)
	}
	return count
}

func uniqueInt64(values []int64) []int64 {
	seen := map[int64]bool{}
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		ret = append(ret, value)
	}
	return ret
}

func (r *runner) writeReports(startedAt time.Time, health map[string]string, usage usageSummary, cleanup map[string]int64, runErr error) (string, string, error) {
	if err := os.MkdirAll(r.outputDir, 0o755); err != nil {
		return "", "", err
	}
	base := fmt.Sprintf("reply-runtime-real-round%d-%s", r.round, r.runID)
	jsonlPath := filepath.Join(r.outputDir, base+".jsonl")
	mdPath := filepath.Join(r.outputDir, base+".md")
	var jsonl strings.Builder
	for _, rec := range r.records {
		b, _ := json.Marshal(rec)
		jsonl.Write(b)
		jsonl.WriteByte('\n')
	}
	if err := os.WriteFile(jsonlPath, []byte(jsonl.String()), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(mdPath, []byte(r.renderMarkdown(startedAt, health, usage, cleanup, runErr)), 0o644); err != nil {
		return "", "", err
	}
	return mdPath, jsonlPath, nil
}

func (r *runner) renderMarkdown(startedAt time.Time, health map[string]string, usage usageSummary, cleanup map[string]int64, runErr error) string {
	total := len(r.records)
	pass := 0
	errors := 0
	latencies := make([]int64, 0, total)
	generateLatencies := make([]int64, 0, total)
	var totalTokens, cachedTokens int
	categories := map[string]int{}
	categoryPass := map[string]int{}
	issueCounts := map[string]int{}
	factSlotsSatisfied := 0
	factSlotsExpected := 0
	for _, rec := range r.records {
		categories[rec.Category]++
		if rec.Passed {
			pass++
			categoryPass[rec.Category]++
		}
		if rec.Status == "error" || rec.ErrorMessage != "" {
			errors++
		}
		if rec.LatencyMs > 0 {
			latencies = append(latencies, rec.LatencyMs)
		}
		if rec.GenerateLatencyMs > 0 {
			generateLatencies = append(generateLatencies, rec.GenerateLatencyMs)
		}
		totalTokens += rec.TotalTokens
		cachedTokens += rec.CachedTokens
		factSlotsSatisfied += rec.FactSlotsSatisfied
		factSlotsExpected += rec.FactSlotsExpected
		for _, issue := range rec.Issues {
			issueCounts[issue]++
		}
	}
	avg, p90, max := latencyStats(latencies)
	generateAvg, generateP90, generateMax := latencyStats(generateLatencies)
	cacheRate := 0.0
	if totalTokens > 0 {
		cacheRate = float64(cachedTokens) / float64(totalTokens) * 100
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Reply Runtime Engine Real Eval Round %d\n\n", r.round))
	b.WriteString(fmt.Sprintf("- runId: `%s`\n", r.runID))
	b.WriteString(fmt.Sprintf("- startedAt: `%s`\n", startedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- elapsed: `%s`\n", time.Since(startedAt).Round(time.Millisecond)))
	b.WriteString(fmt.Sprintf("- wxWorkInstanceId: `%d`, storeId: `%d`, knowledgeBaseId: `%d`\n", r.instance.ID, r.instance.StoreID, r.instance.KnowledgeBaseID))
	b.WriteString("- entry: `MessageService.SendCustomerMessageWithRequestID -> AIReplyService -> Runtime`, ChannelID=0 so no WeCom outbound dispatch\n")
	if runErr != nil {
		b.WriteString(fmt.Sprintf("- runError: `%s`\n", runErr.Error()))
	}
	b.WriteString("\n## Health\n\n")
	for _, key := range sortedKeys(health) {
		b.WriteString(fmt.Sprintf("- %s: `%s`\n", key, health[key]))
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString(fmt.Sprintf("- passRate: %.1f%% (%d/%d)\n", percent(pass, total), pass, total))
	b.WriteString(fmt.Sprintf("- errors: %d\n", errors))
	b.WriteString(fmt.Sprintf("- latency: avg=%dms, p90=%dms, max=%dms\n", avg, p90, max))
	if len(generateLatencies) > 0 {
		b.WriteString(fmt.Sprintf("- generateLatency: avg=%dms, p90=%dms, max=%dms\n", generateAvg, generateP90, generateMax))
	}
	b.WriteString(fmt.Sprintf("- runtimeSummaryTokens: total=%d, cached=%d, cacheHitRate=%.1f%%\n", totalTokens, cachedTokens, cacheRate))
	if factSlotsExpected > 0 {
		b.WriteString(fmt.Sprintf("- requiredFactSlots: %d/%d\n", factSlotsSatisfied, factSlotsExpected))
	}
	if usage.EventCount > 0 {
		b.WriteString(fmt.Sprintf("- usageLedger: events=%d, requests=%d\n", usage.EventCount, usage.DistinctRequests))
	}
	b.WriteString("\n## Usage Ledger\n\n")
	if len(usage.Stages) == 0 {
		b.WriteString("- no usage events recorded\n")
	} else {
		for _, stage := range usage.Stages {
			b.WriteString(fmt.Sprintf("- `%s` provider=`%s` model=`%s` source=`%s` status=`%s`: events=%d, prompt=%d, completion=%d, cached=%d, reasoning=%d, requests=%d, reranks=%d, estimatedContext=%d\n",
				stage.Stage, stage.Provider, stage.Model, stage.MetricSource, stage.Status,
				stage.EventCount, stage.PromptTokens, stage.CompletionTokens, stage.CachedPromptTokens,
				stage.ReasoningTokens, stage.RequestCount, stage.RerankCount, stage.EstimatedContextTokens))
		}
	}
	b.WriteString("\n## Category Pass\n\n")
	for _, key := range sortedKeysInt(categories) {
		b.WriteString(fmt.Sprintf("- %s: %.1f%% (%d/%d)\n", key, percent(categoryPass[key], categories[key]), categoryPass[key], categories[key]))
	}
	b.WriteString("\n## Main Issues\n\n")
	if len(issueCounts) == 0 {
		b.WriteString("- none\n")
	} else {
		type pair struct {
			Key   string
			Count int
		}
		pairs := make([]pair, 0, len(issueCounts))
		for key, count := range issueCounts {
			pairs = append(pairs, pair{key, count})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Count > pairs[j].Count })
		for i, item := range pairs {
			if i >= 12 {
				break
			}
			b.WriteString(fmt.Sprintf("- %dx %s\n", item.Count, item.Key))
		}
	}
	b.WriteString("\n## Records\n\n")
	for _, rec := range r.records {
		status := "PASS"
		if !rec.Passed {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("### %s %s %s\n\n", status, rec.ScenarioID, rec.Name))
		b.WriteString(fmt.Sprintf("- category: `%s`, score: `%d`, status: `%s`, action: `%s`, latency: `%dms`, generateLatency: `%dms`\n", rec.Category, rec.Score, rec.Status, rec.FinalAction, rec.LatencyMs, rec.GenerateLatencyMs))
		b.WriteString(fmt.Sprintf("- intent: `%s/%s`, resourceAction: `%s`, knowledge: `%t`, tokens: `%d`, cached: `%d`\n", rec.Intent, rec.SubIntent, rec.ResourceAction, rec.KnowledgeHit, rec.TotalTokens, rec.CachedTokens))
		if rec.FactSlotsExpected > 0 {
			b.WriteString(fmt.Sprintf("- required fact slots: `%d/%d`\n", rec.FactSlotsSatisfied, rec.FactSlotsExpected))
		}
		if rec.ConfiguredMaxOutputTokens > 0 || rec.EffectiveMaxOutputTokens > 0 {
			b.WriteString(fmt.Sprintf("- model budget: configuredMaxOutput=`%d`, effectiveMaxOutput=`%d`\n", rec.ConfiguredMaxOutputTokens, rec.EffectiveMaxOutputTokens))
		}
		if len(rec.MediaContext) > 0 {
			b.WriteString("- media/file/voice context:\n")
			for _, item := range rec.MediaContext {
				b.WriteString(fmt.Sprintf("  - %s\n", preview(item, 180)))
			}
		}
		b.WriteString("- customer sequence:\n")
		for _, msg := range rec.Messages {
			b.WriteString(fmt.Sprintf("  - %s\n", preview(msg, 180)))
		}
		b.WriteString(fmt.Sprintf("- reply: %s\n", preview(rec.ReplyText, 240)))
		if len(rec.Issues) > 0 {
			b.WriteString("- issues: " + strings.Join(rec.Issues, " | ") + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Cleanup\n\n")
	for _, key := range sortedKeysInt64(cleanup) {
		b.WriteString(fmt.Sprintf("- %s: `%d`\n", key, cleanup[key]))
	}
	return b.String()
}

func latencyStats(values []int64) (avg, p90, max int64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var sum int64
	for _, value := range values {
		sum += value
		if value > max {
			max = value
		}
	}
	avg = sum / int64(len(values))
	idx := int(math.Ceil(float64(len(values))*0.9)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	p90 = values[idx]
	return avg, p90, max
}

func percent(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysInt(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysInt64(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func preview(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

var _ *sql.DB
