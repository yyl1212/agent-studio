package backup

import "errors"

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
