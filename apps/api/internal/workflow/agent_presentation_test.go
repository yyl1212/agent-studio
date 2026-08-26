package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestNormalizeAgentPresentation(t *testing.T) {
	valid := domain.AgentPresentation{
		Title:       "  客户助手  ",
		Description: "  生成建议  ",
		Accent:      domain.AgentAccentTeal,
		SubmitLabel: "  开始生成  ",
		ResultMode:  domain.AgentResultModeAuto,
	}
	got, err := NormalizeAgentPresentation(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "客户助手" || got.Description != "生成建议" || got.SubmitLabel != "开始生成" {
		t.Fatalf("normalized=%+v", got)
	}

	invalid := []domain.AgentPresentation{
		{Title: "", Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto},
		{Title: "助手", Accent: "purple", SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto},
		{Title: "助手", Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: "markdown"},
		{Title: strings.Repeat("界", 81), Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto},
		{Title: "助手", Description: strings.Repeat("界", 501), Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto},
		{Title: "助手", Accent: domain.AgentAccentIndigo, SubmitLabel: strings.Repeat("界", 25), ResultMode: domain.AgentResultModeAuto},
	}
	for _, value := range invalid {
		if _, err := NormalizeAgentPresentation(value); !errors.Is(err, ErrInvalidAgentPresentation) {
			t.Fatalf("value=%+v error=%v", value, err)
		}
	}
}

func TestNormalizeAgentPresentationAcceptsUnicodeLimits(t *testing.T) {
	value := domain.AgentPresentation{
		Title:       strings.Repeat("界", 80),
		Description: strings.Repeat("界", 500),
		Accent:      domain.AgentAccentRose,
		SubmitLabel: strings.Repeat("界", 24),
		ResultMode:  domain.AgentResultModeJSON,
	}
	if _, err := NormalizeAgentPresentation(value); err != nil {
		t.Fatalf("boundary value error=%v", err)
	}
}

func TestDefaultAgentPresentationUsesWorkflowMetadata(t *testing.T) {
	got := DefaultAgentPresentation("知识助手", "回答问题")
	if got.Title != "知识助手" || got.Description != "回答问题" ||
		got.Accent != domain.AgentAccentIndigo || got.SubmitLabel != "运行 Agent" ||
		got.ResultMode != domain.AgentResultModeAuto {
		t.Fatalf("default=%+v", got)
	}
}
