package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const recoveryRunID = "00000000-0000-4000-8000-000000000940"

func TestRunRecoveryRoutesDelegateExactPathAndHidePrivateState(t *testing.T) {
	dependencies := fixtureDeps()
	recovery := dependencies.RunRecovery.(*fixtureRecoveryAPI)
	now := time.Now().UTC()
	recovery.view = workflow.RunRecoveryView{
		RunID: recoveryRunID, Status: domain.RunRecoveryRequired, Reason: domain.RecoveryUncertainEffect,
		RequestedAt: &now, Sequence: 7, Nodes: []workflow.RunRecoveryNode{{NodeID: "action", NodeAttempt: 2, Safety: agentnode.ExecutionSafetySideEffect, RetryAllowed: true, StartedAt: now}},
	}

	view := performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/"+recoveryRunID+"/recovery", "")
	if view.Code != http.StatusOK || recovery.getRunID != recoveryRunID || !strings.Contains(view.Body.String(), `"nodeId":"action"`) {
		t.Fatalf("status=%d body=%s runID=%q", view.Code, view.Body.String(), recovery.getRunID)
	}
	for _, forbidden := range []string{"ciphertext", "leaseOwner", "payload"} {
		if strings.Contains(strings.ToLower(view.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("response leaked %q: %s", forbidden, view.Body.String())
		}
	}

	retry := performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/"+recoveryRunID+"/recovery/nodes/action/retry", `{"nodeAttempt":2,"expectedSequence":7}`)
	if retry.Code != http.StatusOK || recovery.confirmRunID != recoveryRunID || recovery.confirmNodeID != "action" || recovery.confirmRequest.NodeAttempt != 2 || recovery.confirmRequest.ExpectedSequence != 7 {
		t.Fatalf("status=%d body=%s recovery=%+v", retry.Code, retry.Body.String(), recovery)
	}

	terminate := performRequest(NewRouter(dependencies), http.MethodPost, "/api/runs/"+recoveryRunID+"/recovery/terminate", `{"expectedSequence":8}`)
	if terminate.Code != http.StatusOK || recovery.terminateRunID != recoveryRunID || recovery.terminateRequest.ExpectedSequence != 8 {
		t.Fatalf("status=%d body=%s recovery=%+v", terminate.Code, terminate.Body.String(), recovery)
	}
}

func TestRunRecoveryMutationsRequireStrictJSON(t *testing.T) {
	for _, request := range []struct{ path, body string }{
		{path: "/api/runs/" + recoveryRunID + "/recovery/nodes/action/retry", body: `{"nodeAttempt":1,"expectedSequence":4,"unknown":true}`},
		{path: "/api/runs/" + recoveryRunID + "/recovery/terminate", body: `{"expectedSequence":4}{}`},
		{path: "/api/runs/not-a-uuid/recovery/terminate", body: `{"expectedSequence":4}`},
	} {
		dependencies := fixtureDeps()
		recorder := performRequest(NewRouter(dependencies), http.MethodPost, request.path, request.body)
		assertJSONError(t, recorder, http.StatusBadRequest, "REQUEST_INVALID")
		recovery := dependencies.RunRecovery.(*fixtureRecoveryAPI)
		if recovery.confirmCalls != 0 || recovery.terminateCalls != 0 {
			t.Fatal("invalid request reached recovery service")
		}
	}
}

func TestRunRecoveryErrorsUseStablePublicCodes(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{workflow.ErrRunRecoveryNotRequired, http.StatusConflict, "RUN_RECOVERY_NOT_REQUIRED"},
		{workflow.ErrRunRecoveryConflict, http.StatusConflict, "RUN_RECOVERY_CONFLICT"},
		{workflow.ErrRunRecoveryNodeNotFound, http.StatusNotFound, "RUN_RECOVERY_NODE_NOT_FOUND"},
		{workflow.ErrRunRecoveryRetryUnavailable, http.StatusConflict, "RUN_RECOVERY_RETRY_UNAVAILABLE"},
		{workflow.ErrRunRecoveryRetryExhausted, http.StatusConflict, "RUN_RECOVERY_RETRY_EXHAUSTED"},
		{workflow.ErrRunRecoveryPayloadUnavailable, http.StatusConflict, "RUN_RECOVERY_PAYLOAD_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			dependencies := fixtureDeps()
			dependencies.RunRecovery.(*fixtureRecoveryAPI).err = test.err
			recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/"+recoveryRunID+"/recovery", "")
			assertJSONError(t, recorder, test.status, test.code)
		})
	}

	dependencies := fixtureDeps()
	dependencies.RunRecovery.(*fixtureRecoveryAPI).err = errors.New("database secret")
	recorder := performRequest(NewRouter(dependencies), http.MethodGet, "/api/runs/"+recoveryRunID+"/recovery", "")
	assertJSONError(t, recorder, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(recorder.Body.String(), "database secret") {
		t.Fatal("internal recovery error leaked")
	}
}

type fixtureRecoveryAPI struct {
	view             workflow.RunRecoveryView
	err              error
	getRunID         string
	confirmRunID     string
	confirmNodeID    string
	confirmRequest   workflow.ConfirmNodeRetryRequest
	terminateRunID   string
	terminateRequest workflow.TerminateRecoveryRequest
	confirmCalls     int
	terminateCalls   int
}

func (api *fixtureRecoveryAPI) Get(_ context.Context, runID string) (workflow.RunRecoveryView, error) {
	api.getRunID = runID
	return api.view, api.err
}

func (api *fixtureRecoveryAPI) ConfirmNodeRetry(_ context.Context, runID, nodeID string, request workflow.ConfirmNodeRetryRequest) (domain.RunSummary, error) {
	api.confirmCalls++
	api.confirmRunID, api.confirmNodeID, api.confirmRequest = runID, nodeID, request
	return domain.RunSummary{ID: recoveryRunID, Status: domain.RunQueued}, api.err
}

func (api *fixtureRecoveryAPI) Terminate(_ context.Context, runID string, request workflow.TerminateRecoveryRequest) (domain.RunSummary, error) {
	api.terminateCalls++
	api.terminateRunID, api.terminateRequest = runID, request
	return domain.RunSummary{ID: recoveryRunID, Status: domain.RunCancelled}, api.err
}
