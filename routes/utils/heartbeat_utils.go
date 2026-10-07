package utils

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/muety/wakapi/models"
)

const maxTrackedUnknownFields = 100

var (
	knownHeartbeatFields = heartbeatJsonFields()
	validFieldName       = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	unknownFieldsMutex   sync.Mutex
	unknownFieldCounts   = map[string]int64{}
)

func ParseHeartbeats(r *http.Request) ([]*models.Heartbeat, error) {
	heartbeats, err := tryParseBulk(r)
	if err == nil {
		return heartbeats, err
	}

	heartbeats, err = tryParseSingle(r)
	if err == nil {
		return heartbeats, err
	}

	return []*models.Heartbeat{}, err
}

func tryParseBulk(r *http.Request) ([]*models.Heartbeat, error) {
	var heartbeats []*models.Heartbeat

	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	dec := json.NewDecoder(io.NopCloser(bytes.NewBuffer(body)))
	if err := dec.Decode(&heartbeats); err != nil {
		return nil, err
	}

	return heartbeats, nil
}

func tryParseSingle(r *http.Request) ([]*models.Heartbeat, error) {
	var heartbeat models.Heartbeat

	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	dec := json.NewDecoder(io.NopCloser(bytes.NewBuffer(body)))
	if err := dec.Decode(&heartbeat); err != nil {
		return nil, err
	}

	return []*models.Heartbeat{&heartbeat}, nil
}

// TrackUnknownHeartbeatFields counts the keys of the request's heartbeats that do not map to a field of models.Heartbeat.
// Call it once per request (ParseHeartbeats runs more than once, e.g. in the WakaTime relay middleware).
func TrackUnknownHeartbeatFields(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(body, &objects); err != nil {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil {
			return
		}
		objects = append(objects, object)
	}
	trackUnknownFields(objects)
}

// UnknownHeartbeatFieldCounts returns how often each unknown heartbeat field was received since start-up.
// Unknown fields are not stored, so a new field sent by a client shows up here instead of getting lost silently.
func UnknownHeartbeatFieldCounts() map[string]int64 {
	unknownFieldsMutex.Lock()
	defer unknownFieldsMutex.Unlock()

	counts := make(map[string]int64, len(unknownFieldCounts))
	for k, v := range unknownFieldCounts {
		counts[k] = v
	}
	return counts
}

// trackUnknownFields counts heartbeat keys that do not map to a field of models.Heartbeat.
// Only key names are recorded, never values.
func trackUnknownFields(objects []map[string]json.RawMessage) {
	unknownFieldsMutex.Lock()
	defer unknownFieldsMutex.Unlock()

	for _, object := range objects {
		for key := range object {
			if knownHeartbeatFields[strings.ToLower(key)] { // encoding/json matches keys case-insensitively
				continue
			}
			if !validFieldName.MatchString(key) {
				key = "_invalid"
			}
			if _, seen := unknownFieldCounts[key]; !seen {
				if len(unknownFieldCounts) >= maxTrackedUnknownFields {
					continue
				}
				slog.Info("received heartbeat with unknown field, value is not stored", "field", key)
			}
			unknownFieldCounts[key]++
		}
	}
}

// heartbeatJsonFields returns the lower-cased json keys that decode into a models.Heartbeat field.
func heartbeatJsonFields() map[string]bool {
	fields := map[string]bool{"id": true} // sent by some plugins and deliberately ignored
	t := reflect.TypeOf(models.Heartbeat{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[strings.ToLower(name)] = true
		}
	}
	return fields
}
