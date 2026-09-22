package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newSmartRoutingContext(body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func setupSmartRoutingTestDB(t *testing.T, abilities []model.Ability) {
	t.Helper()
	// model.InitDB initializes dialect column names (commonGroupCol etc.) the
	// same way controller/model_list_test.go does for cross-package relay code.
	originalSQLitePath := common.SQLitePath
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	common.SQLitePath = fmt.Sprintf("file:%s_init?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, model.InitDB())
	if sqlDB, err := model.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	common.SQLitePath = originalSQLitePath

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.Ability{}))
	for _, ability := range abilities {
		require.NoError(t, database.Create(&ability).Error)
	}
	previousDB := model.DB
	model.DB = database
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(originalMainType, originalLogType)
	})
}

func useSmartRoutingSettings(t *testing.T, settings operation_setting.AutoRoutingSetting) {
	t.Helper()
	current := operation_setting.GetAutoRoutingSetting()
	previous := *current
	t.Cleanup(func() { *current = previous })
	*current = settings
}

func mustSmartRoutingDecision(t *testing.T, c *gin.Context) smartRoutingDecision {
	t.Helper()
	value, ok := common.GetContextKey(c, constant.ContextKeySmartRoutingDecision)
	require.True(t, ok)
	decision, valid := value.(smartRoutingDecision)
	require.True(t, valid)
	return decision
}

func TestMatchSmartRoutingPoolAnswer(t *testing.T) {
	pool := []string{"gpt-5.1", "claude-sonnet-4.5", "gemini-3-pro"}

	assert.True(t, matchSmartRoutingPoolAnswer("claude-sonnet-4.5", pool))
	assert.False(t, matchSmartRoutingPoolAnswer("", pool))
	assert.False(t, matchSmartRoutingPoolAnswer("gpt-4.1", pool))
	// The closed choice set is exact: alias-normalized matches are rejected
	// so an out-of-pool answer falls back instead of silently widening scope.
	assert.False(t, matchSmartRoutingPoolAnswer("gpt-4-gizmo-g5-7", []string{"gpt-4-gizmo-*"}))
}

func TestSmartRoutingCandidatePool(t *testing.T) {
	setupSmartRoutingTestDB(t, []model.Ability{
		{Group: "default", Model: "gpt-5.1", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "claude-sonnet-4.5", ChannelId: 2, Enabled: true},
		{Group: "default", Model: "gpt-4.1", ChannelId: 3, Enabled: true},
		{Group: "other", Model: "not-for-this-group", ChannelId: 4, Enabled: true},
		{Group: "default", Model: "disabled-model", ChannelId: 5, Enabled: false},
	})

	c := newSmartRoutingContext(`{"model":"auto"}`)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	pool := smartRoutingCandidatePool(c)
	assert.ElementsMatch(t, []string{"gpt-5.1", "claude-sonnet-4.5", "gpt-4.1"}, pool)

	// Token allowlist narrows the pool; disabled restrictions restore it.
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-5.1": true})
	assert.Equal(t, []string{"gpt-5.1"}, smartRoutingCandidatePool(c))

	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, false)
	assert.Len(t, smartRoutingCandidatePool(c), 3)
}

func TestSmartRoutingStateExtraction(t *testing.T) {
	t.Run("string content returns last user message", func(t *testing.T) {
		body := `{"model":"auto","messages":[
			{"role":"system","content":"sys"},
			{"role":"user","content":"first"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"pick a model for this"}
		]}`
		assert.Equal(t, "pick a model for this", smartRoutingState(newSmartRoutingContext(body)))
	})

	t.Run("content parts array joins text parts", func(t *testing.T) {
		body := `{"model":"auto","messages":[
			{"role":"user","content":[
				{"type":"text","text":"line one"},
				{"type":"image_url","image_url":{"url":"x"}},
				{"type":"text","text":"line two"}
			]}
		]}`
		assert.Equal(t, "line one\nline two", smartRoutingState(newSmartRoutingContext(body)))
	})

	t.Run("truncated to max chars", func(t *testing.T) {
		long := strings.Repeat("a", operation_setting.AutoRoutingStateMaxChars+100)
		body := `{"model":"auto","messages":[{"role":"user","content":"` + long + `"}]}`
		assert.Len(t, smartRoutingState(newSmartRoutingContext(body)), operation_setting.AutoRoutingStateMaxChars)
	})

	t.Run("missing messages yields empty", func(t *testing.T) {
		assert.Equal(t, "", smartRoutingState(newSmartRoutingContext(`{"model":"auto"}`)))
	})
}

func TestRewriteRequestBodyModel(t *testing.T) {
	c := newSmartRoutingContext(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	require.NoError(t, rewriteRequestBodyModel(c, "gpt-5.1"))

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	data, err := storage.Bytes()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"model":"gpt-5.1"`)
	assert.NotContains(t, string(data), `"model":"auto"`)
	assert.Contains(t, string(data), `"messages"`)
	assert.Equal(t, len(data), int(c.Request.ContentLength))

	// Downstream reusable unmarshal observes the resolved model.
	var mr ModelRequest
	require.NoError(t, common.UnmarshalBodyReusable(c, &mr))
	assert.Equal(t, "gpt-5.1", mr.Model)
}

func TestJevCriteriaDescriptions(t *testing.T) {
	descriptions := map[string]string{"alias-x": "快速便宜款"}
	got := jevCriteriaWithDescriptions(descriptions, []string{"alias-x", "gpt-5.1"})
	assert.Equal(t, "alias-x: 快速便宜款", got["alias-x"])
	// Models without a registered description keep name-only criteria.
	assert.Equal(t, "gpt-5.1", got["gpt-5.1"])
}

func TestAskJevSystemOne(t *testing.T) {
	// askJevSystemOne enriches criteria from the pricing cache, which needs a DB.
	setupSmartRoutingTestDB(t, nil)
	t.Run("returns choice, version and usage", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			var req jevRequest
			require.NoError(t, common.DecodeJson(r.Body, &req))
			assert.Equal(t, jevRequestModel, req.Model)
			assert.Equal(t, "hello", req.State)
			question, ok := req.Questions["model"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "choice", question["type"])
			criteria, ok := question["criteria"].(map[string]any)
			require.True(t, ok)
			assert.Len(t, criteria, 2)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"model":{"type":"choice","choice":"gpt-5.1"}},"usage":{"input_tokens":296,"output_tokens":20}}`))
		}))
		defer server.Close()

		setting := &operation_setting.AutoRoutingSetting{BaseURL: server.URL, ApiKey: "sk-test", TimeoutMs: 1000}
		c := newSmartRoutingContext(`{"model":"auto"}`)
		answer, jevModel, usage, err := askJevSystemOne(c, setting, []string{"gpt-5.1", "claude-sonnet-4.5"}, "hello")
		require.NoError(t, err)
		assert.Equal(t, "gpt-5.1", answer)
		assert.Equal(t, "jev-1.13.0", jevModel)
		assert.Equal(t, 296, usage.InputTokens)
		assert.Equal(t, "Bearer sk-test", gotAuth)
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`invalid api key`))
		}))
		defer server.Close()

		setting := &operation_setting.AutoRoutingSetting{BaseURL: server.URL, ApiKey: "bad", TimeoutMs: 1000}
		_, _, _, err := askJevSystemOne(newSmartRoutingContext(`{"model":"auto"}`), setting, []string{"m"}, "hi")
		require.ErrorContains(t, err, "401")
	})

	t.Run("unresponsive classifier exceeds timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer server.Close() // runs after the release close below (LIFO)
		defer close(release)

		setting := &operation_setting.AutoRoutingSetting{BaseURL: server.URL, ApiKey: "k", TimeoutMs: 10}
		c := newSmartRoutingContext(`{"model":"auto"}`)
		_, _, _, err := askJevSystemOne(c, setting, []string{"m"}, "hi")
		require.Error(t, err)
	})
}

func TestResolveSmartRoutingModel(t *testing.T) {
	abilities := []model.Ability{
		{Group: "default", Model: "gpt-5.1", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "claude-sonnet-4.5", ChannelId: 2, Enabled: true},
	}
	body := `{"model":"auto","messages":[{"role":"user","content":"hi"}]}`

	t.Run("classifier answer rewrites request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"model":{"type":"choice","choice":"claude-sonnet-4.5"}},"usage":{"input_tokens":42}}`))
		}))
		defer server.Close()
		setupSmartRoutingTestDB(t, abilities)
		useSmartRoutingSettings(t, operation_setting.AutoRoutingSetting{
			Enabled: true, VirtualModel: "auto", BaseURL: server.URL, ApiKey: "k", FallbackModel: "gpt-5.1", TimeoutMs: 1000,
		})

		c := newSmartRoutingContext(body)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		mr := &ModelRequest{Model: "auto"}
		require.NoError(t, resolveSmartRoutingModel(c, mr))
		assert.Equal(t, "claude-sonnet-4.5", mr.Model)
		decision := mustSmartRoutingDecision(t, c)
		assert.Equal(t, "jev", decision.ChosenBy)
		assert.Equal(t, 42, decision.JevUsage.InputTokens)
	})

	t.Run("answer outside pool falls back", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"model":"jev-x","answers":{"model":{"type":"choice","choice":"nonexistent-model"}}}`))
		}))
		defer server.Close()
		setupSmartRoutingTestDB(t, abilities)
		useSmartRoutingSettings(t, operation_setting.AutoRoutingSetting{
			Enabled: true, VirtualModel: "auto", BaseURL: server.URL, ApiKey: "k", FallbackModel: "gpt-5.1", TimeoutMs: 1000,
		})

		c := newSmartRoutingContext(body)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		mr := &ModelRequest{Model: "auto"}
		require.NoError(t, resolveSmartRoutingModel(c, mr))
		assert.Equal(t, "gpt-5.1", mr.Model)
		decision := mustSmartRoutingDecision(t, c)
		assert.Equal(t, "fallback", decision.ChosenBy)
		assert.Contains(t, decision.Reason, "not in pool")
	})

	t.Run("classifier outage falls back", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		setupSmartRoutingTestDB(t, abilities)
		useSmartRoutingSettings(t, operation_setting.AutoRoutingSetting{
			Enabled: true, VirtualModel: "auto", BaseURL: server.URL, ApiKey: "k", FallbackModel: "claude-sonnet-4.5", TimeoutMs: 1000,
		})

		c := newSmartRoutingContext(body)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		mr := &ModelRequest{Model: "auto"}
		require.NoError(t, resolveSmartRoutingModel(c, mr))
		assert.Equal(t, "claude-sonnet-4.5", mr.Model)
		assert.Equal(t, "fallback", mustSmartRoutingDecision(t, c).ChosenBy)
	})

	t.Run("fallback must satisfy the token allowlist", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		setupSmartRoutingTestDB(t, abilities)
		useSmartRoutingSettings(t, operation_setting.AutoRoutingSetting{
			Enabled: true, VirtualModel: "auto", BaseURL: server.URL, ApiKey: "k", FallbackModel: "gpt-5.1", TimeoutMs: 1000,
		})

		c := newSmartRoutingContext(body)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"claude-sonnet-4.5": true})
		mr := &ModelRequest{Model: "auto"}
		err := resolveSmartRoutingModel(c, mr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fallback model gpt-5.1 is not usable")
		assert.Equal(t, "failed", mustSmartRoutingDecision(t, c).ChosenBy)
	})

	t.Run("empty pool without fallback errors", func(t *testing.T) {
		setupSmartRoutingTestDB(t, nil)
		useSmartRoutingSettings(t, operation_setting.AutoRoutingSetting{
			Enabled: true, VirtualModel: "auto", BaseURL: "http://127.0.0.1:1", ApiKey: "k", TimeoutMs: 1000,
		})

		c := newSmartRoutingContext(body)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "empty-group")
		mr := &ModelRequest{Model: "auto"}
		err := resolveSmartRoutingModel(c, mr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty_pool")
		assert.Equal(t, "failed", mustSmartRoutingDecision(t, c).ChosenBy)
	})
}
