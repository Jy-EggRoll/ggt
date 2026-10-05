#!/usr/bin/env bash
# 跑一遍网页看板的浏览器验收（桌面、大屏、平板、手机四档视口）。
#
# 为什么要有这一层：单测证明的是数据与规则，证明不了页面真的渲染、点击真的生效、配置真的写进
# 文件。这套流程把被测服务关进一个临时 HOME 里起起来（配置、临时仓库都在临时目录），
# 再用真实浏览器逐条断言并留下截图。
#
# 用法：bash verify/webui/run.sh
#   VP=desktop        只跑桌面档
#   KEEP=1            跑完保留临时目录（默认只在失败时保留，用于回看现场）
#   BROWSER_VERIFY_LIB 指向 browser-verify skill 的 lib 目录（默认 ~/.dsh/skills/browser-verify/lib）
#
# 退出码就是验收的退出码：全通过为 0
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

work=${GGT_VERIFY_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/ggt-webui-verify.XXXXXX")}
mkdir -p "$work"

server_pid=''
cleanup() {
  local status=$?
  if [ -n "$server_pid" ]; then
    # 精确按 pid 停：这里已经确定它就是本次起的那个服务
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ "$status" -ne 0 ] || [ -n "${KEEP:-}" ]; then
    echo "临时目录保留在 $work（仓库、配置、服务日志都在里面）" >&2
  else
    rm -rf "$work"
  fi
  exit "$status"
}
trap cleanup EXIT

echo "—— 造临时仓库与隔离配置 ——"
bash verify/webui/setup.sh "$work" >/dev/null

echo "—— 构建被测二进制 ——"
go build -o "$work/ggt" .

echo "—— 起服务（隔离 HOME，端口由系统分配）——"
# --port 0 让系统挑一个空闲端口，再把它打印出来的地址读回来；这样不会撞上正在运行的 ggt ui
# HOME 指向临时目录，服务读写的配置都在那里，绝不碰使用者自己的 ~/.config/go-git-ggt
HOME="$work/home" "$work/ggt" ui --no-open --port 0 -l zh-CN > "$work/ui.log" 2>&1 &
server_pid=$!

url=''
for _ in $(seq 1 60); do
  url=$(grep -oE 'http://127\.0\.0\.1:[0-9]+/\?token=[A-Za-z0-9_-]+' "$work/ui.log" | head -1 || true)
  [ -n "$url" ] && break
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "服务启动失败，日志如下：" >&2
    cat "$work/ui.log" >&2
    exit 1
  fi
  sleep 0.5
done
if [ -z "$url" ]; then
  echo "等服务打印出地址超时，日志如下：" >&2
  cat "$work/ui.log" >&2
  exit 1
fi

echo "—— 跑四档视口验收 ——"
set +e
GGT_URL="$url" GGT_VERIFY_CONFIG="$work/home/.config/go-git-ggt/ggt-config.json" \
  node verify/webui/verify.mjs
status=$?
set -e

if [ "$status" -ne 0 ]; then
  echo "服务日志留在 $work/ui.log" >&2
fi
exit "$status"