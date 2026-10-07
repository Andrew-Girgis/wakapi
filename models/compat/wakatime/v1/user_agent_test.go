package v1

import (
	"testing"

	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
)

func TestUserAgentEntry_FromModel_AIModelVersion(t *testing.T) {
	entry := (&UserAgentEntry{}).FromModel(&models.UserAgent{
		Value:   "wakatime/v2.26.15 (darwin-27.0.0-arm64) go1.26.8 opus/4.1-medium claude-code/2.1.284",
		AIModel: "Opus",
	})
	assert.Equal(t, "Opus", entry.AIModel)
	assert.Equal(t, "4.1", entry.AIModelVersion)
	assert.Equal(t, "medium", entry.AIModelComplexity)
}
