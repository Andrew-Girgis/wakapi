package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func resetUnknownFields() {
	unknownFieldsMutex.Lock()
	defer unknownFieldsMutex.Unlock()
	unknownFieldCounts = map[string]int64{}
}

func TestTrackUnknownHeartbeatFields_Bulk(t *testing.T) {
	resetUnknownFields()

	body := `[{"entity":"Claude e2e","time":1791347616.5,"ai_input_tokens":1986,"ai_subscription_plan":"max","dependencies":["fmt"],"future_field":1},
	          {"entity":"main.go","time":1791347617.5,"AI_Input_Tokens":5,"id":"abc","future_field":2}]`
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	heartbeats, err := ParseHeartbeats(r)
	assert.Nil(t, err)
	assert.Len(t, heartbeats, 2)
	assert.Empty(t, UnknownHeartbeatFieldCounts()) // parsing alone does not count, it runs more than once per request

	TrackUnknownHeartbeatFields(r)
	heartbeats, err = ParseHeartbeats(r) // body is still readable afterwards
	assert.Nil(t, err)
	assert.Len(t, heartbeats, 2)

	counts := UnknownHeartbeatFieldCounts()
	assert.Equal(t, map[string]int64{"future_field": 2, "dependencies": 1}, counts)
}

func TestTrackUnknownHeartbeatFields_Single(t *testing.T) {
	resetUnknownFields()

	TrackUnknownHeartbeatFields(httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"entity":"main.go","time":1791347616.5,"userAgent":"x"}`)))
	assert.Equal(t, map[string]int64{"userAgent": 1}, UnknownHeartbeatFieldCounts())
}

func TestTrackUnknownHeartbeatFields_SanitizedAndCapped(t *testing.T) {
	resetUnknownFields()

	TrackUnknownHeartbeatFields(httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"entity":"main.go","time":1,"bad\"key":1}`)))
	assert.Equal(t, map[string]int64{"_invalid": 1}, UnknownHeartbeatFieldCounts())

	resetUnknownFields()
	for i := 0; i < maxTrackedUnknownFields+20; i++ {
		trackUnknownFields([]map[string]json.RawMessage{{fmt.Sprintf("field_%d", i): nil}})
	}
	assert.Len(t, UnknownHeartbeatFieldCounts(), maxTrackedUnknownFields)
}
