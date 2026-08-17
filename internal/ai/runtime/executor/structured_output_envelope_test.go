package executor

import "testing"

func TestNormalizeStructuredModelObjectKnownWrappers(t *testing.T) {
	want := `{"schemaVersion":"reply_output.v3","parts":[]}`
	for _, input := range []string{
		"```json\n" + want + "\n```",
		"```\n" + want + "\n```",
		"<think>internal reasoning</think>\n" + want,
		`"{\"schemaVersion\":\"reply_output.v3\",\"parts\":[]}"`,
	} {
		got, changed := normalizeStructuredModelObject(input)
		if !changed || got != want {
			t.Fatalf("input=%q got=%q changed=%v", input, got, changed)
		}
	}
}

func TestNormalizeStructuredModelObjectRejectsArbitraryProse(t *testing.T) {
	input := `好的，结果是 {"schemaVersion":"reply_output.v3","parts":[]}`
	got, changed := normalizeStructuredModelObject(input)
	if changed || got != input {
		t.Fatalf("arbitrary prose must not be stripped: got=%q changed=%v", got, changed)
	}
}
