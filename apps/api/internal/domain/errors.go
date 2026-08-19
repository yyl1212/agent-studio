package domain

import (
	"context"
	"errors"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrNotFound         = errors.New("record not found")
	ErrRevisionConflict = errors.New("workflow revision conflict")
	ErrSlugConflict     = errors.New("workflow slug conflict")
)

type PublicError struct {
	Code    string              `json:"code"`
	Kind    agentnode.ErrorKind `json:"kind,omitempty"`
	Message string              `json:"message"`
	NodeID  string              `json:"nodeId,omitempty"`
}

func NewPublicNodeError(err error, nodeID string) *PublicError {
	kind := agentnode.KindOf(err)
	return &PublicError{
		Code:    "NODE_EXECUTION_FAILED",
		Kind:    kind,
		Message: publicNodeErrorMessage(kind),
		NodeID:  nodeID,
	}
}

func NewPublicRunError(err error) *PublicError {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &PublicError{Code: "RUN_CANCELLED", Kind: agentnode.ErrorKindCanceled, Message: "运行已取消"}
	}
	return &PublicError{Code: "RUN_FAILED", Kind: agentnode.KindOf(err), Message: "运行失败"}
}

func publicNodeErrorMessage(kind agentnode.ErrorKind) string {
	switch kind {
	case agentnode.ErrorKindConfig:
		return "节点配置无效"
	case agentnode.ErrorKindInput:
		return "节点输入无效"
	case agentnode.ErrorKindTemporary:
		return "节点暂时不可用"
	case agentnode.ErrorKindCanceled:
		return "节点执行已取消"
	default:
		return "节点执行失败"
	}
}

type ValidationIssue struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	NodeID         string `json:"nodeId,omitempty"`
	EdgeID         string `json:"edgeId,omitempty"`
	Path           string `json:"path,omitempty"`
	PackageName    string `json:"packageName,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`
}
