package httpapi

import (
	"errors"
	"net/http"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

var errHandlerPanic = errors.New("handler panic")

type ErrorResponse struct {
	Code      string                   `json:"code"`
	Message   string                   `json:"message"`
	RequestID string                   `json:"requestId,omitempty"`
	Issues    []domain.ValidationIssue `json:"issues,omitempty"`
	Details   *ErrorDetails            `json:"details,omitempty"`
}

type ErrorDetails struct {
	RunID string `json:"runId,omitempty"`
}

func writeRequestError(writer http.ResponseWriter, request *http.Request, err error) {
	writeJSON(writer, http.StatusBadRequest, ErrorResponse{
		Code:      "REQUEST_INVALID",
		Message:   "请求内容无效",
		RequestID: chimiddleware.GetReqID(request.Context()),
	})
}

func writeError(writer http.ResponseWriter, request *http.Request, err error) {
	response := ErrorResponse{RequestID: chimiddleware.GetReqID(request.Context())}
	status := http.StatusInternalServerError
	var validationError *workflow.ValidationError
	var templateValidationError *workflow.TemplateValidationError
	var retryAlreadyCreated *workflow.RunRetryAlreadyCreatedError
	switch {
	case nodeindex.CodeOf(err) == nodeindex.CodeNotFound:
		status, response.Code, response.Message = http.StatusNotFound, string(nodeindex.CodeNotFound), "节点包不存在"
	case nodeindex.CodeOf(err) == nodeindex.CodeContentInvalid:
		status, response.Code, response.Message = http.StatusBadRequest, "REQUEST_INVALID", "请求内容无效"
	case errors.Is(err, domain.ErrNotFound):
		status, response.Code, response.Message = http.StatusNotFound, "NOT_FOUND", "资源不存在"
	case errors.Is(err, domain.ErrRevisionConflict):
		status, response.Code, response.Message = http.StatusConflict, "WORKFLOW_REVISION_CONFLICT", "草稿版本已变化，请刷新后重试"
	case errors.Is(err, domain.ErrSlugConflict):
		status, response.Code, response.Message = http.StatusConflict, "WORKFLOW_SLUG_CONFLICT", "Agent 地址标识已存在"
	case errors.Is(err, domain.ErrWorkflowArchived):
		status, response.Code, response.Message = http.StatusConflict, "WORKFLOW_ARCHIVED", "工作流已归档，请先恢复后再操作"
	case errors.Is(err, workflow.ErrCursorInvalid):
		status, response.Code, response.Message = http.StatusBadRequest, "CURSOR_INVALID", "分页游标无效，请刷新后重试"
	case errors.Is(err, workflow.ErrRunNotCancellable):
		status, response.Code, response.Message = http.StatusConflict, "RUN_NOT_CANCELLABLE", "运行已结束，不能取消"
	case errors.Is(err, workflow.ErrRunNotRetryable):
		status, response.Code, response.Message = http.StatusConflict, "RUN_NOT_RETRYABLE", "当前运行不能完整重试"
	case errors.Is(err, workflow.ErrRunRetrySecretRequired):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "RUN_RETRY_SECRET_REQUIRED", "请重新填写完整重试所需的秘密值"
	case errors.As(err, &retryAlreadyCreated):
		status, response.Code, response.Message = http.StatusConflict, "RUN_RETRY_ALREADY_CREATED", "该重试请求已创建运行"
		if parsed, parseErr := uuid.Parse(retryAlreadyCreated.RunID); parseErr == nil && parsed.String() == retryAlreadyCreated.RunID {
			response.Details = &ErrorDetails{RunID: retryAlreadyCreated.RunID}
		}
	case errors.Is(err, workflow.ErrRunReplayUnavailable):
		status, response.Code, response.Message = http.StatusConflict, "RUN_REPLAY_UNAVAILABLE", "当前运行无法精确回放"
	case errors.Is(err, workflow.ErrRunSnapshotUnsupported):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "RUN_SNAPSHOT_UNSUPPORTED", "当前运行快照不受支持"
	case errors.Is(err, workflow.ErrRunFrozenEdgeUnavailable):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "RUN_FROZEN_EDGE_UNAVAILABLE", "历史分支输入无法重建"
	case errors.Is(err, workflow.ErrRunSideEffectConfirmationRequired):
		status, response.Code, response.Message = http.StatusConflict, "RUN_SIDE_EFFECT_CONFIRMATION_REQUIRED", "请确认可能产生的外部副作用"
	case errors.Is(err, workflow.ErrRunEntryInputInvalid):
		status, response.Code, response.Message = http.StatusBadRequest, "RUN_ENTRY_INPUT_INVALID", "重跑入口输入无效"
	case errors.Is(err, domain.ErrRunEventBudgetExceeded):
		status, response.Code, response.Message = http.StatusRequestEntityTooLarge, "RUN_EVENT_BUDGET_EXCEEDED", "运行事件超过大小限制"
	case errors.As(err, &templateValidationError):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "WORKFLOW_TEMPLATE_INVALID", "工作流模板校验失败"
		response.Issues = templateValidationError.Issues
	case errors.As(err, &validationError):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "WORKFLOW_INVALID", "工作流校验失败"
		response.Issues = validationError.Issues
	case errors.Is(err, workflow.ErrInvalidWorkflowInput), errors.Is(err, workflow.ErrInputValidation):
		status, response.Code, response.Message = http.StatusBadRequest, "REQUEST_INVALID", "请求内容无效"
	case errors.Is(err, workflow.ErrInvalidAgentPresentation):
		status, response.Code, response.Message = http.StatusBadRequest, "REQUEST_INVALID", "请求内容无效"
	case errors.Is(err, workflow.ErrInvalidWorkflowTemplate):
		status, response.Code, response.Message = http.StatusBadRequest, "REQUEST_INVALID", "请求内容无效"
	default:
		response.Code, response.Message = "INTERNAL_ERROR", "内部错误"
	}
	writeJSON(writer, status, response)
}
