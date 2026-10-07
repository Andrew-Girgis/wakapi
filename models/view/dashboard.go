package view

import "time"

// Response models for the /api/dashboard/* endpoints. Durations are in seconds, costs in USD.

type DashboardRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type DashboardTokenTotals struct {
	Input         int64   `json:"input"`        // new input tokens (incl. cache writes, as counted by wakatime-cli)
	CachedInput   int64   `json:"cached_input"` // cache read tokens
	Output        int64   `json:"output"`
	Total         int64   `json:"total"`
	CachedPercent float64 `json:"cached_percent"`
}

type DashboardTime struct {
	TotalSeconds         float64  `json:"total_seconds"`
	PreviousTotalSeconds float64  `json:"previous_total_seconds"`
	ChangePercent        *float64 `json:"change_percent"` // nil when the previous period has no activity
	YouSeconds           float64  `json:"you_seconds"`
	AgentSeconds         float64  `json:"agent_seconds"`
	OverlapSeconds       float64  `json:"overlap_seconds"` // you and agents at the same time; you + agents - overlap = total
	AIPercent            float64  `json:"ai_percent"`      // agent time / total time
}

type DashboardModelCost struct {
	Model         string               `json:"model"`
	Version       string               `json:"version"`
	Tokens        DashboardTokenTotals `json:"tokens"`
	Cost          float64              `json:"cost"`
	Priced        bool                 `json:"priced"`
	Subscription  bool                 `json:"subscription"`
	Subscriptions []string             `json:"subscription_plans,omitempty"`
}

type DashboardCost struct {
	EstimatedAPIValue float64              `json:"estimated_api_value"` // all priced tokens at API list prices
	SubscriptionValue float64              `json:"subscription_value"`  // part of it used through a subscription plan (not billed per token)
	PayPerTokenValue  float64              `json:"pay_per_token_value"` // part of it without a subscription plan
	UnpricedTokens    int64                `json:"unpriced_tokens"`     // tokens of models without a price
	ByModel           []DashboardModelCost `json:"by_model"`
	Currency          string               `json:"currency"`
}

type DashboardOverview struct {
	Range         DashboardRange       `json:"range"`
	Time          DashboardTime        `json:"time"`
	Tokens        DashboardTokenTotals `json:"tokens"`
	Cost          DashboardCost        `json:"cost"`
	CoverageNotes []string             `json:"coverage_notes"`
}

type DashboardTokens struct {
	Range         DashboardRange                  `json:"range"`
	Totals        DashboardTokenTotals            `json:"totals"`
	ByDay         []DashboardDayTokens            `json:"by_day"`
	ByModel       map[string]DashboardTokenTotals `json:"by_model"` // key "Model version", e.g. "Opus 5-5"
	CoverageNotes []string                        `json:"coverage_notes"`
}

type DashboardDayTokens struct {
	Date   string               `json:"date"`
	Tokens DashboardTokenTotals `json:"tokens"`
}

type DashboardCostResponse struct {
	Range         DashboardRange `json:"range"`
	Cost          DashboardCost  `json:"cost"`
	CoverageNotes []string       `json:"coverage_notes"`
}

type DashboardSessions struct {
	Range                DashboardRange `json:"range"`
	Sessions             int            `json:"sessions"`
	ActiveDays           int            `json:"active_days"`
	SessionsPerDay       float64        `json:"sessions_per_day"` // distinct (day, session) pairs / days with agent activity
	MedianSessionSeconds float64        `json:"median_session_seconds"`
}

type DashboardActivityDay struct {
	Date    string             `json:"date"`
	Seconds map[string]float64 `json:"seconds"` // by group
	Tokens  map[string]int64   `json:"tokens"`  // by group
}

type DashboardShare struct {
	Group   string  `json:"group"`
	Seconds float64 `json:"seconds"`
	Percent float64 `json:"percent"`
}

type DashboardActivity struct {
	Range    DashboardRange         `json:"range"`
	Groups   []string               `json:"groups"`
	Days     []DashboardActivityDay `json:"days"`
	Share    []DashboardShare       `json:"share"`
	Sessions DashboardSessions      `json:"sessions"`
}

type DashboardSegment struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Group string    `json:"group"`
}

type DashboardTimelineRow struct {
	Project  string             `json:"project"`
	Seconds  float64            `json:"seconds"`
	Segments []DashboardSegment `json:"segments"`
}

type DashboardTimeline struct {
	Date            string                 `json:"date"`
	WindowStart     time.Time              `json:"window_start"`
	WindowEnd       time.Time              `json:"window_end"`
	Groups          []string               `json:"groups"`
	Rows            []DashboardTimelineRow `json:"rows"`
	PeakConcurrency int                    `json:"peak_concurrency"`
	TotalSeconds    float64                `json:"total_seconds"`
}

type DashboardAgentSeen struct {
	Editor        string    `json:"editor"`
	Group         string    `json:"group"`
	LastSeen      time.Time `json:"last_seen"`
	Heartbeats30d int       `json:"heartbeats_30d"`
}

type DashboardMachine struct {
	Name        string               `json:"name"`
	OS          string               `json:"os"`
	LastSeen    time.Time            `json:"last_seen"`
	Live        bool                 `json:"live"`
	ActiveAgent string               `json:"active_agent"` // group of the most recent heartbeat, when live
	Agents      []DashboardAgentSeen `json:"agents"`
}

type DashboardWarning struct {
	Machine    string    `json:"machine"`
	Editor     string    `json:"editor"`
	SilentDays int       `json:"silent_days"`
	LastSeen   time.Time `json:"last_seen"`
	Message    string    `json:"message"`
}

type DashboardMachines struct {
	Now       time.Time          `json:"now"`
	LiveCount int                `json:"live_count"`
	Machines  []DashboardMachine `json:"machines"`
	Warnings  []DashboardWarning `json:"warnings"`
}

type DashboardProject struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"` // active, paused, done or empty
	Seconds      float64   `json:"seconds"`
	Tokens       int64     `json:"tokens"`
	Cost         float64   `json:"cost"`
	LastActive   time.Time `json:"last_active"`
	DailySeconds []float64 `json:"daily_seconds"` // one value per entry in DashboardProjects.Days
}

type DashboardProjects struct {
	Range        DashboardRange     `json:"range"`
	Days         []string           `json:"days"`
	Projects     []DashboardProject `json:"projects"`
	StatusSource string             `json:"status_source"` // "labels" or "mnemosyne"
}

// DashboardViewModel renders the /dashboard page shell; all data is loaded by the panels from /api/dashboard/*.
type DashboardViewModel struct {
	SharedLoggedInViewModel
	TimeZone string
}

func (s *DashboardViewModel) WithSuccess(m string) *DashboardViewModel {
	s.SetSuccess(m)
	return s
}

func (s *DashboardViewModel) WithError(m string) *DashboardViewModel {
	s.SetError(m)
	return s
}
