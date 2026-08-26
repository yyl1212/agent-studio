package workflow

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

var ErrInvalidAgentPresentation = errors.New("invalid agent presentation")

func DefaultAgentPresentation(name, description string) domain.AgentPresentation {
	return domain.AgentPresentation{
		Title:       strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Accent:      domain.AgentAccentIndigo,
		SubmitLabel: "运行 Agent",
		ResultMode:  domain.AgentResultModeAuto,
	}
}

func NormalizeAgentPresentation(value domain.AgentPresentation) (domain.AgentPresentation, error) {
	value.Title = strings.TrimSpace(value.Title)
	value.Description = strings.TrimSpace(value.Description)
	value.SubmitLabel = strings.TrimSpace(value.SubmitLabel)
	if !validAgentPresentationText(value.Title, 1, 80) ||
		!validAgentPresentationText(value.Description, 0, 500) ||
		!validAgentPresentationText(value.SubmitLabel, 1, 24) ||
		!validAgentAccent(value.Accent) || !validAgentResultMode(value.ResultMode) {
		return domain.AgentPresentation{}, ErrInvalidAgentPresentation
	}
	return value, nil
}

func validAgentPresentationText(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func validAgentAccent(value domain.AgentAccent) bool {
	switch value {
	case domain.AgentAccentIndigo, domain.AgentAccentBlue, domain.AgentAccentTeal, domain.AgentAccentAmber, domain.AgentAccentRose:
		return true
	default:
		return false
	}
}

func validAgentResultMode(value domain.AgentResultMode) bool {
	switch value {
	case domain.AgentResultModeAuto, domain.AgentResultModeText, domain.AgentResultModeJSON:
		return true
	default:
		return false
	}
}
