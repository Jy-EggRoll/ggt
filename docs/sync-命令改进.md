# `ggt sync` — pterm 深度融合改进方案

> 基于 `cmd/sync.go` 当前实现 + `docs/github-rate-limit-research.md` 中的安全并发建议

---

## 一、现状分析

当前 `syncRepo()` 的输出完全依赖 `InfoMsg` / `WarnMsg` / `ErrorMsg` / `SuccessMsg` 四个辅助函数，它们本质上只是 `pterm.Info/Warning/Error/Success.Println` 的别名：

```go
WarnMsg("[repoA] 本地有未提交的更改，必须手动处理")
SuccessMsg("[repoA] 拉取成功")
```

### 存在的问题

| 问题             | 说明                                             |
| ---------------- | ------------------------------------------------ |
| **信息密度低**   | 纯文字输出，一眼扫过去难以区分"成功/跳过/失败"   |
| **缺乏结构**     | 多个仓库的并发输出混在一起，阅读困难             |
| **无决策过程**   | 只看到了结果，看不到 fetch 进度、commit 差异细节 |
| **无汇总**       | 完成后没有全局统计，用户不清楚总共同步了多少     |
| **缺少视觉节奏** | 没有分隔线或分组，相邻仓库的输出连成一片         |

---

## 二、改进目标

1. **每个仓库的决策过程可视** — 让用户看到 fetch → 分析 → 执行 的完整链路
2. **结果一眼可辨** — 使用 pterm 颜色/图标/对齐，让成功/跳过/失败 0.1 秒识别
3. **并发安全** — 输出行不会因为并发而错乱（用 channel 收集结果，主 goroutine 统一打印）
4. **末尾汇总表** — 类似 `ggt remote --all` 的 success/skipped/failed 计数
5. **Terminal 宽度自适应** — 分隔线和对齐自适应终端宽度

---

## 三、具体改进方案

### 3.1 输出结构：每仓库三行

每个仓库的输出固定为 3 行，格式统一：

```
── repo-name ──────────────────────────────────────
  ✔ FETCH   → 成功 (2 new refs)
  ✔ SYNC    → 已是最新 [abc1234]
── repo-name ──────────────────────────────────────
  ✔ FETCH   → 成功
  ⚠ SYNC    → 本地有未提交更改，跳过
── repo-name ──────────────────────────────────────
  ✔ FETCH   → 成功
  ▶ PULL    → ff-only 拉取成功 [abc1234..def5678]
── repo-name ──────────────────────────────────────
  ✔ FETCH   → 成功
  ✗ SYNC    → 非线性分叉，需手动处理
```

**设计要点**：

- 第一行：仓库名作为 section 标题（使用 pterm 的 `FgCyan` + `…` 填充）
- 第二行：fetch 状态（`FETCH` 标签 + 结果）
- 第三行：sync 决策（`SYNC/PULL/BLOCKED` 等标签 + 结果描述 + commit hash 缩写）
- 第四行（可选）：错误详情

### 3.2 标签系统

定义一组固定宽度的彩色标签，放在每行开头：

| 标签      | 颜色 | 含义                              |
| --------- | ---- | --------------------------------- |
| `✔ FETCH` | 绿底 | 拉取成功                          |
| `✗ FETCH` | 红底 | 拉取失败                          |
| `✔ SYNC`  | 绿底 | 已是最新 / 拉取成功               |
| `⏭ SYNC` | 黄底 | 已跳过（有未提交更改 / 领先远程） |
| `✗ SYNC`  | 红底 | 非线性分叉，需手动处理            |
| `▶ PULL`  | 蓝底 | 正在执行 `git pull --ff-only`     |
| `⚠ BLOCK` | 黄底 | 阻塞状态，无法自动处理            |

使用 pterm 的 `<prefix>` 机制或手动拼接 `pterm.BgGreen.Sprint(" ✔ FETCH ")` 实现。

### 3.3 并发安全

当前 `syncRepo` 直接调用 `pterm.Info/Warn` 等函数，这些函数内部调用 `fmt.Println`，没有锁保护。多个 goroutine 同时输出时可能行交错。

**改进方案** — 使用结果收集模式（已在我写的 `remote.go` 中验证）：

```
Run 函数内:
  results := make(chan repoResult, len(repos))
  启动 goroutine 并发执行 syncRepo，每个将结果写入 results channel
  主 goroutine 从 results channel 读取并顺序打印

syncRepo 不再直接输出，而是返回结果结构体
```

### 3.4 汇总表

同步完成后，打印一个汇总面板：

```
═══════════════════════════════════════════════════
  已完成  5 / 12
  ┌──────────────────────────────────┐
  │ ✔ 已是最新         7            │
  │ ✔ 已拉取           2            │
  │ ⏭ 有未提交更改     1            │
  │ ⏭ 领先远程         1            │
  │ ✗ 非线性分叉       1            │
  │ ✗ 错误             0            │
  └──────────────────────────────────┘
```

可以使用 pterm 的 `DefaultTable` 或 `DefaultPanel`，或者手动用 `pterm.FgCyan` / `pterm.FgGreen` 拼接。

### 3.5 fetch 阶段显示 spinner

使用 `pterm.DefaultSpinner` 显示全局 fetch 进度：

```go
spinner, _ := pterm.DefaultSpinner.Start("正在拉取远程变更...")
// ... 并发 fetch ...
spinner.Success("拉取完成")
```

或者在每个仓库级别显示更详细的信息。

---

## 四、与调研报告的结合

根据 `github-rate-limit-research.md` 的第 5.2 节建议，sync 命令应考虑：

| 建议                | 实现方式                                                                                   |
| ------------------- | ------------------------------------------------------------------------------------------ |
| git fetch 并发 ≤ 10 | 全局默认 `min(CPU 核心数/2, 10)`，`-c` 手动设定超出时自动回退到 10，`--unlimit` 解除限制并打印免责声明 |
| 请求间隔 ≥ 1 秒     | 在 `sem` 获取后增加 `time.Sleep(time.Second)`（可选，通过 flag 控制）                      |
| 实现重试机制        | fetch 失败时自动重试 1 次，间隔 2 秒                                                       |
| 使用认证令牌        | 全局 git config 中已有 `credential.helper` 时自动使用，无需额外处理                        |

可在输出中添加一行提示，告知用户当前并发数：

```
ℹ 同步配置: 并发 4 | fetch 超时 30s | 拉取模式 --ff-only
```

---

## 五、代码重构方向

### 5.1 syncRepo 返回结构体而非直接输出

```go
type SyncResult struct {
    Name     string
    Err      error           // fetch 阶段错误
    Action   SyncAction      // 最终决策
    Detail   string          // 人类可读的描述
    Local    string          // HEAD commit hash 缩写
    Remote   string          // upstream commit hash 缩写
    Pulled   bool            // 是否执行了 pull
}

type SyncAction int
const (
    SyncUpToDate   SyncAction = iota // 已是最新
    SyncPulled                       // 已拉取
    SyncDirty                        // 有未提交更改
    SyncAhead                        // 领先远程
    SyncDiverged                     // 非线性分叉
    SyncFetchFailed                  // fetch 失败
)
```

### 5.2 打印函数只处理展示

```go
func printSyncResult(r SyncResult) {
    // 使用 pterm 组装美观的输出
}
```

### 5.3 汇总

```go
func printSyncSummary(results []SyncResult) {
    // 统计各类别的数量，用 pterm.Panel 渲染
}
```

---

## 六、预期效果对比

### 改进前

```
ℹ [repoA] 本地与远程一致，无需处理
⚠ [repoB] 检测到线性更新，正在拉取...
✔ [repoB] 拉取成功
⚠ [repoC] 本地有未提交的更改，必须手动处理
✗ [repoD] 非线性更新，必须手动处理
✔ 所有仓库同步完成
```

### 改进后

```
────────────────────────────────────────────────

 [repoA]  master
   ✔ FETCH   →   远程跟踪已更新 (2 new)
   ✔ SYNC    →   已是最新             [abc1234]

 [repoB]  master
   ✔ FETCH   →   成功
   ▶ PULL    →   ff-only 拉取成功     [abc1234→def5678]

 [repoC]  feature/x
   ✔ FETCH   →   成功
   ⏭ SYNC    →   本地有未提交更改，跳过

 [repoD]  experimental
   ✔ FETCH   →   成功
   ✗ SYNC    →   非线性分叉，需手动处理

────────────────────────────────────────────────
 已完成 4/4 个仓库
  ✔ 已是最新     1
  ✔ 已拉取       1
  ⏭ 已跳过       2
  ✗ 失败         0
────────────────────────────────────────────────
```
