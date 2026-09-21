# 02 改写点：Distribute 前置解析 auto

Status: ready-for-agent
Blocked by: 01

## 内容

`middleware/distributor.go` `Distribute()` 内 `getModelRequest()`（:48）之后、token 限制检查（:57）之前：

1. 开关关 或 `modelRequest.Model` ≠ 虚拟名 → 直接返回，零开销路径。
2. 计算候选池：分组可用模型（`model/channel_cache.go`）∩ token 允许模型（`ContextKeyTokenModelLimit`，若启用）。池 = 候选 ∩ 允许；交集空 → 兜底模型。
3. 取请求最后一条 user 消息（body 已解析为可复用；上限约 2000 字符）。调 `service.ResolveModel`，把结果写回 `modelRequest.Model` 与 gin context（请求体内存副本），使渠道选择、token 限制、计费 `OriginModelName`、retry 全部按真实模型走。
4. 非 chat 协议路径（embeddings/images/tasks/responses）收到虚拟名 → 400，提示智能路由不支持该接口。

## 验收

- 表驱动单测：开/关、交集、越池兜底、池空兜底、非 chat 拒绝。
- 端到端：mock Jev（httptest）+ 真实分组渠道，`model:"auto"` 请求成功且响应 `model`=真实模型。
