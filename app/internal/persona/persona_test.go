package persona

import (
	"os"
	"strings"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	p := Load(t.TempDir())
	if p.Name != "GQY" || p.Tagline == "" {
		t.Fatalf("default = %+v", p)
	}
}

func TestSaveLoad(t *testing.T) {
	home := t.TempDir()
	in := Persona{
		Name:         " 小影 ",
		Tagline:      " 本机助手 ",
		Avatar:       "data:image/png;base64,AAAA",
		SystemPrompt: "说话简短。",
	}
	if err := Save(home, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := Load(home)
	if out.Name != "小影" || out.Tagline != "本机助手" || out.Avatar != in.Avatar || out.SystemPrompt != "说话简短。" {
		t.Fatalf("Load = %+v", out)
	}
	info, err := os.Stat(Path(home))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %v, %v", info, err)
	}
}

func TestValidation(t *testing.T) {
	cases := []Persona{
		{Name: strings.Repeat("长", 41)},
		{Tagline: strings.Repeat("长", 121)},
		{Avatar: "https://example.com/avatar.png"},
		{Avatar: "data:image/png;base64,AAAA", SystemPrompt: strings.Repeat("x", 8001)},
	}
	for _, p := range cases {
		if err := p.Normalize(); err == nil {
			t.Fatalf("%+v 应校验失败", p)
		}
	}
}
