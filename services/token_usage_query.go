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
	err := s.countedScope(user, from, to).Select("source, occurred_ms, machine, project, model, input_tokens, cache_read_tokens, cache_write_tokens, unknown_input_tokens, output_tokens, raw_usage_json, observed_ms").Order("occurred_ms").Find(&rows).Error
	return rows, err
}

func (s *TokenUsageService) FirstCountedTime(user string) (int64, error) {
	var result struct{ First int64 }
	err := s.countedScope(user, 0, 9223372036854775807).Select("COALESCE(MIN(occurred_ms),0) AS first").Scan(&result).Error
	return result.First, err
}
