// Package persona 保存本机的人格与头像设置。
package persona

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gqy/internal/storage"
)

// Persona 是本机的助手身份：名称、一句话介绍、头像（data URL）与系统提示词。
type Persona struct {
	Name         string `json:"name"`
	Tagline      string `json:"tagline"`
	Avatar       string `json:"avatar"`
	SystemPrompt string `json:"system_prompt"`
}

// Default 返回默认人格。
func Default() Persona {
	return Persona{
		Name:    "GQY",
		Tagline: "本地优先的 AI 助手网关",
	}
}

// Path 返回数据根下的人格文件。
func Path(home string) string {
	return filepath.Join(home, "persona.json")
}

// Load 读取人格；文件缺失或损坏时回退默认值，不让启动失败。
func Load(home string) Persona {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		return Default()
	}
	var p Persona
	if err := json.Unmarshal(data, &p); err != nil {
		return Default()
	}
	if err := p.Normalize(); err != nil {
		return Default()
	}
	return p
}

// Save 校验并原子写人格文件（0600）。
func Save(home string, p Persona) error {
	if err := p.Normalize(); err != nil {
		return err
	}
	return storage.AtomicWriteJSON(Path(home), p, 0o600)
}

// Normalize 清理空白并校验字段；头像只接受 data:image/*。
func (p *Persona) Normalize() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Tagline = strings.TrimSpace(p.Tagline)
	p.SystemPrompt = strings.TrimSpace(p.SystemPrompt)
	p.Avatar = strings.TrimSpace(p.Avatar)
	if p.Name == "" {
		p.Name = "GQY"
	}
	if len([]rune(p.Name)) > 40 {
		return errors.New("persona: name 最多 40 个字符")
	}
	if len([]rune(p.Tagline)) > 120 {
		return errors.New("persona: tagline 最多 120 个字符")
	}
	if len(p.SystemPrompt) > 8000 {
		return errors.New("persona: system_prompt 最多 8000 字节")
	}
	if p.Avatar != "" {
		if !strings.HasPrefix(p.Avatar, "data:image/") {
			return errors.New("persona: avatar 必须是 data:image/* 数据 URL")
		}
		if len(p.Avatar) > 768*1024 {
			return errors.New("persona: avatar 超过 768 KB")
		}
		if _, _, ok := strings.Cut(p.Avatar, ","); !ok {
			return errors.New("persona: avatar 数据 URL 不完整")
		}
	}
	return nil
}

// SystemMessage 返回人格的系统提示词；为空返回空串。
func (p Persona) SystemMessage() string {
	if p.SystemPrompt == "" {
		return ""
	}
	return fmt.Sprintf("你是 %s。%s", p.Name, p.SystemPrompt)
}
