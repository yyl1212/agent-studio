package config

import (
	"path/filepath"
	"testing"
)

func TestLoadUsesSafeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MODEL_PROVIDER", "")
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")

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
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected OPENAI_BASE_URL validation error")
	}
}

func TestLoadResolvesNodeIndexCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node-index")
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeIndexCacheDir != dir {
		t.Fatalf("NodeIndexCacheDir=%q", cfg.NodeIndexCacheDir)
	}

	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "relative/cache")
	if _, err := Load(); err == nil {
		t.Fatal("expected relative node index cache dir to be rejected")
	}
}

func TestLoadValidatesMaxActiveAgentRuns(t *testing.T) {
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
	t.Setenv("MODEL_PROVIDER", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("MAX_ACTIVE_AGENT_RUNS", "")
	cfg, err := Load()
	if err != nil || cfg.MaxActiveAgentRuns != 8 {
		t.Fatalf("config=%+v error=%v", cfg, err)
	}
	for _, value := range []string{"0", "129", "bad"} {
		t.Setenv("MAX_ACTIVE_AGENT_RUNS", value)
		if _, err := Load(); err == nil {
			t.Fatalf("MAX_ACTIVE_AGENT_RUNS=%q should fail", value)
		}
	}
}
