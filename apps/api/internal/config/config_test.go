package config

import "testing"

func TestLoadUsesSafeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MODEL_PROVIDER", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr=%q", cfg.HTTPAddr)
	}
	if cfg.ModelProvider != "mock" {
		t.Fatalf("provider=%q", cfg.ModelProvider)
	}
	if cfg.MaxParallelNodes != 4 {
		t.Fatalf("parallel=%d", cfg.MaxParallelNodes)
	}
}

func TestLoadRejectsOpenAIWithoutBaseURL(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("OPENAI_BASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected OPENAI_BASE_URL validation error")
	}
}
