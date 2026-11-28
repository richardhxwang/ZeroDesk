<script lang="ts">
	import 'framework7-icons/css/framework7-icons.css';
	import { onDestroy, onMount } from 'svelte';
	import { api, post } from '🍎/lib/api.ts';
	import { cached, revalidate } from '🍎/lib/swr.svelte.ts';
	import ZagentChat from './ZagentChat.svelte';
	import { preferences } from '🍎/state/preferences.svelte.ts';
	import { data, zagent_lang } from '🍎/state/app_preload.ts';

	// Zagent (the CardputerZero agent client) settings, laid out like System
	// Settings. Groups and rows come from the device tool zagent-settings via
	// the agent (/api/zagent/*); this window only renders and edits them.

	type Row = {
		key: string;
		type: 'toggle' | 'choice' | 'text' | 'secret' | 'action' | 'info';
		label: string;
		value: string;
		options: string[];
		dynamic_options: boolean;
	};
	type Section = { id: string; title: string; rows: Row[] };

	const LOOK: Record<string, { icon: string; tint: string }> = {
		model: { icon: 'cube_box_fill', tint: '#ff9500' },
		voice: { icon: 'mic_fill', tint: '#ff2d55' },
		net: { icon: 'globe', tint: '#34c759' },
		perm: { icon: 'checkmark_shield_fill', tint: '#8e8e93' },
	};

	let sections = $state<Section[] | null>(null);
	// The window asks Zagent for ZeroDesk's language (Zagent has English and
	// Chinese). The rows come back in the language Zagent actually used (an
	// older Zagent ignores the request), and the window follows that, so row
	// labels and buttons never mix.
	const wanted = $derived(zagent_lang(preferences.lang));
	let device_lang = $state('');
	const lang = $derived(device_lang || wanted);
	const L = (en: string, zh: string) => (lang === 'zh' ? zh : en);
	let pane = $state('chat');
	let query = $state('');
	let error = $state('');
	let busy = $state<string | null>(null);
	let row_error = $state<Record<string, string>>({});
	let drafts = $state<Record<string, string>>({});
	let dynamic = $state<Record<string, string[]>>({});

	const shown = $derived(
		(sections ?? []).filter((s) => !query.trim() || s.title.toLowerCase().includes(query.trim().toLowerCase()) ||
			s.rows.some((r) => r.label.toLowerCase().includes(query.trim().toLowerCase()))),
	);
	const current = $derived(sections?.find((s) => s.id === pane));
	const chatting = $derived(pane === 'chat');

	type Settings = { lang: string; sections: Section[] };
	// Same key as the idle / hover prefetch (state/app_preload.ts), so the
	// window opens with the last sections and refreshes them behind.
	const SWR_KEY = 'zagent';

	function show(data: Settings) {
		device_lang = data.lang;
		// a copy: rows are edited in place, the cache keeps what the device said
		sections = structuredClone(data.sections).filter((s) => s.id !== 'web');
	}

	const last = cached<Settings>(SWR_KEY);
	if (last) show(last);

	// ZeroDesk's language changed while the window is open: ask again
	let asked = '';
	$effect(() => {
		const l = wanted;
		if (asked && asked !== l) void load();
		asked = l;
	});

	async function load() {
		try {
			show((await revalidate(SWR_KEY, data.zagent)) as Settings);
			error = '';
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function load_options(row: Row) {
		if (!row.dynamic_options || dynamic[row.key]) return;
		try {
			dynamic[row.key] = [...new Set(await api<string[]>(`/api/zagent/options?key=${encodeURIComponent(row.key)}&lang=${wanted}`))];
		} catch (e) {
			row_error[row.key] = (e as Error).message;
		}
	}

	$effect(() => {
		for (const row of current?.rows ?? []) if (row.dynamic_options) load_options(row);
	});

	async function set(row: Row, value: string) {
		busy = row.key;
		row_error[row.key] = '';
		try {
			const r = await post<{ value: string }>(`/api/zagent/set?lang=${wanted}`, { key: row.key, value });
			row.value = r.value;
			delete drafts[row.key];
		} catch (e) {
			row_error[row.key] = (e as Error).message;
		}
		busy = null;
	}

	// narrow window: the sidebar becomes an overlay opened from the title bar
	let root_width = $state(800);
	const narrow = $derived(root_width < 520);
	let show_list = $state(false);

	let timer: ReturnType<typeof setInterval>;
	onMount(() => {
		load();
		timer = setInterval(() => !busy && load(), 30000);
	});
	onDestroy(() => clearInterval(timer));
</script>

{#snippet tile(icon: string, tint: string, size = 20)}
	<span class="tile" style:--tint={tint} style:--s="{size}px"><i class="f7-icons">{icon}</i></span>
{/snippet}

{#snippet toggle(on: boolean, label: string, onchange: (v: boolean) => void, disabled = false)}
	<button class="switch" class:on role="switch" aria-checked={on} aria-label={label} {disabled} onclick={() => onchange(!on)}><span></span></button>
{/snippet}

<div class="settings" class:narrow class:show-list={show_list} bind:clientWidth={root_width}>
	<aside class="sidebar">
		<div class="lights-band app-window-drag-handle"></div>
		<label class="search">
			<i class="f7-icons">search</i>
			<input bind:value={query} placeholder={L('Search', '搜索')} />
		</label>
		<nav class="panes">
			<button class="item" class:active={chatting} onclick={() => ((pane = 'chat'), (show_list = false))}>
				{@render tile('chat_bubble_2_fill', '#636366')}
				<span>{L('Chat', '对话')}</span>
			</button>
			{#if !sections && !error}
				{#each Array(4) as _, i (i)}
					<div class="item placeholder" aria-hidden="true"><span class="ph-tile"></span><span class="ph-bar" style:width="{[64, 52, 58, 46][i]}%"></span></div>
				{/each}
			{/if}
			{#if !sections && error}
				<button class="item" class:active={pane === 'settings'} onclick={() => ((pane = 'settings'), (show_list = false))}>
					{@render tile('gear_alt_fill', '#8e8e93')}
					<span>{L('Settings', '设置')}</span>
				</button>
			{/if}
			{#each shown as s (s.id)}
				<button class="item" class:active={pane === s.id} onclick={() => ((pane = s.id), (show_list = false))}>
					{@render tile(LOOK[s.id]?.icon ?? 'gear_alt_fill', LOOK[s.id]?.tint ?? '#8e8e93')}
					<span>{s.title}</span>
				</button>
			{/each}
		</nav>
	</aside>

	<section class="detail">
		<header class="titlebar app-window-drag-handle">
			{#if narrow}
				<button class="list-btn" aria-label={L('Sections', '分类')} onclick={() => (show_list = true)}><i class="f7-icons">sidebar_left</i></button>
			{/if}
			<h1>{chatting ? L('Chat', '对话') : (current?.title ?? L('Settings', '设置'))}</h1>
		</header>

		<div class="content" class:chat-pane={chatting}>
			{#if chatting}
				<ZagentChat {lang} />
			{:else if !sections}
				<div class="unavailable">
					<p>{error ? L('Settings unavailable', '设置不可用') : '…'}</p>
					{#if error}<p class="why">{error}</p><button class="btn" onclick={load}>{L('Retry', '重试')}</button>{/if}
				</div>
			{:else if current}
				<div class="card">
					{#each current.rows as row (row.key)}
						{@const list = row.dynamic_options ? (dynamic[row.key] ?? []) : [...new Set(row.options)]}
						<div class="row">
							<span class="grow">{row.label}</span>
							{#if row.type === 'toggle'}
								{@render toggle(row.value === 'on', row.label, (v) => set(row, v ? 'on' : 'off'), busy === row.key)}
							{:else if row.type === 'choice' && row.dynamic_options}
								<input
									class="field wide"
									list="zagent-{row.key}"
									value={drafts[row.key] ?? row.value}
									oninput={(e) => (drafts[row.key] = (e.currentTarget as HTMLInputElement).value)}
								/>
								<datalist id="zagent-{row.key}">
									{#each list as option (option)}<option value={option}></option>{/each}
								</datalist>
								<button
									class="btn"
									disabled={busy === row.key || !drafts[row.key] || drafts[row.key] === row.value}
									onclick={() => set(row, drafts[row.key])}>{L('Apply', '应用')}</button>
							{:else if row.type === 'choice'}
								<select class="field select" value={row.value} disabled={busy === row.key} onchange={(e) => set(row, (e.currentTarget as HTMLSelectElement).value)}>
									{#if !list.includes(row.value)}<option value={row.value}>{row.value}</option>{/if}
									{#each list as option (option)}<option value={option}>{option}</option>{/each}
								</select>
							{:else if row.type === 'text' || row.type === 'secret'}
								<input
									class="field wide"
									type={row.type === 'secret' ? 'password' : 'text'}
									placeholder={row.type === 'secret' ? row.value : ''}
									value={drafts[row.key] ?? (row.type === 'secret' ? '' : row.value)}
									oninput={(e) => (drafts[row.key] = (e.currentTarget as HTMLInputElement).value)}
								/>
								<button
									class="btn"
									disabled={busy === row.key || !drafts[row.key] || drafts[row.key] === row.value}
									onclick={() => set(row, drafts[row.key])}>{L('Apply', '应用')}</button>
							{:else if row.type === 'action'}
								{#if row.value && row.value !== '›'}<span class="value">{row.value}</span>{/if}
								<button class="btn" disabled={busy === row.key} onclick={() => set(row, 'run')}>{busy === row.key ? '…' : L('Run', '执行')}</button>
							{:else}
								<span class="value">{row.value}</span>
							{/if}
						</div>
						{#if row_error[row.key]}<p class="foot err">{row_error[row.key]}</p>{/if}
					{/each}
				</div>
			{/if}
		</div>
	</section>
</div>

<style>
	.settings {
		position: relative;
		display: grid;
		grid-template-columns: 236px minmax(0, 1fr);
		height: 100%;
		min-height: 0;
	}

	.f7-icons {
		font-size: inherit;
		line-height: 1;
		font-style: normal;
	}

	/* ---- sidebar ---- */

	.sidebar {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: var(--zd-sidebar-inset);
		margin-right: 0;
		padding: 0 10px 10px;
		border-radius: var(--zd-radius-sidebar);
		background: var(--zd-sidebar-bg);
		backdrop-filter: blur(30px) saturate(1.4);
		box-shadow: 0 0 0 0.5px var(--zd-hairline);
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
	}

	.lights-band {
		height: calc(var(--zd-titlebar) - var(--zd-sidebar-inset));
		flex: none;
	}

	.search {
		flex: none;
		display: flex;
		align-items: center;
		gap: 6px;
		height: 28px;
		padding: 0 10px;
		margin-bottom: 8px;
		border-radius: 14px;
		background: var(--zd-hover);
		color: var(--zd-text-tertiary);
		font-size: 13px;

		input {
			flex: 1;
			min-width: 0;
			border: 0;
			outline: 0;
			background: none;
			font: inherit;
			color: var(--zd-text);
		}
	}

	.device {
		flex: none;
		justify-content: flex-start;
		gap: 10px;
		padding: 6px 8px;
		margin-bottom: 6px;
		border-radius: 10px;
		text-align: left;

		&.active {
			background: var(--zd-selected);
		}
	}

	.device-text {
		display: flex;
		flex-direction: column;
		min-width: 0;

		b {
			font-size: 13px;
			font-weight: 600;
		}

		small {
			font-size: 11px;
			color: var(--zd-text-secondary);
		}
	}

	.panes {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.gap {
		height: 12px;
	}

	.item.placeholder {
		pointer-events: none;

		.ph-tile,
		.ph-bar {
			background: var(--zd-hover);
			animation: ph-pulse 1.4s ease-in-out infinite;
		}

		.ph-tile {
			flex: none;
			width: 20px;
			height: 20px;
			border-radius: 5px;
		}

		.ph-bar {
			height: 9px;
			border-radius: 5px;
		}
	}

	@keyframes ph-pulse {
		50% {
			opacity: 0.45;
		}
	}

	.item {
		justify-content: flex-start;
		gap: 9px;
		height: 30px;
		padding: 0 8px;
		border-radius: 8px;
		font-size: 13px;

		&.active {
			background: var(--zd-selected);
			font-weight: 600;
		}
	}

	.tile {
		flex: none;
		width: var(--s);
		height: var(--s);
		display: grid;
		place-items: center;
		border-radius: calc(var(--s) * 0.24);
		background: var(--tint);
		box-shadow: inset 0 0 0 0.5px hsla(0, 0%, 0%, 0.12);
		color: #fff;

		:global(body.dark) & {
			box-shadow: inset 0 0 0 0.5px hsla(0, 0%, 100%, 0.18);
		}
		font-size: calc(var(--s) * 0.6);
	}

	/* ---- detail ---- */

	.detail {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}

	.titlebar {
		flex: none;
		height: var(--zd-titlebar);
		display: flex;
		align-items: center;
		padding: 0 24px;

		h1 {
			font-size: 15px;
			font-weight: 600;
		}
	}

	.unavailable {
		margin: auto;
		text-align: center;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;

		p {
			margin: 0;
		}

		.why {
			max-width: 360px;
			font-size: 12px;
			color: var(--zd-text-tertiary);
		}
	}

	.content.chat-pane {
		padding: 0;
		overflow: hidden;
	}

	.content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		padding: 4px 24px 28px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.loading {
		margin: 40px auto;
		color: var(--zd-text-tertiary);
	}

	.hero {
		display: flex;
		align-items: center;
		gap: 14px;
		padding: 4px 4px 6px;

		.hero-text {
			flex: 1;
			display: flex;
			flex-direction: column;
			gap: 2px;
			min-width: 0;

			b {
				font-size: 15px;
				font-weight: 600;
			}

			.big {
				font-size: 20px;
				font-weight: 700;
			}

			small {
				font-size: 12px;
				color: var(--zd-text-secondary);
			}
		}
	}

	h2 {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 6px 4px -4px;
		font-size: 13px;
		font-weight: 600;
	}

	.card {
		border-radius: 12px;
		background: var(--zd-hover);
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline);
		padding: 0 14px;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 12px;
		min-height: 44px;
		padding: 6px 0;
		font-size: 13px;

		& + .row {
			box-shadow: 0 -0.5px 0 var(--zd-hairline);
		}

		&.tall {
			flex-direction: column;
			align-items: stretch;
			gap: 8px;
			padding: 12px 0;
		}

		&.net {
			cursor: default;
		}
	}

	.grow {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 1px;

		small {
			font-size: 11px;
		}
	}

	.name {
		font-weight: 500;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.value,
	.muted {
		color: var(--zd-text-secondary);
		font-variant-numeric: tabular-nums;
	}

	.ok {
		color: var(--zd-green);
	}

	.lock {
		font-size: 12px;
		color: var(--zd-text-tertiary);
	}

	.foot,
	.empty-note {
		margin: -4px 4px 0;
		font-size: 11px;
		color: var(--zd-text-tertiary);
	}

	.empty-note {
		margin-top: 30px;
		text-align: center;
		font-size: 13px;
	}

	.err {
		font-size: 12px;
		color: var(--zd-red);
		margin: 0 4px;
	}

	/* Wi-Fi strength: a dim full glyph with the strong part on top, clipped by bars */
	.wifi-bars {
		position: relative;
		width: 18px;
		height: 16px;
		flex: none;
		font-size: 16px;

		i {
			position: absolute;
			inset: 0;
			text-align: center;
		}

		.back {
			opacity: 0.25;
		}

		&.b1 .front {
			clip-path: inset(62% 0 0 0);
		}

		&.b2 .front {
			clip-path: inset(32% 0 0 0);
		}
	}

	/* ---- controls ---- */

	.btn {
		height: 24px;
		padding: 0 12px;
		border-radius: 7px;
		background: var(--zd-control-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 0.5px 1.5px hsla(0, 0%, 0%, 0.12);
		font-size: 13px;
		color: var(--zd-text);

		&:disabled {
			opacity: 0.5;
		}

		&.primary {
			background: var(--system-color-primary);
			color: var(--system-color-light, #fff);
			box-shadow: none;
		}

		&.danger {
			background: var(--zd-red);
			color: #fff;
			box-shadow: none;
		}
	}

	:global(body.dark) .settings .btn.primary {
		color: #1d1d1f;
	}

	.icon-btn {
		width: 22px;
		height: 22px;
		border-radius: 11px;
		color: var(--zd-text-secondary);
		font-size: 13px;

		&:hover {
			background: var(--zd-hover);
		}

		&.spin i {
			animation: spin 0.9s linear infinite;
		}
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.field {
		height: 26px;
		padding: 0 8px;
		border: 0;
		border-radius: 7px;
		outline: 0;
		background: var(--zd-control-bg);
		box-shadow: 0 0 0 0.5px var(--zd-hairline-strong);
		font: inherit;
		font-size: 13px;
		color: var(--zd-text);

		&:focus {
			box-shadow: 0 0 0 3px color-mix(in srgb, var(--system-color-primary) 30%, transparent);
		}
	}

	.select {
		appearance: none;
		padding-right: 26px;
		background-image: linear-gradient(45deg, transparent 50%, currentColor 50%), linear-gradient(135deg, currentColor 50%, transparent 50%);
		background-position:
			calc(100% - 13px) 11px,
			calc(100% - 9px) 11px;
		background-size:
			4px 4px,
			4px 4px;
		background-repeat: no-repeat;
	}

	.pw-row {
		gap: 8px;

		.pw {
			flex: 1;
			min-width: 0;
		}
	}

	.slider-row {
		display: flex;
		align-items: center;
		gap: 10px;

		.end {
			font-size: 14px;
			color: var(--zd-text-tertiary);
		}

		input {
			flex: 1;
			min-width: 0;
		}
	}

	.label {
		font-size: 13px;
	}

	input[type='range'] {
		appearance: none;
		height: 22px;
		background: transparent;

		&::-webkit-slider-runnable-track {
			height: 4px;
			border-radius: 2px;
			background: linear-gradient(to right, var(--system-color-primary) var(--v), var(--zd-hairline-strong) var(--v));
		}

		&::-webkit-slider-thumb {
			appearance: none;
			width: 20px;
			height: 20px;
			margin-top: -8px;
			border-radius: 50%;
			background: #fff;
			box-shadow:
				0 0 0 0.5px hsla(0, 0%, 0%, 0.18),
				0 1px 4px hsla(0, 0%, 0%, 0.25);
		}

		&::-moz-range-track {
			height: 4px;
			border-radius: 2px;
			background: linear-gradient(to right, var(--system-color-primary) var(--v), var(--zd-hairline-strong) var(--v));
		}

		&::-moz-range-thumb {
			width: 20px;
			height: 20px;
			border: 0;
			border-radius: 50%;
			background: #fff;
			box-shadow: 0 1px 4px hsla(0, 0%, 0%, 0.25);
		}

		&:disabled {
			opacity: 0.4;
		}
	}

	.switch {
		flex: none;
		width: 38px;
		height: 22px;
		padding: 2px;
		justify-content: flex-start;
		border-radius: 11px;
		background: var(--zd-hairline-strong);
		transition: background-color 200ms ease;

		span {
			width: 18px;
			height: 18px;
			border-radius: 9px;
			background: #fff;
			box-shadow: 0 1px 3px hsla(0, 0%, 0%, 0.25);
			transition: transform 380ms var(--zd-spring);
		}

		&.on {
			background: var(--zd-green);

			span {
				transform: translateX(16px);
			}
		}

		&:disabled {
			opacity: 0.6;
		}
	}

	/* battery cell drawn with CSS */
	.cell {
		position: relative;
		width: 54px;
		height: 26px;
		padding: 3px;
		border-radius: 7px;
		box-shadow: inset 0 0 0 1.5px var(--zd-text-tertiary);

		&::after {
			content: '';
			position: absolute;
			right: -5px;
			top: 9px;
			width: 3px;
			height: 8px;
			border-radius: 0 2px 2px 0;
			background: var(--zd-text-tertiary);
		}

		span {
			display: block;
			width: var(--level);
			height: 100%;
			border-radius: 4px;
			background: var(--zd-text);
		}

		&.charging span {
			background: var(--zd-green);
		}
	}

	.restart-bar {
		display: flex;
		justify-content: flex-end;
		align-items: center;
		gap: 8px;
		margin-top: 4px;
		font-size: 13px;
	}

	@container (max-width: 600px) {
		.settings {
			grid-template-columns: 180px minmax(0, 1fr);
		}

		.content {
			padding: 4px 14px 20px;
		}
	}

	.field.wide {
		width: min(320px, 46cqi);
	}

	.list-btn {
		flex: none;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		margin-right: 10px;
		border-radius: 50%;
		font-size: 17px;
		color: var(--zd-text-secondary);
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 1px 4px hsla(0, 0%, 0%, 0.08);
	}

	.settings.narrow {
		grid-template-columns: minmax(0, 1fr);

		.sidebar {
			display: none;
			position: absolute;
			inset: 0;
			z-index: 6;
			margin: var(--zd-sidebar-inset);
		}

		&.show-list .sidebar {
			display: flex;
		}

		.titlebar {
			padding-left: 92px;
		}

		.content {
			padding: 4px 14px 20px;
		}

		/* label on its own line, controls below */
		.row {
			flex-wrap: wrap;
		}

		.row > .grow {
			flex-basis: 100%;
		}

		.field.wide {
			flex: 1;
			width: auto;
			min-width: 0;
		}
	}
</style>
