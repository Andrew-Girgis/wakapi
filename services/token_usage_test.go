package services

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenLedgerPythonRehearsalSchemaCompatibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/rehearsal.db"), &gorm.Config{})
	require.NoError(t, err)
	schema, err := os.ReadFile("../tools/token_ledger/schema.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(schema)).Error)
	require.NoError(t, db.AutoMigrate(&models.TokenUsageEvent{}, &models.TokenUsageSessionRef{}, &models.TokenUsageQuarantinedSession{}, &models.TokenUsageAccountingMode{}, &models.TokenUsageConflict{}))
	stmt := NewTokenUsageService(db)
	out, err := stmt.Ingest("u1", []models.TokenUsageEvent{{Source: "pi", SourceKey: "evt", Machine: "Pythia",
		OccurredMs: time.Now().UnixMilli(), ParserVersion: 3, RawSHA256: strings.Repeat("a", 64), RawUsageJSON: `{}`, CacheReadTokens: 100}})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
}

func TestTokenUsageLedgerReplayAndUserIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/ledger.db"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TokenUsageEvent{}, &models.TokenUsageSessionRef{}, &models.TokenUsageQuarantinedSession{}, &models.TokenUsageAccountingMode{}, &models.TokenUsageConflict{}))
	srv := NewTokenUsageService(db)
	e := models.TokenUsageEvent{Source: "codex", SourceKey: "session:1", SessionKey: "s1", Machine: "Pythia", Project: "wakapi", Model: "gpt",
		OccurredMs: time.Now().UnixMilli(), InputTokens: 10, CacheReadTokens: 100, OutputTokens: 2,
		ParserVersion: 3, RawSHA256: strings.Repeat("a", 64), RawUsageJSON: `{}`}
	out, err := srv.Ingest("u1", []models.TokenUsageEvent{e})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{e})
	require.NoError(t, err)
	require.Equal(t, 1, out.Unchanged)
	changed := e
	changed.CacheReadTokens = 120
	changed.OccurredMs++
	changed.RawSHA256 = strings.Repeat("b", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{changed})
	require.NoError(t, err)
	require.Equal(t, 1, out.Updated)
	regression := changed
	regression.CacheReadTokens = 110
	regression.RawSHA256 = strings.Repeat("c", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{regression})
	require.NoError(t, err)
	require.Equal(t, 1, out.Conflicts)
	var conflicts int64
	require.NoError(t, db.Model(&models.TokenUsageConflict{}).Where("user_id=?", "u1").Count(&conflicts).Error)
	require.Equal(t, int64(1), conflicts)
	total, err := srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "Pythia", "wakapi")
	require.NoError(t, err)
	require.Equal(t, int64(132), total.TotalTokens)
	require.Equal(t, int64(120), total.CacheReadTokens)
	// A lower-priority WakaTime observation for the same session is excluded,
	// but one from a session with no transcript is counted, with unknown cache split.
	fallback := models.TokenUsageEvent{Source: "wakatime", SourceKey: "hb1", SessionKey: "s1", Machine: "Pythia",
		Project: "wakapi", OccurredMs: e.OccurredMs, UnknownInputTokens: 100, OutputTokens: 10,
		ParserVersion: 5, RawSHA256: strings.Repeat("e", 64), RawUsageJSON: `{"ai_input_tokens":100}`}
	otherSession := fallback
	otherSession.SourceKey, otherSession.SessionKey = "hb2", "s2"
	otherSession.UnknownInputTokens, otherSession.OutputTokens = 200, 20
	otherSession.RawSHA256 = strings.Repeat("f", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{fallback, otherSession})
	require.NoError(t, err)
	require.Equal(t, 2, out.Inserted)
	withFallback, err := srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "Pythia", "wakapi")
	require.NoError(t, err)
	require.Equal(t, int64(2), withFallback.Events)
	require.Equal(t, int64(352), withFallback.TotalTokens)
	require.Equal(t, int64(200), withFallback.UnknownInputTokens)
	late := models.TokenUsageEvent{Source: "pi", SourceKey: "late-msg", SessionKey: "s2", Machine: "omphalos",
		Project: "wakapi", OccurredMs: e.OccurredMs, InputTokens: 1, CacheReadTokens: 50, OutputTokens: 5,
		ParserVersion: 5, RawSHA256: strings.Repeat("9", 64), RawUsageJSON: `{}`}
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{late})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
	withFallback, err = srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(2), withFallback.Events) // late transcript retires WakaTime fallback
	require.Equal(t, int64(188), withFallback.TotalTokens)
	copied := late
	copied.SessionKey = "fork-of-s2"
	copied.Machine = "Pythia"
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{copied})
	require.NoError(t, err)
	require.Equal(t, 1, out.Unchanged)
	var refs int64
	require.NoError(t, db.Model(&models.TokenUsageSessionRef{}).Where("user_id=? AND source_key=?", "u1", "late-msg").Count(&refs).Error)
	require.Equal(t, int64(2), refs)
	forged := copied
	forged.SessionKey = "unverified-alias"
	forged.InputTokens++ // same advertised hash must not suppress an unrelated session
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{forged})
	require.NoError(t, err)
	require.Equal(t, 1, out.Conflicts)
	require.NoError(t, db.Model(&models.TokenUsageSessionRef{}).Where("user_id=? AND source_key=?", "u1", "late-msg").Count(&refs).Error)
	require.Equal(t, int64(2), refs)
	forkFallback := fallback
	forkFallback.SourceKey, forkFallback.SessionKey = "hb3", "fork-of-s2"
	forkFallback.RawSHA256 = strings.Repeat("8", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{forkFallback})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
	withFallback, err = srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(188), withFallback.TotalTokens) // copied session suppressed too
	suspect := fallback
	suspect.SourceKey, suspect.SessionKey = "hb-large", "unverified"
	suspect.UnknownInputTokens = 1_000_001
	suspect.RawSHA256 = strings.Repeat("7", 64)
	small := fallback
	small.SourceKey, small.SessionKey = "hb-small", "unverified"
	small.RawSHA256 = strings.Repeat("6", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{suspect, small})
	require.NoError(t, err)
	require.Equal(t, 2, out.Inserted)
	withFallback, err = srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(188), withFallback.TotalTokens)
	require.Equal(t, int64(2), withFallback.QuarantinedEvents)
	require.Equal(t, int64(1_000_121), withFallback.QuarantinedTokens)
	claudeCopy := e
	claudeCopy.Source, claudeCopy.SourceKey, claudeCopy.SessionKey = "claude", "copied-message", "original"
	claudeCopy.Project = "original-project"
	claudeCopy.RawSHA256 = strings.Repeat("5", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{claudeCopy})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
	claudeAlias := claudeCopy
	claudeAlias.SessionKey, claudeAlias.Project, claudeAlias.Machine = "fork", "new-project", "omphalos"
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{claudeAlias})
	require.NoError(t, err)
	require.Equal(t, 1, out.Unchanged)
	require.NoError(t, db.Model(&models.TokenUsageSessionRef{}).Where("user_id=? AND source_key=?", "u1", "copied-message").Count(&refs).Error)
	require.Equal(t, int64(2), refs)
	// Gross reasoning can exceed net output after a provider correction.
	gross := e
	gross.SourceKey = "gross"
	gross.RawSHA256 = strings.Repeat("d", 64)
	gross.RawUsageJSON = `{"output":-92,"reasoning":2006}`
	gross.OutputTokens = 1914
	gross.ReasoningTokens = 2006
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{gross})
	require.NoError(t, err)
	require.Equal(t, 1, out.Inserted)
	filtered, err := srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "omphalos", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), filtered.Events)
	other, err := srv.Summary("u2", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	require.Zero(t, other.TotalTokens)
	// Prefix delivery followed by discovery of per-response data must not count
	// both representations of the same session, even with different source keys.
	beforeModeChange, err := srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	changedMode := e
	changedMode.SourceKey = "s1:response:new-format"
	changedMode.RawSHA256 = strings.Repeat("4", 64)
	out, err = srv.Ingest("u1", []models.TokenUsageEvent{changedMode})
	require.NoError(t, err)
	require.Equal(t, 1, out.Conflicts)
	require.Zero(t, out.Inserted)
	afterModeChange, err := srv.Summary("u1", e.OccurredMs-1, e.OccurredMs+2, "", "")
	require.NoError(t, err)
	require.Equal(t, beforeModeChange, afterModeChange)
	_, err = srv.Ingest("u1", []models.TokenUsageEvent{{Source: "pi", SourceKey: "bad", Machine: "Pythia", OccurredMs: e.OccurredMs,
		InputTokens: -1, ParserVersion: 3, RawSHA256: strings.Repeat("a", 64), RawUsageJSON: `{}`}})
	require.Error(t, err)
}
