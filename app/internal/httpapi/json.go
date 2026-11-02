package httpapi

import (
	"encoding/json"
	"net/http"
)

type apiError struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, `{"error":{"message":"内部序列化失败","type":"server_error","code":"internal_error"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: errorDetail{
		Message: message,
		Type:    errorType(status),
		Code:    code,
	}})
}

func errorType(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid_request_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "server_error"
	}
}
