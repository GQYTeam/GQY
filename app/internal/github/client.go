// 包 github：GitHub App OAuth —— 只读查看账户信息。
//
// OAuth 流程：前端跳授权页 → 用户授权 → GitHub 回调带 code → 换 access_token → 读账户。
// token 只存服务端 $GQY_HOME/github_token.json（0600），绝不进前端。
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	ClientID     string
	ClientSecret string
	Home         string
	HTTP         *http.Client
}

// tokenFile 是 token 落盘位置。
func (c *Client) tokenFile() string {
	return filepath.Join(c.Home, "github_token.json")
}

type token struct {
	AccessToken string    `json:"access_token"`
	Login       string    `json:"login"`
	Name        string    `json:"name"`
	AvatarURL   string    `json:"avatar_url"`
	HTMLURL     string    `json:"html_url"`
	Email       string    `json:"email"`
	CreatedAt   time.Time `json:"created_at"`
}

// AuthURL 生成 GitHub OAuth 授权页 URL。
func (c *Client) AuthURL(redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "read:user user:email") // 只读
	q.Set("state", fmt.Sprintf("%d", time.Now().UnixNano()))
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}

// Exchange 用授权码换 access_token，然后读用户信息，落盘。
func (c *Client) Exchange(ctx context.Context, code, redirectURI string) (*Account, error) {
	// code → access_token
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("GitHub 未返回 access_token")
	}

	// access_token → 用户信息
	acct, err := c.fetchUser(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}

	// 落盘
	t := token{
		AccessToken: tok.AccessToken,
		Login:       acct.Login,
		Name:        acct.Name,
		AvatarURL:   acct.AvatarURL,
		HTMLURL:     acct.HTMLURL,
		Email:       acct.Email,
		CreatedAt:   time.Now(),
	}
	b, _ := json.Marshal(t)
	if err := os.MkdirAll(c.Home, 0o700); err == nil {
		_ = os.WriteFile(c.tokenFile(), b, 0o600)
	}
	return acct, nil
}

func (c *Client) fetchUser(ctx context.Context, accessToken string) (*Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("读取用户信息失败（%d）: %s", resp.StatusCode, string(body))
	}
	var u struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
		Email     string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	// email 可能为空（私有），单独拉
	if u.Email == "" {
		u.Email = c.fetchEmail(ctx, accessToken)
	}
	return &Account{Login: u.Login, Name: u.Name, AvatarURL: u.AvatarURL, HTMLURL: u.HTMLURL, Email: u.Email}, nil
}

func (c *Client) fetchEmail(ctx context.Context, accessToken string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if json.NewDecoder(resp.Body).Decode(&emails) == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				return e.Email
			}
		}
	}
	return ""
}

// Account 是只读账户信息。
type Account struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
	HTMLURL   string `json:"htmlUrl"`
	Email     string `json:"email"`
}

// Me 读取已落盘的账户信息（不含 token）。
func (c *Client) Me() (*Account, error) {
	b, err := os.ReadFile(c.tokenFile())
	if err != nil {
		return nil, err
	}
	var t token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &Account{Login: t.Login, Name: t.Name, AvatarURL: t.AvatarURL, HTMLURL: t.HTMLURL, Email: t.Email}, nil
}

// Disconnect 删除落盘的 token。
func (c *Client) Disconnect() error {
	return os.Remove(c.tokenFile())
}
