package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageMarshalImages(t *testing.T) {
	b, err := json.Marshal(Message{Role: "user", Content: "看图", Images: []string{"data:image/png;base64,AA=="}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"content":[`) || !strings.Contains(s, `image_url`) || !strings.Contains(s, `"text":"看图"`) {
		t.Fatalf("multimodal marshal: %s", s)
	}
	b, err = json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "image_url") || !strings.Contains(string(b), `"content":"hi"`) {
		t.Fatalf("plain marshal: %s", b)
	}
}
