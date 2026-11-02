package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RequestLogEntry 是 requests.jsonl 的一行（R7）。只记元数据，不落正文与密钥。
type RequestLogEntry struct {
	TS         string `json:"ts"`
	RequestID  string `json:"request_id"`
	Session    string `json:"session"`
	Tenant     string `json:"tenant,omitempty"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Account    string `json:"account"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status"`
	DurationMS int64  `json:"duration_ms"`
	Attempt    int    `json:"attempt"`
	ErrorCode  string `json:"error_code"`
}

// AppendRequestLog 向 $GQY_HOME/gateway/requests.jsonl 追加一行。
// 按 ST4「按行写完整行」：O_APPEND + 单行 JSON，不做原地改写。
func AppendRequestLog(home string, entry RequestLogEntry) error {
	dir := filepath.Join(home, "gateway")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- 路径来自 GQY_HOME
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}
