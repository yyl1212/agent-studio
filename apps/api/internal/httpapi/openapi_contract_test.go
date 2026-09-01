package httpapi

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestOpenAPIContractPublishesDurableRunRecovery(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := contractMap(t, document, "paths")
	for _, path := range []string{
		"/api/runs/{runId}/recovery",
		"/api/runs/{runId}/recovery/nodes/{nodeId}/retry",
		"/api/runs/{runId}/recovery/terminate",
	} {
		if _, ok := paths[path]; !ok {
			t.Errorf("missing path %s", path)
		}
	}
	schemas := contractMap(t, contractMap(t, document, "components"), "schemas")
	for _, name := range []string{"RunRecoveryView", "RunRecoveryNode", "ConfirmNodeRetryRequest", "TerminateRecoveryRequest"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema %s", name)
		}
	}
	runStatus := contractEnum(t, schemas, "Run", "status")
	for _, status := range []string{"queued", "recovery_required"} {
		if !containsContractValue(runStatus, status) {
			t.Errorf("Run.status missing %s: %v", status, runStatus)
		}
	}
	for schemaName, propertyName := range map[string]string{"NodeRun": "attempt", "RunEvent": "nodeAttempt"} {
		properties := contractMap(t, contractMapValue(t, schemas, schemaName), "properties")
		if _, ok := properties[propertyName]; !ok {
			t.Errorf("%s missing %s", schemaName, propertyName)
		}
	}
	recoveryWire, err := yaml.Marshal(schemas["RunRecoveryView"])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"leaseOwner", "leaseToken", "leaseExpiresAt", "ciphertext"} {
		if strings.Contains(strings.ToLower(string(recoveryWire)), strings.ToLower(forbidden)) {
			t.Errorf("RunRecoveryView leaked %s", forbidden)
		}
	}
	if _, exists := contractMap(t, contractMapValue(t, schemas, "RunRecoveryView"), "properties")["payload"]; exists {
		t.Error("RunRecoveryView leaked payload property")
	}
}

func contractMap(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	nested, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not a map", key)
	}
	return nested
}

func contractMapValue(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	nested, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not a schema map", key)
	}
	return nested
}

func contractEnum(t *testing.T, schemas map[string]any, schemaName, propertyName string) []any {
	t.Helper()
	properties := contractMap(t, contractMapValue(t, schemas, schemaName), "properties")
	property := contractMapValue(t, properties, propertyName)
	values, ok := property["enum"].([]any)
	if !ok {
		t.Fatalf("%s.%s enum missing", schemaName, propertyName)
	}
	return values
}

func containsContractValue(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
