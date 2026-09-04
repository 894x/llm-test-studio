package domain

import "testing"

func TestTestCaseAppliesToExactUpstreamModels(t *testing.T) {
	testCase := TestCase{ModelTargets: []string{"kimi-k3", "kimi-k2.6"}}
	for _, model := range []string{"kimi-k3", "kimi-k2.6"} {
		if !testCase.AppliesToModel(model) {
			t.Fatalf("AppliesToModel(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"kimi-k2.7-code", " KIMI-K3 ", ""} {
		if testCase.AppliesToModel(model) {
			t.Fatalf("AppliesToModel(%q) = true, want false", model)
		}
	}
	if !(TestCase{}).AppliesToModel("any-upstream-model") {
		t.Fatal("unscoped case did not apply to every upstream model")
	}
}

func TestValidateRejectsInvalidModelTargets(t *testing.T) {
	testCase := TestCase{
		EntityMeta: validEntityMeta(testCaseID), Key: "T001", Name: "case", Dimension: "compatibility",
		Protocol: ProtocolOpenAIChat, Enabled: true, Severity: CaseSeverityNormal,
		ExecutionMode: CaseExecutionAutomatic, Definition: validTestCaseDefinition(),
	}
	for _, targets := range [][]string{
		{""},
		{" kimi-k3"},
		{"kimi-k3", "kimi-k3"},
		{"kimi-k3\n"},
	} {
		testCase.ModelTargets = targets
		if err := testCase.Validate(); err == nil {
			t.Fatalf("Validate() accepted model targets %#v", targets)
		}
	}
}
