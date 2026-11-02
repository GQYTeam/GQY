package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gqy/internal/session"
)

func TestSessionEndpoints(t *testing.T) {
	e := newTestServer(t, "", nil)

	rec := e.request(http.MethodPost, "/api/sessions", strings.NewReader(`{"workspace":"/tmp/ws"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建会话 = %d: %s", rec.Code, rec.Body.String())
	}
	var manifest session.Manifest
	decodeJSON(t, rec, &manifest)
	if manifest.SessionID == "" || manifest.Workspace != "/tmp/ws" {
		t.Fatalf("manifest = %+v", manifest)
	}
	id := manifest.SessionID

	rec = e.request(http.MethodGet, "/api/sessions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("列表 = %d", rec.Code)
	}
	var list struct {
		Sessions []session.Manifest `json:"sessions"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Sessions) != 1 || list.Sessions[0].SessionID != id {
		t.Fatalf("列表 = %+v", list.Sessions)
	}

	rec = e.request(http.MethodGet, "/api/sessions/"+id, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("详情 = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.request(http.MethodPut, "/api/sessions/"+id+"/draft", strings.NewReader(`{"draft":"草稿内容"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存草稿 = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.request(http.MethodGet, "/api/sessions/"+id+"/draft", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取草稿 = %d", rec.Code)
	}
	var draft struct {
		Draft string `json:"draft"`
	}
	decodeJSON(t, rec, &draft)
	if draft.Draft != "草稿内容" {
		t.Fatalf("草稿 = %q", draft.Draft)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("附件内容")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/uploads", &buf)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传 = %d: %s", rec.Code, rec.Body.String())
	}
	var uploaded struct {
		Path string `json:"path"`
	}
	decodeJSON(t, rec, &uploaded)
	data, err := os.ReadFile(uploaded.Path)
	if err != nil || string(data) != "附件内容" {
		t.Fatalf("附件落盘 = %q, %v", data, err)
	}
	if !strings.HasPrefix(uploaded.Path, filepath.Join(e.home, "sessions", id, "tmp", "uploads")) {
		t.Fatalf("附件路径越界: %s", uploaded.Path)
	}

	rec = e.request(http.MethodPost, "/api/sessions/"+id+"/uploads", bytes.NewReader(nil), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空上传应失败: %d", rec.Code)
	}

	rec = e.request(http.MethodPost, "/api/sessions/"+id+"/artifacts", strings.NewReader(`{"kind":"final","path":"out/a.txt"}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("产物 = %d: %s", rec.Code, rec.Body.String())
	}
	var artifact struct {
		Manifest session.Manifest `json:"manifest"`
	}
	decodeJSON(t, rec, &artifact)
	if len(artifact.Manifest.Artifacts) != 1 || artifact.Manifest.Artifacts[0].Path != "out/a.txt" {
		t.Fatalf("产物清单 = %+v", artifact.Manifest.Artifacts)
	}

	rec = e.request(http.MethodDelete, "/api/sessions/"+id, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除 = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.request(http.MethodGet, "/api/sessions/"+id, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("删除后详情 = %d, want 404", rec.Code)
	}
}

func TestSessionRejectsBadID(t *testing.T) {
	e := newTestServer(t, "", nil)
	rec := e.request(http.MethodGet, "/api/sessions/..%2Fescape", nil, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("非法 id 不应返回 200: %s", rec.Body.String())
	}
}

func TestSessionEmptyDraftDoesNotStoreJSON(t *testing.T) {
	e := newTestServer(t, "", nil)
	rec := e.request(http.MethodPost, "/api/sessions", strings.NewReader(`{}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建会话 = %d", rec.Code)
	}
	var manifest session.Manifest
	decodeJSON(t, rec, &manifest)

	rec = e.request(http.MethodPut, "/api/sessions/"+manifest.SessionID+"/draft", strings.NewReader(`{"draft":""}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存空草稿 = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.request(http.MethodGet, "/api/sessions/"+manifest.SessionID+"/draft", nil, nil)
	var body struct {
		Draft string `json:"draft"`
	}
	decodeJSON(t, rec, &body)
	if body.Draft != "" {
		t.Fatalf("空草稿被写成了 %q", body.Draft)
	}
}
