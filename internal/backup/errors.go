package backup

import (
	"context"
	"errors"
)

type Code string

const (
	CodeArchiveInvalid    Code = "BACKUP_ARCHIVE_INVALID"
	CodeChecksumMismatch  Code = "BACKUP_CHECKSUM_MISMATCH"
	CodeFormatUnsupported Code = "BACKUP_FORMAT_UNSUPPORTED"
	CodeRuntimeTooOld     Code = "BACKUP_RUNTIME_TOO_OLD"
	CodeSchemaNotCurrent  Code = "BACKUP_SCHEMA_NOT_CURRENT"
	CodeSizeLimit         Code = "BACKUP_SIZE_LIMIT_EXCEEDED"
	CodeAPIRunning        Code = "BACKUP_API_RUNNING"
	CodeTargetNotEmpty    Code = "BACKUP_TARGET_NOT_EMPTY"
	CodeReferenceInvalid  Code = "BACKUP_REFERENCE_INVALID"
	CodeCreateFailed      Code = "BACKUP_CREATE_FAILED"
	CodeRestoreFailed     Code = "BACKUP_RESTORE_FAILED"
)

type Error struct {
	code      Code
	operation string
	cause     error
}

func (err *Error) Error() string {
	return string(err.code) + ": " + err.operation
}

func (err *Error) Unwrap() error {
	return err.cause
}

func Wrap(code Code, operation string, cause error) error {
	return &Error{code: code, operation: operation, cause: cause}
}

func CodeOf(err error) Code {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

func sanitizePublicBackupError(err error) error {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return err
	}
	if coded, ok := err.(*Error); ok {
		return Wrap(coded.code, coded.operation, nil)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		safe := make([]error, 0, len(children))
		for _, child := range children {
			if sanitized := sanitizePublicBackupError(child); sanitized != nil {
				safe = append(safe, sanitized)
			}
		}
		if len(safe) == 1 {
			return safe[0]
		}
		return errors.Join(safe...)
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return Wrap(CodeRestoreFailed, "backup operation failed", nil)
}
