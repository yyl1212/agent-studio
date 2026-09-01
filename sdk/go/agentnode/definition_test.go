package agentnode

import "testing"

func TestEffectiveExecutionSafetyDefaultsUnknownValuesToSideEffect(t *testing.T) {
	tests := []struct {
		name  string
		value ExecutionSafety
		want  ExecutionSafety
	}{
		{name: "pure", value: ExecutionSafetyPure, want: ExecutionSafetyPure},
		{name: "read only", value: ExecutionSafetyReadOnly, want: ExecutionSafetyReadOnly},
		{name: "side effect", value: ExecutionSafetySideEffect, want: ExecutionSafetySideEffect},
		{name: "empty", want: ExecutionSafetySideEffect},
		{name: "unknown", value: ExecutionSafety("unknown"), want: ExecutionSafetySideEffect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveExecutionSafety(tt.value); got != tt.want {
				t.Fatalf("EffectiveExecutionSafety(%q) = %q; want %q", tt.value, got, tt.want)
			}
		})
	}
}
