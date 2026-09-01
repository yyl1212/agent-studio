package config

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testRunPayloadEncryptionKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func clearOTelEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_SERVICE_NAME",
		"OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_EXPORTER_OTLP_TIMEOUT",
		"OTEL_EXPORTER_OTLP_COMPRESSION",
		"OTEL_METRIC_EXPORT_INTERVAL",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("RUN_PAYLOAD_ENCRYPTION_KEY", testRunPayloadEncryptionKey)
}

func TestLoadRequiresValidRunPayloadEncryptionKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "invalid base64", key: "secret-not-base64"},
		{name: "wrong length", key: base64.StdEncoding.EncodeToString(make([]byte, 31))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearOTelEnv(t)
			t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
			t.Setenv("RUN_PAYLOAD_ENCRYPTION_KEY", tt.key)
			_, err := Load()
			if err == nil {
				t.Fatal("Load() accepted invalid RUN_PAYLOAD_ENCRYPTION_KEY")
			}
			if !strings.Contains(err.Error(), "RUN_PAYLOAD_ENCRYPTION_KEY") {
				t.Fatalf("Load() error lacks variable name: %v", err)
			}
			if tt.key != "" && strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error disclosed encryption key: %v", err)
			}
		})
	}
}

func TestLoadKeepsValidatedRunPayloadEncryptionKey(t *testing.T) {
	clearOTelEnv(t)
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RunPayloadEncryptionKey != testRunPayloadEncryptionKey {
		t.Fatalf("RunPayloadEncryptionKey was not preserved")
	}
}

func TestLoadUsesSafeDefaults(t *testing.T) {
	clearOTelEnv(t)
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
	clearOTelEnv(t)
	t.Setenv("MODEL_PROVIDER", "openai-compatible")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected OPENAI_BASE_URL validation error")
	}
}

func TestLoadResolvesNodeIndexCacheDir(t *testing.T) {
	clearOTelEnv(t)
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
	clearOTelEnv(t)
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

func TestLoadOTelDefaultsKeepExportDisabled(t *testing.T) {
	clearOTelEnv(t)
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OTelEndpoint != "" {
		t.Fatalf("OTelEndpoint = %q, want disabled", cfg.OTelEndpoint)
	}
	if cfg.OTelServiceName != "agent-studio-api" {
		t.Fatalf("OTelServiceName = %q", cfg.OTelServiceName)
	}
	if cfg.OTelResourceAttributes != "" {
		t.Fatalf("OTelResourceAttributes = %q", cfg.OTelResourceAttributes)
	}
	if cfg.OTelExportTimeout != 5*time.Second {
		t.Fatalf("OTelExportTimeout = %s", cfg.OTelExportTimeout)
	}
	if cfg.OTelCompression != "gzip" {
		t.Fatalf("OTelCompression = %q", cfg.OTelCompression)
	}
	if cfg.OTelMetricExportInterval != 10*time.Second {
		t.Fatalf("OTelMetricExportInterval = %s", cfg.OTelMetricExportInterval)
	}
}

func TestLoadAcceptsBoundedOTelConfiguration(t *testing.T) {
	clearOTelEnv(t)
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector.example/tenant/otel")
	t.Setenv("OTEL_SERVICE_NAME", "agent-studio-test")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test,service.namespace=studio")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "2500")
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "none")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "15000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OTelEndpoint != "https://collector.example/tenant/otel" {
		t.Fatalf("OTelEndpoint = %q", cfg.OTelEndpoint)
	}
	if cfg.OTelServiceName != "agent-studio-test" {
		t.Fatalf("OTelServiceName = %q", cfg.OTelServiceName)
	}
	if cfg.OTelResourceAttributes != "deployment.environment=test,service.namespace=studio" {
		t.Fatalf("OTelResourceAttributes = %q", cfg.OTelResourceAttributes)
	}
	if cfg.OTelExportTimeout != 2500*time.Millisecond || cfg.OTelMetricExportInterval != 15*time.Second {
		t.Fatalf("unexpected durations: timeout=%s interval=%s", cfg.OTelExportTimeout, cfg.OTelMetricExportInterval)
	}
	if cfg.OTelCompression != "none" {
		t.Fatalf("OTelCompression = %q", cfg.OTelCompression)
	}
}

func TestLoadRejectsUnsafeOTelEndpointsWithoutEchoingValues(t *testing.T) {
	for _, endpoint := range []string{
		"collector:4318",
		"ftp://collector.example/otel",
		"https:///otel",
		"https://token@collector.example/otel",
		"https://collector.example/otel?token=secret-query",
		"https://collector.example/otel#secret-fragment",
	} {
		t.Run(endpoint, func(t *testing.T) {
			clearOTelEnv(t)
			t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted unsafe endpoint %q", endpoint)
			}
			if strings.Contains(err.Error(), endpoint) || strings.Contains(err.Error(), "secret-") {
				t.Fatalf("validation error disclosed endpoint: %v", err)
			}
			if !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
				t.Fatalf("validation error lacks key: %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidOTelDurationsAndCompression(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "OTEL_EXPORTER_OTLP_TIMEOUT", value: "0"},
		{key: "OTEL_EXPORTER_OTLP_TIMEOUT", value: "-1"},
		{key: "OTEL_EXPORTER_OTLP_TIMEOUT", value: "1s"},
		{key: "OTEL_METRIC_EXPORT_INTERVAL", value: "0"},
		{key: "OTEL_METRIC_EXPORT_INTERVAL", value: "bad"},
		{key: "OTEL_EXPORTER_OTLP_COMPRESSION", value: "zstd"},
	}
	for _, test := range tests {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			clearOTelEnv(t)
			t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
			t.Setenv(test.key, test.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=%q", test.key, test.value)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("validation error lacks key %s: %v", test.key, err)
			}
		})
	}
}

func TestLoadRejectsOversizedOTelResourceAttributes(t *testing.T) {
	tests := []string{
		strings.Repeat("a", 257),
		strings.Repeat("key=value,", 32) + "key=value",
	}
	for _, attributes := range tests {
		clearOTelEnv(t)
		t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", attributes)

		_, err := Load()
		if err == nil {
			t.Fatal("Load() accepted oversized resource attributes")
		}
		if strings.Contains(err.Error(), attributes) {
			t.Fatalf("validation error disclosed resource attributes: %v", err)
		}
	}
}
