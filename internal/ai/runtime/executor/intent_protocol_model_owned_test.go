package executor

import "testing"

func TestIntentProtocolAcceptsModelOwnedResolvedMeaning(t *testing.T) {
	task := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	task.SubIntent = "parking_charging"
	task.ResolvedText = "酒店停车场有没有电车充电桩"
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("resolvedText meaning belongs to IntentDetect, got %v", err)
	}
}

func TestIntentProtocolAcceptsModelOwnedClassification(t *testing.T) {
	task := validRuntimeIntentProtocolTask("房间有空调吗", "social")
	task.Intent = "interaction"
	task.SubIntent = "chat"
	task.NeedsKnowledge = false
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("protocol validation must not locally reclassify task semantics: %v", err)
	}
}

func TestIntentProtocolAcceptsModelOwnedTaskCount(t *testing.T) {
	text := "早餐几点？停车免费吗？发票怎么开？"
	task := validRuntimeIntentProtocolTask(text, "compound_information")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err != nil {
		t.Fatalf("protocol validation must not derive task count from punctuation or keywords: %v", err)
	}
}

func TestIntentProtocolRejectsTaskTextInventedOutsidePrimarySource(t *testing.T) {
	task := validRuntimeIntentProtocolTask("停车免费吗", "price")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "早餐几点"); err == nil {
		t.Fatal("task text still must be traceable to its primary current-turn source")
	}
}
