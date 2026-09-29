// 아이콘 래스터화: icon.svg / icon-small.svg → PNG 들 + app.ico (magick 없이 헤드리스 크롬으로).
//
//   cd branding && node render.js            # 크롬 경로가 다르면 CHROME=... node render.js
//
// app.ico 는 PNG 를 그대로 담는 ico(윈도우 비스타 이후 지원): 256·128·64 는 icon.svg, 48·32·16 은 icon-small.svg.
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const CHROME = process.env.CHROME || 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const sleep = ms => new Promise(r => setTimeout(r, ms));

async function main() {
  const prof = fs.mkdtempSync(path.join(os.tmpdir(), 'icon-chrome-'));
  const port = 9400 + Math.floor(Math.random() * 400);
  const chrome = spawn(CHROME, ['--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`,
    '--no-first-run', 'about:blank'], { stdio: 'ignore' });
  try {
    for (let i = 0; i < 60; i++) { try { await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); break; } catch (e) { await sleep(200); } }
    const t = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
    const ws = new WebSocket(t.webSocketDebuggerUrl); await new Promise(r => ws.onopen = r);
    let id = 0; const pend = {};
    ws.onmessage = e => { const m = JSON.parse(e.data); if (m.id && pend[m.id]) { pend[m.id](m); delete pend[m.id]; } };
    const cmd = (method, params = {}) => new Promise(r => { const i = ++id; pend[i] = r; ws.send(JSON.stringify({ id: i, method, params })); });
    await cmd('Page.enable');
    await cmd('Emulation.setDefaultBackgroundColorOverride', { color: { r: 0, g: 0, b: 0, a: 0 } });

    const render = async (svgFile, size) => {
      const svg = fs.readFileSync(path.join(__dirname, svgFile), 'utf8');
      const html = `<html><body style="margin:0;background:transparent"><img style="display:block;width:${size}px;height:${size}px" src="data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}"></body></html>`;
      await cmd('Emulation.setDeviceMetricsOverride', { width: size, height: size, deviceScaleFactor: 1, mobile: false });
      await cmd('Page.navigate', { url: 'data:text/html;base64,' + Buffer.from(html).toString('base64') });
      await sleep(400);
      const r = await cmd('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width: size, height: size, scale: 1 } });
      return Buffer.from(r.result.data, 'base64');
    };

    for (const [file, size] of [['app-icon-1024.png', 1024], ['icon-512.png', 512], ['icon-256.png', 256]]) {
      fs.writeFileSync(path.join(__dirname, file), await render('icon.svg', size));
      console.log('wrote', file);
    }
    const entries = [];
    for (const size of [256, 128, 64]) entries.push([size, await render('icon.svg', size)]);
    for (const size of [48, 32, 16]) entries.push([size, await render('icon-small.svg', size)]);
    fs.writeFileSync(path.join(__dirname, 'app.ico'), ico(entries));
    console.log('wrote app.ico', entries.map(e => e[0]).join('·'));
  } finally {
    chrome.kill();
    try { fs.rmSync(prof, { recursive: true, force: true }); } catch (e) {}
  }
}

// ico 파일: 헤더(6) + 항목(16 × n) + PNG 데이터들
function ico(entries) {
  const head = Buffer.alloc(6);
  head.writeUInt16LE(0, 0); head.writeUInt16LE(1, 2); head.writeUInt16LE(entries.length, 4);
  const dir = Buffer.alloc(16 * entries.length);
  let off = 6 + dir.length;
  entries.forEach(([size, png], i) => {
    const o = i * 16;
    dir.writeUInt8(size >= 256 ? 0 : size, o); dir.writeUInt8(size >= 256 ? 0 : size, o + 1);
    dir.writeUInt8(0, o + 2); dir.writeUInt8(0, o + 3);
    dir.writeUInt16LE(1, o + 4); dir.writeUInt16LE(32, o + 6);
    dir.writeUInt32LE(png.length, o + 8); dir.writeUInt32LE(off, o + 12);
    off += png.length;
  });
  return Buffer.concat([head, dir, ...entries.map(e => e[1])]);
}

main().catch(e => { console.error(e); process.exit(1); });
