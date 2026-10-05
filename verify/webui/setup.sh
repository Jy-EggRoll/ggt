#!/usr/bin/env bash
# 造出网页验收要用的临时仓库与配置。
#
# 为什么要现造，而不是指向某个固定目录：验收里的断言（暂存区那个文件是 400 行新增、有纯改名、
# 合并提交相对第一个父提交改了哪两个文件、板上正好两个仓库、行内高亮只盖住变化的字符）
# 都建立在这份内容上；内容一变，断言就不再说明任何问题。每次从零造，才能保证这套验收在任何
# 机器上都是同一件事。
#
# 隔离：配置写在 <目标目录>/home/.config/go-git-ggt/ggt-config.json，被测服务以 HOME=<目标目录>/home
# 启动，因此它读写的都是这份临时配置，绝不碰使用者自己的 ~/.config/go-git-ggt
#
# 用法：setup.sh <目标目录>
# 成功后把三条信息打印到标准输出（供调用方读取）：仓库根、配置路径、两个仓库路径
set -euo pipefail

target=${1:?用法: setup.sh <目标目录>}
repos="$target/repos"
home_dir="$target/home"
config="$home_dir/.config/go-git-ggt/ggt-config.json"

rm -rf "$repos" "$home_dir"
mkdir -p "$repos" "$(dirname "$config")"

# 提交时间固定：截图与列表里的时间戳保持一致，两次运行之间除了被测代码没有别的差异
export GIT_AUTHOR_DATE='2026-01-02T03:04:05+08:00'
export GIT_COMMITTER_DATE="$GIT_AUTHOR_DATE"
export GIT_AUTHOR_NAME=ggt-verify GIT_AUTHOR_EMAIL=verify@example.com
export GIT_COMMITTER_NAME=ggt-verify GIT_COMMITTER_EMAIL=verify@example.com
# 不读使用者与系统的 git 配置：全局的 core.autocrlf、diff.renames、别名都会改变输出，
# 而验收断言的是 ggt 自己的行为
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# ——— 仓库一：改动齐全的那一个 ———
# 它要同时提供：已暂存的整段新增与纯改名、未暂存的一行追加、一个未跟踪的二进制文件、
# 以及一条含二进制与中文文件名的提交。网页上那几条断言正好各自对应其中一项
demo="$repos/demo"
mkdir -p "$demo"
cd "$demo"
git init -q -b main .
printf 'hello\n' > a.txt
git add -A
git commit -qm '初个提交'

printf 'hello\nworld\n' > a.txt
printf '\000\001\002\003\377\376\375' > blob.bin
printf 'line1\nline2\nline3\n' > normal.txt
printf 'a\nb\nc\n' > '带空格 的文件.txt'
printf '旧一\n旧二\n' > '旧名 文件.txt'
printf '目标\n' > '重命名 目标.txt'
git add -A
git commit -qm '加两个文件'

# 已暂存：整段替换（400 行新增）与一次纯改名（增删都是 0，只能靠旧路径认出来）
seq 1 400 > normal.txt
git add normal.txt
git mv '旧名 文件.txt' '新名 文件.txt'
# 未暂存：在已暂存的那份之上再追加一行，于是同一个文件在两侧各出现一次
printf '未暂存的一行\n' >> normal.txt
# 未跟踪：二进制文件，正文没法按 diff 显示，页面应当给提示而不是解码后的乱码
printf '\007\010\011\012' > '新增 blob.bin'

# ——— 仓库二：含一次 --no-ff 合并的那一个 ———
# 合并提交在 git 里默认不给 diff（组合格式页面认不出来），所以它必须有专门的用例盯着，
# 而这一份就是那个用例的输入：按第一个父提交看，它改了 feature.txt 与 side-only.txt
merge="$repos/merge-demo"
mkdir -p "$merge"
cd "$merge"
git init -q -b main .
printf 'base\n' > base.txt
printf 'one\ntwo\nthree\n' > feature.txt
git add -A
git commit -qm '初始提交'
git checkout -q -b side
# 这份 feature.txt 同时给两条断言当输入：two 改成 two 二 是“在行尾插入两个字符”，用来断言
# 高亮范围严格窄于整行；three 改成 THREE 是大小写改写，两者都会出现在合并提交的正文里。
# 两处都不改变文件集合，所以“按第一个父提交改了 2 个文件”那条断言不受影响
printf 'one\ntwo 二\nTHREE\nfour\n' > feature.txt
printf '侧\n' > side-only.txt
git add -A
git commit -qm '侧面的改动'
git checkout -q main
printf 'base\nbase2\n' > base.txt
git add -A
git commit -qm '主干上的改动'
git merge --no-ff -q side -m '合并 side 分支'

# ——— 被测服务要用的配置 ———
# 仓库顺序就是看板上从上到下的顺序，验收里按行号点第几个仓库依赖它
# log_level 留一个非默认值：面板里“恢复默认”那条用例要先把它改回默认，再看键是否从文件里消失
cd "$target"
printf '{\n  "log_level": "debug",\n  "repo_paths": [\n    "%s",\n    "%s"\n  ],\n  "theme": ""\n}\n' "$demo" "$merge" > "$config"

printf '%s\n' "$repos" "$config" "$demo" "$merge"