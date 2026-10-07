package services

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/models/view"
	"github.com/patrickmn/go-cache"
)

// Agent groups shown on the dashboard, in display order. "You" is time in non-AI categories.
const (
	GroupClaudeCode = "Claude Code"
	GroupCodex      = "Codex"
	GroupPi         = "Pi"
	GroupOpenCode   = "OpenCode"
	GroupOther      = "Other"
	GroupYou        = "You"
)

var DashboardGroups = []string{GroupClaudeCode, GroupCodex, GroupPi, GroupOpenCode, GroupOther, GroupYou}

// Cached input tokens are missing in this window: wakatime-cli moved them to ai_cached_input_tokens
// (wakatime/wakatime-cli@430a1d8f, 6 Aug 2026) and this fork stores that field since its deploy (7 Oct 2026 01:24 EDT).
var (
	cachedTokensGapFrom = time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	cachedTokensGapTo   = time.Date(2026, 10, 7, 5, 24, 0, 0, time.UTC)
)

const (
	dashboardLiveWindow    = 5 * time.Minute
	dashboardSilentAfter   = 3 * 24 * time.Hour
	dashboardSilentMinHb   = 20
	dashboardSparklineDays = 14
)

type DashboardFilters struct {
	Project string
	Machine string
}

type dashSpan struct {
	Interval
	Project string
	Group   string
	Machine string
	Agent   bool
	Stream  string
}

type dashData struct {
	heartbeats []*models.Heartbeat
	spans      []dashSpan
}

type DashboardService struct {
	config        *config.Config
	heartbeatSrvc IHeartbeatService
	aliasSrvc     IAliasService
	labelSrvc     IProjectLabelService
	pricing       *PricingService
	cache         *cache.Cache
	httpClient    *http.Client
	mnemosyneMu   sync.Mutex
}

func NewDashboardService(heartbeatService IHeartbeatService, aliasService IAliasService, labelService IProjectLabelService, pricing *PricingService) *DashboardService {
	return &DashboardService{
		config:        config.Get(),
		heartbeatSrvc: heartbeatService,
		aliasSrvc:     aliasService,
		labelSrvc:     labelService,
		pricing:       pricing,
		cache:         cache.New(30*time.Second, time.Minute),
		httpClient:    &http.Client{Timeout: 3 * time.Second},
	}
}

// AgentGroup maps an editor name to its dashboard group.
func AgentGroup(editor string) string {
	e := strings.ToLower(editor)
	switch {
	case strings.HasPrefix(e, "claude"):
		return GroupClaudeCode
	case strings.HasPrefix(e, "codex"), e == "gpt":
		return GroupCodex
	case e == "pi", strings.HasPrefix(e, "pi-"):
		return GroupPi
	case strings.HasPrefix(e, "opencode"):
		return GroupOpenCode
	default:
		return GroupOther
	}
}

func isAgentHeartbeat(h *models.Heartbeat) bool {
	return strings.EqualFold(h.Category, models.HeartbeatCategoryAiCoding)
}

// load returns the user's heartbeats in [from, to) after filters, and their activity spans.
// Unlike Wakapi durations, agent "thinking time" heartbeats (type app, category ai coding) are counted.
func (srv *DashboardService) load(user *models.User, from, to time.Time, filters DashboardFilters) (*dashData, error) {
	key := fmt.Sprintf("%s|%d|%d|%s|%s", user.ID, from.Unix(), to.Unix(), filters.Project, filters.Machine)
	if cached, ok := srv.cache.Get(key); ok {
		return cached.(*dashData), nil
	}

	all, err := srv.heartbeatSrvc.GetAllWithin(from, to, user)
	if err != nil {
		return nil, err
	}

	data := &dashData{}
	for _, h := range all {
		h.Machine = srv.machineName(user, h.Machine)
		h.Project = srv.projectName(user, h.Project)
		if filters.Project != "" && !strings.EqualFold(h.Project, filters.Project) {
			continue
		}
		if filters.Machine != "" && !strings.EqualFold(h.Machine, filters.Machine) {
			continue
		}
		data.heartbeats = append(data.heartbeats, h)
	}
	sort.SliceStable(data.heartbeats, func(i, j int) bool { return data.heartbeats[i].Time.T().Before(data.heartbeats[j].Time.T()) })

	streams := map[string][]*models.Heartbeat{}
	var order []string
	for _, h := range data.heartbeats {
		var stream string
		switch {
		case isAgentHeartbeat(h) && h.AISession != "":
			stream = "s|" + h.AISession
		case isAgentHeartbeat(h):
			stream = "a|" + h.Machine + "|" + h.Editor
		default:
			stream = "h|" + h.Machine + "|" + h.Editor
		}
		if _, ok := streams[stream]; !ok {
			order = append(order, stream)
		}
		streams[stream] = append(streams[stream], h)
	}

	timeout := user.HeartbeatsTimeout()
	for _, stream := range order {
		hbs := streams[stream]
		for k := 0; k+1 < len(hbs); k++ {
			start, end := hbs[k].Time.T(), hbs[k+1].Time.T()
			if gap := end.Sub(start); gap <= 0 || gap > timeout {
				continue
			}
			agent := isAgentHeartbeat(hbs[k])
			group := GroupYou
			if agent {
				group = AgentGroup(hbs[k].Editor)
			}
			data.spans = append(data.spans, dashSpan{
				Interval: Interval{start, end},
				Project:  hbs[k].Project,
				Group:    group,
				Machine:  hbs[k].Machine,
				Agent:    agent,
				Stream:   stream,
			})
		}
	}

	srv.cache.SetDefault(key, data)
	return data, nil
}

func (srv *DashboardService) machineName(user *models.User, machine string) string {
	if name, err := srv.aliasSrvc.GetAliasOrDefault(user.ID, models.SummaryMachine, machine); err == nil {
		return name
	}
	return srv.config.App.MachineName(machine)
}

func (srv *DashboardService) projectName(user *models.User, project string) string {
	if name, err := srv.aliasSrvc.GetAliasOrDefault(user.ID, models.SummaryProject, project); err == nil {
		return name
	}
	return project
}

func intervalsOf(spans []dashSpan, keep func(dashSpan) bool) []Interval {
	var out []Interval
	for _, s := range spans {
		if keep == nil || keep(s) {
			out = append(out, s.Interval)
		}
	}
	return out
}

func (srv *DashboardService) Overview(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardOverview, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	prev, err := srv.load(user, from.Add(-to.Sub(from)), from, filters)
	if err != nil {
		return nil, err
	}

	return &view.DashboardOverview{
		Range:         view.DashboardRange{From: from, To: to},
		Time:          srv.timeStats(data, prev),
		Tokens:        tokenTotals(data.heartbeats),
		Cost:          srv.cost(data.heartbeats),
		CoverageNotes: coverageNotes(from, to),
	}, nil
}

func (srv *DashboardService) timeStats(data, prev *dashData) view.DashboardTime {
	all := intervalsOf(data.spans, nil)
	you := intervalsOf(data.spans, func(s dashSpan) bool { return !s.Agent })
	agents := intervalsOf(data.spans, func(s dashSpan) bool { return s.Agent })

	t := view.DashboardTime{
		TotalSeconds:         TotalDuration(all).Seconds(),
		PreviousTotalSeconds: TotalDuration(intervalsOf(prev.spans, nil)).Seconds(),
		YouSeconds:           TotalDuration(you).Seconds(),
		AgentSeconds:         TotalDuration(agents).Seconds(),
		OverlapSeconds:       TotalDuration(IntersectIntervals(you, agents)).Seconds(),
	}
	if t.TotalSeconds > 0 {
		t.AIPercent = round1(100 * t.AgentSeconds / t.TotalSeconds)
	}
	if t.PreviousTotalSeconds > 0 {
		change := round1(100 * (t.TotalSeconds - t.PreviousTotalSeconds) / t.PreviousTotalSeconds)
		t.ChangePercent = &change
	}
	return t
}

func tokenTotals(heartbeats []*models.Heartbeat) view.DashboardTokenTotals {
	var t view.DashboardTokenTotals
	for _, h := range heartbeats {
		addTokens(&t, h)
	}
	return finishTokens(t)
}

func addTokens(t *view.DashboardTokenTotals, h *models.Heartbeat) {
	t.Input += int64(h.AIInputTokens)
	t.CachedInput += int64(h.AICachedInputTokens)
	t.Output += int64(h.AIOutputTokens)
}

func finishTokens(t view.DashboardTokenTotals) view.DashboardTokenTotals {
	t.Total = t.Input + t.CachedInput + t.Output
	if t.Total > 0 {
		t.CachedPercent = round1(100 * float64(t.CachedInput) / float64(t.Total))
	}
	return t
}

func (srv *DashboardService) cost(heartbeats []*models.Heartbeat) view.DashboardCost {
	type key struct {
		model, version string
		subscription   bool
	}
	byKey := map[key]*view.DashboardModelCost{}
	for _, h := range heartbeats {
		if h.AIInputTokens == 0 && h.AICachedInputTokens == 0 && h.AIOutputTokens == 0 {
			continue
		}
		k := key{h.AIModel, h.AIModelVersion, h.AISubscriptionPlan != ""}
		mc, ok := byKey[k]
		if !ok {
			mc = &view.DashboardModelCost{Model: k.model, Version: k.version, Subscription: k.subscription}
			byKey[k] = mc
		}
		addTokens(&mc.Tokens, h)
		if h.AISubscriptionPlan != "" && !containsString(mc.Subscriptions, h.AISubscriptionPlan) {
			mc.Subscriptions = append(mc.Subscriptions, h.AISubscriptionPlan)
		}
	}

	c := view.DashboardCost{Currency: "USD", ByModel: []view.DashboardModelCost{}}
	for _, mc := range byKey {
		mc.Tokens = finishTokens(mc.Tokens)
		mc.Cost, mc.Priced = srv.pricing.Cost(mc.Model, mc.Version, mc.Tokens.Input, mc.Tokens.CachedInput, mc.Tokens.Output)
		if !mc.Priced {
			c.UnpricedTokens += mc.Tokens.Total
		}
		c.EstimatedAPIValue += mc.Cost
		if mc.Subscription {
			c.SubscriptionValue += mc.Cost
		} else {
			c.PayPerTokenValue += mc.Cost
		}
		c.ByModel = append(c.ByModel, *mc)
	}
	sort.Slice(c.ByModel, func(i, j int) bool { return c.ByModel[i].Cost > c.ByModel[j].Cost })
	c.EstimatedAPIValue, c.SubscriptionValue, c.PayPerTokenValue = round2(c.EstimatedAPIValue), round2(c.SubscriptionValue), round2(c.PayPerTokenValue)
	return c
}

func (srv *DashboardService) Tokens(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardTokens, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	days := dayStarts(from, to, user.TZ())
	byDay := make([]view.DashboardDayTokens, len(days))
	for i, d := range days {
		byDay[i].Date = d.Format("2006-01-02")
	}
	byModel := map[string]view.DashboardTokenTotals{}
	for _, h := range data.heartbeats {
		if i := dayIndex(days, h.Time.T()); i >= 0 {
			addTokens(&byDay[i].Tokens, h)
		}
		if h.AIModel != "" {
			k := strings.TrimSpace(h.AIModel + " " + h.AIModelVersion)
			t := byModel[k]
			addTokens(&t, h)
			byModel[k] = t
		}
	}
	for i := range byDay {
		byDay[i].Tokens = finishTokens(byDay[i].Tokens)
	}
	for k, t := range byModel {
		byModel[k] = finishTokens(t)
	}
	return &view.DashboardTokens{
		Range:         view.DashboardRange{From: from, To: to},
		Totals:        tokenTotals(data.heartbeats),
		ByDay:         byDay,
		ByModel:       byModel,
		CoverageNotes: coverageNotes(from, to),
	}, nil
}

func (srv *DashboardService) Cost(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardCostResponse, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	return &view.DashboardCostResponse{
		Range:         view.DashboardRange{From: from, To: to},
		Cost:          srv.cost(data.heartbeats),
		CoverageNotes: coverageNotes(from, to),
	}, nil
}

func (srv *DashboardService) Sessions(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardSessions, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	s := sessionStats(data.heartbeats, user.HeartbeatsTimeout(), user.TZ())
	s.Range = view.DashboardRange{From: from, To: to}
	return &s, nil
}

// sessionStats counts AI sessions. A session's length is its active time (gaps up to the timeout), not first-to-last.
func sessionStats(heartbeats []*models.Heartbeat, timeout time.Duration, tz *time.Location) view.DashboardSessions {
	times := map[string][]time.Time{}
	sessionDays := map[string]bool{}
	activeDays := map[string]bool{}
	for _, h := range heartbeats {
		if !isAgentHeartbeat(h) {
			continue
		}
		day := h.Time.T().In(tz).Format("2006-01-02")
		activeDays[day] = true
		if h.AISession == "" {
			continue
		}
		times[h.AISession] = append(times[h.AISession], h.Time.T())
		sessionDays[day+"|"+h.AISession] = true
	}

	var lengths []float64
	for _, ts := range times {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		if d := TotalDuration(HeartbeatIntervals(ts, timeout)); d > 0 {
			lengths = append(lengths, d.Seconds())
		}
	}

	s := view.DashboardSessions{Sessions: len(times), ActiveDays: len(activeDays)}
	if len(activeDays) > 0 {
		s.SessionsPerDay = round1(float64(len(sessionDays)) / float64(len(activeDays)))
	}
	if n := len(lengths); n > 0 {
		sort.Float64s(lengths)
		if n%2 == 1 {
			s.MedianSessionSeconds = lengths[n/2]
		} else {
			s.MedianSessionSeconds = (lengths[n/2-1] + lengths[n/2]) / 2
		}
	}
	return s
}

func (srv *DashboardService) Activity(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardActivity, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	tz := user.TZ()
	days := dayStarts(from, to, tz)
	out := &view.DashboardActivity{
		Range:  view.DashboardRange{From: from, To: to},
		Groups: DashboardGroups,
		Days:   make([]view.DashboardActivityDay, len(days)),
	}

	byGroup := map[string][]Interval{}
	for _, s := range data.spans {
		byGroup[s.Group] = append(byGroup[s.Group], s.Interval)
	}
	var total float64
	for i, d := range days {
		out.Days[i] = view.DashboardActivityDay{Date: d.Format("2006-01-02"), Seconds: map[string]float64{}, Tokens: map[string]int64{}}
		end := d.AddDate(0, 0, 1)
		for g, ivs := range byGroup {
			if secs := TotalDuration(ClipIntervals(ivs, d, end)).Seconds(); secs > 0 {
				out.Days[i].Seconds[g] = secs
			}
		}
	}
	for _, h := range data.heartbeats {
		if i := dayIndex(days, h.Time.T()); i >= 0 && isAgentHeartbeat(h) {
			out.Days[i].Tokens[AgentGroup(h.Editor)] += int64(h.AIInputTokens + h.AICachedInputTokens + h.AIOutputTokens)
		}
	}

	shares := map[string]float64{}
	for g, ivs := range byGroup {
		shares[g] = TotalDuration(ivs).Seconds()
		total += shares[g]
	}
	for _, g := range DashboardGroups {
		if secs := shares[g]; secs > 0 {
			out.Share = append(out.Share, view.DashboardShare{Group: g, Seconds: secs, Percent: round1(100 * secs / total)})
		}
	}
	if out.Share == nil {
		out.Share = []view.DashboardShare{}
	}

	out.Sessions = sessionStats(data.heartbeats, user.HeartbeatsTimeout(), tz)
	out.Sessions.Range = out.Range
	return out, nil
}

func (srv *DashboardService) Timeline(user *models.User, date time.Time, filters DashboardFilters) (*view.DashboardTimeline, error) {
	tz := user.TZ()
	from := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, tz)
	to := from.AddDate(0, 0, 1)
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}

	out := &view.DashboardTimeline{Date: from.Format("2006-01-02"), Groups: DashboardGroups, Rows: []view.DashboardTimelineRow{}}

	type rowKey struct{ project, group string }
	segments := map[rowKey][]Interval{}
	projectIvs := map[string][]Interval{}
	agentStreams := map[string][]Interval{}
	for _, s := range data.spans {
		clipped := ClipIntervals([]Interval{s.Interval}, from, to)
		if len(clipped) == 0 {
			continue
		}
		segments[rowKey{s.Project, s.Group}] = append(segments[rowKey{s.Project, s.Group}], clipped...)
		projectIvs[s.Project] = append(projectIvs[s.Project], clipped...)
		if s.Agent {
			agentStreams[s.Stream] = append(agentStreams[s.Stream], clipped...)
		}
	}

	var first, last time.Time
	for project, ivs := range projectIvs {
		row := view.DashboardTimelineRow{Project: project, Seconds: TotalDuration(ivs).Seconds()}
		for k, segs := range segments {
			if k.project != project {
				continue
			}
			for _, iv := range UnionIntervals(segs) {
				row.Segments = append(row.Segments, view.DashboardSegment{Start: iv.Start, End: iv.End, Group: k.group})
				if first.IsZero() || iv.Start.Before(first) {
					first = iv.Start
				}
				if iv.End.After(last) {
					last = iv.End
				}
			}
		}
		sort.Slice(row.Segments, func(i, j int) bool { return row.Segments[i].Start.Before(row.Segments[j].Start) })
		out.Rows = append(out.Rows, row)
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Seconds > out.Rows[j].Seconds })

	var all []Interval
	for _, ivs := range projectIvs {
		all = append(all, ivs...)
	}
	out.TotalSeconds = TotalDuration(all).Seconds()

	streams := make([][]Interval, 0, len(agentStreams))
	for _, ivs := range agentStreams {
		streams = append(streams, ivs)
	}
	out.PeakConcurrency = PeakConcurrency(streams)

	if first.IsZero() {
		out.WindowStart, out.WindowEnd = from.Add(9*time.Hour), from.Add(18*time.Hour)
	} else {
		out.WindowStart = first.In(tz).Truncate(time.Hour)
		out.WindowEnd = last.In(tz).Truncate(time.Hour)
		if out.WindowEnd.Before(last) {
			out.WindowEnd = out.WindowEnd.Add(time.Hour)
		}
	}
	return out, nil
}

func (srv *DashboardService) Machines(user *models.User, now time.Time) (*view.DashboardMachines, error) {
	data, err := srv.load(user, now.Add(-30*24*time.Hour).Truncate(time.Minute), now.Truncate(time.Minute).Add(time.Minute), DashboardFilters{})
	if err != nil {
		return nil, err
	}

	type agentKey struct{ machine, editor string }
	machines := map[string]*view.DashboardMachine{}
	latest := map[string]*models.Heartbeat{}
	agents := map[agentKey]*view.DashboardAgentSeen{}
	for _, h := range data.heartbeats {
		m, ok := machines[h.Machine]
		if !ok {
			m = &view.DashboardMachine{Name: h.Machine}
			machines[h.Machine] = m
		}
		t := h.Time.T()
		if !t.Before(m.LastSeen) {
			m.LastSeen, m.OS = t, h.OperatingSystem
			latest[h.Machine] = h
		}
		k := agentKey{h.Machine, h.Editor}
		a, ok := agents[k]
		if !ok {
			a = &view.DashboardAgentSeen{Editor: h.Editor, Group: AgentGroup(h.Editor)}
			if !isAgentHeartbeat(h) {
				a.Group = GroupYou
			}
			agents[k] = a
		}
		a.Heartbeats30d++
		if t.After(a.LastSeen) {
			a.LastSeen = t
		}
	}

	out := &view.DashboardMachines{Now: now, Machines: []view.DashboardMachine{}, Warnings: []view.DashboardWarning{}}
	for k, a := range agents {
		machines[k.machine].Agents = append(machines[k.machine].Agents, *a)
		if silent := now.Sub(a.LastSeen); a.Heartbeats30d >= dashboardSilentMinHb && silent >= dashboardSilentAfter {
			days := int(silent.Hours() / 24)
			out.Warnings = append(out.Warnings, view.DashboardWarning{
				Machine: k.machine, Editor: a.Editor, SilentDays: days, LastSeen: a.LastSeen,
				Message: fmt.Sprintf("%s on %s: silent for %d days", a.Editor, k.machine, days),
			})
		}
	}
	for name, m := range machines {
		m.Live = now.Sub(m.LastSeen) <= dashboardLiveWindow
		if m.Live {
			out.LiveCount++
			if h := latest[name]; isAgentHeartbeat(h) {
				m.ActiveAgent = AgentGroup(h.Editor)
			} else {
				m.ActiveAgent = h.Editor
			}
		}
		sort.Slice(m.Agents, func(i, j int) bool { return m.Agents[i].LastSeen.After(m.Agents[j].LastSeen) })
		out.Machines = append(out.Machines, *m)
	}
	sort.Slice(out.Machines, func(i, j int) bool { return out.Machines[i].LastSeen.After(out.Machines[j].LastSeen) })
	sort.Slice(out.Warnings, func(i, j int) bool { return out.Warnings[i].SilentDays < out.Warnings[j].SilentDays })
	return out, nil
}

func (srv *DashboardService) Projects(user *models.User, from, to time.Time, filters DashboardFilters) (*view.DashboardProjects, error) {
	data, err := srv.load(user, from, to, filters)
	if err != nil {
		return nil, err
	}
	tz := user.TZ()
	days := dayStarts(from, to, tz)
	if len(days) > dashboardSparklineDays {
		days = days[len(days)-dashboardSparklineDays:]
	}

	out := &view.DashboardProjects{Range: view.DashboardRange{From: from, To: to}, Projects: []view.DashboardProject{}}
	for _, d := range days {
		out.Days = append(out.Days, d.Format("2006-01-02"))
	}

	ivs := map[string][]Interval{}
	for _, s := range data.spans {
		ivs[s.Project] = append(ivs[s.Project], s.Interval)
	}
	byProject := map[string][]*models.Heartbeat{}
	for _, h := range data.heartbeats {
		byProject[h.Project] = append(byProject[h.Project], h)
	}

	statuses, source := srv.projectStatuses(user)
	out.StatusSource = source
	for project, hbs := range byProject {
		if project == "" {
			continue
		}
		p := view.DashboardProject{
			Name:         project,
			Status:       statuses[strings.ToLower(project)],
			Seconds:      TotalDuration(ivs[project]).Seconds(),
			Tokens:       tokenTotals(hbs).Total,
			Cost:         srv.cost(hbs).EstimatedAPIValue,
			LastActive:   hbs[len(hbs)-1].Time.T(),
			DailySeconds: make([]float64, len(days)),
		}
		for i, d := range days {
			p.DailySeconds[i] = TotalDuration(ClipIntervals(ivs[project], d, d.AddDate(0, 0, 1))).Seconds()
		}
		if p.Seconds > 0 || p.Tokens > 0 {
			out.Projects = append(out.Projects, p)
		}
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Seconds > out.Projects[j].Seconds })
	return out, nil
}

// projectStatuses returns lower-cased project name -> status (active, paused, done).
// Default source: Wakapi project labels named "active", "paused", "done" (or "status:active" etc.).
// When app.mnemosyne_url is set, Mnemosyne's project lifecycle is used instead.
func (srv *DashboardService) projectStatuses(user *models.User) (map[string]string, string) {
	if srv.config.App.MnemosyneURL != "" {
		if statuses, err := srv.mnemosyneStatuses(); err == nil {
			return statuses, "mnemosyne"
		}
	}
	statuses := map[string]string{}
	if grouped, err := srv.labelSrvc.GetByUserGrouped(user.ID); err == nil {
		for project, labels := range grouped {
			for _, l := range labels {
				if status := labelStatus(l.Label); status != "" {
					statuses[strings.ToLower(project)] = status
				}
			}
		}
	}
	return statuses, "labels"
}

func labelStatus(label string) string {
	l := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(label)), "status:")
	switch l {
	case "active", "paused", "done":
		return l
	}
	return ""
}

func (srv *DashboardService) mnemosyneStatuses() (map[string]string, error) {
	srv.mnemosyneMu.Lock()
	defer srv.mnemosyneMu.Unlock()
	if cached, ok := srv.cache.Get("mnemosyne-statuses"); ok {
		return cached.(map[string]string), nil
	}

	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(srv.config.App.MnemosyneURL, "/")+"/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.config.App.MnemosyneToken)
	res, err := srv.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mnemosyne returned %d", res.StatusCode)
	}
	var body struct {
		Projects []struct {
			Key       string `json:"key"`
			Name      string `json:"name"`
			Lifecycle string `json:"lifecycle"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	statuses := map[string]string{}
	for _, p := range body.Projects {
		status := labelStatus(p.Lifecycle)
		if status == "" {
			continue
		}
		statuses[strings.ToLower(p.Name)] = status
		key := p.Key[strings.LastIndex(p.Key, "/")+1:]
		statuses[strings.ToLower(key)] = status
	}
	srv.cache.Set("mnemosyne-statuses", statuses, 5*time.Minute)
	return statuses, nil
}

func coverageNotes(from, to time.Time) []string {
	notes := []string{}
	if from.Before(cachedTokensGapTo) && to.After(cachedTokensGapFrom) {
		notes = append(notes, "Cached tokens are missing from 6 Aug 2026 to 7 Oct 2026 01:24: token totals and cost for that period are too low.")
	}
	if from.Before(cachedTokensGapFrom) {
		notes = append(notes, "Before 6 Aug 2026, cached tokens were counted as input tokens, so cost for that period is too high.")
	}
	return notes
}

// dayStarts returns the start of each local day that overlaps [from, to).
func dayStarts(from, to time.Time, tz *time.Location) []time.Time {
	f := from.In(tz)
	d := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, tz)
	var out []time.Time
	for ; d.Before(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func dayIndex(days []time.Time, t time.Time) int {
	for i := len(days) - 1; i >= 0; i-- {
		if !t.Before(days[i]) {
			if i == len(days)-1 && !t.Before(days[i].AddDate(0, 0, 1)) {
				return -1
			}
			return i
		}
	}
	return -1
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func round2(f float64) float64 { return math.Round(f*100) / 100 }
