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
    back: 'Back',
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
    back: '返回',
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
const GAP = 16;

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
const emptyEl = document.getElementById('empty');
const statusEl = document.getElementById('status');
const diffEl = document.getElementById('diff');
const diffBackEl = document.getElementById('diff-back');
const diffTitleEl = document.getElementById('diff-title');
const diffBodyEl = document.getElementById('diff-body');

// 返回按钮的文字在 JS 里填：它要跟随语言，而 index.html 是静态骨架、不参与翻译
diffBackEl.textContent = '← ' + t('back');

// 从 URL 取 token。页面是由 Go 端带 token 的地址打开的，之后所有请求改用请求头传递：
// 把凭据留在 URL 里会进入浏览器历史、也可能随 Referer 泄露
const TOKEN = new URLSearchParams(location.search).get('token') || '';
const authHeaders = TOKEN ? { 'X-WebUI-Token': TOKEN } : undefined;

// rowEls 是「上一次渲染留下的行元素」，按 key 索引。
// 保留它们是为了 DOM 复用：数据刷新时能复用的元素就复用，位置变化由 CSS transition 平滑过渡；
// 若每次都重建 DOM，卡片会瞬间跳到新位置，看起来像整页闪烁
let rowEls = new Map();
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
const WORKTREE = {
  M: { letter: 'M', cls: 'st-modified' },
  D: { letter: 'D', cls: 'st-deleted' },
  T: { letter: 'T', cls: 'st-type-changed' },
};

// fileStatus 把一个变更文件归到 VSCode 的某一个 Status 上。
//
// 归并规则与理由：
//   - 未合并优先。VSCode 的七种冲突状态（BOTH_MODIFIED 等）统一取字母 '!' 与
//     conflictingResourceForeground，上游注释写明不用 ⚠ 是因为它在 Windows 上显示很糟
//   - 未跟踪取 'U'。注意这与 `git status --short` 的 '??' 不同，是 VSCode 的字母表
//   - 已经被忽略的条目取 'I'（依赖 git.RunStatus 把 ignored 也解析出来）
//   - 一个文件同时有暂存与未暂存改动时（porcelain 的 MM），VSCode 会让它同时出现在
//     「Staged Changes」与「Changes」两个分组里；本看板是平铺列表没有分组，
//     因此取暂存侧为主——与 VSCode 的 Status 中 INDEX_* 是独立取值这一事实一致
function fileStatus(f) {
  if (f.unmerged) return { letter: '!', cls: 'st-conflicting' };
  if (f.untracked) return { letter: 'U', cls: 'st-untracked' };
  const ix = f.index || ' ';
  const wk = f.work || ' ';
  if (ix !== ' ' && ix !== '?' && ix !== '!') {
    if (STAGED[ix]) return STAGED[ix];
  }
  if (ix === '!') return { letter: 'I', cls: 'st-ignored' };
  if (WORKTREE[wk]) return WORKTREE[wk];
  // 认不出的状态码不猜：保留原始字符并沿用未跟踪的中性色，便于发现解析遗漏
  return { letter: (ix + wk).trim().slice(0, 1) || '?', cls: 'st-ignored' };
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

// buildSpecs 把仓库列表摊平成行序列。顺序即渲染顺序，排序由服务端完成（排序规则只有一处实现）
//
// 干净的仓库只占一行（只有标题行，不额外补一行「工作区干净」说明）：
// 实测真实配置下 37 个仓库里有 33 个是干净的，若每个都补一行说明，
// 大半屏都在重复同一句话，而它的信息量等于零——也正是用户提出的痛点
function buildSpecs(repos) {
  const specs = [];
  for (const repo of repos) {
    specs.push({ key: repo.path + '\u0000h', kind: 'head', repo });

    if (repo.error) {
      // 采集失败的仓库必须显式说明失败，不能显示成「工作区干净」
      specs.push({ key: repo.path + '\u0000e', kind: 'note', repo, text: t('failed') + ': ' + repo.error });
    } else if (repo.files.length > 0) {
      for (const f of repo.files) {
        // 行的 key 用文件路径而不是下标：文件增删时，其余文件的行元素还能被复用，
        // 用下标的话一次插入就会让后面所有行的 key 全变，全部重建、动画全丢
        specs.push({ key: repo.path + '\u0000f\u0000' + f.path, kind: 'file', repo, file: f });
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

  if (s.kind === 'file') {
    const f = s.file;
    const st = fileStatus(f);
    // 路径拆成「文件名 + 目录」两部分：文件名用状态色，目录用更淡的色。
    // 与 VSCode 在列表模式下把路径作为 description 淡化显示一致，也让同类文件名更容易对齐扫读
    const slash = f.path.lastIndexOf('/');
    const base = slash === -1 ? f.path : f.path.slice(slash + 1);
    const dir = slash === -1 ? '' : f.path.slice(0, slash);
    return (
      iconHTML(base) +
      // 目录用 .dir 而不是 .branch：.branch 的规则只作用于仓库标题行（.row.head .branch），
      // 用在文件行上会匹配不到任何规则，目录便继承了文件名的状态色——而两处注释都写明
      // 目录应当比文件名更淡。这是一处类名与规则名对不上的笔误，颜色上的表现是"路径整段同色"
      '<span class="path">' + esc(base) + (dir ? '<span class="dir"> ' + esc(dir) + '</span>' : '') + '</span>' +
      '<span class="letter">' + esc(st.letter) + '</span>'
    );
  }

  return esc(s.text || '');
}

// reconcile 把行元素调整到与 specs 一致，尽量复用已有元素。
// 返回与 specs 一一对应的元素数组，供布局使用
function reconcile(specs) {
  const next = new Map();
  const els = [];

  for (const s of specs) {
    let el = rowEls.get(s.key);
    if (!el) {
      el = document.createElement('div');
      board.appendChild(el);
    }

    const cls = ['row', s.kind];
    if (s.foot) cls.push('foot');
    if (s.file) cls.push(fileStatus(s.file).cls);
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
// 已知性能限制：本函数在同一个循环里既读 el.offsetHeight 又写 el.style.transform，
// 读写交替会让浏览器每次都强制同步重排，整体是 O(行数²)。实测 3332 行耗时 1.6 秒、
// 20332 行耗时 88.7 秒（一次忘记写 .gitignore 的 node_modules 就足以触发），
// 而它对每次轮询与每次窗口缩放都会重跑一遍。VSCode 侧的做法是给 status 设上限
// （statusLimit 默认 10000）并在超限时杀掉子进程（git.ts:2835），本项目暂未做这层保护。
// 修法已经清楚：三种行高本来就由 CSS 变量决定、且已在函数开头读入，不必再去问 DOM，
// 把读与写分成两趟即可消除强制重排。本轮按用户决定「记为已知限制、排在后面修」
function layout(els, specs) {
  const colW = cssVar('--col-w', 320);
  const headH = cssVar('--head-h', 22);
  const rowH = cssVar('--row-h', 22);
  const contH = cssVar('--cont-h', 22);
  const colH = window.innerHeight - 32; // 32 = 页面上下各 16px 内边距

  // tailRows[i] 是第 i 条标题行之后、属于同一张卡片的内容行数，用来判断
  // 「列尾还值不值得起一张新卡片」。
  // 原实现对所有卡片一律要求「标题 + 三行内容」（88px），于是「干净仓库」这种本来
  // 只有一行标题的卡片也被要求 88px：列尾明明还放得下它（实测 92px），却提前换列，
  // 左列因此只填到 956/1048px。按卡片自己的行数算之后，没有内容行的卡片只需一行的高度
  const tailRows = new Array(specs.length).fill(0);
  for (let i = specs.length - 1, n = 0; i >= 0; i--) {
    if (specs[i].kind === 'head') { tailRows[i] = n; n = 0; } else n++;
  }

  // 续段标识每次布局重建。它们的数量等于「被截断的卡片数」，通常个位数，
  // 重建比维护增删更不容易出错
  for (const el of board.querySelectorAll('.cont')) el.remove();

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

    const h = el.offsetHeight || (s.kind === 'head' ? headH : rowH);

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
        board.appendChild(cont);

        used = contH;
      }
    }

    el.style.transform = 'translate(' + col * (colW + GAP) + 'px, ' + used + 'px)';
    used += h;
  }

  board.style.width = (col + 1) * (colW + GAP) + 'px';
  board.style.height = colH + 'px';
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

// openDiff 打开某个仓库（file 为空 → 整个仓库）或某个文件的 diff。
// 异步：本地接口也有一次往返，期间先显示"加载中"，避免看起来是点了没反应
async function openDiff(spec) {
  const repo = spec.repo;
  const file = spec.file || null;
  const seq = ++diffSeq;

  // 标题只用我们已经知道的信息（仓库名、文件路径），不必等接口回来才显示
  const shown = file ? (file.origPath ? file.origPath + ' → ' + file.path : file.path) : '';
  diffTitleEl.innerHTML =
    '<span>' + esc(repo.name) + '</span>' + (shown ? '<span class="dir"> ' + esc(shown) + '</span>' : '');
  diffBodyEl.textContent = t('diffLoading');

  diffOpen = true;
  diffScrollX = window.scrollX;
  diffEl.classList.add('open');
  diffEl.setAttribute('aria-hidden', 'false');
  // 看板仍是布局中的元素，只是被盖住；不锁住 html 的话滚轮与方向键还能滚它
  document.documentElement.style.overflow = 'hidden';
  stopPolling();
  diffBackEl.focus();

  const params = new URLSearchParams({ repo: repo.path });
  if (file) params.set('file', file.path);

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
  if (seq !== diffSeq || !diffOpen) return;
  renderDiff(repo, file, out);
}

// closeDiff 关闭覆盖层并恢复看板。
function closeDiff() {
  if (!diffOpen) return;
  diffOpen = false;
  diffSeq++; // 作废可能还在路上的那次响应
  diffEl.classList.remove('open');
  diffEl.setAttribute('aria-hidden', 'true');
  document.documentElement.style.overflow = '';
  diffBodyEl.textContent = ''; // 释放大 diff 占用的 DOM
  window.scrollTo(diffScrollX, 0);
  // 覆盖层期间没有刷新过看板，关闭时补一次再恢复轮询（期间工作区可能已经变了）
  refresh();
  startPolling();
}

// 点行即可打开：用事件委托而不是给每行挂监听——行元素会被复用、也会被增删，
// 逐个挂就要在 reconcile 里同步维护，委托只需这一处
board.addEventListener('click', (e) => {
  const row = e.target.closest('.row');
  if (!row || !row.__spec) return;
  if (row.__spec.kind !== 'head' && row.__spec.kind !== 'file') return;
  openDiff(row.__spec);
});

diffBackEl.addEventListener('click', closeDiff);

document.addEventListener('keydown', (e) => {
  // 只认 Esc：覆盖层是只读视图，没有输入框，不需要考虑"正在输入时 Esc 另有含义"
  if (e.key === 'Escape') closeDiff();
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
  if (!diffOpen && lastSpecs.length > 0) layout(lastEls, lastSpecs);
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
  const max = Math.max(0, document.documentElement.scrollWidth - window.innerWidth);
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
    // diff 覆盖层打开时把手势让回浏览器：那边要滚的是 diff 正文（纵向），
    // 被本处理器抢去转横向会让正文完全滚不动
    if (diffOpen) return;
    // 横向手势（触控板横扫、Shift+滚轮）交给浏览器原生处理：那条路自带缓动，手感最好
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY) || e.deltaY === 0) return;
    e.preventDefault();
    // 距离直接用事件给的增量，不强行折算成「一格 60px」：浏览器给的是像素增量
    // （鼠标一格约 100px，触控板是细粒度像素），照搬 60px 会把触控板的手感拉坏。
    // 只有行/页两种模式需要折算，行模式按 Slint 的 line→60px 对齐
    const px = e.deltaMode === 1 ? e.deltaY * LINE_PX
      : e.deltaMode === 2 ? e.deltaY * window.innerHeight
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
  // 覆盖层打开期间由 closeDiff 统一恢复（它会先 refresh 再 startPolling），
  // 这里不能抢先启动，否则看板会在 diff 后面偷偷刷新
  if (diffOpen) return;
  refresh();
  startPolling();
});

document.title = 'ggt';

// 图标主题与首次取数并行：数据先到就先画（没有类型图标），图标到位后再用同一份数据重画一次。
// 串行等待会让首屏白屏时间平白多出一次本地 fetch
loadIconTheme().then(() => {
  if (lastRepos.length > 0) render(lastRepos);
});
refresh();
startPolling();