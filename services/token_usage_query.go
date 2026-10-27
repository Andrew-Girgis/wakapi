package services

import (
	"fmt"
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

const usageUncovered = `NOT EXISTS (SELECT 1 FROM token_usage_session_refs AS transcript
 WHERE transcript.user_id = token_usage_events.user_id
 AND transcript.session_key = token_usage_events.session_key
 AND transcript.source <> 'wakatime')`
const usageQuarantined = `EXISTS (SELECT 1 FROM token_usage_quarantined_sessions AS suspect
 WHERE suspect.user_id = token_usage_events.user_id
 AND suspect.source = 'wakatime'
 AND suspect.session_key = token_usage_events.session_key
 AND suspect.status = 'open')`

// Exclusion is global to a session, not restricted to the selected project,
// machine, model or date. Filtering first would resurrect copied fallback.
func (s *TokenUsageService) countedScope(user string, from, to int64) *gorm.DB {
	return s.db.Model(&models.TokenUsageEvent{}).
		Where("token_usage_events.user_id=? AND occurred_ms>=? AND occurred_ms<?", user, from, to).
		Where("(token_usage_events.source <> 'wakatime' OR " + usageUncovered + ")").
		Where("(token_usage_events.source <> 'wakatime' OR NOT " + usageQuarantined + ")")
}

func (s *TokenUsageService) CountedEvents(user string, from, to int64) ([]models.TokenUsageEvent, error) {
	if user == "" || from < 0 || to <= from {
		return nil, fmt.Errorf("invalid usage range")
	}
	var rows []models.TokenUsageEvent
	err := s.countedScope(user, from, to).Select("source, source_key, session_key, occurred_ms, machine, project, model, input_tokens, cache_read_tokens, cache_write_tokens, unknown_input_tokens, output_tokens, raw_usage_json, observed_ms").Order("occurred_ms").Find(&rows).Error
	return rows, err
}

func (s *TokenUsageService) FirstCountedTime(user string) (int64, error) {
	var result struct{ First int64 }
	err := s.countedScope(user, 0, 9223372036854775807).Select("COALESCE(MIN(occurred_ms),0) AS first").Scan(&result).Error
	return result.First, err
}

// SessionRefs maps (source, source_key) of copied/forked events to every session that contains them.
func (s *TokenUsageService) SessionRefs(user string) (map[[2]string][]string, error) {
	var rows []models.TokenUsageSessionRef
	if err := s.db.Where("user_id=?", user).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[[2]string][]string{}
	for _, r := range rows {
		k := [2]string{r.Source, r.SourceKey}
		out[k] = append(out[k], r.SessionKey)
	}
	return out, nil
}

// SessionPlans returns, per heartbeat ai_session, the editor and subscription plan of every agent heartbeat of
// that session. No time bound: a session's attribution must not depend on the selected range. Queried by the
// indexed user_id with chunked IN lists; adds no index, so the original heartbeat schema stays unchanged.
func (s *TokenUsageService) SessionPlans(user string, sessions []string) (map[string][]SessionPlanRow, error) {
	out := map[string][]SessionPlanRow{}
	for start := 0; start < len(sessions); start += 500 {
		end := min(start+500, len(sessions))
		var rows []SessionPlanRow
		err := s.db.Table("heartbeats").Select("ai_session, editor, category, ai_subscription_plan").
			Where("user_id=? AND ai_session IN ?", user, sessions[start:end]).
			Group("ai_session, editor, category, ai_subscription_plan").Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.AISession] = append(out[r.AISession], r)
		}
	}
	return out, nil
}

type SessionPlanRow struct {
	AISession          string `gorm:"column:ai_session"`
	Editor             string `gorm:"column:editor"`
	Category           string `gorm:"column:category"`
	AISubscriptionPlan string `gorm:"column:ai_subscription_plan"`
}
