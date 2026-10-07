package services

import (
	"testing"
	"time"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
)

// fakes implement only what DashboardService uses; other interface methods panic if called
type fakeHeartbeatSrvc struct {
	IHeartbeatService
	heartbeats []*models.Heartbeat
}

func (f *fakeHeartbeatSrvc) GetAllWithin(from, to time.Time, _ *models.User) ([]*models.Heartbeat, error) {
	var out []*models.Heartbeat
	for _, h := range f.heartbeats {
		if t := h.Time.T(); !t.Before(from) && t.Before(to) {
			c := *h
			out = append(out, &c)
		}
	}
	return out, nil
}

func (f *fakeHeartbeatSrvc) GetFirstByUser(*models.User) (time.Time, error) {
	first := time.Time{}
	for _, h := range f.heartbeats {
		if first.IsZero() || h.Time.T().Before(first) {
			first = h.Time.T()
		}
	}
	return first, nil
}

type fakeAliasSrvc struct{ IAliasService }

func (f *fakeAliasSrvc) GetAliasOrDefault(_ string, t uint8, v string) (string, error) {
	if t == models.SummaryMachine {
		return config.Get().App.MachineName(v), nil
	}
	return v, nil
}

type fakeLabelSrvc struct{ IProjectLabelService }

func (f *fakeLabelSrvc) GetByUserGrouped(string) (map[string][]*models.ProjectLabel, error) {
	return map[string][]*models.ProjectLabel{"wakapi": {{ProjectKey: "wakapi", Label: "paused"}}}, nil
}

var (
	dashTZ    = time.FixedZone("EDT", -4*3600)
	dashDay   = time.Date(2026, 10, 7, 0, 0, 0, 0, dashTZ)
	dashStart = dashDay.Add(9 * time.Hour)
	dashUser  = &models.User{ID: "agirgis", Location: "America/Toronto", HeartbeatsTimeoutSec: 600}
)

func at(min int) models.CustomTime {
	return models.CustomTime(dashStart.Add(time.Duration(min) * time.Minute))
}

// Fixture (7 Oct 2026, EDT):
//
//	Claude session s1 on "Mac" (= Pythia), mnemosyne, 09:00-09:30, Opus 5-5, subscription "max"
//	Codex session s2 on omphalos, wakapi, 09:20-09:40, GPT 6-sol, pay per token
//	you in Zed on omphalos, wakapi, 09:35-09:45
//	Codex-cli on "Mac.localdomain": 25 heartbeats 10 days earlier, silent since
func dashboardFixture() []*models.Heartbeat {
	var hbs []*models.Heartbeat
	for m := 0; m <= 30; m += 2 {
		hbs = append(hbs, &models.Heartbeat{Time: at(m), Machine: "Mac", OperatingSystem: "Macos", Editor: "Claude", Category: "ai coding", Type: "app",
			Project: "mnemosyne", AISession: "s1", AIModel: "Opus", AIModelVersion: "5-5", AISubscriptionPlan: "max",
			AIInputTokens: 100_000, AICachedInputTokens: 1_000_000, AIOutputTokens: 10_000})
	}
	for m := 20; m <= 40; m += 2 {
		hbs = append(hbs, &models.Heartbeat{Time: at(m), Machine: "omphalos", OperatingSystem: "Linux", Editor: "Codex-vscode", Category: "ai coding", Type: "app",
			Project: "wakapi", AISession: "s2", AIModel: "Gpt", AIModelVersion: "6-sol",
			AIInputTokens: 50_000, AICachedInputTokens: 500_000, AIOutputTokens: 5_000})
	}
	for m := 35; m <= 45; m++ {
		hbs = append(hbs, &models.Heartbeat{Time: at(m), Machine: "omphalos", OperatingSystem: "Linux", Editor: "Zed", Category: "coding", Type: "file", Project: "wakapi"})
	}
	for k := 0; k < 25; k++ {
		hbs = append(hbs, &models.Heartbeat{Time: models.CustomTime(dashStart.AddDate(0, 0, -10).Add(time.Duration(k) * time.Minute)), Machine: "Mac.localdomain",
			OperatingSystem: "Macos", Editor: "Codex-cli", Category: "ai coding", Type: "app", Project: "kag", AISession: "old"})
	}
	return hbs
}

func newDashboardTestService() *DashboardService {
	cfg := config.Empty()
	cfg.App.MachineAliases = map[string]string{"Mac": "Pythia", "Mac.localdomain": "Pythia"}
	config.Set(cfg)
	pricing := NewPricingServiceWith([]config.AIPrice{
		{Family: "opus", Versions: []string{"5-5"}, Input: 4, CachedInput: 0.2, Output: 20},
		{Family: "gpt", Versions: []string{"6-sol"}, Input: 2, CachedInput: 0.2, Output: 10},
	})
	return NewDashboardService(&fakeHeartbeatSrvc{heartbeats: dashboardFixture()}, &fakeAliasSrvc{}, &fakeLabelSrvc{}, pricing)
}

func dayRange() (time.Time, time.Time) { return dashDay, dashDay.AddDate(0, 0, 1) }

func TestDashboardService_Overview(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	o, err := sut.Overview(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Equal(t, 45*60.0, o.Time.TotalSeconds)  // 09:00-09:45
	assert.Equal(t, 40*60.0, o.Time.AgentSeconds)  // 09:00-09:40
	assert.Equal(t, 10*60.0, o.Time.YouSeconds)    // 09:35-09:45
	assert.Equal(t, 5*60.0, o.Time.OverlapSeconds) // 09:35-09:40
	assert.Equal(t, 88.9, o.Time.AIPercent)        // 40 / 45
	assert.Nil(t, o.Time.ChangePercent)            // nothing the day before
	assert.Equal(t, int64(16*100_000+11*50_000), o.Tokens.Input)
	assert.Equal(t, int64(16*1_000_000+11*500_000), o.Tokens.CachedInput)
	assert.Equal(t, int64(16*10_000+11*5_000), o.Tokens.Output)
	assert.Equal(t, 15.55, o.Cost.EstimatedAPIValue) // opus 16 * 0.80 + gpt 11 * 0.25
	assert.Equal(t, 12.8, o.Cost.SubscriptionValue)
	assert.Equal(t, 2.75, o.Cost.PayPerTokenValue)
	assert.Len(t, o.CoverageNotes, 1) // 7 Oct 2026 is inside the cached-token gap (until 01:24)
}

func TestDashboardService_Filters(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	o, _ := sut.Overview(dashUser, from, to, DashboardFilters{Machine: "pythia"})
	assert.Equal(t, 30*60.0, o.Time.TotalSeconds)
	o, _ = sut.Overview(dashUser, from, to, DashboardFilters{Project: "wakapi"})
	assert.Equal(t, 25*60.0, o.Time.TotalSeconds) // 09:20-09:45
}

func TestDashboardService_ActivityAndSessions(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	a, err := sut.Activity(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Len(t, a.Days, 1)
	assert.Equal(t, 30*60.0, a.Days[0].Seconds[GroupClaudeCode])
	assert.Equal(t, 20*60.0, a.Days[0].Seconds[GroupCodex])
	assert.Equal(t, 10*60.0, a.Days[0].Seconds[GroupYou])
	assert.Equal(t, int64(16*1_110_000), a.Days[0].Tokens[GroupClaudeCode])
	assert.Equal(t, GroupClaudeCode, a.Share[0].Group)
	assert.Equal(t, 50.0, a.Share[0].Percent) // 30 of 60 group minutes

	assert.Equal(t, 2, a.Sessions.Sessions)
	assert.Equal(t, 2.0, a.Sessions.SessionsPerDay)
	assert.Equal(t, 25*60.0, a.Sessions.MedianSessionSeconds) // median of 30 and 20 minutes
}

func TestDashboardService_Timeline(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()

	tl, err := sut.Timeline(dashUser, dashDay, DashboardFilters{})
	assert.Nil(t, err)
	assert.Equal(t, "2026-10-07", tl.Date)
	assert.Equal(t, 2, tl.PeakConcurrency) // s1 and s2 overlap 09:20-09:30
	assert.Equal(t, 45*60.0, tl.TotalSeconds)
	assert.True(t, dashStart.Equal(tl.WindowStart))
	assert.True(t, dashStart.Add(time.Hour).Equal(tl.WindowEnd))
	assert.Len(t, tl.Rows, 2)
	assert.Equal(t, "mnemosyne", tl.Rows[0].Project)
	assert.Equal(t, "wakapi", tl.Rows[1].Project)
	assert.Len(t, tl.Rows[1].Segments, 2) // Codex 09:20-09:40, You 09:35-09:45
}

func TestDashboardService_Machines(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()

	m, err := sut.Machines(dashUser, dashStart.Add(46*time.Minute))
	assert.Nil(t, err)
	assert.Len(t, m.Machines, 2) // Mac and Mac.localdomain are one machine
	assert.Equal(t, 1, m.LiveCount)
	assert.Equal(t, "omphalos", m.Machines[0].Name)
	assert.True(t, m.Machines[0].Live)
	assert.Equal(t, "Zed", m.Machines[0].ActiveAgent) // editors keep their name
	assert.Equal(t, "Pythia", m.Machines[1].Name)
	assert.False(t, m.Machines[1].Live)
	assert.Len(t, m.Warnings, 1)
	assert.Equal(t, "Codex CLI on Pythia: silent for 10 days", m.Warnings[0].Message)
}

func TestDashboardService_Projects(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	p, err := sut.Projects(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Equal(t, "labels", p.StatusSource)
	assert.Len(t, p.Projects, 2)
	assert.Equal(t, "mnemosyne", p.Projects[0].Name)
	assert.Equal(t, 30*60.0, p.Projects[0].Seconds)
	assert.Equal(t, 12.8, p.Projects[0].Cost)
	assert.Equal(t, "wakapi", p.Projects[1].Name)
	assert.Equal(t, "paused", p.Projects[1].Status)
	assert.Equal(t, []float64{25 * 60}, p.Projects[1].DailySeconds)
}

func TestDashboardService_TokensAndCost(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	from, to := dayRange()

	tk, err := sut.Tokens(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Equal(t, int64(16*1_000_000), tk.ByModel["Opus 5-5"].CachedInput)
	assert.Equal(t, tk.Totals, tk.ByDay[0].Tokens)

	c, err := sut.Cost(dashUser, from, to, DashboardFilters{})
	assert.Nil(t, err)
	assert.Len(t, c.Cost.ByModel, 2)
	assert.Equal(t, "Opus", c.Cost.ByModel[0].Model)
	assert.True(t, c.Cost.ByModel[0].Subscription)
	assert.Equal(t, int64(0), c.Cost.UnpricedTokens)
}

func TestAgentGroup(t *testing.T) {
	assert.Equal(t, GroupClaudeCode, AgentGroup("Claude code"))
	assert.Equal(t, GroupCodex, AgentGroup("Codex-vscode"))
	assert.Equal(t, GroupCodex, AgentGroup("Gpt"))
	assert.Equal(t, GroupPi, AgentGroup("Pi-coding-agent"))
	assert.Equal(t, GroupOpenCode, AgentGroup("Opencode cli"))
	assert.Equal(t, GroupOther, AgentGroup("Zed"))
}

func TestCoverageNotes(t *testing.T) {
	assert.Empty(t, coverageNotes(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)))
	assert.Len(t, coverageNotes(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)), 1)
	assert.Len(t, coverageNotes(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)), 2)
}

func TestLabelStatus(t *testing.T) {
	assert.Equal(t, "paused", labelStatus("Paused"))
	assert.Equal(t, "done", labelStatus("status:done"))
	assert.Equal(t, "", labelStatus("client-work"))
}

func TestAgentTool(t *testing.T) {
	assert.Equal(t, "Claude Code", AgentTool("Claude"))
	assert.Equal(t, "Claude Code", AgentTool("Claude code"))
	assert.Equal(t, "Codex CLI", AgentTool("Codex cli"))
	assert.Equal(t, "Codex CLI", AgentTool("Codex-cli"))
	assert.Equal(t, "Codex VS Code", AgentTool("Codex-vscode"))
	assert.Equal(t, "Pi", AgentTool("Pi-coding-agent"))
	assert.Equal(t, "", AgentTool("Mnemosyne-m1-check"))
	assert.Equal(t, "", AgentTool("Zed"))
}

func TestDashboardService_Machines_IgnoresNonAgentTools(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	fake := sut.heartbeatSrvc.(*fakeHeartbeatSrvc)
	for k := 0; k < 25; k++ { // a test client that stopped on purpose
		fake.heartbeats = append(fake.heartbeats, &models.Heartbeat{Time: models.CustomTime(dashStart.AddDate(0, 0, -8).Add(time.Duration(k) * time.Minute)),
			Machine: "omphalos", Editor: "Mnemosyne-m1-check", Category: "ai coding", Type: "app"})
	}
	m, _ := sut.Machines(dashUser, dashStart.Add(46*time.Minute))
	assert.Len(t, m.Warnings, 1)
}

func TestDashboardService_ClampFrom(t *testing.T) {
	defer config.Set(config.Empty())
	sut := newDashboardTestService()
	epoch := time.Unix(0, 0)
	got := sut.ClampFrom(dashUser, epoch)
	assert.True(t, got.Equal(dashDay.AddDate(0, 0, -10)), got)      // first heartbeat is the old Codex-cli history, 10 days earlier
	assert.True(t, sut.ClampFrom(dashUser, dashDay).Equal(dashDay)) // ranges after the first heartbeat are unchanged
}
