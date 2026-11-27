// Terminal regression checks: tabs, panes, focus, find, fit and teardown.
// usage: node terminal.mjs [port] [test-name-prefix]   (see README.md)
import { chromium } from 'playwright-core';
const ZERO = `http://127.0.0.1:${process.argv[2] || 18700}`;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const b = await chromium.launch({ channel: 'chrome' });
let fails = 0;
const expect = (cond, msg, extra = '') => { console.log((cond ? 'PASS ' : 'FAIL ') + msg + (extra ? '  ' + extra : '')); if (!cond) fails++; };

async function open_terminal(vp = { width: 1440, height: 900 }) {
	const ctx = await b.newContext({ viewport: vp });
	await ctx.addInitScript(() => localStorage.setItem('zerodesk:preferences', JSON.stringify({ lang: 'en', seen_terminal_settings: true })));
	const p = await ctx.newPage();
	p.errors = [];
	p.on('pageerror', (e) => p.errors.push(e.message));
	p.on('console', (m) => m.type() === 'error' && p.errors.push(m.text()));
	await p.goto(ZERO + '/', { waitUntil: 'networkidle' });
	for (let i = 0; i < 4; i++) { const c = p.locator('section.window [aria-label="close"]').first(); if (!(await c.count())) break; await c.click(); await sleep(400); }
	await p.locator('[aria-label="Terminal"]').first().click();
	await sleep(2500);
	return p;
}
const run = async (p, cmd, wait = 900) => { await p.keyboard.type(cmd, { delay: 15 }); await p.keyboard.press('Enter'); await sleep(wait); };
// visible panes of the visible tab, with their text and position
const panes = (p) => p.evaluate(() => [...document.querySelectorAll('section.window.active .tab-body:not(.hidden) .cell, section.window.active .tab-body:not(.hidden) .split .pane')].filter((c) => c.classList.contains('cell')).map((c) => { const r = c.getBoundingClientRect(); return { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height), text: c.querySelector('.xterm-rows')?.textContent.replace(/\s+/g, ' ') ?? '' }; }));
// old build has no .cell: fall back to every .pane in the visible tab
const all_panes = async (p) => {
	const c = await panes(p);
	if (c.length) return c;
	return p.evaluate(() => [...document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')].map((c) => { const r = c.getBoundingClientRect(); return { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height), text: c.querySelector('.xterm-rows')?.textContent.replace(/\s+/g, ' ') ?? '' }; }));
};
const tab_els = (p) => p.locator('section.window.active .tab');

async function t1_close_background_tab() {
	const p = await open_terminal();
	await run(p, 'echo TAB-A');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-B');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-C');
	await tab_els(p).nth(1).click(); await sleep(600);           // select B
	await tab_els(p).nth(0).locator('.x').click(); await sleep(800); // close A
	await run(p, 'echo INPUT-1');
	const ps = await all_panes(p);
	const shown = ps.map((x) => x.text).join('|');
	expect(shown.includes('TAB-B') && shown.includes('INPUT-1'), '1 closing a tab left of the current one keeps the current tab and the keyboard', shown.includes('TAB-C') ? '(now on C)' : '');
	await p.context().close();
}

async function t1b_background_exit_keeps_find_focus() {
	const p = await open_terminal();
	await run(p, 'echo TAB-A');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'sleep 4; exit', 300);
	await tab_els(p).nth(0).click(); await sleep(600);          // back to A while B sleeps
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('TAB', { delay: 30 });
	await sleep(5000);                                            // B exits in the background
	const where = await p.evaluate(() => { const a = document.activeElement; return a?.tagName + '.' + (a?.className || '') + (a?.getAttribute('placeholder') ? '[' + a.getAttribute('placeholder') + ']' : ''); });
	expect(where.startsWith('INPUT') && where.includes('Find'), '1b a background tab whose shell exits does not take the keyboard from the find bar', where);
	await p.context().close();
}

async function t2_nested_close_focus() {
	const p = await open_terminal();
	await run(p, 'echo PANE-A');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await run(p, 'echo PANE-B');
	await p.keyboard.press('Meta+Shift+d'); await sleep(1800);
	await run(p, 'echo PANE-C');
	await run(p, 'exit', 1500);
	await run(p, 'echo INPUT-2');
	const ps = await all_panes(p);
	const got = ps.find((x) => x.text.includes('INPUT-2'));
	expect(!!got && got.text.includes('PANE-B'), '2 closing the lower right pane gives the keyboard to the pane above it', got ? (got.text.includes('PANE-A') ? '(went to A)' : '') : '(input lost)');
	await p.context().close();
}

async function t3_find_follows_pane() {
	const p = await open_terminal();
	await run(p, 'echo ONLY\\INA');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	let ps = await all_panes(p);
	await p.mouse.click(ps[0].x + 40, ps[0].y + ps[0].h - 40); await sleep(400);  // focus A
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('ONLYINA', { delay: 30 }); await sleep(800);
	const before = await p.locator('section.window.active .find .count').textContent();
	ps = await all_panes(p);
	await p.mouse.click(ps[1].x + 40, ps[1].y + ps[1].h - 40); await sleep(1000); // focus B (below the find bar)
	const after = await p.locator('section.window.active .find .count').textContent().catch(() => '(no find bar)');
	const a_decos = await p.evaluate(() => { const c = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[0]; return c.querySelectorAll('[class*="xterm-find"], .xterm-decoration').length; });
	expect(before.trim() !== '' && !/no/i.test(before) && (/no/i.test(after) || after.trim() === ''), '3 the match count follows the focused pane', `A: "${before.trim()}" -> B: "${after.trim()}"`);
	expect(a_decos === 0, '3 the previous pane\'s highlights are cleared', `decorations left in A: ${a_decos}`);
	await p.context().close();
}

async function t4_fit_rows() {
	const p = await open_terminal({ width: 1200, height: 800 });
	await p.locator('section.window.active [aria-label="zoom"]').first().click(); await sleep(800);
	await p.keyboard.press('Meta+Shift+d'); await sleep(1800);
	let worst = -99, at = 0;
	for (let h = 560; h <= 900; h += 7) {
		await p.setViewportSize({ width: 1200, height: h }); await sleep(350);
		const over = await p.evaluate(() => Math.max(...[...document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')].map((pane) => {
			const host = pane.querySelector('.host'); const scr = pane.querySelector('.xterm-screen');
			const hr = host.getBoundingClientRect(); const cs = getComputedStyle(host);
			const content_bottom = hr.bottom - parseFloat(cs.paddingBottom);
			return scr.getBoundingClientRect().bottom - Math.min(content_bottom, pane.getBoundingClientRect().bottom);
		})));
		if (over > worst) { worst = over; at = h; }
	}
	expect(worst <= 0.5, '4 the terminal grid fits inside its pane at every window height', `worst overflow ${worst.toFixed(1)} px at viewport height ${at}`);
	await p.context().close();
}

async function t5_destroy_while_loading() {
	const ctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
	await ctx.addInitScript(() => localStorage.setItem('zerodesk:preferences', JSON.stringify({ lang: 'en' })));
	const p = await ctx.newPage();
	const errors = [];
	p.on('pageerror', (e) => errors.push(e.message));
	p.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
	await p.goto(ZERO + '/', { waitUntil: 'networkidle' });
	for (let i = 0; i < 4; i++) { const c = p.locator('section.window [aria-label="close"]').first(); if (!(await c.count())) break; await c.click(); await sleep(400); }
	// hold back every script requested from now on (the xterm modules) for 3 s
	await p.route(/\.js(\?|$)/, async (r) => { await sleep(3000); await r.continue(); });
	await p.locator('[aria-label="Terminal"]').first().click();
	await sleep(1200);
	await p.locator('section.window.active [aria-label="close"]').first().click().catch(() => {});
	await sleep(5000);
	const bad = errors.filter((e) => /parent element|Cannot read|undefined/i.test(e));
	expect(bad.length === 0, '5 closing a pane while xterm is still loading throws nothing', bad.join(' / ').slice(0, 160));
	await ctx.close();
}

async function t6_shortcut_from_find_button() {
	const p = await open_terminal();
	await p.keyboard.press('Meta+f'); await sleep(400);
	const n0 = (await all_panes(p)).length;
	await p.locator('section.window.active .find button').last().focus(); await sleep(200);
	await p.keyboard.press('Meta+d'); await sleep(1800);
	const n1 = (await all_panes(p)).length;
	expect(n1 === n0 + 1, '6 ⌘D splits while a find bar button has the keyboard', `${n0} -> ${n1} panes`);
	const ps = await all_panes(p);
	await p.mouse.click(ps[0].x + 40, ps[0].y + 60); await sleep(400);
	await p.keyboard.press('Meta+d'); await sleep(1800);
	const n2 = (await all_panes(p)).length;
	expect(n2 === n1 + 1, '6 ⌘D in a pane splits exactly once', `${n1} -> ${n2} panes`);
	await p.context().close();
}


const active = (p) => p.evaluate(() => { const a = document.activeElement; return a?.tagName + (a?.getAttribute('placeholder') ? '[' + a.getAttribute('placeholder') + ']' : ''); });
const count = (p) => p.locator('section.window.active .find .count').textContent().then((x) => x.trim());

async function t1c_click_background_x_keeps_find() {
	const p = await open_terminal();
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('zz', { delay: 30 });
	await tab_els(p).nth(0).locator('.x').click(); await sleep(800); // close background A
	const where = await active(p);
	expect(where === 'INPUT[Find]', '1c clicking a background tab\'s × while typing in the find bar keeps the find bar', where);
	await p.context().close();
}

async function t1d_enter_on_background_x() {
	const p = await open_terminal();
	await run(p, 'echo TAB-A');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-B');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-C');
	await tab_els(p).nth(0).locator('.x').focus();
	await p.keyboard.press('Enter'); await sleep(800);
	await run(p, 'echo INPUT-3');
	const shown = (await all_panes(p)).map((x) => x.text).join('|');
	expect(shown.includes('TAB-C') && shown.includes('INPUT-3'), '1d Enter on a background tab\'s × keeps the current tab and the keyboard', shown.includes('TAB-B') ? '(now on B)' : '');
	await p.context().close();
}

async function t3b_split_with_query_searches_new_pane() {
	const p = await open_terminal();
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('MARKX', { delay: 30 }); await sleep(500);
	await p.keyboard.press('Meta+d'); await sleep(2500);           // new pane gets the find bar
	const ps = await all_panes(p);
	await p.mouse.click(ps[1].x + 40, ps[1].y + ps[1].h - 40); await sleep(500);
	await run(p, "echo MARK''X", 1500);
	const c = await count(p);
	expect(/1\/1/.test(c), '3b a pane split off with the find bar open is searched once it starts', `count "${c}"`);
	await p.context().close();
}

async function t3c_quick_switch_back_keeps_selection() {
	const p = await open_terminal();
	await run(p, 'echo TWO\\X; echo TWO\\X');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	let ps = await all_panes(p);
	await p.mouse.click(ps[0].x + 40, ps[0].y + ps[0].h - 40); await sleep(400);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('TWOX', { delay: 30 }); await sleep(800);
	const c0 = await count(p);
	await p.mouse.click(ps[1].x + 40, ps[1].y + ps[1].h - 40); await sleep(60);
	await p.mouse.click(ps[0].x + 40, ps[0].y + ps[0].h - 40); await sleep(700);
	await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(500);
	const c1 = await count(p);
	const sel = await p.evaluate(() => getSelection()?.toString() ?? '');
	expect(c0 === '1/2' && c1 === '2/2', '3c switching away and straight back keeps the search working', `"${c0}" -> next -> "${c1}"`);
	await p.context().close();
}

async function t1e_tab_bar_goes_keyboard_stays() {
	const p = await open_terminal();
	await run(p, 'echo TAB-A');
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-B');
	await tab_els(p).nth(1).focus(); await sleep(200);               // keyboard on B's tab head
	await tab_els(p).nth(0).locator('.x').click(); await sleep(800);  // close A: the tab bar goes
	await run(p, 'echo INPUT-4');
	const shown = (await all_panes(p)).map((x) => x.text).join('|');
	expect(shown.includes('INPUT-4'), '1e closing the other tab with the keyboard on a tab head gives the keyboard to the shell', await active(p));
	await p.context().close();
}

async function t3d_pointercancel_still_searches() {
	const p = await open_terminal();
	await run(p, 'echo NEED\\LE');
	await p.keyboard.press('Meta+d'); await sleep(1800);            // B, no match
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
	// a touch on A that the browser cancels (it turned into a scroll)
	await p.evaluate(() => {
		const a = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[0];
		a.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch' }));
		a.querySelector('textarea').focus();
	});
	await sleep(300);
	await p.evaluate(() => dispatchEvent(new PointerEvent('pointercancel', { bubbles: true, pointerType: 'touch' })));
	await sleep(800);
	const c = await count(p);
	expect(/1\/1/.test(c), '3d a touch that is cancelled still searches the pane it focused', `count "${c}"`);
	await p.context().close();
}

async function t3e_drag_select_is_kept() {
	const p = await open_terminal();
	await run(p, 'echo NEED\\LE');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await run(p, 'clear; echo MANUALWORD');
	let ps = await all_panes(p);
	await p.mouse.click(ps[0].x + 40, ps[0].y + ps[0].h - 40); await sleep(400);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
	// drag over MANUALWORD in B
	const box = await p.evaluate(() => {
		const b = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[1];
		const row = [...b.querySelectorAll('.xterm-rows > div')].find((r) => r.textContent.trim() === 'MANUALWORD');
		const scr = b.querySelector('.xterm-screen').getBoundingClientRect();
		const r = row.getBoundingClientRect();
		return { x: scr.left, y: r.top + r.height / 2, off: Math.round(r.top - scr.top) };
	});
	await p.mouse.move(box.x + 2, box.y); await p.mouse.down();
	await p.mouse.move(box.x + 60, box.y, { steps: 8 }); await p.mouse.up();
	await sleep(1000);
	const copied = await p.evaluate((off) => {
		const b = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[1];
		const d = [...b.querySelectorAll('.xterm-selection div')];
		return d.length && Math.round(parseFloat(d[0].style.top)) === off ? 'MANUALWORD row' : `(selection ${d.map((x) => x.style.top).join(',') || 'none'}, row at ${off}px)`;
	}, box.off);
	expect(copied === 'MANUALWORD row', '3e a drag selection in another pane is kept while the find bar is open', `copied "${copied}"`);
	await p.context().close();
}

async function t1f_background_exit_with_tab_head_focused() {
	const p = await open_terminal();
	await run(p, 'sleep 4; exit', 300);
	await p.locator('[aria-label="New Tab"]').first().click(); await sleep(1800);
	await run(p, 'echo TAB-B');
	await tab_els(p).nth(1).focus(); await sleep(200);
	await sleep(5000);                                             // A exits: the tab bar goes
	await run(p, 'echo INPUT-5');
	const shown = (await all_panes(p)).map((x) => x.text).join('|');
	expect(shown.includes('INPUT-5'), '1f a background shell exiting while a tab head has the keyboard gives it to the shell', await active(p));
	await p.context().close();
}

async function t3f_next_after_drag_starts_at_selection() {
	const p = await open_terminal();
	await run(p, 'echo NEED\\LE');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await run(p, 'clear; echo MANUALWORD NEEDLE and NEEDLE');
	let ps = await all_panes(p);
	await p.mouse.click(ps[0].x + 40, ps[0].y + ps[0].h - 40); await sleep(400);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
	const box = await p.evaluate(() => {
		const b = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[1];
		const row = [...b.querySelectorAll('.xterm-rows > div')].find((r) => r.textContent.trim().startsWith('MANUALWORD'));
		const scr = b.querySelector('.xterm-screen').getBoundingClientRect();
		const r = row.getBoundingClientRect();
		return { x: scr.left, y: r.top + r.height / 2, off: Math.round(r.top - scr.top) };
	});
	await p.mouse.move(box.x + 2, box.y); await p.mouse.down();
	await p.mouse.move(box.x + 60, box.y, { steps: 8 }); await p.mouse.up();
	await sleep(1000);
	const sel_w = () => p.evaluate(() => { const d = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[1].querySelector('.xterm-selection div'); return d ? Math.round(parseFloat(d.style.left)) + ':' + Math.round(parseFloat(d.style.width)) : 'none'; });
	const before = await sel_w();
	await sleep(1500);                                              // nothing may move it later
	const later = await sel_w();
	const c0 = await count(p);
	await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(500);
	const c1 = await count(p);
	expect(before !== 'none' && before === later && before.startsWith('0:'), '3f a drag selection stays as made', `${before} -> ${later}`);
	expect(c0 === '' && c1 === '1/2', '3f after a drag, Next goes to the first match after the selection', `"${c0}" -> next -> "${c1}"`);
	await p.context().close();
}

// drag-select `word` (its n-th occurrence) in pane `idx` of the visible tab, measured with a DOM Range
async function drag_word(p, idx, word, n = 0) {
	const r = await p.evaluate(([idx, word, n]) => {
		const pane = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[idx];
		let k = 0;
		for (const row of pane.querySelectorAll('.xterm-rows > div')) {
			const text = row.textContent;
			let at = -1, from = 0;
			while ((at = text.indexOf(word, from)) >= 0) {
				if (k++ === n) {
					const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
					let off = 0, node, sx, ex, y;
					while ((node = walker.nextNode())) {
						const len = node.textContent.length;
						const rg = document.createRange();
						if (sx === undefined && at < off + len) { rg.setStart(node, at - off); rg.setEnd(node, at - off + 1); const b = rg.getBoundingClientRect(); sx = b.left + 1; y = b.top + b.height / 2; }
						const e = at + word.length - 1;
						if (ex === undefined && e < off + len) { rg.setStart(node, e - off); rg.setEnd(node, e - off + 1); const b = rg.getBoundingClientRect(); ex = b.right - 1; }
						off += len;
					}
					return { sx, ex, y };
				}
				from = at + 1;
			}
		}
		return null;
	}, [idx, word, n]);
	await p.mouse.move(r.sx, r.y); await p.mouse.down();
	await p.mouse.move(r.ex, r.y, { steps: 8 }); await p.mouse.up();
}
const copy_in = (p, idx) => p.evaluate((idx) => {
	const pane = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[idx];
	pane.querySelector('textarea').focus();
	let got = '';
	const grab = (e) => (got = e.clipboardData.getData('text/plain'));
	addEventListener('copy', grab);
	document.execCommand('copy');
	removeEventListener('copy', grab);
	return got;
}, idx);
const click_pane = async (p, idx, wait = 400) => { const ps = await all_panes(p); await p.mouse.click(ps[idx].x + 40, ps[idx].y + ps[idx].h - 40); await sleep(wait); };

async function setup_two(p, a_cmd, b_cmd) {
	await run(p, a_cmd);
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await run(p, b_cmd);
	await click_pane(p, 0);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
}

async function t3g_next_after_dragging_a_match() {
	const p = await open_terminal();
	await setup_two(p, 'echo NEED\\LE', 'clear; echo; echo; echo; echo NEED\\LE one NEED\\LE two NEED\\LE');
	await drag_word(p, 1, 'NEEDLE', 0); await sleep(800);
	await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(500);
	const c = await count(p);
	expect(c === '2/3', '3g Next after dragging over the first match goes to the second', `"${c}"`);
	await p.context().close();
}

async function t3h_previous_after_drag() {
	const p = await open_terminal();
	await setup_two(p, 'echo NEED\\LE', 'clear; echo; echo; echo; echo NEED\\LE manual NEED\\LE');
	await drag_word(p, 1, 'manual');
	await sleep(800);
	const kept = await copy_in(p, 1);
	await p.locator('section.window.active .find [aria-label="Previous"]').click(); await sleep(500);
	const c = await count(p);
	expect(kept === 'manual' && c === '1/2', '3h Previous after a drag goes to the match before the selection', `copied "${kept}", "${c}"`);
	await p.context().close();
}

async function t3i_output_then_switch_keeps_new_selection() {
	const p = await open_terminal();
	await run(p, 'echo NEED\\LE; echo NEED\\LE manual');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await click_pane(p, 0);
	await run(p, 'sleep 3; echo LATER', 200);
	await p.keyboard.press('Meta+f'); await sleep(300);
	await p.keyboard.type('NEEDLE', { delay: 30 });
	await p.waitForFunction(() => document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[0].querySelector('.xterm-rows').textContent.includes('LATER'), null, { timeout: 8000, polling: 10 });
	await click_pane(p, 1, 30);
	await click_pane(p, 0, 30);
	await drag_word(p, 0, 'manual');
	await sleep(600);
	const got = await copy_in(p, 0);
	const c = await count(p);
	expect(got === 'manual' && !/No/.test(c), '3i output right before switching panes does not clear a new selection', `copied "${got}", count "${c}"`);
	await p.context().close();
}

// screen position of a cell in pane idx: the row whose text starts with `prefix`, column col (left edge + dx)
async function cell(p, idx, prefix, col, dx = 0.5) {
	return p.evaluate(([idx, prefix, col, dx]) => {
		const pane = document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[idx];
		const rows = [...pane.querySelectorAll('.xterm-rows > div')];
		const row = rows.find((r) => r.textContent.startsWith(prefix));
		const scr = pane.querySelector('.xterm-screen').getBoundingClientRect();
		const cw = scr.width / Number(pane.querySelector('.xterm-screen').dataset.cols || 0) || null;
		const r = row.getBoundingClientRect();
		return { x: scr.left, y: r.top + r.height / 2, h: r.height, w: scr.width };
	}, [idx, prefix, col, dx]);
}
async function find_in_b_then(p, a_cmd) {
	await run(p, 'echo NEED\\LE');
	await p.keyboard.press('Meta+d'); await sleep(1800);
	await click_pane(p, 0);
	await run(p, a_cmd, 1500);
	await click_pane(p, 1);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
}

async function t3j_newline_only_selection() {
	const res = [];
	for (const btn of ['Next', 'Previous']) {
		const p = await open_terminal();
		await find_in_b_then(p, "clear; echo; echo; echo; echo NEED\\LE A; printf 'x%.0s' $(seq 1 $COLUMNS); echo NEED\\LE B; echo NEED\\LE C");
		const a = await cell(p, 0, 'xxxx', 0);
		await p.mouse.move(a.x + a.w - 2, a.y); await p.mouse.down();
		await p.mouse.move(a.x + 3, a.y + a.h, { steps: 8 }); await p.mouse.up(); await sleep(600);
		const sel = await copy_in(p, 0);
		await p.locator(`section.window.active .find [aria-label="${btn}"]`).click(); await sleep(500);
		res.push(JSON.stringify(sel) + ' ' + btn + ' ' + (await count(p)));
		await p.context().close();
	}
	expect(res[0].endsWith('Next 2/3') && res[1].endsWith('Previous 1/3'), '3j a selection of just a line break: Next/Previous move on from it', res.join(' | '));
}

async function t3k_previous_to_match_on_last_row() {
	const p = await open_terminal();
	await find_in_b_then(p, "clear; printf '\\n%.0s' $(seq 1 $(($LINES-1))); printf 'manual NEED''LE'; sleep 60");
	const last = await p.evaluate(() => { const rows = [...document.querySelectorAll('section.window.active .tab-body:not(.hidden) .pane')[0].querySelectorAll('.xterm-rows > div')]; return rows.length - 1 === rows.findIndex((r) => r.textContent.startsWith('manual')); });
	if (!last) console.log('   (3k: manual is not on the last row)');
	await drag_word(p, 0, 'manual'); await sleep(600);
	await p.locator('section.window.active .find [aria-label="Previous"]').click(); await sleep(500);
	const c = await count(p);
	expect(c === '1/1', '3k Previous from a selection on the last row finds its only match', `"${c}"`);
	await p.context().close();
}


async function t3m_block_drag_then_several_nexts() {
	const p = await open_terminal();
	await find_in_b_then(p, "clear; echo; echo; echo; echo manual; printf '.%.0s' $(seq 1 $(($COLUMNS-4))); echo 'NEED''LE NEED''LE'; echo NEED\\LE");
	const r = await drag_word_alt(p, 0, 'manual');
	const seen = [];
	for (let k = 0; k < 3; k++) { await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(400); seen.push(await count(p)); }
	expect(seen.join(' ') === '1/3 2/3 3/3', '3m after an Alt-drag, repeated Next visits every match in order', seen.join(' -> '));
	await p.context().close();
}
async function drag_word_alt(p, idx, word) { await p.keyboard.down('Alt'); await drag_word(p, idx, word); await p.keyboard.up('Alt'); await sleep(600); }

async function t3n_block_drag_then_type_query() {
	const p = await open_terminal();
	await run(p, "clear; echo; echo; echo; echo manual; printf '.%.0s' $(seq 1 $(($COLUMNS-4))); echo 'NEED''LE NEED''LE'; echo NEED\\LE", 1500);
	await drag_word_alt(p, 0, 'manual');
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(600);
	const c0 = await count(p);
	await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(400);
	const c1 = await count(p);
	expect(c0 === '1/3' && c1 === '2/3', '3n Alt-drag, then typing the query, then Next goes to the second match', `"${c0}" -> "${c1}"`);
	await p.context().close();
}

async function t3o_block_drag_then_output_refresh() {
	const p = await open_terminal();
	await run(p, "clear; echo; echo; echo; printf '.%.0s' $(seq 1 $(($COLUMNS-4))); echo 'NEED''LE zz NEED''LE'; echo NEED\\LE; sleep 4; echo LATER", 1500);
	await p.keyboard.press('Meta+f'); await sleep(400);
	await p.keyboard.type('NEEDLE', { delay: 30 }); await sleep(400);
	await p.keyboard.down('Alt'); await drag_word(p, 0, 'zz', 0); await p.keyboard.up('Alt'); await sleep(300);
	await p.waitForFunction(() => document.querySelector('section.window.active .tab-body:not(.hidden) .pane .xterm-rows').textContent.includes('LATER'), null, { timeout: 8000 });
	await sleep(600);                                              // the addon refreshes 200 ms after output
	const c0 = await count(p);
	await p.locator('section.window.active .find [aria-label="Next"]').click(); await sleep(400);
	const c1 = await count(p);
	expect(/^[123]\/3$/.test(c0) && c1 === `${(Number(c0[0]) % 3) + 1}/3`, '3o after an Alt-drag and new output, Next goes to the match after the refreshed one', `"${c0}" -> "${c1}"`);
	await p.context().close();
}

const only = process.argv[3];
const all = [t1_close_background_tab, t1b_background_exit_keeps_find_focus, t1c_click_background_x_keeps_find, t1d_enter_on_background_x, t1e_tab_bar_goes_keyboard_stays, t1f_background_exit_with_tab_head_focused, t2_nested_close_focus, t3_find_follows_pane, t3b_split_with_query_searches_new_pane, t3c_quick_switch_back_keeps_selection, t3d_pointercancel_still_searches, t3e_drag_select_is_kept, t3f_next_after_drag_starts_at_selection, t3g_next_after_dragging_a_match, t3h_previous_after_drag, t3i_output_then_switch_keeps_new_selection, t3j_newline_only_selection, t3k_previous_to_match_on_last_row, t3m_block_drag_then_several_nexts, t3n_block_drag_then_type_query, t3o_block_drag_then_output_refresh, t4_fit_rows, t5_destroy_while_loading, t6_shortcut_from_find_button];
for (const t of all.filter((t) => !only || t.name.startsWith(only))) {
	try { await t(); } catch (e) { fails++; console.log('ERROR ' + t.name + ': ' + e.message.split('\n')[0]); }
}
await b.close();
console.log(fails ? `${fails} failed` : 'all passed');
process.exit(fails ? 1 : 0);
