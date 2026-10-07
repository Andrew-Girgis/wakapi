package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var dashT0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

func iv(fromMin, toMin int) Interval {
	return Interval{dashT0.Add(time.Duration(fromMin) * time.Minute), dashT0.Add(time.Duration(toMin) * time.Minute)}
}

func TestUnionIntervals(t *testing.T) {
	assert.Nil(t, UnionIntervals(nil))
	assert.Equal(t, []Interval{iv(0, 30), iv(40, 50)}, UnionIntervals([]Interval{iv(20, 30), iv(0, 10), iv(5, 25), iv(40, 50)}))
	assert.Equal(t, []Interval{iv(0, 20)}, UnionIntervals([]Interval{iv(0, 10), iv(10, 20)})) // touching
	assert.Equal(t, []Interval{iv(0, 10)}, UnionIntervals([]Interval{iv(0, 10), iv(5, 5)}))   // empty interval dropped
}

func TestTotalDuration_CountsOverlapOnce(t *testing.T) {
	assert.Equal(t, 30*time.Minute, TotalDuration([]Interval{iv(0, 20), iv(10, 30)}))
	assert.Equal(t, time.Duration(0), TotalDuration(nil))
}

func TestIntersectIntervals(t *testing.T) {
	you := []Interval{iv(0, 30), iv(60, 90)}
	agents := []Interval{iv(20, 70), iv(80, 85)}
	assert.Equal(t, []Interval{iv(20, 30), iv(60, 70), iv(80, 85)}, IntersectIntervals(you, agents))
	// you + agents - overlap = total
	overlap := TotalDuration(IntersectIntervals(you, agents))
	total := TotalDuration(append(append([]Interval{}, you...), agents...))
	assert.Equal(t, TotalDuration(you)+TotalDuration(agents)-overlap, total)
}

func TestClipIntervals(t *testing.T) {
	assert.Equal(t, []Interval{iv(10, 20), iv(30, 35)}, ClipIntervals([]Interval{iv(0, 20), iv(30, 50), iv(60, 70)}, dashT0.Add(10*time.Minute), dashT0.Add(35*time.Minute)))
}

func TestPeakConcurrency(t *testing.T) {
	assert.Equal(t, 0, PeakConcurrency(nil))
	assert.Equal(t, 1, PeakConcurrency([][]Interval{{iv(0, 10), iv(5, 15)}}))    // one stream never counts twice
	assert.Equal(t, 1, PeakConcurrency([][]Interval{{iv(0, 10)}, {iv(10, 20)}})) // back to back
	assert.Equal(t, 3, PeakConcurrency([][]Interval{{iv(0, 30)}, {iv(10, 20)}, {iv(15, 40)}, {iv(35, 50)}}))
}

func TestHeartbeatIntervals(t *testing.T) {
	times := []time.Time{dashT0, dashT0.Add(2 * time.Minute), dashT0.Add(4 * time.Minute), dashT0.Add(30 * time.Minute), dashT0.Add(31 * time.Minute)}
	got := HeartbeatIntervals(times, 10*time.Minute)
	assert.Equal(t, []Interval{iv(0, 2), iv(2, 4), iv(30, 31)}, got) // the 26 minute gap is not counted
	assert.Nil(t, HeartbeatIntervals([]time.Time{dashT0}, 10*time.Minute))
}
