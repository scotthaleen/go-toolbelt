package ai

import (
	"testing"
)

func TestNewUsesDirectOpenAIDefaultModel(t *testing.T) {
	client := New(Config{})
	if client.cfg.Model != "gpt-4.1-mini" {
		t.Fatalf("default model = %q, want gpt-4.1-mini", client.cfg.Model)
	}
}
