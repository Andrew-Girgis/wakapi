package services

import (
	"sort"
	"time"
)

// Interval is a half-open time range [Start, End).
type Interval struct {
	Start time.Time
	End   time.Time
}

func (i Interval) Duration() time.Duration {
	if i.End.Before(i.Start) {
		return 0
	}
	return i.End.Sub(i.Start)
}

// UnionIntervals merges overlapping and touching intervals. The input is not modified.
func UnionIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return nil
	}
	sorted := make([]Interval, 0, len(intervals))
	for _, i := range intervals {
		if i.End.After(i.Start) {
			sorted = append(sorted, i)
		}
	}
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Start.Before(sorted[b].Start) })

	var out []Interval
	for _, i := range sorted {
		if n := len(out); n > 0 && !i.Start.After(out[n-1].End) {
			if i.End.After(out[n-1].End) {
				out[n-1].End = i.End
			}
			continue
		}
		out = append(out, i)
	}
	return out
}

// TotalDuration returns the length of the union of the intervals, so overlapping time is counted once.
func TotalDuration(intervals []Interval) time.Duration {
	var total time.Duration
	for _, i := range UnionIntervals(intervals) {
		total += i.Duration()
	}
	return total
}

// IntersectIntervals returns the time covered by both a and b.
func IntersectIntervals(a, b []Interval) []Interval {
	ua, ub := UnionIntervals(a), UnionIntervals(b)
	var out []Interval
	for i, j := 0, 0; i < len(ua) && j < len(ub); {
		start := maxTime(ua[i].Start, ub[j].Start)
		end := minTime(ua[i].End, ub[j].End)
		if end.After(start) {
			out = append(out, Interval{start, end})
		}
		if ua[i].End.Before(ub[j].End) {
			i++
		} else {
			j++
		}
	}
	return out
}

// ClipIntervals cuts the intervals to [from, to).
func ClipIntervals(intervals []Interval, from, to time.Time) []Interval {
	var out []Interval
	for _, i := range intervals {
		start, end := maxTime(i.Start, from), minTime(i.End, to)
		if end.After(start) {
			out = append(out, Interval{start, end})
		}
	}
	return out
}

// PeakConcurrency returns the highest number of streams active at the same moment.
// Each stream's intervals are merged first, so one stream never counts twice.
func PeakConcurrency(streams [][]Interval) int {
	type event struct {
		t     time.Time
		delta int
	}
	var events []event
	for _, s := range streams {
		for _, i := range UnionIntervals(s) {
			events = append(events, event{i.Start, 1}, event{i.End, -1})
		}
	}
	// ends before starts at the same instant, so back-to-back intervals do not overlap
	sort.Slice(events, func(a, b int) bool {
		if events[a].t.Equal(events[b].t) {
			return events[a].delta < events[b].delta
		}
		return events[a].t.Before(events[b].t)
	})
	current, peak := 0, 0
	for _, e := range events {
		current += e.delta
		if current > peak {
			peak = current
		}
	}
	return peak
}

// HeartbeatIntervals turns a time-sorted heartbeat stream into activity intervals:
// the gap to the next heartbeat counts when it is at most timeout, like Wakapi's and WakaTime's duration logic.
func HeartbeatIntervals(times []time.Time, timeout time.Duration) []Interval {
	var out []Interval
	for k := 0; k+1 < len(times); k++ {
		gap := times[k+1].Sub(times[k])
		if gap > 0 && gap <= timeout {
			out = append(out, Interval{times[k], times[k+1]})
		}
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
