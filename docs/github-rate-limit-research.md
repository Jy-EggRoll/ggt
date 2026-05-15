# GitHub Git 操作限制与账号安全调研报告

> 调研时间：2026-05-15
> 调研目的：确保 git fetch/pull 操作不触发账号封禁，同时最大限度提高效率

---

## 一、概述

本报告调研了 GitHub 对 git fetch/pull 操作的并发数、数据吞吐量限制，以及导致账号被封（suspended/banned）的条件和最佳安全实践。

**核心结论**：

- GitHub 对 git 操作**无明确公开的并发数限制数值**
- 官方有 API 并发 ≤100 的明确限制
- 过度批量活动可能导致账号封禁
- 正常使用方法无需担心

---

## 二、并发连接数限制

### 2.1 API 并发限制（官方明确）

根据 GitHub 官方 REST API 文档，**Secondary Rate Limits** 规定：

| 限制类型         | 具体数值                    | 说明                               |
| ---------------- | --------------------------- | ---------------------------------- |
| **并发请求数**   | **≤100 个**                 | REST API 和 GraphQL API 共享此限制 |
| REST API 点数    | 900 points/分钟             |                                    |
| GraphQL API 点数 | 2,000 points/分钟           |                                    |
| CPU 时间         | 60 秒真实时间最多 90 秒 CPU |                                    |
| 内容创建请求     | 80 个/分钟，500 个/小时     |                                    |

> 原文：_"No more than 100 concurrent requests are allowed. This limit is shared across the REST API and GraphQL API."_

### 2.2 认证后的 Rate Limits

| 认证方式                             | 请求限制              |
| ------------------------------------ | --------------------- |
| 未认证（公开数据）                   | 60 requests/hour      |
| 个人访问令牌（PAT）                  | 5,000 requests/hour   |
| GitHub Enterprise + GitHub App/OAuth | 15,000 requests/hour  |
| GitHub LFS（认证）                   | 3,000 requests/minute |

### 2.3 Git Clone/Fetch 的限制

**重要说明**：GitHub **没有专门针对 git clone/fetch 的独立并发连接数官方文档说明**。上述 100 个并发限制是针对 API 请求的。对于纯 git 协议操作，GitHub 会将其纳入整体服务监控。

---

## 三、数据吞吐量限制

### 3.1 带宽限制（官方政策）

根据 GitHub Acceptable Use Policies 第 9 节 **"Excessive Bandwidth Use"**：

> _"If we determine your bandwidth usage to be significantly excessive in relation to other users of similar features, we reserve the right to suspend your Account, throttle your file hosting, or otherwise limit your activity."_

**关键点**：

- GitHub **没有公布具体数值**（如 "每分钟 X GB"）
- 采取 **"相对于同类用户的相对限制"** 策略
- 超出限制可能导致：暂停账号、限制文件托管、限制活动

### 3.2 Git LFS 限制

| 类型     | 限制                                  |
| -------- | ------------------------------------- |
| 未认证   | 300 requests/minute                   |
| 认证     | 3,000 requests/minute                 |
| 批量处理 | 默认每个 API 请求处理 100 个 LFS 对象 |

### 3.3 Enterprise 版本

| 版本                     | 限制情况                             |
| ------------------------ | ------------------------------------ |
| GitHub Enterprise Server | 部署在用户自己基础设施上，无内置限制 |
| GitHub Enterprise Cloud  | 标准 SaaS 限制（与上面相同）         |

---

## 四、封号/ban 触发条件

### 4.1 官方明确禁止的行为

根据 **GitHub Acceptable Use Policies**，以下行为会导致封号：

| 禁止行为                     | 说明                                |
| ---------------------------- | ----------------------------------- |
| 自动化过度批量活动           | "automated excessive bulk activity" |
| 协调性虚假活动               | "coordinated inauthentic activity"  |
| 垃圾信息                     | spam                                |
| 加密货币挖矿                 | cryptocurrency mining               |
| 未经授权的产品许可证密钥分享 |                                     |
| 冒充他人                     |                                     |
| 侵犯隐私                     |                                     |

### 4.2 过度并发是否会导致封号？

**根据官方文档**：

- 超过 rate limit 会收到 `403` 或 `429` 响应
- 在被限制期间**继续请求**可能导致封号
- 官方原文：_"Continuing to make requests while you are rate limited may result in the banning of your integration."_

**这不是自动封号，而是针对持续滥用的惩罚**。

### 4.3 封号前是否有警告？

根据官方文档：

- 首先会收到 `429` 或 `403` 错误响应
- 响应包含 `retry-after` 或 `x-ratelimit-reset` 头信息
- **没有明确说明会有预先人工警告**

---

## 五、安全并发建议

### 5.1 官方推荐做法

根据 Best Practices for Using the REST API：

> _"To avoid exceeding secondary rate limits, you should make requests serially instead of concurrently."_

官方建议：

1. **使用认证请求**（5,000 vs 60 请求/小时）
2. **串行执行而非并发**
3. **mutative 请求之间停顿至少 1 秒**
4. **收到限流错误后等待**：
    - 有 `retry-after` 头：等待指定秒数
    - 有 `x-ratelimit-reset`：等待到该时间戳
    - 否则至少等待 1 分钟
5. **使用指数退避策略**

### 5.2 推荐的并发数上限

基于官方文档和社区经验：

| 场景            | 建议最大并发                   |
| --------------- | ------------------------------ |
| API 请求        | **< 50**（官方限制 100）       |
| git clone/fetch | **< 5-10**（避免触发异常监控） |
| 自动化脚本      | **< 10**，配合重试机制         |

### 5.3 分场景建议

| 场景       | 建议                                                          |
| ---------- | ------------------------------------------------------------- |
| 日常使用   | 无需特别担心，正常的 fetch/pull 不会被限制                    |
| 自动化脚本 | 使用认证令牌，避免短时间大量请求                              |
| CI/CD 场景 | 使用 GitHub Actions 的 `GITHUB_TOKEN`（1,000 请求/小时/仓库） |
| 并发需求   | 保持并发 < 10，间隔 > 1 秒                                    |
| 大量 clone | 考虑使用 GitHub Archive 或第三方镜像                          |

---

## 六、Gitea 自建平台对比

### 6.1 Gitea 的限制策略

根据 Gitea 官方文档，Gitea **默认不施加 GitHub 风格的 rate limits**。

### 6.2 GitHub vs Gitea 对比

| 特性         | GitHub.com             | Gitea（自建）    |
| ------------ | ---------------------- | ---------------- |
| 并发请求限制 | 100 个/共享            | **无内置限制**   |
| 速率限制     | 5,000 请求/小时        | **无内置限制**   |
| 带宽限制     | 相对限制（无具体数值） | **完全由您控制** |
| 账号封禁     | 可能（滥用/违规）      | **不会**         |
| API 限流     | 有                     | 无（可配置）     |

### 6.3 Gitea 结论

**优点**：

- 完全没有 GitHub 风格的 rate limits
- 可以自由配置服务器资源
- 不会因为"过度使用"被封号

**缺点**：

- 需要自行维护服务器
- 没有 GitHub 的全球 CDN 加速
- 需要自行处理 DDoS 防护
- 安全责任完全自负

---

## 七、总结与建议

### 7.1 核心结论

1. **正常使用**：GitHub 对正常使用者足够宽容，无需担心
2. **自动化场景**：
    - 使用认证令牌
    - 并发数 ≤ 10
    - 请求间隔 ≥ 1 秒
    - 实现重试机制和指数退避
3. **触发限流**：收到 `429` 后等待 `retry-after` 时间，不要持续重试

### 7.2 如果需要无限制使用

| 需求        | 推荐方案                             |
| ----------- | ------------------------------------ |
| 小团队/个人 | Gitea（开源，自建）                  |
| 企业级需求  | GitHub Enterprise Server（自己部署） |
| 最高控制    | 自建 GitLab/Gitea                    |

---

## 参考来源

1. [GitHub REST API Rate Limits](https://docs.github.com/en/rest/overview/rate-limits-for-the-rest-api)
2. [GitHub Acceptable Use Policies](https://docs.github.com/en/site-policy/acceptable-use-policies/github-acceptable-use-policies)
3. [Best practices for using the REST API](https://docs.github.com/en/rest/guides/best-practices-for-using-the-rest-api)
4. [GitHub Terms for Additional Products and Features](https://docs.github.com/en/site-policy/github-terms/github-terms-for-additional-products-and-features)
5. [Gitea Configuration Cheat Sheet](https://docs.gitea.com/administration/config-cheat-sheet)
6. [Gitea vs Other Git Hosting Comparison](https://docs.gitea.com/installation/comparison)
