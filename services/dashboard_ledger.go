package services

import (
	"fmt"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/models/view"
	"sort"
	"strings"
	"time"
)

// Model punctuation/family aliases are for filtering only, never fuzzy pricing.
func metricModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"anthropic/", "openai/"} {
		m = strings.TrimPrefix(m, prefix)
	}
	m = strings.ReplaceAll(strings.ReplaceAll(m, " ", "-"), ".", "-")
	for _, family := range []string{"opus", "sonnet", "haiku", "fable"} {
		if m == family || strings.HasPrefix(m, family+"-") {
			m = "claude-" + m
			break
		}
	}
	return m
}
func matchesModel(actual, selected string) bool {
	return selected == "" || metricModel(actual) == metricModel(selected)
}
func heartbeatMatchesModel(h *models.Heartbeat, model string) bool {
	return matchesModel(strings.TrimSpace(h.AIModel+" "+h.AIModelVersion), model)
}
func tokenHeartbeats(rows []*models.Heartbeat, f DashboardFilters) []*models.Heartbeat {
	if f.Model == "" {
		return rows
	}
	out := []*models.Heartbeat{}
	for _, h := range rows {
		if heartbeatMatchesModel(h, f.Model) {
			out = append(out, h)
		}
	}
	return out
}

func (s *DashboardService) usingLedger() bool { return s.config.App.TokenSource() == "ledger" }
func (s *DashboardService) accounting() view.DashboardAccounting {
	return view.DashboardAccounting{TokenSource: s.config.App.TokenSource(), Provisional: s.usingLedger()}
}

// Cache one immutable counted projection for every panel's exact UTC scope.
// Normalize aliases AFTER global transcript/fallback exclusion, before filters.
func (s *DashboardService) loadLedger(u *models.User, from, to time.Time, f DashboardFilters, fresh ...bool) ([]models.TokenUsageEvent, error) {
	if s.ledger == nil {
		return nil, fmt.Errorf("ledger service unavailable")
	}
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	key := fmt.Sprintf("ledger|%s|%d|%d", u.ID, from.UnixMilli(), to.UnixMilli())
	var all []models.TokenUsageEvent
	bypass := len(fresh) > 0 && fresh[0]
	if found, ok := s.cache.Get(key); ok && !bypass {
		all = found.([]models.TokenUsageEvent)
	} else {
		var err error
		all, err = s.ledger.CountedEvents(u.ID, from.UnixMilli(), to.UnixMilli())
		if err != nil {
			return nil, err
		}
		for i := range all {
			all[i].Machine = s.machineName(u, all[i].Machine)
			all[i].Project = s.projectName(u, all[i].Project)
		}
		if !bypass {
			s.cache.SetDefault(key, all)
		}
	}
	rows := make([]models.TokenUsageEvent, 0, len(all))
	for _, e := range all {
		if f.Machine != "" && !strings.EqualFold(f.Machine, e.Machine) {
			continue
		}
		if f.Project != "" && !strings.EqualFold(f.Project, e.Project) {
			continue
		}
		if !matchesModel(e.Model, f.Model) {
			continue
		}
		rows = append(rows, e)
	}
	return rows, nil
}
func ledgerEventTotal(e models.TokenUsageEvent) int64 {
	return e.InputTokens + e.CacheReadTokens + e.CacheWriteTokens + e.UnknownInputTokens + e.OutputTokens
}
func addLedgerTokens(t *view.DashboardTokenTotals, e models.TokenUsageEvent) {
	t.Input += e.InputTokens
	t.CachedInput += e.CacheReadTokens
	t.CacheWrite += e.CacheWriteTokens
	t.UnknownInput += e.UnknownInputTokens
	t.Output += e.OutputTokens
}
func ledgerTotals(rows []models.TokenUsageEvent) view.DashboardTokenTotals {
	var t view.DashboardTokenTotals
	for _, e := range rows {
		addLedgerTokens(&t, e)
	}
	return finishTokens(t)
}
func ledgerGroup(source string) string {
	switch source {
	case "claude":
		return GroupClaudeCode
	case "codex":
		return GroupCodex
	case "pi":
		return GroupPi
	case "opencode":
		return GroupOpenCode
	case "wakatime":
		return "WakaTime fallback"
	}
	return GroupOther
}

func (s *DashboardService) ledgerNotes(u *models.User, rows []models.TokenUsageEvent) []string {
	notes := []string{"Provisional accounted usage: surviving source records, not certified complete lifetime history. Background reconciliation remains enabled.", "Time, sessions and live machines use heartbeat activity; model filters apply to tokens only.", "Copied usage counts once; machine/project attribution follows first-observed history, not certified execution origin."}
	t := ledgerTotals(rows)
	if t.CacheWrite > 0 {
		notes = append(notes, "Cache writes are priced only with a complete verified TTL split and an explicit configured write rate; remaining writes are unpriced.")
	}
	if t.UnknownInput > 0 {
		notes = append(notes, fmt.Sprintf("%d input tokens have an unknown cache split and remain unpriced.", t.UnknownInput))
	}
	var fallback int64
	for _, e := range rows {
		if e.Source == "wakatime" {
			fallback += ledgerEventTotal(e)
		}
	}
	if fallback > 0 {
		notes = append(notes, fmt.Sprintf("%d tokens are non-overlapping WakaTime fallback; tool-level attribution is unavailable in the stored ledger.", fallback))
	}
	var holds, conflicts int64
	if err := s.ledger.db.Model(&models.TokenUsageQuarantinedSession{}).Where("user_id=? AND status='open'", u.ID).Count(&holds).Error; err == nil && holds > 0 {
		notes = append(notes, fmt.Sprintf("%d quarantined fallback sessions are excluded (user-wide).", holds))
	}
	if err := s.ledger.db.Model(&models.TokenUsageConflict{}).Where("user_id=?", u.ID).Count(&conflicts).Error; err == nil && conflicts > 0 {
		notes = append(notes, fmt.Sprintf("%d conflicting source observations await review (user-wide); incoming revisions are not counted.", conflicts))
	}
	return notes
}

// Estimate only verified buckets at exactly configured rates, never old spend.
func (s *DashboardService) ledgerCost(rows []models.TokenUsageEvent) view.DashboardCost {
	c := view.DashboardCost{Currency: "USD", PricingBasis: "Configured standard-tier, short-context/global-routing USD rates; partial API-equivalent value, not historical bills. Unknown input, missing rates and unverified cache-write TTL/rates are excluded.", ByModel: []view.DashboardModelCost{}}
	groups := map[string][]models.TokenUsageEvent{}
	for _, e := range rows {
		groups[e.Model] = append(groups[e.Model], e)
	}
	for model, events := range groups {
		mc := view.DashboardModelCost{Model: model, Tokens: ledgerTotals(events)}
		for _, e := range events {
			if s.pricing != nil {
				value, priced := s.pricing.LedgerEventCost(e)
				mc.Cost += value
				mc.PricedTokens += priced
			}
		}
		mc.Priced = mc.PricedTokens > 0
		mc.UnpricedTokens = mc.Tokens.Total - mc.PricedTokens
		c.EstimatedAPIValue += mc.Cost
		c.PricedTokens += mc.PricedTokens
		c.UnpricedTokens += mc.UnpricedTokens
		mc.Cost = round2(mc.Cost)
		c.ByModel = append(c.ByModel, mc)
	}
	c.Available = c.PricedTokens > 0
	c.EstimatedAPIValue = round2(c.EstimatedAPIValue)
	sort.Slice(c.ByModel, func(i, j int) bool {
		if c.ByModel[i].Cost == c.ByModel[j].Cost {
			return c.ByModel[i].Model < c.ByModel[j].Model
		}
		return c.ByModel[i].Cost > c.ByModel[j].Cost
	})
	return c
}

func (s *DashboardService) ledgerTokens(u *models.User, from, to time.Time, f DashboardFilters) (*view.DashboardTokens, error) {
	rows, err := s.loadLedger(u, from, to, f)
	if err != nil {
		return nil, err
	}
	days := dayStarts(from, to, u.TZ())
	out := &view.DashboardTokens{DashboardAccounting: s.accounting(), Range: view.DashboardRange{From: from, To: to}, Totals: ledgerTotals(rows), ByDay: make([]view.DashboardDayTokens, len(days)), ByModel: map[string]view.DashboardTokenTotals{}, CoverageNotes: s.ledgerNotes(u, rows)}
	for i, d := range days {
		out.ByDay[i].Date = d.Format("2006-01-02")
	}
	for _, e := range rows {
		if i := dayIndex(days, time.UnixMilli(e.OccurredMs)); i >= 0 {
			addLedgerTokens(&out.ByDay[i].Tokens, e)
		}
		t := out.ByModel[e.Model]
		addLedgerTokens(&t, e)
		out.ByModel[e.Model] = t
	}
	for i := range out.ByDay {
		out.ByDay[i].Tokens = finishTokens(out.ByDay[i].Tokens)
	}
	for k, t := range out.ByModel {
		out.ByModel[k] = finishTokens(t)
	}
	return out, nil
}

func (s *DashboardService) applyLedgerProjects(u *models.User, from, to time.Time, f DashboardFilters, out *view.DashboardProjects, statuses map[string]string) error {
	rows, err := s.loadLedger(u, from, to, f)
	if err != nil {
		return err
	}
	projects := map[string]*view.DashboardProject{}
	for i := range out.Projects {
		p := &out.Projects[i]
		p.Tokens = 0
		p.Cost = 0
		p.CostAvailable = false
		projects[p.Name] = p
	}
	groups := map[string][]models.TokenUsageEvent{}
	for _, e := range rows {
		groups[e.Project] = append(groups[e.Project], e)
	}
	// Append first, then build pointers, so slice growth cannot invalidate them.
	for name := range groups {
		if _, ok := projects[name]; !ok {
			out.Projects = append(out.Projects, view.DashboardProject{Name: name, Status: statuses[strings.ToLower(name)], DailySeconds: make([]float64, len(out.Days))})
		}
	}
	projects = map[string]*view.DashboardProject{}
	for i := range out.Projects {
		projects[out.Projects[i].Name] = &out.Projects[i]
	}
	for name, events := range groups {
		p := projects[name]
		p.Tokens = ledgerTotals(events).Total
		c := s.ledgerCost(events)
		p.Cost = c.EstimatedAPIValue
		p.CostAvailable = c.Available
		p.UnpricedTokens = c.UnpricedTokens
		// last_active remains heartbeat-based; don't manufacture activity from usage.
	}
	return nil
}

func (s *DashboardService) Options(u *models.User) (*view.DashboardOptions, error) {
	out := &view.DashboardOptions{DashboardAccounting: s.accounting(), Projects: []string{}, Machines: []string{}, Models: []string{}}
	p, m, v := map[string]bool{}, map[string]bool{}, map[string]bool{}
	from := s.ClampFrom(u, time.Unix(0, 0))
	to := time.Now().Add(time.Second)
	data, err := s.load(u, from, to, DashboardFilters{})
	if err != nil {
		return nil, err
	}
	for _, h := range data.heartbeats {
		p[h.Project] = true
		m[h.Machine] = true
		if !s.usingLedger() {
			v[strings.TrimSpace(h.AIModel+" "+h.AIModelVersion)] = true
		}
	}
	if s.usingLedger() {
		rows, err := s.loadLedger(u, from, to, DashboardFilters{})
		if err != nil {
			return nil, err
		}
		for _, e := range rows {
			p[e.Project] = true
			m[e.Machine] = true
			v[e.Model] = true
		}
	}
	for key := range p {
		if key != "" {
			out.Projects = append(out.Projects, key)
		}
	}
	for key := range m {
		if key != "" {
			out.Machines = append(out.Machines, key)
		}
	}
	for key := range v {
		if key != "" {
			out.Models = append(out.Models, key)
		}
	}
	sort.Strings(out.Projects)
	sort.Strings(out.Machines)
	sort.Strings(out.Models)
	return out, nil
}
