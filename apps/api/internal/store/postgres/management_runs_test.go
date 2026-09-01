package postgres

import (
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestRunManagementQuerySupportsQueuedAndRecoveryStatuses(t *testing.T) {
	statement, arguments, err := buildRunSummaryQuery(workflowservice.RunSummaryStoreQuery{
		Statuses: []domain.RunStatus{domain.RunQueued, domain.RunRecoveryRequired}, Limit: 51,
	})
	if err != nil || !strings.Contains(statement, "r.status=ANY") || len(arguments) != 2 {
		t.Fatalf("statement=%s arguments=%v error=%v", statement, arguments, err)
	}
	statuses, ok := arguments[0].([]string)
	if !ok || len(statuses) != 2 || statuses[0] != string(domain.RunQueued) || statuses[1] != string(domain.RunRecoveryRequired) {
		t.Fatalf("statuses=%#v", arguments[0])
	}
}
