package services

import (
	"github.com/glebarez/sqlite"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func dashboardLedgerFixture(t *testing.T) *DashboardService {
	t.Helper()
	s := newDashboardTestService()
	s.config.App.DashboardTokenSource = "ledger"
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/ledger.db"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TokenUsageEvent{}, &models.TokenUsageSessionRef{}, &models.TokenUsageQuarantinedSession{}, &models.TokenUsageAccountingMode{}, &models.TokenUsageConflict{}))
	s.ledger = NewTokenUsageService(db)
	e := models.TokenUsageEvent{Source: "pi", SourceKey: "message", SessionKey: "s1", Machine: "Mac", Project: "wakapi", Model: "claude-opus-4-6", OccurredMs: dashStart.UnixMilli(), InputTokens: 10, CacheReadTokens: 100, CacheWriteTokens: 20, UnknownInputTokens: 30, OutputTokens: 2, ReasoningTokens: 1, ParserVersion: 9, RawSHA256: strings.Repeat("a", 64), RawUsageJSON: `{}`}
	_, err = s.ledger.Ingest(dashUser.ID, []models.TokenUsageEvent{e})
	require.NoError(t, err)
	copied := e
	copied.Machine = "omphalos"
	copied.SessionKey = "copy"
	ack, err := s.ledger.Ingest(dashUser.ID, []models.TokenUsageEvent{copied})
	require.NoError(t, err)
	require.Equal(t, 1, ack.Unchanged)
	fallback := models.TokenUsageEvent{Source: "wakatime", SourceKey: "fallback", SessionKey: "copy", Machine: "Pythia", Project: "other", Model: "Opus 4-6", OccurredMs: dashStart.UnixMilli(), UnknownInputTokens: 200, OutputTokens: 20, ParserVersion: 9, RawSHA256: strings.Repeat("b", 64), RawUsageJSON: `{}`}
	separate := fallback
	separate.SourceKey = "independent"
	separate.SessionKey = "uncovered"
	separate.Project = ""
	_, err = s.ledger.Ingest(dashUser.ID, []models.TokenUsageEvent{fallback, separate})
	require.NoError(t, err)
	_, err = s.ledger.Ingest("another-user", []models.TokenUsageEvent{e})
	require.NoError(t, err)
	return s
}

func TestDashboardLedger_ConsistentPanelsAndFilters(t *testing.T) {
	defer config.Set(config.Empty())
	s := dashboardLedgerFixture(t)
	from, to := dayRange()
	overview, err := s.Overview(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Equal(t, int64(382), overview.Tokens.Total)
	require.Equal(t, int64(230), overview.Tokens.UnknownInput)
	require.Equal(t, int64(20), overview.Tokens.CacheWrite)
	require.True(t, overview.Provisional)
	require.Equal(t, "ledger", overview.TokenSource)
	require.NotNil(t, overview.LegacyTokens)
	require.Equal(t, 45*60.0, overview.Time.TotalSeconds)
	require.False(t, overview.Cost.Available)
	require.Equal(t, int64(382), overview.Cost.UnpricedTokens)
	tokens, err := s.Tokens(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Equal(t, overview.Tokens, tokens.Totals)
	require.Equal(t, int64(382), tokens.ByDay[0].Tokens.Total)
	activity, err := s.Activity(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	var sum int64
	for _, d := range activity.Days {
		for _, value := range d.Tokens {
			sum += value
		}
	}
	require.Equal(t, int64(382), sum)
	require.Equal(t, int64(220), activity.Days[0].Tokens["WakaTime fallback"])
	projects, err := s.Projects(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	sum = 0
	for _, p := range projects.Projects {
		sum += p.Tokens
		require.False(t, p.CostAvailable)
	}
	require.Equal(t, int64(382), sum)
	cost, err := s.Cost(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	sum = 0
	for _, m := range cost.Cost.ByModel {
		sum += m.Tokens.Total
	}
	require.Equal(t, int64(382), sum)
	filtered, err := s.Tokens(dashUser, from, to, DashboardFilters{Machine: "pythia", Project: "wakapi", Model: "Opus 4.6"})
	require.NoError(t, err)
	require.Equal(t, int64(162), filtered.Totals.Total)
	// Copied coverage excludes fallback even when the transcript has another project/model.
	filtered, err = s.Tokens(dashUser, from, to, DashboardFilters{Project: "other", Model: "Opus 4-6"})
	require.NoError(t, err)
	require.Zero(t, filtered.Totals.Total)
	evidence, err := s.LedgerSummary(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Equal(t, int64(382), evidence.TotalTokens)
	options, err := s.Options(dashUser)
	require.NoError(t, err)
	require.Contains(t, options.Machines, "Pythia")
	require.Contains(t, options.Models, "claude-opus-4-6")
}

func TestDashboardLedger_RollbackAndUnavailableAreDistinct(t *testing.T) {
	defer config.Set(config.Empty())
	s := dashboardLedgerFixture(t)
	from, to := dayRange()
	before, err := s.Overview(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	s.config.App.DashboardTokenSource = "heartbeats"
	legacy, err := s.Overview(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Equal(t, *before.LegacyTokens, legacy.Tokens)
	require.False(t, legacy.Provisional)
	require.True(t, legacy.Cost.Available)
	s.config.App.DashboardTokenSource = "ledger"
	again, err := s.Overview(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Equal(t, before.Tokens, again.Tokens)
	s.ledger = nil
	_, err = s.Tokens(dashUser, from, to, DashboardFilters{})
	require.Error(t, err)
	_, err = s.Overview(dashUser, from, to, DashboardFilters{})
	require.Error(t, err)
}

func TestDashboardLedger_AllTimeIncludesEarlierUsageAndDSTBins(t *testing.T) {
	defer config.Set(config.Empty())
	s := dashboardLedgerFixture(t)
	e := models.TokenUsageEvent{Source: "claude", SourceKey: "early", SessionKey: "early", Machine: "Pythia", OccurredMs: time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC).UnixMilli(), InputTokens: 1, ParserVersion: 9, RawSHA256: strings.Repeat("c", 64), RawUsageJSON: `{}`}
	_, err := s.ledger.Ingest(dashUser.ID, []models.TokenUsageEvent{e})
	require.NoError(t, err)
	first := s.ClampFrom(dashUser, time.Unix(0, 0))
	require.Equal(t, "2024-01-02", first.In(dashUser.TZ()).Format("2006-01-02"))
	from := time.Date(2026, 3, 8, 0, 0, 0, 0, dashUser.TZ())
	to := from.AddDate(0, 0, 1)
	require.Equal(t, 23*time.Hour, to.Sub(from))
	e.SourceKey = "dst"
	e.OccurredMs = from.Add(22 * time.Hour).UnixMilli()
	e.RawSHA256 = strings.Repeat("d", 64)
	_, err = s.ledger.Ingest(dashUser.ID, []models.TokenUsageEvent{e})
	require.NoError(t, err)
	result, err := s.Tokens(dashUser, from, to, DashboardFilters{})
	require.NoError(t, err)
	require.Len(t, result.ByDay, 1)
	require.Equal(t, int64(1), result.ByDay[0].Tokens.Total)
}
