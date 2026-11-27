<script lang="ts">
	import { cached, revalidate } from '🍎/lib/swr.svelte.ts';
	import { data } from '🍎/state/app_preload.ts';
	import Glyph from '../common/Glyph.svelte';
	import { onDestroy, onMount, tick } from 'svelte';
	import WaveSurfer from 'wavesurfer.js';
	import TimelinePlugin from 'wavesurfer.js/dist/plugins/timeline.js';
	import MinimapPlugin from 'wavesurfer.js/dist/plugins/minimap.js';
	import { api, post } from '🍎/lib/api.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { apps } from '🍎/state/apps.svelte.ts';
	import { locale_of, preferences } from '🍎/state/preferences.svelte.ts';
	import { fmt_time, list_audio, type Track } from '../Music/library.ts';

	// Voice Memos for the Zero, laid out like the macOS app: recordings list on
	// the left (56 pt rows, red record button floating at the bottom), and the
	// selected memo on the right: a grey band with a scrolling waveform under a
	// playhead, a time ruler, an overview strip, a big timer and the transport
	// capsule. Waveform, ruler and overview are wavesurfer.js with its Timeline
	// and Minimap plugins. Recording uses the Zero's microphone (agent
	// /api/rec/*) into ~/Recordings, shared with the device's own Recorder app;
	// while recording the window shows only the recording view.

	type Peaks = { duration: number; peaks: number[] };
	type Status = { recording: boolean; paused?: boolean; elapsed?: number; levels?: number[]; path?: string; error?: string };

	// last known recordings paint at once; a refresh follows (lib/swr)
	let items = $state<Track[]>([...(cached<Track[]>('recordings') ?? [])].sort((a, b) => b.modified - a.modified));
	let selected = $state<string | null>(null);
	// narrow window: the list becomes an overlay toggled from the toolbar (Voice Memos' sidebar button)
	let memos_width = $state(800);
	const narrow = $derived(memos_width < 520);
	let show_list = $state(true);
	let query = $state('');
	let status = $state<Status>({ recording: false });
	let error = $state('');
	let renaming = $state<string | null>(null);
	let rename_value = $state('');
	let list_el = $state<HTMLElement>();

	let band_el = $state<HTMLElement>();
	let ruler_el = $state<HTMLElement>();
	let overview_el = $state<HTMLElement>();
	let ws: WaveSurfer | null = null;
	let playing = $state(false);
	let position = $state(0);
	let duration = $state(0);

	const dark = $derived(preferences.theme.scheme === 'dark');
	const sel = $derived(items.find((x) => x.path === selected) ?? null);
	const shown = $derived(query.trim() ? items.filter((x) => x.title.toLowerCase().includes(query.trim().toLowerCase())) : items);
	const locale = $derived(locale_of(preferences.lang));
	const day_fmt = $derived(new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }));
	const full_fmt = $derived(new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }));

	const src = (p: string) => `/api/fs/get?path=${encodeURIComponent(p)}`;

	/** 00:11.90 — the big timer */
	function fmt_cs(s: number) {
		const cs = Math.floor(Math.max(0, s) * 100);
		const m = Math.floor(cs / 6000);
		const sec = Math.floor((cs % 6000) / 100);
		return `${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}.${String(cs % 100).padStart(2, '0')}`;
	}

	// ---- library ----

	async function load() {
		const list = [...((await revalidate('recordings', data.recordings)) as Track[])];
		if (!alive) return;
		items = list.sort((a, b) => b.modified - a.modified);
		if (!selected && items[0]) select(items[0]);
	}

	function select(it: Track) {
		show_list = false;
		if (selected === it.path) return;
		selected = it.path;
		position = 0;
		duration = it.duration ?? 0;
		load_wave(it.path);
	}

	// long recordings: the Zero computes the peaks, the audio streams
	async function load_wave(path: string) {
		if (!ws) return;
		try {
			// a finished recording never changes: peaks seen before (or preloaded) draw at once
			const key = `peaks:${path}`;
			const r = cached<Peaks>(key) ?? ((await revalidate(key, data.peaks(path))) as Peaks);
			if (!alive || selected !== path || !ws) return;
			await ws.load(src(path), [r.peaks], r.duration);
		} catch {
			if (alive && selected === path) ws?.load(src(path)).catch(() => {});
		}
	}

	// ---- waveform (wavesurfer.js) ----

	function colors() {
		return dark
			? { wave: 'hsla(0,0%,100%,0.42)', progress: 'hsla(0,0%,100%,0.92)', ruler: 'hsla(0,0%,100%,0.5)', overlay: 'hsla(0,0%,100%,0.14)' }
			: { wave: 'hsla(0,0%,0%,0.32)', progress: 'hsla(0,0%,0%,0.86)', ruler: 'hsla(0,0%,0%,0.45)', overlay: 'hsla(0,0%,0%,0.08)' };
	}

	function build() {
		if (!band_el || !ruler_el || !overview_el) return;
		const was = ws?.getCurrentTime() ?? 0;
		ws?.destroy();
		ruler_el.replaceChildren();
		overview_el.replaceChildren();
		const c = colors();
		const accent = getComputedStyle(band_el).getPropertyValue('--system-color-primary').trim() || '#0a84ff';
		ws = WaveSurfer.create({
			container: band_el,
			height: 'auto',
			waveColor: c.wave,
			progressColor: c.progress,
			cursorColor: accent,
			cursorWidth: 2,
			barWidth: 2,
			barGap: 2,
			barRadius: 1,
			barMinHeight: 1,
			normalize: true,
			minPxPerSec: 60,
			fillParent: true,
			autoScroll: true,
			autoCenter: true,
			hideScrollbar: true,
			dragToSeek: true,
			plugins: [
				TimelinePlugin.create({
					container: ruler_el,
					height: 18,
					timeInterval: 0.5,
					primaryLabelInterval: 1,
					secondaryLabelOpacity: 0,
					formatTimeCallback: (s) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`,
					style: { color: c.ruler, fontSize: '10px', fontVariantNumeric: 'tabular-nums' },
				}),
				MinimapPlugin.create({
					container: overview_el,
					height: 30,
					waveColor: c.wave,
					progressColor: c.progress,
					cursorColor: accent,
					cursorWidth: 2,
					barWidth: 1,
					barGap: 1,
					normalize: true,
					overlayColor: c.overlay,
					dragToSeek: true,
				}),
			],
		});
		ws.on('play', () => (playing = true));
		ws.on('pause', () => (playing = false));
		ws.on('finish', () => (playing = false));
		ws.on('timeupdate', (s) => (position = s));
		ws.on('ready', (d) => {
			duration = d;
			if (was && was < d) ws?.setTime(was);
			const p = selected;
			items = items.map((x) => (x.path === p ? { ...x, duration: d } : x));
		});
		if (selected) load_wave(selected);
	}

	// rebuild with the right colours when the appearance changes
	let built_dark: boolean | null = null;
	$effect(() => {
		const d = dark;
		if (built_dark === null || built_dark === d || !band_el) return;
		built_dark = d;
		build();
	});

	function toggle() {
		if (!sel || !ws) return;
		ws.playPause();
	}

	function skip(d: number) {
		if (!ws) return;
		ws.setTime(Math.max(0, Math.min(duration, ws.getCurrentTime() + d)));
	}

	// ---- recording ----

	let poll: ReturnType<typeof setInterval> | undefined;
	// false once the window has closed: replies that arrive later must not load or poll
	let alive = true;
	/** (Re)start polling the recording status; never after the window has closed (callers await first). */
	function start_poll() {
		clearInterval(poll);
		if (alive) poll = setInterval(refresh_status, 120);
	}
	let status_at = 0;
	async function refresh_status() {
		try {
			status = await api<Status>('/api/rec/status');
			status_at = performance.now();
			if (status.error) {
				error = status.error === 'microphone unavailable' ? t('rec.mic_busy') : status.error;
				clearInterval(poll);
			}
		} catch {
			/* keep last state */
		}
	}

	async function start() {
		error = '';
		try {
			ws?.pause();
			await post('/api/rec/start', {});
			await refresh_status();
			start_poll();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function set_paused(paused: boolean) {
		try {
			await post('/api/rec/pause', { paused });
			await refresh_status();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function stop() {
		clearInterval(poll);
		try {
			const r = await post<{ path: string }>('/api/rec/stop', {});
			status = { recording: false };
			selected = null;
			await load();
			const it = items.find((x) => x.path === r.path);
			if (it) select(it);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	// live waveform while recording: device levels arrive 20 per second; the
	// red playhead stays in the middle and the waveform moves left under it
	const RATE = 20;
	const STEP = 5; // px per level -> 100 px per second
	let live_el = $state<HTMLCanvasElement>();
	let raf = 0;

	function draw_live() {
		const c = live_el;
		if (!c) return;
		const dpr = devicePixelRatio || 1;
		const w = c.clientWidth;
		const h = c.clientHeight;
		if (c.width !== Math.round(w * dpr)) c.width = Math.round(w * dpr);
		if (c.height !== Math.round(h * dpr)) c.height = Math.round(h * dpr);
		const ctx = c.getContext('2d')!;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, w, h);

		const ruler = 20;
		const band = h - ruler;
		const head = Math.round(w / 2);
		// smooth between status polls
		const drift = status.paused ? 0 : Math.min(0.25, (performance.now() - status_at) / 1000);
		const now = (status.elapsed ?? 0) + drift;
		const shift = drift * RATE * STEP;
		const ink = dark ? 'hsla(0,0%,100%,' : 'hsla(0,0%,0%,';

		ctx.fillStyle = ink + '0.05)';
		ctx.fillRect(0, 0, w, band);

		const levels = status.levels ?? [];
		ctx.fillStyle = '#ff3b30';
		for (let i = levels.length - 1, k = 0; i >= 0; i--, k++) {
			const x = head - k * STEP - 3 - shift;
			if (x < -STEP) break;
			const bh = Math.max(2, levels[i] * (band - 24));
			ctx.beginPath();
			ctx.roundRect(x, (band - bh) / 2, 2, bh, 1);
			ctx.fill();
		}

		// ruler: past on the left, the future greyed on the right
		ctx.font = '10px -apple-system, system-ui, sans-serif';
		ctx.textBaseline = 'middle';
		const px_s = RATE * STEP;
		const first = Math.floor(now - head / px_s) - 1;
		const last = Math.ceil(now + (w - head) / px_s) + 1;
		for (let q = first * 2; q <= last * 2; q++) {
			const s = q / 2;
			if (s < 0) continue;
			const x = head + (s - now) * px_s;
			ctx.fillStyle = ink + (s <= now ? '0.45)' : '0.2)');
			ctx.fillRect(Math.round(x), band + 2, 1, q % 2 ? 3 : 6);
			if (q % 2 === 0) ctx.fillText(`${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`, x + 3, band + 12);
		}

		// playhead with a dot at each end
		ctx.fillStyle = '#ff3b30';
		ctx.fillRect(head - 1, 4, 2, band - 8);
		for (const y of [4, band - 4]) {
			ctx.beginPath();
			ctx.arc(head, y, 3.5, 0, Math.PI * 2);
			ctx.fill();
		}
	}

	$effect(() => {
		if (!status.recording || !live_el) return;
		const loop = () => {
			draw_live();
			raf = requestAnimationFrame(loop);
		};
		raf = requestAnimationFrame(loop);
		return () => cancelAnimationFrame(raf);
	});

	// ---- list editing ----

	async function begin_rename(it: Track) {
		renaming = it.path;
		rename_value = it.title;
		await tick();
		list_el?.querySelector<HTMLInputElement>('input.rename')?.select();
	}

	async function commit_rename() {
		const it = items.find((x) => x.path === renaming);
		renaming = null;
		const name = rename_value.trim();
		if (!it || !name || name === it.title) return;
		const to = it.path.slice(0, it.path.lastIndexOf('/') + 1) + name + '.' + it.ext;
		try {
			await post('/api/fs/op', { op: 'rename', path: it.path, to });
			if (selected === it.path) selected = to;
			await load();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function trash(it: Track) {
		const i = items.findIndex((x) => x.path === it.path);
		if (selected === it.path) {
			ws?.empty();
			selected = null;
			position = duration = 0;
		}
		await post('/api/fs/op', { op: 'trash', path: it.path });
		window.dispatchEvent(new CustomEvent('zerodesk-trash-changed'));
		await load();
		const next = items[Math.min(i, items.length - 1)];
		if (next && !selected) select(next);
	}

	function on_key(e: KeyboardEvent) {
		if ((e.target as HTMLElement).closest('input')) return;
		if (status.recording) {
			if (e.key === ' ') {
				e.preventDefault();
				set_paused(!status.paused);
			}
			return;
		}
		const i = shown.findIndex((x) => x.path === selected);
		if (e.key === ' ') {
			e.preventDefault();
			toggle();
		} else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			const n = shown[Math.max(0, Math.min(shown.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))];
			if (n) select(n);
		} else if (e.key === 'Enter' && sel) {
			begin_rename(sel);
		} else if ((e.key === 'Backspace' || e.key === 'Delete') && e.metaKey && sel) {
			trash(sel);
		} else if (e.key === 'ArrowRight' && sel) {
			skip(15);
		} else if (e.key === 'ArrowLeft' && sel) {
			skip(-15);
		}
	}

	onMount(async () => {
		built_dark = dark;
		build();
		if (!selected && items[0]) select(items[0]);
		await refresh_status();
		if (!alive) return;
		if (status.recording) start_poll();
		await load();
	});
	onDestroy(() => {
		alive = false;
		clearInterval(poll);
		cancelAnimationFrame(raf);
		ws?.destroy();
	});

	// pick up recordings made on the Zero itself when the window comes back to front
	$effect(() => {
		if (apps.active === 'recorder' && !status.recording) load();
	});

</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="memos" class:narrow class:show-list={show_list} tabindex="0" role="application" aria-label={t('app.recorder')} onkeydown={on_key} bind:clientWidth={memos_width}>
	<aside class="list-col">
		<header class="list-head app-window-drag-handle">
			<span class="head-title">{t('rec.all')}</span>
		</header>
		<div class="list" bind:this={list_el}>
			{#each shown as it (it.path)}
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<div class="row" class:selected={selected === it.path} role="option" aria-selected={selected === it.path} tabindex="-1" onclick={() => select(it)}>
					{#if renaming === it.path}
						<input
							class="rename"
							bind:value={rename_value}
							onblur={commit_rename}
							onkeydown={(e) => {
								if (e.key === 'Enter') (e.currentTarget as HTMLInputElement).blur();
								if (e.key === 'Escape') renaming = null;
							}}
						/>
					{:else}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<span class="title" ondblclick={() => begin_rename(it)}>{it.title}</span>
					{/if}
					<span class="meta">
						<span>{day_fmt.format(it.modified)}</span>
						<span class="tnum">{it.duration ? fmt_time(it.duration) : ''}</span>
					</span>
				</div>
			{/each}
			{#if !shown.length}
				<p class="empty">{t('rec.empty')}</p>
			{/if}
		</div>
		<div class="rec-fade">
			<button class="rec" aria-label={t('rec.record')} onclick={start}></button>
		</div>
	</aside>

	<section class="detail">
		<header class="toolbar app-window-drag-handle">
			{#if narrow}
				<button class="glass-circle" aria-label={t('rec.all')} onclick={() => (show_list = true)}><Glyph name="sidebar_left" size={17} /></button>
			{/if}
			<div class="capsule">
				<button class="tb" aria-label={t('rec.delete')} disabled={!sel} onclick={() => sel && trash(sel)}>
					<Glyph name="trash" size={16} />
				</button>
			</div>
			<label class="search">
				<Glyph name="search" size={13} />
				<input bind:value={query} placeholder={t('rec.search')} />
			</label>
		</header>

		<div class="memo" class:hidden={!sel}>
			<div class="memo-head">
				<p class="memo-title">{sel?.title ?? ''}</p>
				<p class="memo-sub tnum">{sel ? `${full_fmt.format(sel.modified)} · ${fmt_time(duration || sel.duration || 0)}` : ''}</p>
			</div>
			<div class="band" bind:this={band_el}></div>
			<div class="ruler" bind:this={ruler_el}></div>
			<div class="overview" bind:this={overview_el}></div>
			<p class="timer tnum">{fmt_cs(position)}</p>
			<div class="transport">
				<button class="tc" aria-label="-15s" onclick={() => skip(-15)}>
					<Glyph name="gobackward_15" size={22} />
				</button>
				<button class="tc" aria-label={playing ? 'Pause' : 'Play'} onclick={toggle}>
					{#if playing}
						<Glyph name="pause_fill" size={22} />
					{:else}
						<Glyph name="play_fill" size={22} />
					{/if}
				</button>
				<button class="tc" aria-label="+15s" onclick={() => skip(15)}>
					<Glyph name="goforward_15" size={22} />
				</button>
			</div>
		</div>
		{#if !sel}
			<div class="none">
				<Glyph name="waveform" size={88} />
				<p>{t('rec.none')}</p>
			</div>
		{/if}
	</section>

	{#if status.recording}
		<div class="rec-view">
			<header class="rec-head app-window-drag-handle">
				<p class="memo-title">{t('rec.new')}</p>
				<p class="memo-sub">{t('rec.mic')}</p>
			</header>
			<canvas class="live" bind:this={live_el} aria-hidden="true"></canvas>
			<p class="timer tnum">{fmt_cs(status.elapsed ?? 0)}</p>
			<footer class="rec-foot">
				<button class="capsule pause" aria-label={status.paused ? t('rec.resume') : t('rec.pause')} onclick={() => set_paused(!status.paused)}>
					{#if status.paused}
						<span class="resume">{t('rec.resume')}</span>
					{:else}
						<Glyph name="pause_fill" size={17} />
					{/if}
				</button>
				<button class="done" onclick={stop}>{t('rec.done')}</button>
			</footer>
		</div>
	{/if}

	{#if error}<p class="error">{error}</p>{/if}
</div>

<style>
	.memos {
		position: relative;
		display: grid;
		grid-template-columns: 300px minmax(0, 1fr);
		height: 100%;
		min-height: 0;
		outline: none;
		background: var(--zd-window-bg);
	}

	/* ---- list ---- */

	.list-col {
		position: relative;
		display: flex;
		flex-direction: column;
		min-height: 0;
		box-shadow: inset -0.5px 0 0 var(--zd-hairline-strong);
	}

	.list-head {
		flex: none;
		height: var(--zd-titlebar);
		display: flex;
		align-items: center;
		padding-left: 92px;
	}

	.head-title {
		font-size: 15px;
		font-weight: 600;
	}

	.list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		padding: 2px 10px 90px;
	}

	.row {
		position: relative;
		height: 56px;
		display: flex;
		flex-direction: column;
		justify-content: center;
		gap: 3px;
		padding: 0 10px;
		border-radius: 10px;
		cursor: default;

		/* hairline separators, hidden next to the selection */
		& + .row::before {
			content: '';
			position: absolute;
			top: 0;
			left: 10px;
			right: 10px;
			height: 0.5px;
			background: var(--zd-hairline-strong);
		}

		&.selected::before,
		&.selected + .row::before {
			display: none;
		}

		&.selected {
			background: var(--zd-selected);
		}
	}

	.title {
		font-size: 13px;
		font-weight: 600;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.rename {
		font: inherit;
		font-size: 13px;
		font-weight: 600;
		padding: 1px 4px;
		margin: -2px -5px;
		border: 0;
		border-radius: 4px;
		outline: 3px solid color-mix(in srgb, var(--system-color-primary) 45%, transparent);
		background: var(--zd-control-bg);
		color: var(--zd-text);
	}

	.meta {
		display: flex;
		justify-content: space-between;
		gap: 12px;
		font-size: 11px;
		color: var(--zd-text-secondary);
	}

	.empty {
		margin: 40px 0;
		text-align: center;
		font-size: 13px;
		color: var(--zd-text-tertiary);
	}

	.rec-fade {
		position: absolute;
		left: 0;
		right: 0.5px;
		bottom: 0;
		height: 96px;
		display: grid;
		place-items: center;
		padding-top: 16px;
		background: linear-gradient(to bottom, transparent, var(--zd-window-bg) 45%);
		pointer-events: none;
	}

	.rec {
		width: 40px;
		height: 40px;
		border-radius: 50%;
		background: var(--zd-red);
		box-shadow: 0 1px 4px hsla(0, 90%, 40%, 0.3);
		pointer-events: auto;
		transition: transform 160ms var(--zd-spring);

		&:active {
			transform: scale(0.9);
		}
	}

	/* ---- detail ---- */

	.detail {
		position: relative;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}

	.toolbar {
		flex: none;
		height: var(--zd-titlebar);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 0 12px;
	}

	.capsule {
		display: flex;
		align-items: center;
		height: 36px;
		padding: 0 4px;
		border-radius: 18px;
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 1px 4px hsla(0, 0%, 0%, 0.08);
		backdrop-filter: blur(20px) saturate(1.6);
	}

	.glass-circle {
		flex: none;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border-radius: 50%;
		color: var(--zd-text-secondary);
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 1px 4px hsla(0, 0%, 0%, 0.08);
		backdrop-filter: blur(20px) saturate(1.6);
	}

	.tb {
		width: 32px;
		height: 28px;
		display: grid;
		place-items: center;
		border-radius: 14px;
		color: var(--zd-text-secondary);

		&:hover:not(:disabled) {
			background: var(--zd-hover);
			color: var(--zd-text);
		}

		&:disabled {
			opacity: 0.35;
		}
	}

	.search {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 200px;
		min-width: 0;
		height: 36px;
		padding: 0 12px;
		border-radius: 18px;
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 1px 4px hsla(0, 0%, 0%, 0.08);
		color: var(--zd-text-tertiary);

		input {
			flex: 1;
			min-width: 0;
			border: 0;
			outline: 0;
			background: none;
			font: inherit;
			font-size: 13px;
			color: var(--zd-text);
		}
	}

	.memo {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		padding-bottom: 20px;

		&.hidden {
			visibility: hidden;
		}
	}

	.memo-head {
		flex: none;
		text-align: center;
		padding: 2px 24px 14px;
		max-width: 100%;
	}

	.memo-title {
		font-size: 13px;
		font-weight: 600;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.memo-sub {
		margin-top: 2px;
		font-size: 11px;
		color: var(--zd-text-secondary);
	}

	.band {
		flex: 1;
		width: 100%;
		min-height: 120px;
		max-height: 260px;
		background: hsla(0, 0%, 50%, 0.08);

		/* playhead: a dot at each end, like Voice Memos */
		& :global(div::part(cursor)) {
			overflow: visible;
		}

		& :global(div::part(cursor)::before),
		& :global(div::part(cursor)::after) {
			content: '';
			position: absolute;
			left: 50%;
			width: 7px;
			height: 7px;
			margin-left: -3.5px;
			border-radius: 50%;
			background: inherit;
		}

		& :global(div::part(cursor)::before) {
			top: 0;
		}

		& :global(div::part(cursor)::after) {
			bottom: 0;
		}
	}

	.ruler {
		flex: none;
		width: 100%;
		height: 18px;
		overflow: hidden;
	}

	.overview {
		flex: none;
		width: calc(100% - 48px);
		margin-top: 14px;
		border-radius: 6px;
		overflow: hidden;
	}

	.timer {
		flex: none;
		margin-top: 16px;
		font-size: 28px;
		font-weight: 700;
		letter-spacing: -0.3px;
	}

	.tnum {
		font-variant-numeric: tabular-nums;
	}

	.transport {
		flex: none;
		display: flex;
		align-items: center;
		gap: 4px;
		margin-top: 14px;
		height: 48px;
		padding: 0 8px;
		border-radius: 24px;
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 2px 10px hsla(0, 0%, 0%, 0.1);
		backdrop-filter: blur(20px) saturate(1.6);
	}

	.tc {
		width: 44px;
		height: 36px;
		display: grid;
		place-items: center;
		border-radius: 18px;
		color: var(--zd-text);
		transition: transform 140ms var(--zd-spring);

		&:hover {
			background: var(--zd-hover);
		}

		&:active {
			transform: scale(0.9);
		}
	}

	.none {
		position: absolute;
		inset: var(--zd-titlebar) 0 0 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 14px;
		color: var(--zd-text-tertiary);

		p {
			font-size: 22px;
			font-weight: 700;
		}
	}

	/* ---- recording: the whole window ---- */

	.rec-view {
		position: absolute;
		inset: 0;
		z-index: 2;
		display: flex;
		flex-direction: column;
		align-items: center;
		background: var(--zd-window-bg);
		animation: rec-in 220ms ease-out;
	}

	@keyframes rec-in {
		from {
			opacity: 0;
		}
	}

	.rec-head {
		flex: none;
		width: 100%;
		height: calc(var(--zd-titlebar) + 18px);
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: flex-end;
		padding-bottom: 12px;
	}

	.live {
		flex: 1;
		width: 100%;
		min-height: 140px;
		max-height: 300px;
	}

	.rec-view .timer {
		margin-top: 18px;
	}

	.rec-foot {
		flex: 1;
		width: 100%;
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		padding: 0 16px 16px;
	}

	.pause {
		min-width: 52px;
		justify-content: center;
		padding: 0 16px;
		color: var(--zd-red);
	}

	.resume {
		font-size: 12px;
		font-weight: 600;
		letter-spacing: 0.4px;
		text-transform: uppercase;
	}

	.done {
		height: 36px;
		padding: 0 18px;
		border-radius: 18px;
		background: hsla(0, 0%, 50%, 0.14);
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
		font-size: 13px;
		font-weight: 500;
		color: var(--zd-text);
	}

	@container (max-width: 620px) {
		.memos {
			grid-template-columns: 170px minmax(0, 1fr);
		}

		.list-head {
			padding-left: 14px;
			align-items: flex-end;
			padding-bottom: 6px;
		}

		.search {
			width: 120px;
		}
	}

	.memos.narrow {
		grid-template-columns: minmax(0, 1fr);

		.list-col {
			display: none;
			position: absolute;
			inset: 0;
			z-index: 6;
			background: var(--zd-window-bg);
		}

		&.show-list .list-col {
			display: flex;
		}

		.list-head {
			padding: 0 0 0 92px;
			align-items: center;
		}

		.detail .toolbar {
			padding-left: 92px;
			justify-content: flex-start;
		}

		.search {
			margin-left: auto;
		}
	}

	.error {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 72px;
		z-index: 3;
		text-align: center;
		font-size: 12px;
		color: var(--zd-red);
	}
</style>
