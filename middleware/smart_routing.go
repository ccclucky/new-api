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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

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
	PoolSize  int    `json:"pool_size"`
	ChosenBy  string `json:"chosen_by"`
	Reason    string `json:"reason,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	JevModel  string `json:"jev_model,omitempty"`
}

// resolveSmartRoutingModel rewrites modelRequest.Model from the virtual name
// to a concrete model, keeping the cached request body in sync.
func resolveSmartRoutingModel(c *gin.Context, modelRequest *ModelRequest) error {
	setting := operation_setting.GetSmartRoutingSetting()
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
		answer, jevModel, err := askJevSystemOne(c, setting, pool, smartRoutingState(c))
		decision.LatencyMs = time.Since(start).Milliseconds()
		switch {
		case err != nil:
			decision.Reason = err.Error()
		default:
			if matched, ok := matchSmartRoutingAnswer(answer, pool); ok {
				chosen = matched
				decision.JevModel = jevModel
			} else {
				decision.Reason = fmt.Sprintf("answer %q not in pool", answer)
			}
		}
	}

	if chosen == "" {
		if setting.FallbackModel == "" {
			decision.ChosenBy = "failed"
			c.Set("smart_routing", decision)
			return fmt.Errorf("smart routing failed (%s) and no fallback model is configured", decision.Reason)
		}
		chosen = setting.FallbackModel
		decision.ChosenBy = "fallback"
	} else {
		decision.ChosenBy = "jev"
	}
	c.Set("smart_routing", decision)

	if err := rewriteRequestBodyModel(c, chosen); err != nil {
		return err
	}
	modelRequest.Model = chosen
	return nil
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
	if utf8.RuneCountInString(text) > operation_setting.SmartRoutingStateMaxChars {
		text = string([]rune(text)[:operation_setting.SmartRoutingStateMaxChars])
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
}

// askJevSystemOne poses one closed choice question whose criteria keys are
// exactly the candidate models, and returns the chosen answer plus the
// concrete classifier version that answered.
func askJevSystemOne(c *gin.Context, setting *operation_setting.SmartRoutingSetting, pool []string, state string) (string, string, error) {
	criteria := make(map[string]string, len(pool))
	for _, modelName := range pool {
		criteria[modelName] = modelName
	}
	payload, err := common.Marshal(jevRequest{
		Model: jevRequestModel,
		State: state,
		Questions: map[string]any{
			"model": map[string]any{"type": "choice", "criteria": criteria},
		},
	})
	if err != nil {
		return "", "", err
	}
	requestContext, cancel := context.WithTimeout(c.Request.Context(), time.Duration(setting.Timeout())*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, strings.TrimRight(setting.BaseURL, "/")+jevSystemOnePath, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+setting.ApiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := jevHTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, jevResponseMaxBytes))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("jev status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed jevResponse
	if err := common.Unmarshal(responseBody, &parsed); err != nil {
		return "", "", err
	}
	return parsed.Answers["model"].Choice, parsed.Model, nil
}

// matchSmartRoutingAnswer accepts the classifier answer when it names a pool
// model, tolerating routing-normalized aliases the way channel selection does.
func matchSmartRoutingAnswer(answer string, pool []string) (string, bool) {
	if answer == "" {
		return "", false
	}
	if slices.Contains(pool, answer) {
		return answer, true
	}
	normalizedAnswer := ratio_setting.RoutingMatchModelName(answer)
	for _, modelName := range pool {
		if ratio_setting.RoutingMatchModelName(modelName) == normalizedAnswer {
			return modelName, true
		}
	}
	return "", false
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
