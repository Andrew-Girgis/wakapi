package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/mocks"
	"github.com/muety/wakapi/models"
	routeutils "github.com/muety/wakapi/routes/utils"
	"github.com/muety/wakapi/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func dashboardRequest(target string, user *models.User) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), config.KeySharedData, config.NewSharedData()))
	routeutils.SetPrincipal(req, user)
	return req
}

func newDashboardTestHandler() (*DashboardApiHandler, *mocks.HeartbeatServiceMock) {
	config.Set(config.Empty())
	heartbeats := new(mocks.HeartbeatServiceMock)
	aliases := new(mocks.AliasServiceMock)
	aliases.On("GetAliasOrDefault", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	labels := new(mocks.ProjectLabelServiceMock)
	labels.On("GetByUserGrouped", mock.Anything).Return(map[string][]*models.ProjectLabel{}, nil)
	heartbeats.On("GetFirstByUser", mock.Anything).Return(time.Time{}, nil)
	srv := services.NewDashboardService(heartbeats, aliases, labels, services.NewPricingServiceWith(nil))
	return NewDashboardApiHandler(nil, srv), heartbeats
}

func TestDashboardApi_DefaultRangeIsLast7Days(t *testing.T) {
	handler, heartbeats := newDashboardTestHandler()
	user := &models.User{ID: "testuser", Location: "America/Toronto"}

	var gotFrom, gotTo time.Time
	heartbeats.On("GetAllWithin", mock.Anything, mock.Anything, user).Run(func(args mock.Arguments) {
		if gotFrom.IsZero() {
			gotFrom, gotTo = args.Get(0).(time.Time), args.Get(1).(time.Time)
		}
	}).Return([]*models.Heartbeat{}, nil)

	rec := httptest.NewRecorder()
	handler.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return handler.dashboardSrvc.Overview(u, from, to, f)
	})(rec, dashboardRequest("/api/dashboard/overview", user))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, 7*24, gotTo.Sub(gotFrom).Hours(), 1)
	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "time")
	assert.Contains(t, body, "tokens")
	assert.Contains(t, body, "cost")
}

func TestDashboardApi_FiltersAndBadRange(t *testing.T) {
	handler, _ := newDashboardTestHandler()
	user := &models.User{ID: "testuser"}

	var got services.DashboardFilters
	h := handler.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		got = f
		return map[string]string{}, nil
	})

	rec := httptest.NewRecorder()
	h(rec, dashboardRequest("/api/dashboard/projects?interval=today&project=wakapi&machine=Pythia", user))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, services.DashboardFilters{Project: "wakapi", Machine: "Pythia"}, got)

	rec = httptest.NewRecorder()
	h(rec, dashboardRequest("/api/dashboard/projects?from=not-a-date", user))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardApi_Timeline(t *testing.T) {
	handler, heartbeats := newDashboardTestHandler()
	user := &models.User{ID: "testuser", Location: "America/Toronto"}
	heartbeats.On("GetAllWithin", mock.Anything, mock.Anything, user).Return([]*models.Heartbeat{}, nil)

	rec := httptest.NewRecorder()
	handler.GetTimeline(rec, dashboardRequest("/api/dashboard/timeline?date=2026-10-07", user))
	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "2026-10-07", body["date"])

	rec = httptest.NewRecorder()
	handler.GetTimeline(rec, dashboardRequest("/api/dashboard/timeline?date=07/10/2026", user))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardApi_Machines(t *testing.T) {
	handler, heartbeats := newDashboardTestHandler()
	user := &models.User{ID: "testuser"}
	heartbeats.On("GetAllWithin", mock.Anything, mock.Anything, user).Return([]*models.Heartbeat{}, nil)

	rec := httptest.NewRecorder()
	handler.GetMachines(rec, dashboardRequest("/api/dashboard/machines", user))
	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(0), body["live_count"])
	assert.Equal(t, []any{}, body["machines"])
}

func TestDashboardApi_ServiceErrorIs500(t *testing.T) {
	handler, _ := newDashboardTestHandler()
	rec := httptest.NewRecorder()
	handler.respond(rec, dashboardRequest("/api/dashboard/overview", &models.User{ID: "x"}))(nil, assert.AnError)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), assert.AnError.Error()) // internal errors are not leaked
}

func TestLastDays(t *testing.T) {
	tz, _ := time.LoadLocation("America/Toronto")
	now := time.Date(2026, 10, 7, 2, 30, 0, 0, tz)
	from, to := LastDays(now, 7, tz)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, tz), from)
	assert.Equal(t, time.Date(2026, 10, 8, 0, 0, 0, 0, tz), to)
}

func TestDashboardApi_DaysParameter(t *testing.T) {
	handler, _ := newDashboardTestHandler()
	user := &models.User{ID: "testuser", Location: "America/Toronto"}
	var gotFrom, gotTo time.Time
	h := handler.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		gotFrom, gotTo = from, to
		return map[string]string{}, nil
	})

	rec := httptest.NewRecorder()
	h(rec, dashboardRequest("/api/dashboard/activity?days=7", user))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, gotFrom.In(user.TZ()).Hour())
	assert.Equal(t, 7, int(gotTo.Sub(gotFrom).Hours()/24+0.5))

	rec = httptest.NewRecorder()
	h(rec, dashboardRequest("/api/dashboard/activity?days=0", user))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
