# ggt

ggt（Git 仓库管理工具）是一个用于集中管理多个 Git 仓库的命令行工具。它基于 [Cobra](https://github.com/spf13/cobra) + [Viper](https://github.com/spf13/viper) + [pterm](https://github.com/pterm/pterm) 构建，支持对一批仓库并发执行状态检查、大小统计、同步、批量提交与远程协议切换。

## 特性

- 通过 `repo_paths` 直接登记仓库，或通过 `parent_paths` 扫描父目录下的所有仓库
- 并发执行，速度随仓库数量线性提升
- 子模块在 ggt 中被视为一等仓库，自动随主仓库一并处理（带 `[子]` 标识）
- 远程协议（HTTPS / SSH）一键切换，支持 `--all` 批量模式与 `toggle` 取反
- 统一的彩色输出样式，信息层次清晰

## 安装

### 从源码构建

需要本地已安装 Go 1.21 及以上版本：

```bash
go install github.com/Jy-EggRoll/ggt@latest
```

或克隆仓库后本地构建：

```bash
git clone https://github.com/Jy-EggRoll/ggt.git
cd ggt
go build -o ggt .
```

构建产物也可通过 `Taskfile` 生成全平台二进制：

```bash
task build-all
```

产物位于 `dist/` 下按平台命名的目录中。

### 验证安装

```bash
ggt version
```

## 快速开始

```bash
# 添加一个仓库（路径必须是已初始化的 git 仓库）
ggt repo add ~/GitRepo/my-project

# 添加一个父目录，ggt 会自动扫描其直接子目录中的 git 仓库
ggt repo add-parent ~/GitRepo

# 查看当前已配置的所有仓库
ggt repo list

# 检查所有仓库的状态
ggt status

# 统计所有仓库的大小并按阈值分桶
ggt size
```

## 配置

配置文件位于 `~/.config/go-git-ggt/ggt-config.json`，首次运行任意命令时若不存在会自动生成默认值。也可用以下命令查看：

```bash
ggt config show     # 打印当前生效配置
ggt config path     # 打印配置文件绝对路径
```

支持的配置项：

| 配置项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `repo_paths` | string[] | 空 | 直接登记的仓库绝对路径列表 |
| `parent_paths` | string[] | 空 | 父目录列表，运行时扫描其中的 git 仓库 |
| `concurrency` | string | `CPUHalf` | 并发数。可取语义值 `CPUHalf`/`CPUFull`/`CPUQuarter`，或显式数字串（如 `"8"`）。命令行 `-c` 仅当大于 0 时覆盖此项 |
| `ignore_submodules` | bool | `false` | 为 `true` 时在所有功能中忽略子模块；默认 `false`（包含子模块） |
| `size_bucket_low_mb` | int | `500` | `size` 命令分桶的下界阈值（MB） |
| `size_bucket_high_mb` | int | `800` | `size` 命令分桶的上界阈值（MB） |
| `size_unit` | string | `decimal` | `size` 命令的 MB 换算口径：`decimal`（1 MB = 1,000,000 字节）或 `binary`（1 MB = 1024×1024 字节，即 MiB） |

示例配置：

```json
{
  "repo_paths": ["/home/user/GitRepo/my-project"],
  "parent_paths": ["/home/user/GitRepo"],
  "concurrency": "CPUHalf",
  "ignore_submodules": false,
  "size_bucket_low_mb": 500,
  "size_bucket_high_mb": 800,
  "size_unit": "decimal"
}
```

`concurrency` 同时兼容旧版的数字写法（如 `"36"`），加载时会自动按字符串处理。

## 子命令

### repo —— 管理仓库路径配置

| 命令 | 说明 |
| --- | --- |
| `ggt repo list` | 列出所有已配置的仓库路径 |
| `ggt repo add <path>` | 添加一个仓库路径（必须已初始化为 git 仓库） |
| `ggt repo remove <path>` | 从配置中移除一个仓库路径 |
| `ggt repo add-parent <path>` | 添加一个父目录，运行时自动扫描其中的 git 仓库 |

### status —— 查看仓库状态

`ggt status`（别名 `ggt st`）并发检查所有仓库的 `git status`，输出每个仓库的分支与未提交改动。

### size —— 统计仓库大小

`ggt size`（别名 `ggt sz`）并发统计每个仓库的磁盘占用与包文件大小，并按阈值分桶。可通过 `--low`、`--high`、`--unit` 临时覆盖配置：

```bash
ggt size --low 200 --high 600 --unit binary
```

### sync —— 批量同步

`ggt sync` 对每个仓库执行 `fetch --all --prune`，再依据本地 HEAD、远程 upstream、共同祖先的关系自动 fast-forward 拉取，或提示手动推送/处理分叉。未设置上游跟踪分支的仓库会被跳过并给出明确提示。

### summary —— 交互式提交与推送

`ggt summary`（别名 `ggt sum`）先并发检查所有仓库的变更，再对存在变更的仓库逐一展示 diff 并询问是否一键提交并推送（含子模块）。

### remote —— 切换远程协议

在 HTTPS 与 SSH 之间切换远程 `origin` 的地址：

| 命令 | 说明 |
| --- | --- |
| `ggt remote https` | 当前仓库切换为 HTTPS |
| `ggt remote ssh` | 当前仓库切换为 SSH |
| `ggt remote toggle` | 在当前仓库的 HTTPS / SSH 之间取反切换 |
| `ggt remote https --all` | 所有已配置仓库切换为 HTTPS（`ssh` 同理） |

### owned —— 获取所有权（仅 Windows）

`ggt owned` 调用 Windows `takeown` 命令批量获取所有仓库目录及其 `.git` 目录的所有权。非 Windows 系统会直接提示并跳过。

### version / config

- `ggt version`：打印版本信息
- `ggt config show` / `ggt config path`：查看配置内容与配置文件路径

## 子模块处理

ggt 将子模块统一抽象为与普通仓库平级的条目，任何功能都会把子模块当作一个完整的仓库处理：

- 输出中子模块以 `[子] 名称` 形式标识（如 `[子] my-sub`）
- 默认包含子模块；将配置 `ignore_submodules` 设为 `true` 可在所有功能中忽略它们
- `remote` 的 `toggle` / `https` / `ssh` 以及 `--all` 都会辐射到子模块，并计入统计数量

## 参考信源

- [Cobra](https://github.com/spf13/cobra)
- [Viper](https://github.com/spf13/viper)
- [pterm](https://github.com/pterm/pterm)
- [Git submodule 文档](https://git-scm.com/docs/git-submodule)
