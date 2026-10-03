# ggt

ggt（Git 仓库管理工具）是一个用于集中管理多个 Git 仓库的命令行工具。它基于 [Cobra](https://github.com/spf13/cobra) + [pterm](https://github.com/pterm/pterm) 构建（配置解码用 [mapstructure](https://github.com/go-viper/mapstructure)，不引入 Viper 的包级全局状态），支持对一批仓库并发执行状态检查、大小统计、同步、批量提交与远程协议切换，也可以打开本机网页看板（`ggt ui`）逐项查看变更明细。

## 特性

- 通过 `repo_paths` 直接登记仓库，或通过 `parent_paths` 扫描父目录下的所有仓库
- 并发执行，速度随仓库数量线性提升
- 子模块在 ggt 中被视为一等仓库，自动随主仓库一并处理（带 `[子]` 标识）
- 远程协议（HTTPS / SSH）一键切换，支持 `--all` 批量模式与 `toggle` 取反
- 本机网页看板（`ggt ui`）：仓库按多列铺开，一屏之内看到尽可能多的仓库与变更明细，视觉逐项对齐 VSCode 的源码管理界面
- 统一的彩色输出样式，信息层次清晰
- 内置中英双语，默认英文，可通过 `--lang` 或配置项 `language` 切换
- 内置自升级（`ggt upgrade`）：升级前校验发布产物的 SHA-256 摘要，并在下一次运行时自动清理替换残留
- 诊断日志分级：`-v` 输出 info（含各阶段耗时）、`-vv` 及以上输出 debug，也可用配置项 `log_level` 设定默认级别

## 安装

### 从源码构建

需要本地已安装 Go 1.26 或更高版本（与 `go.mod` 中的要求一致）。

> **注意**：下述 `go install` 依赖已发布 tag 的版本，而 ggt 目前尚未发布任何版本，因此暂时只能走源码构建。

```bash
go install github.com/jy-eggroll/ggt@latest
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

产物位于 `build/` 下，按 `ggt-<系统>-<架构>[.exe]` 命名。

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
| `log_level` | string | `warn` | 诊断日志级别，可选 `debug`/`info`/`warn`/`error`。命令行 `-v`/`-vv` 优先于此项；取值非法时不中止命令，改为回退默认级别并提示 |

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
  "language": "en",
  "log_level": "warn"
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

底层实现是一个可复用的库 **`pkg/l10n`**（`ggt` 是它的第一个使用者），配套工具在
`pkg/l10n/cmd/l10n`。库本身不含任何 ggt 专属信息，换项目只需提供自己的 `l10n.Options`。

- 源码里直接写英文原文，交给 `l10n.T` 翻译并插值：
  `l10n.T("Total size: {{.Size}}", map[string]any{"Size": s})`
- `internal/locales/en.json` 是**生成物**，内容为 `{英文原文: 英文原文}` 的自映射，
  **不要手工编辑**——它由工具扫描源码覆盖写入
- `internal/locales/zh-CN.json` 是**人工维护**的译文，形如 `{英文原文: 中文}`
- 缺失译文时回退显示英文原文，所以漏翻只会表现为"这句还是英文"，不会出现空串或裸 key
- ggt 自己的语言列表与默认语言在 `internal/locales/locales.go`（`Options()` / `Supported()`）

两个任务（沿用 `fmt:check` / `fmt` 的"门禁 / 修复"成对约定）：

```bash
task l10n:export   # 改过文案后重新生成语言文件，并打印待翻译与待迁移清单
task l10n:check    # 只读门禁，已接入 task verify，CI 会跑
```

写文案时的约定，违反其中任何一条都会被 `pkg/l10n/cmd/l10n` 拦下：

- 消息调用的首个参数**必须是字符串字面量**，不能是变量或拼接结果——工具靠它确定消息 id。
  任何透传封装都会被判为违规，因此各命令直接调用 `l10n.T`，不设薄封装
- 只认**显式选择器**（`l10n.T(...)` 或该包的导入别名）。裸 `T(...)` 不算，以免别的包里
  的同名函数或泛型类型参数被误判
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

### ui —— 本机网页看板

`ggt ui` 起一个只绑本机的网页服务，用多列看板展示所有仓库的变更明细。每列填满窗口高度，
变更文件多的仓库会延续到下一列（断口处保持直角，一眼看得出是同一张卡片）；页面只横向滚动，
滚轮与触控板横扫都能横向浏览。脏仓库置顶，同级按名称升序。看板与 `ggt status` 共用同一份
`git status --porcelain=v2` 采集结论，因此两者不会给出不同答案。

在页面上还能直接干活：

- **看板按 VSCode 的源码管理那样分组**：每个仓库卡片内部依次是「未合并的改动」「已暂存的改动」
  「未暂存的改动」，空的那段不显示。同一个文件两侧都改了（`git add` 之后又改了）会在两组各出现
  一次，各自只带自己那侧的操作：已暂存那行给 `−`（取消暂存），未暂存那行给 `+`（暂存）。
  状态字母按各自那侧显示（暂存组看 porcelain 的 X 位、未暂存组看 Y 位）
- **点仓库名进入仓库面板**：面板是这个仓库的主页面，头部是仓库名、当前分支与领先/落后，
  正文是入口行（目前是「改动 N」）。分支图之后也加在这里
- **点开看 diff**：从面板里点「改动」看整个仓库的改动，或直接点某个文件行看该文件的 diff。
  整页覆盖层展示，分「已暂存的改动」与「未暂存的改动」两段，只按行首的 `+` / `-` 着色
  （不做语法高亮），长行折行显示。未跟踪文件整份按新增展示，二进制只给一行提示，
  单份输出超过 2 MiB 会截断并标注。按 `Esc` 或左上角的「返回」逐层退回
- **暂存 / 取消暂存**：鼠标移到变更文件行上，行尾出现 `+` 或 `−`。未合并（有冲突）的文件不提供
  这个按钮——`git add` 一个仍带着冲突标记的文件等于把这些标记当成分辨结果，一次误点就可能提交进去
- **写提交信息并提交、推送当前分支**：在 diff 视图的标题栏里输入提交信息，回车或点「提交」即
  提交已暂存的改动；「推送」推当前分支。失败时页面上显示的是 **git 的原话**（例如没有 upstream
  分支、没有可提交的内容），不会替你决定怎么处理

页面上的写操作与读操作受同一套门禁保护（只绑本机、Host 允许清单、token、写请求同源校验），
且仓库与文件都必须来自页面已看到的那份状态快照，手工构造的请求传不进配置之外的路径。

**配色跟随你的 VSCode 主题**

把任意一个合法的 VSCode 主题 JSON 丢进配置目录（`~/.config/go-git-ggt/` 或它的 `themes/`
子目录），刷新页面就能在左下角的下拉框里选它——你在 VSCode 里用的是哪套，看板就是哪套。

主题文件按 VSCode 自己的规则解析：JSONC 的注释与尾逗号、`include` 链逐层合并、以及
**主题没写的令牌回落到 VSCode 颜色注册表的默认值**（`gitDecoration.*`、`diffEditor.*`
这些颜色本来就不在主题文件里，VSCode 用的正是那些默认值）。因此颜色与 VSCode 的观感一致，
而不是"看起来差不多"。

下拉框里另有内置的 12 套：VSCode 官方的 8 套（2026 Dark/Light、Dark/Light Modern、Dark+/Light+、
Visual Studio Dark/Light）与 Catppuccin 的 4 套口味。选「跟随系统」则按系统深浅自动在内置的
2026 Dark / 2026 Light 之间切换（默认就是这个）。选中的主题记在配置文件的 `theme` 键里，
也可以直接改它或 `ggt config set theme <id>`。

内置主题文件逐字取自上游（VSCode 与 Catppuccin，均为 MIT 许可，许可文本随文件一起放在
`internal/theme/builtin/*/LICENSE.txt`）。

已知限制：不支持 VSCode 的高对比主题（`hcDark`/`hcLight`）——那是为无障碍场景单独设计的一套
视觉，不是换几个色值就行；也不读主题里的 `tokenColors`（语法高亮），本看板不做语法高亮。

```bash
ggt ui                # 起服务并自动打开浏览器
ggt ui --no-open      # 只打印地址
ggt ui --port 8721    # 绑定固定端口
```

| 参数 | 说明 |
| --- | --- |
| `--port` | 监听端口；默认 `0` 由系统挑一个空闲端口，指定端口被占用时依次 +1 重试，最多 100 次 |
| `--host` | 绑定地址，默认 `127.0.0.1`；设为 `0.0.0.0` 会把页面暴露给同网段所有人，此时会打印警告 |
| `--allow-host` | 在 Host 请求头中额外接受的主机名（可重复指定）；绑定地址会被自动纳入允许清单 |
| `--no-open` | 不自动拉起浏览器，只打印地址 |

访问需要启动时打印的那个带 `token` 的地址，且 Host 头必须在允许清单之内。这道门禁是为防
本机其它进程直接抓取——页面能读到本机全部仓库的路径与变更。页面语言跟随 `--lang` 或配置项
`language`，与命令行输出一致。

变更行数很多时布局也不会卡：`layout()` 既不读布局属性，也不往已布局的容器里逐个插元素，
因此不会触发强制同步重排——这两处原来都会让耗时随总行数呈 O(n²) 增长。实测 4020 行每次
重排由 780 毫秒降到 11 毫秒，24021 行为 20 毫秒出头（修复前的记录是 20332 行约 88.7 秒，
期间页面不出画面）。

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

### upgrade —— 自升级

`ggt upgrade`（别名 `update`、`up`）从 GitHub Release 检查并升级到最新版本。

| 命令 | 说明 |
| --- | --- |
| `ggt upgrade` | 检查并升级到正式版通道的最新版本 |
| `ggt upgrade --check` | 只检查版本，不下载也不替换 |
| `ggt upgrade --dev` | 改走开发版通道（`x.y.z.dev.n`） |
| `ggt upgrade --force` | 即使版本相同也重新安装 |
| `ggt upgrade --yes` | 跳过确认，非交互环境（CI、管道）下必须加 |

升级会先校验发布产物的 SHA-256 摘要，内容与 Release 记录不符时拒绝安装。替换完成后需重启 ggt 才会切换到新版本。

匿名访问 GitHub API 有较低的速率限制；设置环境变量 `GITHUB_TOKEN` 可提高配额（未设置时升级命令会提示一次）。

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
- [mapstructure](https://github.com/go-viper/mapstructure)
- [pterm](https://github.com/pterm/pterm)
- [Git submodule 文档](https://git-scm.com/docs/git-submodule)
