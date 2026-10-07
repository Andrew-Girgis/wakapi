package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	conf "github.com/muety/wakapi/config"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models/view"
	"github.com/muety/wakapi/services"
)

// DashboardHandler serves the /dashboard page shell. Its panels load their data from /api/dashboard/*.
type DashboardHandler struct {
	config   *conf.Config
	userSrvc services.IUserService
}

func NewDashboardHandler(userService services.IUserService) *DashboardHandler {
	return &DashboardHandler{config: conf.Get(), userSrvc: userService}
}

func (h *DashboardHandler) RegisterRoutes(router chi.Router) {
	r := chi.NewRouter()
	r.Use(middlewares.NewWebAuthenticateMiddleware(h.userSrvc).
		WithRedirectTarget(defaultErrorRedirectTarget()).
		WithRedirectErrorMessage("unauthorized").Handler,
	)
	r.Get("/", h.GetIndex)

	router.Mount("/dashboard", r)
}

func (h *DashboardHandler) GetIndex(w http.ResponseWriter, r *http.Request) {
	if h.config.IsDev() {
		loadTemplates()
	}
	user := middlewares.GetPrincipal(r)
	vm := &view.DashboardViewModel{
		SharedLoggedInViewModel: view.SharedLoggedInViewModel{
			SharedViewModel: view.NewSharedViewModel(h.config, nil),
			User:            user,
		},
		TimeZone: user.TZ().String(),
	}
	if err := templates[conf.DashboardTemplate].Execute(w, vm); err != nil {
		conf.Log().Request(r).Error("failed to render dashboard page", "error", err)
	}
}
