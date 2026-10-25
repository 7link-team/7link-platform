package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Settings stores tenant retrieval policy; version zero in a response means the
// documented defaults have not yet been persisted.
type Settings struct {
	IndexStrategy    string    `gorm:"column:index_strategy;not null;default:LEXICAL"`
	SourcePermission string    `gorm:"column:source_permission;not null;default:WRITERS"`
	MemoryApproval   string    `gorm:"column:memory_approval;not null;default:RULES_ONLY"`
	TenantID         guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	CandidateLimit   int       `gorm:"column:candidate_limit;not null"`
	Version          int       `gorm:"column:version;not null"`
	UpdatedBy        guid.ID   `gorm:"column:updated_by;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Settings) TableName() string { return "knowledge_settings" }

// Source owns uploaded UTF-8 text. Remote locators are references only: this
// module never fetches a URL or repository without a configured connector.
type Source struct {
	OriginKind       *string    `gorm:"column:origin_kind;size:32"`
	OriginID         *guid.ID   `gorm:"column:origin_id"`
	OriginVersion    int        `gorm:"column:origin_version;not null;default:0"`
	WorkItemID       *guid.ID   `gorm:"column:work_item_id"`
	ClientAccepted   bool       `gorm:"column:client_accepted;not null;default:false"`
	TagsJSON         string     `gorm:"column:tags_json;not null;default:[]"`
	ArchivedAt       *time.Time `gorm:"column:archived_at"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	Scope            string     `gorm:"column:scope;not null"`
	ProjectID        *guid.ID   `gorm:"column:project_id"`
	AgentPrincipalID *guid.ID   `gorm:"column:agent_principal_id"`
	SourceKind       string     `gorm:"column:source_kind;not null"`
	Title            string     `gorm:"column:title;not null;size:200"`
	Locator          string     `gorm:"column:locator;not null;size:512"`
	Format           string     `gorm:"column:format;not null"`
	Content          string     `gorm:"column:content;not null"`
	ContentHash      string     `gorm:"column:content_hash;not null;size:64"`
	ChunkerVersion   int        `gorm:"column:chunker_version;not null"`
	State            string     `gorm:"column:state;not null"`
	StateReason      string     `gorm:"column:state_reason;not null;size:64"`
	ChunkCount       int        `gorm:"column:chunk_count;not null"`
	TextChars        int        `gorm:"column:text_chars;not null"`
	Attempts         int        `gorm:"column:attempts;not null"`
	IndexedAt        *time.Time `gorm:"column:indexed_at"`
	Version          int        `gorm:"column:version;not null"`
	CreatedBy        guid.ID    `gorm:"column:created_by;not null"`
	UpdatedBy        guid.ID    `gorm:"column:updated_by;not null"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Source) TableName() string { return "knowledge_sources" }

// Chunk links deterministic source fragments to canonical retrieval entries.
// Invalidating a chunk revokes its entry in the same transaction.
type Chunk struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	SourceID      guid.ID    `gorm:"column:source_id;not null"`
	EntryID       guid.ID    `gorm:"column:entry_id;not null"`
	Ordinal       int        `gorm:"column:ordinal;not null"`
	Kind          string     `gorm:"column:kind;not null"`
	HeadingPath   string     `gorm:"column:heading_path;not null;size:300"`
	Content       string     `gorm:"column:content;not null"`
	SearchTokens  string     `gorm:"column:search_tokens;not null;default:''"`
	ContentHash   string     `gorm:"column:content_hash;not null;size:64"`
	CharCount     int        `gorm:"column:char_count;not null"`
	TokenEstimate int        `gorm:"column:token_estimate;not null"`
	InvalidatedAt *time.Time `gorm:"column:invalidated_at"`
	LiveSlot      *int       `gorm:"column:live_slot"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Chunk) TableName() string { return "knowledge_chunks" }
