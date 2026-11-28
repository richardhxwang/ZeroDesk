// App Store installs: a second Get while one installs waits its turn, a waiting
// one can be cancelled, a failed install shows an alert that does not move the
// page. The catalogue and install endpoints are mocked (no store or dpkg here).
// usage: node store.mjs [port]
import { chromium } from 'playwright-core';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = process.argv[2] || 18700;
const app = (id, title) => ({
	id, package: id, title, summary: `${title} summary`, description: '', categories: ['Utilities'], author: 'test',
	version: '1.0', published_at: '2026-11-01', updated_at: '2026-11-01', license: 'MIT', source_repo: '', icon: '', size: 1000,
});
const catalog = { apps: [app('alpha', 'Alpha'), app('beta', 'Beta'), app('gamma', 'Gamma'), app('delta', 'Delta')] };

const b = await chromium.launch({ channel: 'chrome' });
const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
await ctx.addInitScript(() => localStorage.setItem('zerodesk:preferences', JSON.stringify({ lang: 'en', seen_terminal_settings: true })));
await ctx.route('**/api/store/catalog*', (r) => r.fulfill({ json: catalog }));
// installs are mocked in the page so the body can stream like the agent's:
// headers at once, "[exit] 0" when the install ends
await ctx.addInitScript(() => {
	window.__hits = [];
	const real = window.fetch;
	window.fetch = async (url, init) => {
		if (String(url).endsWith('/api/store/install')) {
			const { id } = JSON.parse(init.body);
			window.__hits.push({ id, t: Date.now() });
			if (id === 'gamma') return new Response(JSON.stringify({ error: 'dpkg: error processing package gamma' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
			const ms = id === 'alpha' ? 4000 : 500;
			const enc = new TextEncoder();
			const body = new ReadableStream({ async start(c) { c.enqueue(enc.encode('[download] 10\n')); await new Promise((r) => setTimeout(r, ms)); c.enqueue(enc.encode('[exit] 0\n')); c.close(); } });
			return new Response(body, { status: 200, headers: { 'Content-Type': 'text/plain' } });
		}
		return real(url, init);
	};
});
const p = await ctx.newPage();
await p.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'networkidle' });
for (let i = 0; i < 4; i++) { const c = p.locator('section.window [aria-label="close"]').first(); if (!(await c.count())) break; await c.click(); await sleep(400); }
await p.locator('[aria-label="App Store"]').first().click();
await sleep(2500);

let fails = 0;
const expect = (cond, msg) => { console.log((cond ? 'PASS ' : 'FAIL ') + msg); if (!cond) fails++; };
const win = p.locator('section.window.active');
const rowBtn = (title) => win.locator(`div.row:has(b:text-is("${title}")) button.get`).first();
const label = async (title) => ((await rowBtn(title).count()) ? (await rowBtn(title).innerText()).trim() : '(ring)');

await rowBtn('Alpha').click();
await sleep(300);
await rowBtn('Beta').click();
await rowBtn('Delta').click();
await sleep(300);
expect((await label('Alpha')) === '(ring)', `Alpha installing shows the ring (got ${await label('Alpha')})`);
expect((await label('Beta')) === 'Waiting', `Beta waits its turn (got ${await label('Beta')})`);
expect((await label('Delta')) === 'Waiting', `Delta waits its turn (got ${await label('Delta')})`);
const hits0 = await p.evaluate(() => window.__hits);
expect(hits0.length === 1, `only one install request while Alpha runs (got ${hits0.length})`);

await rowBtn('Delta').click(); // cancel the waiting one
await sleep(300);
expect((await label('Delta')) === 'Get', `clicking Waiting cancels it (got ${await label('Delta')})`);

await sleep(5000);
const hits = await p.evaluate(() => window.__hits);
const ids = hits.map((h) => h.id);
expect(ids.join(',') === 'alpha,beta', `installs ran in order and cancelled one never started (got ${ids})`);
expect(hits[1] && hits[1].t - hits[0].t >= 3500, 'Beta started only after Alpha finished');

const before = await win.boundingBox().then(() => p.evaluate(() => document.querySelector('section.window.active .get')?.getBoundingClientRect().top));
await rowBtn('Gamma').click();
await sleep(1500);
const alert = win.locator('[role="alertdialog"]');
expect((await alert.count()) === 1, 'failed install shows an alert');
expect((await alert.innerText()).includes('Couldn’t install “Gamma”'), 'alert names the app');
expect((await alert.innerText()).includes('dpkg: error processing package gamma'), 'alert shows the error');
const after = await p.evaluate(() => document.querySelector('section.window.active .get')?.getBoundingClientRect().top);
expect(before === after, `alert does not push the page down (${before} → ${after})`);
await p.screenshot({ path: process.env.ZD_E2E_SHOT || '/dev/null' });
await alert.locator('button').click();
await sleep(300);
expect((await alert.count()) === 0, 'OK closes the alert');
expect((await label('Gamma')) === 'Get', `Gamma back to Get after failing (got ${await label('Gamma')})`);

await b.close();
console.log(fails ? `${fails} failed` : 'all passed');
process.exit(fails ? 1 : 0);
