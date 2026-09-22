package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 智能路由：客户端以虚拟模型名（默认 auto）调用时，把请求最后一条 user
// 消息发给外部决策模型（Jev / TypeSafe AI System One），答案集即候选池内
// 的模型名。改写成功后下游选道、计费、重试全部按真实模型运行；任何决策
// 失败退回兜底模型，请求不因路由器不可用而失败。

const (
	jevSystemOnePath    = "/v1/systemone"
	jevRequestModel     = "jev-latest"
	jevResponseMaxBytes = 1 << 20
)

var jevHTTPClient = &http.Client{}

type smartRoutingDecision struct {
	PoolSize  int      `json:"pool_size"`
	ChosenBy  string   `json:"chosen_by"`
	Reason    string   `json:"reason,omitempty"`
	LatencyMs int64    `json:"latency_ms,omitempty"`
	JevModel  string   `json:"jev_model,omitempty"`
	JevUsage  jevUsage `json:"jev_usage,omitempty"`
}

// jevUsage is the classifier call's own token cost, recorded in the admin log
// as gateway-side consumption (the classifier is not billed to the user).
type jevUsage struct {
	InputTokens int `json:"input_tokens"`
}

// resolveSmartRoutingModel rewrites modelRequest.Model from the virtual name
// to a concrete model, keeping the cached request body in sync.
func resolveSmartRoutingModel(c *gin.Context, modelRequest *ModelRequest) error {
	setting := operation_setting.GetAutoRoutingSetting()
	pool := smartRoutingCandidatePool(c)
	decision := smartRoutingDecision{PoolSize: len(pool)}

	chosen := ""
	switch {
	case len(pool) == 0:
		decision.Reason = "empty_pool"
	case setting.ApiKey == "":
		decision.Reason = "missing_api_key"
	default:
		start := time.Now()
		answer, jevModel, usage, err := askJevSystemOne(c, setting, pool, smartRoutingState(c))
		decision.LatencyMs = time.Since(start).Milliseconds()
		decision.JevUsage = usage
		switch {
		case err != nil:
			decision.Reason = err.Error()
		default:
			if matchSmartRoutingPoolAnswer(answer, pool) {
				chosen = answer
				decision.JevModel = jevModel
			} else {
				decision.Reason = fmt.Sprintf("answer %q not in pool", answer)
			}
		}
	}

	if chosen == "" {
		// The fallback must satisfy the same boundary as the pool: an operator
		// misconfiguration surfaces as an explicit error instead of a downstream
		// 403, so a healthy classifier answer is never silently blocked.
		if setting.FallbackModel == "" {
			decision.ChosenBy = "failed"
			common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, decision)
			return fmt.Errorf("smart routing failed (%s) and no fallback model is configured", decision.Reason)
		}
		if !slices.Contains(pool, setting.FallbackModel) && !smartRoutingTokenAllows(c, setting.FallbackModel) {
			decision.ChosenBy = "failed"
			decision.Reason += "; fallback model " + setting.FallbackModel + " is not usable for this request"
			common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, decision)
			return fmt.Errorf("%s", decision.Reason)
		}
		chosen = setting.FallbackModel
		decision.ChosenBy = "fallback"
	} else {
		decision.ChosenBy = "jev"
	}
	common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, decision)

	if err := rewriteRequestBodyModel(c, chosen); err != nil {
		return err
	}
	modelRequest.Model = chosen
	return nil
}

// smartRoutingTokenAllows reports whether the token allowlist admits a
// fallback model when a pool membership check is impossible (empty pool).
func smartRoutingTokenAllows(c *gin.Context, modelName string) bool {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return false
	}
	limit, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
	tokenModelLimit, valid := limit.(map[string]bool)
	return ok && valid && TokenModelLimitAllows(tokenModelLimit, modelName)
}

// smartRoutingCandidatePool is the set of models the classifier may choose:
// enabled models of the request group intersected with the token allowlist.
func smartRoutingCandidatePool(c *gin.Context) []string {
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	groupModels := service.GetGroupsEnabledModels([]string{usingGroup})
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return groupModels
	}
	limit, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
	tokenModelLimit, valid := limit.(map[string]bool)
	if !ok || !valid {
		return []string{}
	}
	pool := make([]string, 0, len(groupModels))
	for _, modelName := range groupModels {
		if TokenModelLimitAllows(tokenModelLimit, modelName) {
			pool = append(pool, modelName)
		}
	}
	return pool
}

// smartRoutingState extracts the latest user message text (string or
// content-part array, OpenAI and Anthropic shapes) truncated for the
// classifier. Only this excerpt leaves the gateway.
func smartRoutingState(c *gin.Context) string {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return ""
	}
	body, err := storage.Bytes()
	if err != nil {
		return ""
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	var text string
	items := messages.Array()
	for i := len(items) - 1; i >= 0; i-- {
		message := items[i]
		if message.Get("role").String() != "user" {
			continue
		}
		content := message.Get("content")
		switch {
		case content.Type == gjson.String:
			text = content.String()
		case content.IsArray():
			parts := make([]string, 0)
			for _, part := range content.Array() {
				if part.Get("type").String() == "text" {
					parts = append(parts, part.Get("text").String())
				}
			}
			text = strings.Join(parts, "\n")
		}
		break
	}
	if utf8.RuneCountInString(text) > operation_setting.AutoRoutingStateMaxChars {
		text = string([]rune(text)[:operation_setting.AutoRoutingStateMaxChars])
	}
	return text
}

type jevRequest struct {
	Model     string         `json:"model"`
	State     string         `json:"state"`
	Questions map[string]any `json:"questions"`
}

type jevResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type   string `json:"type"`
		Choice string `json:"choice"`
	} `json:"answers"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// jevCriteria builds the closed choice options: each pool model name, enriched
// with the administrator's model description when present so the classifier can
// judge unfamiliar names by their stated strengths instead of guessing.
func jevCriteria(pool []string) map[string]string {
	descriptions := map[string]string{}
	for _, pricing := range model.GetPricing() {
		if pricing.Description != "" {
			descriptions[pricing.ModelName] = pricing.Description
		}
	}
	return jevCriteriaWithDescriptions(descriptions, pool)
}

func jevCriteriaWithDescriptions(descriptions map[string]string, pool []string) map[string]string {
	criteria := make(map[string]string, len(pool))
	for _, modelName := range pool {
		hint := modelName
		if desc, ok := descriptions[modelName]; ok {
			hint = modelName + ": " + desc
		}
		criteria[modelName] = hint
	}
	return criteria
}

// askJevSystemOne poses one closed choice question whose criteria keys are
// exactly the candidate models, and returns the chosen answer, the concrete
// classifier version that answered, and the call's own input-token usage.
func askJevSystemOne(c *gin.Context, setting *operation_setting.AutoRoutingSetting, pool []string, state string) (string, string, jevUsage, error) {
	payload, err := common.Marshal(jevRequest{
		Model: jevRequestModel,
		State: state,
		Questions: map[string]any{
			"model": map[string]any{"type": "choice", "criteria": jevCriteria(pool)},
		},
	})
	if err != nil {
		return "", "", jevUsage{}, err
	}
	requestContext, cancel := context.WithTimeout(c.Request.Context(), time.Duration(setting.Timeout())*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, strings.TrimRight(setting.BaseURL, "/")+jevSystemOnePath, bytes.NewReader(payload))
	if err != nil {
		return "", "", jevUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+setting.ApiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := jevHTTPClient.Do(req)
	if err != nil {
		return "", "", jevUsage{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, jevResponseMaxBytes))
	if err != nil {
		return "", "", jevUsage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", jevUsage{}, fmt.Errorf("jev status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed jevResponse
	if err := common.Unmarshal(responseBody, &parsed); err != nil {
		return "", "", jevUsage{}, err
	}
	return parsed.Answers["model"].Choice, parsed.Model, jevUsage{InputTokens: parsed.Usage.InputTokens}, nil
}

// matchSmartRoutingPoolAnswer accepts the classifier answer only when it is
// exactly one of the pool model names sent as the closed choice set.
func matchSmartRoutingPoolAnswer(answer string, pool []string) bool {
	return answer != "" && slices.Contains(pool, answer)
}

// rewriteRequestBodyModel replaces the top-level model field in the cached
// request body so the upstream request, validation and billing all observe
// the resolved model instead of the virtual name.
func rewriteRequestBodyModel(c *gin.Context, resolved string) error {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	body, err := storage.Bytes()
	if err != nil {
		return err
	}
	newBody, err := sjson.SetBytes(body, "model", resolved)
	if err != nil {
		return err
	}
	newStorage, err := common.CreateBodyStorage(newBody)
	if err != nil {
		return err
	}
	_ = storage.Close()
	c.Set(common.KeyBodyStorage, newStorage)
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(newBody))
	c.Request.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
	return nil
}
