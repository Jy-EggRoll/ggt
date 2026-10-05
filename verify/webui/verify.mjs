// 网页看板的浏览器验收：设置面板能不能开、改了能不能真落盘、整仓 diff 有没有按文件分段、
// 分支图上能不能看某次提交改了什么。
//
// 这一层证明的是接缝——页面渲染、点击、请求、文件真的被改写。配置项的取值规则由
// internal/config 的单测覆盖，diff 数据的形状由 cmd 的单测覆盖，都不在这里重复一遍。
// 临时仓库与隔离配置由同目录的 setup.sh 现造，见那里的说明
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const BASE = process.env.GGT_URL
if (!BASE) throw new Error('缺少 GGT_URL（形如 http://127.0.0.1:8742/?token=...）')
const CONFIG = process.env.GGT_VERIFY_CONFIG
if (!CONFIG) throw new Error('缺少 GGT_VERIFY_CONFIG（由 setup.sh 打印出来的配置文件路径）')
// 有几条用例直接调用接口验证“候选之外的取值会被拒绝”，那些请求要自己带上令牌
const TOKEN = new URLSearchParams(new URL(BASE).search).get('token')

// 浏览器工具链来自 browser-verify skill：截图、档位、清单都不在本仓库重新实现，
// 也不把它拷进仓库。skill 装在别处时用 BROWSER_VERIFY_LIB 指过去
const libDir = process.env.BROWSER_VERIFY_LIB
  || path.join(os.homedir(), '.dsh', 'skills', 'browser-verify', 'lib')

async function loadLib(named) {
  const file = path.join(libDir, named)
  try {
    return await import(pathToFileURL(file).href)
  } catch (err) {
    throw new Error(`读不到浏览器工具链 ${file}：装上 browser-verify skill，或用 BROWSER_VERIFY_LIB 指向它的 lib 目录（原始错误：${err.message}）`)
  }
}

const { launchBrowser, openPage, closePage } = await loadLib('browser.mjs')
const { createRun } = await loadLib('artifacts.mjs')
const { createReport } = await loadLib('report.mjs')
const { resolveViewports } = await loadLib('viewports.mjs')
// 面板的项数不与写死的数字比，而是与首页注入的那份快照比：
// 快照本身就是由配置注册表生成的，于是“注册表里加一项，面板自动多一项、页面一行不用改”
// 这件事才真的被验到了（写死项数只能验出“我有没有记得改测试”）

const presets = resolveViewports(process.env.VP)
const run = createRun({ title: '网页看板：设置面板、整仓 diff 与提交详情', project: 'ggt-webui' })
const report = createReport({ title: '网页看板：设置面板、整仓 diff 与提交详情', run, viewports: presets })
const { browser } = await launchBrowser({ headless: true })

// readConfig 读出被测服务实际写下的配置文件
function readConfig() {
  return JSON.parse(fs.readFileSync(CONFIG, 'utf8'))
}

await report.acrossViewports(async (preset) => {
  const page = await openPage(browser, { preset })
  report.attachPage(() => page)
  const errors = []
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message))
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push('console: ' + m.text())
  })

  try {
    await report.check('看板加载出仓库行', async () => {
      await page.goto(BASE, { waitUntil: 'domcontentloaded' })
      await page.waitForSelector('.row', { timeout: 10000 })
      const n = await page.locator('.row').count()
      if (n === 0) throw new Error('看板上一行都没有')
    })

    await report.check('底栏有设置入口，主题下拉框已移除', async () => {
      if (!(await page.locator('#settings-btn').isVisible())) throw new Error('底栏看不到设置按钮')
      for (const id of ['#theme-select', '#theme-dark', '#theme-light']) {
        const n = await page.locator(id).count()
        if (n !== 0) throw new Error(`${id} 仍然存在 ${n} 个`)
      }
      // 主题清单原来由服务端注入页面，现在这份数据只走 /api/settings
      const injected = await page.evaluate(() => typeof window.__GGT_THEME__)
      if (injected !== 'undefined') throw new Error(`主题数据仍在注入页面：${injected}`)
    })

    await report.check('点齿轮打开面板，列出全部配置项', async () => {
      await page.locator('#settings-btn').click()
      await page.waitForSelector('#settings.open', { timeout: 5000 })
      const rows = page.locator('#settings .setting-row')
      await rows.first().waitFor({ timeout: 5000 })
      const expected = await page.evaluate(() =>
        Array.isArray(window.__GGT_SETTINGS__) ? window.__GGT_SETTINGS__.length : -1,
      )
      const n = await rows.count()
      if (expected <= 0) throw new Error(`首页注入的设置快照是空的：${expected}`)
      if (n !== expected) throw new Error(`面板列出 ${n} 项，注册表有 ${expected} 项`)
      // 逐项对上键名：面板显示的必须就是注册表里那些键，不多不少
      const keys = await page.locator('#settings .setting-row').evaluateAll((els) =>
        els.map((el) => el.dataset.key),
      )
      const want = await page.evaluate(() => window.__GGT_SETTINGS__.map((s) => s.key))
      const missing = want.filter((k) => !keys.includes(k))
      const extra = keys.filter((k) => !want.includes(k))
      if (missing.length || extra.length) {
        throw new Error(`面板与注册表对不上：少了 ${JSON.stringify(missing)}，多了 ${JSON.stringify(extra)}`)
      }
      const titles = await page.locator('#settings .setting-title').allTextContents()
      for (const t of titles) if (!t.trim()) throw new Error('有配置项没有标题')
      if (!titles.includes('并发数')) throw new Error(`标题没有跟随中文：${JSON.stringify(titles)}`)
      const path = await page.locator('#settings-path').textContent()
      if (!path.includes(CONFIG)) throw new Error(`面板没显示配置文件路径：${JSON.stringify(path)}`)
    })

    await report.check('主题控件按来源分组，并有“跟随系统”这一项', async () => {
      const sel = page.locator('#settings .setting-row[data-key="theme"] select')
      const opts = await sel.locator('option').allTextContents()
      if (!opts.includes('跟随系统')) throw new Error(`主题候选里没有跟随系统：${JSON.stringify(opts)}`)
      const groups = await sel.locator('optgroup').count()
      if (groups < 1) throw new Error('主题候选没有按来源分组')
    })

    await report.check('由命令管理的项是只读的，并说明该去哪儿改', async () => {
      const row = page.locator('#settings .setting-row[data-key="repo_paths"]')
      if (await row.locator('button.setting-reset').count() !== 0) throw new Error('仓库列表不该有恢复默认按钮')
      if (!(await row.locator('textarea').isDisabled())) throw new Error('仓库列表应当只读')
      const hint = await row.locator('.setting-hint').textContent()
      if (!hint.includes('ggt repo')) throw new Error(`说明没指出该用哪个命令：${JSON.stringify(hint)}`)
      if (!hint.includes('管理')) throw new Error(`说明没有跟随中文：${JSON.stringify(hint)}`)
    })

    await report.check('Esc 关闭面板并回到看板', async () => {
      await page.keyboard.press('Escape')
      await page.waitForSelector('#settings.open', { state: 'detached', timeout: 5000 }).catch(() => {})
      if (await page.locator('#settings.open').count() !== 0) throw new Error('面板没有关闭')
      if (!(await page.locator('.row').first().isVisible())) throw new Error('回到看板后看不到仓库行')
      // 关闭后页面滚动要解锁，否则看板滚不动
      const overflow = await page.evaluate(() => document.documentElement.style.overflow)
      if (overflow === 'hidden') throw new Error('关闭面板后页面仍被锁住滚动')
    })

    await report.check('仓库卡片仍能打开（浮层接线没被改坏）', async () => {
      await page.locator('.row.head').first().click()
      await page.waitForSelector('#graph.open', { timeout: 5000 })
      await page.keyboard.press('Escape')
      await page.waitForSelector('#graph.open', { state: 'detached', timeout: 5000 }).catch(() => {})
      if (await page.locator('#graph.open').count() !== 0) throw new Error('仓库卡片没有关闭')
    })

    await report.check('全程没有控制台报错', async () => {
      if (errors.length > 0) throw new Error(errors.join(' | '))
    })

    // ——— 写操作：只在桌面档做一遍，避免四档互相改同一份配置 ———

    await report.check('恢复默认会把键从配置文件里删掉', async () => {
      await page.locator('#settings-btn').click()
      await page.waitForSelector('#settings.open', { timeout: 5000 })
      const row = page.locator('#settings .setting-row[data-key="log_level"]')
      await row.locator('button.setting-reset').click()
      await page.waitForFunction(
        () => (document.querySelector('#settings-op')?.textContent || '').includes('设置已保存'),
        null, { timeout: 5000 },
      )
      const cfg = readConfig()
      if ('log_level' in cfg) throw new Error(`恢复默认后文件里仍有 log_level：${JSON.stringify(cfg.log_level)}`)
      const value = await row.locator('select').inputValue()
      if (value !== 'warn') throw new Error(`恢复默认后控件应显示默认值 warn，实得 ${value}`)
    }, { viewports: ['desktop'] })

    await report.check('改了配置并保存，值真的写进了配置文件', async () => {
      const row = page.locator('#settings .setting-row[data-key="log_level"]')
      await row.locator('select').selectOption('debug')
      const label = await page.locator('#settings-save').textContent()
      if (!label.includes('保存（1）')) throw new Error(`保存按钮没反映待保存项数：${JSON.stringify(label)}`)
      await page.locator('#settings-save').click()
      await page.waitForFunction(
        () => (document.querySelector('#settings-op')?.textContent || '').includes('设置已保存')
          && document.querySelectorAll('#settings .setting-row.dirty').length === 0,
        null, { timeout: 5000 },
      )
      const cfg = readConfig()
      if (cfg.log_level !== 'debug') throw new Error(`配置文件里的 log_level 不是 debug：${JSON.stringify(cfg.log_level)}`)
    }, { viewports: ['desktop'] })

    await report.check('候选之外的取值被拒绝，且不落盘', async () => {
      const out = await page.evaluate(async (token) => {
        const res = await fetch('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-WebUI-Token': token },
          body: JSON.stringify({ values: { theme: '/tmp/evil-theme.json' } }),
        })
        return { status: res.status, body: await res.json() }
      }, new URLSearchParams(new URL(BASE).search).get('token'))
      if (out.status !== 200) throw new Error(`期望 200，实得 ${out.status}`)
      const msg = (out.body.errors || {}).theme || ''
      if (!msg.includes('只能是下列取值之一')) throw new Error(`拒绝理由不对：${JSON.stringify(msg)}`)
      // 比的是“值没被改”而不是“键不存在”：前面的用例可能已经把 theme 写进文件了，
      // 键在不在取决于跑过哪些用例，用来判断“拒绝生效”会误报
      if (readConfig().theme === '/tmp/evil-theme.json') throw new Error('被拒绝的取值落盘了')
      const applied = (out.body.applied || []).includes('theme')
      if (applied) throw new Error('被拒绝的取值不该出现在 applied 里')
    }, { viewports: ['desktop'] })

    await report.check('改到页面已烧进去的配置项时要求刷新', async () => {
      const out = await page.evaluate(async (token) => {
        const res = await fetch('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-WebUI-Token': token },
          body: JSON.stringify({ values: { theme: '' } }),
        })
        return await res.json()
      }, new URLSearchParams(new URL(BASE).search).get('token'))
      if (out.reload !== true) throw new Error(`期望 reload 为真，实得 ${JSON.stringify(out.reload)}`)
    }, { viewports: ['desktop'] })

    // ——— 通知自动消失：配合配置项 notify_timeout，页面上看得到效果 ———

    // setNotifyTimeout 直接调接口改这一项（面板改要通过界面点，这里只关心效果），
    // 改完整页刷新一次——页面把时长读成常量，不刷新就还是旧值
    const setNotifyTimeout = async (body) => {
      await page.evaluate(async ({ token, payload }) => {
        await fetch('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-WebUI-Token': token },
          body: JSON.stringify(payload),
        })
      }, { token: TOKEN, payload: body })
      await page.goto(BASE, { waitUntil: 'domcontentloaded' })
    }

    await report.check('提示按配置的秒数自动消失', async () => {
      await setNotifyTimeout({ values: { notify_timeout: '2' } })
      if (!(await page.evaluate(() => typeof notify === 'function'))) throw new Error('页面里没有 notify 可调')
      await page.evaluate(() => notify('自动消失测试', 'info'))
      await page.waitForSelector('#notifications .notif', { timeout: 3000 })
      await page.waitForFunction(() => document.querySelectorAll('#notifications .notif').length === 0, null, { timeout: 8000 })
      // 历史里那条还在：收起提示不等于抹掉记录
      const inHistory = await page.evaluate(() => notifItems.filter((it) => it.text === '自动消失测试').length)
      if (inHistory !== 1) throw new Error(`收起后历史里应仍有一条，实得 ${inHistory}`)
    }, { viewports: ['desktop'] })

    await report.check('鼠标停在提示上就停表，移开重新计', async () => {
      await page.evaluate(() => notify('悬停测试', 'info'))
      const toast = page.locator('#notifications .notif').last()
      await toast.hover()
      await page.waitForTimeout(4000)
      if (!(await page.locator('#notifications .notif').count())) throw new Error('鼠标停在上面时提示却消失了')
      await page.mouse.move(0, 0)
      await page.waitForFunction(() => document.querySelectorAll('#notifications .notif').length === 0, null, { timeout: 8000 })
    }, { viewports: ['desktop'] })

    await report.check('错误提示不自动消失', async () => {
      // 直接摆一条 error 档提示：这里要验的是倒计时对 error 的例外规则。
      // 原本想用“拉取全部”造一条真实错误，但这个测试仓库没有远程，git fetch 成功且无输出，
      // 页面按规矩不为空结果建通知——那测到的是另一件事
      const shown = await page.evaluate(() => {
        const it = notify('错误不倒计时测试', 'error')
        return it ? it.level : ''
      })
      if (shown !== 'error') throw new Error(`没有摆出 error 档提示：${JSON.stringify(shown)}`)
      await page.waitForTimeout(4000)
      if (!(await page.locator('#notifications .notif--error').count())) throw new Error('错误提示被自动收走了')
      // 收尾：清干净，免得影响后面的断言
      await page.evaluate(() => { for (const it of notifItems.slice()) removeNotif(it) })
      if (await page.locator('#notifications .notif').count()) throw new Error('清理后仍有提示挂在屏幕上')
    }, { viewports: ['desktop'] })

    await report.check('改回默认后配置里不留这一项', async () => {
      await setNotifyTimeout({ unset: ['notify_timeout'] })
      if ('notify_timeout' in readConfig()) throw new Error('恢复默认后文件里仍有 notify_timeout')
      await page.locator('#settings-btn').click()
      await page.waitForSelector('#settings.open', { timeout: 5000 })
      const value = await page.locator('#settings .setting-row[data-key="notify_timeout"] input').inputValue()
      if (value !== '0') throw new Error(`面板应显示默认值 0，实得 ${value}`)
    }, { viewports: ['desktop'] })

    await report.check('设置面板里能用滚轮滚动正文', async () => {
      // 这一条必须打真实滚轮。面板正文的滚轮先经过 window 上的处理器，那个处理器
      // 把纵向滚轮改写成看板的横向滚动；它一旦没把设置面板排除掉，正文就完全滚不动，
      // 而拖滚动条仍然有效——所以直接赋 scrollTop 的写法照不出这个问题
      const box = page.locator('#settings-body')
      const metrics = await box.evaluate((el) => ({ h: el.scrollHeight, c: el.clientHeight }))
      if (metrics.h <= metrics.c) throw new Error('面板内容不够长，这条断言失去意义')

      const r = await box.boundingBox()
      await page.mouse.move(r.x + r.width / 2, r.y + r.height / 2)
      await page.mouse.wheel(0, 400)
      await page.waitForTimeout(300)
      if ((await box.evaluate((el) => el.scrollTop)) === 0) {
        throw new Error('设置面板正文用滚轮滚不动，纵向手势被看板的横向滚动抢走了')
      }
    }, { viewports: ['desktop'] })

    // closeAllOverlays 逐层退回看板。
    // 不用"按几下 Esc"：层级是 diff → 提交卡 → 仓库卡片，Esc 由谁消费还取决于焦点，
    // 数次数迟早数错（实测差一层就让后面的用例点不到看板行）。这里每轮先看当前开着什么，再点它自己的出口
    const closeAllOverlays = async () => {
      for (let i = 0; i < 5; i++) {
        if (await page.locator('#diff.open').count()) {
          await page.locator('#diff-back').click()
        } else if (await page.locator('#graph-popup:not([hidden]) .hovercard-top .icon-btn').count()) {
          await page.locator('#graph-popup:not([hidden]) .hovercard-top .icon-btn').click()
        } else if (await page.locator('#graph.open').count()) {
          await page.locator('#graph-back').click()
        } else if (await page.locator('#settings.open').count()) {
          await page.keyboard.press('Escape')
        } else {
          break
        }
        await page.waitForTimeout(200)
      }
    }

    // ——— 从提交卡看某次提交的改动 ———

    // openCommitRow 打开第 i 个仓库的图，点第 j 行提交把详情卡固定（点一下即固定，
    // 所以窄屏上也能走这条路；行尾是卡片的落点，点行首免得被卡片挡住）
    const openCommitRow = async (repoRow, commitRow) => {
      await closeAllOverlays()
      await page.locator('.row.head').nth(repoRow).click()
      await page.waitForSelector('#graph.open', { timeout: 8000 })
      await page.waitForSelector('#graph-list .g-row', { timeout: 10000 })
      await page.locator('#graph-list .g-row').nth(commitRow).click({ position: { x: 20, y: 10 } })
      await page.waitForSelector('#graph-popup .files li', { timeout: 10000 })
    }

    await report.check('提交卡里点文件行能看那次提交里这个文件的改动', async () => {
      await openCommitRow(0, 0)
      // 改名行显示成“旧 → 新”，与 diff 段首的路径写法不同，挑一个非改名的行来对
      const row = page.locator('#graph-popup .files li').filter({ hasNotText: '→' }).first()
      const want = (await row.locator('.path').textContent()).trim()
      await row.click()

      await page.waitForSelector('#diff.open', { timeout: 8000 })
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })
      const title = await page.locator('#diff-title').textContent()
      if (!/[0-9a-f]{7}/.test(title)) throw new Error(`标题里没有提交短号：${title}`)
      const shown = (await page.locator('#diff-body .diff-file-path').first().textContent()).trim()
      if (shown !== want) throw new Error(`打开的是 ${shown}，点的是 ${want}`)
      // 提交视图的段标题是那条提交的主题，不是"已暂存/未暂存"
      const groups = await page.locator('#diff-body .diff-file-group').allTextContents()
      if (groups.some((g) => g.includes('已暂存') || g.includes('未暂存'))) {
        throw new Error(`提交视图里出现了工作区的段标题：${JSON.stringify(groups)}`)
      }
    })

    await report.check('提交卡里能看整条提交的改动，一段一段按文件分好', async () => {
      await openCommitRow(0, 0)
      const n = await page.locator('#graph-popup .files li').count()
      await page.locator('#graph-popup .open-diff').click()
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })

      const got = await page.locator('#diff-body .diff-file').count()
      if (got !== n) throw new Error(`这次提交改了 ${n} 个文件，正文却分出 ${got} 段`)

      const body = await page.locator('#diff-body').textContent()
      // 提交看的是历史，与“当前工作区还有哪些未跟踪文件”无关
      if (body.includes('未跟踪文件不在整仓 diff')) throw new Error('提交视图不该出现未跟踪文件的提示')
      const sticky = await page.evaluate(() =>
        getComputedStyle(document.querySelector('#diff-body .diff-file-head')).position,
      )
      if (sticky !== 'sticky') throw new Error(`提交视图的段首没有贴住顶部：${sticky}`)
    })

    await report.check('合并提交注明只跟第一个父提交比，正文认得出来', async () => {
      await openCommitRow(1, 0) // 第二个仓库的最新一条就是那一次 --no-ff 合并
      const subject = await page.locator('#graph-popup h2').first().textContent()
      if (!subject.includes('合并')) throw new Error(`最新一条不是合并提交：${subject}`)
      await page.locator('#graph-popup .open-diff').click()
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })

      const notes = await page.locator('#diff-body .diff-note').allTextContents()
      if (!notes.some((s) => s.includes('合并提交'))) {
        throw new Error(`没有注明这是相对第一个父提交的改动：${JSON.stringify(notes)}`)
      }
      const body = await page.locator('#diff-body').textContent()
      // 组合格式（--cc）一列里同时写两个父提交的差异，页面按行首单个 +/- 着色，认不了它
      if (body.includes('diff --cc')) throw new Error('正文里出现了组合格式')

      const paths = await page.locator('#diff-body .diff-file-path').allTextContents()
      if (paths.length !== 2) throw new Error(`按第一个父提交应当是 2 个文件，实得 ${JSON.stringify(paths)}`)
    })

    // ——— 整仓 diff 的文件级分段 ———

    // openWholeRepoDiff 从看板进到整仓 diff
    const openWholeRepoDiff = async () => {
      await closeAllOverlays()
      await page.locator('.row.head').first().click()
      await page.waitForSelector('#graph.open', { timeout: 5000 })
      await page.locator('#graph-diff').click()
      await page.waitForSelector('#diff.open', { timeout: 5000 })
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })
    }

    await report.check('整仓 diff 按文件分段，每段带路径与增删行数', async () => {
      await openWholeRepoDiff()

      const n = await page.locator('#diff-body .diff-file').count()
      if (n < 3) throw new Error(`整仓 diff 只分出 ${n} 段，应当每个文件一段`)

      const paths = await page.locator('#diff-body .diff-file-path').allTextContents()
      // 带空格与中文的路径：文件名取自 git --numstat -z，不经页面解析，所以应当是原样的
      if (!paths.includes('新名 文件.txt')) throw new Error(`没有按新路径给出文件名：${JSON.stringify(paths)}`)
      if (!paths.includes('normal.txt')) throw new Error(`少了 normal.txt 那一段：${JSON.stringify(paths)}`)
      for (const p of paths) if (!p.trim()) throw new Error('有文件段没有路径')

      // 改名要能看出从哪来
      const from = await page.locator('#diff-body .diff-file-from').allTextContents()
      if (!from.some((s) => s.includes('旧名 文件.txt'))) throw new Error(`改名没有显示旧路径：${JSON.stringify(from)}`)

      // 增删行数由 git --numstat 给出：长文件是 400 行新增
      const stats = await page.locator('#diff-body .diff-stats').allTextContents()
      if (!stats.some((s) => s.includes('+400'))) throw new Error(`没有显示增删行数：${JSON.stringify(stats)}`)

      // 标题会一直贴在顶部，所以它自己要说明这是哪一段（上方那行分组标题早滚出去了）
      const groups = await page.locator('#diff-body .diff-file-group').allTextContents()
      if (!groups.includes('已暂存的改动') || !groups.includes('未暂存的改动')) {
        throw new Error(`文件标题里缺少段落名：${JSON.stringify(groups)}`)
      }

      // 未跟踪文件不在 git diff 里，页面要说清它们在哪看
      const note = await page.locator('#diff-body .diff-note').allTextContents()
      if (!note.some((s) => s.includes('1 个未跟踪文件'))) {
        throw new Error(`没有提示未跟踪文件不在整仓 diff 里：${JSON.stringify(note)}`)
      }

      // 管道信息不该出现在正文里：路径已经在标题上了
      const body = await page.locator('#diff-body').textContent()
      if (body.includes('diff --git ')) throw new Error('正文里还留着 diff --git 那一行')
    })

    await report.check('滚动到文件中部时标题贴住顶部', async () => {
      const position = await page.evaluate(() =>
        getComputedStyle(document.querySelector('#diff-body .diff-file-head')).position,
      )
      if (position !== 'sticky') throw new Error(`标题的 position 是 ${position}，不是 sticky`)

      const scrolled = await page.evaluate(() => {
        // 往正文里滚一段，落进第一个文件（它就是那份 400 行的改动）的内部：
        // 按偏移量定位，不用"滚固定的 400px"，后者在窄屏上会停在两段之间
        const box = document.getElementById('diff-body')
        box.scrollTop = 300
        return box.scrollTop
      })
      if (scrolled === 0) throw new Error('整仓 diff 滚不动，这条断言就失去意义了')

      await page.waitForTimeout(200)
      const pinned = await page.evaluate(() => {
        const box = document.getElementById('diff-body')
        // 基准取“内容盒”的上沿：sticky 的 top: 0 是相对滚动容器内容盒算的，
        // 容器自己还有一段内边距，直接用边框盒上沿会比实际低这么多
        const padding = parseFloat(getComputedStyle(box).paddingTop) || 0
        const top = box.getBoundingClientRect().top + padding
        const hit = Array.from(document.querySelectorAll('#diff-body .diff-file-head')).find((h) => {
          const r = h.getBoundingClientRect()
          return Math.abs(r.top - top) <= 8 && r.bottom > top
        })
        return hit ? hit.querySelector('.diff-file-path')?.textContent : ''
      })
      if (pinned !== 'normal.txt') {
        throw new Error(`贴住顶部的标题是 ${JSON.stringify(pinned)}，期望正在读的 normal.txt`)
      }
    })

    await report.check('整仓 diff 也能滚动到末尾，没有异常', async () => {
      await page.evaluate(() => {
        const box = document.getElementById('diff-body')
        box.scrollTop = box.scrollHeight
      })
      await page.waitForTimeout(150)
      const atEnd = await page.evaluate(() => {
        const box = document.getElementById('diff-body')
        return box.scrollHeight - box.scrollTop - box.clientHeight < 40
      })
      if (!atEnd) throw new Error('滚不到末尾')
    })
  } finally {
    await closePage(page)
  }
})

const code = report.summary()
await browser.close()
report.finish()
console.log(`截图清单：${run.dir}/shots.json`)
process.exitCode = code