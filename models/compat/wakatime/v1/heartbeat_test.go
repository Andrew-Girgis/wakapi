package v1

import (
	"testing"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
)

func TestHeartbeatsToCompat_AIFields(t *testing.T) {
	heartbeats := []*models.Heartbeat{{
		ID:                  1,
		Entity:              "Claude 7782ee12",
		Time:                models.CustomTime(time.Unix(1791347616, 0)),
		AISession:           "7782ee12",
		AIInputTokens:       1986,
		AICachedInputTokens: 90512,
		AIOutputTokens:      526,
	}}

	out := HeartbeatsToCompat(heartbeats)
	assert.Len(t, out, 1)
	assert.Equal(t, 1986, out[0].AIInputTokens)
	assert.Equal(t, 90512, out[0].AICachedInputTokens)
	assert.Equal(t, 526, out[0].AIOutputTokens)
}
