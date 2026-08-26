package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunJSONOmitsAgentRequestKey(t *testing.T) {
	key := "00000000-0000-4000-8000-000000000901"
	run := Run{ID: "run-1", AgentRequestKey: &key}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "agentRequestKey") {
		t.Fatalf("run JSON leaked agent request key: %s", encoded)
	}
}
