package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

const (
	defaultDatabaseURL                    = "postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable"
	defaultOTelServiceName                = "agent-studio-api"
	defaultOTelExportTimeoutMilliseconds  = 5000
	defaultOTelMetricIntervalMilliseconds = 10000
	maxOTelResourceAttributeCount         = 32
	maxOTelResourceAttributeLength        = 256
	maxDurationMilliseconds               = int64((1<<63)-1) / int64(time.Millisecond)
)

type Config struct {
	HTTPAddr                 string
	DatabaseURL              string
	WebOrigin                string
	ModelProvider            string
	OpenAIBaseURL            string
	OpenAIAPIKey             string
	OpenAIDefaultModel       string
	HTTPNodeAllowPrivate     bool
	NodeIndexCacheDir        string
	MaxParallelNodes         int
	MaxActiveAgentRuns       int
	WorkflowTimeout          time.Duration
	OTelEndpoint             string
	OTelServiceName          string
	OTelResourceAttributes   string
	OTelExportTimeout        time.Duration
	OTelCompression          string
	OTelMetricExportInterval time.Duration
}

func Load() (Config, error) {
	maxParallelNodes, err := intEnv("MAX_PARALLEL_NODES", 4)
	if err != nil {
		return Config{}, err
	}
	maxActiveAgentRuns, err := boundedIntEnv("MAX_ACTIVE_AGENT_RUNS", 8, 1, 128)
	if err != nil {
		return Config{}, err
	}
	workflowTimeout, err := durationEnv("WORKFLOW_TIMEOUT", 120*time.Second)
	if err != nil {
		return Config{}, err
	}
	allowPrivate, err := boolEnv("HTTP_NODE_ALLOW_PRIVATE", false)
	if err != nil {
		return Config{}, err
	}
	nodeIndexCacheDir, err := nodeindex.ResolveCacheDir(os.Getenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR"))
	if err != nil {
		return Config{}, fmt.Errorf("AGENT_STUDIO_NODE_INDEX_CACHE_DIR: %w", err)
	}
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if err := validateOTelEndpoint(otelEndpoint); err != nil {
		return Config{}, err
	}
	otelExportTimeout, err := millisecondsEnv("OTEL_EXPORTER_OTLP_TIMEOUT", defaultOTelExportTimeoutMilliseconds)
	if err != nil {
		return Config{}, err
	}
	otelMetricExportInterval, err := millisecondsEnv("OTEL_METRIC_EXPORT_INTERVAL", defaultOTelMetricIntervalMilliseconds)
	if err != nil {
		return Config{}, err
	}
	otelCompression, err := otelCompressionEnv()
	if err != nil {
		return Config{}, err
	}
	otelResourceAttributes := os.Getenv("OTEL_RESOURCE_ATTRIBUTES")
	if err := validateOTelResourceAttributes(otelResourceAttributes); err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:                 stringEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:              stringEnv("DATABASE_URL", defaultDatabaseURL),
		WebOrigin:                stringEnv("WEB_ORIGIN", "http://localhost:5173"),
		ModelProvider:            stringEnv("MODEL_PROVIDER", "mock"),
		OpenAIBaseURL:            os.Getenv("OPENAI_BASE_URL"),
		OpenAIAPIKey:             os.Getenv("OPENAI_API_KEY"),
		OpenAIDefaultModel:       os.Getenv("OPENAI_DEFAULT_MODEL"),
		HTTPNodeAllowPrivate:     allowPrivate,
		NodeIndexCacheDir:        nodeIndexCacheDir,
		MaxParallelNodes:         maxParallelNodes,
		MaxActiveAgentRuns:       maxActiveAgentRuns,
		WorkflowTimeout:          workflowTimeout,
		OTelEndpoint:             otelEndpoint,
		OTelServiceName:          stringEnv("OTEL_SERVICE_NAME", defaultOTelServiceName),
		OTelResourceAttributes:   otelResourceAttributes,
		OTelExportTimeout:        otelExportTimeout,
		OTelCompression:          otelCompression,
		OTelMetricExportInterval: otelMetricExportInterval,
	}

	if cfg.ModelProvider == "openai-compatible" && cfg.OpenAIBaseURL == "" {
		return Config{}, fmt.Errorf("MODEL_PROVIDER=openai-compatible requires OPENAI_BASE_URL")
	}

	return cfg, nil
}

func validateOTelEndpoint(value string) error {
	if value == "" {
		return nil
	}
	if strings.Contains(value, "#") {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must not include userinfo, query, or fragment")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute HTTP(S) URL with a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must not include userinfo, query, or fragment")
	}
	return nil
}

func millisecondsEnv(key string, fallback int) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return time.Duration(fallback) * time.Millisecond, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > maxDurationMilliseconds {
		return 0, fmt.Errorf("%s must be a positive integer in milliseconds", key)
	}
	return time.Duration(parsed) * time.Millisecond, nil
}

func otelCompressionEnv() (string, error) {
	compression := stringEnv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	if compression != "gzip" && compression != "none" {
		return "", fmt.Errorf("OTEL_EXPORTER_OTLP_COMPRESSION must be gzip or none")
	}
	return compression, nil
}

func validateOTelResourceAttributes(value string) error {
	if value == "" {
		return nil
	}
	attributes := strings.Split(value, ",")
	if len(attributes) > maxOTelResourceAttributeCount {
		return fmt.Errorf("OTEL_RESOURCE_ATTRIBUTES must contain at most %d entries", maxOTelResourceAttributeCount)
	}
	for _, item := range attributes {
		if len(item) > maxOTelResourceAttributeLength {
			return fmt.Errorf("OTEL_RESOURCE_ATTRIBUTES entries must contain at most %d bytes", maxOTelResourceAttributeLength)
		}
	}
	return nil
}

func stringEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func boundedIntEnv(key string, fallback, minimum, maximum int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}
