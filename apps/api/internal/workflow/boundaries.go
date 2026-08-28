package workflow

import "github.com/yyl1212/agent-studio/apps/api/internal/domain"

func validateDraftBoundaries(graph domain.Graph) []domain.ValidationIssue {
	startCount := 0
	endCount := 0
	for _, node := range graph.Nodes {
		switch node.Type {
		case "start":
			startCount++
		case "end":
			endCount++
		}
	}

	issues := make([]domain.ValidationIssue, 0, 2)
	if startCount != 1 {
		issues = append(issues, domain.ValidationIssue{
			Code:    "WORKFLOW_START_COUNT",
			Message: "工作流必须恰有一个开始节点",
			Path:    "nodes",
		})
	}
	if endCount != 1 {
		issues = append(issues, domain.ValidationIssue{
			Code:    "WORKFLOW_END_COUNT",
			Message: "工作流必须恰有一个结束节点",
			Path:    "nodes",
		})
	}
	return issues
}
