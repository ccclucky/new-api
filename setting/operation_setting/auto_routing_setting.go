package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// AutoRoutingSetting 智能路由：客户端用虚拟模型名调用时，网关把请求内容
// 交给外部决策模型（Jev / TypeSafe AI System One）选择真实模型。默认关闭：
// 开启即会把用户内容发给第三方，需管理员显式同意。
type AutoRoutingSetting struct {
	Enabled       bool   `json:"enabled"`        // 总开关，默认关闭
	VirtualModel  string `json:"virtual_model"`  // 虚拟模型名，默认 auto
	BaseURL       string `json:"base_url"`       // 决策模型 API 地址
	ApiKey        string `json:"api_key"`        // 决策模型 API key
	FallbackModel string `json:"fallback_model"` // 决策失败或答案越池时的兜底模型
	TimeoutMs     int    `json:"timeout_ms"`     // 决策请求超时，毫秒
}

const (
	AutoRoutingDefaultVirtualModel = "auto"
	AutoRoutingDefaultTimeoutMs    = 500
	// AutoRoutingStateMaxChars 发送给决策模型的用户内容上限（rune 数）。
	AutoRoutingStateMaxChars = 2000
)

var autoRoutingSetting = AutoRoutingSetting{
	Enabled:      false,
	VirtualModel: AutoRoutingDefaultVirtualModel,
	BaseURL:      "https://api.typesafe.ai",
	ApiKey:       "",
	TimeoutMs:    AutoRoutingDefaultTimeoutMs,
}

func init() {
	config.GlobalConfig.Register("auto_routing_setting", &autoRoutingSetting)
}

func GetAutoRoutingSetting() *AutoRoutingSetting {
	return &autoRoutingSetting
}

// VirtualName 返回生效的虚拟模型名，管理员留空时回退默认值。
func (s *AutoRoutingSetting) VirtualName() string {
	if s.VirtualModel == "" {
		return AutoRoutingDefaultVirtualModel
	}
	return s.VirtualModel
}

// Timeout 返回生效的决策超时，非法值回退默认值。
func (s *AutoRoutingSetting) Timeout() int {
	if s.TimeoutMs <= 0 {
		return AutoRoutingDefaultTimeoutMs
	}
	return s.TimeoutMs
}
