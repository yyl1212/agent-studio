package domain

import (
	"context"
	"errors"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrNotFound                    = errors.New("record not found")
	ErrRevisionConflict            = errors.New("workflow revision conflict")
	ErrSlugConflict                = errors.New("workflow slug conflict")
	ErrWorkflowArchived            = errors.New("workflow archived")
	ErrWorkflowVersionNotFound     = errors.New("workflow version not found")
	ErrWorkflowSnapshotUnsupported = errors.New("workflow snapshot unsupported")
	ErrRollbackUndoUnavailable     = errors.New("workflow rollback undo unavailable")
	ErrRunEventSequence            = errors.New("run event sequence invalid")
	ErrRunEventBudgetExceeded      = errors.New("run event budget exceeded")
	ErrRunLeaseLost                = errors.New("run lease lost")
	ErrRunNotCancellable           = errors.New("run is not cancellable")
)

type PublicError struct {
	Code    string              `json:"code"`
	Kind    agentnode.ErrorKind `json:"kind,omitempty"`
	Message string              `json:"message"`
	NodeID  string              `json:"nodeId,omitempty"`
}

func NewPublicNodeError(err error, nodeID, nodeType, nodeVersion string) *PublicError {
	kind := agentnode.KindOf(err)
	if nodeType == "llm" && nodeVersion == "2" {
		var nodeErr *agentnode.NodeError
		if errors.As(err, &nodeErr) && nodeErr.Kind == agentnode.ErrorKindInternal {
			if code, message, ok := publicLLMV2Error(nodeErr.Code); ok {
				return &PublicError{Code: code, Kind: kind, Message: message, NodeID: nodeID}
			}
		}
	}
	return &PublicError{
		Code:    "NODE_EXECUTION_FAILED",
		Kind:    kind,
		Message: publicNodeErrorMessage(kind),
		NodeID:  nodeID,
	}
}

func publicLLMV2Error(code string) (string, string, bool) {
	switch code {
	case "model_structured_output_rejected":
		return "MODEL_STRUCTURED_OUTPUT_REJECTED", "模型不支持或拒绝结构化输出", true
	case "model_provider_auth_failed":
		return "MODEL_PROVIDER_AUTH_FAILED", "模型服务认证配置无效", true
	case "model_refused":
		return "MODEL_REFUSED", "模型拒绝生成此内容", true
	case "model_output_invalid":
		return "MODEL_OUTPUT_INVALID", "模型返回结果不符合输出结构", true
	default:
		return "", "", false
	}
}

func NewPublicRunError(err error) *PublicError {
	if errors.Is(err, ErrRunEventBudgetExceeded) {
		return &PublicError{Code: "RUN_EVENT_BUDGET_EXCEEDED", Message: "运行事件超过大小限制"}
	}
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
