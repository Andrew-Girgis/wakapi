package services

import (
	"testing"
	"time"

	"github.com/muety/wakapi/config"
	"github.com/stretchr/testify/assert"
)

func TestDashboardService_Breakdown(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	b, err := sut.Breakdown(dashUser, from, to, DashboardFilters{}, dashStart.Add(3*time.Hour))
	assert.Nil(t, err)
	assert.Equal(t, 1, b.ActiveDays)
	assert.Equal(t, 45*60.0, b.DailyAverageSeconds)
	assert.Equal(t, 1, b.StreakDays) // the Codex-cli day 10 days earlier is not consecutive
	// 7 Oct 2026 is a Wednesday (row 2), all activity between 09:00 and 09:45 local time
	assert.Equal(t, 40*60.0, b.RhythmAgents[2][9])
	assert.Equal(t, 10*60.0, b.RhythmYou[2][9])
	assert.Equal(t, 0.0, b.RhythmAgents[2][10])
	assert.Equal(t, "ai coding", b.Categories[0].Name)
	assert.Equal(t, 40*60.0, b.Categories[0].Seconds) // union of two parallel agent sessions
	assert.Equal(t, []string{"Claude Code", "Codex VS Code", "Zed"}, []string{b.Editors[0].Name, b.Editors[1].Name, b.Editors[2].Name})
	assert.Empty(t, b.Languages)
}

func TestStreak(t *testing.T) {
	today := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	days := map[string]bool{"2026-10-25": true, "2026-10-24": true, "2026-10-22": true}
	assert.Equal(t, 2, streak(days, today)) // nothing yet today does not break it
	days["2026-10-26"] = true
	assert.Equal(t, 3, streak(days, today))
	assert.Equal(t, 0, streak(map[string]bool{"2026-10-23": true}, today))
}

func TestRhythm_SplitsAtHourBoundaries(t *testing.T) {
	tz := time.UTC
	start := time.Date(2026, 10, 26, 9, 45, 0, 0, tz) // Monday
	r := rhythm([]Interval{{start, start.Add(30 * time.Minute)}, {start, start.Add(10 * time.Minute)}}, tz)
	assert.Equal(t, 15*60.0, r[0][9])
	assert.Equal(t, 15*60.0, r[0][10])
}

func TestDashboardService_AgentFilter(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	o, err := sut.Overview(dashUser, from, to, DashboardFilters{Agent: "Codex"})
	assert.Nil(t, err)
	assert.Equal(t, 20*60.0, o.Time.TotalSeconds) // Codex 09:20-09:40 only
	assert.Equal(t, 0.0, o.Time.YouSeconds)
	assert.Equal(t, int64(11*50_000), o.Tokens.Input)
}

func TestDashboardService_Agents(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	a, err := sut.Agents(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Len(t, a.Agents, 2)
	assert.Equal(t, GroupClaudeCode, a.Agents[0].Group)
	assert.Equal(t, 30*60.0, a.Agents[0].Seconds)
	assert.Equal(t, 1, a.Agents[0].Sessions)
	assert.Equal(t, int64(16*1_110_000), a.Agents[0].Tokens)
	assert.Equal(t, 0.0, a.Agents[0].TokensPerLineChange)
	assert.Equal(t, GroupCodex, a.Agents[1].Group)
	assert.Equal(t, 20*60.0, a.Agents[1].Seconds)
	assert.Equal(t, "Opus 5-5", a.Models[0].Model)
}
