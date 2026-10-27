package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/mocks"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/services"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenUsageAuthenticatedRoutes(t *testing.T) {
	config.Set(config.Empty())
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/api.db"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.TokenUsageEvent{}, &models.TokenUsageSessionRef{},
		&models.TokenUsageQuarantinedSession{}, &models.TokenUsageAccountingMode{}, &models.TokenUsageConflict{}))
	users := new(mocks.UserServiceMock)
	user := &models.User{ID: "owner", ApiKey: "fixture-full-key"}
	users.On("GetUserByKey", "fixture-full-key", true).Return(user, nil)
	users.On("GetUserByKey", "fixture-full-key", false).Return(user, nil)
	users.On("GetUserByKey", "fixture-read-key", false).Return(user, nil)
	users.On("GetUserByKey", "fixture-read-key", true).Return((*models.User)(nil), errors.New("read-only key"))
	router := chi.NewRouter()
	NewTokenUsageApiHandler(users, services.NewTokenUsageService(db)).RegisterRoutes(router)
	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), config.KeySharedData, config.NewSharedData()))
		if key != "" {
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	at := time.Now().UnixMilli()
	e := models.TokenUsageEvent{Source: "codex", SourceKey: "s:fork:counter", SessionKey: "s",
		OccurredMs: at, Machine: "Pythia", Project: "fixture", Model: "test", InputTokens: 1,
		CacheReadTokens: 100, OutputTokens: 2, ParserVersion: 9, RawSHA256: strings.Repeat("a", 64), RawUsageJSON: `{}`}
	encoded, err := json.Marshal([]models.TokenUsageEvent{e})
	require.NoError(t, err)
	body := string(encoded)
	writePath := "/usage-write/events"
	readPath := "/usage/summary?from_ms=" + strconv.FormatInt(at-1, 10) + "&to_ms=" + strconv.FormatInt(at+1, 10)
	require.Equal(t, http.StatusUnauthorized, request("POST", writePath, "", body).Code)
	require.Equal(t, http.StatusUnauthorized, request("POST", writePath, "fixture-read-key", body).Code)
	for _, expected := range []string{"inserted", "unchanged"} {
		w := request("POST", writePath, "fixture-full-key", body)
		require.Equal(t, http.StatusOK, w.Code)
		var ack services.UsageIngestResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ack))
		if expected == "inserted" {
			require.Equal(t, 1, ack.Inserted)
		} else {
			require.Equal(t, 1, ack.Unchanged)
		}
	}
	w := request("GET", readPath, "fixture-read-key", "")
	require.Equal(t, http.StatusOK, w.Code)
	var summary services.UsageSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))
	require.Equal(t, int64(103), summary.TotalTokens)
	receiptBody, err := json.Marshal([]services.UsageReceipt{{Source: e.Source, SourceKey: e.SourceKey, RawSHA256: e.RawSHA256},
		{Source: e.Source, SourceKey: e.SourceKey, RawSHA256: strings.Repeat("f", 64)},
		{Source: e.Source, SourceKey: "absent", RawSHA256: e.RawSHA256}})
	require.NoError(t, err)
	check := request("POST", "/usage/receipts", "fixture-read-key", string(receiptBody))
	require.Equal(t, http.StatusOK, check.Code)
	var receipts services.UsageReceiptResult
	require.NoError(t, json.Unmarshal(check.Body.Bytes(), &receipts))
	require.Equal(t, services.UsageReceiptResult{Matched: 1, Missing: 1, Mismatched: 1}, receipts)
	require.Equal(t, http.StatusUnauthorized, request("POST", "/usage/receipts", "", string(receiptBody)).Code)
	require.Equal(t, http.StatusUnauthorized, request("GET", readPath, "", "").Code)
	require.Equal(t, http.StatusBadRequest, request("GET", "/usage/summary?from_ms=2&to_ms=1", "fixture-full-key", "").Code)
	for _, invalid := range []string{body + " {}", `[{"unrecognized":true}]`, `[]`, strings.Repeat(" ", 2<<20) + body} {
		require.Equal(t, http.StatusBadRequest, request("POST", writePath, "fixture-full-key", invalid).Code)
	}
	// Same session, different representation: return 409 and leave its total intact.
	e.SourceKey = "s:response:new-representation"
	e.RawSHA256 = strings.Repeat("b", 64)
	encoded, err = json.Marshal([]models.TokenUsageEvent{e})
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, request("POST", writePath, "fixture-full-key", string(encoded)).Code)
	w = request("GET", readPath, "fixture-full-key", "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))
	require.Equal(t, int64(103), summary.TotalTokens)
	var rows int64
	require.NoError(t, db.Model(&models.TokenUsageEvent{}).Where("user_id = ?", "owner").Count(&rows).Error)
	require.Equal(t, int64(1), rows)
}
