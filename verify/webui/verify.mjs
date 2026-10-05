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

    // 图标有没有居中，读代码看不出来：内联 SVG 落在文字基线上，基线下面还留着降部那段高度，
    // 于是图标看着比按钮中心高。这条直接量几何中心，别等用户看出来了才发现
    await report.check('底栏的设置齿轮与铃铛垂直居中', async () => {
      const geom = await page.evaluate(() => {
        const box = (sel) => {
          const el = document.querySelector(sel)
          if (!el) throw new Error(`找不到 ${sel}`)
          const r = el.getBoundingClientRect()
          return { cy: r.top + r.height / 2, h: r.height }
        }
        return {
          gear: box('#settings-btn .settings-icon'),
          bell: box('#bell .bell-icon'),
          gearBtn: box('#settings-btn'),
          bellBtn: box('#bell'),
        }
      })
      // 两个图标各自的按钮同高、同在一行，因此它们的中心应当几乎相等
      const between = Math.abs(geom.gear.cy - geom.bell.cy)
      if (between > 0.5) {
        throw new Error(`齿轮与铃铛的纵向中心相差 ${between.toFixed(2)}px（齿轮 ${geom.gear.cy}，铃铛 ${geom.bell.cy}）`)
      }
      for (const [name, icon, btn] of [
        ['齿轮', geom.gear, geom.gearBtn],
        ['铃铛', geom.bell, geom.bellBtn],
      ]) {
        const off = Math.abs(icon.cy - btn.cy)
        if (off > 0.5) throw new Error(`${name}没在自己按钮里居中，偏离 ${off.toFixed(2)}px`)
      }
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

    // ——— 按区域换字体：注册表新增两项，服务端渲染时注入一条 :root 规则 ———

    // 这一条从界面走完整个来回，而不是直接调接口：新加的两项要在面板上看得见、改得动、改完刷新页面
    // 就生效，三处里断掉任何一处，用户在界面上看到的结果都是“字体没变”。
    // 断言分两层：一层是浏览器解析出来的字体栈（接线对不对），一层是同一段文字按两套字体栈渲染的
    // 宽度（浏览器真的换到了另一套字体）——只查前者的话，把取值写成一个本机没有的字体名也能通过，
    // 而页面上什么都没变
    await report.check('设置面板里能换字体，保存并刷新后真的生效', async () => {
      // 面板每次打开都是先清空、再按接口返回的内容重画，而重画要等一次接口往返：
      // 打开之后立刻取元素，取到的很可能还是上一版留在页面上的控件，随后那次重画会把它换掉
      // （在这之后填进去的值属于已经被换下的控件，保存按钮也就不会启用）。
      // 这里的办法是先记住当前那一行，等它变成另一个节点，确认看到的是本次打开画出来的那一版
      const openPanel = async () => {
        const stale = await page.$('#settings .setting-row[data-key="font_mono"]')
        await page.locator('#settings-btn').click()
        await page.waitForSelector('#settings.open', { timeout: 5000 })
        if (!stale) return
        await page.waitForFunction(
          (old) => {
            const now = document.querySelector('#settings .setting-row[data-key="font_mono"]')
            return !!now && now !== old
          },
          stale,
          { timeout: 5000 },
        )
      }
      await openPanel()

      // 两片区域都要在面板上，而且都不带候选清单：字体栈由使用者自己写，我们不预设任何一套
      // （内置一份清单等于替人挑字体，而清单里写到的字体在别人机器上多半没装）
      for (const key of ['font_ui', 'font_mono']) {
        const row = page.locator(`#settings .setting-row[data-key="${key}"]`)
        // 用 waitFor 而不是 count：面板每次打开都是先清空再按接口返回的内容重画，
        // 清空与重画之间有一小段什么都没有的空窗期，count 正好落在空窗期里就会误判成“没有这一项”
        await row.waitFor({ state: 'attached', timeout: 5000 })
        const options = await row.locator('datalist option').evaluateAll((els) => els.map((e) => e.value))
        if (options.length !== 0) throw new Error(`${key} 不该再有候选清单，实得 ${JSON.stringify(options)}`)
        if ((await row.locator('input[list]').count()) !== 0) throw new Error(`${key} 的输入框不该再挂候选清单`)
      }
      // 候选清单这套机制本身没被拆掉：并发数那一项仍然用它，只有字体两项不用
      const concRow = page.locator('#settings .setting-row[data-key="concurrency"]')
      await concRow.waitFor({ state: 'attached', timeout: 5000 })
      if ((await concRow.locator('datalist option').count()) === 0) {
        throw new Error('并发数的候选清单不见了：候选清单机制被误删')
      }

      // 默认只声明“特性”，不替使用者挑字体：界面区 sans-serif、等宽区 monospace，两处都是通用族
      // 关键字而不是字体名，具体落到哪一个字体由浏览器回退决定（也正因如此，配置里留空时页面
      // 在各平台都还是无衬线界面 + 等宽代码，不会随系统默认字体变样）。
      // 读的是计算样式而不是源码：样式表里写坏一个值，浏览器会静默丢掉整条声明，只看源码看不出来
      const defaults = await page.evaluate(() => {
        const root = getComputedStyle(document.documentElement)
        return {
          ui: root.getPropertyValue('--font-ui').trim(),
          mono: root.getPropertyValue('--font-mono').trim(),
          bodyFamily: getComputedStyle(document.body).fontFamily,
        }
      })
      if (defaults.ui !== 'sans-serif') throw new Error(`界面字体默认值应为 sans-serif，实得 ${JSON.stringify(defaults.ui)}`)
      if (defaults.mono !== 'monospace') throw new Error(`等宽字体默认值应为 monospace，实得 ${JSON.stringify(defaults.mono)}`)
      if (defaults.bodyFamily !== 'sans-serif') {
        throw new Error(`界面区域不该指定具体字体，应停在 sans-serif，实得 ${JSON.stringify(defaults.bodyFamily)}`)
      }
      report.note(`默认字体：界面区 ${defaults.bodyFamily}，等宽区 ${defaults.mono}`)

      const defaultMono = defaults.mono

      // 量宽度的办法：往页面里临时插一个元素，让它按给定的字体栈渲染同一段文字。
      // 两套字体栈渲染同一段文字的宽度不同，配合下面“元素解析出来的字体栈”就能确认页面上的字
      // 真的换了字形。用临时元素而不是量界面上那个键名：面板内容是异步重画的，
      // 等它、取它、再量它，中间随时可能被重画打断，量出来的宽度也就不作数了
      const keyEl = page.locator('#settings .setting-row[data-key="log_level"] .setting-key')
      const familyOf = async () => {
        await keyEl.waitFor({ state: 'visible', timeout: 5000 })
        return keyEl.evaluate((el) => getComputedStyle(el).fontFamily)
      }
      const textWidth = (family) => page.evaluate((fam) => {
        const span = document.createElement('span')
        span.style.cssText = `position:absolute;left:-9999px;top:0;white-space:pre;font-size:13px;font-family:${fam}`
        // 这个字符串里有下划线与小写字母：等宽字体与比例字体在它们身上的宽度差最明显
        span.textContent = 'font_mono_iii'
        document.body.appendChild(span)
        const width = span.getBoundingClientRect().width
        span.remove()
        return width
      }, family)

      const builtinFamily = await familyOf()
      const builtinWidth = await textWidth(builtinFamily)
      if (!(builtinWidth > 0)) throw new Error(`量不到内置字体栈下这段文字的宽度：${builtinWidth}`)

      const monoInput = page.locator('#settings .setting-row[data-key="font_mono"] input')
      await monoInput.fill('serif')
      // 填完再确认一次：填进去的若是已被换下的那个控件，保存按钮不会启用，
      // 那样后面点保存就会一直等一个永远不启用的按钮，报错也说不清是哪一步出的问题
      const filled = await page.evaluate(() => {
        const input = document.querySelector('#settings .setting-row[data-key="font_mono"] input')
        return input?.value === 'serif' && !document.querySelector('#settings-save').disabled
      })
      if (!filled) throw new Error('填进 font_mono 的取值没有落到当前控件上')
      // 这一项属于“页面已经烧进去”的清单，保存会整页刷新；刷新前留一个标记，刷新后它就不在了，
      // 否则后面的断言可能读到的还是刷新前那份页面（宽度一样，换了也看不出来）
      await page.evaluate(() => { window.__fontSaved = true })
      await page.locator('#settings-save').click()
      await page.waitForFunction(() => window.__fontSaved === undefined, null, { timeout: 10000 })

      const cfg = readConfig()
      if (cfg.font_mono !== 'serif') throw new Error(`配置里的 font_mono 不是 serif：${JSON.stringify(cfg.font_mono)}`)
      const applied = await page.evaluate(
        () => getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim(),
      )
      if (applied !== 'serif') throw new Error(`注入的 --font-mono 不是 serif：${JSON.stringify(applied)}`)

      await openPanel()
      const changedFamily = await familyOf()
      const changedWidth = await textWidth(changedFamily)
      report.note(`等宽字体：默认 ${builtinFamily}；换成 ${changedFamily} 后同一段文字由 ${builtinWidth.toFixed(1)}px 变成 ${changedWidth.toFixed(1)}px`)
      if (changedFamily !== 'serif') {
        throw new Error(`配置键名解析出来的字体栈不是 serif：${JSON.stringify(changedFamily)}`)
      }
      if (Math.abs(changedWidth - builtinWidth) < 1) {
        throw new Error(`改成 serif 后文字宽度没变（${builtinWidth} → ${changedWidth}），页面上的字体没有真的换`)
      }

      // 恢复默认：键从配置文件里删掉，页面回到内置字体栈（宽度也要回到换字体之前）
      await page.evaluate(() => { window.__fontReset = true })
      await page.locator('#settings .setting-row[data-key="font_mono"] button.setting-reset').click()
      await page.waitForFunction(() => window.__fontReset === undefined, null, { timeout: 10000 })

      if ('font_mono' in readConfig()) throw new Error('恢复默认后配置里仍有 font_mono')
      const back = await page.evaluate(
        () => getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim(),
      )
      if (back !== defaultMono) throw new Error(`恢复默认后 --font-mono 应为 ${defaultMono}，实得 ${back}`)

      await openPanel()
      const restoredFamily = await familyOf()
      const restoredWidth = await textWidth(restoredFamily)
      report.note(`恢复默认后字体栈回到 ${restoredFamily}，同一段文字的宽度回到 ${restoredWidth.toFixed(1)}px`)
      if (restoredFamily !== builtinFamily) {
        throw new Error(`恢复默认后字体栈应为 ${builtinFamily}，实得 ${restoredFamily}`)
      }
      if (Math.abs(restoredWidth - builtinWidth) > 0.5) {
        throw new Error(`恢复默认后文字宽度没有回到原值（${builtinWidth} → ${restoredWidth}）`)
      }

      // 收尾把新增的两项滚到看得见的位置：这条检查的截图里就能直接看到它们，
      // 不必再去翻面板下面还藏着什么
      await page.locator('#settings .setting-row[data-key="font_ui"]').scrollIntoViewIfNeeded()
    }, { viewports: ['desktop'] })

    // ——— 重画把正在编辑的控件换掉时，旧控件上的改动不该算进待保存清单 ———

    // 这一条针对一条真实发生过的竞态：面板的重画要等一次接口往返，而重画可能正好夹在
    // “聚焦输入框”与“把值写进去并派发 input”之间（连续操作时窗口只有几毫秒，偶发命中）。
    // 命中之后，被换下的那个输入框虽然已经不在页面上，它的监听器仍然连着模块级的待保存集合，
    // 于是按自己那个控件的值记了一笔；保存时读到的却是新控件（此刻还是服务端那份，输入框为空），
    // 配置文件里就此多出一个用户从没输入过的空值。
    // 这里把那次交错拆开、稳定重演：先让这一次取数慢下来（重画必须在派发 input 之前完成），
    // 再等在页面上把那一行换掉，最后在换下来的输入框上派发一次 input
    await report.check('重画换下的控件不会把改动算成待保存项', async () => {
      const staleInput = await page.$('#settings .setting-row[data-key="font_mono"] input')
      if (!staleInput) throw new Error('font_mono 那一行不在页面上，重演这次交错的前提不成立')
      await page.route('**/api/settings', async (route) => {
        if (route.request().method() === 'GET') await new Promise((r) => setTimeout(r, 300))
        await route.continue()
      })
      await page.locator('#settings-btn').click()
      await page.waitForFunction(
        (old) => document.querySelector('#settings .setting-row[data-key="font_mono"] input') !== old,
        staleInput,
        { timeout: 5000 },
      )
      await staleInput.evaluate((el) => {
        el.value = 'serif'
        el.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const state = await page.evaluate(() => ({
        value: document.querySelector('#settings .setting-row[data-key="font_mono"] input')?.value,
        saveDisabled: document.querySelector('#settings-save').disabled,
      }))
      await page.unroute('**/api/settings')
      if (!state.saveDisabled) {
        throw new Error(`旧控件上的改动被算成了待保存项，保存会写下一个用户没输入过的值（当前控件里是 ${JSON.stringify(state.value)}）`)
      }
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

    // 行内高亮是这一层最容易悄悄失效的地方：引擎挂在另一个脚本上、改动块要自己切，
    // 任何一环出问题都不会报错，只是回到整行着色——所以必须有断言盯住“具体是哪几个字符变了”
    await report.check('行内高亮标出具体改了哪几个字符', async () => {
      await openCommitRow(1, 0)
      await page.locator('#graph-popup .open-diff').click()
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })

      const texts = await page.locator('#diff-body .hl').allTextContents()
      if (texts.length === 0) throw new Error('正文里一处行内高亮都没有')

      // two → two 二：高亮范围应当只是插入的那两个字符，不能是整行
      if (!texts.includes(' 二')) {
        throw new Error(`没有把行尾插入的两个字符单独标出：${JSON.stringify(texts)}`)
      }

      // three → THREE：整行内容都变了，此时高亮注定盖满整行，但也要确实存在
      if (!texts.some((s) => s === 'three' || s === 'THREE')) {
        throw new Error(`没有标出大小写改写：${JSON.stringify(texts)}`)
      }

      // 词级高亮与整行底色必须不是同一个颜色，否则“这一行里是哪几个词变了”又看不出来了
      const pair = await page.evaluate(() => {
        const hl = document.querySelector('#diff-body .dl.add .hl, #diff-body .dl.del .hl')
        if (!hl) return null
        return { hl: getComputedStyle(hl).backgroundColor, row: getComputedStyle(hl.closest('.dl')).backgroundColor }
      })
      if (!pair) throw new Error('正文里找不到带行内高亮的元素')
      if (pair.hl === pair.row) throw new Error(`行内高亮与整行底色同色，等于没标：${pair.hl}`)
      report.note(`行内高亮 ${pair.hl}，所在行的底色 ${pair.row}`)
    })

    // 行首那一个 + / - 是统一 diff 的标记，不是代码的一部分：代码本身以这两个字符开头时
    // （Markdown 列表项、diff 的 diff）会与标记连成 "+- item"，分不清哪一段是内容。
    // 页面改成整行底色加左侧色条来表示增删，这条断言同时盯住“标记真的没了”和“色条真的画了”
    await report.check('正文不再有行首标记，增删改用底色与左侧色条', async () => {
      await openCommitRow(1, 0)
      await page.locator('#graph-popup .open-diff').click()
      await page.waitForSelector('#diff-body .diff-file', { timeout: 10000 })

      // 测试素材里这几行：删掉 two 与 three，新增 two 二 与 THREE。按元素取文本，等于同时确认了
      // 标记已剥掉（带标记时会多出一列）与合并路径仍然按行给出内容
      const added = await page.locator('#diff-body .dl.add').allTextContents()
      const deleted = await page.locator('#diff-body .dl.del').allTextContents()
      if (!added.includes('two 二') || !added.includes('THREE')) {
        throw new Error(`新增行的正文里还留着 + 标记：${JSON.stringify(added)}`)
      }
      if (!deleted.includes('two') || !deleted.includes('three')) {
        throw new Error(`删除行的正文里还留着 - 标记：${JSON.stringify(deleted)}`)
      }

      // 色条与底色都来自主题令牌，但断言不能去读“有没有写渐变”“两个令牌是否相等”这类写法：
      // 那挡不住“色条与底色在屏幕上其实看不出差别”这种失效——这一版之前正是那样
      // （用同一个令牌画 3 像素，或者把色条挡在底色之外）。所以按实际叠色算观感再比
      const colors = await page.evaluate(() => {
        const pick = (sel) => {
          const el = document.querySelector(sel)
          if (!el) return null
          const s = getComputedStyle(el)
          return { bg: s.backgroundColor, rail: s.borderLeftColor, width: s.borderLeftWidth, clip: s.backgroundClip }
        }
        return {
          add: pick('#diff-body .dl.add'),
          del: pick('#diff-body .dl.del'),
          first: pick('#diff-body .dl'),
          card: getComputedStyle(document.querySelector('#diff-body pre.diff')).backgroundColor,
        }
      })
      const transparent = (c) => !c || c === 'transparent' || /rgba\(0, 0, 0, 0\)/.test(c)
      // 把半透明色叠到下层上：色条压在整行底色之上，整行底色又压在卡片底色之上。
      // 下层既可能是 CSS 颜色串，也可能是上一步算出来的结果，两种都收
      const over = (fg, bg) => {
        const parse = (c) => {
          if (typeof c !== 'string') return c
          const n = c.match(/[\d.]+/g).map(Number)
          return { r: n[0], g: n[1], b: n[2], a: n.length > 3 ? n[3] : 1 }
        }
        const f = parse(fg)
        const b = parse(bg)
        return { r: f.a * f.r + (1 - f.a) * b.r, g: f.a * f.g + (1 - f.a) * b.g, b: f.a * f.b + (1 - f.a) * b.b }
      }
      const diff = (a, b) => Math.abs(a.r - b.r) + Math.abs(a.g - b.g) + Math.abs(a.b - b.b)
      const rgb = (c) => `rgb(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)})`

      for (const [name, c] of [['新增行', colors.add], ['删除行', colors.del]]) {
        if (!c) throw new Error(`正文里找不到${name}`)
        if (transparent(c.bg)) throw new Error(`${name}没有整行底色：${c.bg}`)
        if (transparent(c.rail)) throw new Error(`${name}没有左侧色条：${c.rail}`)
        // 底色必须铺到色条之下（border-box）。浏览器没有提供“读出屏幕上实际像素”的接口，
        // 下面那段叠色是按这个前提推算的，因此把这个前提本身也一起确认下来——否则色条被挡在底色之外时，
        // 推算值照样漂亮，屏幕上却什么都看不出来（实测那种写法的色阶差只有 13）
        if (c.clip !== 'border-box') {
          throw new Error(`${name}的底色没有铺到色条之下（background-clip: ${c.clip}），色条会看不见`)
        }
        const row = over(c.bg, colors.card)
        const rail = over(c.rail, row)
        // 20 是实测出来的下限：三通道差值之和低于它就已经看不出边界了。把色条挡在底色之外
        // 的那种写法只有 13，现在的写法是 70 左右，取 20 恰好能把前者挡在门外
        if (diff(rail, row) < 20) {
          throw new Error(`${name}的色条与底色看不出差别（色阶差 ${Math.round(diff(rail, row))}）：色条 ${c.rail}，底色 ${c.bg}`)
        }
        report.note(`${name}：底色观感 ${rgb(row)}，色条观感 ${rgb(rail)}`)
      }
      // 上下文行不染色，但必须给色条让出同样的宽度，否则它的正文会比增删行靠左 3 像素
      if (colors.first && colors.first.width !== colors.add.width) {
        throw new Error(`上下文行没给色条让出同样的宽度：${colors.first.width} 对 ${colors.add.width}`)
      }

      // 上面那条只管住了边框宽度，管不住内边距与行内高亮各自带来的位移——正文左端是否真的对齐
      // 只有量出来才算数（截图上看，增删行里的文字像是比上下文行右移了一点）
      const lefts = await page.evaluate(() => {
        const textLeft = (el) => {
          const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
          while (walker.nextNode()) {
            const node = walker.currentNode
            if (node.textContent.trim() === '') continue
            const range = document.createRange()
            range.selectNodeContents(node)
            const r = range.getBoundingClientRect()
            if (r.width === 0 && r.height === 0) continue
            return r.left
          }
          return null
        }
        const rows = [...document.querySelectorAll('#diff-body .dl')]
        return {
          add: rows.filter((r) => r.classList.contains('add')).map(textLeft),
          del: rows.filter((r) => r.classList.contains('del')).map(textLeft),
          ctx: rows.filter((r) => r.className.trim() === 'dl').map(textLeft),
        }
      })
      const base = lefts.ctx.find((v) => v !== null)
      // 三种行都必须真的量到：量不到就落进下面的循环等于没查（undefined 与数字比较恒为假），
      // 那正是“断言悄悄变成空转”的写法
      if (base === undefined || lefts.add.length === 0 || lefts.del.length === 0) {
        throw new Error(`没有量到三种行的正文位置：上下文 ${lefts.ctx}，新增 ${lefts.add}，删除 ${lefts.del}`)
      }
      if (lefts.add.includes(null) || lefts.del.includes(null)) {
        throw new Error(`有增删行量不出正文位置：新增 ${lefts.add}，删除 ${lefts.del}`)
      }
      for (const [name, list] of [['新增行', lefts.add], ['删除行', lefts.del]]) {
        for (const left of list) {
          if (Math.abs(left - base) > 0.5) {
            throw new Error(`${name}的正文左端与上下文行差了 ${(left - base).toFixed(1)} 像素（${left} 对 ${base}）`)
          }
        }
      }
      report.note(`正文左端：上下文 ${base}，新增 ${lefts.add.join('、')}，删除 ${lefts.del.join('、')}`)
    })

    // 单个改动块的耗时随内容而变（实测每行几十字符的数字表格比批量改名慢一倍多），
    // 上限附近最容易碰到超时那条线，而超时的结果会被整份丢掉；这一条盯的就是这个上界。
    // 它不点界面：测试素材里造不出“两侧各 100 行且彼此高度相似”的内容，直接在页面里调这一层，
    // 跑的是同一个函数、同一份引擎
    await report.check('接近规模上限的改动块也能拿到行内高亮', async () => {
      const out = await page.evaluate(() => {
        const side = (prefix, bump) => {
          const rows = []
          for (let i = 0; i < 100; i++) {
            const nums = []
            for (let j = 0; j < 8; j++) nums.push(String(i * 8 + j + bump))
            rows.push(prefix + nums.join('\t'))
          }
          return rows
        }
        const text = '@@ -1,100 +1,100 @@\n' + side('-', 0).join('\n') + '\n' + side('+', 1).join('\n')
        const t0 = performance.now()
        const html = window.diffHTML(text, false)
        const ms = Math.round(performance.now() - t0)

        // 两行四十万字符：行数上限挡不住它，由字符上限当场判掉，不该白等一次超时
        const freak = '@@ -1,2 +1,2 @@\n-' + 'x'.repeat(400000) + '\n+' + 'y'.repeat(400000)
        const t1 = performance.now()
        const freakHl = (window.diffHTML(freak, false).match(/class="hl"/g) || []).length
        return { ms, hl: (html.match(/class="hl"/g) || []).length, gateMs: Math.round(performance.now() - t1), freakHl }
      })
      report.note(`规模上限的块：${out.ms}ms、${out.hl} 处高亮；两行四十万字符的块：${out.gateMs}ms、${out.freakHl} 处高亮`)
      if (out.hl === 0) throw new Error(`规模上限的改动块一处行内高亮都没有，耗时 ${out.ms}ms`)
      if (out.freakHl !== 0) throw new Error(`超长行的块不该做行内比对，实得 ${out.freakHl} 处高亮`)
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

    // 行内高亮要逐行建元素，这与“连续同类行合并成一个元素”存在直接冲突：
    // 一旦合并失效，几百行的改动就会变成几百个节点，正是当初合并要避免的情形。
    // 因此这条断言盯的不是好不好看，而是元素数有没有随行数一起涨
    await report.check('几百行的改动仍然是合并过的少量元素', async () => {
      await openWholeRepoDiff()

      // 已暂存那份 normal.txt 是整段替换：400 行新增加 3 行删除。
      // 这个规模超过行内比对的上限，因此整段都该走合并路径
      const section = page
        .locator('#diff-body .diff-file')
        .filter({ has: page.locator('.diff-file-path', { hasText: 'normal.txt' }) })
        .filter({ hasText: '已暂存的改动' })
        .first()
      const pre = section.locator('pre.diff')
      const lines = (await pre.textContent()).split('\n').length
      if (lines < 400) throw new Error(`这段只有 ${lines} 行，不再是 400 行那一份，断言失去意义`)

      const nodes = await pre.locator('.dl').count()
      if (nodes > 20) {
        throw new Error(`${lines} 行的改动被拆成 ${nodes} 个元素，合并同类行没有生效`)
      }

      const total = await page.locator('#diff-body .hl').count()
      if (total !== 0) {
        throw new Error(`超过上限的整段替换不该有行内高亮，实得 ${total} 处`)
      }
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