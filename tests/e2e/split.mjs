// Splitting or closing a pane must keep the other panes' shells and scrollback.
// usage: node split.mjs [port]
import { chromium } from 'playwright-core';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const b = await chromium.launch({ channel: 'chrome' });
const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
await ctx.addInitScript(() => localStorage.setItem('zerodesk:preferences', JSON.stringify({ lang: 'en', seen_terminal_settings: true })));
const p = await ctx.newPage();
await p.goto(`http://127.0.0.1:${process.argv[2] || 18700}/`, { waitUntil: 'networkidle' });
for (let i = 0; i < 4; i++) { const c = p.locator('section.window [aria-label="close"]').first(); if (!(await c.count())) break; await c.click(); await sleep(400); }
await p.locator('[aria-label="Terminal"]').first().click();
await sleep(2500);
const panes = () => p.evaluate(() => [...document.querySelectorAll('section.window.active .tab-body:not(.hidden) .cell')].map((c) => { const r = c.getBoundingClientRect(); return { box: [r.left, r.top, r.width, r.height].map(Math.round), text: c.querySelector('.xterm-rows')?.textContent.replace(/\s+/g, ' ').trim().slice(-120) }; }));
const run = async (cmd) => { await p.keyboard.type(cmd, { delay: 20 }); await p.keyboard.press('Enter'); await sleep(900); };
let fails = 0;
const expect = (cond, msg) => { console.log((cond ? 'PASS ' : 'FAIL ') + msg); if (!cond) fails++; };

await run('echo MARK-A');
await p.locator('[aria-label="Split Right"]').first().click();
await sleep(2000);
await run('echo MARK-B');
let ps = await panes();
expect(ps.length === 2, 'two panes after split right');
expect(ps[0].text.includes('MARK-A'), 'left pane kept its scrollback after split right');
expect(ps[1].text.includes('MARK-B'), 'new pane is the focused one');

await p.keyboard.press('Meta+Shift+d');
await sleep(2000);
await run('echo MARK-C');
ps = await panes();
expect(ps.length === 3, 'three panes after split down');
expect(ps.some((x) => x.text.includes('MARK-A')) && ps.some((x) => x.text.includes('MARK-B')), 'A and B kept after split down');

await run('exit');
await sleep(1500);
ps = await panes();
expect(ps.length === 2, 'two panes after exit');
expect(ps.some((x) => x.text.includes('MARK-A')) && ps.some((x) => x.text.includes('MARK-B')), 'A and B kept after closing C');
expect(ps.every((x) => x.box[2] > 200), 'remaining panes fill the window');
await b.close();
console.log(fails ? `${fails} failed` : 'all passed');
process.exit(fails ? 1 : 0);
