package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/muety/wakapi/config"
	"github.com/stretchr/testify/assert"
)

func TestSummaryHandler_GetRedirect(t *testing.T) {
	cfg := config.Empty()
	h := &SummaryHandler{config: cfg}

	for _, tc := range []struct{ basePath, target, want string }{
		{"", "/summary?interval=any", "/dashboard?interval=any"},
		{"/", "/summary?interval=today&project=wakapi", "/dashboard?interval=today&project=wakapi"},
		{"", "/summary", "/dashboard"},
		{"/wakapi", "/wakapi/summary?from=2026-10-01&to=2026-10-07", "/wakapi/dashboard?from=2026-10-01&to=2026-10-07"},
	} {
		cfg.Server.BasePath = tc.basePath
		rec := httptest.NewRecorder()
		h.GetRedirect(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		assert.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, tc.want, rec.Header().Get("Location"))
	}
}
