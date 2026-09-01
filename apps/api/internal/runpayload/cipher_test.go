package runpayload

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestNewRejectsInvalidEncryptionKeysWithoutEchoingThem(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "invalid base64", key: "private-key-not-base64"},
		{name: "wrong length", key: base64.StdEncoding.EncodeToString(make([]byte, 31))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.key)
			if err == nil {
				t.Fatal("New() accepted invalid encryption key")
			}
			if tt.key != "" && bytes.Contains([]byte(err.Error()), []byte(tt.key)) {
				t.Fatalf("New() error disclosed encryption key: %v", err)
			}
		})
	}
}

func TestCipherSealUsesRandomNonceAndRoundTrips(t *testing.T) {
	codec := newTestCipher(t)
	metadata := testMetadata()
	plaintext := []byte(`{"secret":"same input"}`)

	first, err := codec.Seal(metadata, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := codec.Seal(metadata, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatalf("identical plaintext produced identical envelopes: %x", first)
	}
	opened, err := codec.Open(metadata, first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %q; want %q", opened, plaintext)
	}
}

func TestCipherOpenRejectsMetadataChanges(t *testing.T) {
	codec := newTestCipher(t)
	metadata := testMetadata()
	envelope, err := codec.Seal(metadata, []byte("private payload"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(Metadata) Metadata
	}{
		{name: "run id", mutate: func(value Metadata) Metadata { value.RunID = "run-2"; return value }},
		{name: "sequence", mutate: func(value Metadata) Metadata { value.Sequence++; return value }},
		{name: "kind", mutate: func(value Metadata) Metadata { value.Kind = domain.RunPayloadNodeOutput; return value }},
		{name: "node id", mutate: func(value Metadata) Metadata { value.NodeID = "node-2"; return value }},
		{name: "node attempt", mutate: func(value Metadata) Metadata { value.NodeAttempt++; return value }},
		{name: "execution protocol", mutate: func(value Metadata) Metadata { value.ExecutionProtocol++; return value }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if opened, err := codec.Open(tt.mutate(metadata), envelope); err == nil {
				t.Fatalf("Open() accepted changed metadata and returned %q", opened)
			}
		})
	}
}

func TestCipherOpenRejectsTamperedEnvelope(t *testing.T) {
	codec := newTestCipher(t)
	metadata := testMetadata()
	envelope, err := codec.Seal(metadata, []byte("private payload"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 0xff
	if opened, err := codec.Open(metadata, tampered); err == nil {
		t.Fatalf("Open() accepted tampered envelope and returned %q", opened)
	}
}

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	codec, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func testMetadata() Metadata {
	return Metadata{
		RunID:             "run-1",
		Sequence:          7,
		Kind:              domain.RunPayloadNodeInput,
		NodeID:            "node-1",
		NodeAttempt:       2,
		ExecutionProtocol: domain.CurrentExecutionProtocol,
	}
}
