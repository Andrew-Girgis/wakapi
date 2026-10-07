package migrations

import (
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/utils"
	"gorm.io/gorm"
)

// Fills ai_model_version and ai_model_complexity for heartbeats stored before these columns existed.
// Both are parsed from the stored user agent, so each distinct user agent is only parsed once.
func init() {
	const name = "20261007-backfill_ai_model_version"
	f := migrationFunc{
		name:       name,
		background: true,
		f: func(db *gorm.DB, cfg *config.Config) error {
			if hasRun(name, db) {
				return nil
			}

			var userAgents []string
			if err := db.Model(&models.Heartbeat{}).
				Distinct("user_agent").
				Where("ai_model <> ''").
				Where("ai_model_version IS NULL OR ai_model_version = ''").
				Pluck("user_agent", &userAgents).Error; err != nil {
				return err
			}

			for _, ua := range userAgents {
				parsed, err := utils.ParseUserAgent(ua)
				if err != nil || parsed.AIModelVersion == "" {
					continue
				}
				if err := db.Model(&models.Heartbeat{}).
					Where("user_agent = ?", ua).
					Where("ai_model <> ''").
					Updates(map[string]any{
						"ai_model_version":    parsed.AIModelVersion,
						"ai_model_complexity": parsed.AIModelComplexity,
					}).Error; err != nil {
					return err
				}
			}

			setHasRun(name, db)
			return nil
		},
	}

	registerPostMigration(f)
}
