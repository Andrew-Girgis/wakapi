package models

// TokenUsageEvent is a separate, additive accounting ledger. It does not share
// heartbeat hashes, insertion rules, or activity aggregation.
type TokenUsageEvent struct {
	UserID             string `json:"-" gorm:"column:user_id;primaryKey;size:255;index:idx_token_usage_range,priority:1;index:idx_token_usage_filters,priority:1;index:idx_token_usage_session,priority:1"`
	Source             string `json:"source" gorm:"column:source;primaryKey;size:32;index:idx_token_usage_session,priority:3"`
	SourceKey          string `json:"source_key" gorm:"column:source_key;primaryKey;size:255"`
	SessionKey         string `json:"session_key" gorm:"column:session_key;size:255;not null;index:idx_token_usage_session,priority:2"`
	OccurredMs         int64  `json:"occurred_ms" gorm:"column:occurred_ms;not null;index:idx_token_usage_range,priority:2;index:idx_token_usage_filters,priority:4"`
	Machine            string `json:"machine" gorm:"column:machine;not null;index:idx_token_usage_filters,priority:2"`
	Project            string `json:"project" gorm:"column:project;not null;index:idx_token_usage_filters,priority:3"`
	Model              string `json:"model" gorm:"column:model;not null"`
	InputTokens        int64  `json:"input_tokens" gorm:"column:input_tokens;not null"`
	CacheReadTokens    int64  `json:"cache_read_tokens" gorm:"column:cache_read_tokens;not null"`
	CacheWriteTokens   int64  `json:"cache_write_tokens" gorm:"column:cache_write_tokens;not null"`
	UnknownInputTokens int64  `json:"unknown_input_tokens" gorm:"column:unknown_input_tokens;not null;default:0"`
	OutputTokens       int64  `json:"output_tokens" gorm:"column:output_tokens;not null"`
	ReasoningTokens    int64  `json:"reasoning_tokens" gorm:"column:reasoning_tokens;not null"`
	ParserVersion      int    `json:"parser_version" gorm:"column:parser_version;not null"`
	RawSHA256          string `json:"raw_sha256" gorm:"column:raw_sha256;size:64;not null"`
	RawUsageJSON       string `json:"raw_usage_json" gorm:"column:raw_usage_json;type:text;not null;default:'{}'"`
	ObservedMs         int64  `json:"-" gorm:"column:observed_ms;not null"`
}

func (TokenUsageEvent) TableName() string { return "token_usage_events" }

// A single usage event may appear in copied/forked sessions on multiple machines.
// Every session is covered, but the tokens are counted only once.
type TokenUsageSessionRef struct {
	UserID     string `gorm:"column:user_id;primaryKey;size:255;index:idx_token_usage_refs_session,priority:1"`
	Source     string `gorm:"column:source;primaryKey;size:32;index:idx_token_usage_refs_session,priority:3"`
	SourceKey  string `gorm:"column:source_key;primaryKey;size:255"`
	SessionKey string `gorm:"column:session_key;primaryKey;size:255;index:idx_token_usage_refs_session,priority:2"`
}

func (TokenUsageSessionRef) TableName() string { return "token_usage_session_refs" }

// Retain suspect raw observations without using them in the ledger summary.
// An audited resolution can change status without deleting evidence.
type TokenUsageQuarantinedSession struct {
	UserID     string `gorm:"column:user_id;primaryKey;size:255"`
	Source     string `gorm:"column:source;primaryKey;size:32"`
	SessionKey string `gorm:"column:session_key;primaryKey;size:255"`
	Reason     string `gorm:"column:reason;type:text;not null"`
	Status     string `gorm:"column:status;size:16;not null"`
}

func (TokenUsageQuarantinedSession) TableName() string { return "token_usage_quarantined_sessions" }

// Pin Codex representation so a later fork/response replay cannot coexist with
// the same session's previously delivered cumulative accounting.
type TokenUsageAccountingMode struct {
	UserID     string `gorm:"column:user_id;primaryKey;size:255"`
	Source     string `gorm:"column:source;primaryKey;size:32"`
	SessionKey string `gorm:"column:session_key;primaryKey;size:255"`
	Mode       string `gorm:"column:mode;size:32;not null"`
}

func (TokenUsageAccountingMode) TableName() string { return "token_usage_accounting_modes" }

type TokenUsageConflict struct {
	UserID         string `gorm:"column:user_id;primaryKey;size:255"`
	Source         string `gorm:"column:source;primaryKey;size:32"`
	SourceKey      string `gorm:"column:source_key;primaryKey;size:255"`
	PreviousSHA256 string `gorm:"column:previous_sha256;size:64;not null"`
	IncomingSHA256 string `gorm:"column:incoming_sha256;primaryKey;size:64"`
	Reason         string `gorm:"column:reason;not null"`
	ObservedMs     int64  `gorm:"column:observed_ms;not null"`
}

func (TokenUsageConflict) TableName() string { return "token_usage_conflicts" }
