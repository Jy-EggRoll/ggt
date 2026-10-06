'use strict';

/*
 * ggt ui 的页面逻辑。
 *
 * 布局方案：行级流式横向布局。把“行”（仓库标题行、变更文件行）而不是“整张卡片”当作布局单元，
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
    diffUntrackedOmitted: '{{n}} untracked files are not part of the whole-repo diff — open a file row to see its contents',
    diffMergeFirstParent: 'Merge commit — shown as the change against its first parent',
    diffOpenCommit: 'Show the whole commit',
    diffOpenFile: 'Show how this file changed in this commit',
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
    themeFollow: 'Follow the system',
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
    settings: 'Settings',
    settingsSave: 'Save',
    settingsSaveCount: 'Save ({{n}})',
    settingsSaved: 'Settings saved',
    settingsSaveFailed: 'Some settings were not saved',
    settingsReset: 'Reset to default',
    settingsPath: 'Config file',
    settingsEmpty: 'No settings to show',
    settingManaged: 'Managed by {{cmd}}; change it there',
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
    diffUntrackedOmitted: '另有 {{n}} 个未跟踪文件不在整仓 diff 里，点它的文件行可以看内容',
    diffMergeFirstParent: '合并提交 —— 下面是相对第一个父提交的改动',
    diffOpenCommit: '看这次提交的完整改动',
    diffOpenFile: '看这个文件在那次提交里改了什么',
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
    themeFollow: '跟随系统',
    graphEntry: '分支图',
    graphAllRefs: '全部分支与 tag',
    graphCount: '已显示 {{shown}} / {{total}}',
    graphLoading: '加载中…',
    graphEnd: '已加载全部',
    graphLimitReached: '已到 {{n}} 条上限，可取消勾选“全部分支与 tag”往回看',
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
    settings: '设置',
    settingsSave: '保存',
    settingsSaveCount: '保存（{{n}}）',
    settingsSaved: '设置已保存',
    settingsSaveFailed: '部分设置未能保存',
    settingsReset: '恢复默认',
    settingsPath: '配置文件',
    settingsEmpty: '没有可显示的配置项',
    settingManaged: '由 {{cmd}} 管理，请在那里修改',
  },
};

const LANG = (typeof window.__GGT_LANG__ === 'string' && window.__GGT_LANG__) || 'en';

// PAGE_SETTINGS 是服务端随首页注入的设置快照（按配置注册表生成），供“页面行为”读用。
// 不在页面启动时去 /api/settings 取一次，是因为这些取值只影响页面行为、不影响首屏渲染，
// 为它们多一次请求不值得。拿不到时按空数组处理，各用途退回自己的默认行为
const PAGE_SETTINGS = Array.isArray(window.__GGT_SETTINGS__) ? window.__GGT_SETTINGS__ : [];

// settingValue 从快照里取一项的文本形态取值，这一项不在快照里时返回 fallback。
// 设置面板不走这里——面板每次打开都现取一次，否则命令行改过的值要刷新页面才看得见
function settingValue(key, fallback) {
  const item = PAGE_SETTINGS.find((it) => it && it.key === key);
  return item && item.value !== undefined ? item.value : fallback;
}

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
// 每次请求都会触发一轮服务端采集（每个仓库一个 git 进程），服务端虽会缓存 2 秒，
// 多个标签页同时开着时压力仍会叠加
const POLL_MS = 5000;

// 卡片之间的纵向间距。与 CSS 无关：行的位置完全由本文件计算
// 列间距也从 CSS 变量读：它是布局参数，与 --col-w 一样只该有一处定义
const GAP = cssVar('--col-gap', 16);

// 滚轮平滑的参数。曲线照抄宿主已有的
// AvaloniaDesktopKit/Behaviors/SmoothWheelScroll.cs（它又刻意与 Slint 1.18 对齐），
// 详细理由见下方 smoothScrollBy 处注释。
// 时长必须与样式表的 --dur-move 同值：滚轮与浮层进退、行位移属于同一类变化，两边不一致就会
// 出现两种时间感。CSS 没法引用 JS 常量，只能靠这条注释约束两处一起改
const WHEEL_MS = 400;
const NOMINAL_FRAME_MS = 1000 / 60;
// DOM_DELTA_LINE 时一行折算的像素。与 Slint 的 line→60 逻辑像素对齐
// （i-slint-backend-winit 的 LineDelta(lx, ly) => (lx * 60., ly * 60.)）
const LINE_PX = 60;
// 判定“位置被别人改了”的像素阈值：拖滚动条、按方向键、触控板横扫都会改它，
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
// 提交信息与提交按钮属于“仓库卡片”（VSCode 的源码管理视图里这一对也在最上方）
const commitMsgEl = document.getElementById('graph-msg');
const commitBtnEl = document.getElementById('graph-commit-btn');
const fetchBtnEl = document.getElementById('fetch-btn');
// 设置面板：底栏的入口按钮，以及面板的头部、正文、结果行、配置文件路径、保存按钮
const settingsBtnEl = document.getElementById('settings-btn');
const settingsEl = document.getElementById('settings');
const settingsBackEl = document.getElementById('settings-back');
const settingsTitleEl = document.getElementById('settings-title');
const settingsBodyEl = document.getElementById('settings-body');
const settingsOpEl = document.getElementById('settings-op');
const settingsPathEl = document.getElementById('settings-path');
const settingsSaveEl = document.getElementById('settings-save');
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
// 提交详情卡（悬浮即显，点一下固定）
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

// 返回按钮的文字在 JS 里填：它要跟随语言，而 index.html 是静态结构、不参与翻译
diffBackEl.textContent = '← ' + t('back');
graphBackEl.textContent = '← ' + t('back');
fetchBtnEl.textContent = t('fetch');
// 卡片里的几个按钮与输入框的文案由 openRepoCard 按当前语言填（它们只在打开卡片时才出现）

// 从 URL 取 token。页面是由 Go 端带 token 的地址打开的，之后所有请求改用请求头传递：
// 把凭据留在 URL 里会进入浏览器历史、也可能随 Referer 泄露
const TOKEN = new URLSearchParams(location.search).get('token') || '';
const authHeaders = TOKEN ? { 'X-WebUI-Token': TOKEN } : undefined;

// rowEls 是“上一次渲染留下的行元素”，按 key 索引。
// 保留它们是为了 DOM 复用：数据刷新时能复用的元素就复用，位置变化由 CSS transition 平滑过渡；
// 若每次都重建 DOM，卡片会瞬间跳到新位置，看起来像整页闪烁
let rowEls = new Map();
// newRows 是“本次渲染新建的行”，layout 定位完它们之后要在下一帧把过渡打开回来。
// 之所以要记一批而不是逐行处理：见 reconcile 与 layout 末尾的说明
let newRows = [];
let lastSpecs = [];
let lastEls = [];
let lastRepos = [];
let pollTimer = null;

// cssVar 读取样式表里的长度变量并转成数字。
// 布局参数只在一处定义（style.css 的 :root），JS 从这里读，避免两边各写一份数字后对不上
function cssVar(name, fallback) {
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name);
  const n = parseFloat(raw);
  return Number.isFinite(n) ? n : fallback;
}

// motionEasing 读样式表里的缓动曲线，原样交给 Web Animations API。
// 与 cssVar 分开是因为后者要 parseFloat 成数字、只适合长度；曲线是 cubic-bezier(...) 这样的
// 字符串，写死一份在本文件里就等于把动效参数抄成了两处
function motionEasing(name, fallback) {
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name);
  return raw.trim() || fallback;
}

// motionMs 读样式表里的时长令牌并转成毫秒数（Web Animations API 的 duration 要数字）。
//
// 实现就是 cssVar——两者都只是把自定义属性 parseFloat 一下。分成两个名字是因为校对去处不同：
// cssVar 读布局长度，回退值到样式表里的“数字 + px”声明中校对（错开会让列高少算一截，
// 症状只是最后一行卡片被底栏压住，不报任何异常）；motionMs 读时长，回退值到“数字 + ms”
// 声明中校对。两类令牌各校各的，混在一起会让那条测试报出“无从校对”
function motionMs(name, fallback) {
  return cssVar(name, fallback);
}

// reduceMotion 是系统“减少动态效果”偏好。页面里所有由 JS 驱动的动画都要先问它一句：
// CSS 那一侧由样式表末尾的 @media 段落负责，两处必须成对维护——关掉了 CSS 动画却仍由 JS
// 播一段，等于降级只做了一半
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

// fadeIn 让一段刚换上的内容淡入，避免内容“啪”一下跳变。
//
// 为什么用 Web Animations API，而不是“加个类、靠 CSS 动画重放”：类没变时 CSS 动画不会重放，
// 想重放就得先读一次布局属性（例如 offsetWidth）把样式强制结算一遍，而 diff 正文可能有上万行，
// 那一次结算的代价不小。WAAPI 每次调用都独立播一遍，也不触发样式结算
//
// 时长与曲线仍从样式表读，参数因此只有 style.css 一处定义（与 cssVar 同一路数）
function fadeIn(el) {
  if (reduceMotion.matches) return;
  el.animate([{ opacity: 0 }, { opacity: 1 }], {
    duration: motionMs('--dur-move', 400),
    easing: motionEasing('--ease-decel', 'linear'),
  });
}

// loadingBlock 造一个“正在读取”的占位：转动的圈加一行文字。
// 文字复用既有的 diffLoading 键（中英各一条），不新增文案；它替代的是原来那句纯文字，
// 让“还在跑、不是卡住了”看得出来
function loadingBlock() {
  const box = document.createElement('div');
  box.className = 'loading';
  const spinner = document.createElement('span');
  spinner.className = 'loading-spinner';
  spinner.setAttribute('aria-hidden', 'true');
  const text = document.createElement('span');
  text.textContent = t('diffLoading');
  box.append(spinner, text);
  return box;
}

// viewportSize 返回真实的视口尺寸（布局视口，CSS 像素）。
//
// 为什么不能直接用 window.innerWidth / innerHeight：页面内容一旦横向或纵向溢出，Chromium
// 会把这两个值跟着内容一起撑大，而它们看起来就是个普通的窗口尺寸，读的人不会起疑。
// 实测（手机档，视口 390×844、看板按 4 列排宽 1344px）：
//     window.innerWidth  1360    window.innerHeight  2944
//     documentElement.clientWidth  390    clientHeight  844
// 看板的宽高偏偏又由 JS 按列数算出来写到元素上（见 layout 末尾的 board.style.width/height），
// 于是“尺寸读大了 → 看板算大了 → 溢出更多 → 尺寸读得更大”会这样一直互相放大：窄屏下按 4 列
// 排并横向溢出。分列、贴边、滚轮横滚上限与翻页折算因此全都按一个被撑大的尺寸在算。
// 更隐蔽的一层：layout 是轮询也会重跑的，一次重排就能把列高按 2944 算成近三倍，整块看板
// 塌成一列。改读 documentElement 的 client 尺寸后，这两条路都不会再被内容反过来影响
//
// 有一件事要说明白：一开始把“窄屏下详情卡被摆到视口外、点不到关闭按钮”也记在这条因果上，
// 实测证明不成立——那种情形下新旧代码算出的位置一模一样，真因是锚点本身在视口外，
// 见 commitCardPosition 的注释。这里不再重复那个错误结论
//
// 取 client 尺寸而不是 visualViewport：后者语义是“当前可见区”，会随捏合缩放变化，而这里要的是
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
// 两种状态下标：porcelain 的 X 位是“暂存区相对 HEAD”，Y 位是“工作区相对暂存区”。

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
// 即索引位是 "."、工作区位是 "A"。上游把它当“已新增”处理（repository.ts 的 raw.y === 'A'
// 映射到 INTENT_TO_ADD，字母 'A'、配色 addedResourceForeground）。此前这里没有 A，
// 该记录会落到下面 fileStatus 的默认分支，字母侥幸还是 A，颜色却取了“已忽略”的灰并加斜体，
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
  if (f.untracked) return true; // 未跟踪文件的两位都是 '?'，按“只在未暂存这一组”处理
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
// 颜色用图标文档里的 fontColor（Seti 为每种类型配了色），因此图标颜色是“类型色”，
// 与文件名、状态字母的“状态色”互不干扰——这正是 VSCode 里的观感
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
// 干净的仓库只占一行（只有标题行，不额外补一行“工作区干净”说明）：
// 实测真实配置下 37 个仓库里有 33 个是干净的，若每个都补一行说明，
// 大半屏都在重复同一句话，而它的信息量等于零——也正是用户提出的问题
//
// 分组是 VSCode 语义的照搬：每个仓库内部按 UI_GROUPS 分段，空分组不显示。
// 分组的行头也占一行（高度与其它行同为 22px），因此布局那套“行高即常量”的前提不受影响
function buildSpecs(repos) {
  const specs = [];
  for (const repo of repos) {
    specs.push({ key: repo.path + '\u0000h', kind: 'head', repo });

    if (repo.error) {
      // 采集失败的仓库必须显式说明失败，不能显示成“工作区干净”
      specs.push({ key: repo.path + '\u0000e', kind: 'note', repo, text: t('failed') + ': ' + repo.error });
    } else {
      for (const group of UI_GROUPS) {
        const files = repo.files.filter(group.has);
        if (files.length === 0) continue; // 空分组不显示（VSCode 也不显示）
        specs.push({ key: repo.path + '\u0000g\u0000' + group.id, kind: 'group', repo, group, count: files.length });
        for (const f of files) {
          // 行的 key 用“分组 + 文件路径”而不是下标：文件增删时其余行的元素还能被复用，
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

    // 标记卡片末行：CSS 靠它画下边框与圆角。跨列被切断的卡片，其上一段的末行不带这个标记，
    // 视觉上自然表现为“还没结束，下接另一列”
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
    // 多仓库看板需要在一行内同时说清“哪个分支、有没有没推的东西”
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
    // 分组行头：组名 + 该组的文件数，外加“整组动作”——未暂存那组给 +（暂存全部），
    // 已暂存那组给 −（全部取消暂存）。
    // 未合并组刻意不给按钮：冲突得由人来分辨，批量暂存会把还带着冲突标记的文件一起 stage 进去
    // （与单个文件那个按钮同一套理由），而“点一下全部暂存”恰恰是最容易误触的操作
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
    // 路径拆成“文件名 + 目录”两部分：文件名用状态色，目录用更淡的色。
    // 与 VSCode 在列表模式下把路径作为 description 淡化显示一致，也让同类文件名更容易对齐扫读
    const slash = f.path.lastIndexOf('/');
    const base = slash === -1 ? f.path : f.path.slice(slash + 1);
    const dir = slash === -1 ? '' : f.path.slice(0, slash);
    // 行尾的动作按钮：只给“属于这一组”的那一个。未暂存组给“暂存”，已暂存组给“取消暂存”，
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
      // 目录应当比文件名更淡。这是一处类名与规则名对不上的笔误，颜色上的表现是“路径整段同色”
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
    // 分组类：只用来给“已暂存 / 未暂存”两组铺不同的半透明底色（见 style.css）。
    // 状态类（st-*）负责文件自己的字母与文字颜色，两者互不干扰
    if (s.group) cls.push('g-' + s.group.id);
    if (s.file) cls.push(fileStatus(s.file, s.group ? s.group.id : 'work').cls);
    if (isNew) {
      // 新行在首次定位前必须关掉过渡：行是绝对定位的，刚建出来时 transform 是 none
      // （即页面左上角），而它的真实坐标要等 layout() 才写入，带着过渡就会“从左上角飞过来”
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
    // 点击时（事件委托）只有从这里才能拿到“这一行是哪个仓库的哪个文件”
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
// 修法不是“优化”，而是拿掉触发点：
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
  // 而“JS 里一份、CSS 里一份”的数字迟早会对不上
  // 视口高走 viewportSize 而不是 window.innerHeight：内容溢出时后者会被撑大，而看板的高度又是
  // 按它算出来写回元素的，那正是“看板把自己撑高”的循环（见 viewportSize 的注释）
  const colH = viewportSize().h - cssVar('--page-pad', 16) * 2 - cssVar('--statusbar-h', 32);

  // tailRows[i] 是第 i 条标题行之后、属于同一张卡片的内容行数，用来判断
  // “列尾还值不值得起一张新卡片”。
  // 原实现对所有卡片一律要求“标题 + 三行内容”（88px），于是“干净仓库”这种本来
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

    // 列尾放不下“标题 + 这张卡片最多三行内容”就整卡顺延。上限取三行是为了避免
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

  // 新行的坐标已经写入，下一帧再把过渡打开：同一帧里“关过渡 → 写坐标 → 开过渡”会被浏览器
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
// 覆盖整页打开某个仓库或某个文件的改动（对齐结论：不做语法高亮、分“已暂存 / 未暂存”两段、
// 覆盖整页替换看板、Esc 或返回键回看板）。
//
// “不做语法高亮”说的是不按语言着色，与行内（词级）高亮不冲突：后者只标出这一行里哪几个字符
// 变了，颜色仍然取自主题的增删令牌，见下面“行内（词级）比对”一节。

// diffOpen 为真表示覆盖层正打开。它同时关掉三件事，各自的理由不同：
//   - 轮询：看板被盖住，刷了也没人看，而每次刷新都要让服务端为每个仓库起一趟 git
//   - resize 重排：看板仍是“被盖住但仍在布局中”，尺寸没有变化，重排纯属白花
//   - 滚轮转横向：覆盖层里滚轮应当滚动 diff 正文，被抢去滚看板会让 diff 滚不动
let diffOpen = false;
// 打开前的横向滚动位置。看板在覆盖层关闭后要回到用户刚才看的那一列，
// 否则关掉 diff 会莫名跳回最左
let diffScrollX = 0;
// 每次打开的序号：响应回来时用它丢弃“用户已经关掉或换了目标”的那次结果
let diffSeq = 0;

// settingsOpen 为真表示设置面板正打开。
// 它刻意与 diff、卡片这两个标志声明在一起，并由下面的 anyOverlayOpen 统一汇总：
// 三者都是“整页覆盖 + 锁滚动”的浮层，凡是要问“现在有没有浮层开着”的地方都该走那一个入口，
// 散着写迟早漏一处——wheel 处理器就漏过设置面板，表现为面板正文用滚轮滚不动、只能拖滚动条
let settingsOpen = false;

// anyOverlayOpen 汇总“当前有没有浮层开着”。
// 需要它的至少三处：锁住页面滚动、浮层关掉后恢复看板、以及判断纵向滚轮该不该让回浏览器。
// 最后那处原先自己写了一遍三标志的判断且漏掉了设置面板，所以这里只留一个入口
function anyOverlayOpen() {
  return diffOpen || cardOpen || settingsOpen;
}

// syncScrollLock 统一决定要不要锁住页面滚动，并顺带切换遮罩层。
// 遮罩与滚动锁由同一处决定：两件事都取决于“有没有浮层开着”，分头写迟早会出现
// “层关了、模糊还在”这种半截状态
function syncScrollLock() {
  const overlayOpen = anyOverlayOpen();
  document.documentElement.style.overflow = overlayOpen ? 'hidden' : '';
  document.body.classList.toggle('overlay-open', overlayOpen);
}

// resume 在浮层都关掉之后恢复看板：补一次取数（期间工作区可能已经变了）并恢复轮询
function resume() {
  if (anyOverlayOpen()) return;
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

// diffLineBody 剥掉统一 diff 给每一行加的那个标记字符，只留这一行真正的内容。
//
// 三个标记字符是：+ 增、- 删、空格上下文。页面上都不显示——增删由整行底色与左侧色条表示
// （见 style.css 的 pre.diff .add/.del），标记留在正文里有一处实打实的坏处：
// 代码自己以 + 或 - 开头时（Markdown 列表项、diff 的 diff、递增表达式）会与标记连成
// "+- item"、"-- item"，看不出哪一部分是标记、哪一部分是内容。
// VSCode 的 diff 视图同样不在正文里放这两个字符
//
// 上下文行那个空格也一并剥掉：只留下它，正文里就数上下文行多一个前导空格，整屏代码的左边界
// 会参差不齐——这一列本来就不该算进内容的缩进
//
// 认不出类别的行（空行、畸形 diff）原样返回：少剥一个字符只是难看，多剥一个是丢内容
function diffLineBody(cls, line) {
  if (cls === 'add' || cls === 'del') return line.slice(1);
  if (cls === '' && line.startsWith(' ')) return line.slice(1);
  return line;
}

// ——— 行内（词级）比对 ———
//
// 这一层只回答一个问题：一行里究竟是哪几个字符变了。答案来自收编的 VSCode diff 引擎
// （vendor/vscode-diff.js，来源、许可证与再生成步骤见该文件抬头与同目录 LICENSE），
// 它原本服务于 VSCode 自己的 diff 编辑器
//
// 算法细节一概不在这里复刻：整块的对齐、两侧行数不等时的配对、标点与词边界的取舍都在引擎内部。
// 本文件只做三件事——把统一 diff 切成“改动块”、把块交给引擎、把回给的行列区间摊回每一行

// diffInnerTimeoutMs 是单个改动块的比对时限（毫秒），用来应付“比预期慢得多”那种情况
//
// 上游给这项计算的预算是 5000 毫秒（src/vs/editor/common/config/diffEditor.ts 的
// maxComputationTime），那是给后台线程留的额度；本视图跑在页面主线程上，超时直接表现为界面卡住，
// 因此压到 1000 毫秒
//
// 为什么不是更小：这是按真实经过的时间判定的，耗时就落在时限附近的块会时超时不超时，
// 于是同一份输入时有时无。实测（真实浏览器、上限以内的块）是 40 到 100 毫秒，且随内容而变——
// 批量改名这类每行十几字符的内容约 40 毫秒，每行几十字符的数字表格到 100 毫秒；
// 早先取 100 毫秒恰好压在这个区间上，“两侧合计接近 200 行”的块稳定拿不到任何行内高亮，
// 同一份输入还会一次有一处高亮、一次一处都没有。定在 1000 毫秒之后，上限以内的实测耗时留出
// 十倍余量，判定不再贴着边界；它仍然挡得住畸形输入（字符极多、结构极碎的那种，实测可跑到分钟级）
//
// 超时不是错误，但超时的结果要丢掉：引擎在这种情形下会把整块算作一处改动，于是块内每一行
// 都被整行高亮——逐词的信息一点没多、节点数却按行数翻倍（见 diffInnerSpans 里的判断），
// 而且“整行都变了”是句错话，宁可什么都不标
const diffInnerTimeoutMs = 1000;

// diffInnerMaxLines 是单个改动块两侧合计的行数上限，超过就不做行内比对
//
// 上游没有这一道上限，它替整个文件只设一个超时；本视图多这一道是因为：改动块一旦长到这个规模，
// 它已经是“整段重写”而不是“改了几个词”，逐词高亮不再有信息量，而代价（对齐的耗时加上随后
// 每行两个元素的 DOM）却随行数线性增长。定在两百行以内，单个块的耗时与节点数就都固定在一个
// 可预期的范围内，也省掉一次注定要作废的比对
//
// 耗时随内容而变，行数只是它的粗略代理：实测（真实浏览器）这个上限附近是 40 到 100 毫秒——
// 批量改名这类每行十几字符的内容约 40 毫秒，每行几十字符的数字表格到 100 毫秒。
// 单个块最坏约 400 个节点（带行内高亮的行各一个 .dl 加一个 .hl），与文件总行数无关
const diffInnerMaxLines = 200;

// diffInnerMaxChars 是单个改动块两侧合计的字符数上限，同样超过就不做行内比对
//
// 为什么行数上限之外还要这一道：行数不是耗时的可靠代理，引擎的代价随内容的总字符数走。
// 两行四十万字符（打包产物、base64、单行 JSON）这种块行数只有 2，行数上限挡不住它，
// 代价却要按分钟算（实测过 60 秒以上仍未返回）——真到那一步只能等超时，
// 用户白等一秒，还是看不到高亮
//
// 定在 16 KiB 是让这两道上限在“每行约八十字符的普通代码”上正好衔接：行再长的由这一道拦住，
// 行数更多的由前一道拦住。按实测每千字符约二十毫秒算，这一档离上面的时限还有一个量级
const diffInnerMaxChars = 16384;

// diffEngine 是收编引擎的实例：undefined 表示还没建，null 表示引擎不可用
//
// 为什么要挡“不可用”这一种情况：引擎来自 vendor/vscode-diff.js，而那个文件由 go:embed
// 打进二进制、又排在 app.js 之前引入，正常情况下一定存在；可一旦嵌入或引入顺序出问题，
// 页面不该整片白掉——行内高亮是锦上添花的一层，拿不到就退回整行着色
let diffEngine;
function ensureDiffEngine() {
  if (diffEngine === undefined) {
    diffEngine =
      typeof ggtDiffEngine !== 'undefined' && ggtDiffEngine.DefaultLinesDiffComputer
        ? new ggtDiffEngine.DefaultLinesDiffComputer()
        : null;
  }
  return diffEngine;
}

// isNoNewlineMarker 判定这一行是不是 git 的“\ No newline at end of file”标记。
// 它夹在删行与增行之间时不该把改动块切成两半：它说的是相邻那一行缺行尾换行，
// 与那处改动同属一块
function isNoNewlineMarker(line) {
  return line.startsWith('\\');
}

// diffLineSpans 找出所有改动块，交给引擎算出行内高亮区间，返回与 lines 等长的区间表。
//
// 入参 lines 是剥掉行首标记之后的行内容（见 diffLineBody），列号要按它的下标算，
// 因此这里不再自己 slice
//
// 改动块的定义是“一段连续的删行紧跟一段连续的增行”。统一 diff 的排版保证了 hunk 内所有 - 行
// 一定排在所有 + 行之前，因此“先删后增且相邻”就是块，不必再去猜哪一行对着哪一行；
// 块内两侧行数不等（删两行加五行）是常态，配对由引擎整块对齐时一并解决
//
// 块外的上下文行（以空格开头那些）不参与比对。它们看起来是现成的参照物——紧邻的上下文行
// 往往与增行只差几个字符，把引擎的对齐引过去就能只标那几个字符——但 git 已经判定那些行没变，
// 让引擎把它们跟增行配成一对，就会出现“上下文行没变，却与它配对的增行只标了两个字”这种
// 与整行底色互相矛盾的结论。块内配对、块外不动，两侧说法才一致
function diffLineSpans(lines, classes) {
  const spans = new Array(lines.length);

  let i = 0;
  while (i < lines.length) {
    if (classes[i] !== 'del') {
      i++;
      continue;
    }
    // 先收删行，再收紧接着的增行，两段扫描都允许跨过“\ No newline”标记
    const delIdx = [];
    while (i < lines.length && (classes[i] === 'del' || isNoNewlineMarker(lines[i]))) {
      if (classes[i] === 'del') delIdx.push(i);
      i++;
    }
    const addIdx = [];
    while (i < lines.length && (classes[i] === 'add' || isNoNewlineMarker(lines[i]))) {
      if (classes[i] === 'add') addIdx.push(i);
      i++;
    }
    // 只有一侧的改动（纯删或纯增）没有“哪几个字符变了”可言，整行着色已经说清
    if (!delIdx.length || !addIdx.length) continue;
    // 规模上限：超限的块直接不做，省掉一次注定要作废的比对。两道上限都在这里判，
    // 都是纯按输入量算的，所以同一份 diff 每次得到的结论一致
    if (delIdx.length + addIdx.length > diffInnerMaxLines) continue;
    let chars = 0;
    for (const n of delIdx) chars += lines[n].length;
    for (const n of addIdx) chars += lines[n].length;
    if (chars > diffInnerMaxChars) continue;

    const found = diffInnerSpans(
      delIdx.map((n) => lines[n]),
      addIdx.map((n) => lines[n]),
    );
    for (let k = 0; k < delIdx.length; k++) if (found.del[k]) spans[delIdx[k]] = found.del[k];
    for (let k = 0; k < addIdx.length; k++) if (found.add[k]) spans[addIdx[k]] = found.add[k];
  }
  return spans;
}

// diffInnerSpans 用引擎算出块两侧每一行该高亮的字符区间。
//
// 入参是块内删侧与增侧的行内容（已剥掉行首的 +/-，不含换行符），出参与入参同行序：
// 每行一个 [起, 止) 的字符区间数组，列号按 UTF-16 码元算——这正是 JS 字符串下标的口径，
// 因此拿到的区间可以直接切字符串，中文与 emoji 都不会被切成半个
//
// 行内容里不含换行符这一点必须守住：引擎按编辑器的行模型处理，传进去的行若自带换行，
// 列号会整体错位一行（实测过，症状是中文改字被标到上一行的行尾）
function diffInnerSpans(delLines, addLines) {
  const spans = { del: new Array(delLines.length), add: new Array(addLines.length) };
  const engine = ensureDiffEngine();
  if (!engine) return spans;

  let result;
  try {
    result = engine.computeDiff(delLines, addLines, {
      // 行尾空白也算改动：这一行既然走到了这里，就说明 git 已经判过它与对面那行不同，
      // 再把空白差异抹掉，会让“只改了缩进”这类改动在高亮上是一片空白。
      // 上游这个选项默认取 true（diffEditor.ts 的 ignoreTrimWhitespace），此处刻意取反
      ignoreTrimWhitespace: false,
      // 搬家检测解决的是“同一段代码挪到了别处”，对一屏之内的字符对齐没有增量，开着只是白花预算
      computeMoves: false,
      maxComputationTimeMs: diffInnerTimeoutMs,
      // “细分到子词”的第二轮扩展本轮不做（本项目也没有对应设置项），与上游默认一致
      extendToSubwords: false,
    });
  } catch (err) {
    // 引擎对畸形输入会抛错（它自己认为不该出现的情形，例如两侧行数极度悬殊）。
    // 拿不到行内区间只是少一层高亮，不该把整份 diff 一起搭进去
    return spans;
  }

  // 超时的结果不能用：引擎在这种情形下会把整块算作一处改动，于是块内每一行都被整行高亮——
  // 逐词的信息一点没多，节点数却按行数翻倍，正好把合并同类行那项优化抵消掉。
  // 退回整行着色才是这种规模该有的样子
  if (result.hitTimeout) return spans;

  const changes = result.changes;
  for (const ch of changes) {
    for (const ic of ch.innerChanges || []) {
      spreadInnerRange(spans.del, delLines, ic.originalRange);
      spreadInnerRange(spans.add, addLines, ic.modifiedRange);
    }
  }
  spans.del = spans.del.map(mergeSpans);
  spans.add = spans.add.map(mergeSpans);
  return spans;
}

// spreadInnerRange 把引擎给出的一处区间摊到它覆盖的每一行上。
// 区间可能横跨多行（整段重写就是这种形状），此时中间那些整行都要算进来
function spreadInnerRange(perLine, lines, range) {
  for (let n = range.startLineNumber; n <= range.endLineNumber; n++) {
    const line = lines[n - 1];
    // 行号越界一律跳过：宁可少标一处高亮，也不能把高亮标到别的行上
    if (line === undefined) continue;
    // 列号 1 起算、末列不含。首行从引擎给的起列开始，末行到引擎给的止列为止，
    // 中间的行则是整行
    const start = n === range.startLineNumber ? Math.max(range.startColumn - 1, 0) : 0;
    const end = n === range.endLineNumber ? Math.min(range.endColumn - 1, line.length) : line.length;
    // 起止相等表示“就在这个位置插入或删除”，没有任何字符可以着色；留着它会产出一个
    // 空的高亮元素，看不见也选不中
    if (end > start) (perLine[n - 1] || (perLine[n - 1] = [])).push([start, end]);
  }
}

// mergeSpans 把一行内的区间按起点排序，并合并重叠或首尾相接的部分。
// 引擎会把同一处改动拆成几段相邻区间，不合并就会产出多个紧邻的高亮元素——视觉一样，节点多一份
function mergeSpans(list) {
  if (!list || !list.length) return null;
  if (list.length === 1) return list;
  list.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const out = [list[0]];
  for (let i = 1; i < list.length; i++) {
    const last = out[out.length - 1];
    if (list[i][0] <= last[1]) {
      if (list[i][1] > last[1]) last[1] = list[i][1];
      continue;
    }
    out.push(list[i]);
  }
  return out;
}

// hlLineHTML 造一行带行内高亮的 HTML。
//
// 入参是这一行的正文（剥掉行首标记之后的内容，见 diffLineBody），区间也按同一个下标算——
// 传给引擎的正是这段正文，两边口径一致
function hlLineHTML(line, spans) {
  let html = '';
  let pos = 0;
  for (const [start, end] of spans) {
    // 把起止夹回 pos 与行尾之间：引擎原则上不会给出越界值，但高亮错位比少一处高亮难查得多
    const s = Math.min(Math.max(start, pos), line.length);
    const e = Math.min(Math.max(end, s), line.length);
    if (s > pos) html += esc(line.slice(pos, s));
    if (e > s) html += '<span class="hl">' + esc(line.slice(s, e)) + '</span>';
    pos = e;
  }
  if (pos < line.length) html += esc(line.slice(pos));
  return html;
}

// diffHTML 把一份统一 diff 转成用于 <pre> 的 HTML：剥掉每行行首的标记字符、按类别给增删行
// 铺底色、把行内变化的字符再压深一档，并把连续同类且没有行内高亮的行合并进同一个元素。
//
// 为什么合并：一次忘记写 .gitignore 就能产生几万行新增，按行建元素会让浏览器为几万个节点
// 排版（本视图没有虚拟滚动）。合并后典型的增删块只有个位数节点，而视觉上完全一致；
// 只有带行内高亮的行必须独占一个元素（它内部还有子元素），这类行的数量由改动块的规模决定，
// 与文件总行数无关
function diffHTML(text, allAdded) {
  const lines = text.split('\n');
  // 统一 diff 与文件正文都以换行结尾，split 会多出一个空串；不去掉它，末尾就会多出一条空行
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();

  // allAdded 用于未跟踪文件：那边拿到的是文件正文而不是 diff，每一行都是新增，
  // 也就没有对面那一侧可比，行内区间恒为空
  const classes = lines.map((line) => (allAdded ? 'add' : diffLineClass(line)));
  // 未跟踪文件拿到的是文件正文而不是 diff，行首本来就没有标记，剥了就是白丢一个字符
  const body = allAdded ? lines : lines.map((line, i) => diffLineBody(classes[i], line));
  const spans = diffLineSpans(body, classes);

  const groups = [];
  for (let i = 0; i < body.length; i++) {
    const cls = classes[i];
    const last = groups[groups.length - 1];
    if (spans[i]) {
      groups.push({ cls: cls, html: hlLineHTML(body[i], spans[i]) });
      continue;
    }
    if (last && last.cls === cls && last.html === undefined) last.lines.push(body[i]);
    else groups.push({ cls: cls, lines: [body[i]] });
  }

  return groups
    .map(
      (g) =>
        '<span class="dl' +
        (g.cls ? ' ' + g.cls : '') +
        '">' +
        (g.html === undefined ? esc(g.lines.join('\n')) : g.html) +
        '</span>',
    )
    .join('');
}

// diffSectionTitle 给一段正文挑标题。
//
// 段的种类由服务端给（staged / unstaged / commit），文案在页面这一侧取——
// 后端不认识页面上的翻译表（那份表只服务页面自己），前端文案也不该走 Go 的提取管线
function diffSectionTitle(kind, spec) {
  if (kind === 'commit') return (spec.commit && spec.commit.subject) || t('graphNoSubject');
  if (kind === 'unstaged') return t('diffUnstaged');
  return t('diffStaged');
}

// diffFileStat 造一段文件头右侧的增删行数。
// 二进制显示“二进制”而不是 +0 −0：那两个 0 是“git 数不出来”，不是“没改”。
// 字段名与提交卡的文件行一致（都来自 git 的 --numstat），两处不必各记一套
function diffFileStat(f) {
  if (!f) return '';
  if (f.binary) {
    return '<span class="diff-stats"><span class="diff-stat">' + esc(t('graphBinary')) + '</span></span>';
  }
  // 纯改名、只改权限这类改动增删都是 0：右侧再挂一个"+0 −0"只是噪声，
  // 新旧路径那一段已经把事情说清了
  if (f.adds === 0 && f.dels === 0) return '';
  return (
    '<span class="diff-stats">' +
    '<span class="diff-stat add">+' + esc(String(f.adds)) + '</span>' +
    '<span class="diff-stat del">−' + esc(String(f.dels)) + '</span>' +
    '</span>'
  );
}

// diffFileSections 把一段整仓 diff 按文件切开，每段带上名字与增删行数。
//
// 切的位置是行首的 "diff --git "：hunk 正文的每一行都以 +、- 或空格开头，顶格出现这串
// 只可能是文件边界，因此按它切是安全的。
//
// 名字与行数用服务端给的清单（git --numstat -z 的结果：路径原样、改名有新旧两条、
// 顺序与分段一致，见 cmd/ui_diff_test.go），按下标对上；分段比清单还多时退回显示 git
// 原文那一行——位置错开的标题比难看的标题糟糕得多
function diffFileSections(text, files) {
  const parts = text.split(/^diff --git /m);
  const head = parts.shift();
  // 正文不以 "diff --git " 开头时不切：切了会把开头那一小段内容直接丢掉
  if (parts.length === 0 || head.trim() !== '') return null;

  const list = Array.isArray(files) ? files : [];
  // 文本可能因为超过上限被截断，而截断只砍尾部：剩下的分段仍是清单的前缀，按下标配名照样成立。
  // 反过来的情形（分段比清单还多）才是真对不上，那时退回显示 git 原文那一行
  const aligned = list.length >= parts.length;

  return parts.map((body, i) => {
    const f = aligned ? list[i] : null;
    const lines = body.split('\n');
    // 去掉两行纯管道信息：路径已经在标题里，index 行只是一串 blob 哈希。
    // 其余（模式变化、similarity、rename from/to）都留着，它们说的是实际发生的事
    const rawTitle = lines.shift();
    if (lines.length > 0 && lines[0].startsWith('index ')) lines.shift();
    // 尾部的空串不用管：diffHTML 自己会去掉一次（统一 diff 与文件正文都以换行结尾）
    return {
      path: f ? f.path : rawTitle,
      origPath: f ? f.origPath : '',
      stat: diffFileStat(f),
      html: diffHTML(lines.join('\n'), false),
    };
  });
}

// renderDiff 把 /api/diff 的响应画进覆盖层。
// spec 是这次请求的那一行（仓库 + 可选文件 + 可选提交），只用于标题与“要不要提示未跟踪文件”，
// 正文一律来自响应——页面不猜“应该有哪些改动”
function renderDiff(repo, spec, out) {
  const blocks = [];
  const file = spec.file || null;

  if (out.error) {
    blocks.push('<p class="diff-note">' + esc(t('diffFailed', { err: out.error })) + '</p>');
    diffBodyEl.innerHTML = blocks.join('');
    return;
  }

  // 五条提示都放在正文之前：它们说明“下面的内容为什么长这样或为什么不完整”，
  // 放在末尾会被长 diff 推到看不见的地方
  if (out.untracked) blocks.push('<p class="diff-note">' + esc(t('diffUntracked')) + '</p>');
  if (out.unmerged) blocks.push('<p class="diff-note">' + esc(t('diffUnmerged')) + '</p>');
  if (out.binary) blocks.push('<p class="diff-note">' + esc(t('diffBinary')) + '</p>');
  if (out.truncated) blocks.push('<p class="diff-note">' + esc(t('diffTruncated')) + '</p>');
  // 合并提交只跟第一个父提交比（后端把 git 的组合格式挡掉了）：不说明的话，
  // “这次提交就改了这些”会被理解成相对两个父提交的合计
  if (spec.commit && spec.commit.merge) {
    blocks.push('<p class="diff-note">' + esc(t('diffMergeFirstParent')) + '</p>');
  }
  // 整仓视图看不到未跟踪文件（git diff 不含它们）。与其让人以为“这个仓库只有这些改动”，
  // 不如说清它们在哪儿看。提交视图与它无关：那看的是历史，不是当前工作区
  if (!file && !spec.commit) {
    const untracked = (repo.files || []).filter((it) => it.untracked).length;
    if (untracked > 0) {
      blocks.push('<p class="diff-note">' + esc(t('diffUntrackedOmitted', { n: untracked })) + '</p>');
    }
  }

  const sections = (out.sections || []).map((s) => ({
    title: diffSectionTitle(s.kind, spec),
    text: s.text,
    files: s.files,
  }));
  for (const s of sections) {
    // 单文件视图与“切不开”的两段都退回整块渲染：那边只有一份内容，分段没有意义
    const parts = s.files === undefined ? null : diffFileSections(s.text, s.files);
    if (!parts) {
      const html = diffHTML(s.text, !!out.untracked);
      blocks.push('<section><h2>' + esc(s.title) + '</h2><pre class="diff">' + html + '</pre></section>');
      continue;
    }
    const body = parts
      .map(
        (p) =>
          '<section class="diff-file">' +
          '<h3 class="diff-file-head">' +
          // 段落名进标题，是因为标题会一直贴在顶部：滚到一个文件的中段时，上方那行
          // "已暂存/未暂存" 早就滚出视野了，而同一个文件可能两段各出现一次
          '<span class="diff-file-group">' + esc(s.title) + '</span>' +
          (p.origPath ? '<span class="diff-file-from">' + esc(p.origPath) + ' →</span>' : '') +
          '<span class="diff-file-path">' + esc(p.path) + '</span>' +
          p.stat +
          '</h3>' +
          '<pre class="diff">' + p.html + '</pre>' +
          '</section>',
      )
      .join('');
    blocks.push('<section><h2>' + esc(s.title) + '</h2>' + body + '</section>');
  }

  // 只在“既没有分段也没有提示”时才是真的没有改动：二进制未跟踪文件就是这种情形，
  // 它有提示、正文为空，此时说一句“没有改动”会与提示自相矛盾
  if (sections.length === 0 && blocks.length === 0) {
    blocks.push('<p class="diff-note">' + esc(t('diffEmpty')) + '</p>');
  }

  diffBodyEl.innerHTML = blocks.join('');
  // 正文换上来时淡入：它可能一下换成上千行，硬切就是一整片的跳变。
  // 只让新内容淡入，不给上面那个占位做淡出——占位是被整块替换掉的，让它淡出就得与替换抢时序，
  // 反而容易闪
  fadeIn(diffBodyEl);
}

// diffSpec 是覆盖层当前展示的那一行（仓库 + 可选文件）。
// 提交或推送之后要重取一次 diff（暂存区变了，两段内容就跟着变），靠它重建请求
let diffSpec = null;

// draftMsg 按仓库暂存提交信息草稿。
// 为什么需要它：写好了又去翻看看板、或切到另一个仓库再回来，信息不该丢；
// 而把信息留在同一个输入框里不管，则会串仓——给 A 写的信息被提给了 B
const draftMsg = new Map();

// loadDiff 按 diffSpec 取一次 diff 并渲染。
// 与 openDiff 分开，是因为写操作之后要“重取同一份”而不该重走一遍打开的副作用
// （改标题、抢焦点、重置滚动位置）
async function loadDiff() {
  const spec = diffSpec;
  const seq = ++diffSeq;
  // 取数期间摆一个“正在读取”的占位（转动的圈加一行文字），而不是原来那句静止的“加载中”：
  // 这里正是本地接口也可能要等一会儿的地方，一个在动的指示能说明“还在跑、不是卡住”
  diffBodyEl.replaceChildren(loadingBlock());

  const params = new URLSearchParams({ repo: spec.repo.path });
  if (spec.file) params.set('file', spec.file.path);
  if (spec.commit) params.set('commit', spec.commit.hash);

  let out;
  try {
    const res = await fetch('/api/diff?' + params.toString(), { headers: authHeaders, cache: 'no-store' });
    out = await res.json();
    if (!res.ok && !out.error) out = { error: 'HTTP ' + res.status };
  } catch (err) {
    out = { error: err.message };
  }

  // 用户在响应到达之前按了 Esc（或又点开了别的）就把这次结果丢掉，
  // 否则会出现“已经回到看板却又被旧结果写了一次”
  if (seq !== diffSeq || !diffOpen || spec !== diffSpec) return;
  renderDiff(spec.repo, spec, out);
}

// openDiff 打开某个仓库（file 为空 → 整个仓库）或某个文件的 diff。
// 异步：本地接口也有一次往返，期间先显示“加载中”，避免看起来是点了没反应
async function openDiff(spec) {
  const repo = spec.repo;
  const file = spec.file || null;
  diffSpec = spec;

  // 标题只用我们已经知道的信息（仓库名、文件路径、提交短号），不必等接口回来才显示
  const shown = file ? (file.origPath ? file.origPath + ' → ' + file.path : file.path) : '';
  const scope = spec.commit ? shortHash(spec.commit.hash) : '';
  diffTitleEl.innerHTML =
    '<span>' + esc(repo.name) + '</span>' +
    (scope ? '<span class="dir"> ' + esc(scope) + '</span>' : '') +
    (shown ? '<span class="dir"> ' + esc(shown) + '</span>' : '');
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
// “最近一次结果的占位”——下一次写操作、下一次重绘都会把它覆盖掉。用户点完推送，成功信息出现
// 不到一秒就被别的东西冲掉，原话是“一个成功的信息一闪而过，这肯定是不合适的”。
//
// 因此通知不走三行文字那条路：它有独立的容器与独立的列表，不与任何会重绘的元素抢位置，
// 也就不会被冲掉；并且刻意不设定时器，只有用户点关闭才消失——抱怨的根源就是“还没看清就没了”，
// 再挂一个自动消失只是把同一个问题换个地方重现。
//
// 三行文字因此只留给“需要一直可见的持续状态”：取数失败（要一直看着才知道仓库读不出来）、
// 续取上限提示（要一直提醒为什么滚不到更早的提交）、拉取进行中的“正在拉取…”
// （网络操作期间的状态，结束时由结果通知接手）。一次性动作的结果一律走通知

// 通知在内存里的上限。为什么要上限：页面开着不动也会一直攒，上限保证内存与面板高度都是常数级。
// 超限丢最旧的——用户要找的是刚发生的那几条
const NOTIF_MAX = 50;

// NOTIFY_TIMEOUT_MS 是提示自动消失的时长，0 表示不自动消失。它来自配置项 notify_timeout，
// 经首页注入的设置快照读到。默认 0：让提示留着，比猜一个时长、把用户还没看完的提示收走安全。
// 读了配置项的页面改动要刷新才生效，因此这个键也在 cmd/ui_settings.go 的 uiReloadKeys 里
const NOTIFY_TIMEOUT_MS = Math.max(0, Math.round(Number(settingValue('notify_timeout', 0)) || 0)) * 1000;

// notifItems 是通知的唯一数据源，按发生顺序（旧 -> 新）存放，右下角的堆叠与面板里的历史
// 都从它渲染，不各自维护一份。每项含：
//   text / level / at  正文、严重度（info|warn|error）、发生时间
//   el                 右下角那一条的元素（收起或清除后置空），挂在数据上是为了让
//                      “追加一条”不必重建已有元素，也不会让屏幕上的通知重放入场动画
//   dismissed          是否已经被用户收起过：收起只是关掉提示，条目仍留在历史里
//   timer              自动消失的定时器（0 时长或 error 档没有；收起、清除时一并停掉）
const notifItems = [];

// 未读数：右下角堆叠上出现一条不等于用户在面板里看过，因此单独记一个数，
// 打开面板时清零
let notifUnread = 0;

// notifCenterOpen 记录面板开着没有：开着时新通知直接算已读（用户正看着面板），
// 也用于“点面板外面就收起”这条判断
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
    // “点别处就收起面板”是靠 notifCenterEl.contains(e.target) 判断的——按钮已脱离文档时
    // 那个判断必然为假，于是“清除一条”会顺带把面板也关掉
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
// 元素挂在 item.el 上，因此“同一条”跨渲染还认得出来；已在容器里的元素重新 append 只是移动位置，
// 不会重放动画
function renderToasts() {
  for (const item of notifItems) {
    // 收起过的条目不再回到屏幕上，但它还在历史里（见 dismissToast）
    if (item.dismissed) continue;
    if (!item.el) {
      item.el = buildToast(item);
      // 倒计时挂在元素上，且只在造出来时装一次：整块重建会让已在屏幕上的通知
      // 重放动画，也会把倒计时一起重置
      startToastCountdown(item);
    }
    notificationsEl.appendChild(item.el);
  }
  // 清场：超限丢掉的那些、以及被清除掉的，元素都该离开容器
  const keep = new Set(notifItems.filter((it) => !it.dismissed).map((it) => it.el));
  for (const el of Array.from(notificationsEl.children)) {
    if (keep.has(el)) continue;
    // 正在退场的元素交给 removeToastEl 自己收尾：在这里再摘一次，退场动画会当场断掉，
    // 反而比不做动画更难看
    if (el.classList.contains('notif--out')) continue;
    el.remove();
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
    stopToastCountdown(dropped);
    // 被上限挤掉的那条也要淡出，不然整排通知会毫无征兆地少一条
    dropToastEl(dropped);
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

// notifFromResult 是 runWrite 那个“显示结果”回调的适配层：
// 把它的 (文本, 是否出错) 翻成一条通知
function notifFromResult(text, isError) {
  notify(text, isError ? 'error' : 'info');
}

// stopToastCountdown 停掉某条提示的倒计时（本来没在走也安全）。
// 提示被收起、被清除、被上限挤掉时都要停：留着定时器，稍后它会对一条已经不在屏幕上的
// 提示再调一次收起，虽然幂等，但会让“这条通知到底还在不在计时”变得说不清
function stopToastCountdown(item) {
  if (item.timer) {
    clearTimeout(item.timer);
    item.timer = null;
  }
}

// startToastCountdown 给一条提示装上倒计时。三条规则都由“提示是给人看的，不该在没看完时
// 消失”这条推出来：
//   - 时长 0 表示不自动消失（默认就是 0）
//   - error 档不倒计时：错误提示多半要人去处理（拉取失败、提交被拒），
//     正读到一半、或去别处处理完再回来时它已经没了，等于把线索藏起来
//   - 鼠标停在上面就停表，移开重新计时：停上去往往正是要看清楚它
function startToastCountdown(item) {
  if (NOTIFY_TIMEOUT_MS <= 0 || item.level === 'error' || !item.el) return;
  const arm = () => {
    stopToastCountdown(item);
    item.timer = setTimeout(() => {
      item.timer = null;
      dismissToast(item);
    }, NOTIFY_TIMEOUT_MS);
  };
  item.el.addEventListener('mouseenter', () => stopToastCountdown(item));
  item.el.addEventListener('mouseleave', () => {
    if (!item.dismissed) arm();
  });
  arm();
}

// dismissToast 只收起右下角那一条，条目本身留在历史里。
// 与“清除”分开是有意的（VSCode 也是这么分的）：收起弹出来的提示，往往只表示“我看过了、别挡着”，
// 不等于要把这条记录从历史里抹掉——事后回到通知中心还要能翻到它。
// 这也是本文件里 dismissed 与 removeNotif 两套动作并存的原因
function dismissToast(item) {
  stopToastCountdown(item);
  item.dismissed = true;
  // 元素引用要与元素一起收掉，理由见 dropToastEl
  dropToastEl(item);
}

// removeToastEl 把一条提示从右下角摘掉，摘之前先让它淡出。
//
// 为什么不是直接 el.remove()：硬切正好发生在用户刚做完一次写操作、目光会移过去的地方。
//
// 四条路径都走这里（收起、从历史里清除、被上限挤掉、全部清除），其中后两条是必须的：
//   - 正常情况：加 .notif--out 触发退场动画，animationend 时摘除
//   - 系统开了“减少动态效果”：样式表把那一段动画关掉了，animationend 因此永远不会来，
//     此时直接摘除——只靠事件收尾的话，降级模式下通知会一条条永远留在屏幕上
//   - 动画没跑起来时（元素此刻不可见、或被别的动作打断）animationend 同样不会来，
//     再用一个略长于动画的定时器收尾。上限 50 条，漏一条不会成灾，但“通知消不掉”会被看见
function removeToastEl(el) {
  if (!el) return;
  if (reduceMotion.matches) {
    el.remove();
    return;
  }
  let done = false;
  const finish = () => {
    if (done) return;
    done = true;
    el.removeEventListener('animationend', onEnd);
    el.remove();
  };
  // 只认这条通知自己的动画：animationend 会冒泡，直接挂在整条元素上意味着日后往通知里放一个
  // 自带动画的子元素时，子元素先结束的那次事件会把整条通知提前摘掉，退场动画当场断掉
  const onEnd = (e) => {
    if (e.target === el) finish();
  };
  el.addEventListener('animationend', onEnd);
  // 退场动画走 --dur-fast，这里的兜底宽限按它的两倍取：正常情况下它在动画结束时就已经先跑掉了
  setTimeout(finish, 400);
  el.classList.add('notif--out');
}

// dropToastEl 收掉某条通知在右下角的元素，并切断条目对它的引用。
//
// 四条路径都必须成对做这两件事：只收元素而不切断引用，renderToasts 的清场会按 item.el 把它
// 认成“还该在屏幕上”的那一条、又 append 回容器，退场动画做到一半被拽回去；
// 只切断引用而不收元素，它会一直留在屏幕上
function dropToastEl(item) {
  const el = item.el;
  item.el = null;
  removeToastEl(el);
}

// removeNotif 从历史里彻底清掉一条：面板里那一行的 × 与“全部清除”走这条。
// 还在屏幕上的那条提示也要一起收掉——同一个通知不该在历史里没了、提示还挂着
//
// 为什么按对象而不是按下标：面板里每一行的按钮闭包拿着的是点击那一刻的那条通知，
// 而列表在此期间可能已经因为新通知或别处清除而移位，用下标会删错行
function removeNotif(item) {
  const i = notifItems.indexOf(item);
  if (i === -1) return;
  notifItems.splice(i, 1);
  stopToastCountdown(item);
  // 与 dismissToast 同一处理：从历史里清掉时，屏幕上那条也应该淡出而不是硬消失
  dropToastEl(item);
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
  // 一条都没有时“全部清除”没有意义，置灰而不是藏起来——位置固定，不会让标题行跳动
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
  // 显隐改由 .open 类驱动（样式表里配了进出场过渡）：hidden 属性会让元素当场离开渲染树，
  // 退场动画因此没机会播
  notifCenterEl.classList.add('open');
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
  notifCenterEl.classList.remove('open');
  if (returnFocus) bellEl.focus();
}

bellEl.addEventListener('click', () => {
  if (notifCenterOpen) closeNotifCenter(false);
  else openNotifCenter();
});

notifClearAllEl.addEventListener('click', () => {
  for (const item of notifItems) {
    // 全部清除是一次清掉一整排，逐条淡出比整块瞬间消失平稳
    dropToastEl(item);
  }
  notifItems.length = 0;
  // 与另外三条路径一样重绘一次：清场会跳过正在退场的元素（见 renderToasts 里的那条判断），
  // 不会把动画打断，这样“容器里装的就是正在管理的通知”在四条路径上都成立
  renderToasts();
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
// 不设标签的话屏幕阅读器只会念出“按钮”
renderBell();

// ——— 写操作：暂存 / 取消暂存 / 提交 / 推送 ———

// writeBusy 为真时拒绝新的写操作：一次只让一个请求在飞。
// 行尾那两个按钮只有几个像素宽，连点两下的代价是发出两条 git 命令，其中一条必然失败，
// 弹出一条让人摸不着头脑的错误
let writeBusy = false;

// setOp / setBoardOp 各写一处持续状态行：覆盖层标题栏下的 #diff-op，与看板底栏里的 #op。
// 分两处而不是共用一处，是因为两者可能同时存在，共用一个元素会互相覆盖
//
// 注意它们现在只管“需要一直可见的状态”，不再管一次性动作的结果——那些改走右上角通知，
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
  // 非 JSON 响应（基座直接回的 401/403 纯文本）也要能说出原因，否则页面只会显示“失败了”
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
    // 都是用户判断“到底发生了什么”的唯一依据，页面不再自拟一套说法
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
// 拉取是网络操作，几十个仓库可能要等好几秒：期间在底栏留一行“正在拉取…”，否则会让人以为
// 按钮没反应。这一行是持续状态——操作没结束就一直在，结束时无论成败都由下面的回调清掉，
// 因此留在底栏而不发通知：发成通知的话，操作结束后它会变成一条永远停在“正在拉取”的假消息
function fetchAll() {
  if (writeBusy) return;
  setBoardOp(t('fetching'), false);
  return runWrite('/api/fetch', {}, (text, isError) => {
    setBoardOp('', false);
    notifFromResult(text, isError);
  });
}

// ——— 设置面板 ———
//
// 面板里的每一项都由服务端给的元数据长出来（GET /api/settings）：Kind 决定控件形态、
// Options 决定候选、Min/Max 决定输入边界、ManagedBy 决定是否只读。页面不认识任何一个具体的
// 配置键，新增一项配置因此不必回来改这个文件——这是“配置逻辑与界面共用一份抽象”的直接结果。
//
// 写入走 POST /api/settings，与命令行的 "ggt config set" 是同一份后端实现：解析与校验因此
// 只在后端做一次。页面这边不做取值校验——前端校验只能当提示，不能成为唯一防线

// settingsItems 是服务端给的清单；settingsControls 按配置键索引控件（保存时要读回控件里的值）；
// settingsDirty 只记录用户改过、还没保存的键。
// 保存时只发改过的键：没碰过的项不发送，页面因此不会因为“少认识某个值”而把配置改坏
let settingsItems = [];
let settingsControls = new Map();
let settingsDirty = new Set();
// 打开面板前的焦点元素：关闭时还回去，否则焦点会掉在已经隐藏的浮层里
let settingsFocusReturn = null;

// wrapControl 把控件放进一个容器里再返回。
//
// 为什么要多这一层：面板的样式表按 “.setting-control 里的输入框”这条后代选择器统一给外观，
// 而把 setting-control 直接挂在控件自己身上时，那条选择器匹配不到（控件与 .setting-control
// 是同一个元素），表现是下拉框与数字框还留着浏览器的原生外观，跟卡片里的控件不是一套
function wrapControl(el) {
  const box = document.createElement('div');
  box.appendChild(el);
  return box;
}

// settingsControlFor 按元数据生成一项的控件，返回 { el, get, readOnly }。
// get 给出控件当前的文本形态取值——与后端收字符串一致，页面因此不需要知道任何类型转换
function settingsControlFor(item) {
  // 受命令管理的项（仓库列表）：显示出来让用户知道它存在、现在是什么值，以及该去哪儿改。
  // 禁用而不是不显示——不显示会让人以为这一项不存在
  if (item.managedBy) {
    const box = document.createElement('textarea');
    box.rows = 2;
    box.readOnly = true;
    box.disabled = true;
    box.value = item.value;
    return { el: wrapControl(box), get: () => item.value, readOnly: true };
  }

  // 布尔项用复选框：两个候选放进下拉框也能用，但开关一眼就看得懂
  if (item.kind === 'bool') {
    const label = document.createElement('label');
    label.className = 'setting-bool';
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = item.value === 'true';
    label.appendChild(input);
    return { el: wrapControl(label), get: () => (input.checked ? 'true' : 'false') };
  }

  // 只在候选里取值：下拉框。候选按 Group 分组（主题来自不同来源，组标题是品牌名，不翻译）
  if (item.options.length > 0 && !item.allowCustom) {
    const sel = document.createElement('select');
    const groups = new Map();
    for (const o of item.options) {
      const name = o.group || '';
      if (!groups.has(name)) {
        if (name) {
          const optgroup = document.createElement('optgroup');
          optgroup.label = name;
          sel.appendChild(optgroup);
          groups.set(name, optgroup);
        } else {
          // 没有来源分组的是用户自己放进配置目录的主题，组标题是页面文案
          groups.set(name, sel);
        }
      }
      const option = document.createElement('option');
      option.value = o.value;
      // 空值代表“不做选择”（主题里就是跟随系统）。这一条的显示名由页面给：
      // 服务端那份注册表里是英文，页面按当前语言说才自然
      option.textContent = o.label || (o.value === '' ? t('themeFollow') : o.value);
      groups.get(name).appendChild(option);
    }
    sel.value = item.value;
    // 文件里的值若不在候选里，服务端会把它补进候选（见 internal/config 的 viewOptions），
    // 因此这次赋值总能落在某个候选项上，不会静默显示成别的取值
    return { el: wrapControl(sel), get: () => sel.value };
  }

  // 整数项：数字输入框，边界来自注册表的 Min/Max（边界与各自的解析器由测试钉在一起）
  if (item.kind === 'int') {
    const input = document.createElement('input');
    input.type = 'number';
    input.inputMode = 'numeric';
    if (item.min !== null) input.min = String(item.min);
    if (item.max !== null) input.max = String(item.max);
    input.value = item.value;
    return { el: wrapControl(input), get: () => input.value.trim() };
  }

  // 有候选又允许自由输入（并发数那种）：输入框 + 候选清单。
  // 用 datalist 而不是“下拉框再加一个输入框”：同一个控件既能点选也能手写
  if (item.options.length > 0) {
    const input = document.createElement('input');
    input.type = 'text';
    input.value = item.value;
    const listId = 'setting-list-' + item.key;
    const list = document.createElement('datalist');
    list.id = listId;
    for (const o of item.options) {
      const option = document.createElement('option');
      option.value = o.value;
      list.appendChild(option);
    }
    input.setAttribute('list', listId);
    const wrap = document.createElement('div');
    wrap.append(input, list);
    return { el: wrap, get: () => input.value.trim() };
  }

  // 路径列表：一行一条。多行输入框而不是逗号分隔——路径里本来就可能带空格
  if (item.kind === 'paths') {
    const box = document.createElement('textarea');
    box.rows = 3;
    box.value = item.value;
    return { el: wrapControl(box), get: () => box.value.trim() };
  }

  const input = document.createElement('input');
  input.type = 'text';
  input.value = item.value;
  return { el: wrapControl(input), get: () => input.value.trim() };
}

// settingsRow 画一项配置：标题 + 键名 + 恢复默认按钮 + 控件 + 说明 + 该项的错误行。
//
// 标题与键名都显示：标题是给人读的，键名是给命令行用的（ggt config set <键名> <值>），
// 少哪一个都会让用户在两处之间来回对照
function settingsRow(item) {
  const row = document.createElement('div');
  row.className = 'setting-row';
  row.dataset.key = item.key;

  const head = document.createElement('div');
  head.className = 'setting-head';
  const title = document.createElement('span');
  title.className = 'setting-title';
  title.textContent = item.title || item.key;
  const key = document.createElement('code');
  key.className = 'setting-key';
  key.textContent = item.key;
  head.append(title, key);

  if (!item.managedBy) {
    const reset = document.createElement('button');
    reset.type = 'button';
    reset.className = 'btn btn--secondary setting-reset';
    reset.textContent = t('settingsReset');
    reset.addEventListener('click', () => resetSetting(item.key));
    head.appendChild(reset);
  }
  row.appendChild(head);

  const ctl = settingsControlFor(item);
  settingsControls.set(item.key, ctl);
  ctl.el.classList.add('setting-control');
  if (!ctl.readOnly) {
    // input 与 change 都听：文本框靠 input 即时反馈，下拉框与复选框只发 change
    const onChange = () => {
      // 这一行可能已经被重画换掉：面板每次打开、每次保存成功、以及恢复某项默认都会重画，
      // 而重画要等一次接口往返。用户连续操作时它可能正好夹在“聚焦这个输入框”与
      // “把值写进去并派发 input”之间（实测窗口只有几毫秒，偶发命中），此时被换下的输入框
      // 虽然已经不在页面上，它的监听器仍然连着模块级的待保存集合，于是按自己那个控件的值
      // 记了一笔；而保存时读到的是刚造出来、还是服务端那份的新控件（输入框为空）——
      // 配置文件里就此多出一个用户从没输入过的值。
      // 因此先认出“我已经不是当前控件”，是就什么都不做
      if (settingsControls.get(item.key) !== ctl) return;
      if (ctl.get() === item.value) settingsDirty.delete(item.key);
      else settingsDirty.add(item.key);
      row.classList.toggle('dirty', settingsDirty.has(item.key));
      updateSaveButton();
    };
    ctl.el.addEventListener('input', onChange);
    ctl.el.addEventListener('change', onChange);
  }
  row.appendChild(ctl.el);

  // 说明行：受管理的项说清去哪儿改，其余给合法取值的描述（与服务端报错里那句同源）
  const hint = document.createElement('p');
  hint.className = 'setting-hint';
  hint.textContent = item.managedBy ? t('settingManaged', { cmd: item.managedBy }) : item.expected || '';
  row.appendChild(hint);

  const err = document.createElement('p');
  err.className = 'setting-error';
  err.hidden = true;
  row.appendChild(err);
  return row;
}

// updateSaveButton 让保存按钮反映“有几项改动待保存”，没有改动时禁用。
// 按钮上带数目，用户因此知道自己刚才改了几项，不必回头一个个找
function updateSaveButton() {
  const n = settingsDirty.size;
  settingsSaveEl.disabled = n === 0;
  settingsSaveEl.textContent = n > 0 ? t('settingsSaveCount', { n }) : t('settingsSave');
}

// setSettingsOp 写面板顶部那行结果提示（与 diff、卡片里那一行是同一种角色）
function setSettingsOp(text, isError) {
  settingsOpEl.textContent = text || '';
  settingsOpEl.classList.toggle('error', !!isError);
}

// fetchSettingsView 取一次配置项清单。取数与渲染分开的理由见 resetSetting
async function fetchSettingsView() {
  try {
    const res = await fetch('/api/settings', { headers: authHeaders, cache: 'no-store' });
    const out = await res.json();
    // 非 JSON 响应（基座直接回的 401/403 纯文本）也要能说出原因
    if (!res.ok && !out.error) out.error = 'HTTP ' + res.status;
    return out;
  } catch (err) {
    return { error: err.message };
  }
}

// loadSettings 取清单并重画面板。
// 保存成功后也走它：面板显示的因此永远是配置文件里的真实内容，而不是“用户输入了什么就显示什么”
async function loadSettings() {
  const out = await fetchSettingsView();
  // 用户在响应到达之前关掉了面板（或又打开了一次）就把这次结果丢掉，
  // 否则会出现“已经回到看板却又被旧结果写了一次”
  if (!settingsOpen) return;
  if (out.error) {
    setSettingsOp(t('loadFailed', { err: out.error }), true);
    return;
  }
  renderSettings(out);
}

function renderSettings(out) {
  settingsItems = out.items || [];
  settingsControls = new Map();
  settingsDirty = new Set();
  setSettingsOp('');
  settingsPathEl.textContent = t('settingsPath') + ': ' + (out.path || '');
  settingsBodyEl.replaceChildren();
  if (settingsItems.length === 0) {
    settingsBodyEl.textContent = t('settingsEmpty');
  } else {
    const frag = document.createDocumentFragment();
    for (const item of settingsItems) frag.appendChild(settingsRow(item));
    settingsBodyEl.appendChild(frag);
  }
  updateSaveButton();
}

// saveSettings 把改过的项一次发给服务端。
//
// 一次请求而不是逐项往返：改三项就是三次串行等待，而它们本来可以一起写。
// 失败按项回归属：一项写不进去不该让整批都失败，用户改对那一项再保存即可
async function saveSettings() {
  if (settingsDirty.size === 0) return;
  const values = {};
  for (const key of settingsDirty) {
    const ctl = settingsControls.get(key);
    if (ctl) values[key] = ctl.get();
  }
  settingsSaveEl.disabled = true;
  const out = await postJSON('/api/settings', { values });
  if (out.error) {
    setSettingsOp(out.error, true);
    notify(out.error, 'error');
    updateSaveButton();
    return;
  }

  const failed = Object.keys(out.errors || {});
  if (failed.length > 0) {
    // 逐项贴回它自己那一行：用户不必猜是哪一项被拒了
    for (const key of failed) {
      const row = settingsBodyEl.querySelector('.setting-row[data-key="' + key + '"]');
      if (!row) continue;
      const err = row.querySelector('.setting-error');
      err.textContent = out.errors[key];
      err.hidden = false;
      row.classList.add('invalid');
    }
    setSettingsOp(t('settingsSaveFailed'), true);
    updateSaveButton();
    return;
  }

  // 结果写在面板自己那一行，而不是发成通知：通知堆叠区就在右下角，恰好压在面板页脚那颗
  // 保存按钮上（通知的层级高于浮层），保存一次之后想再点一次就会被它挡住。
  // 这也是 #diff-op / #graph-op 一直以来的做法——动作发生在这个面里，结果就写在这个面里；
  // 只有“这件事在动作结束后还有用”的消息才值得发成通知
  const notes = Object.values(out.notes || {});
  const text = notes.length > 0 ? t('settingsSaved') + ' · ' + notes.join(' ') : t('settingsSaved');
  // notes 是“这项改完还要做什么”，例如语言要下次运行才生效：与结果写在一起，
  // 用户不必去别处找这句话
  setSettingsOp(text, false);
  // 主题这类由服务端烧进首页的取值，只改配置文件不会反映到当前页面上，必须刷新
  if (out.reload) {
    location.reload();
    return;
  }
  await loadSettings();
  // 提示写在重画之后：renderSettings 会先把这一行清空，先写的话会被一起清掉
  setSettingsOp(text, false);
}

// resetSetting 把一项恢复成内置默认值：服务端会把这个键从配置文件里删掉，
// 与命令行的 "ggt config reset" 是同一种做法（文件里因此只留下用户真正改过的项）。
//
// 立即生效而不是等“保存”：它本身就是一次明确的动作，再让用户去按一次保存反而多一步。
// 只重画这一行而不是整块重画：整块重画会把用户还没保存的其他改动一起抹掉
async function resetSetting(key) {
  const out = await postJSON('/api/settings', { unset: [key] });
  const err = out.error || (out.errors || {})[key];
  if (err) {
    setSettingsOp(err, true);
    return;
  }
  if (out.reload) {
    location.reload();
    return;
  }

  const view = await fetchSettingsView();
  if (!settingsOpen || view.error) return;
  const item = (view.items || []).find((it) => it.key === key);
  const row = settingsBodyEl.querySelector('.setting-row[data-key="' + key + '"]');
  if (!item || !row) return;
  // item.value 同步成服务端给的新值，这一行随后的改动比较才有正确的基准
  const idx = settingsItems.findIndex((it) => it.key === key);
  if (idx >= 0) settingsItems[idx] = item;
  settingsDirty.delete(key);
  row.replaceWith(settingsRow(item));
  updateSaveButton();
  // 结果同样写面板自己那一行，理由见 saveSettings
  setSettingsOp(t('settingsSaved'), false);
}

// openSettings 打开面板：先把浮层摆出来再取数据，取数据的那一趟往返期间面板已经可见，
// 不至于点了没反应
async function openSettings() {
  settingsFocusReturn = document.activeElement;
  settingsOpen = true;
  settingsEl.classList.add('open');
  settingsEl.setAttribute('aria-hidden', 'false');
  // 视口锁与遮罩由 syncScrollLock 统一决定，这里不自己改样式
  syncScrollLock();
  stopPolling();
  settingsBackEl.focus();
  await loadSettings();
}

function closeSettings() {
  if (!settingsOpen) return;
  settingsOpen = false;
  settingsEl.classList.remove('open');
  settingsEl.setAttribute('aria-hidden', 'true');
  syncScrollLock();
  resume();
  if (settingsFocusReturn && settingsFocusReturn.focus) settingsFocusReturn.focus();
  settingsFocusReturn = null;
}

// 面板里的固定文案与三个入口在加载时接好：文案要跟随语言，而 index.html 是静态结构、不参与翻译
settingsTitleEl.textContent = t('settings');
settingsBackEl.textContent = '← ' + t('back');
settingsBtnEl.setAttribute('aria-label', t('settings'));
settingsSaveEl.textContent = t('settingsSave');
settingsBtnEl.addEventListener('click', openSettings);
settingsBackEl.addEventListener('click', closeSettings);
settingsSaveEl.addEventListener('click', saveSettings);





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

// pushFromUI 推送当前分支。推送不改动图的内容，但会把“领先 N”这类状态清掉，
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
  // 行尾的动作按钮优先于“打开”：它是行内动作，不该顺带把卡片打开。
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
  // 设置面板与通知中心都是“后开的先收”：面板是整页浮层，压在看板之上，因此排在第一个
  if (settingsOpen) {
    closeSettings();
    return;
  }
  // 通知中心浮在最上面，先收它：一层一层退，顺序与打开时相反。
  // 焦点同时交还铃铛，否则焦点会掉在已经隐藏的面板里
  if (notifCenterOpen) {
    closeNotifCenter(true);
    return;
  }
  // 焦点在提交输入框里时，Esc 先退出输入而不是关掉卡片——否则“想退出输入框”这个动作会把
  // 刚写好的提交信息一起丢掉（它会留在草稿里，但用户并不知道）。再按一次才关
  if (document.activeElement === commitMsgEl) {
    commitMsgEl.blur();
    return;
  }
  if (diffOpen) {
    closeDiff();
    return;
  }
  // 固定的提交详情先收，再收卡片：一层一层退，顺序与打开时相反
  if (cardPinned) {
    hideCommitCard(true);
    return;
  }
  if (cardOpen) closeRepoCard();
});

// ——— 主循环 ———

// boardShown 记录看板是否已经画过第一份数据：只用来决定要不要做那一次首屏淡入（见 render）
let boardShown = false;

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
    // 用类而不是 hidden 属性切换（样式表里配了淡入淡出）：空状态是一整块文字，硬切很突兀
    emptyEl.classList.add('show');
  } else {
    emptyEl.classList.remove('show');
  }

  // 首屏第一份数据画上去时让整块看板淡入。在此之前页面是空的（aria-busy 期间），
  // 一屏卡片“啪”地出现是整页最明显的一次跳变。
  // 只在第一次做：之后每 5 秒的轮询刷新走的是行位移过渡（reconcile 与 layout 那套），
  // 本身已经是平滑的，再叠一层淡入会让每次轮询都闪一下
  if (!boardShown) {
    boardShown = true;
    fadeIn(board);
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
// 浮层打开期间不重排：面板盖住看板时尺寸变化根本看不到，重排还要量一遍行高、纯属白花；
// 关掉浮层时各自会走 resume → refresh，布局在那时补上。判据走 anyOverlayOpen
window.addEventListener('resize', () => {
  if (!anyOverlayOpen() && lastSpecs.length > 0) layout(lastEls, lastSpecs);
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
// 为什么不用 CSS 的 scroll-behavior: smooth：它由 UA 按滚动距离决定时长（明显长于 WHEEL_MS），
// 而每拨一格都会重新发起一次滚动，连续拨动时表现为“上一段动画没走完就被生硬截断”。
//
// 曲线照抄宿主自己的 AvaloniaDesktopKit/Behaviors/SmoothWheelScroll.cs，它又刻意与
// Slint 1.18 的 Flickable 对齐：固定时长（WHEEL_MS）等减速——以恒定减速度走完全程、终点速度恰好
// 降到 0，归一化位置 p(u) = 2u - u²（u 为已过时长占比），起手最快、结尾干脆停住。
// 每个滚轮事件都从“当前位置”重起一段曲线（Slint 本身就是这个行为，不是缺陷），
// 并先按一个标称帧推进一次，保证即使下一帧还没到，拨动当帧也立刻有反馈
// reduceMotion 定义在文件靠前的常量区：这里的平滑滚动与 fadeIn 等 JS 驱动的动画共用它
let scrollAnim = null;
let scrollRaf = 0;

function smoothScrollBy(px) {
  // 减的是视口宽而不是 window.innerWidth：有横向溢出时后者会等于 scrollWidth，相减恒为 0，
  // 滚轮横滚于是彻底失效——窄屏看板正是这种情形（见 viewportSize 的注释）
  const max = Math.max(0, document.documentElement.scrollWidth - viewportSize().w);
  // 目标基于“当前位置”累加：连续拨动因此不会丢失位移，也不会跳回去
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

// 轨迹只由“已过时长”决定，因此与帧率无关：掉帧只会让采样变粗，不会改变曲线形状
function stepScroll(now) {
  scrollRaf = 0;
  const a = scrollAnim;
  if (!a) return;
  // 位置被别人改了（拖滚动条、方向键、触控板横扫）就放弃本轮动画、改为跟随真实位置，
  // 免得两边互相打架。事件可能是异步送达的，所以只能按位置差判断来源、不能用标志位
  // ——参照实现遇到过这个问题：标志位会把自己的每帧写入当成外部滚动，动画第一帧就被掐断
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
    // diff 覆盖层、仓库面板或设置面板打开时把手势让回浏览器：那边要滚的是正文（纵向），
    // 被本处理器抢去转横向会让正文完全滚不动。判据走 anyOverlayOpen，别在这里另写一遍
    if (anyOverlayOpen()) return;
    // 横向手势（触控板横扫、Shift+滚轮）交给浏览器原生处理：那条路自带缓动，手感最好
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY) || e.deltaY === 0) return;
    e.preventDefault();
    // 距离直接用事件给的增量，不强行折算成“一格 60px”：浏览器给的是像素增量
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
  // 浮层打开期间由各自的关闭函数统一恢复（它们会先 refresh 再 startPolling），
  // 这里不能抢先启动，否则看板会在浮层后面偷偷刷新。判据走 anyOverlayOpen：
  // 原先这里自己拼了两个标志、漏掉设置面板，于是开着设置切走再回来会在面板后面刷新
  if (anyOverlayOpen()) return;
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
// 浏览器不会把它当图形画——表现为“什么都没有”，控制台也不报错
const SVG_NS = 'http://www.w3.org/2000/svg';

let cardOpen = false;
let cardSpec = null; // 当前仓库（来自看板快照）
let graphItems = []; // 已加载的“提交 + 泳道”
let graphTotal = 0;
let graphLimit = 0; // 已请求的条数；滚到底翻倍
let graphMaxLimit = 2000;
let graphAllRefs = true; // 默认跨全部分支与 tag
let cardSeq = 0; // 作废过期响应（卡片被关掉、或换了仓库时）
let cardSelected = ''; // 当前固定详情的那条提交

// graphColor 把接口给的变量名包成 var(...)。变量名为空（主题没写那个令牌、也没默认值）时
// 用回退色，而不是留一个空的 stroke——那会让线整条消失
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
  // 描边颜色，都交给 CSS：这两个颜色必须等于“这一行当前的实际底色”，而底色会随 hover 与
  // 选中变化，写进 JS 就固定成了初始底色，hover 时会露出一圈不跟着变的色边。上游把这两件
  // 事也放在样式表里（scm.css 的 .graph > circle 与 circle:last-child 规则，含 hover 与
  // 选中三组变体），此前这里的注释把那段误读成“上游不设填充（SVG 默认黑）”，于是把填充
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
// 只把“颜色 id → CSS 变量”这一步换成接口已经翻好的变量名
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

// 展示用的提交短号长度。取 7 是因为 git 默认的缩写就在这个长度上下。
// 它只用来显示：所有请求传的都是完整哈希，页面不拿短号去认提交（短号在仓库变大后可能不再唯一）
const HASH_SHORT_LEN = 7;

function shortHash(hash) {
  return String(hash || '').slice(0, HASH_SHORT_LEN);
}

// openCommitDiff 看某条提交改了什么：file 为空表示整条提交。
//
// 与工作区的 diff 走同一个覆盖层，因为要解决的问题完全一样（按文件分段、列表、着色）；
// 差别只在请求里带的是 commit 而不是工作区快照
function openCommitDiff(item, file) {
  if (!cardSpec) return;
  // 先收起提示卡：它停在所有浮层之上（提示类的东西不能盖在别的浮层底下），
  // 不收起就会正好压住 diff 正文。被固定的那条提交仍是选中态，退回图上看得见
  hideCommitCard(true);
  openDiff({
    repo: cardSpec.repo,
    file: file ? { path: file.path, origPath: file.origPath } : null,
    // merge 由页面自己算：父提交就在这条提交的元信息里，不必再问服务端
    commit: { hash: item.hash, subject: item.subject, merge: (item.parents || []).length > 1 },
  });
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

  // 尾部一行：还有更多就说“加载中”，到底了就说“已加载全部”。滚动到底的判据就看它
  if (graphItems.length > 0) {
    const tail = mkEl('p', 'hint', graphItems.length >= graphTotal ? t('graphEnd') : t('graphLoading'));
    tail.style.padding = '6px 12px';
    frag.appendChild(tail);
  }

  // 只有从“正在读取”的占位换成真内容时才淡入（也就是首次填充）。
  // 续取时这里同样整表重建，但前面的行位置不动、只是尾部接上一批，再淡入一次会让整块图闪一下
  const fromLoading = !!(graphListEl.firstElementChild && graphListEl.firstElementChild.classList.contains('loading'));
  graphListEl.replaceChildren(frag);
  if (fromLoading) fadeIn(graphListEl);
  graphCountEl.textContent = graphTotal > 0 ? t('graphCount', { shown: graphItems.length, total: graphTotal }) : '';
}

// setGraphOp 写顶栏下面那行提示（取数失败时是 git 的原话）
function setGraphOp(text, isError) {
  graphOpEl.textContent = text || '';
  graphOpEl.classList.toggle('error', !!isError);
}

// loadGraph 取一批历史。more 为真表示“滚到底了，再取一批”——做法是把 limit 翻倍重取，
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
      // 失败时把占位撤掉：留着一个“正在读取”的圈在转，会让人以为还在等，而它已经不会来了。
      // 错误原话在上面的状态行里，列表空着才是此刻该有的样子
      graphListEl.replaceChildren();
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
    // 这也是“切分支成功了吗”在页面上唯一看得见的结果
    const repo = currentRepo();
    graphTitleEl.innerHTML =
      '<span>' + esc(repo.name) + '</span>' +
      (data.branch ? '<span class="dir"> ' + esc(data.branch) + '</span>' : '');
    graphStateEl.textContent = repoStateText(repo);
    renderGraphRows();
  } catch (err) {
    if (seq === cardSeq) {
      setGraphOp(err.message, true);
      // 与上面 data.error 那条同理：占位不能留在一个已经失败的请求上
      graphListEl.replaceChildren();
    }
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
// 这一层替代了原来的“仓库面板”：点仓库直接进来，不再有中间那一步选择；仓库自身的操作
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
  // 先摆上“正在读取”的占位：/api/log 要跑一次 git log，仓库大时不是瞬时的，
  // 列表空着会让人以为这个仓库没有提交
  graphListEl.replaceChildren(loadingBlock());

  cardOpen = true;
  graphEl.classList.add('open');
  graphEl.setAttribute('aria-hidden', 'false');
  syncScrollLock();
  stopPolling();
  graphBackEl.focus();

  loadGraph(false);
}

// closeCardState 只收起卡片这一层，不碰焦点与轮询。
// 与关掉整张卡片分开，是为了让“只换内容”这类场景不误触轮询与焦点
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
// “回到上一层”，不是“只关掉最上面那层”
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

// 滚到底续取：判据是“已经滚到最后 120px 以内”，不用 IntersectionObserver——
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

/* ——— 提交详情卡：悬浮即显 + 点击固定 ———
 *
 * 为什么不做成侧栏常驻面板：泳道图占满整个卡片宽度才看得清分叉与合并，侧栏会一直占去一块宽度。
 * 但“只能靠悬浮”也不行——键盘用户根本触发不了 hover，而“点什么就出什么”是这次的要求。
 * 因此做成两级：
 *   - 鼠标移过某一行：立刻贴着光标弹出（元信息来自内存，文件清单异步补上并缓存）
 *   - 点一下（或按上下键 / 回车）：固定，不再随鼠标移开消失，Esc 或点别处才收
 */
const commitCardCache = new Map(); // 提交哈希 -> 文件清单：同一个提交反复划过时不重复请求
let hoverSeq = 0;
let cardPinned = false;
let hoverCardTimer = null;
// cardShownHash 记录卡片此刻显示的是哪条提交：只有从一条提交换到另一条才算“换了内容”，
// 同一条提交异步补上文件清单属于“同一份内容长出来”，不该再做一次交叉淡入
let cardShownHash = '';
// CARD_CROSSFADE_MS 是交叉淡入里那层旧内容的存活时长，必须与样式表的 --dur-fast 等值。
// 不用 transitionend 收尾：降级模式（prefers-reduced-motion）下过渡被整段关掉，事件不会派发，
// 那一层就会永远留在卡片里
const CARD_CROSSFADE_MS = 200;
// lastPointer 记录指针最后一次移动到的位置；hoverSuppressAt 是“刚收起卡片时指针所在的位置”。
//
// 为什么需要后者：收起卡片会让指针下方的元素从卡片换成泳道图的行，浏览器随后就地补派一次
// mouseover 给那个新元素——鼠标其实一动没动。不认这一次的话，点右上角那个 × 收起之后，卡片
// 会立刻被同一个位置重新弹开，用户看到的是“关了又弹”。
// 判据是“坐标与收起时几乎相同”：不用时间窗（慢机器与合成事件有延迟时都不可靠），
// 也不用“等指针移动”（mouseover 可能先于 mousemove 派发，会连带丢掉用户在别的行上的正常悬停）
let lastPointer = { x: -1, y: -1 };
let hoverSuppressAt = null;

// commitCardPosition 把卡片摆在光标右下 14px；靠近右/下边缘时翻到另一侧，别被窗口切掉
function commitCardPosition(x, y) {
  const box = graphPopupEl.getBoundingClientRect();
  // 贴边判断与最终落位都按真实视口算，不用 window.innerWidth / innerHeight：内容溢出时那两个值
  // 会被一起撑大（见 viewportSize），“右边放不下”于是判断不出来，卡片会跨出屏幕右缘
  const vp = viewportSize();
  let left = x + 14;
  let top = y + 14;
  if (left + box.width > vp.w - 8) left = x - box.width - 14;
  if (top + box.height > vp.h - 8) top = y - box.height - 14;
  // 翻转只把卡片挪到锚点的另一侧，救不了“锚点本身就在视口外”这种情形：点击与键盘固定都走
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
  graphPopupEl.classList.remove('open');
  graphPopupEl.classList.remove('pinned');
  // 收起即忘掉“刚才是哪条提交”：下次打开（哪怕是同一条）都算首次出现，
  // 该瞬时落位、不该从上次的位置滑过来，也不该跟旧内容做交叉淡入
  cardShownHash = '';
  // 收起时记住指针位置：卡片一消失，指针下方的元素就换成泳道图的行，浏览器会就地补派一次
  // mouseover 给新元素——那一处坐标与这里几乎相同，据此把它挡掉（见 hoverSuppressAt 的说明）
  hoverSuppressAt = { x: lastPointer.x, y: lastPointer.y };
}

// showCommitCard 立刻画出能拿到的那一半（元信息在内存里，要什么有什么），
// 文件清单随后补上——不为了“一次画全”让卡片迟一拍才出现
function showCommitCard(vm, x, y, pinned) {
  cardPinned = !!pinned;
  graphPopupEl.classList.toggle('pinned', cardPinned);
  // 首次出现（上一刻还收起着）必须瞬时落位，不能从上次停的那个位置滑过来。
  // 这个类只在这一帧里起作用，落位完成后立刻摘掉：后续换行才走位移过渡
  const first = !graphPopupEl.classList.contains('open');
  if (first) graphPopupEl.classList.add('no-move');
  // 显隐改由 .open 类驱动：hidden 属性会让元素当场离开渲染树，退场淡出因此没机会播。
  // 样式表那边同时把“没有 .open 时不接收指针事件”写死了，退场那一档里鼠标点不到它——
  // 这与 hoverSuppressAt 那段要处理的是同一件事，两边配合才成立，不能只留其中一个
  graphPopupEl.classList.add('open');

  // 只有换到另一条提交才算换了内容：同一条提交异步补上文件清单是“同一份内容长出来”，
  // 此时再交叉淡入一次，卡片会在原地无谓地闪一下
  const changed = !first && cardShownHash !== '' && cardShownHash !== vm.item.hash;
  cardShownHash = vm.item.hash;

  const cached = commitCardCache.get(vm.item.hash);
  swapCommitCardContent(renderCommitCard(vm, cached || null, cardPinned), changed);
  commitCardPosition(x, y);
  if (first) {
    // 强制一次回流，让上面那次“无位移过渡”的落位先成为既成事实，再恢复过渡。
    // 顺序反过来的话，left/top 会被当成“从上一个值过渡到新值”，卡片仍旧是飞过来的
    void graphPopupEl.offsetWidth;
    graphPopupEl.classList.remove('no-move');
  }
  if (cached) return;

  const seq = ++hoverSeq;
  const params = new URLSearchParams({ repo: cardSpec.repo.path, hash: vm.item.hash });
  fetch('/api/commit-files?' + params.toString(), { cache: 'no-store', headers: authHeaders })
    .then((res) => res.json())
    .then((data) => {
      // 用户可能已经移到别的提交、或把卡片收起来了：只在卡片还开着时补内容
      if (seq !== hoverSeq || !graphPopupEl.classList.contains('open')) return;
      const files = data && !data.error ? data : null;
      if (!files) return;
      commitCardCache.set(vm.item.hash, files);
      // 补内容不做交叉淡入，理由见上面 changed 那段
      swapCommitCardContent(renderCommitCard(vm, files, cardPinned), false);
      commitCardPosition(x, y);
    })
    .catch(() => {
      // 文件清单取不到不影响元信息：那一半照常显示
    });
}

// swapCommitCardContent 换掉卡片正文。crossfade 为真时，旧正文先搬进一层浮层淡出，
// 新正文已经在它下面——位置动画与内容切换因此同时发生，而不是“内容先硬切、位置再滑”。
//
// 为什么旧正文必须搬走而不是就地淡出：卡片高度由正文撑开，旧正文若还在文档流里，
// 卡片会先按旧高度定一次位（commitCardPosition 拿的是 getBoundingClientRect），
// 等新正文上屏再变一次高度，位移的目标就错了一次，看起来是“滑到半路又拐一下”
function swapCommitCardContent(fragment, crossfade) {
  if (!crossfade || !graphPopupEl.firstElementChild) {
    graphPopupEl.replaceChildren(fragment);
    return;
  }
  // 只搬非浮层的节点：连续快速换行时上一个浮层可能还在淡出，让它留在原地等自己的定时器收，
  // 再搬一次就会把“正在淡出的旧内容”套进新浮层里，叠成洋葱
  const ghost = mkEl('div', 'hovercard-ghost');
  for (const node of Array.from(graphPopupEl.childNodes)) {
    if (node.nodeType === 1 && node.classList.contains('hovercard-ghost')) continue;
    ghost.appendChild(node);
  }
  graphPopupEl.replaceChildren(fragment, ghost);
  // 强制回流后旧内容才有一帧“不透明”的起点，否则加 .out 可能被合并进同一帧，
  // 透明度从 1 直接跳到 0，看着仍旧是硬切
  void graphPopupEl.offsetWidth;
  ghost.classList.add('out');
  // 定时清理而不是等 transitionend：降级模式下过渡被关掉，那个事件不会来（见 CARD_CROSSFADE_MS）
  setTimeout(() => ghost.remove(), CARD_CROSSFADE_MS);
}

// pinCommitCard 把某条提交固定住。键盘路径没有光标，因此贴着那一行定位
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
// pinned 为真时在顶部画一个关闭按钮：固定之后卡片不再随鼠标移开而消失，若没有可见的出口，
// 用户只能靠猜（Esc，或去点别的提交）才能把它收起来。按钮因此只在固定态出现——悬浮预览态
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

    // 整条提交的改动：这张卡片是个预览框，改动明细放不进来
    const openAll = mkEl('button', 'btn btn--secondary open-diff', t('diffOpenCommit'));
    openAll.type = 'button';
    openAll.addEventListener('click', () => openCommitDiff(vm.item, null));
    box.appendChild(openAll);

    const list = mkEl('ul', 'files');
    for (const f of files.files) {
      const li = mkEl('li');
      // 点一行看这个文件在那次提交里改了什么：卡片只说“改了哪些文件”，
      // 看不到改了什么，这正是它此前最缺的一环
      li.classList.add('clickable');
      li.title = t('diffOpenFile');
      li.addEventListener('click', () => openCommitDiff(vm.item, f));
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

// 指针位置要随时记着：hideCommitCard 收起卡片时拿它当“哪一次 mouseover 该被忽略”的基准
document.addEventListener('mousemove', (e) => {
  lastPointer = { x: e.clientX, y: e.clientY };
});

// 鼠标移过某一行就弹卡；移开时给 120ms 宽限再收——不留宽限的话，从行移向卡片的那段空隙
// 会把它闪掉，而“贴着光标”的卡片本来就常常需要把鼠标移进去看更多内容
graphListEl.addEventListener('mouseover', (e) => {
  if (!cardOpen || cardPinned) return;
  // 卡片刚收起时，指针下方的元素由卡片换成了泳道图的行，浏览器会就地补派一次 mouseover，
  // 坐标与收起时几乎相同——认了它就是“点 × 关了又弹”。只忽略这一次。
  // 判据用坐标而不是“等指针移动”：mouseover 可能先于 mousemove 派发，那种顺序下“等移动解除”
  // 会把用户在别的行上的第一次悬停一起丢掉（实际遇到过：卡片再也弹不出来）
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

// 点一下即固定：这条路径不依赖悬浮，鼠标停在别处也能把详情留在屏幕上
graphListEl.addEventListener('click', (e) => {
  const row = e.target.closest('.g-row');
  if (!row) return;
  const vm = graphItems.find((v) => v.item.hash === row.dataset.hash);
  if (vm) pinCommitCard(vm, row);
});

// 键盘：上下键在提交之间移动并固定详情，回车同样固定当前这条。
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
 * 全部走 runWrite：统一处理“一次只允许一个写操作在飞”、失败时把 git 的原话交出来、
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
// 全仓 diff 从卡片顶栏进：它浮在卡片之上，关掉回到卡片（不再占据“入口行”那样的对等地位）
graphDiffEl.addEventListener('click', () => openDiff({ repo: currentRepo() }));