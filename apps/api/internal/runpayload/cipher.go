package runpayload

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const envelopeVersion byte = 1

var (
	ErrInvalidEnvelope = errors.New("invalid run payload envelope")
	ErrAuthentication  = errors.New("run payload authentication failed")
)

type Metadata struct {
	RunID             string
	Sequence          int64
	Kind              domain.RunPayloadKind
	NodeID            string
	NodeAttempt       int
	ExecutionProtocol int16
}

type Cipher struct {
	aead   cipher.AEAD
	random io.Reader
}

func New(encodedKey string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("encryption key must be Base64 encoded and contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize run payload cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize run payload authentication: %w", err)
	}
	return &Cipher{aead: aead, random: rand.Reader}, nil
}

func (codec *Cipher) Seal(metadata Metadata, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(codec.random, nonce); err != nil {
		return nil, fmt.Errorf("generate run payload nonce: %w", err)
	}
	authenticated := codec.aead.Seal(nil, nonce, plaintext, metadataAAD(metadata))
	envelope := make([]byte, 1+len(nonce)+len(authenticated))
	envelope[0] = envelopeVersion
	copy(envelope[1:], nonce)
	copy(envelope[1+len(nonce):], authenticated)
	return envelope, nil
}

func (codec *Cipher) Open(metadata Metadata, envelope []byte) ([]byte, error) {
	minimumLength := 1 + codec.aead.NonceSize() + codec.aead.Overhead()
	if len(envelope) < minimumLength || envelope[0] != envelopeVersion {
		return nil, ErrInvalidEnvelope
	}
	nonceEnd := 1 + codec.aead.NonceSize()
	plaintext, err := codec.aead.Open(nil, envelope[1:nonceEnd], envelope[nonceEnd:], metadataAAD(metadata))
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func metadataAAD(metadata Metadata) []byte {
	sequence := make([]byte, 8)
	binary.BigEndian.PutUint64(sequence, uint64(metadata.Sequence))
	nodeAttempt := make([]byte, 8)
	binary.BigEndian.PutUint64(nodeAttempt, uint64(metadata.NodeAttempt))
	executionProtocol := make([]byte, 2)
	binary.BigEndian.PutUint16(executionProtocol, uint16(metadata.ExecutionProtocol))

	aad := make([]byte, 0, len(metadata.RunID)+len(metadata.Kind)+len(metadata.NodeID)+42)
	aad = appendLengthPrefixed(aad, []byte(metadata.RunID))
	aad = appendLengthPrefixed(aad, sequence)
	aad = appendLengthPrefixed(aad, []byte(metadata.Kind))
	aad = appendLengthPrefixed(aad, []byte(metadata.NodeID))
	aad = appendLengthPrefixed(aad, nodeAttempt)
	aad = appendLengthPrefixed(aad, executionProtocol)
	return aad
}

func appendLengthPrefixed(destination, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	destination = append(destination, length[:]...)
	return append(destination, value...)
}
