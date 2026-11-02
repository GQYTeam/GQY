package memory

// 视图层负责脱敏与截断：owner 掩码、正文超 4000 字符截断并标 truncated。
// 存储里的原值一律不动（MEM6、memory.md §3.1）。

// MaskOwner 把归属人掩码成「首字符 + *** + 末字符」，例如 B***t。
func MaskOwner(owner string) string {
	runes := []rune(owner)
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return "*"
	}
	return string(runes[0]) + "***" + string(runes[len(runes)-1])
}

// TruncateContent 按字符数截断正文，返回截断后的正文与是否发生了截断。
func TruncateContent(content string) (string, bool) {
	runes := []rune(content)
	if len(runes) <= MaxContentRunes {
		return content, false
	}
	return string(runes[:MaxContentRunes]), true
}

// FactView 是事实的对外视图（已脱敏，memory.md §3.1）。
type FactView struct {
	ID               int64            `json:"id"`
	Kind             string           `json:"kind"`
	Content          string           `json:"content"`
	Truncated        bool             `json:"truncated,omitempty"`
	MemoryType       string           `json:"memory_type,omitempty"`
	TruthStatus      string           `json:"truth_status,omitempty"`
	Status           string           `json:"status,omitempty"`
	Importance       int              `json:"importance"`
	Confidence       float64          `json:"confidence"`
	Strength         float64          `json:"strength"`
	RecallCount      int              `json:"recall_count"`
	CreatedAt        string           `json:"created_at,omitempty"`
	UpdatedAt        string           `json:"updated_at,omitempty"`
	Visibility       string           `json:"visibility,omitempty"`
	Owner            string           `json:"owner,omitempty"`
	Tags             []string         `json:"tags,omitempty"`
	Subjects         []map[string]any `json:"subjects,omitempty"`
	SourceEpisodeIDs []int64          `json:"source_episode_ids,omitempty"`
	LegacyID         string           `json:"legacy_id,omitempty"`
}

// FactViewOf 把事实记录转成脱敏视图。
func FactViewOf(rec Record) FactView {
	content, truncated := TruncateContent(rec.Content)
	return FactView{
		ID:               rec.ID,
		Kind:             kindOf(rec),
		Content:          content,
		Truncated:        truncated,
		MemoryType:       rec.MemoryType,
		TruthStatus:      rec.TruthStatus,
		Status:           rec.Status,
		Importance:       rec.Importance,
		Confidence:       rec.Confidence,
		Strength:         rec.Strength,
		RecallCount:      rec.RecallCount,
		CreatedAt:        rec.CreatedAt,
		UpdatedAt:        rec.UpdatedAt,
		Visibility:       rec.Visibility,
		Owner:            ownerOf(rec),
		Tags:             []string(rec.Tags),
		Subjects:         []map[string]any(rec.Subjects),
		SourceEpisodeIDs: []int64(rec.SourceEpisodeIDs),
		LegacyID:         rec.LegacyID,
	}
}

// EpisodeView 是片段视图：公共字段 + 片段专属字段。
type EpisodeView struct {
	FactView
	Retention               string `json:"retention,omitempty"`
	UserMessage             string `json:"user_message,omitempty"`
	AssistantMessage        string `json:"assistant_message,omitempty"`
	MessagesTruncated       bool   `json:"messages_truncated,omitempty"`
	ExpiresAt               string `json:"expires_at,omitempty"`
	ConsolidatedAt          string `json:"consolidated_at,omitempty"`
	PromotedAt              string `json:"promoted_at,omitempty"`
	PromotionPending        bool   `json:"promotion_pending,omitempty"`
	SourceKey               string `json:"source_key,omitempty"`
	OriginKind              string `json:"origin_kind,omitempty"`
	OriginPlatform          string `json:"origin_platform,omitempty"`
	OriginAccountID         string `json:"origin_account_id,omitempty"`
	OriginConversationKind  string `json:"origin_conversation_kind,omitempty"`
	OriginConversationID    string `json:"origin_conversation_id,omitempty"`
	OriginSenderID          string `json:"origin_sender_id,omitempty"`
	OriginSenderDisplayName string `json:"origin_sender_display_name,omitempty"`
	OriginMessageID         string `json:"origin_message_id,omitempty"`
}

// EpisodeViewOf 把片段记录转成脱敏视图。
func EpisodeViewOf(rec Record) EpisodeView {
	user, userTruncated := TruncateContent(rec.UserMessage)
	assistant, assistantTruncated := TruncateContent(rec.AssistantMessage)
	return EpisodeView{
		FactView:                FactViewOf(rec),
		Retention:               rec.Retention,
		UserMessage:             user,
		AssistantMessage:        assistant,
		MessagesTruncated:       userTruncated || assistantTruncated,
		ExpiresAt:               rec.ExpiresAt,
		ConsolidatedAt:          rec.ConsolidatedAt,
		PromotedAt:              rec.PromotedAt,
		PromotionPending:        rec.PromotionPending,
		SourceKey:               rec.SourceKey,
		OriginKind:              rec.OriginKind,
		OriginPlatform:          rec.OriginPlatform,
		OriginAccountID:         rec.OriginAccountID,
		OriginConversationKind:  rec.OriginConversationKind,
		OriginConversationID:    rec.OriginConversationID,
		OriginSenderID:          rec.OriginSenderID,
		OriginSenderDisplayName: rec.OriginSenderDisplayName,
		OriginMessageID:         rec.OriginMessageID,
	}
}

// EvictedView 是被挤出上下文的轮次视图。
type EvictedView struct {
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	Content         string `json:"content"`
	Truncated       bool   `json:"truncated,omitempty"`
	Role            string `json:"role,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Visibility      string `json:"visibility,omitempty"`
	Owner           string `json:"owner,omitempty"`
	OriginSessionID string `json:"origin_session_id,omitempty"`
	LegacyID        string `json:"legacy_id,omitempty"`
}

// EvictedViewOf 把被挤出轮次转成脱敏视图。
func EvictedViewOf(rec Record) EvictedView {
	content, truncated := TruncateContent(rec.Content)
	return EvictedView{
		ID:              rec.ID,
		Kind:            kindOf(rec),
		Content:         content,
		Truncated:       truncated,
		Role:            rec.Role,
		Timestamp:       rec.Timestamp,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		Visibility:      rec.Visibility,
		Owner:           ownerOf(rec),
		OriginSessionID: rec.OriginSessionID,
		LegacyID:        rec.LegacyID,
	}
}

// RevisionView 是修订链的一行（审计用，只增不改）。
type RevisionView struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	MemoryID         int64   `json:"memory_id"`
	OldContent       string  `json:"old_content"`
	NewContent       string  `json:"new_content"`
	Truncated        bool    `json:"truncated,omitempty"`
	SourceEpisodeIDs []int64 `json:"source_episode_ids,omitempty"`
	CreatedAt        string  `json:"created_at,omitempty"`
	LegacyID         string  `json:"legacy_id,omitempty"`
}

// RevisionViewOf 把修订记录转成脱敏视图。
func RevisionViewOf(rec Record) RevisionView {
	oldContent, oldTruncated := TruncateContent(rec.OldContent)
	newContent, newTruncated := TruncateContent(rec.NewContent)
	return RevisionView{
		ID:               rec.ID,
		Kind:             kindOf(rec),
		MemoryID:         rec.MemoryID,
		OldContent:       oldContent,
		NewContent:       newContent,
		Truncated:        oldTruncated || newTruncated,
		SourceEpisodeIDs: []int64(rec.SourceEpisodeIDs),
		CreatedAt:        rec.CreatedAt,
		LegacyID:         rec.LegacyID,
	}
}

// kindOf 返回记录种类，空值按 fact 处理。
func kindOf(rec Record) string {
	if rec.Kind == "" {
		return KindFact
	}
	return rec.Kind
}

// ownerOf 取归属人并掩码：优先显示名，其次 principal（MEM6）。
func ownerOf(rec Record) string {
	if rec.OwnerDisplayName != "" {
		return MaskOwner(rec.OwnerDisplayName)
	}
	return MaskOwner(rec.OwnerPrincipal)
}
