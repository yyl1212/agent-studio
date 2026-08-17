package engine

import (
	"sort"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func sortIssues(issues []domain.ValidationIssue) {
	sort.Slice(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		if left.EdgeID != right.EdgeID {
			return left.EdgeID < right.EdgeID
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Code < right.Code
	})
}

func issue(code, message, nodeID, edgeID, path string) domain.ValidationIssue {
	return domain.ValidationIssue{
		Code:    code,
		Message: message,
		NodeID:  nodeID,
		EdgeID:  edgeID,
		Path:    path,
	}
}
