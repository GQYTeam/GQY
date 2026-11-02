package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 统一错误码（C9、C10）。
const (
	CodeInvalidRequest    = "invalid_request"
	CodeAuthRequired      = "auth_required"
	CodePermissionDenied  = "permission_denied"
	CodeModelNotFound     = "model_not_found"
	CodeContextTooLong    = "context_length_exceeded"
	CodeRateLimited       = "rate_limited"
	CodeConnectionError   = "connection_error"
	CodeProviderError     = "provider_error"
	CodeUpstreamTimeout   = "upstream_timeout"
	CodeNoCandidate       = "no_candidate"
	CodeQueueTimeout      = "queue_timeout"
	CodeStreamInterrupted = "stream_interrupted"
	CodeInvalidResponse   = "invalid_response"
	CodeCanceled          = "canceled"
)

// Error 是适配器归一后的错误，网关只消费 Retryable 与 Code（C9）。
type Error struct {
	Code       string
	Retryable  bool
	HTTPStatus int
	Message    string
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// ClientStatus 把内部错误映射成对外 HTTP 状态码（C10）。
func (e *Error) ClientStatus() int {
	if e == nil {
		return 500
	}
	switch e.Code {
	case CodeInvalidRequest:
		return 400
	case CodeAuthRequired:
		return 401
	case CodePermissionDenied:
		return 403
	case CodeModelNotFound:
		return 404
	case CodeContextTooLong:
		if e.HTTPStatus == 413 {
			return 413
		}
		return 400
	case CodeRateLimited:
		return 429
	case CodeConnectionError, CodeProviderError, CodeInvalidResponse, CodeStreamInterrupted:
		return 502
	case CodeUpstreamTimeout:
		return 504
	case CodeNoCandidate, CodeQueueTimeout:
		return 503
	case CodeCanceled:
		return 408
	default:
		return 500
	}
}

// AsError 取出 provider 错误；普通错误归一成 provider_error。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return &Error{Code: CodeProviderError, Retryable: true, HTTPStatus: 502, Message: err.Error(), Err: err}
}

// FromStatus 按上游 HTTP 状态与错误体归一（C9、C10）。
func FromStatus(status int, body []byte) *Error {
	e := &Error{
		HTTPStatus: status,
		Retryable:  status == 429 || status >= 500,
		Message:    extractMessage(body),
	}
	switch {
	case status == 401:
		e.Code = CodeAuthRequired
	case status == 403:
		e.Code = CodePermissionDenied
	case status == 404:
		e.Code = CodeModelNotFound
	case status == 413:
		e.Code = CodeContextTooLong
	case status == 429:
		e.Code = CodeRateLimited
	case status >= 500:
		e.Code = CodeProviderError
	case status == 400:
		lower := strings.ToLower(e.Message)
		if strings.Contains(lower, "context") && (strings.Contains(lower, "length") || strings.Contains(lower, "token")) {
			e.Code = CodeContextTooLong
		} else {
			e.Code = CodeInvalidRequest
		}
	default:
		e.Code = CodeInvalidRequest
	}
	return e
}

// NetworkError 把连接、超时、取消归一。
func NetworkError(err error) *Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: CodeUpstreamTimeout, Retryable: true, HTTPStatus: 504, Message: "上游超时", Err: err}
	case errors.Is(err, context.Canceled):
		return &Error{Code: CodeCanceled, Retryable: false, HTTPStatus: 408, Message: "请求已取消", Err: err}
	default:
		return &Error{Code: CodeConnectionError, Retryable: true, HTTPStatus: 502, Message: "连接上游失败", Err: err}
	}
}

func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var openai struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &openai); err == nil && openai.Error.Message != "" {
		return truncate(openai.Error.Message)
	}
	var generic struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &generic); err == nil && generic.Message != "" {
		return truncate(generic.Message)
	}
	return truncate(strings.TrimSpace(string(body)))
}

func truncate(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// NewError 构造一个内部错误；用于网关终态。
func NewError(code string, retryable bool, status int, message string) *Error {
	return &Error{Code: code, Retryable: retryable, HTTPStatus: status, Message: message}
}

// ErrorCode 返回错误码，没有则返回空串。
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// Wrap 给错误补上下文，同时保留 provider.Error 的类型。
func Wrap(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}
