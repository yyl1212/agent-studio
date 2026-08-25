package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestPublicModelErrorsUseSafeAllowlistForLLMV2(t *testing.T) {
	tests := []struct {
		internal   string
		publicCode string
		message    string
	}{
		{internal: "model_structured_output_rejected", publicCode: "MODEL_STRUCTURED_OUTPUT_REJECTED", message: "模型不支持或拒绝结构化输出"},
		{internal: "model_provider_auth_failed", publicCode: "MODEL_PROVIDER_AUTH_FAILED", message: "模型服务认证配置无效"},
		{internal: "model_refused", publicCode: "MODEL_REFUSED", message: "模型拒绝生成此内容"},
		{internal: "model_output_invalid", publicCode: "MODEL_OUTPUT_INVALID", message: "模型返回结果不符合输出结构"},
	}
	for _, test := range tests {
		t.Run(test.internal, func(t *testing.T) {
			err := agentnode.NewError(agentnode.ErrorKindInternal, test.internal, errors.New("secret raw response"), map[string]any{"token": "secret"})
			got := NewPublicNodeError(err, "llm-2", "llm", "2")
			if got.Code != test.publicCode || got.Message != test.message || got.Kind != agentnode.ErrorKindInternal || got.NodeID != "llm-2" {
				t.Fatalf("public=%+v", got)
			}
			if strings.Contains(got.Message, "secret") {
				t.Fatalf("public error leaked cause: %+v", got)
			}
		})
	}
}

func TestPublicModelErrorsCannotBeSpoofedByOtherNodeIdentities(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		nodeType string
		version  string
	}{
		{name: "unknown code", code: "model_fake", nodeType: "llm", version: "2"},
		{name: "other node", code: "model_output_invalid", nodeType: "example.model", version: "1"},
		{name: "llm v1", code: "model_output_invalid", nodeType: "llm", version: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := agentnode.NewError(agentnode.ErrorKindInternal, test.code, errors.New("private"), nil)
			got := NewPublicNodeError(err, "node", test.nodeType, test.version)
			if got.Code != "NODE_EXECUTION_FAILED" || got.Message != "节点执行失败" {
				t.Fatalf("public=%+v", got)
			}
		})
	}
}

func TestPublicModelErrorsRequireInternalKind(t *testing.T) {
	err := agentnode.NewError(agentnode.ErrorKindInput, "model_output_invalid", errors.New("private"), nil)
	got := NewPublicNodeError(err, "llm-2", "llm", "2")
	if got.Code != "NODE_EXECUTION_FAILED" || got.Message != "节点输入无效" {
		t.Fatalf("public=%+v", got)
	}
}

func TestPublicRunEventBudgetErrorUsesStableSafeMessage(t *testing.T) {
	got := NewPublicRunError(fmt.Errorf("private payload details: %w", ErrRunEventBudgetExceeded))
	if got.Code != "RUN_EVENT_BUDGET_EXCEEDED" || got.Message != "运行事件超过大小限制" {
		t.Fatalf("public=%+v", got)
	}
	if strings.Contains(got.Message, "private") {
		t.Fatalf("public error leaked cause: %+v", got)
	}
}
