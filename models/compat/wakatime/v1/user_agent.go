package v1

import (
	"time"

	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/utils"
)

type UserAgentsViewModel struct {
	Data       []*UserAgentEntry `json:"data"`
	TotalPages int               `json:"total_pages"`
}

type UserAgentEntry struct {
	Id                 string `json:"id"`
	Editor             string `json:"editor"`
	AIModel            string `json:"ai_model"`
	AIModelVersion     string `json:"ai_model_version"`
	AIModelComplexity  string `json:"ai_model_complexity"`
	Os                 string `json:"os"`
	Value              string `json:"value"`
	Version            string `json:"version"`              // currently not implemented
	IsBrowserExtension bool   `json:"is_browser_extension"` // currently not implemented
	IsDesktopApp       bool   `json:"is_desktop_app"`       // currently not implemented
	FirstSeen          string `json:"first_seen"`
	LastSeen           string `json:"last_seen"`
}

func (e *UserAgentEntry) FromModel(userAgent *models.UserAgent) *UserAgentEntry {
	e.Id = userAgent.Id
	e.Editor = userAgent.Editor
	e.AIModel = userAgent.AIModel
	if parsed, err := utils.ParseUserAgent(userAgent.Value); err == nil && parsed.AIModel != "" {
		e.AIModelVersion = parsed.AIModelVersion
		e.AIModelComplexity = parsed.AIModelComplexity
	}
	e.Os = userAgent.Os
	e.Value = userAgent.Value
	e.FirstSeen = userAgent.FirstSeen.Format(time.RFC3339)
	e.LastSeen = userAgent.LastSeen.Format(time.RFC3339)
	return e
}
