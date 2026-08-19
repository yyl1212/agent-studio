package httpapi

import (
	"errors"
	"net/http"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

var errHandlerPanic = errors.New("handler panic")

type ErrorResponse struct {
	Code      string                   `json:"code"`
	Message   string                   `json:"message"`
	RequestID string                   `json:"requestId,omitempty"`
	Issues    []domain.ValidationIssue `json:"issues,omitempty"`
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
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status, response.Code, response.Message = http.StatusNotFound, "NOT_FOUND", "资源不存在"
	case errors.Is(err, domain.ErrRevisionConflict):
		status, response.Code, response.Message = http.StatusConflict, "WORKFLOW_REVISION_CONFLICT", "草稿版本已变化，请刷新后重试"
	case errors.Is(err, domain.ErrSlugConflict):
		status, response.Code, response.Message = http.StatusConflict, "WORKFLOW_SLUG_CONFLICT", "Agent 地址标识已存在"
	case errors.As(err, &templateValidationError):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "WORKFLOW_TEMPLATE_INVALID", "工作流模板校验失败"
		response.Issues = templateValidationError.Issues
	case errors.As(err, &validationError):
		status, response.Code, response.Message = http.StatusUnprocessableEntity, "WORKFLOW_INVALID", "工作流校验失败"
		response.Issues = validationError.Issues
	case errors.Is(err, workflow.ErrInvalidWorkflowInput), errors.Is(err, workflow.ErrInputValidation):
		status, response.Code, response.Message = http.StatusBadRequest, "REQUEST_INVALID", "请求内容无效"
	default:
		response.Code, response.Message = "INTERNAL_ERROR", "内部错误"
	}
	writeJSON(writer, status, response)
}
