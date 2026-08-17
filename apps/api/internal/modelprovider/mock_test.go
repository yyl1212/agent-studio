package modelprovider

import (
	"context"
	"testing"
)

func TestMockIsDeterministic(t *testing.T) {
	provider := NewMock()
	got, err := provider.Complete(context.Background(), Request{Model: "mock", Prompt: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Mock 回复：你好" {
		t.Fatalf("text=%q", got.Text)
	}
}
