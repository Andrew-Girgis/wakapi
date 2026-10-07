package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	conf "github.com/muety/wakapi/config"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/services"
)

// DashboardApiHandler serves one JSON endpoint per panel of the /dashboard page, so panels load and fail independently.
type DashboardApiHandler struct {
	config        *conf.Config
	userSrvc      services.IUserService
	dashboardSrvc *services.DashboardService
}

func NewDashboardApiHandler(userService services.IUserService, dashboardService *services.DashboardService) *DashboardApiHandler {
	return &DashboardApiHandler{config: conf.Get(), userSrvc: userService, dashboardSrvc: dashboardService}
}

func (h *DashboardApiHandler) RegisterRoutes(router chi.Router) {
	r := chi.NewRouter()
	r.Use(middlewares.NewApiAuthenticateMiddleware(h.userSrvc).Handler)
	r.Get("/overview", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Overview(u, from, to, f)
	}))
	r.Get("/activity", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Activity(u, from, to, f)
	}))
	r.Get("/tokens", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Tokens(u, from, to, f)
	}))
	r.Get("/cost", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Cost(u, from, to, f)
	}))
	r.Get("/sessions", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Sessions(u, from, to, f)
	}))
	r.Get("/projects", h.withRange(func(u *models.User, from, to time.Time, f services.DashboardFilters) (any, error) {
		return h.dashboardSrvc.Projects(u, from, to, f)
	}))
	r.Get("/timeline", h.GetTimeline)
	r.Get("/machines", h.GetMachines)

	router.Mount("/dashboard", r)
}

type rangeFunc func(*models.User, time.Time, time.Time, services.DashboardFilters) (any, error)

// withRange parses interval (default last_7_days) or from/to, plus the project and machine filters.
func (h *DashboardApiHandler) withRange(f rangeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("interval") == "" && q.Get("from") == "" && q.Get("start") == "" {
			q.Set("interval", (*models.IntervalPast7Days)[0])
			r.URL.RawQuery = q.Encode()
		}
		params, err := helpers.ParseSummaryParams(r)
		if err != nil {
			helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		h.respond(w, r)(f(params.User, params.From, params.To, filtersFrom(r)))
	}
}

// GetTimeline returns one day of activity; date=YYYY-MM-DD in the user's time zone (default today).
func (h *DashboardApiHandler) GetTimeline(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)
	date := time.Now().In(user.TZ())
	if d := r.URL.Query().Get("date"); d != "" {
		parsed, err := time.ParseInLocation("2006-01-02", d, user.TZ())
		if err != nil {
			helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid 'date' parameter, expected YYYY-MM-DD"})
			return
		}
		date = parsed
	}
	h.respond(w, r)(h.dashboardSrvc.Timeline(user, date, filtersFrom(r)))
}

func (h *DashboardApiHandler) GetMachines(w http.ResponseWriter, r *http.Request) {
	h.respond(w, r)(h.dashboardSrvc.Machines(middlewares.GetPrincipal(r), time.Now()))
}

func (h *DashboardApiHandler) respond(w http.ResponseWriter, r *http.Request) func(any, error) {
	return func(data any, err error) {
		if err != nil {
			conf.Log().Request(r).Error("failed to build dashboard panel", "path", r.URL.Path, "error", err)
			helpers.RespondJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to load data"})
			return
		}
		helpers.RespondJSON(w, r, http.StatusOK, data)
	}
}

func filtersFrom(r *http.Request) services.DashboardFilters {
	return services.DashboardFilters{Project: r.URL.Query().Get("project"), Machine: r.URL.Query().Get("machine")}
}
