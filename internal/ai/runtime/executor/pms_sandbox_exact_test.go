package executor

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var sandboxExactDemoCases = []struct {
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

func TestSandboxExactDemoQuestionsBypassModels(t *testing.T) {
	enableSandboxRuntimeTest(t)
	previousDB := sqls.DB()
	t.Cleanup(func() { sqls.SetDB(previousDB) })
	sqls.SetDB(nil)

	for _, tc := range sandboxExactDemoCases {
		t.Run(tc.scene, func(t *testing.T) {
			req := sandboxExactRunInput(tc.question)
			var db *gorm.DB
			var resource models.PMSSandboxResource
			if tc.scene == "F" {
				db, resource = setupSandboxExactResourceTest(t, req)
			}
			// An empty AIConfig and absent A-E database make accidental model or
			// order/member lookup dependencies fail this deterministic entry test.
			summary, err := NewService().ExecuteRun(context.Background(), req)
			if err != nil {
				t.Fatalf("ExecuteRun: %v", err)
			}
			trace := assertSandboxExactResult(t, summary, tc.scene, tc.reply)
			if tc.scene != "F" {
				if summary.ToolCallCount != 0 || len(trace.SandboxResources) != 0 {
					t.Fatalf("text-only demo queried a backend: %#v", summary)
				}
				return
			}
			if len(trace.SandboxResources) != 1 {
				t.Fatalf("missing original product reference: %#v", trace.SandboxResources)
			}
			ref := trace.SandboxResources[0]
			task := trace.Pipeline.ReplyPlan.TaskPlans[0]
			if ref.TaskID == "" || ref.TaskID != task.TaskID || ref.StoreID != resource.StoreID ||
				ref.DatasetID != resource.DatasetID || ref.ResourceID != resource.ID {
				t.Fatalf("product reference lost scope or task ownership: %#v", ref)
			}
			if task.ResourceAction != "" || len(trace.Pipeline.Intent.ResourceActions) != 0 {
				t.Fatalf("pillow card was rerouted to a hotel variable: %#v", task)
			}
			for _, model := range []any{&models.PMSOperation{}, &models.PMSSandboxOrder{}, &models.PMSSandboxMember{}, &models.PMSSandboxBinding{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("demo created business data in %T: %d", model, count)
				}
			}
			var stored models.PMSSandboxResource
			if err := db.First(&stored, resource.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, resource) {
				t.Fatalf("original bound card was changed: got %#v want %#v", stored, resource)
			}
		})
	}
}

func TestSandboxExactDemoAcceptsPunctuationWhitespaceAndSingleQuestionBurst(t *testing.T) {
	enableSandboxRuntimeTest(t)
	previousDB := sqls.DB()
	t.Cleanup(func() { sqls.SetDB(previousDB) })
	sqls.SetDB(nil)

	for _, tc := range []struct {
		name     string
		question string
		scene    string
		reply    string
		sources  int
	}{
		{"ascii punctuation", "有点吵,可以换个安静点的吗?", "C", sandboxExactDemoCases[2].reply, 1},
		{"whitespace", " \t我是什么会员？\n生日有什么福利？\r\n", "E", sandboxExactDemoCases[4].reply, 1},
		{"single source burst", utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [文字31] 酒店有停车场吗？"}), "A", sandboxExactDemoCases[0].reply, 1},
		{"split single question", utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [文字30] 我是什么会员？", "2. [文字31] 生日有什么福利？"}), "E", sandboxExactDemoCases[4].reply, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := sandboxExactRunInput(tc.question)
			handled, err := tryCompleteSandboxExactDemoReply(context.Background(), req, &RunResult{}, callbacks.NewRuntimeTraceCollector())
			if !handled || err != nil {
				t.Fatalf("complete demo input fell through to the normal AI path: handled=%v err=%v", handled, err)
			}
			summary, err := NewService().ExecuteRun(context.Background(), req)
			if err != nil {
				t.Fatalf("ExecuteRun: %v", err)
			}
			trace := assertSandboxExactResult(t, summary, tc.scene, tc.reply)
			if len(trace.Input.CurrentTurnSources) != tc.sources {
				t.Fatalf("current message sources were lost: %#v", trace.Input.CurrentTurnSources)
			}
		})
	}
}

func TestSandboxExactDemoMissingPillowCardDoesNotFallBackToModel(t *testing.T) {
	enableSandboxRuntimeTest(t)
	req := sandboxExactRunInput(sandboxExactDemoCases[5].question)
	db, resource := setupSandboxExactResourceTest(t, req)
	if err := db.Model(&models.PMSSandboxResource{}).Where("id = ?", resource.ID).Update("card_payload", "").Error; err != nil {
		t.Fatal(err)
	}
	summary, err := NewService().ExecuteRun(context.Background(), req)
	if err == nil || summary == nil || summary.Status != "error" || summary.ReplyText != "" {
		t.Fatalf("unavailable original card produced a success or model fallback: summary=%#v err=%v", summary, err)
	}
	if len(summary.ModelUsageCalls) != 0 || summary.TotalTokens != 0 {
		t.Fatalf("missing product card invoked a model: %#v", summary)
	}
	var trace callbacks.RuntimeTraceData
	if err := json.Unmarshal([]byte(summary.TraceData), &trace); err != nil {
		t.Fatal(err)
	}
	if len(trace.SandboxResources) != 0 || len(trace.SandboxOperationIDs) != 0 {
		t.Fatalf("missing product card produced a resource or operation: %#v", trace)
	}
}

func TestSandboxExactDemoDoesNotCaptureOtherQuestions(t *testing.T) {
	enableSandboxRuntimeTest(t)
	for _, question := range []string{
		"早餐几点开始？",
		"酒店有停车场吗？另外早餐几点开始？",
		"请翻译“酒店有停车场吗？”",
		"“酒店有停车场吗？”",
		"能帮我升个房吗？请帮我转人工。",
		utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [文字30] 酒店有停车场吗？", "2. [文字31] 早餐几点开始？"}),
		utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [图片30]", "2. [文字31] 酒店有停车场吗？"}),
		utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [图片30] 酒店有停车场吗？"}),
	} {
		t.Run(question, func(t *testing.T) {
			assertSandboxExactNotHandled(t, question)
		})
	}
}

func TestSandboxExactDemoRequiresExplicitTest2Sandbox(t *testing.T) {
	previous := config.CurrentOrNil()
	t.Cleanup(func() { config.SetCurrent(previous) })
	for _, tc := range []struct {
		name string
		pms  config.PMSConfig
	}{
		{"disabled", config.PMSConfig{Provider: "sandbox", Environment: "test-2", SandboxEnabled: true}},
		{"feature disabled", config.PMSConfig{Enabled: true, Provider: "sandbox", Environment: "test-2"}},
		{"production", config.PMSConfig{Enabled: true, Provider: "sandbox", Environment: "production", SandboxEnabled: true}},
		{"other test", config.PMSConfig{Enabled: true, Provider: "sandbox", Environment: "test-1", SandboxEnabled: true}},
		{"external provider", config.PMSConfig{Enabled: true, Provider: "hpms", Environment: "test-2", SandboxEnabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetCurrent(&config.Config{PMS: tc.pms})
			assertSandboxExactNotHandled(t, sandboxExactDemoCases[0].question)
		})
	}
}

func sandboxExactRunInput(question string) RunInput {
	return RunInput{
		Conversation: models.Conversation{ID: 23, CustomerID: 29},
		UserMessage: models.Message{
			ID: 31, ConversationID: 23, SenderType: enums.IMSenderTypeCustomer,
			MessageType: enums.IMMessageTypeText, Content: question, SeqNo: 2, ClientMsgID: "exact-demo-question",
		},
	}
}

func assertSandboxExactNotHandled(t *testing.T, question string) {
	t.Helper()
	summary := &RunResult{Status: "untouched", ReplyText: "untouched"}
	before := *summary
	collector := callbacks.NewRuntimeTraceCollector()
	traceBefore := collector.Marshal()
	handled, err := tryCompleteSandboxExactDemoReply(context.Background(), sandboxExactRunInput(question), summary, collector)
	if handled || err != nil {
		t.Fatalf("non-demo input was intercepted: handled=%v err=%v question=%q", handled, err, question)
	}
	if !reflect.DeepEqual(*summary, before) || collector.Marshal() != traceBefore {
		t.Fatalf("non-demo input changed the normal runtime state: %#v", summary)
	}
}

func assertSandboxExactResult(t *testing.T, summary *RunResult, scene, reply string) callbacks.RuntimeTraceData {
	t.Helper()
	if summary == nil || summary.Status != "completed" || summary.ReplyText != reply || summary.SkipReply || summary.Interrupted {
		t.Fatalf("approved reply was rewritten or not completed: %#v; want %q", summary, reply)
	}
	sanitized, err := SanitizeGeneratedReplyText(summary.ReplyText)
	if err != nil || sanitized != reply {
		t.Fatalf("Commit sanitation changed approved text: got %q err=%v want %q", sanitized, err, reply)
	}
	if summary.PromptTokens != 0 || summary.CompletionTokens != 0 || summary.TotalTokens != 0 ||
		summary.CachedPromptTokens != 0 || summary.ReasoningTokens != 0 || len(summary.ModelUsageCalls) != 0 {
		t.Fatalf("fixed demo invoked a model: %#v", summary)
	}
	var trace callbacks.RuntimeTraceData
	if err := json.Unmarshal([]byte(summary.TraceData), &trace); err != nil {
		t.Fatalf("invalid trace: %v", err)
	}
	if trace.Output.ReplyText != reply || trace.Pipeline.Generate.Status != "skipped" ||
		trace.Pipeline.Validate.Status != "passed" || len(trace.SandboxOperationIDs) != 0 {
		t.Fatalf("fixed reply was generated or proposed an operation: %#v", trace)
	}
	if len(trace.Pipeline.ReplyPlan.TaskPlans) != 1 {
		t.Fatalf("expected one fixed reply task: %#v", trace.Pipeline.ReplyPlan)
	}
	task := trace.Pipeline.ReplyPlan.TaskPlans[0]
	if task.SandboxScene != scene || !task.FixedReply || task.AnswerText == nil ||
		*task.AnswerText != reply || !task.ReplyRequired || task.NeedsHumanRoute {
		t.Fatalf("fixed task contract was lost: %#v", task)
	}
	return trace
}

func setupSandboxExactResourceTest(t *testing.T, req RunInput) (*gorm.DB, models.PMSSandboxResource) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Conversation{}, &models.ConversationRouteState{}, &models.Message{}, &models.PMSOperation{},
		&models.PMSSandboxStore{}, &models.PMSSandboxDataset{}, &models.PMSSandboxRoomType{},
		&models.PMSSandboxRoom{}, &models.PMSSandboxOrder{}, &models.PMSSandboxGrade{},
		&models.PMSSandboxMember{}, &models.PMSSandboxRule{}, &models.PMSSandboxResource{}, &models.PMSSandboxBinding{},
	); err != nil {
		t.Fatal(err)
	}
	previousDB := sqls.DB()
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(previousDB)
		raw, err := db.DB()
		if err == nil {
			_ = raw.Close()
		}
	})
	resource := models.PMSSandboxResource{
		ID: 17, StoreID: 7, DatasetID: 11, Code: "pillow", Name: "丽斯严选零压力护颈椎枕头",
		CardPayload: `{"username":"gh_product@app","appid":"wx-product","title":"枕头","page_path":"product/view?id=123","appicon":"https://example.test/pillow.png"}`,
		MessageType: string(enums.IMMessageTypeMiniProgram), SourceMessageID: 37, Enabled: true,
	}
	for _, row := range []any{
		&req.Conversation,
		&req.UserMessage,
		&models.ConversationRouteState{ConversationID: req.Conversation.ID, StoreID: resource.StoreID},
		&models.PMSSandboxStore{StoreID: resource.StoreID, ActiveDatasetID: resource.DatasetID},
		&models.PMSSandboxDataset{ID: resource.DatasetID, StoreID: resource.StoreID, Version: 1},
		&resource,
		&models.Message{
			ID: resource.SourceMessageID, ConversationID: req.Conversation.ID, SenderType: enums.IMSenderTypeAgent,
			MessageType: enums.IMMessageTypeMiniProgram, Content: resource.Name, Payload: resource.CardPayload,
			SeqNo: 1, ClientMsgID: "original-bound-pillow-card",
		},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, resource
}
