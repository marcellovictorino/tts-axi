// tts-axi deck recorder. Starts the Slidev dev server for one deck, opens it
// in a hidden Chrome for Testing on the wall clock (?clock=1&chrome=0), and
// keeps every screencast frame with its own timestamp, so the video can be
// rebuilt at true timing and joined with the narration.
//
//   node record.mjs <deck folder> <frames folder> <chrome> <slidev.mjs> <port> <seconds>
//
// It writes <frames>/fNNNNN.jpg and <frames>/frames.json ({start, frames}),
// prints one JSON line, and stops the server. tts-axi runs it in its own
// process group, so stopping it stops Slidev and Chrome too.
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'

const [,, deckDir, framesDir, chrome, slidevBin, port, seconds] = process.argv
// playwright-core is installed beside Slidev; resolve it from the deck folder.
const require = createRequire(path.join(deckDir, 'package.json'))
const { chromium } = require('playwright-core')
const total = Number(seconds)
// Slidev's dev server listens on localhost only (its --bind needs --remote).
const base = `http://localhost:${port}`
fs.mkdirSync(framesDir, { recursive: true })

const server = spawn(process.execPath, [slidevBin, 'slides.md', '--port', port],
  { cwd: deckDir, stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, BROWSER: 'none', CI: '1' } })
let serverLog = ''
server.stdout.on('data', d => { serverLog = (serverLog + d).slice(-4000) })
server.stderr.on('data', d => { serverLog = (serverLog + d).slice(-4000) })
const fail = (msg) => {
  console.log(JSON.stringify({ error: msg, server: serverLog.replace(/\x1b\[[0-9;]*m/g, '').trim().split('\n').slice(-5).join(' / ') }))
  server.kill('SIGKILL')
  process.exit(1)
}
server.on('exit', code => fail(`Slidev stopped (exit ${code})`))
// Whatever goes wrong, the server goes with the recorder.
process.on('uncaughtException', e => fail(String(e && e.message || e).split('\n')[0]))
process.on('unhandledRejection', e => fail(String(e && e.message || e).split('\n')[0]))

async function waitForServer() {
  const until = Date.now() + 120_000
  while (Date.now() < until) {
    try {
      const r = await fetch(base + '/')
      if (r.ok) return
    } catch {}
    await new Promise(r => setTimeout(r, 300))
  }
  fail('Slidev did not start within 120 s')
}

await waitForServer()
// On macOS the hidden window needs 87 px more than the 720 px it shows, and
// viewport: null lets the page use the window size.
let browser
try {
  browser = await chromium.launch({ headless: true, executablePath: chrome, args: ['--window-size=1280,807', '--autoplay-policy=no-user-gesture-required'] })
  const page = await browser.newPage({ viewport: null })
  const url = `${base}/1?clock=1&chrome=0`
  // The first visit makes Vite prepare its dependencies, which can reload the
  // page; the second visit is the one recorded.
  await page.goto(url, { waitUntil: 'networkidle', timeout: 120_000 })
  await page.waitForFunction(() => typeof window.__narrate === 'function', null, { timeout: 120_000 })
  await page.waitForTimeout(1500)
  await page.goto(url, { waitUntil: 'networkidle', timeout: 120_000 })
  await page.waitForFunction(() => typeof window.__narrate === 'function', null, { timeout: 120_000 })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(1500)

  const cdp = await page.context().newCDPSession(page)
  const frames = []
  cdp.on('Page.screencastFrame', async f => {
    const n = frames.length
    fs.writeFileSync(path.join(framesDir, `f${String(n).padStart(5, '0')}.jpg`), Buffer.from(f.data, 'base64'))
    frames.push(f.metadata.timestamp)
    await cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {})
  })
  await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 88, maxWidth: 1280, maxHeight: 720, everyNthFrame: 1 })
  await page.waitForTimeout(500)
  // The narration's start by the browser's clock, which stamps the frames.
  const start = await page.evaluate(() => window.__narrate())
  await page.waitForFunction(() => window.__narrationDone === true, null, { timeout: (total + 60) * 1000, polling: 200 })
  await page.waitForTimeout(800) // hold the last frame a moment
  await cdp.send('Page.stopScreencast')
  fs.writeFileSync(path.join(framesDir, 'frames.json'), JSON.stringify({ start, frames }))
  const end = await page.evaluate(() => location.pathname)
  console.log(JSON.stringify({ frames: frames.length, end }))
} catch (e) {
  if (browser) await browser.close().catch(() => {})
  fail(String(e && e.message || e).split('\n')[0])
}
await browser.close()
server.removeAllListeners('exit')
server.kill('SIGTERM')
process.exit(0)
