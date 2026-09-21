# Spec: 智能路由（Smart Routing / auto 模型）

Status: ready-for-agent
来源：grilling 会话 2026-09-21；决策记录见 `docs/adr/0001-smart-routing-external-decision-model.md`；术语见 `CONTEXT.md`。

## 问题

客户端调用网关时不想选模型。希望用虚拟模型名（默认 `auto`）发请求，网关按任务复杂度自动挑合适模型。

## 方案（已达成共识）

在 `middleware/distributor.go` 的 `Distribute()` 内、`getModelRequest()`（:48）之后、token 模型限制检查（:57）之前，加一步「若 `modelRequest.Model` == 虚拟名，则调 Jev 解析为真实模型」。改写点在渠道选择（:103）之前，下游选道/计费/重试/日志天然按真实模型运行，链路零侵入。

- 决策器：Jev（TypeSafe AI System One），`POST {base}/v1/systemone`，Bearer key。`state`=请求最后一条 user 消息（截断，上限约 2000 字符），`questions`=封闭问题「该用哪个模型」，答案集=候选池。采纳 top-1，不看置信分。
- 候选池：分组全部可用模型（`model/channel_cache.go` 的 group2model2channels 反查）∩ token 允许模型（`ContextKeyTokenModelLimit`）。
- 兜底：Jev 失败/超时/池空/答案越池 → 全局配置的兜底模型。请求绝不因路由器失败。
- 每轮独立路由，无会话状态。
- 计费按真实命中模型（`OriginModelName` 取解析后的值，Jev 调用记为网关自身消耗）；响应 `model` 回填真实模型（现有 converter 已如此，确认不泄漏 `auto`）；路由决策进用量日志。
- 开关：全局、默认关。隐私：用户内容发给第三方，须显式同意才开启。

## 范围边界

- 协议：仅 OpenAI chat completions 与 Anthropic messages。`model` 必填接口（embeddings/images/tasks/responses）用 `auto` 直接报错，提示不支持。
- 不做：粘性、置信阈值、成本上限、分组级开关。`pg/` playground 路径复用同一改写点（模型同为 `auto` 时行为一致），不单测。
- 配置项（option 表 + 管理页一个卡片）：`auto_routing.enabled`、虚拟名（默认 `auto`）、base URL、API key、兜底模型、超时 ms（默认 ~500）。

## 验证

- 单测：池计算（交集/越池）、兜底路径、改写点（Jev mock/httptest）。
- 手工：开/关开关各发一轮，确认日志、计费、响应 model。
