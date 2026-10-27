package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/services"
)

type TokenUsageApiHandler struct {
	users services.IUserService
	usage *services.TokenUsageService
}

func NewTokenUsageApiHandler(users services.IUserService, usage *services.TokenUsageService) *TokenUsageApiHandler {
	return &TokenUsageApiHandler{users: users, usage: usage}
}

func (h *TokenUsageApiHandler) RegisterRoutes(router chi.Router) {
	read := chi.NewRouter()
	read.Use(middlewares.NewApiAuthenticateMiddleware(h.users).Handler)
	read.Get("/summary", h.summary)
	read.Post("/receipts", h.receipts)
	router.Mount("/usage", read)

	write := chi.NewRouter()
	write.Use(middlewares.NewApiAuthenticateMiddleware(h.users).WithFullAccessOnly(true).Handler)
	write.Post("/events", h.events)
	router.Mount("/usage-write", write)
}

func (h *TokenUsageApiHandler) events(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var batch []models.TokenUsageEvent
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid usage batch"})
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "trailing JSON data"})
		return
	}
	result, err := h.usage.Ingest(middlewares.GetPrincipal(r).ID, batch)
	if err != nil {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid usage batch or database error"})
		return
	}
	if result.Conflicts > 0 {
		helpers.RespondJSON(w, r, http.StatusConflict, result)
		return
	}
	helpers.RespondJSON(w, r, http.StatusOK, result)
}

func (h *TokenUsageApiHandler) receipts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var batch []services.UsageReceipt
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&batch) != nil || decoder.Decode(&extra) != io.EOF {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid receipt batch"})
		return
	}
	result, err := h.usage.Receipts(middlewares.GetPrincipal(r).ID, batch)
	if err != nil {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid receipt batch or database error"})
		return
	}
	helpers.RespondJSON(w, r, http.StatusOK, result)
}

func (h *TokenUsageApiHandler) summary(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, errFrom := strconv.ParseInt(query.Get("from_ms"), 10, 64)
	to, errTo := strconv.ParseInt(query.Get("to_ms"), 10, 64)
	if errFrom != nil || errTo != nil || from < 0 || to <= from || to > time.Now().Add(24*time.Hour).UnixMilli() {
		helpers.RespondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid range"})
		return
	}
	out, err := h.usage.Summary(middlewares.GetPrincipal(r).ID, from, to, query.Get("machine"), query.Get("project"))
	if err != nil {
		helpers.RespondJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "usage summary unavailable"})
		return
	}
	helpers.RespondJSON(w, r, http.StatusOK, out)
}
