package workflow

import (
	"fmt"
	"slices"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestValidateDraftBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  []string
	}{
		{name: "valid", types: []string{"start", "template", "end"}},
		{name: "missing both", types: []string{"template"}, want: []string{"WORKFLOW_START_COUNT", "WORKFLOW_END_COUNT"}},
		{name: "duplicate start", types: []string{"start", "start", "end"}, want: []string{"WORKFLOW_START_COUNT"}},
		{name: "duplicate end", types: []string{"start", "end", "end"}, want: []string{"WORKFLOW_END_COUNT"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := domain.Graph{SchemaVersion: 1}
			for index, nodeType := range test.types {
				graph.Nodes = append(graph.Nodes, domain.Node{ID: fmt.Sprintf("node-%d", index), Type: nodeType})
			}

			issues := validateDraftBoundaries(graph)
			got := make([]string, 0, len(issues))
			for _, issue := range issues {
				got = append(got, issue.Code)
				if issue.Path != "nodes" || issue.NodeID != "" || issue.EdgeID != "" {
					t.Fatalf("issue=%+v", issue)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("codes=%v want=%v", got, test.want)
			}
		})
	}
}
