// 包 catalog：models.dev 模型目录的拉取、缓存与过滤。
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	modelsURL    = "https://models.dev/api.json"
	modelsTTL    = 6 * time.Hour
	maxModelsRes = 8 << 20
)

// 非对话模型类型：chat 模型不带 type 字段（或 type 为空）。显式排除专用类型。
var nonChatTypes = map[string]bool{
	"embedding":     true,
	"speech":        true,
	"tts":           true,
	"stt":           true,
	"transcription": true,
	"image":         true,
	"video":         true,
	"audio":         true,
	"reranking":     true,
	"rerank":        true,
	"moderation":    true,
	"decision":      true,
}

// Entry 是前端模型选择器消费的精简条目。
type Entry struct {
	Key          string   `json:"key"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Provider     string   `json:"provider"`
	ProviderName string   `json:"providerName"`
	API          string   `json:"api"`
	Description  string   `json:"description,omitempty"`
	Family       string   `json:"family,omitempty"`
	Reasoning    bool     `json:"reasoning"`
	ToolCall     bool     `json:"toolCall"`
	Attachment   bool     `json:"attachment"`
	Temperature  bool     `json:"temperature"`
	InputModes   []string `json:"inputModes"`
	OutputModes  []string `json:"outputModes"`
	Context      int      `json:"context"`
	Output       int      `json:"output"`
	Cost         *Cost    `json:"cost,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type Cost struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	CacheRead float64 `json:"cacheRead"`
}

// mdProvider / mdModel 是 models.dev api.json 的原始形状（只解析用到的字段）。
type mdProvider struct {
	ID     string             `json:"id"`
	Name   string             `json:"name"`
	API    string             `json:"api"`
	Models map[string]mdModel `json:"models"`
}

type mdModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Family      string `json:"family"`
	Type        string `json:"type"`
	Reasoning   bool   `json:"reasoning"`
	ToolCall    bool   `json:"tool_call"`
	Attachment  bool   `json:"attachment"`
	Temperature bool   `json:"temperature"`
	Status      string `json:"status"`
	Modalities  struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cache_read"`
	} `json:"cost"`
}

// Catalog 缓存 models.dev 结果，刷新失败时用旧数据兜底。
type Catalog struct {
	client  *http.Client
	mu      sync.Mutex
	data    []Entry
	fetched time.Time
}

func New(client *http.Client) *Catalog {
	return &Catalog{client: client}
}

func (c *Catalog) Snapshot() ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && time.Since(c.fetched) < modelsTTL {
		return c.data, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return c.staleOrErr(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gqy/1")

	resp, err := c.client.Do(req)
	if err != nil {
		return c.staleOrErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.staleOrErr(fmt.Errorf("models.dev 返回 %d", resp.StatusCode))
	}

	limited := io.LimitReader(resp.Body, maxModelsRes)
	var raw map[string]mdProvider
	if err := json.NewDecoder(limited).Decode(&raw); err != nil {
		return c.staleOrErr(err)
	}

	entries := make([]Entry, 0, 2048)
	for _, p := range raw {
		for _, m := range p.Models {
			if !isChatModel(m) {
				continue
			}
			id := m.ID
			key := p.ID + "/" + id
			entries = append(entries, Entry{
				Key:          key,
				ID:           id,
				Name:         m.Name,
				Provider:     p.ID,
				ProviderName: p.Name,
				API:          p.API,
				Description:  m.Description,
				Family:       m.Family,
				Reasoning:    m.Reasoning,
				ToolCall:     m.ToolCall,
				Attachment:   m.Attachment,
				Temperature:  m.Temperature,
				InputModes:   m.Modalities.Input,
				OutputModes:  m.Modalities.Output,
				Context:      m.Limit.Context,
				Output:       m.Limit.Output,
				Cost:         costOrNil(m),
				Status:       m.Status,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	c.data = entries
	c.fetched = time.Now()
	return entries, nil
}

func (c *Catalog) FetchedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetched
}

func (c *Catalog) staleOrErr(err error) ([]Entry, error) {
	if c.data != nil {
		return c.data, nil
	}
	return nil, err
}

func isChatModel(m mdModel) bool {
	if m.Type != "" && nonChatTypes[m.Type] {
		return false
	}
	if strings.TrimSpace(m.ID) == "" && strings.TrimSpace(m.Name) == "" {
		return false
	}
	return true
}

func costOrNil(m mdModel) *Cost {
	if m.Cost.Input == 0 && m.Cost.Output == 0 && m.Cost.CacheRead == 0 {
		return nil
	}
	return &Cost{Input: m.Cost.Input, Output: m.Cost.Output, CacheRead: m.Cost.CacheRead}
}
