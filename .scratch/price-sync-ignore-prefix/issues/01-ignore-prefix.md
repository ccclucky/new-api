# 01 价格同步忽略供应商前缀

Status: resolved
来源：用户诉求「加一个是否忽略前缀的开关」；背景：models.dev/OpenRouter 价格键带 `provider/` 前缀，本地裸名（glm-5.2、MiniMax-M2.5）同步时对不上。

## 内容

1. 「同步模型定价」弹窗加「忽略供应商前缀」勾选（默认关），请求带 `ignore_prefix`。
2. 后端 `FetchUpstreamRatios`：勾选时以上游数据里含 `/` 的键做别名——按「最后一段小写」规范化，映射到网关自身模型名（`model.GetEnabledModels()`）；本地精确名优先、冲突取字母序第一；不改写本地数据。

## 验收

- 上游 `z-ai/GLM-5.2` 在本地 `glm-5.2` 上出差异并可应用；`minimax/minimax-m2.5` 命中 `MiniMax-M2.5`
- 不开开关行为与之前完全一致
- 单测：`TestAddVendorPrefixAliases`、前端 `sends ignore_prefix when checked`
