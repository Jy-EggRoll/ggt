# ggt

ggt（Git 仓库管理工具）是一个用于集中管理多个 Git 仓库的命令行工具。它基于 [Cobra](https://github.com/spf13/cobra) + [Viper](https://github.com/spf13/viper) + [pterm](https://github.com/pterm/pterm) 构建，支持对一批仓库并发执行状态检查、大小统计、同步、批量提交与远程协议切换。

## 特性

- 通过 `repo_paths` 直接登记仓库，或通过 `parent_paths` 扫描父目录下的所有仓库
- 并发执行，速度随仓库数量线性提升
- 子模块在 ggt 中被视为一等仓库，自动随主仓库一并处理（带 `[子]` 标识）
- 远程协议（HTTPS / SSH）一键切换，支持 `--all` 批量模式与 `toggle` 取反
- 统一的彩色输出样式，信息层次清晰
- 内置中英双语，默认英文，可通过 `--lang` 或配置项 `language` 切换

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

配置文件位于 `~/.config/go-git-ggt/ggt-config.json`。**文件不存在时 ggt 不会自动创建它**，而是在内存里补上默认值；只有 `ggt repo add` 这类写入命令才会落盘。

```bash
ggt config                     # 打印当前生效配置
ggt config show                # 同上
ggt config path                # 打印配置文件绝对路径（面向脚本，无色输出）

ggt config get <key>           # 打印某项的生效值
ggt config set <key> <value>   # 校验后写入单项
ggt config reset <key>         # 删除该项，使其回落到默认值
ggt config reset <key> --defaults   # 改为显式写入默认值
ggt config reset --all         # 删除整个配置文件（需确认）
ggt config reset --all --defaults   # 写入一份只含默认值的配置文件

ggt config validate            # 体检配置文件
```

`get` / `validate` / `path` 的输出不带颜色，便于管道消费（`ggt config show | jq` 也可用；非终端环境下可另设 `NO_COLOR=1` 全局关闭着色）。

`reset` 有两种模式：默认**删除**该值（键从文件里消失），加 `--defaults` 则**写入默认值**。`--all` 同理——默认删掉整个配置文件，加 `--defaults` 则写一份只含默认值的文件。两条路径都会清空已登记的仓库，删除前会要求确认（`--yes` 可跳过，非交互环境下必须加）。

`validate` 会报出那些**运行期会被静默忽略或替换**的问题，逐条标明级别：未知键、非法取值、大小写重复键、UTF-8 BOM 属 error；阈值倒置、路径不存在、相对路径、重复条目属 warning。存在 error 时退出码为 1。

`repo_paths` 与 `parent_paths` 不能用 `config set` 修改——增删请走 `ggt repo add` / `remove` / `add-parent`，那里有去重、git 仓库校验与路径规范化。

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
| `language` | string | `en` | 输出语言，可选 `en` 或 `zh-CN`。命令行 `--lang` / `-l` 优先于此项 |

示例配置：

```json
{
  "repo_paths": ["/home/user/GitRepo/my-project"],
  "parent_paths": ["/home/user/GitRepo"],
  "concurrency": "CPUHalf",
  "ignore_submodules": false,
  "size_bucket_low_mb": 500,
  "size_bucket_high_mb": 800,
  "size_unit": "decimal",
  "language": "en"
}
```

`concurrency` 同时兼容旧版的数字写法（如 `"36"`），加载时会自动按字符串处理。

## 语言

ggt 内置中英双语，**默认英文**，中文作为兼容层按需启用。

语言按以下优先级确定，前一项优先：

1. 命令行 `--lang` / `-l`，如 `ggt --lang zh-CN size`
2. 配置文件的 `language` 字段
3. 默认值 `en`

语言串大小写不敏感，且同语种的地区/字形变体会自动收敛到已发布的那一种：
`zh`、`zh-Hans`、`zh-hans`、`zh-TW` 都会被识别为 `zh-CN`，`en-US` 会被识别为 `en`。
无法识别的取值不会报错，一律回退到 `en`。

已知限制：

- cobra 框架自身的帮助骨架（`Usage:`、`Available Commands:`、`Flags:`、`-h` 的说明、`completion` 子命令、`unknown flag` 等报错）始终是英文，不随语言切换
- 语言只认命令行与配置文件，不读取 `LANG` / `LC_ALL` 等环境变量
- **不支持同一句英文的多义翻译**：消息 id 就是英文原文，两处相同的英文必然共用一条译文。
  需要区分时只能把英文写得更具体
- **不支持复数**：`{{.Count}} repositories` 在 `Count` 为 1 时仍会输出 `repositories`。
  涉及数量的文案请改用语序回避（如 `Repositories: {{.Count}}`）

### 文案与翻译

文案采用与 VSCode `l10n` 相同的模型：**英文原文本身就是消息 id**。

- 源码里直接写英文原文，交给 `i18n.T` 翻译并插值：
  `i18n.T("Total size: {{.Size}}", map[string]any{"Size": s})`
- `internal/i18n/locales/en.json` 是**生成物**，内容为 `{英文原文: 英文原文}` 的自映射，
  **不要手工编辑**——它由工具扫描源码覆盖写入
- `internal/i18n/locales/zh-CN.json` 是**人工维护**的译文，形如 `{英文原文: 中文}`
- 缺失译文时回退显示英文原文，所以漏翻只会表现为"这句还是英文"，不会出现空串或裸 key

两个任务（沿用 `fmt:check` / `fmt` 的"门禁 / 修复"成对约定）：

```bash
task l10n:export   # 改过文案后重新生成语言文件，并打印待翻译与待迁移清单
task l10n:check    # 只读门禁，已接入 task verify，CI 会跑
```

写文案时的约定，违反其中任何一条都会被 `tools/l10n` 拦下：

- 消息调用的首个参数**必须是字符串字面量**，不能是变量或拼接结果——工具靠它确定消息 id。
  任何透传封装都会被判为违规，因此 `cmd` 包刻意不提供 `T` 薄封装，各命令直接调用 `i18n.T`
- 变量插值用 go-i18n 的 `{{.Var}}` 模板语法，不要用 Go 的 `%s` / `%d`
- 含非 ASCII 字的 Go 字面量必须包在 `i18n.T` 里，否则会被 `l10n:export` 列为"待迁移文案"
  （全仓文案已迁移完毕，该清单恒为空；它一旦重新出现就说明有新文案漏包了）
- 多行原文（如命令的 `Long` 描述）不得含制表符、行尾空白或首尾空行：id 就是原文，
  源码重排会让 id 漂移、既有译文静默失效
- 消息 id 不能是 `other`、`one`、`hash`、`id`、`description` 等 go-i18n 保留字（忽略大小写），
  否则整份语言文件会解析失败或条目被丢弃
- flag 说明中不能出现反引号对：pflag 会把第一对反引号里的内容当成类型名显示，并从说明文本中移除

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

分桶结果按严重程度着色：
- **< 低阈值**（默认 <500MB）：浅蓝信息展示
- **低阈值 ~ 高阈值**（默认 500~800MB）：黄色警告
- **> 高阈值**（默认 >800MB）：红色警告

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
- `ggt config`：查看与编辑配置，详见上文「配置」一节

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
