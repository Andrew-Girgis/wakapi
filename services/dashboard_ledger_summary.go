package services

import (
	"github.com/muety/wakapi/models"
	"strings"
	"time"
)

// Verification/provenance API uses fresh data, not the display's short cache.
func (s *DashboardService) LedgerSummary(u *models.User, from, to time.Time, f DashboardFilters) (UsageSummary, error) {
	rows, err := s.loadLedger(u, from, to, f, true)
	if err != nil {
		return UsageSummary{}, err
	}
	t := ledgerTotals(rows)
	out := UsageSummary{Events: int64(len(rows)), InputTokens: t.Input, CacheReadTokens: t.CachedInput, CacheWriteTokens: t.CacheWrite, UnknownInputTokens: t.UnknownInput, OutputTokens: t.Output, TotalTokens: t.Total}
	var held []models.TokenUsageEvent
	err = s.ledger.db.Model(&models.TokenUsageEvent{}).Where("token_usage_events.user_id=? AND occurred_ms>=? AND occurred_ms<? AND source='wakatime'", u.ID, from.UnixMilli(), to.UnixMilli()).Where(usageUncovered).Where(usageQuarantined).Select("machine,project,model,input_tokens,cache_read_tokens,cache_write_tokens,unknown_input_tokens,output_tokens").Find(&held).Error
	if err != nil {
		return out, err
	}
	for _, e := range held {
		if f.Agent != "" { // held observations are WakaTime fallback, which has no tool
			continue
		}
		if f.Machine != "" && !strings.EqualFold(f.Machine, s.machineName(u, e.Machine)) {
			continue
		}
		if f.Project != "" && !strings.EqualFold(f.Project, s.projectName(u, e.Project)) {
			continue
		}
		if !matchesModel(e.Model, f.Model) {
			continue
		}
		out.QuarantinedEvents++
		out.QuarantinedTokens += ledgerEventTotal(e)
	}
	return out, nil
}
