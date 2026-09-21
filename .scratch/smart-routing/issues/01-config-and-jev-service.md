# 01 全局配置与 Jev 决策服务

Status: ready-for-agent

## 内容

- `setting/` 新增 auto_routing 配置包（仿 `setting/payment_stripe.go` 的 option-map 模式）：enabled（默认 false）、虚拟模型名（默认 `auto`）、base URL、API key、兜底模型、超时 ms（默认 500）。注册进 `model/option.go`。
- `service/` 新增 Jev 客户端：`ResolveModel(ctx, pool []string, lastUserMsg string) (string, bool)` —— 构造 `POST {base}/v1/systemone` 请求（state=截断消息，questions=封闭模型选择），解析答案；超时/错误/答案不在池内 → 返回兜底模型。客户端可注入 base URL，便于 httptest。
- 管理页设置卡片（`web/`）：开关、URL、key、兜底模型、超时、虚拟名。i18n 按 `web/src/i18n` 规范。

## 验收

单测覆盖：池内答案采纳、越池兜底、超时兜底。管理页可保存生效。
