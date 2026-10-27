package services

import (
	"sort"
	"strings"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/models/view"
)

const dashboardStreakDays = 365

// Breakdown returns the Overview's secondary numbers: daily average, streak, rhythm heatmap and compact bars.
func (srv *DashboardService) Breakdown(user *models.User, from, to time.Time, filters DashboardFilters, now time.Time) (*view.DashboardBreakdown, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	tz := user.TZ()
	out := &view.DashboardBreakdown{Range: view.DashboardRange{From: from, To: to}}

	all := intervalsOf(data.spans, nil)
	active := activeDays(all, tz)
	out.ActiveDays = len(active)
	if len(active) > 0 {
		out.DailyAverageSeconds = TotalDuration(all).Seconds() / float64(len(active))
	}

	// the streak looks back before the selected range, up to a year
	local := now.In(tz)
	tomorrow := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, tz)
	history, err := srv.load(user, tomorrow.AddDate(0, 0, -dashboardStreakDays), tomorrow, filters)
	if err != nil {
		return nil, err
	}
	out.StreakDays = streak(activeDays(intervalsOf(history.spans, nil), tz), local)

	out.RhythmAgents = rhythm(intervalsOf(data.spans, func(s dashSpan) bool { return s.Agent }), tz)
	out.RhythmYou = rhythm(intervalsOf(data.spans, func(s dashSpan) bool { return !s.Agent }), tz)

	out.Languages = bars(data.spans, func(s dashSpan) string { return s.Hb.Language })
	out.Categories = bars(data.spans, func(s dashSpan) string { return strings.ToLower(s.Hb.Category) })
	out.Editors = bars(data.spans, func(s dashSpan) string {
		if tool := AgentTool(s.Hb.Editor); tool != "" {
			return tool
		}
		return s.Hb.Editor
	})
	return out, nil
}

// activeDays returns the local dates (YYYY-MM-DD) with any activity.
func activeDays(intervals []Interval, tz *time.Location) map[string]bool {
	days := map[string]bool{}
	for _, iv := range UnionIntervals(intervals) {
		for d := iv.Start.In(tz); d.Before(iv.End); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, tz) {
			days[d.Format("2006-01-02")] = true
		}
	}
	return days
}

// streak counts consecutive active days ending today; a day without activity yet today does not break it.
func streak(days map[string]bool, today time.Time) int {
	d := today
	if !days[d.Format("2006-01-02")] {
		d = d.AddDate(0, 0, -1)
	}
	n := 0
	for days[d.Format("2006-01-02")] && n < dashboardStreakDays {
		n++
		d = d.AddDate(0, 0, -1)
	}
	return n
}

// rhythm spreads the union of intervals over [weekday][hour] buckets in the user's time zone.
func rhythm(intervals []Interval, tz *time.Location) [][]float64 {
	out := make([][]float64, 7)
	for i := range out {
		out[i] = make([]float64, 24)
	}
	for _, iv := range UnionIntervals(intervals) {
		for t := iv.Start.In(tz); t.Before(iv.End); {
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, tz)
			end := minTime(next, iv.End)
			out[(int(t.Weekday())+6)%7][t.Hour()] += end.Sub(t).Seconds()
			t = end.In(tz)
		}
	}
	return out
}

// bars sums time per key as a union per key, so parallel streams with the same key count once. Empty keys are skipped.
func bars(spans []dashSpan, keyOf func(dashSpan) string) []view.DashboardBar {
	byKey := map[string][]Interval{}
	for _, s := range spans {
		if k := keyOf(s); k != "" {
			byKey[k] = append(byKey[k], s.Interval)
		}
	}
	out := []view.DashboardBar{}
	for k, ivs := range byKey {
		out = append(out, view.DashboardBar{Name: k, Seconds: TotalDuration(ivs).Seconds()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seconds != out[j].Seconds {
			return out[i].Seconds > out[j].Seconds
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Agents returns per agent group time, tokens, sessions and line changes, plus time by model version and reasoning effort.
// Tokens follow the selected token source (ledger or heartbeats), like every other token panel.
func (srv *DashboardService) Agents(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardAgents, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	out := &view.DashboardAgents{DashboardAccounting: srv.accounting(), Range: view.DashboardRange{From: from, To: to}, Agents: []view.DashboardAgentStats{}, Models: []view.DashboardModelEffort{}}

	stats := map[string]*view.DashboardAgentStats{}
	get := func(group string) *view.DashboardAgentStats {
		if stats[group] == nil {
			stats[group] = &view.DashboardAgentStats{Group: group}
		}
		return stats[group]
	}
	ivs := map[string][]Interval{}
	type modelKey struct{ group, model, effort string }
	modelIvs := map[modelKey][]Interval{}
	for _, s := range data.spans {
		if !s.Agent {
			continue
		}
		ivs[s.Group] = append(ivs[s.Group], s.Interval)
		k := modelKey{s.Group, strings.TrimSpace(s.Hb.AIModel + " " + s.Hb.AIModelVersion), strings.ToLower(s.Hb.AIModelComplexity)}
		modelIvs[k] = append(modelIvs[k], s.Interval)
	}
	for g, list := range ivs {
		get(g).Seconds = TotalDuration(list).Seconds()
	}
	byGroup := map[string][]*models.Heartbeat{}
	for _, h := range data.heartbeats {
		if !isAgentHeartbeat(h) {
			continue
		}
		g := AgentGroup(h.Editor)
		byGroup[g] = append(byGroup[g], h)
		get(g).LineChanges += int64(h.AILineChanges)
		if !srv.usingLedger() && heartbeatMatchesModel(h, filters.Model) {
			get(g).Tokens += int64(h.AIInputTokens + h.AICachedInputTokens + h.AIOutputTokens)
		}
	}
	for g, hbs := range byGroup {
		s := sessionStats(hbs, user.HeartbeatsTimeout(), user.TZ())
		get(g).Sessions, get(g).MedianSessionSeconds = s.Sessions, s.MedianSessionSeconds
	}
	if srv.usingLedger() {
		rows, err := srv.loadLedger(user, from, to, filters)
		if err != nil {
			return nil, err
		}
		for _, e := range rows {
			get(ledgerGroup(e.Source)).Tokens += ledgerEventTotal(e)
		}
	}
	for _, s := range stats {
		if s.LineChanges > 0 {
			s.TokensPerLineChange = round1(float64(s.Tokens) / float64(s.LineChanges))
		}
		out.Agents = append(out.Agents, *s)
	}
	sort.Slice(out.Agents, func(i, j int) bool {
		if out.Agents[i].Seconds != out.Agents[j].Seconds {
			return out.Agents[i].Seconds > out.Agents[j].Seconds
		}
		return out.Agents[i].Tokens > out.Agents[j].Tokens
	})
	for k, list := range modelIvs {
		out.Models = append(out.Models, view.DashboardModelEffort{Group: k.group, Model: k.model, Effort: k.effort, Seconds: TotalDuration(list).Seconds()})
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].Seconds != out.Models[j].Seconds {
			return out.Models[i].Seconds > out.Models[j].Seconds
		}
		return out.Models[i].Model+out.Models[i].Effort < out.Models[j].Model+out.Models[j].Effort
	})
	return out, nil
}
