package workspace

import "testing"

func TestSlugifyAndFeatureBranch(t *testing.T) {
	tests := map[string]string{
		"Payment Flow":       "payment-flow",
		"  API_v2 Cleanup  ": "api-v2-cleanup",
		"支付链路优化":             "req-5ec9da01",
	}

	for input, want := range tests {
		if got := Slugify(input); got != want {
			t.Fatalf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}

	if got := FeatureBranch("payment-flow"); got != "feature/payment-flow" {
		t.Fatalf("FeatureBranch() = %q", got)
	}
}
