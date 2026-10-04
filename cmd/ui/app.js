'use strict';

/*
 * ggt ui 的页面逻辑。
 *
 * 布局方案：行级流式横向布局。把「行」（仓库标题行、变更文件行）而不是「整张卡片」当作布局单元，
 * 依次填进当前列；列高恒等于视口高，填满就换到右侧下一列，页面只横向滚动。
 *
 * 为什么不用 CSS 的 columns（多列文本流）：实测三处不可接受——
 *   1. 元素本身比列盒高时 break-inside: avoid 会失效，卡片被浏览器拦腰截断，续段完全没有标题，
 *      看不出那是哪个仓库的后续
 *   2. 矮卡片装不进当前列剩余空间时就整张跳到下一列，且后续内容不会回填，实测屏幕利用率只有 57%
 *      （行级流式实测 82%，纯 masonry 86%）
 *   3. 分片元素的 getBoundingClientRect 返回错乱的包围盒，连测量都不可信
 * 行级流式则天然没有这些问题：绝对定位的元素不参与分片，行永远不会被切成半个字，
 * 跨列延续恰好发生在行边界上，而且续段是我们自己插入的，能带上标识。
 *
 * 与 VSCode 的关系：凡 VSCode 有明确定义的视觉语义，本文件一律照抄，不自拟。
 *   - 状态字母表与配色      照抄 extensions/git/src/repository.ts 的 Resource.getStatusLetter /
 *                          getStatusColor（字母 M/A/D/R/T/U/I/C/!，颜色取 gitDecoration.* 令牌）
 *   - 文件类型图标          照抄默认图标主题 Seti，连解析顺序（fileNames -> fileExtensions ->
 *                          languageIds -> file）都一样，见 seti-icon-theme.json
 *   - 行高、徽标排版、hover 在 style.css 里（那里逐项标了源码出处）
 * 有意保留的差异只有这些，均为用户明确要求：多列布局、按待办置顶排序、只横向滚动、
 * 连续网格不要分组总览条、卡片内直接列出变更文件、文件名按状态上色
 */

// ——— 页面文案 ———
//
// 前端文案不经过 Go 的 l10n 提取管线（那条管线只扫 .go 文件），所以页面自带一份
// 以英文源串为 key 的翻译表，Go 端只负责把当前语言写进 window.__GGT_LANG__
// 新增文案时两种语言都要补齐，缺了会回退到英文（而不是显示成 key）
const MSG = {
  en: {
    noCommits: 'no commits yet',
    detached: 'detached HEAD',
    ahead: '↑{{n}}',
    behind: '↓{{n}}',
    continued: '{{name}} (continued {{n}})',
    updated: 'updated {{time}} · {{ms}} ms',
    loadFailed: 'Failed to load: {{err}}',
    failed: 'status failed',
    noRepos: 'No repositories configured — add one with "ggt repo add <path>"',
    diffStaged: 'Staged Changes',
    diffUnstaged: 'Unstaged Changes',
    diffEmpty: 'No changes',
    diffLoading: 'Loading…',
    diffFailed: 'Failed to load the diff: {{err}}',
    diffUntracked: 'New file (untracked) — shown in full as an addition',
    diffBinary: 'Binary file — contents not shown',
    diffUnmerged: 'Unmerged — conflict markers shown below',
    diffTruncated: 'Output truncated — the change is too large to show in full',
    groupUnmerged: 'Unmerged Changes',
    changes: 'Changes',
    fetchRepo: 'Fetch',
    sync: 'Sync',
    branchLabel: 'Branch',
    openDiff: 'Changes ({{n}})',
    openWholeRepoDiff: 'Show the whole repository diff',
    stageAll: 'Stage all changes',
    unstageAll: 'Unstage all changes',
    back: 'Back',
    stage: 'Stage this file',
    unstage: 'Unstage this file',
    commit: 'Commit',
    push: 'Push',
    commitMsg: 'Commit message',
    fetch: 'Fetch all',
    fetching: 'Fetching…',
    theme: 'Theme',
    themeFollow: 'Follow the system',
    themeMine: 'My themes',
    themeDarkPref: 'Dark',
    themeLightPref: 'Light',
    themeDarkPrefLabel: 'Preferred dark theme when following the system',
    themeLightPrefLabel: 'Preferred light theme when following the system',
    graphEntry: 'History graph',
    graphAllRefs: 'All branches and tags',
    graphCount: 'showing {{shown}} / {{total}}',
    graphLoading: 'Loading…',
    graphEnd: 'All commits loaded',
    graphLimitReached: 'Reached the {{n}}-commit limit; uncheck "All branches and tags" to see further back',
    graphNoCommits: 'No commits yet',
    graphNoSubject: '(no subject)',
    graphFiles: 'Changed files',
    graphFilesSummary: '{{n}} files, +{{adds}} −{{dels}}',
    graphBinary: 'binary',
    graphHash: 'Commit',
    graphAuthor: 'Author',
    graphDate: 'Date',
    graphRefs: 'Refs',
    graphClose: 'Close details',
    notifTitle: 'Notifications',
    notifClearAll: 'Clear all',
    notifClear: 'Clear this notification',
    notifClose: 'Dismiss this notification',
    notifEmpty: 'No notifications',
    notifBell: 'Notifications',
  },
  'zh-CN': {
    noCommits: '尚无提交',
    detached: '游离 HEAD',
    ahead: '领先 {{n}}',
    behind: '落后 {{n}}',
    continued: '{{name}}（续 {{n}}）',
    updated: '更新于 {{time}} · {{ms}} 毫秒',
    loadFailed: '加载失败：{{err}}',
    failed: '状态读取失败',
    noRepos: '尚未配置仓库 —— 用 "ggt repo add <路径>" 添加',
    diffStaged: '已暂存的改动',
    diffUnstaged: '未暂存的改动',
    diffEmpty: '没有改动',
    diffLoading: '加载中…',
    diffFailed: '加载 diff 失败：{{err}}',
    diffUntracked: '新文件（未跟踪）—— 整份按新增展示',
    diffBinary: '二进制文件 —— 不显示内容',
    diffUnmerged: '未合并 —— 下面显示冲突标记',
    diffTruncated: '输出过大，已截断，仅显示前面一部分',
    groupUnmerged: '未合并的改动',
    changes: '改动',
    fetchRepo: '拉取',
    sync: '同步',
    branchLabel: '分支',
    openDiff: '改动 {{n}}',
    openWholeRepoDiff: '查看整个仓库的改动',
    stageAll: '暂存全部',
    unstageAll: '全部取消暂存',
    back: '返回',
    stage: '暂存这个文件',
    unstage: '取消暂存这个文件',
    commit: '提交',
    push: '推送',
    commitMsg: '提交信息',
    fetch: '拉取全部',
    fetching: '正在拉取…',
    theme: '主题',
    themeFollow: '跟随系统',
    themeMine: '我放进去的主题',
    themeDarkPref: '深色',
    themeLightPref: '浅色',
    themeDarkPrefLabel: '跟随系统时的深色主题',
    themeLightPrefLabel: '跟随系统时的浅色主题',
    graphEntry: '分支图',
    graphAllRefs: '全部分支与 tag',
    graphCount: '已显示 {{shown}} / {{total}}',
    graphLoading: '加载中…',
    graphEnd: '已加载全部',
    graphLimitReached: '已到 {{n}} 条上限，可取消勾选「全部分支与 tag」往回看',
    graphNoCommits: '还没有提交',
    graphNoSubject: '（无提交信息）',
    graphFiles: '改动的文件',
    graphFilesSummary: '{{n}} 个文件，+{{adds}} −{{dels}}',
    graphBinary: '二进制',
    graphHash: '提交',
    graphAuthor: '作者',
    graphDate: '时间',
    graphRefs: '引用',
    graphClose: '关闭详情',
    notifTitle: '通知',
    notifClearAll: '全部清除',
    notifClear: '清除这条通知',
    notifClose: '关闭这条通知',
    notifEmpty: '没有通知',
    notifBell: '通知中心',
  },
};

const LANG = (typeof window.__GGT_LANG__ === 'string' && window.__GGT_LANG__) || 'en';

// t 取出当前语言的文案并做 {{var}} 插值。
// 用 split/join 而不是 replace：变量值里若含 $& 这类替换模式字符，replace 会把它们当模式解析
function t(key, vars) {
  const table = MSG[LANG] || MSG.en;
  let s = table[key] !== undefined ? table[key] : (MSG.en[key] !== undefined ? MSG.en[key] : key);
  if (vars) {
    for (const k of Object.keys(vars)) s = s.split('{{' + k + '}}').join(String(vars[k]));
  }
  return s;
}

// ——— 常量 ———

// 轮询间隔。取值权衡：状态变化（保存一个文件）要能较快反映，但又不能太频繁——
// 每次请求都会触发一轮服务端采集（每个仓库一个 git 进程），服务端虽有 2 秒缓存兜底，
// 多个标签页同时开着时压力仍会叠加
const POLL_MS = 5000;

// 卡片之间的纵向间距。与 CSS 无关：行的位置完全由本文件计算
// 列间距也从 CSS 变量读：它是布局参数，与 --col-w 一样只该有一处定义
const GAP = cssVar('--col-gap', 16);

// 滚轮平滑的参数。本轮动画的固定时长与曲线照抄宿主已有的
// AvaloniaDesktopKit/Behaviors/SmoothWheelScroll.cs（它又刻意与 Slint 1.18 对齐），
// 详细理由见下方 smoothScrollBy 处注释
const WHEEL_MS = 180;
const NOMINAL_FRAME_MS = 1000 / 60;
// DOM_DELTA_LINE 时一行折算的像素。与 Slint 的 line→60 逻辑像素对齐
// （i-slint-backend-winit 的 LineDelta(lx, ly) => (lx * 60., ly * 60.)）
const LINE_PX = 60;
// 判定「位置被别人改了」的像素阈值：拖滚动条、按方向键、触控板横扫都会改它，
// 阈值用来吸收浏览器取整的误差
const EXTERNAL_SCROLL_TOLERANCE = 3;

const board = document.getElementById('board');
const contLayer = document.getElementById('cont-layer');
const emptyEl = document.getElementById('empty');
const statusEl = document.getElementById('status');
const diffEl = document.getElementById('diff');
const diffBackEl = document.getElementById('diff-back');
const diffTitleEl = document.getElementById('diff-title');
const diffBodyEl = document.getElementById('diff-body');
const diffOpEl = document.getElementById('diff-op');
// 提交信息与提交按钮属于"仓库卡片"（VSCode 的源码管理视图里这一对也在最上方）
const commitMsgEl = document.getElementById('graph-msg');
const commitBtnEl = document.getElementById('graph-commit-btn');
const fetchBtnEl = document.getElementById('fetch-btn');
const themeSelectEl = document.getElementById('theme-select');
// 跟随系统时分别用哪套深色/浅色主题的两个下拉框，只在"跟随系统"下显示
const themeDarkEl = document.getElementById('theme-dark');
const themeLightEl = document.getElementById('theme-light');
// 仓库卡片：容器、列表、头部，以及顶栏上的仓库操作（分支选择、拉取、同步、推送、全仓 diff）
const graphEl = document.getElementById('graph');
const graphListEl = document.getElementById('graph-list');
const graphTitleEl = document.getElementById('graph-title');
const graphStateEl = document.getElementById('graph-state');
const graphCountEl = document.getElementById('graph-count');
const graphOpEl = document.getElementById('graph-op');
const graphBackEl = document.getElementById('graph-back');
const graphAllRefsEl = document.getElementById('graph-all-refs');
const graphFilterLabelEl = document.getElementById('graph-filter-label');
const graphBranchEl = document.getElementById('graph-branch');
const graphFetchEl = document.getElementById('graph-fetch');
const graphSyncEl = document.getElementById('graph-sync');
const graphPushEl = document.getElementById('graph-push');
const graphDiffEl = document.getElementById('graph-diff');
// 提交详情卡（悬浮即显，点一下钉住）
const graphPopupEl = document.getElementById('graph-popup');
const boardOpEl = document.getElementById('op');
// 通知：右下角的堆叠区、底栏的铃铛与未读徽标、以及铃铛点开的历史面板
const notificationsEl = document.getElementById('notifications');
const bellEl = document.getElementById('bell');
const bellCountEl = document.getElementById('bell-count');
const notifCenterEl = document.getElementById('notif-center');
const notifTitleEl = document.getElementById('notif-title');
const notifClearAllEl = document.getElementById('notif-clear-all');
const notifListEl = document.getElementById('notif-list');
const notifEmptyEl = document.getElementById('notif-empty');

// 返回按钮的文字在 JS 里填：它要跟随语言，而 index.html 是静态骨架、不参与翻译
diffBackEl.textContent = '← ' + t('back');
graphBackEl.textContent = '← ' + t('back');
fetchBtnEl.textContent = t('fetch');
// 卡片里的几个按钮与输入框的文案由 openRepoCard 按当前语言填（它们只在打开卡片时才出现）

// 从 URL 取 token。页面是由 Go 端带 token 的地址打开的，之后所有请求改用请求头传递：
// 把凭据留在 URL 里会进入浏览器历史、也可能随 Referer 泄露
const TOKEN = new URLSearchParams(location.search).get('token') || '';
const authHeaders = TOKEN ? { 'X-WebUI-Token': TOKEN } : undefined;

// rowEls 是「上一次渲染留下的行元素」，按 key 索引。
// 保留它们是为了 DOM 复用：数据刷新时能复用的元素就复用，位置变化由 CSS transition 平滑过渡；
// 若每次都重建 DOM，卡片会瞬间跳到新位置，看起来像整页闪烁
let rowEls = new Map();
// newRows 是「本次渲染新建的行」，layout 定位完它们之后要在下一帧把过渡打开回来。
// 之所以要记一批而不是逐行处理：见 reconcile 与 layout 末尾的说明
let newRows = [];
let lastSpecs = [];
let lastEls = [];
let lastRepos = [];
let pollTimer = null;

// cssVar 读取样式表里的长度变量并转成数字。
// 布局参数只在一处定义（style.css 的 :root），JS 从这里读，避免两边各写一份数字后漂移
function cssVar(name, fallback) {
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name);
  const n = parseFloat(raw);
  return Number.isFinite(n) ? n : fallback;
}

// viewportSize 返回真实的视口尺寸（布局视口，CSS 像素）。
//
// 为什么不能直接用 window.innerWidth / innerHeight：页面内容一旦横向或纵向溢出，Chromium
// 会把这两个值跟着内容一起撑大，而它们看起来就是个普通的窗口尺寸，读的人不会起疑。
// 实测（手机档，视口 390×844、看板按 4 列排宽 1344px）：
//     window.innerWidth  1360    window.innerHeight  2944
//     documentElement.clientWidth  390    clientHeight  844
// 看板的宽高偏偏又由 JS 按列数算出来写到元素上（见 layout 末尾的 board.style.width/height），
// 于是"尺寸读大了 → 看板算大了 → 溢出更多 → 尺寸读得更大"能自举成一个稳态：窄屏下按 4 列
// 排并横向溢出。分列、贴边、滚轮横滚上限与翻页折算因此全都按一个被撑大的尺寸在算。
// 更隐蔽的一层：layout 是轮询也会重跑的，一次重排就能把列高按 2944 算成近三倍，整块看板
// 塌成一列。改读 documentElement 的 client 尺寸后，这两条路都不会再被内容反过来影响
//
// 有一件事要说明白：一开始把"窄屏下详情卡被摆到视口外、点不到关闭按钮"也记在这条因果上，
// 实测证明不成立——那种情形下新旧代码算出的位置一模一样，真因是锚点本身在视口外，
// 见 commitCardPosition 的注释。这里不再重复那个错误结论
//
// 取 client 尺寸而不是 visualViewport：后者语义是"当前可见区"，会随捏合缩放变化，而这里要的是
// 布局依据。client 尺寸还会扣掉经典滚动条占宽，用于分列与贴边反而更准——无溢出时它与
// innerWidth 的差别也就只有滚动条那十几像素，其余场合与原来的取值一致
function viewportSize() {
  const de = document.documentElement;
  return { w: de.clientWidth, h: de.clientHeight };
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

// ——— 变更状态的字母与配色 ———
//
// 逐条照抄 VSCode extensions/git/src/repository.ts 的 Resource.getStatusLetter 与 getStatusColor。
// 两种状态下标：porcelain 的 X 位是「暂存区相对 HEAD」，Y 位是「工作区相对暂存区」。

// 暂存侧（X 位）
const STAGED = {
  M: { letter: 'M', cls: 'st-stage-modified' },
  A: { letter: 'A', cls: 'st-added' },
  D: { letter: 'D', cls: 'st-stage-deleted' },
  R: { letter: 'R', cls: 'st-renamed' },
  C: { letter: 'C', cls: 'st-renamed' },
  T: { letter: 'T', cls: 'st-type-changed' },
};

// 工作区侧（Y 位）
//
// A 这一档对应 git 的 intent-to-add（`git add -N`）：v2 输出形如 "1 .A N... path"，
// 即索引位是 "."、工作区位是 "A"。上游把它当"已新增"处理（repository.ts 的 raw.y === 'A'
// 映射到 INTENT_TO_ADD，字母 'A'、配色 addedResourceForeground）。此前这里没有 A，
// 该记录会落到下面 fileStatus 的兜底分支，字母侥幸还是 A，颜色却取了"已忽略"的灰并加斜体，
// 与 VSCode 的绿 A 不一致——这是实测（`git add -N` 后取 status）发现的
const WORKTREE = {
  A: { letter: 'A', cls: 'st-added' },
  M: { letter: 'M', cls: 'st-modified' },
  D: { letter: 'D', cls: 'st-deleted' },
  T: { letter: 'T', cls: 'st-type-changed' },
};

// fileStatus 把一个变更文件归到 VSCode 的某一个 Status 上。
//
// side 决定看哪一侧：'index' 是暂存区相对 HEAD（porcelain 的 X 位），'work' 是工作区相对
// 暂存区（Y 位）。分组之后同一个文件会在两组里各出现一次，而两组的字母本来就不是同一个
// （暂存组看 X 位、未暂存组看 Y 位），这正是分组的价值所在
//
// 归并规则与理由：
//   - 未合并优先。VSCode 的七种冲突状态（BOTH_MODIFIED 等）统一取字母 '!' 与
//     conflictingResourceForeground，上游注释写明不用 ⚠ 是因为它在 Windows 上显示很糟
//   - 未跟踪取 'U'。注意这与 `git status --short` 的 '??' 不同，是 VSCode 的字母表
//   - 已经被忽略的条目取 'I'（依赖 git.RunStatus 把 ignored 也解析出来）
function fileStatus(f, side) {
  if (f.unmerged) return { letter: '!', cls: 'st-conflicting' };
  if (f.untracked) return { letter: 'U', cls: 'st-untracked' };
  const ix = f.index || '.';
  const wk = f.work || '.';
  if (ix === '!') return { letter: 'I', cls: 'st-ignored' };
  if (side === 'index') {
    // 认不出的状态码不猜：保留原始字符并沿用中性色，便于发现解析遗漏
    return STAGED[ix] || { letter: ix, cls: 'st-ignored' };
  }
  return WORKTREE[wk] || { letter: wk, cls: 'st-ignored' };
}

// hasStaged / hasWork 判断一个文件在某一侧是否有改动，分组的依据就是它俩
function hasStaged(f) {
  if (f.unmerged || f.untracked) return false;
  const ix = f.index || '.';
  return ix !== '.' && ix !== '?';
}

function hasWork(f) {
  if (f.unmerged) return false;
  if (f.untracked) return true; // 未跟踪文件的两位都是 '?'，按"只在未暂存这一组"处理
  return (f.work || '.') !== '.';
}

// ——— 文件类型图标（Seti） ———
//
// 与 VSCode 用同一份图标主题文档与同一套解析顺序。之所以要运行时解析而不是编译期映射：
// 图标主题里 fileNames/fileExtensions 只是前两级，第三级 languageIds 需要文件的语言 id，
// 而语言 id 是 VSCode 由各语言扩展声明出来的——那张表另存在 vscode-language-map.json

let iconDark = null; // 深色主题的查找表
let iconLight = null; // 浅色主题的查找表（Seti 的 light 段是全量平行表）
let langMap = null; // 扩展名/文件名 -> 语言 id
const iconCache = new Map(); // 文件名 -> 图标 id，避免同一扩展名反复查表

const prefersLight = window.matchMedia('(prefers-color-scheme: light)');

// 取图标主题文档。两份 JSON 都是静态资源，与页面同源，走与其它请求相同的 token 规则
async function loadIconTheme() {
  try {
    const [theme, langs] = await Promise.all([
      fetch('seti-icon-theme.json', { cache: 'no-store' }).then((r) => r.json()),
      fetch('vscode-language-map.json', { cache: 'no-store' }).then((r) => r.json()),
    ]);
    const defs = theme.iconDefinitions || {};
    iconDark = {
      defs,
      file: theme.file,
      fileNames: theme.fileNames || {},
      fileExtensions: theme.fileExtensions || {},
      languageIds: theme.languageIds || {},
    };
    const light = theme.light || {};
    iconLight = {
      defs,
      file: light.file,
      fileNames: light.fileNames || {},
      fileExtensions: light.fileExtensions || {},
      languageIds: light.languageIds || {},
    };
    langMap = langs;
  } catch (err) {
    // 图标取不到只影响观感，不该让整块看板失败：其余信息照常渲染，只是没有类型图标
    iconDark = null;
    iconLight = null;
    langMap = null;
    console.warn('图标主题加载失败，本次渲染不带类型图标：' + err.message);
  }
}

// extCandidates 按 VSCode 的规则给出某个文件名的全部候选扩展名，从最长到最短。
// 例：foo.bar.js -> ['bar.js', 'js']；.gitignore -> ['gitignore']
// 之所以要有多个候选：图标主题里存在 map、bash_profile 这类多点后缀
function extCandidates(lower) {
  const out = [];
  let i = lower.indexOf('.');
  while (i !== -1) {
    const e = lower.slice(i + 1);
    if (e) out.push(e);
    i = lower.indexOf('.', i + 1);
  }
  return out;
}

// resolveIconId 按 VSCode 的解析顺序取图标 id：fileNames -> fileExtensions -> languageIds -> file
function resolveIconId(base, map) {
  const lower = base.toLowerCase();
  let id = map.fileNames[lower];
  if (!id) {
    for (const e of extCandidates(lower)) {
      const hit = map.fileExtensions[e];
      if (hit) {
        id = hit;
        break;
      }
    }
  }
  if (!id && langMap) {
    const exts = extCandidates(lower);
    let lang = langMap.byFileName[lower];
    if (!lang) {
      for (const e of exts) {
        lang = langMap.byExtension[e];
        if (lang) break;
      }
    }
    if (lang) id = map.languageIds[lang];
  }
  return id || map.file;
}

// iconGlyph 把图标定义里的 fontCharacter 转成字符。
// 文档里的写法是反斜杠加十六进制（形如 \E001），那是 VSCode 侧的转义表示，
// 到了 JSON 里就是普通的反斜杠加 4 位十六进制，必须按十六进制解析后再取私有区码位
function iconGlyph(def) {
  const m = /^\\([0-9A-Fa-f]{1,6})$/.exec((def && def.fontCharacter) || '');
  return m ? String.fromCodePoint(parseInt(m[1], 16)) : '';
}

// iconHTML 生成文件类型图标的 HTML。
// 颜色用图标文档里的 fontColor（Seti 为每种类型配了色），因此图标颜色是「类型色」，
// 与文件名、状态字母的「状态色」互不干扰——这正是 VSCode 里的观感
function iconHTML(path) {
  const map = prefersLight.matches ? iconLight : iconDark;
  if (!map) return '';
  const base = path.split('/').pop();
  let id = iconCache.get(base);
  if (id === undefined) {
    id = resolveIconId(base, map);
    iconCache.set(base, id);
  }
  const def = map.defs[id];
  const ch = iconGlyph(def);
  if (!ch) return '';
  const color = def && def.fontColor ? ' style="color:' + esc(def.fontColor) + '"' : '';
  return '<i class="seti"' + color + '>' + esc(ch) + '</i>';
}

// ——— 数据获取 ———

async function fetchRepos() {
  const res = await fetch('/api/repos', { headers: authHeaders, cache: 'no-store' });
  if (!res.ok) throw new Error('HTTP ' + res.status);
  return res.json();
}

// ——— 行结构 ———

// UI_GROUPS 是卡片内部的分组，顺序照 VSCode 的源码管理视图：未合并的在最前，
// 然后已暂存、然后未暂存。同一文件同时有暂存与未暂存改动时会在两组各出现一次——VSCode 就是这样，
// 而两组的行各自只提供自己那一侧的操作，语义因此是自洽的
const UI_GROUPS = [
  { id: 'merge', label: 'groupUnmerged', has: (f) => f.unmerged },
  { id: 'index', label: 'diffStaged', has: hasStaged },
  { id: 'work', label: 'diffUnstaged', has: hasWork },
];

// buildSpecs 把仓库列表摊平成行序列。顺序即渲染顺序，排序由服务端完成（排序规则只有一处实现）
//
// 干净的仓库只占一行（只有标题行，不额外补一行「工作区干净」说明）：
// 实测真实配置下 37 个仓库里有 33 个是干净的，若每个都补一行说明，
// 大半屏都在重复同一句话，而它的信息量等于零——也正是用户提出的痛点
//
// 分组是 VSCode 语义的照搬：每个仓库内部按 UI_GROUPS 分段，空分组不显示。
// 分组的行头也占一行（高度与其它行同为 22px），因此布局那套"行高即常量"的前提不受影响
function buildSpecs(repos) {
  const specs = [];
  for (const repo of repos) {
    specs.push({ key: repo.path + '\u0000h', kind: 'head', repo });

    if (repo.error) {
      // 采集失败的仓库必须显式说明失败，不能显示成「工作区干净」
      specs.push({ key: repo.path + '\u0000e', kind: 'note', repo, text: t('failed') + ': ' + repo.error });
    } else {
      for (const group of UI_GROUPS) {
        const files = repo.files.filter(group.has);
        if (files.length === 0) continue; // 空分组不显示（VSCode 也不显示）
        specs.push({ key: repo.path + '\u0000g\u0000' + group.id, kind: 'group', repo, group, count: files.length });
        for (const f of files) {
          // 行的 key 用"分组 + 文件路径"而不是下标：文件增删时其余行的元素还能被复用，
          // 用下标的话一次插入就让后面所有行的 key 全变、全部重建、动画全丢。
          // 而带上分组是必须的——同一个文件会在两组各出现一次，不带分组的 key 会让两组抢同一个元素
          specs.push({
            key: repo.path + '\u0000g\u0000' + group.id + '\u0000f\u0000' + f.path,
            kind: 'file',
            repo,
            group,
            file: f,
          });
        }
      }
    }
    // 干净仓库不再补说明行：实时状态已经在标题行上（分支、领先/落后、游离 HEAD、尚无提交）

    // 标记卡片末行：CSS 靠它画下边框与圆角收口。跨列被切断的卡片，其上一段的末行不带这个标记，
    // 视觉上自然表现为「还没结束，下接另一列」
    specs[specs.length - 1].foot = true;
  }
  return specs;
}

// rowHTML 生成一行的内容。
function rowHTML(s) {
  if (s.kind === 'head') {
    const r = s.repo;
    const parts = ['<span class="name">' + esc(r.name) + '</span>'];

    if (r.error) {
      parts.push('<span class="badge err">!</span>');
    } else if (r.files.length > 0) {
      parts.push('<span class="badge">' + r.files.length + '</span>');
    } else if (r.ahead > 0) {
      parts.push('<span class="badge">' + t('ahead', { n: r.ahead }) + '</span>');
    }

    // 分支与待办写在标题行右侧，字体更小、颜色更淡——与 VSCode 的仓库标题只显示名字不同，
    // 多仓库看板需要在一行内同时说清「哪个分支、有没有没推的东西」
    const meta = [];
    if (r.noCommits) meta.push(t('noCommits'));
    else if (r.detached) meta.push(esc(r.branch || t('detached')));
    else if (r.branch) meta.push(esc(r.branch));
    if (r.upstream && !r.noCommits) {
      if (r.ahead > 0) meta.push(t('ahead', { n: r.ahead }));
      if (r.behind > 0) meta.push(t('behind', { n: r.behind }));
    }
    if (meta.length) parts.push('<span class="branch">' + meta.join(' · ') + '</span>');
    return parts.join('');
  }

  if (s.kind === 'group') {
    // 分组行头：组名 + 该组的文件数，外加"整组动作"——未暂存那组给 +（暂存全部），
    // 已暂存那组给 −（全部取消暂存）。
    // 未合并组刻意不给按钮：冲突得由人来分辨，批量暂存会把还带着冲突标记的文件一起 stage 进去
    // （与单个文件那个按钮同一套理由），而"点一下全部暂存"恰恰是最容易误触的操作
    let action = '';
    if (s.group.id === 'work') {
      action = '<button class="act always" type="button" data-act="stage-all" title="' +
        esc(t('stageAll')) + '">+</button>';
    } else if (s.group.id === 'index') {
      action = '<button class="act always" type="button" data-act="unstage-all" title="' +
        esc(t('unstageAll')) + '">−</button>';
    }
    return '<span class="group-name">' + esc(t(s.group.label)) + '</span>' +
      (action ? '<span class="acts">' + action + '</span>' : '') +
      '<span class="badge">' + s.count + '</span>';
  }

  if (s.kind === 'file') {
    const f = s.file;
    const side = s.group ? s.group.id : 'work';
    const st = fileStatus(f, side);
    // 路径拆成「文件名 + 目录」两部分：文件名用状态色，目录用更淡的色。
    // 与 VSCode 在列表模式下把路径作为 description 淡化显示一致，也让同类文件名更容易对齐扫读
    const slash = f.path.lastIndexOf('/');
    const base = slash === -1 ? f.path : f.path.slice(slash + 1);
    const dir = slash === -1 ? '' : f.path.slice(0, slash);
    // 行尾的动作按钮：只给"属于这一组"的那一个。未暂存组给「暂存」，已暂存组给「取消暂存」，
    // 未合并组两个都不给（冲突要人来解决，后端也会拒绝）——与 VSCode 的行内动作一致
    let acts = '';
    if (side === 'work') {
      acts = '<span class="acts"><button class="act" type="button" data-act="stage" title="' +
        esc(t('stage')) + '">+</button></span>';
    } else if (side === 'index') {
      acts = '<span class="acts"><button class="act" type="button" data-act="unstage" title="' +
        esc(t('unstage')) + '">−</button></span>';
    }
    return (
      iconHTML(base) +
      // 目录用 .dir 而不是 .branch：.branch 的规则只作用于仓库标题行（.row.head .branch），
      // 用在文件行上会匹配不到任何规则，目录便继承了文件名的状态色——而两处注释都写明
      // 目录应当比文件名更淡。这是一处类名与规则名对不上的笔误，颜色上的表现是"路径整段同色"
      '<span class="path">' + esc(base) + (dir ? '<span class="dir"> ' + esc(dir) + '</span>' : '') + '</span>' +
      '<span class="letter">' + esc(st.letter) + '</span>' +
      acts
    );
  }

  // 说明行（例如"采集失败: <git 的原话>"）的正文要放进一个盒子里：flex 容器里的匿名文本项
  // 不响应 text-overflow，长报错会被硬裁掉、连省略号都没有
  return '<span class="text">' + esc(s.text || '') + '</span>';
}

// reconcile 把行元素调整到与 specs 一致，尽量复用已有元素。
// 返回与 specs 一一对应的元素数组，供布局使用
function reconcile(specs) {
  const next = new Map();
  const els = [];
  // 每次渲染重置：上一轮的新行在 layout 之后已经交出去了（见 layout 末尾）
  newRows = [];

  for (const s of specs) {
    let el = rowEls.get(s.key);
    let isNew = false;
    if (!el) {
      el = document.createElement('div');
      board.appendChild(el);
      isNew = true;
    }

    const cls = ['row', s.kind];
    if (s.foot) cls.push('foot');
    // 分组类：只用来给「已暂存 / 未暂存」两组铺不同的半透明底色（见 style.css）。
    // 状态类（st-*）负责文件自己的字母与文字颜色，两者互不干扰
    if (s.group) cls.push('g-' + s.group.id);
    if (s.file) cls.push(fileStatus(s.file, s.group ? s.group.id : 'work').cls);
    if (isNew) {
      // 新行在首次定位前必须关掉过渡：行是绝对定位的，刚建出来时 transform 是 none
      // （即页面左上角），而它的真实坐标要等 layout() 才写入，带着过渡就会「从左上角飞过来」
      // ——暂存一个文件后，新出现在另一组里的那一行正是这么飞的
      cls.push('no-anim');
      newRows.push(el);
    }
    if (el.className !== cls.join(' ')) el.className = cls.join(' ');

    const html = rowHTML(s);
    // 只在内容真的变了才写 innerHTML：无条件写入会让浏览器丢弃并重建子树，
    // 正在进行的 transition 会被打断，动画表现为一顿一顿
    if (el.__html !== html) {
      el.innerHTML = html;
      el.__html = html;
    }

    next.set(s.key, el);
    // 元素与它当前对应的行数据挂钩：元素是复用的，数据每次渲染都在换，
    // 点击时（事件委托）只有从这里才能拿到"这一行是哪个仓库的哪个文件"
    el.__spec = s;
    els.push(el);
  }

  // 消失的行直接移除（元素已不在视图中，没有值得保留的过渡）
  for (const [key, el] of rowEls) {
    if (!next.has(key)) el.remove();
  }

  rowEls = next;
  return els;
}

// ——— 布局 ———

// layout 按顺序把行填进各列，列高恒等于视口高。
//
// 三处规则来自明确的用户诉求：
//   - 卡片开头若剩余空间不足（放不下标题与几行内容），整卡顺延到下一列，避免标题孤零零留在列尾
//   - 卡片中途被列边界截断时，在新列顶部插入续段标识并预留其高度，让续段能认出属于哪个仓库
//   - 列填满就向右开新列，页面只横向滚动
//
// 性能：本函数既不读布局属性，也不往已布局的容器里逐个插元素——这两件事都会让浏览器
// 立刻结算当时积压的样式，把本该 O(行数) 的活变成 O(行数²)。下面两条都是踩出来的，
// 修法不是"优化"，而是拿掉触发点：
//
//   1) 行高曾经在循环里读 el.offsetHeight。写一次 transform 就让浏览器把待结算的样式算一遍，
//      紧接着的读取又强制它立刻结算，于是每行都真的重排一次。而三种行（标题行、文件/说明行、
//      续段标识）的高度各有一个 CSS 变量、且都已在函数开头读入，行内纵向溢出又被 overflow:
//      hidden 裁掉，行高恒等于变量值，直接取变量即可。
//      这是一条必须维护的不变量：将来若有哪种行的高度会随内容变化（例如允许长文件名折行），
//      就不能再把行高当常量用，得先量一次再算位置
//   2) 续段标识曾经在循环里逐个 board.appendChild。插入新元素同样会结算积压的样式，而当时
//      正压着几万条待写的 transform，插入次数一多就退化成 O(插入数 × 行数)。现在改为在
//      脱离文档的 fragment 里建好、循环结束后由 contLayer 一次 replaceChildren 换入
//
// 实测（本机、同一份数据、修前修后行坐标逐条一致）：
//   4020 行    每次重排 780ms -> 11ms
//   24021 行   360ms -> 21~25ms（首屏之后还有一次约 100ms 的结算，属于热身，不是每次重排的代价）
// 此前记录过的最坏情况是 20332 行 88.7 秒（一次忘记写 .gitignore 的 node_modules 就足以触发），
// 而轮询与窗口缩放都会重跑这个函数
function layout(els, specs) {
  const colW = cssVar('--col-w', 320);
  const headH = cssVar('--head-h', 22);
  const rowH = cssVar('--row-h', 22);
  const contH = cssVar('--cont-h', 22);
  // 列高 = 视口高 − 页面上下留白 − 底栏高度。三个数字都从 CSS 变量读（cssVar 见上），
  // 不在这里自己写死：底栏是后加的，硬编码的话它一出现就会压住最下面一行卡片，
  // 而"JS 里一份、CSS 里一份"的数字迟早会漂移
  // 视口高走 viewportSize 而不是 window.innerHeight：内容溢出时后者会被撑大，而看板的高度又是
  // 按它算出来写回元素的，那正是"看板把自己撑高"的闭环（见 viewportSize 的注释）
  const colH = viewportSize().h - cssVar('--page-pad', 16) * 2 - cssVar('--statusbar-h', 30);

  // tailRows[i] 是第 i 条标题行之后、属于同一张卡片的内容行数，用来判断
  // 「列尾还值不值得起一张新卡片」。
  // 原实现对所有卡片一律要求「标题 + 三行内容」（88px），于是「干净仓库」这种本来
  // 只有一行标题的卡片也被要求 88px：列尾明明还放得下它（实测 92px），却提前换列，
  // 左列因此只填到 956/1048px。按卡片自己的行数算之后，没有内容行的卡片只需一行的高度
  const tailRows = new Array(specs.length).fill(0);
  for (let i = specs.length - 1, n = 0; i >= 0; i--) {
    if (specs[i].kind === 'head') { tailRows[i] = n; n = 0; } else n++;
  }

  // 续段标识每次布局整批重建：先在脱离文档的 fragment 里全部建好，最后一次性换入。
  // 为什么不在循环里逐个 appendChild：往已布局的容器里插入一个新元素，会让浏览器把当时
  // 积压的样式一并结算，而循环里正压着几万条待写的 transform，于是每次插入都要付一次
  // O(已写行数)——实测 750 次插入把 24000 行的重排从 20ms 抬到 360ms。
  // 建在 fragment 上则完全不碰文档，代价恒定
  const conts = document.createDocumentFragment();

  let col = 0;
  let used = 0;
  let prevRepo = null;
  const segments = new Map();

  for (let i = 0; i < specs.length; i++) {
    const s = specs[i];
    const el = els[i];

    // 同一张卡片内部行行相连；换卡片时若不在列首，先让出卡片间距
    if (s.repo.path !== prevRepo && used > 0) used += GAP;
    prevRepo = s.repo.path;

    // 列尾放不下「标题 + 这张卡片最多三行内容」就整卡顺延。上限取三行是为了避免
    // 标题孤零零留在列尾，但下限必须按卡片自己的行数来，否则单行卡片会被白白推走
    if (s.kind === 'head' && used > 0 && colH - used < headH + Math.min(tailRows[i], 3) * rowH) {
      col++;
      used = 0;
    }

    // 行高取自 CSS 变量而不是 el.offsetHeight，理由见函数头的性能说明
    const h = s.kind === 'head' ? headH : rowH;

    if (used + h > colH && used > 0) {
      col++;
      used = 0;
      if (s.kind !== 'head') {
        const n = (segments.get(s.repo.path) || 1) + 1;
        segments.set(s.repo.path, n);

        const cont = document.createElement('div');
        cont.className = 'cont';
        cont.textContent = t('continued', { name: s.repo.name, n });
        cont.style.transform = 'translate(' + col * (colW + GAP) + 'px, 0px)';
        conts.appendChild(cont);

        used = contH;
      }
    }

    el.style.transform = 'translate(' + col * (colW + GAP) + 'px, ' + used + 'px)';
    used += h;
  }

  // 一次性换掉全部续段标识（顺带清掉上一轮的）
  contLayer.replaceChildren(conts);
  board.style.width = (col + 1) * (colW + GAP) + 'px';
  board.style.height = colH + 'px';

  // 新行的坐标已经写入，下一帧再把过渡打开：同一帧里「关过渡 → 写坐标 → 开过渡」会被浏览器
  // 合并成一次样式结算，等于没关，动画照样从左上角起飞（must 经过一次真正的样式结算才行）
  if (newRows.length) {
    const pending = newRows;
    newRows = [];
    requestAnimationFrame(() => {
      for (const el of pending) el.classList.remove('no-anim');
    });
  }
}

// ——— diff 视图 ———
//
// 覆盖整页打开某个仓库或某个文件的改动（对齐结论：不做语法高亮、分「已暂存 / 未暂存」两段、
// 覆盖整页替换看板、Esc 或返回键回看板）。

// diffOpen 为真表示覆盖层正打开。它同时关掉三件事，各自的理由不同：
//   - 轮询：看板被盖住，刷了也没人看，而每次刷新都要让服务端为每个仓库起一趟 git
//   - resize 重排：看板仍是"被盖住但仍在布局中"，尺寸没有变化，重排纯属白花
//   - 滚轮转横向：覆盖层里滚轮应当滚动 diff 正文，被抢去滚看板会让 diff 滚不动
let diffOpen = false;
// 打开前的横向滚动位置。看板在覆盖层关闭后要回到用户刚才看的那一列，
// 否则关掉 diff 会莫名跳回最左
let diffScrollX = 0;
// 每次打开的序号：响应回来时用它丢弃"用户已经关掉或换了目标"的那次结果
let diffSeq = 0;

// syncScrollLock 统一决定要不要锁住页面滚动，并顺带切换遮罩层。
// 遮罩与滚动锁由同一处决定：两件事都取决于"有没有浮层开着"，分头写迟早会出现
// "层关了、模糊还在"这种半截状态
function syncScrollLock() {
  const overlayOpen = diffOpen || cardOpen;
  document.documentElement.style.overflow = overlayOpen ? 'hidden' : '';
  document.body.classList.toggle('overlay-open', overlayOpen);
}

// resume 在两个浮层都关掉之后恢复看板：补一次取数（期间工作区可能已经变了）并恢复轮询
function resume() {
  if (diffOpen || cardOpen) return;
  refresh();
  startPolling();
}

// diffLineClass 按行首字符判定这一行属于哪一类。
// 必须先判元信息再判 +/-：diff --git、index、---、+++ 全都以 - 或 + 开头，
// 顺序写反会把它们染成增删色，页面看起来像多改了几行
function diffLineClass(line) {
  if (
    line.startsWith('diff --git ') ||
    line.startsWith('index ') ||
    line.startsWith('--- ') ||
    line.startsWith('+++ ') ||
    line.startsWith('new file mode ') ||
    line.startsWith('deleted file mode ') ||
    line.startsWith('old mode ') ||
    line.startsWith('new mode ') ||
    line.startsWith('similarity index ') ||
    line.startsWith('rename ') ||
    line.startsWith('copy ') ||
    line.startsWith('\\') // "\ No newline at end of file"
  ) {
    return 'meta';
  }
  if (line.startsWith('@@')) return 'hunk';
  if (line.startsWith('+')) return 'add';
  if (line.startsWith('-')) return 'del';
  return '';
}

// diffHTML 把一份统一 diff 转成用于 <pre> 的 HTML：按行首字符着色，
// 并把连续同类行合并进同一个元素。
//
// 为什么合并：一次忘记写 .gitignore 就能产生几万行新增，按行建元素会让浏览器为几万个节点
// 排版（本视图没有虚拟滚动）。合并后典型的增删块只有个位数节点，而视觉上完全一致
function diffHTML(text, allAdded) {
  const lines = text.split('\n');
  // 统一 diff 与文件正文都以换行结尾，split 会多出一个空串；不去掉它，末尾就会多出一条空行
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();

  const groups = [];
  for (const line of lines) {
    // allAdded 用于未跟踪文件：那边拿到的是文件正文而不是 diff，每一行都是新增
    const cls = allAdded ? 'add' : diffLineClass(line);
    const last = groups[groups.length - 1];
    if (last && last.cls === cls) last.lines.push(line);
    else groups.push({ cls: cls, lines: [line] });
  }

  return groups
    .map((g) => '<span class="dl' + (g.cls ? ' ' + g.cls : '') + '">' + esc(g.lines.join('\n')) + '</span>')
    .join('');
}

// renderDiff 把 /api/diff 的响应画进覆盖层。
// repo 与 file 只用于标题，内容一律来自响应——页面不猜"应该有哪些改动"
function renderDiff(repo, file, out) {
  const blocks = [];

  if (out.error) {
    blocks.push('<p class="diff-note">' + esc(t('diffFailed', { err: out.error })) + '</p>');
    diffBodyEl.innerHTML = blocks.join('');
    return;
  }

  // 四条提示都放在正文之前：它们说明"下面的内容为什么长这样或为什么不完整"，
  // 放在末尾会被长 diff 推到看不见的地方
  if (out.untracked) blocks.push('<p class="diff-note">' + esc(t('diffUntracked')) + '</p>');
  if (out.unmerged) blocks.push('<p class="diff-note">' + esc(t('diffUnmerged')) + '</p>');
  if (out.binary) blocks.push('<p class="diff-note">' + esc(t('diffBinary')) + '</p>');
  if (out.truncated) blocks.push('<p class="diff-note">' + esc(t('diffTruncated')) + '</p>');

  const sections = [];
  if (out.staged) sections.push({ title: t('diffStaged'), html: diffHTML(out.staged, false) });
  if (out.unstaged) {
    sections.push({ title: t('diffUnstaged'), html: diffHTML(out.unstaged, !!out.untracked) });
  }
  for (const s of sections) {
    blocks.push('<section><h2>' + esc(s.title) + '</h2><pre class="diff">' + s.html + '</pre></section>');
  }

  // 只在"既没有分段也没有提示"时才是真的没有改动：二进制未跟踪文件就是这种情形，
  // 它有提示、正文为空，此时说一句"没有改动"会与提示自相矛盾
  if (sections.length === 0 && blocks.length === 0) {
    blocks.push('<p class="diff-note">' + esc(t('diffEmpty')) + '</p>');
  }

  diffBodyEl.innerHTML = blocks.join('');
}

// diffSpec 是覆盖层当前展示的那一行（仓库 + 可选文件）。
// 提交或推送之后要重取一次 diff（暂存区变了，两段内容就跟着变），靠它重建请求
let diffSpec = null;

// draftMsg 按仓库暂存提交信息草稿。
// 为什么需要它：写好了又去翻看看板、或切到另一个仓库再回来，信息不该丢；
// 而把信息留在同一个输入框里不管，则会串仓——给 A 写的信息被提给了 B
const draftMsg = new Map();

// loadDiff 按 diffSpec 取一次 diff 并渲染。
// 与 openDiff 分开，是因为写操作之后要"重取同一份"而不该重走一遍打开的副作用
// （改标题、抢焦点、重置滚动位置）
async function loadDiff() {
  const spec = diffSpec;
  const seq = ++diffSeq;
  diffBodyEl.textContent = t('diffLoading');

  const params = new URLSearchParams({ repo: spec.repo.path });
  if (spec.file) params.set('file', spec.file.path);

  let out;
  try {
    const res = await fetch('/api/diff?' + params.toString(), { headers: authHeaders, cache: 'no-store' });
    out = await res.json();
    if (!res.ok && !out.error) out = { error: 'HTTP ' + res.status };
  } catch (err) {
    out = { error: err.message };
  }

  // 用户在响应到达之前按了 Esc（或又点开了别的）就把这次结果丢掉，
  // 否则会出现"已经回到看板却又被旧结果写了一次"
  if (seq !== diffSeq || !diffOpen || spec !== diffSpec) return;
  renderDiff(spec.repo, spec.file || null, out);
}

// openDiff 打开某个仓库（file 为空 → 整个仓库）或某个文件的 diff。
// 异步：本地接口也有一次往返，期间先显示"加载中"，避免看起来是点了没反应
async function openDiff(spec) {
  const repo = spec.repo;
  const file = spec.file || null;
  diffSpec = spec;

  // 标题只用我们已经知道的信息（仓库名、文件路径），不必等接口回来才显示
  const shown = file ? (file.origPath ? file.origPath + ' → ' + file.path : file.path) : '';
  diffTitleEl.innerHTML =
    '<span>' + esc(repo.name) + '</span>' + (shown ? '<span class="dir"> ' + esc(shown) + '</span>' : '');
  // 上一次的写操作结果属于上一个仓库，不该带到这次来
  setOp('');
  commitMsgEl.value = draftMsg.get(repo.path) || '';

  diffOpen = true;
  diffScrollX = window.scrollX;
  diffEl.classList.add('open');
  diffEl.setAttribute('aria-hidden', 'false');
  // 看板仍是布局中的元素，只是被盖住；不锁住 html 的话滚轮与方向键还能滚它
  syncScrollLock();
  stopPolling();
  diffBackEl.focus();

  await loadDiff();
}

// closeDiff 关闭覆盖层并恢复看板。
function closeDiff() {
  if (!diffOpen) return;
  diffOpen = false;
  diffSeq++; // 作废可能还在路上的那次响应
  diffEl.classList.remove('open');
  diffEl.setAttribute('aria-hidden', 'true');
  syncScrollLock();
  diffBodyEl.textContent = ''; // 释放大 diff 占用的 DOM
  window.scrollTo(diffScrollX, 0);
  // 覆盖层期间没有刷新过看板，关闭时补一次再恢复轮询（期间工作区可能已经变了）
  resume();
}

// ——— 通知 ———
//
// 为什么要有这套东西：写操作的结果原来写在 #op、#graph-op、#diff-op 三行文字里，而这三行是
// "最近一次结果的占位"——下一次写操作、下一次重绘都会把它覆盖掉。用户点完推送，成功信息出现
// 不到一秒就被别的东西冲掉，原话是"一个成功的信息一闪而过，这肯定是不合适的"。
//
// 因此通知不走三行文字那条路：它有独立的容器与独立的列表，不与任何会重绘的元素抢位置，
// 也就不会被冲掉；并且刻意不设定时器，只有用户点关闭才消失——抱怨的根源就是"还没看清就没了"，
// 再挂一个自动消失只是把同一个问题换个地方重现。
//
// 三行文字因此只留给"需要一直可见的持续状态"：取数失败（要一直看着才知道仓库读不出来）、
// 续取上限提示（要一直提醒为什么滚不到更早的提交）、拉取进行中的"正在拉取…"
// （网络操作期间的状态，结束时由结果通知接手）。一次性动作的结果一律走通知

// 通知在内存里的上限。为什么要上限：页面开着不动也会一直攒，上限保证内存与面板高度都是常数级。
// 超限丢最旧的——用户要找的是刚发生的那几条
const NOTIF_MAX = 50;

// notifItems 是通知的唯一数据源，按发生顺序（旧 -> 新）存放，右下角的堆叠与面板里的历史
// 都从它渲染，不各自维护一份。每项含：
//   text / level / at  正文、严重度（info|warn|error）、发生时间
//   el                 右下角那一条的元素（收起或清除后置空），挂在数据上是为了让
//                      "追加一条"不必重建已有元素，也不会让屏幕上的通知重放入场动画
//   dismissed          是否已经被用户收起过：收起只是关掉提示，条目仍留在历史里
const notifItems = [];

// 未读数：右下角堆叠上出现一条不等于用户在面板里看过，因此单独记一个数，
// 打开面板时清零
let notifUnread = 0;

// notifCenterOpen 记录面板开着没有：开着时新通知直接算已读（用户正看着面板），
// 也用于"点面板外面就收起"这条判断
let notifCenterOpen = false;

// 三种严重度的图标。用内联 SVG 而不是字符：图标要跟着主题的严重度前景色走，
// 而现成的方块字字形在各系统上粗细不一。fill/stroke 一律 currentColor，颜色仍只有一个来源
const NOTIF_ICONS = {
  info:
    '<circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" stroke-width="1.5"/>' +
    '<circle cx="8" cy="4.9" r="1" fill="currentColor"/>' +
    '<rect x="7.2" y="6.9" width="1.6" height="4.6" fill="currentColor"/>',
  warn:
    '<path d="M8 2.2 14.6 13.4H1.4z" fill="none" stroke="currentColor" stroke-width="1.5"/>' +
    '<circle cx="8" cy="7" r="1" fill="currentColor"/>' +
    '<rect x="7.2" y="9" width="1.6" height="2.8" fill="currentColor"/>',
  error:
    '<circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" stroke-width="1.5"/>' +
    '<path d="M5.6 5.6 10.4 10.4M10.4 5.6 5.6 10.4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>',
};

// notifIconSvg 给出某个严重度的图标标记。
// 全是写死的字面量、不含任何外部数据，因此拼接后可以直接交给 innerHTML——
// 而正文来自 git 的原话，那条路必须走 textContent，否则仓库里一个 < 就能把页面结构破了
function notifIconSvg(level) {
  return (
    '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">' +
    NOTIF_ICONS[level] +
    '</svg>'
  );
}

// notifTime 把时间戳格式化成时分秒。
// 不走 toLocaleTimeString：它的输出随语言与系统设置变化，同一份数据在两台机器上显示不同，
// 断言与截图也就没法比对。通知都是本次会话里发生的，日期没有信息量
function notifTime(at) {
  const d = new Date(at);
  const pad = (n) => (n < 10 ? '0' + n : String(n));
  return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
}

// notifCloseButton 造一个关闭小按钮。两处（单条通知与面板里的每一行）共用，
// 因此按钮的样式类、字形与无障碍标签只在这里写一次；点下去做什么由调用方给，
// 因为这两处的语义并不一样（收起提示 vs 从历史里清除）
function notifCloseButton(labelKey, onClick) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'icon-btn';
  btn.textContent = '×';
  btn.setAttribute('aria-label', t(labelKey));
  btn.addEventListener('click', (e) => {
    // 挡掉冒泡：清除会把这个按钮自己（或它所在的那一行）从 DOM 里摘掉，而页面级的
    // "点别处就收起面板"是靠 notifCenterEl.contains(e.target) 判断的——按钮已脱离文档时
    // 那个判断必然为假，于是"清除一条"会顺带把面板也关掉
    e.stopPropagation();
    onClick();
  });
  return btn;
}

// buildToast 造右下角那一条。结构与面板里的一行刻意不同：堆叠上不需要时间戳
// （刚发生的事，时间没有信息量），面板里才需要
function buildToast(item) {
  const el = document.createElement('div');
  el.className = 'notif notif--' + item.level;
  const icon = document.createElement('span');
  icon.className = 'notif-icon';
  icon.innerHTML = notifIconSvg(item.level);
  const text = document.createElement('span');
  text.className = 'notif-text';
  text.textContent = item.text;
  el.append(icon, text, notifCloseButton('notifClose', () => dismissToast(item)));
  return el;
}

// renderToasts 让右下角的堆叠与 notifItems 对齐。
//
// 为什么不每次整块重建：重建会让已经在屏幕上的通知重放入场动画，看起来像一起抖了一下。
// 元素挂在 item.el 上，因此"同一条"跨渲染还认得出来；已在容器里的元素重新 append 只是移动位置，
// 不会重放动画
function renderToasts() {
  for (const item of notifItems) {
    // 收起过的条目不再回到屏幕上，但它还在历史里（见 dismissToast）
    if (item.dismissed) continue;
    if (!item.el) item.el = buildToast(item);
    notificationsEl.appendChild(item.el);
  }
  // 清场：超限丢掉的那些、以及被清除掉的，元素都该离开容器
  const keep = new Set(notifItems.filter((it) => !it.dismissed).map((it) => it.el));
  for (const el of Array.from(notificationsEl.children)) {
    if (!keep.has(el)) el.remove();
  }
}

// notify 记一条通知并摆到右下角，返回这条通知（调用方一般不需要）。
//
// severity 只认 info/warn/error 三档，落在这三档之外的按 info 处理：
// 三档对应 VSCode 的 editorInfo/editorWarning/editorError 三个语义色，
// 见 cmd/ui_theme.go 的 status-info / status-warn / status-error
function notify(text, severity) {
  const s = text === undefined || text === null ? '' : String(text);
  // 空结果不入列：git 成功时可能什么都不输出，此时摆一条空通知只会是一个看不懂的框
  if (!s) return null;
  const level = NOTIF_ICONS[severity] ? severity : 'info';
  const item = { text: s, level, at: Date.now() };
  notifItems.push(item);
  while (notifItems.length > NOTIF_MAX) {
    const dropped = notifItems.shift();
    if (dropped.el) {
      dropped.el.remove();
      dropped.el = null;
    }
  }
  renderToasts();
  // 面板开着的时候用户正看着，直接算已读；否则累加未读并在铃铛上显示
  if (notifCenterOpen) renderNotifCenter();
  else {
    notifUnread++;
    renderBell();
  }
  return item;
}

// notifFromResult 是 runWrite 那个"显示结果"回调的适配层：
// 把它的 (文本, 是否出错) 翻成一条通知
function notifFromResult(text, isError) {
  notify(text, isError ? 'error' : 'info');
}

// dismissToast 只收起右下角那一条，条目本身留在历史里。
// 与"清除"分开是有意的（VSCode 也是这么分的）：收起弹出来的提示，往往只表示"我看过了、别挡着"，
// 不等于要把这条记录从历史里抹掉——事后回到通知中心还要能翻到它。
// 这也是本文件里 dismissed 与 removeNotif 两套动作并存的原因
function dismissToast(item) {
  if (item.el) {
    item.el.remove();
    item.el = null;
  }
  item.dismissed = true;
}

// removeNotif 从历史里彻底清掉一条：面板里那一行的 × 与"全部清除"走这条。
// 还在屏幕上的那条提示也要一起收掉——同一个通知不该在历史里没了、提示还挂着
//
// 为什么按对象而不是按下标：面板里每一行的按钮闭包拿着的是点击那一刻的那条通知，
// 而列表在此期间可能已经因为新通知或别处清除而移位，用下标会删错行
function removeNotif(item) {
  const i = notifItems.indexOf(item);
  if (i === -1) return;
  notifItems.splice(i, 1);
  if (item.el) {
    item.el.remove();
    item.el = null;
  }
  renderToasts();
  renderBell();
  renderNotifCenter();
}

// renderBell 刷新铃铛上的未读数与无障碍标签。
// 标签里带上条数：只靠一个数字，屏幕阅读器用户不知道那是什么的计数
function renderBell() {
  bellCountEl.textContent = String(notifUnread);
  bellCountEl.hidden = notifUnread === 0;
  bellEl.setAttribute('aria-label', notifUnread > 0 ? t('notifBell') + ' (' + notifUnread + ')' : t('notifBell'));
}

// buildNotifRow 造面板里的一行：图标、正文、时间、清除按钮。
// 正文与时间分两行而不是并排：git 的原话可能很长，并排会把时间挤出可视区
function buildNotifRow(item) {
  const row = document.createElement('div');
  row.className = 'notif-row';
  const icon = document.createElement('span');
  icon.className = 'notif-icon';
  icon.innerHTML = notifIconSvg(item.level);
  const body = document.createElement('div');
  body.className = 'notif-row-body';
  const text = document.createElement('div');
  text.className = 'notif-row-text';
  text.textContent = item.text;
  const time = document.createElement('div');
  time.className = 'notif-row-time';
  time.textContent = notifTime(item.at);
  body.append(text, time);
  row.append(icon, body, notifCloseButton('notifClear', () => removeNotif(item)));
  return row;
}

// renderNotifCenter 重建面板内容。
// 面板只在打开与增删时重建（不像看板那样每 5 秒一轮），因此不必复用元素
function renderNotifCenter() {
  notifTitleEl.textContent = t('notifTitle');
  notifClearAllEl.textContent = t('notifClearAll');
  // 一条都没有时"全部清除"没有意义，置灰而不是藏起来——位置固定，不会让标题行跳动
  notifClearAllEl.disabled = notifItems.length === 0;
  notifEmptyEl.textContent = t('notifEmpty');
  notifEmptyEl.hidden = notifItems.length > 0;

  const frag = document.createDocumentFragment();
  // 时间倒序：最近发生的排最前
  for (let i = notifItems.length - 1; i >= 0; i--) frag.appendChild(buildNotifRow(notifItems[i]));
  notifListEl.replaceChildren(frag);
}

// openNotifCenter / closeNotifCenter 管面板的开合。
// 焦点在两处要做交接：打开时进面板（否则键盘用户点开铃铛后 Tab 还得从头走一遍），
// 收起时回铃铛（否则焦点掉在 body 上，下一次 Tab 从页首开始）
function openNotifCenter() {
  notifCenterOpen = true;
  notifCenterEl.hidden = false;
  // 打开即视为已读：面板里已经能看到全部内容，铃铛上再挂个数字只会让人以为还有没看的
  notifUnread = 0;
  renderBell();
  renderNotifCenter();
  const first = notifListEl.querySelector('.icon-btn');
  if (first) first.focus();
  else if (!notifClearAllEl.disabled) notifClearAllEl.focus();
}

function closeNotifCenter(returnFocus) {
  if (!notifCenterOpen) return;
  notifCenterOpen = false;
  notifCenterEl.hidden = true;
  if (returnFocus) bellEl.focus();
}

bellEl.addEventListener('click', () => {
  if (notifCenterOpen) closeNotifCenter(false);
  else openNotifCenter();
});

notifClearAllEl.addEventListener('click', () => {
  for (const item of notifItems) {
    if (item.el) {
      item.el.remove();
      item.el = null;
    }
  }
  notifItems.length = 0;
  notifUnread = 0;
  renderBell();
  renderNotifCenter();
});

// 点面板与铃铛之外的地方就收起：它是个浮层，不收的话会一直压着右下角的内容。
// 判据用 closest 而不是 target 相等，因为面板里还有子元素
document.addEventListener('click', (e) => {
  if (!notifCenterOpen) return;
  if (notifCenterEl.contains(e.target) || bellEl.contains(e.target)) return;
  closeNotifCenter(false);
});

// 首屏先把铃铛摆正：未读为 0（徽标隐藏），无障碍标签也要有——按钮里只有一个图形，
// 不设标签的话屏幕阅读器只会念出"按钮"
renderBell();

// ——— 写操作：暂存 / 取消暂存 / 提交 / 推送 ———

// writeBusy 为真时拒绝新的写操作：一次只让一个请求在飞。
// 行尾那两个按钮只有几个像素宽，连点两下的代价是发出两条 git 命令，其中一条必然失败，
// 弹出一条让人摸不着头脑的错误
let writeBusy = false;

// setOp / setBoardOp 各写一处持续状态行：覆盖层标题栏下的 #diff-op，与看板底栏里的 #op。
// 分两处而不是共用一处，是因为两者可能同时存在，共用一个元素会互相覆盖
//
// 注意它们现在只管"需要一直可见的状态"，不再管一次性动作的结果——那些改走右上角通知，
// 理由见上面通知那一段的说明。判断标准：这条信息在动作结束之后还有没有用？
// 有（取数失败、续取到上限、拉取还在进行中）就留在这里，没有就发一条通知
function setOp(text, isError) {
  diffOpEl.textContent = text || '';
  diffOpEl.classList.toggle('error', !!isError);
}
function setBoardOp(text, isError) {
  boardOpEl.textContent = text || '';
  boardOpEl.classList.toggle('error', !!isError);
}

// postJSON 发一个写请求。四个写端点都在 POST 上：基座只对非 GET/HEAD 做同源校验，
// 同源 fetch 会自动带上 Origin；token 仍走请求头，与读请求同一套
async function postJSON(path, body) {
  const headers = Object.assign({ 'Content-Type': 'application/json' }, authHeaders || {});
  const res = await fetch(path, { method: 'POST', headers, body: JSON.stringify(body) });
  const out = await res.json().catch(() => ({}));
  // 非 JSON 响应（基座直接回的 401/403 纯文本）也要能说出原因，否则页面只会显示"失败了"
  if (out.error === undefined && !res.ok) out.error = 'HTTP ' + res.status;
  return out;
}

// runWrite 是所有写操作的公共外壳：置忙 -> 发请求 -> 把结果交给调用方显示 -> 重取数据。
// 结果写到哪里由调用方决定（覆盖层里还是看板左下角），因为只有调用方知道
//
// 无论成败都重取：失败也可能已经改变了仓库状态（例如 push 已送达但退出码非零）
async function runWrite(path, body, show) {
  if (writeBusy) return null;
  writeBusy = true;
  board.classList.add('busy');
  // 一次只允许一个写操作在飞：把卡片上的写控件与底栏那个按钮一起置灰。
  // 分支选择器只在列表为空时保持禁用，别把它永久锁上
  const writable = [commitBtnEl, graphPushEl, graphSyncEl, graphFetchEl];
  for (const el of writable) el.disabled = true;
  graphBranchEl.disabled = true;
  fetchBtnEl.disabled = true;
  let out;
  try {
    out = await postJSON(path, body);
    // git 的原话优先：失败的说明（没有 upstream、没有可提交的内容）与成功的摘要
    // 都是用户判断"到底发生了什么"的唯一依据，页面不再自拟一套说法
    show(out.error || out.output || '', !!out.error);
  } catch (err) {
    out = { error: err.message };
    show(t('loadFailed', { err: err.message }), true);
  } finally {
    writeBusy = false;
    board.classList.remove('busy');
    for (const el of writable) el.disabled = false;
    graphBranchEl.disabled = graphBranchEl.options.length === 0;
    fetchBtnEl.disabled = false;
    refresh();
  }
  return out;
}

// applyFileAction 执行行内动作：单个文件的暂存 / 取消暂存，以及分组行头上的整组动作。
// 结果走通知：这类动作一行里可能连着点好几次，写在原位的话上一条必然被下一条冲掉
function applyFileAction(spec, action) {
  if (action === 'stage-all' || action === 'unstage-all') {
    return runWrite(
      action === 'stage-all' ? '/api/stage-all' : '/api/unstage-all',
      { repo: spec.repo.path },
      notifFromResult,
    );
  }
  return runWrite(
    action === 'stage' ? '/api/stage' : '/api/unstage',
    { repo: spec.repo.path, file: spec.file.path },
    notifFromResult,
  );
}

// fetchAll 拉取全部仓库的远程数据。
// 拉取是网络操作，几十个仓库可能要等好几秒：期间在底栏留一行"正在拉取…"，否则会让人以为
// 按钮没反应。这一行是持续状态——操作没结束就一直在，结束时无论成败都由下面的回调清掉，
// 因此留在底栏而不发通知：发成通知的话，操作结束后它会变成一条永远停在"正在拉取"的假消息
function fetchAll() {
  if (writeBusy) return;
  setBoardOp(t('fetching'), false);
  return runWrite('/api/fetch', {}, (text, isError) => {
    setBoardOp('', false);
    notifFromResult(text, isError);
  });
}

// fillThemeOptions 把一份主题清单填进某个下拉框。followLabel 非空时在最前面加一项空值选项——
// 那是主选择器要的"跟随系统"；两个偏好下拉框不要它，它们本身就是"跟随系统时用哪套"
function fillThemeOptions(sel, groups, followLabel) {
  sel.textContent = '';
  if (followLabel) {
    const follow = document.createElement('option');
    follow.value = '';
    follow.textContent = followLabel;
    sel.appendChild(follow);
  }
  for (const group of groups || []) {
    const optgroup = document.createElement('optgroup');
    optgroup.label = group.label || t('themeMine');
    for (const item of group.themes || []) {
      const option = document.createElement('option');
      option.value = item.id;
      option.textContent = item.name;
      optgroup.appendChild(option);
    }
    sel.appendChild(optgroup);
  }
  sel.disabled = false;
}

// buildThemeSelect 按服务端注入的清单搭出三个选择器：主选择器（含"跟随系统"）与两个偏好
// （跟随系统时分别用哪套深色/浅色，对应 VSCode 的 preferredDark/LightColorTheme）。
//
// 分组标题与"跟随系统"都是页面文案，因此在这里按当前语言给——服务端那份清单里，
// 用户自己那组的标题刻意留空，就是交给这里填（页面文案不走 Go 的 l10n 管线）
function buildThemeSelect() {
  const data = window.__GGT_THEME__ && typeof window.__GGT_THEME__ === 'object' ? window.__GGT_THEME__ : {};

  fillThemeOptions(themeSelectEl, data.groups, t('themeFollow'));
  themeSelectEl.value = data.current || '';
  themeSelectEl.setAttribute('aria-label', t('theme'));

  fillThemeOptions(themeDarkEl, data.groups, '');
  themeDarkEl.value = data.dark || '';
  themeDarkEl.setAttribute('aria-label', t('themeDarkPrefLabel'));

  fillThemeOptions(themeLightEl, data.groups, '');
  themeLightEl.value = data.light || '';
  themeLightEl.setAttribute('aria-label', t('themeLightPrefLabel'));

  // 两个偏好只在"跟随系统"下有意义：不跟随时把它们收起（VSCode 里这两个设置始终可见、
  // 只是被忽略，而底栏空间有限，按依赖关系收起更省地方，也让"它们为什么在这里"不言自明）
  const following = !(data.current || '');
  themeDarkEl.hidden = !following;
  themeLightEl.hidden = !following;
}

// 换主题：把选择写进配置文件，然后整页重载。
//
// 为什么重载而不是就地改 CSS 变量：配色是服务端渲染 index.html 时注入的，就地改就等于让
// 客户端再实现一遍主题解析（重新取颜色、自己拼变量），同一件事两处实现迟早漂移。
// 重载的代价只是一次本地请求，而换主题本来就是低频动作
//
// 结果走通知与其他写操作一致，但要知道一处局限：成功之后紧跟着就是重载，那条通知会随页面
// 一起消失，因此"换主题成功"这件事在界面上仍然看不到（失败时不会重载，通知会一直留着，
// 这是本次真正的改善）。要让成功也看得见，只能把通知存到内存之外再在重载后取回，
// 而那与"通知只存在内存里"这条约束直接冲突，故本轮不做，见提交说明里的取舍
themeSelectEl.addEventListener('change', async () => {
  const out = await runWrite('/api/theme', { key: 'theme', value: themeSelectEl.value }, notifFromResult);
  if (!out || out.error) {
    buildThemeSelect(); // 没写成就把选择器拨回当前生效的那套，别让它显示一个没生效的值
    return;
  }
  location.reload();
});

// 两个偏好下拉框走同一条路，只是写的是不同的配置键（/api/theme 的 key 参数）
for (const [sel, key] of [
  [themeDarkEl, 'theme_dark'],
  [themeLightEl, 'theme_light'],
]) {
  sel.addEventListener('change', async () => {
    const out = await runWrite('/api/theme', { key, value: sel.value }, notifFromResult);
    if (!out || out.error) {
      buildThemeSelect();
      return;
    }
    location.reload();
  });
}

// commitFromUI 用卡片里那个输入框提交。成功才清空输入框：失败时保留原文，
// 便于用户改一处再试，而不是从头再敲一遍。
// 提交之后分支图上要多出一条新提交，因此图与看板都刷新一次
async function commitFromUI() {
  const repo = currentRepo();
  const out = await runWrite('/api/commit', { repo: repo.path, message: commitMsgEl.value.trim() }, notifFromResult);
  if (!out || out.error) return;
  commitMsgEl.value = '';
  draftMsg.delete(repo.path);
  await loadGraph(false);
}

// pushFromUI 推送当前分支。推送不改动图的内容，但会把"领先 N"这类状态清掉，
// 因此看板的快照在 runWrite 里已重取，这里只把卡片头部与图也刷新一次
async function pushFromUI() {
  const out = await runWrite('/api/push', { repo: currentRepo().path }, notifFromResult);
  if (out && !out.error) await loadGraph(false);
}

// 点行即可打开：用事件委托而不是给每行挂监听——行元素会被复用、也会被增删，
// 逐个挂就要在 reconcile 里同步维护，委托只需这一处
board.addEventListener('click', (e) => {
  const row = e.target.closest('.row');
  if (!row || !row.__spec) return;
  const s = row.__spec;
  // 分组行头也参与：它上面有"整组暂存/取消暂存"两个按钮
  if (s.kind !== 'head' && s.kind !== 'file' && s.kind !== 'group') return;
  // 行尾的动作按钮优先于"打开"：它是行内动作，不该顺带把卡片打开。
  // 分组行头上的"整组暂存/取消暂存"也走这一条
  const act = e.target.closest('.act');
  if (act) {
    applyFileAction(s, act.dataset.act);
    return;
  }
  // 仓库标题行直接进仓库卡片（默认就是它的分支图）；文件行进它自己的 diff
  if (s.kind === 'head') openRepoCard(s);
  else if (s.kind === 'file') openDiff(s);
});

diffBackEl.addEventListener('click', closeDiff);
commitBtnEl.addEventListener('click', commitFromUI);
graphPushEl.addEventListener('click', pushFromUI);
fetchBtnEl.addEventListener('click', fetchAll);

// 回车即提交：写提交信息时手不用离开键盘
commitMsgEl.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') commitFromUI();
});

document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape') return;
  // 通知中心浮在最上面，先收它：一层一层退，顺序与打开时相反。
  // 焦点同时交还铃铛，否则焦点会掉在已经隐藏的面板里
  if (notifCenterOpen) {
    closeNotifCenter(true);
    return;
  }
  // 焦点在提交输入框里时，Esc 先退出输入而不是关掉卡片——否则"想退出输入框"这个动作会把
  // 刚写好的提交信息一起丢掉（它会留在草稿里，但用户并不知道）。再按一次才关
  if (document.activeElement === commitMsgEl) {
    commitMsgEl.blur();
    return;
  }
  if (diffOpen) {
    closeDiff();
    return;
  }
  // 钉住的提交详情先收，再收卡片：一层一层退，顺序与打开时相反
  if (cardPinned) {
    hideCommitCard(true);
    return;
  }
  if (cardOpen) closeRepoCard();
});

// ——— 主循环 ———

// render 把一份仓库数据画到页面上。与取数分开，是为了图标主题晚一步就绪时
// 可以拿同一份数据重画一次，而不必再打一次接口
function render(repos) {
  const specs = buildSpecs(repos);
  const els = reconcile(specs);
  lastSpecs = specs;
  lastEls = els;
  layout(els, specs);
  board.setAttribute('aria-busy', 'false');

  if (repos.length === 0) {
    emptyEl.textContent = t('noRepos');
    emptyEl.hidden = false;
  } else {
    emptyEl.hidden = true;
  }
}

async function refresh() {
  try {
    const payload = await fetchRepos();
    const repos = Array.isArray(payload.repos) ? payload.repos : [];
    lastRepos = repos;
    render(repos);

    statusEl.classList.remove('error');
    statusEl.textContent = t('updated', {
      time: new Date(payload.generatedAt).toLocaleTimeString(),
      ms: payload.durationMs,
    });
  } catch (err) {
    // 失败时保留上一次的行不动（旧数据好过空白），只在角落里说明状态，
    // 避免网络抖动就让整屏内容消失
    statusEl.classList.add('error');
    statusEl.textContent = t('loadFailed', { err: err.message });
  }
}

function startPolling() {
  if (pollTimer !== null) return;
  pollTimer = window.setInterval(refresh, POLL_MS);
}

function stopPolling() {
  if (pollTimer === null) return;
  window.clearInterval(pollTimer);
  pollTimer = null;
}

// 窗口尺寸变化会改变列高，必须重新布局（不重新取数）。
// 覆盖层打开期间不重排：看板尺寸没变，重排要量行高、纯属白花，且此刻没人看得到结果
window.addEventListener('resize', () => {
  if (!diffOpen && !cardOpen && lastSpecs.length > 0) layout(lastEls, lastSpecs);
});

// 系统明暗主题切换时图标表要换一套（Seti 的浅色段是另一份平行表），因此重画一次；
// 只重画不重新取数
prefersLight.addEventListener('change', () => {
  iconCache.clear();
  if (lastRepos.length > 0) render(lastRepos);
});

// ——— 滚轮转横向滚动 ———

// 为什么必须自己把 deltaY 接到 scrollLeft 上：普通鼠标滚轮只产生 deltaY，横向容器不会
// 因此滚动（浏览器只在触控板横扫 / Shift+滚轮 时给出 deltaX），而纵向滚动已被禁用，
// 不做转换的话滚轮会毫无反应，用户会以为页面卡死。
//
// 为什么不用 CSS 的 scroll-behavior: smooth：它由 UA 按滚动距离决定时长（明显长于 180ms），
// 而每拨一格都会重新发起一次滚动，连续拨动时表现为「上一段动画没走完就被生硬截断」。
//
// 曲线照抄宿主自己的 AvaloniaDesktopKit/Behaviors/SmoothWheelScroll.cs，它又刻意与
// Slint 1.18 的 Flickable 对齐：固定 180ms 等减速——以恒定减速度走完全程、终点速度恰好
// 降到 0，归一化位置 p(u) = 2u - u²（u 为已过时长占比），起手最快、结尾干脆停住。
// 每个滚轮事件都从「当前位置」重起一段曲线（Slint 本身就是这个行为，不是缺陷），
// 并先按一个标称帧推进一次，保证即使下一帧还没到，拨动当帧也立刻有反馈
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
let scrollAnim = null;
let scrollRaf = 0;

function smoothScrollBy(px) {
  // 减的是视口宽而不是 window.innerWidth：有横向溢出时后者会等于 scrollWidth，相减恒为 0，
  // 滚轮横滚于是彻底失效——窄屏看板正是这种情形（见 viewportSize 的注释）
  const max = Math.max(0, document.documentElement.scrollWidth - viewportSize().w);
  // 目标基于「当前位置」累加：连续拨动因此不会丢失位移，也不会跳回去
  const target = Math.min(max, Math.max(0, window.scrollX + px));
  if (reduceMotion.matches) {
    scrollAnim = null;
    window.scrollTo(target, 0);
    return;
  }
  scrollAnim = {
    start: window.scrollX,
    target,
    elapsed: NOMINAL_FRAME_MS,
    last: performance.now(),
    // 记录我们刚写进去的位置，用来分辨下一帧读到的位置是不是自己造成的
    written: Math.round(window.scrollX),
  };
  // 已经有一帧在排队就不重复排队：它会在下一帧读到刚换成的新目标
  if (!scrollRaf) scrollRaf = requestAnimationFrame(stepScroll);
}

// 轨迹只由「已过时长」决定，因此与帧率无关：掉帧只会让采样变粗，不会改变曲线形状
function stepScroll(now) {
  scrollRaf = 0;
  const a = scrollAnim;
  if (!a) return;
  // 位置被别人改了（拖滚动条、方向键、触控板横扫）就放弃本轮动画、改为跟随真实位置，
  // 免得两边互相打架。事件可能是异步送达的，所以只能按位置差判断来源、不能用标志位
  // ——参照实现踩过这个坑：标志位会把自己的每帧写入当成外部滚动，动画第一帧就被掐断
  if (Math.abs(window.scrollX - a.written) > EXTERNAL_SCROLL_TOLERANCE) {
    scrollAnim = null;
    return;
  }
  a.elapsed += now - a.last;
  a.last = now;
  const u = Math.min(1, a.elapsed / WHEEL_MS);
  const p = 2 * u - u * u;
  const x = a.start + (a.target - a.start) * p;
  a.written = Math.round(x);
  window.scrollTo(x, 0);
  if (u < 1) scrollRaf = requestAnimationFrame(stepScroll);
  else scrollAnim = null;
}

window.addEventListener(
  'wheel',
  (e) => {
    // diff 覆盖层或仓库面板打开时把手势让回浏览器：那边要滚的是正文（纵向），
    // 被本处理器抢去转横向会让正文完全滚不动
    if (diffOpen || cardOpen) return;
    // 横向手势（触控板横扫、Shift+滚轮）交给浏览器原生处理：那条路自带缓动，手感最好
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY) || e.deltaY === 0) return;
    e.preventDefault();
    // 距离直接用事件给的增量，不强行折算成「一格 60px」：浏览器给的是像素增量
    // （鼠标一格约 100px，触控板是细粒度像素），照搬 60px 会把触控板的手感拉坏。
    // 只有行/页两种模式需要折算，行模式按 Slint 的 line→60px 对齐
    const px = e.deltaMode === 1 ? e.deltaY * LINE_PX
      : e.deltaMode === 2 ? e.deltaY * viewportSize().h
        : e.deltaY;
    smoothScrollBy(px);
  },
  { passive: false },
);

// 页面不可见时停掉轮询：后台标签页没人看，继续采集只是白白占用 CPU 与磁盘
document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    stopPolling();
    return;
  }
  // 覆盖层打开期间由 closeDiff / closeRepoPanel 统一恢复（它们会先 refresh 再 startPolling），
  // 这里不能抢先启动，否则看板会在覆盖层后面偷偷刷新
  if (diffOpen || cardOpen) return;
  refresh();
  startPolling();
});

document.title = 'ggt';

// 图标主题与首次取数并行：数据先到就先画（没有类型图标），图标到位后再用同一份数据重画一次。
// 串行等待会让首屏白屏时间平白多出一次本地 fetch
buildThemeSelect();
loadIconTheme().then(() => {
  if (lastRepos.length > 0) render(lastRepos);
});
refresh();
startPolling();

// ——— 分支图 ———
//
// 泳道分配在 Go 侧（internal/git/graph.go，照搬 VSCode 的 toISCMHistoryItemViewModelArray），
// 这里只负责画：renderGraphRow 逐行移植 VSCode 的 renderSCMHistoryItemGraph
// （src/vs/workbench/contrib/scm/browser/scmHistory.ts，MIT），几何常量也照抄它

const SWIMLANE_HEIGHT = 22; // 必须等于 CSS 里的 --row-h：线段按行边界画，行高一变连线就断开
const SWIMLANE_WIDTH = 11;
const SWIMLANE_CURVE_RADIUS = 5;
const CIRCLE_RADIUS = 4;
const CIRCLE_STROKE_WIDTH = 2;

// SVG 元素必须用 createElementNS：createElement('circle') 造出来的是 HTML 元素，
// 浏览器不会把它当图形画——表现为"什么都没有"，控制台也不报错
const SVG_NS = 'http://www.w3.org/2000/svg';

let cardOpen = false;
let cardSpec = null; // 当前仓库（来自看板快照）
let graphItems = []; // 已加载的"提交 + 泳道"
let graphTotal = 0;
let graphLimit = 0; // 已请求的条数；滚到底翻倍
let graphMaxLimit = 2000;
let graphAllRefs = true; // 默认跨全部分支与 tag
let cardSeq = 0; // 作废过期响应（卡片被关掉、或换了仓库时）
let cardSelected = ''; // 当前钉住详情的那条提交

// graphColor 把接口给的变量名包成 var(...)。变量名为空（主题没写那个令牌、也没默认值）时
// 用兜底色，而不是留一个空的 stroke——那会让线整条消失
function graphColor(name, fallback) {
  return name ? 'var(--' + name + ')' : fallback;
}

function svgNode(name, attrs) {
  const el = document.createElementNS(SVG_NS, name);
  for (const k in attrs) el.setAttribute(k, attrs[k]);
  return el;
}

function graphPath(color, strokeWidth) {
  const path = svgNode('path', {
    fill: 'none',
    'stroke-width': (strokeWidth || 1) + 'px',
    'stroke-linecap': 'round',
  });
  path.style.stroke = color;
  return path;
}

function graphCircle(index, radius, strokeWidth, color) {
  const c = svgNode('circle', {
    cx: SWIMLANE_WIDTH * (index + 1),
    cy: SWIMLANE_WIDTH,
    r: radius,
  });
  c.style.strokeWidth = strokeWidth + 'px';
  // 只有带颜色的圆点才在这里定填充。没颜色的那个是 HEAD 的内圈，它的填充、以及所有圆点的
  // 描边颜色，都交给 CSS：这两个颜色必须等于"这一行当前的实际底色"，而底色会随 hover 与
  // 选中变化，写进 JS 就固定成了初始底色，hover 时会露出一圈不跟着变的色边。上游把这两件
  // 事也放在样式表里（scm.css 的 .graph > circle 与 circle:last-child 规则，含 hover 与
  // 选中三组变体），此前这里的注释把那段误读成"上游不设填充（SVG 默认黑）"，于是把填充
  // 硬编码成了 --bg（editor.background），而这一行真正坐在 --panel-bg（editorWidget.background）
  // 上，两者本就不是同一个颜色，见 style.css 里那段圆点规则
  if (color) c.style.fill = color;
  return c;
}

function graphVLine(x, y1, y2, color, strokeWidth) {
  const p = graphPath(color, strokeWidth);
  p.setAttribute('d', 'M ' + x + ' ' + y1 + ' V ' + y2);
  return p;
}

function findLastNodeIndex(nodes, id) {
  for (let i = nodes.length - 1; i >= 0; i--) if (nodes[i].id === id) return i;
  return -1;
}

// renderGraphRow 画一行的泳道：连线 + 圆点。逐行移植上游 renderSCMHistoryItemGraph，
// 只把"颜色 id → CSS 变量"这一步换成接口已经翻好的变量名
function renderGraphRow(vm, laneColumnWidth) {
  const svg = svgNode('svg', { height: SWIMLANE_HEIGHT, width: laneColumnWidth });
  const item = vm.item;
  const input = vm.inputSwimlanes || [];
  const output = vm.outputSwimlanes || [];
  const parents = item.parents || [];

  const inputIndex = input.findIndex((n) => n.id === item.hash);
  const circleIndex = vm.index;
  const circleColor = graphColor(
    circleIndex < output.length
      ? output[circleIndex].color
      : circleIndex < input.length
        ? input[circleIndex].color
        : '',
    'var(--text-dim)',
  );

  let outputIndex = 0;
  for (let index = 0; index < input.length; index++) {
    const color = graphColor(input[index].color, 'var(--text-dim)');

    if (input[index].id === item.hash) {
      // 圆点不在自己那条泳道上：从左侧拐一道弧线过去（分叉被压到别的泳道时会出现）
      if (index !== circleIndex) {
        const d = [];
        d.push('M ' + SWIMLANE_WIDTH * (index + 1) + ' 0');
        d.push(
          'A ' +
            SWIMLANE_WIDTH +
            ' ' +
            SWIMLANE_WIDTH +
            ' 0 0 1 ' +
            SWIMLANE_WIDTH * index +
            ' ' +
            SWIMLANE_WIDTH,
        );
        d.push('H ' + SWIMLANE_WIDTH * (circleIndex + 1));
        const path = graphPath(color);
        path.setAttribute('d', d.join(' '));
        svg.append(path);
      } else {
        outputIndex++;
      }
      continue;
    }

    if (outputIndex < output.length && input[index].id === output[outputIndex].id) {
      if (index === outputIndex) {
        svg.append(graphVLine(SWIMLANE_WIDTH * (index + 1), 0, SWIMLANE_HEIGHT, color));
      } else {
        const d = [];
        const path = graphPath(color);
        d.push('M ' + SWIMLANE_WIDTH * (index + 1) + ' 0');
        d.push('V 6');
        d.push(
          'A ' +
            SWIMLANE_CURVE_RADIUS +
            ' ' +
            SWIMLANE_CURVE_RADIUS +
            ' 0 0 1 ' +
            (SWIMLANE_WIDTH * (index + 1) - SWIMLANE_CURVE_RADIUS) +
            ' ' +
            SWIMLANE_HEIGHT / 2,
        );
        d.push('H ' + (SWIMLANE_WIDTH * (outputIndex + 1) + SWIMLANE_CURVE_RADIUS));
        d.push(
          'A ' +
            SWIMLANE_CURVE_RADIUS +
            ' ' +
            SWIMLANE_CURVE_RADIUS +
            ' 0 0 0 ' +
            SWIMLANE_WIDTH * (outputIndex + 1) +
            ' ' +
            (SWIMLANE_HEIGHT / 2 + SWIMLANE_CURVE_RADIUS),
        );
        d.push('V ' + SWIMLANE_HEIGHT);
        path.setAttribute('d', d.join(' '));
        svg.append(path);
      }
      outputIndex++;
    }
  }

  // 其余父提交（合并的第二个及以后）：从圆点向右下方拐出去，接上各自那条泳道
  for (let i = 1; i < parents.length; i++) {
    const parentOutputIndex = findLastNodeIndex(output, parents[i]);
    if (parentOutputIndex === -1) continue;

    const d = [];
    const path = graphPath(graphColor(output[parentOutputIndex].color, 'var(--text-dim)'));
    d.push('M ' + SWIMLANE_WIDTH * parentOutputIndex + ' ' + SWIMLANE_HEIGHT / 2);
    d.push(
      'A ' +
        SWIMLANE_WIDTH +
        ' ' +
        SWIMLANE_WIDTH +
        ' 0 0 1 ' +
        SWIMLANE_WIDTH * (parentOutputIndex + 1) +
        ' ' +
        SWIMLANE_HEIGHT,
    );
    d.push('M ' + SWIMLANE_WIDTH * parentOutputIndex + ' ' + SWIMLANE_HEIGHT / 2);
    d.push('H ' + SWIMLANE_WIDTH * (circleIndex + 1));
    path.setAttribute('d', d.join(' '));
    svg.append(path);
  }

  // 圆点上下的两段竖线：把这一行与上下两行接起来
  if (inputIndex !== -1) {
    svg.append(
      graphVLine(
        SWIMLANE_WIDTH * (circleIndex + 1),
        0,
        SWIMLANE_HEIGHT / 2,
        graphColor(input[inputIndex].color, 'var(--text-dim)'),
      ),
    );
  }
  if (parents.length > 0) {
    svg.append(
      graphVLine(SWIMLANE_WIDTH * (circleIndex + 1), SWIMLANE_HEIGHT / 2, SWIMLANE_HEIGHT, circleColor),
    );
  }

  // 圆点本身：HEAD 画成空心圈，多父提交画成同心圈，其余是实心点
  if (vm.kind === 'HEAD') {
    svg.append(graphCircle(circleIndex, CIRCLE_RADIUS + 3, CIRCLE_STROKE_WIDTH, circleColor));
    svg.append(graphCircle(circleIndex, CIRCLE_STROKE_WIDTH, CIRCLE_RADIUS, ''));
  } else if (parents.length > 1) {
    svg.append(graphCircle(circleIndex, CIRCLE_RADIUS + 2, CIRCLE_STROKE_WIDTH, circleColor));
    svg.append(graphCircle(circleIndex, CIRCLE_RADIUS - 1, CIRCLE_STROKE_WIDTH, circleColor));
  } else {
    svg.append(graphCircle(circleIndex, CIRCLE_RADIUS + 1, CIRCLE_STROKE_WIDTH, circleColor));
  }
  return svg;
}

// graphLaneColumnWidth 按已加载行里最宽的泳道算一个固定列宽：这样标题的左边缘是对齐的。
// 上游每行用自己那份宽度，标签会一行一个位置；这里多给一个固定容器，SVG 在里面左对齐
function graphLaneColumnWidth() {
  let lanes = 1;
  for (const vm of graphItems) {
    lanes = Math.max(lanes, (vm.inputSwimlanes || []).length, (vm.outputSwimlanes || []).length);
  }
  return SWIMLANE_WIDTH * (lanes + 1);
}

function mkEl(tag, className, text) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (text !== undefined) el.textContent = text;
  return el;
}

function formatGraphTime(ts) {
  const d = new Date(ts * 1000);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString() + ' ' + d.toLocaleTimeString();
}

// renderGraphRows 整表重建而不是追加：泳道是逐行递推出来的，续取之后前面那些行的列宽也可能变，
// 只追加会让新旧两段错位。行数上限由 Go 侧的 graphMaxLimit 管着，重建代价可控
function renderGraphRows() {
  const colW = graphLaneColumnWidth();
  const frag = document.createDocumentFragment();

  for (const vm of graphItems) {
    const row = document.createElement('div');
    row.className = 'g-row' + (vm.item.hash === cardSelected ? ' selected' : '');
    row.dataset.hash = vm.item.hash;

    const lanes = mkEl('div', 'lanes');
    lanes.style.width = colW + 'px';
    lanes.appendChild(renderGraphRow(vm, colW));
    row.appendChild(lanes);

    for (const ref of vm.item.refs || []) {
      const tag = mkEl('span', 'ref');
      if (ref.color) {
        const dot = mkEl('span', 'dot');
        dot.style.background = graphColor(ref.color, 'transparent');
        tag.appendChild(dot);
      }
      tag.appendChild(mkEl('span', 'name', ref.name));
      row.appendChild(tag);
    }

    row.appendChild(mkEl('span', 'hash', vm.item.hash.slice(0, 8)));
    row.appendChild(mkEl('span', 'subject', vm.item.subject || t('graphNoSubject')));
    row.appendChild(mkEl('span', 'meta', vm.item.author + ' · ' + formatGraphTime(vm.item.timestamp)));
    frag.appendChild(row);
  }

  // 尾部一行：还有更多就说"加载中"，到底了就说"已加载全部"。滚动到底的判据就看它
  if (graphItems.length > 0) {
    const tail = mkEl('p', 'hint', graphItems.length >= graphTotal ? t('graphEnd') : t('graphLoading'));
    tail.style.padding = '6px 12px';
    frag.appendChild(tail);
  }

  graphListEl.replaceChildren(frag);
  graphCountEl.textContent = graphTotal > 0 ? t('graphCount', { shown: graphItems.length, total: graphTotal }) : '';
}

// setGraphOp 写顶栏下面那行提示（取数失败时是 git 的原话）
function setGraphOp(text, isError) {
  graphOpEl.textContent = text || '';
  graphOpEl.classList.toggle('error', !!isError);
}

// loadGraph 取一批历史。more 为真表示"滚到底了，再取一批"——做法是把 limit 翻倍重取，
// 而不是 skip：泳道是逐行递推的，只取第二页会让整页的线从最左边重新开始
async function loadGraph(more) {
  if (!cardSpec) return;
  const repoPath = cardSpec.repo.path;
  const seq = ++cardSeq;
  const nextLimit = more ? Math.min(graphLimit * 2, graphMaxLimit) : 100;

  try {
    const params = new URLSearchParams({
      repo: repoPath,
      limit: String(nextLimit),
      all: graphAllRefs ? '1' : '0',
    });
    const res = await fetch('/api/log?' + params.toString(), { cache: 'no-store', headers: authHeaders });
    const data = await res.json();
    // 用户可能已经关掉卡片或换了仓库：这一份响应就作废
    if (seq !== cardSeq || !cardOpen) return;
    if (data.error) {
      setGraphOp(data.error, true);
      return;
    }

    setGraphOp('');
    graphLimit = nextLimit;
    graphItems = data.items || [];
    graphTotal = data.total || 0;
    graphMaxLimit = data.maxLimit || graphMaxLimit;
    if (data.branch) cardSpec.repo.branch = data.branch;
    fillBranchSelect(data.branches || [], data.branch || '');
    // 头部随仓库状态更新：切了分支、提交或推送之后，标题上的分支与领先/落后必须跟着变。
    // 这也是"切分支成功了吗"在页面上唯一看得见的结果
    const repo = currentRepo();
    graphTitleEl.innerHTML =
      '<span>' + esc(repo.name) + '</span>' +
      (data.branch ? '<span class="dir"> ' + esc(data.branch) + '</span>' : '');
    graphStateEl.textContent = repoStateText(repo);
    renderGraphRows();
  } catch (err) {
    if (seq === cardSeq) setGraphOp(err.message, true);
  }
}

// fillBranchSelect 用本地分支名填满分支选择器，并把当前分支选中。
// 当前分支不在列表里（例如 detached HEAD）时补一项进去，免得选择器空着
function fillBranchSelect(branches, current) {
  graphBranchEl.replaceChildren();
  const names = branches.slice();
  if (current && !names.includes(current)) names.unshift(current);
  for (const name of names) {
    const option = document.createElement('option');
    option.value = name;
    option.textContent = name;
    graphBranchEl.appendChild(option);
  }
  graphBranchEl.value = current;
  graphBranchEl.disabled = names.length === 0;
  graphBranchEl.setAttribute('aria-label', t('branchLabel'));
}

// repoStateText 拼出卡片头部的状态串（未提交 / detached / 领先落后），与看板卡片那行同源
function repoStateText(r) {
  const state = [];
  if (r.noCommits) state.push(t('noCommits'));
  else if (r.detached) state.push(t('detached'));
  if (r.upstream && !r.noCommits) {
    if (r.ahead > 0) state.push(t('ahead', { n: r.ahead }));
    if (r.behind > 0) state.push(t('behind', { n: r.behind }));
  }
  return state.join(' · ');
}

// openRepoCard 打开某个仓库的卡片：默认就是这个仓库的提交分支图，顶栏是这个仓库的操作。
//
// 这一层替代了原来的"仓库面板"：点仓库直接进来，不再有中间那一步选择；仓库自身的操作
// （切分支、拉取、同步、推送、提交）也都收在这张卡片里，不散落到页面上别处
function openRepoCard(spec) {
  cardSpec = spec;
  const r = spec.repo;

  graphTitleEl.innerHTML =
    '<span>' + esc(r.name) + '</span>' +
    (r.branch && !r.noCommits ? '<span class="dir"> ' + esc(r.branch) + '</span>' : '');
  graphStateEl.textContent = repoStateText(r);
  graphFilterLabelEl.textContent = t('graphAllRefs');
  graphAllRefsEl.checked = graphAllRefs;
  graphBranchEl.disabled = true; // 分支列表要等 /api/log 回来，先禁用免得点开是空的

  // 顶栏与提交行的文案按当前语言填（结构在 HTML 里，文案在这里）
  graphFetchEl.textContent = t('fetchRepo');
  graphSyncEl.textContent = t('sync');
  graphPushEl.textContent = t('push');
  graphDiffEl.textContent = t('openDiff', { n: r.files.length });
  graphDiffEl.title = t('openWholeRepoDiff');
  commitBtnEl.textContent = t('commit');
  commitMsgEl.placeholder = t('commitMsg');
  commitMsgEl.value = draftMsg.get(r.path) || '';

  cardSelected = '';
  hideCommitCard(true);
  graphItems = [];
  graphTotal = 0;
  graphLimit = 0;
  graphCountEl.textContent = '';
  setGraphOp('');
  graphListEl.replaceChildren();

  cardOpen = true;
  graphEl.classList.add('open');
  graphEl.setAttribute('aria-hidden', 'false');
  syncScrollLock();
  stopPolling();
  graphBackEl.focus();

  loadGraph(false);
}

// closeCardState 只收起卡片这一层，不碰焦点与轮询。
// 与关掉整张卡片分开，是为了让"只换内容"这类场景不误触轮询与焦点
function closeCardState() {
  if (!cardOpen) return;
  cardOpen = false;
  cardSeq++; // 作废在路上的那次响应
  hoverSeq++;
  hideCommitCard(true);
  graphEl.classList.remove('open');
  graphEl.setAttribute('aria-hidden', 'true');
  // 一张图可能上千行，关掉就释放这些元素
  graphListEl.replaceChildren();
  graphItems = [];
}

// closeRepoCard 关闭卡片：若 diff 还开着（从卡片里进去的），一并关掉——返回键的语义是
// "回到上一层"，不是"只关掉最上面那层"
function closeRepoCard() {
  if (!cardOpen) return;
  if (diffOpen) closeDiff();
  // 草稿留在内存里：下次打开同一个仓库时把没提交完的信息放回去
  if (cardSpec) draftMsg.set(cardSpec.repo.path, commitMsgEl.value);
  closeCardState();
  syncScrollLock();
  resume();
}

graphBackEl.addEventListener('click', closeRepoCard);

// 滚到底续取：判据是"已经滚到最后 120px 以内"，不用 IntersectionObserver——
// 这里只有一个哨兵，滚动事件本身很便宜
graphListEl.addEventListener('scroll', () => {
  if (!cardOpen) return;
  const el = graphListEl;
  if (el.scrollTop + el.clientHeight < el.scrollHeight - 120) return;
  if (graphItems.length >= graphTotal) return;
  if (graphLimit >= graphMaxLimit) {
    setGraphOp(t('graphLimitReached', { n: graphMaxLimit }), false);
    return;
  }
  loadGraph(true);
});

// 过滤开关：默认全部分支与 tag，勾掉只看当前分支
graphAllRefsEl.addEventListener('change', () => {
  graphAllRefs = graphAllRefsEl.checked;
  cardSelected = '';
  hideCommitCard(true);
  loadGraph(false);
});

/* ——— 提交详情卡：悬浮即显 + 点击钉住 ———
 *
 * 为什么不做成侧栏常驻面板：泳道图占满整个卡片宽度才看得清分叉与合并，侧栏会一直吃掉一块宽度。
 * 但"只能靠悬浮"也不行——键盘用户根本触发不了 hover，而"点什么就出什么"是这次的要求。
 * 因此做成两级：
 *   - 鼠标移过某一行：立刻贴着光标弹出（元信息来自内存，文件清单异步补上并缓存）
 *   - 点一下（或按上下键 / 回车）：钉住，不再随鼠标移开消失，Esc 或点别处才收
 */
const commitCardCache = new Map(); // 提交哈希 -> 文件清单：同一个提交反复划过时不重复请求
let hoverSeq = 0;
let cardPinned = false;
let hoverCardTimer = null;
// lastPointer 记录指针最后一次移动到的位置；hoverSuppressAt 是"刚收起卡片时指针所在的位置"。
//
// 为什么需要后者：收起卡片会让指针下方的元素从卡片换成泳道图的行，浏览器随后就地补派一次
// mouseover 给那个新元素——鼠标其实一动没动。不认这一次的话，点右上角那个 × 收起之后，卡片
// 会立刻被同一个位置重新弹开，用户看到的是"关了又弹"。
// 判据是"坐标与收起时几乎相同"：不用时间窗（慢机器与合成事件有延迟时都不可靠），
// 也不用"等指针移动"（mouseover 可能先于 mousemove 派发，会连带吃掉用户在别的行上的正常悬停）
let lastPointer = { x: -1, y: -1 };
let hoverSuppressAt = null;

// commitCardPosition 把卡片摆在光标右下 14px；靠近右/下边缘时翻到另一侧，别被窗口切掉
function commitCardPosition(x, y) {
  const box = graphPopupEl.getBoundingClientRect();
  // 贴边判断与最终落位都按真实视口算，不用 window.innerWidth / innerHeight：内容溢出时那两个值
  // 会被一起撑大（见 viewportSize），"右边放不下"于是判断不出来，卡片会跨出屏幕右缘
  const vp = viewportSize();
  let left = x + 14;
  let top = y + 14;
  if (left + box.width > vp.w - 8) left = x - box.width - 14;
  if (top + box.height > vp.h - 8) top = y - box.height - 14;
  // 翻转只把卡片挪到锚点的另一侧，救不了"锚点本身就在视口外"这种情形：点击与键盘钉住都走
  // pinCommitCard，锚点取自行尾（row.right − 40），而泳道图在窄屏下比视口宽（实测 390 的视口里
  // #graph-list 就有 986），行尾连同锚点都在屏幕外——实测卡片因此被摆到 x=795，连右上角那个
  // 关闭按钮都点不到。所以最后再夹一次：宁可叠在行上，也不能把这张卡唯一的可见出口挪出屏幕
  left = Math.max(8, Math.min(left, vp.w - box.width - 8));
  top = Math.max(8, Math.min(top, vp.h - box.height - 8));
  graphPopupEl.style.left = left + 'px';
  graphPopupEl.style.top = top + 'px';
}

function hideCommitCard(force) {
  if (cardPinned && !force) return;
  cardPinned = false;
  if (hoverCardTimer) clearTimeout(hoverCardTimer);
  hoverCardTimer = null;
  graphPopupEl.hidden = true;
  graphPopupEl.classList.remove('pinned');
  // 收起时记住指针位置：卡片一消失，指针下方的元素就换成泳道图的行，浏览器会就地补派一次
  // mouseover 给新元素——那一处坐标与这里几乎相同，据此把它挡掉（见 hoverSuppressAt 的说明）
  hoverSuppressAt = { x: lastPointer.x, y: lastPointer.y };
}

// showCommitCard 立刻画出能拿到的那一半（元信息在内存里，要什么有什么），
// 文件清单随后补上——不为了"一次画全"让卡片迟一拍才出现
function showCommitCard(vm, x, y, pinned) {
  cardPinned = !!pinned;
  graphPopupEl.classList.toggle('pinned', cardPinned);
  graphPopupEl.hidden = false;

  const cached = commitCardCache.get(vm.item.hash);
  graphPopupEl.replaceChildren(renderCommitCard(vm, cached || null, cardPinned));
  commitCardPosition(x, y);
  if (cached) return;

  const seq = ++hoverSeq;
  const params = new URLSearchParams({ repo: cardSpec.repo.path, hash: vm.item.hash });
  fetch('/api/commit-files?' + params.toString(), { cache: 'no-store', headers: authHeaders })
    .then((res) => res.json())
    .then((data) => {
      // 用户可能已经移到别的提交、或把卡片收起来了：只在卡片还开着时补内容
      if (seq !== hoverSeq || graphPopupEl.hidden) return;
      const files = data && !data.error ? data : null;
      if (!files) return;
      commitCardCache.set(vm.item.hash, files);
      graphPopupEl.replaceChildren(renderCommitCard(vm, files, cardPinned));
      commitCardPosition(x, y);
    })
    .catch(() => {
      // 文件清单取不到不影响元信息：那一半照常显示
    });
}

// pinCommitCard 把某条提交钉住。键盘路径没有光标，因此贴着那一行定位
function pinCommitCard(vm, row) {
  cardSelected = vm.item.hash;
  for (const r of graphListEl.children) {
    if (r.classList) r.classList.toggle('selected', r.dataset.hash === cardSelected);
  }
  const box = row.getBoundingClientRect();
  showCommitCard(vm, box.right - 40, box.bottom - 4, true);
}

// renderCommitCard 画详情卡。files 为 null 时只画元信息与提交信息（文件清单随后补）
//
// pinned 为真时在顶部画一个关闭按钮：钉住之后卡片不再随鼠标移开而消失，若没有可见的出口，
// 用户只能靠猜（Esc，或去点别的提交）才能把它收起来。按钮因此只在钉住态出现——悬浮预览态
// 本来就跟着鼠标走，再放一个 × 反而是噪音。
// 之所以放在这个函数里而不是 showCommitCard 里 append：文件清单是异步补上的，补上时会整卡
// 重画一次（replaceChildren），按钮若在函数外挂就得在两处各挂一次，迟早漏掉一处
function renderCommitCard(vm, files, pinned) {
  const item = vm.item;
  const box = document.createDocumentFragment();

  if (pinned) {
    const bar = mkEl('div', 'hovercard-top');
    const close = mkEl('button', 'icon-btn', '×');
    close.type = 'button';
    close.title = t('graphClose');
    close.setAttribute('aria-label', t('graphClose'));
    // 必须带 force：此时 cardPinned 为真，hideCommitCard 不加 force 会直接 return，按钮成摆设
    close.addEventListener('click', () => hideCommitCard(true));
    bar.appendChild(close);
    box.appendChild(bar);
  }

  box.appendChild(mkEl('h2', '', item.subject || t('graphNoSubject')));

  const dl = mkEl('dl');
  const addRow = (label, value) => {
    dl.appendChild(mkEl('dt', '', label));
    dl.appendChild(mkEl('dd', '', value));
  };
  addRow(t('graphHash'), item.hash);
  addRow(t('graphAuthor'), item.authorEmail ? item.author + ' <' + item.authorEmail + '>' : item.author);
  addRow(t('graphDate'), formatGraphTime(item.timestamp));
  if ((item.refs || []).length > 0) {
    addRow(t('graphRefs'), item.refs.map((r) => r.name).join('、'));
  }
  box.appendChild(dl);

  if (item.message && item.message !== item.subject) {
    box.appendChild(mkEl('p', 'msg', item.message));
  }

  if (files) {
    box.appendChild(mkEl('h2', '', t('graphFiles')));
    box.appendChild(mkEl('p', 'hint', t('graphFilesSummary', {
      n: files.files.length,
      adds: files.insertions,
      dels: files.deletions,
    })));
    const list = mkEl('ul', 'files');
    for (const f of files.files) {
      const li = mkEl('li');
      li.appendChild(mkEl('span', 'path', f.origPath ? f.origPath + ' → ' + f.path : f.path));
      if (f.binary) {
        li.appendChild(mkEl('span', 'stat', t('graphBinary')));
      } else {
        li.appendChild(mkEl('span', 'stat add', '+' + f.adds));
        li.appendChild(mkEl('span', 'stat del', '−' + f.dels));
      }
      list.appendChild(li);
    }
    box.appendChild(list);
  }

  return box;
}

// 指针位置要随时记着：hideCommitCard 收起卡片时拿它当"哪一次 mouseover 该被吃掉"的基准
document.addEventListener('mousemove', (e) => {
  lastPointer = { x: e.clientX, y: e.clientY };
});

// 鼠标移过某一行就弹卡；移开时给 120ms 宽限再收——不留宽限的话，从行移向卡片的那段空隙
// 会把它闪掉，而"贴着光标"的卡片本来就常常需要把鼠标移进去看更多内容
graphListEl.addEventListener('mouseover', (e) => {
  if (!cardOpen || cardPinned) return;
  // 卡片刚收起时，指针下方的元素由卡片换成了泳道图的行，浏览器会就地补派一次 mouseover，
  // 坐标与收起时几乎相同——认了它就是"点 × 关了又弹"。只吃掉这一次。
  // 判据用坐标而不是"等指针移动"：mouseover 可能先于 mousemove 派发，那种顺序下"等移动解除"
  // 会把用户在别的行上的第一次悬停一起吃掉（实测踩过：卡片再也弹不出来）
  if (
    hoverSuppressAt &&
    Math.abs(e.clientX - hoverSuppressAt.x) <= 2 &&
    Math.abs(e.clientY - hoverSuppressAt.y) <= 2
  ) {
    return;
  }
  hoverSuppressAt = null;
  const row = e.target.closest('.g-row');
  if (!row) return;
  const vm = graphItems.find((v) => v.item.hash === row.dataset.hash);
  if (!vm) return;
  if (hoverCardTimer) clearTimeout(hoverCardTimer);
  showCommitCard(vm, e.clientX, e.clientY, false);
});
graphListEl.addEventListener('mouseleave', () => {
  if (cardPinned) return;
  hoverCardTimer = setTimeout(() => hideCommitCard(false), 120);
});
// 鼠标移进卡片本身时取消那次收起：用户正把鼠标挪过去看文件清单
graphPopupEl.addEventListener('mouseenter', () => {
  if (hoverCardTimer) clearTimeout(hoverCardTimer);
  hoverCardTimer = null;
});
graphPopupEl.addEventListener('mouseleave', () => {
  if (cardPinned) return;
  hoverCardTimer = setTimeout(() => hideCommitCard(false), 120);
});

// 点一下即钉住：这条路径不依赖悬浮，鼠标停在别处也能把详情留在屏幕上
graphListEl.addEventListener('click', (e) => {
  const row = e.target.closest('.g-row');
  if (!row) return;
  const vm = graphItems.find((v) => v.item.hash === row.dataset.hash);
  if (vm) pinCommitCard(vm, row);
});

// 键盘：上下键在提交之间移动并钉住详情，回车同样钉住当前这条。
// 没有这条路的话，键盘用户永远看不到提交详情
graphListEl.addEventListener('keydown', (e) => {
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
  if (graphItems.length === 0) return;
  e.preventDefault();
  const step = e.key === 'ArrowDown' ? 1 : -1;
  const current = graphItems.findIndex((v) => v.item.hash === cardSelected);
  const index = current === -1 ? 0 : Math.min(graphItems.length - 1, Math.max(0, current + step));
  const vm = graphItems[index];
  const row = graphListEl.querySelector('.g-row[data-hash="' + vm.item.hash + '"]');
  if (!row) return;
  row.scrollIntoView({ block: 'nearest' });
  pinCommitCard(vm, row);
});

/* ——— 卡片顶栏上的仓库操作 ———
 *
 * 五件：切分支、拉取本仓库、同步、推送、提交，外加进入全仓 diff 的入口。
 * 全部走 runWrite：统一处理"一次只允许一个写操作在飞"、失败时把 git 的原话交出来、
 * 以及写完之后失效快照
 */
function currentRepo() {
  // 快照会在每次刷新后重建，卡片手里的那份可能已经旧了：按路径回去找当前的那一份
  const found = lastRepos.find((r) => r.path === cardSpec.repo.path);
  return found || cardSpec.repo;
}

// cardWrite 发一次仓库操作。成功后先刷新看板快照、再刷新分支图：切分支、提交、拉取都会改变
// 图的内容，而头部那行状态来自看板快照（顺序反了会读到上一轮的分支与领先/落后）
async function cardWrite(path, body) {
  const out = await runWrite(path, body, notifFromResult);
  if (out && !out.error) {
    await refresh();
    await loadGraph(false);
  }
  return out;
}

graphBranchEl.addEventListener('change', async () => {
  const out = await cardWrite('/api/checkout', { repo: cardSpec.repo.path, branch: graphBranchEl.value });
  // 失败（工作区脏、分支名不存在）时把选择器拨回当前分支，别让它显示一个没生效的值
  if (out && out.error) graphBranchEl.value = cardSpec.repo.branch || '';
});
graphFetchEl.addEventListener('click', () => cardWrite('/api/fetch', { repo: cardSpec.repo.path }));
graphSyncEl.addEventListener('click', () => cardWrite('/api/sync', { repo: cardSpec.repo.path }));
graphPushEl.addEventListener('click', () => cardWrite('/api/push', { repo: cardSpec.repo.path }));
// 全仓 diff 从卡片顶栏进：它浮在卡片之上，关掉回到卡片（不再占据"入口行"那样的对等地位）
graphDiffEl.addEventListener('click', () => openDiff({ repo: currentRepo() }));