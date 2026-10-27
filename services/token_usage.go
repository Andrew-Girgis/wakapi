package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var usageHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

type TokenUsageService struct{ db *gorm.DB }

func NewTokenUsageService(db *gorm.DB) *TokenUsageService { return &TokenUsageService{db: db} }

type UsageIngestResult struct {
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Conflicts int `json:"conflicts"`
}

func validateUsage(e models.TokenUsageEvent) error {
	if e.Source != "claude" && e.Source != "pi" && e.Source != "codex" && e.Source != "opencode" && e.Source != "wakatime" {
		return fmt.Errorf("unsupported usage source")
	}
	if len(e.SourceKey) == 0 || len(e.SourceKey) > 255 || len(e.SessionKey) > 255 ||
		(e.Source == "wakatime" && e.SessionKey == "") || len(e.Machine) == 0 || len(e.Machine) > 255 ||
		len(e.Project) > 1024 || len(e.Model) > 255 || !usageHash.MatchString(e.RawSHA256) || e.ParserVersion < 1 ||
		e.OccurredMs < 946684800000 || e.OccurredMs > time.Now().Add(24*time.Hour).UnixMilli() ||
		e.InputTokens < 0 || e.CacheReadTokens < 0 || e.CacheWriteTokens < 0 || e.UnknownInputTokens < 0 ||
		e.OutputTokens < 0 || e.ReasoningTokens < 0 ||
		e.InputTokens > 1_000_000_000_000 || e.CacheReadTokens > 1_000_000_000_000 ||
		e.CacheWriteTokens > 1_000_000_000_000 || e.UnknownInputTokens > 1_000_000_000_000 ||
		e.OutputTokens > 1_000_000_000_000 || e.ReasoningTokens > 1_000_000_000_000 ||
		len(e.RawUsageJSON) > 4096 ||
		!json.Valid([]byte(e.RawUsageJSON)) {
		return fmt.Errorf("invalid token usage event")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(e.RawUsageJSON), &raw); err != nil || raw == nil || !numericUsage(raw) {
		return fmt.Errorf("invalid raw numeric usage")
	}
	return nil
}

func numericUsage(raw map[string]any) bool {
	for _, value := range raw {
		switch typed := value.(type) {
		case float64:
			if math.Trunc(typed) != typed || math.IsInf(typed, 0) || math.IsNaN(typed) {
				return false
			}
		case map[string]any:
			if !numericUsage(typed) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Ingest checks each source identity within one transaction. Replays are no-ops;
// revised records are quarantined instead of quietly replacing verified counts.
func (s *TokenUsageService) Ingest(userID string, batch []models.TokenUsageEvent) (UsageIngestResult, error) {
	var result UsageIngestResult
	if userID == "" || len(batch) == 0 || len(batch) > 200 {
		return result, fmt.Errorf("invalid usage batch")
	}
	for i := range batch {
		if err := validateUsage(batch[i]); err != nil {
			return result, err
		}
		batch[i].UserID = userID // never trust client-provided ownership
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, e := range batch {
			compatible, err := pinUsageMode(tx, userID, e)
			if err != nil {
				return err
			}
			if !compatible {
				conflict := models.TokenUsageConflict{UserID: userID, Source: e.Source, SourceKey: e.SourceKey,
					IncomingSHA256: e.RawSHA256, Reason: "accounting representation changed; audited migration required",
					ObservedMs: time.Now().UnixMilli()}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conflict).Error; err != nil {
					return err
				}
				result.Conflicts++
				continue
			}
			var prior models.TokenUsageEvent
			err = tx.Where("user_id = ? AND source = ? AND source_key = ?", userID, e.Source, e.SourceKey).First(&prior).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				if prior.RawSHA256 == e.RawSHA256 && sameUsagePayload(prior, e) {
					if err := recordUsageCoverage(tx, userID, e); err != nil {
						return err
					}
					result.Unchanged++
					continue
				}
				// Streaming providers can publish an incomplete message followed by a
				// more complete one. Replace only if every bucket increases monotonically
				// and source attribution is unchanged. Everything else needs review.
				if e.RawSHA256 != prior.RawSHA256 && e.Machine == prior.Machine && e.Project == prior.Project && e.Model == prior.Model && e.SessionKey == prior.SessionKey &&
					e.OccurredMs >= prior.OccurredMs && e.ParserVersion >= prior.ParserVersion &&
					e.InputTokens >= prior.InputTokens && e.CacheReadTokens >= prior.CacheReadTokens &&
					e.CacheWriteTokens >= prior.CacheWriteTokens && e.UnknownInputTokens >= prior.UnknownInputTokens &&
					e.OutputTokens >= prior.OutputTokens &&
					e.ReasoningTokens >= prior.ReasoningTokens {
					e.ObservedMs = time.Now().UnixMilli()
					if err := tx.Model(&prior).Updates(map[string]any{
						"occurred_ms": e.OccurredMs, "input_tokens": e.InputTokens,
						"cache_read_tokens": e.CacheReadTokens, "cache_write_tokens": e.CacheWriteTokens,
						"unknown_input_tokens": e.UnknownInputTokens,
						"output_tokens":        e.OutputTokens, "reasoning_tokens": e.ReasoningTokens,
						"parser_version": e.ParserVersion, "raw_sha256": e.RawSHA256,
						"raw_usage_json": e.RawUsageJSON, "observed_ms": e.ObservedMs,
					}).Error; err != nil {
						return err
					}
					if err := recordUsageCoverage(tx, userID, e); err != nil {
						return err
					}
					result.Updated++
					continue
				}
				conflict := models.TokenUsageConflict{UserID: userID, Source: e.Source, SourceKey: e.SourceKey,
					PreviousSHA256: prior.RawSHA256, IncomingSHA256: e.RawSHA256,
					Reason: "revised source record", ObservedMs: time.Now().UnixMilli()}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conflict).Error; err != nil {
					return err
				}
				result.Conflicts++
				continue
			}
			e.ObservedMs = time.Now().UnixMilli()
			if err := tx.Create(&e).Error; err != nil {
				return err
			}
			if err := recordUsageCoverage(tx, userID, e); err != nil {
				return err
			}
			result.Inserted++
		}
		return nil
	})
	return result, err
}

type UsageReceipt struct {
	Source     string `json:"source"`
	SourceKey  string `json:"source_key"`
	SessionKey string `json:"session_key"`
	RawSHA256  string `json:"raw_sha256"`
}

type UsageReceiptResult struct {
	Matched    int `json:"matched"`
	Missing    int `json:"missing"`
	Mismatched int `json:"mismatched"`
	Conflicts  int `json:"conflicts"`
}

// Verify private local checkpoints without exposing transcript content or
// returning another user's usage. This endpoint performs no writes.
func (s *TokenUsageService) Receipts(userID string, batch []UsageReceipt) (UsageReceiptResult, error) {
	var result UsageReceiptResult
	if userID == "" || len(batch) == 0 || len(batch) > 200 {
		return result, fmt.Errorf("invalid receipt batch")
	}
	for _, item := range batch {
		if len(item.Source) == 0 || len(item.Source) > 32 || len(item.SourceKey) == 0 || len(item.SourceKey) > 255 || len(item.SessionKey) > 255 || !usageHash.MatchString(item.RawSHA256) {
			return result, fmt.Errorf("invalid receipt")
		}
		var row models.TokenUsageEvent
		err := s.db.Select("raw_sha256", "session_key").Where("user_id=? AND source=? AND source_key=?", userID, item.Source, item.SourceKey).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result.Missing++
			continue
		}
		if err != nil {
			return result, err
		}
		matches := row.RawSHA256 == item.RawSHA256
		if matches && item.SessionKey != "" {
			if item.Source == "wakatime" {
				matches = row.SessionKey == item.SessionKey
			} else {
				var refs int64
				if err := s.db.Model(&models.TokenUsageSessionRef{}).Where("user_id=? AND source=? AND source_key=? AND session_key=?",
					userID, item.Source, item.SourceKey, item.SessionKey).Count(&refs).Error; err != nil {
					return result, err
				}
				matches = refs > 0
			}
		}
		if matches {
			result.Matched++
		} else {
			result.Mismatched++
		}
		var conflicts int64
		if err := s.db.Model(&models.TokenUsageConflict{}).Where("user_id=? AND source=? AND source_key=?", userID, item.Source, item.SourceKey).Count(&conflicts).Error; err != nil {
			return result, err
		}
		if conflicts > 0 {
			result.Conflicts++
		}
	}
	return result, nil
}

func usageMode(key string) string {
	if strings.Contains(key, ":response:") {
		return "response"
	}
	if strings.Contains(key, ":fork:") {
		return "fork"
	}
	return "cumulative"
}

func pinUsageMode(tx *gorm.DB, userID string, e models.TokenUsageEvent) (bool, error) {
	if e.Source != "codex" || e.SessionKey == "" {
		return true, nil
	}
	mode := usageMode(e.SourceKey)
	var prior models.TokenUsageAccountingMode
	err := tx.Where("user_id=? AND source=? AND session_key=?", userID, e.Source, e.SessionKey).First(&prior).Error
	if err == nil {
		return prior.Mode == mode, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	// Also guard additive upgrades of a pre-mode ledger: never assume it is empty.
	var keys []string
	if err := tx.Model(&models.TokenUsageEvent{}).Where("user_id=? AND source=? AND session_key=?",
		userID, e.Source, e.SessionKey).Pluck("source_key", &keys).Error; err != nil {
		return false, err
	}
	for _, key := range keys {
		if usageMode(key) != mode {
			return false, nil
		}
	}
	pin := models.TokenUsageAccountingMode{UserID: userID, Source: e.Source, SessionKey: e.SessionKey, Mode: mode}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&pin).Error; err != nil {
		return false, err
	}
	err = tx.Where("user_id=? AND source=? AND session_key=?", userID, e.Source, e.SessionKey).First(&prior).Error
	return prior.Mode == mode, err
}

func sameUsagePayload(a, b models.TokenUsageEvent) bool {
	return a.OccurredMs == b.OccurredMs && (a.Project == b.Project || a.Source == "claude") && a.Model == b.Model &&
		(a.Source != "wakatime" || a.SessionKey == b.SessionKey) &&
		a.InputTokens == b.InputTokens && a.CacheReadTokens == b.CacheReadTokens &&
		a.CacheWriteTokens == b.CacheWriteTokens && a.UnknownInputTokens == b.UnknownInputTokens &&
		a.OutputTokens == b.OutputTokens && a.ReasoningTokens == b.ReasoningTokens &&
		a.RawUsageJSON == b.RawUsageJSON
}

func recordUsageCoverage(tx *gorm.DB, userID string, e models.TokenUsageEvent) error {
	if e.Source == "wakatime" {
		if e.InputTokens+e.CacheReadTokens+e.CacheWriteTokens+e.UnknownInputTokens+e.OutputTokens <= 1_000_000 {
			return nil
		}
		suspect := models.TokenUsageQuarantinedSession{UserID: userID, Source: e.Source,
			SessionKey: e.SessionKey, Reason: "individual fallback observation exceeds 1M tokens", Status: "open"}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&suspect).Error
	}
	if e.SessionKey == "" {
		return nil
	}
	ref := models.TokenUsageSessionRef{UserID: userID, Source: e.Source, SourceKey: e.SourceKey, SessionKey: e.SessionKey}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ref).Error
}

type UsageSummary struct {
	Events             int64 `json:"events"`
	InputTokens        int64 `json:"input_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens"`
	CacheWriteTokens   int64 `json:"cache_write_tokens"`
	UnknownInputTokens int64 `json:"unknown_input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	QuarantinedEvents  int64 `json:"quarantined_events"`
	QuarantinedTokens  int64 `json:"quarantined_tokens"`
}

func (s *TokenUsageService) Summary(userID string, from, to int64, machine, project string) (UsageSummary, error) {
	var result UsageSummary
	if userID == "" || from < 0 || to <= from {
		return result, fmt.Errorf("invalid usage range")
	}
	scope := func() *gorm.DB {
		query := s.db.Model(&models.TokenUsageEvent{}).
			Where("token_usage_events.user_id = ? AND occurred_ms >= ? AND occurred_ms < ?", userID, from, to)
		if machine != "" {
			query = query.Where("machine = ?", machine)
		}
		if project != "" {
			query = query.Where("project = ?", project)
		}
		return query
	}
	uncovered := usageUncovered
	quarantined := usageQuarantined
	query := scope().Where("(token_usage_events.source <> 'wakatime' OR " + uncovered + ")").
		Where("(token_usage_events.source <> 'wakatime' OR NOT " + quarantined + ")")
	err := query.Select(`COUNT(*) AS events,
		COALESCE(SUM(input_tokens),0) AS input_tokens,
		COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens,
		COALESCE(SUM(cache_write_tokens),0) AS cache_write_tokens,
		COALESCE(SUM(unknown_input_tokens),0) AS unknown_input_tokens,
		COALESCE(SUM(output_tokens),0) AS output_tokens,
		COALESCE(SUM(input_tokens + cache_read_tokens + cache_write_tokens + unknown_input_tokens + output_tokens),0) AS total_tokens`).Scan(&result).Error
	if err != nil {
		return result, err
	}
	var skipped struct {
		Events int64
		Tokens int64
	}
	err = scope().Where("token_usage_events.source = 'wakatime'").Where(uncovered).
		Where(quarantined).Select(`COUNT(*) AS events,
		COALESCE(SUM(input_tokens + cache_read_tokens + cache_write_tokens + unknown_input_tokens + output_tokens),0) AS tokens`).Scan(&skipped).Error
	result.QuarantinedEvents, result.QuarantinedTokens = skipped.Events, skipped.Tokens
	return result, err
}
