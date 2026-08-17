package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const defaultDatabaseURL = "postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable"

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	WebOrigin            string
	ModelProvider        string
	OpenAIBaseURL        string
	OpenAIAPIKey         string
	OpenAIDefaultModel   string
	HTTPNodeAllowPrivate bool
	MaxParallelNodes     int
	WorkflowTimeout      time.Duration
}

func Load() (Config, error) {
	maxParallelNodes, err := intEnv("MAX_PARALLEL_NODES", 4)
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

	cfg := Config{
		HTTPAddr:             stringEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:          stringEnv("DATABASE_URL", defaultDatabaseURL),
		WebOrigin:            stringEnv("WEB_ORIGIN", "http://localhost:5173"),
		ModelProvider:        stringEnv("MODEL_PROVIDER", "mock"),
		OpenAIBaseURL:        os.Getenv("OPENAI_BASE_URL"),
		OpenAIAPIKey:         os.Getenv("OPENAI_API_KEY"),
		OpenAIDefaultModel:   os.Getenv("OPENAI_DEFAULT_MODEL"),
		HTTPNodeAllowPrivate: allowPrivate,
		MaxParallelNodes:     maxParallelNodes,
		WorkflowTimeout:      workflowTimeout,
	}

	if cfg.ModelProvider == "openai-compatible" && cfg.OpenAIBaseURL == "" {
		return Config{}, fmt.Errorf("MODEL_PROVIDER=openai-compatible requires OPENAI_BASE_URL")
	}

	return cfg, nil
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
