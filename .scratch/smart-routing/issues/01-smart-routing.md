# 01 智能路由（auto 模型）完整实现

Status: ready-for-agent
详情见 `../spec.md`；决策见 `docs/adr/0001-smart-routing-external-decision-model.md`。

## 内容

1. 配置：`setting/` 新增 auto_routing option 包（enabled 默认 false、虚拟名默认 `auto`、base URL、API key、兜底模型、超时 ms 默认 500），注册进 `model/option.go`；管理页设置卡片 + i18n。
2. Jev 客户端（`service/` 新文件）：`ResolveModel(ctx, pool, lastUserMsg)` → `POST {base}/v1/systemone`，state=截断消息，questions=封闭模型选择；失败/超时/越池 → 兜底模型。base URL 可注入，便于 httptest。
3. 改写点：`middleware/distributor.go` `Distribute()` 内 `getModelRequest()`（:48）后、token 限制检查（:57）前——开关关或模型非虚拟名零开销返回；池=分组可用模型 ∩ token 允许模型；解析结果写回 `modelRequest.Model` 与请求体 context，下游选道/计费/重试天然按真实模型；非 chat 协议用虚拟名 → 400。
4. 可见性：功能开启时 `/v1/models` 与定价页展示虚拟名条目（标注智能路由，不编造价格）；用量日志记路由决策（所选模型、来源、耗时），复用现有扩展字段机制。

## 门禁

实现 3 前必读 `.agents/rules/billing.md`（触计费路径）。

## 验收

- 表驱动单测：开/关、交集、越池/超时/池空兜底、非 chat 拒绝。
- 端到端：mock Jev（httptest）+ 真实渠道，`model:"auto"` 成功且响应 `model`=真实模型、日志含决策。
- 手工：开关开/关各一轮；`GET /v1/models` 含/不含 `auto`。
