<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, api_error } from '🍎/lib/api.ts';
	import { open_device_app } from '🍎/state/apps.svelte.ts';
	import { format_bytes, format_date, t } from '🍎/state/i18n.svelte.ts';
	import { preferences } from '🍎/state/preferences.svelte.ts';
	import { revalidate } from '🍎/lib/swr.svelte.ts';
	import { data } from '🍎/state/app_preload.ts';
	import { cancel_queued, enqueue, store_state, type StoreApp } from '🍎/state/appstore.svelte.ts';

	// The App Store, laid out like the Mac App Store: a sidebar (search,
	// Discover, Categories, Updates) and pages of apps with a Get button
	// that becomes a progress ring while the Zero downloads and installs
	// the package, then Open. The catalogue is the official CardputerZero
	// registry (agent/store.go).

	type Pane = 'discover' | 'categories' | 'updates';

	let pane = $state<Pane>('discover');
	let category = $state<string | null>(null);
	let section = $state<'new' | 'updated' | null>(null);
	let detail = $state<StoreApp | null>(null);
	let query = $state('');
	let error = $state('');
	let content_el = $state<HTMLElement>();

	// the Store's localized summary and description (its locale keys: zh-CN, ja)
	const store_locale = $derived(({ zh: 'zh-CN', ja: 'ja' } as Record<string, string>)[preferences.lang]);
	const loc = (a: StoreApp) => (store_locale ? a.locales?.[store_locale] : undefined);
	const title = (a: StoreApp) => loc(a)?.title || a.title;
	const summary = (a: StoreApp) => loc(a)?.summary || a.summary;
	const description = (a: StoreApp) => loc(a)?.description || a.description;

	const CATS: Record<string, [string, string, string]> = {
		'System Tools': ['系统工具', 'gear_alt', '#8e8e93'],
		'Radio & Comms': ['无线电与通信', 'antenna_radiowaves_left_right', '#ff9f0a'],
		Media: ['媒体', 'play_rectangle', '#ff375f'],
		'Hardware & IoT': ['硬件与物联网', 'cube_box', '#30b0c7'],
		Games: ['游戏', 'gamecontroller', '#bf5af2'],
		Lifestyle: ['生活', 'heart', '#ff453a'],
		Security: ['安全', 'lock_shield', '#32d74b'],
		Development: ['开发', 'hammer', '#64d2ff'],
		'Creative & Office': ['创作与办公', 'paintbrush', '#ffd60a'],
		AI: ['人工智能', 'sparkles', '#5e5ce6'],
		Education: ['教育', 'book', '#ac8e68'],
		Emulators: ['模拟器', 'square_stack_3d_up', '#ff6482'],
		Other: ['其他', 'ellipsis_circle', '#98989d'],
	};
	const CATS_JA: Record<string, string> = {
		'System Tools': 'システムツール',
		'Radio & Comms': '無線と通信',
		Media: 'メディア',
		'Hardware & IoT': 'ハードウェアと IoT',
		Games: 'ゲーム',
		Lifestyle: 'ライフスタイル',
		Security: 'セキュリティ',
		Development: '開発',
		'Creative & Office': 'クリエイティブとオフィス',
		AI: 'AI',
		Education: '教育',
		Emulators: 'エミュレータ',
		Other: 'その他',
	};
	const cat_name = (c: string) =>
		preferences.lang === 'zh' ? (CATS[c]?.[0] ?? c) : preferences.lang === 'ja' ? (CATS_JA[c] ?? c) : c;
	const PERMS: Record<string, string> = {
		network: 'store.perm_network',
		camera: 'store.perm_camera',
		microphone: 'store.perm_microphone',
		imu: 'store.perm_imu',
		additional_hardware: 'store.perm_hardware',
		background_service: 'store.perm_background',
		external_display: 'store.perm_display',
	};

	const apps = $derived(store_state.apps);
	const updates = $derived(apps.filter((a) => a.update));
	// like the Mac App Store, an app is browsed under its primary (first) category
	const primary = (a: StoreApp) => a.categories[0] ?? 'Other';
	const categories = $derived(
		[...new Set(apps.map(primary))].sort((a, b) => (a === 'Other' ? 1 : b === 'Other' ? -1 : a.localeCompare(b))),
	);
	const by_date = (k: 'published_at' | 'updated_at') => [...apps].sort((a, b) => b[k].localeCompare(a[k]));
	const newest = $derived(by_date('published_at'));
	const latest = $derived(by_date('updated_at'));
	const fresh = $derived(newest.slice(0, 6));
	const updated = $derived(latest.filter((a) => !fresh.includes(a)).slice(0, 6));
	const alpha = $derived([...apps].sort((a, b) => title(a).localeCompare(title(b))));
	const results = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return [];
		return alpha.filter((a) => [title(a), summary(a), a.author, a.package, ...a.categories].some((s) => s?.toLowerCase().includes(q)));
	});

	// The catalogue is the live registry: new apps and versions appear without
	// anyone touching ZeroDesk. Opening the window, the refresh button and an
	// open window every 10 minutes skip the Zero's 10-minute cache (the
	// registry's own CDN still caches for up to 10 minutes).
	let refreshing = $state(false);

	async function load(refresh = false) {
		if (refresh) refreshing = true;
		try {
			const r = await api<{ apps: StoreApp[] }>(`/api/store/catalog${refresh ? '?refresh=1' : ''}`);
			store_state.apps = r.apps;
			store_state.loaded = true;
			error = '';
			if (detail) detail = r.apps.find((a) => a.id === detail!.id) ?? detail;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			if (refresh) refreshing = false;
		}
	}

	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(() => {
		load(true);
		timer = setInterval(() => load(true), 10 * 60 * 1000);
	});
	onDestroy(() => clearInterval(timer));

	function go(p: Pane) {
		pane = p;
		category = null;
		section = null;
		detail = null;
		query = '';
		content_el?.scrollTo({ top: 0 });
	}

	function see_all(s: 'new' | 'updated') {
		section = s;
		category = null;
		detail = null;
		query = '';
		content_el?.scrollTo({ top: 0 });
	}

	function back() {
		if (detail) detail = null;
		else if (section) section = null;
		else category = null;
	}

	function show(a: StoreApp) {
		detail = a;
		content_el?.scrollTo({ top: 0 });
	}

	// ---- Get / Update / Open ----

	const get = (a: StoreApp) => enqueue(a, title(a));

	function update_all() {
		for (const a of updates) get(a);
	}

	// an install finished: refresh the catalogue (installed versions, updates)
	onMount(() => {
		const on_done = () => load();
		window.addEventListener('zerodesk-store-changed', on_done);
		return () => window.removeEventListener('zerodesk-store-changed', on_done);
	});

	function open(a: StoreApp) {
		if (a.desktop_id) open_device_app({ id: a.desktop_id, name: title(a) });
	}

	const header = $derived(
		detail
			? ''
			: query.trim()
				? t('store.results')
				: pane === 'categories'
					? category
						? cat_name(category)
						: t('store.categories')
					: pane === 'updates'
						? t('store.updates')
						: section
							? section === 'new'
								? t('store.new')
								: t('store.recently_updated')
							: t('store.discover'),
	);
</script>

{#snippet icon(a: StoreApp, size: number)}
	<span class="icon" style:--s="{size}px">
		{#if a.icon}<img src={a.icon} alt="" loading="lazy" width={size} height={size} draggable="false" />{/if}
	</span>
{/snippet}

{#snippet button(a: StoreApp)}
	{@const task = store_state.tasks[a.id]}
	{#if task?.phase === 'queued'}
		<!-- waiting for the install before it; a click takes it out of the queue -->
		<button class="get queued" title={t('store.cancel_waiting')} onclick={(e) => (e.stopPropagation(), cancel_queued(a.id))}>{t('store.waiting')}</button>
	{:else if task && task.phase !== 'auth'}
		<!-- the Mac App Store's ring: it fills with the download and install;
		     a short spinning arc only until the first progress arrives -->
		<span class="ring" class:spin={task.phase === 'waiting'} title={t('store.installing')} style:--p={task.phase === 'waiting' ? 0.2 : task.pct}>
			<svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true">
				<circle cx="12" cy="12" r="10" class="track" />
				<circle cx="12" cy="12" r="10" class="fill" />
				<rect x="8.5" y="8.5" width="7" height="7" rx="1.2" />
			</svg>
		</span>
	{:else if task}
		<!-- waiting for the password: the button stays, the ring comes after -->
		<button class="get" disabled>{a.update ? t('store.update') : t('store.get')}</button>
	{:else if a.update}
		<button class="get" onclick={(e) => (e.stopPropagation(), get(a))}>{t('store.update')}</button>
	{:else if a.installed}
		<button class="get" disabled={!a.desktop_id} onclick={(e) => (e.stopPropagation(), open(a))}>{t('store.open')}</button>
	{:else}
		<button class="get" onclick={(e) => (e.stopPropagation(), get(a))}>{t('store.get')}</button>
	{/if}
{/snippet}

{#snippet row(a: StoreApp)}
	<div class="row" role="button" tabindex="0" onclick={() => show(a)} onkeydown={(e) => e.key === 'Enter' && show(a)}>
		{@render icon(a, 52)}
		<span class="text">
			<b>{title(a)}</b>
			<small>{summary(a)}</small>
		</span>
		{@render button(a)}
	</div>
{/snippet}

{#snippet skeleton()}
	<div class="grid" aria-busy="true">
		{#each Array(9) as _, i (i)}
			<div class="row ghost"><span class="icon" style:--s="52px"></span><span class="text"><b></b><small></small></span></div>
		{/each}
	</div>
{/snippet}

<div class="store">
	<aside class="sidebar">
		<div class="lights-band app-window-drag-handle"></div>
		<label class="search">
			<i class="f7-icons">search</i>
			<input bind:value={query} placeholder={t('store.search')} oninput={() => (detail = null)} />
		</label>
		<nav>
			<button class="item" class:active={pane === 'discover' && !query && !detail} onclick={() => go('discover')}>
				<i class="f7-icons">star</i><span>{t('store.discover')}</span>
			</button>
			<button class="item" class:active={pane === 'categories' && !query && !detail} onclick={() => go('categories')}>
				<i class="f7-icons">square_grid_2x2</i><span>{t('store.categories')}</span>
			</button>
			<button class="item" class:active={pane === 'updates' && !query && !detail} onclick={() => go('updates')}>
				<i class="f7-icons">arrow_down_circle</i><span>{t('store.updates')}</span>
				{#if updates.length}<span class="badge">{updates.length}</span>{/if}
			</button>
		</nav>
	</aside>

	<section class="main">
		<header class="titlebar app-window-drag-handle">
			{#if detail || category || section}
				<button class="back" aria-label={t('store.back')} onclick={back}>
					<i class="f7-icons">chevron_left</i>
				</button>
			{/if}
			<h1>{header}</h1>
			{#if pane === 'updates' && !detail && !query && updates.length > 1}
				<button class="get wide" onclick={update_all}>{t('store.update_all')}</button>
			{/if}
			<button class="back refresh" aria-label={t('store.refresh')} title={t('store.refresh')} disabled={refreshing} onclick={() => load(true)}>
				<i class="f7-icons" class:spinning={refreshing}>arrow_clockwise</i>
			</button>
		</header>

		<div class="content" bind:this={content_el}>
			<!-- narrow window: the sidebar's pages as tabs -->
			<div class="tabs">
				<label class="search small">
					<i class="f7-icons">search</i>
					<input bind:value={query} placeholder={t('store.search')} oninput={() => (detail = null)} />
				</label>
				<div class="seg">
					<button class:on={pane === 'discover'} onclick={() => go('discover')}>{t('store.discover')}</button>
					<button class:on={pane === 'categories'} onclick={() => go('categories')}>{t('store.categories')}</button>
					<button class:on={pane === 'updates'} onclick={() => go('updates')}>{t('store.updates')}{updates.length ? ` ${updates.length}` : ''}</button>
				</div>
			</div>
			{#if error}<p class="err">{error}</p>{/if}

			{#if detail}
				{@const a = detail}
				<div class="hero">
					{@render icon(a, 112)}
					<div class="hero-text">
						<h2>{title(a)}</h2>
						<p>{summary(a)}</p>
						<div class="hero-actions">
							{@render button(a)}
						</div>
					</div>
				</div>

				<div class="strip">
					<div><small>{t('store.category')}</small><b>{cat_name(primary(a))}</b></div>
					<div><small>{t('store.developer')}</small><b>{a.author}</b></div>
					<div><small>{t('store.size')}</small><b>{format_bytes(a.size)}</b></div>
					<div><small>{t('store.version')}</small><b>{a.version}</b></div>
				</div>

				{#if a.screenshots?.length}
					<div class="shots">
						{#each a.screenshots as s (s)}<img src={s} alt="" loading="lazy" width="480" height="255" draggable="false" />{/each}
					</div>
				{/if}

				<p class="description">{description(a)}</p>

				<h3>{t('store.information')}</h3>
				<dl class="info">
					<div><dt>{t('store.developer')}</dt><dd>{a.author}</dd></div>
					<div><dt>{t('store.size')}</dt><dd>{format_bytes(a.size)}</dd></div>
					<div><dt>{t('store.category')}</dt><dd>{a.categories.map(cat_name).join(', ')}</dd></div>
					<div><dt>{t('store.version')}</dt><dd>{a.version}{a.installed && a.installed !== a.version ? ` (${t('store.installed_v', { v: a.installed })})` : ''}</dd></div>
					<div><dt>{t('store.updated')}</dt><dd>{format_date(a.updated_at)}</dd></div>
					<div><dt>{t('store.license')}</dt><dd>{a.license || '—'}</dd></div>
					<div>
						<dt>{t('store.permissions')}</dt>
						<dd>
							{Object.entries(a.permissions ?? {})
								.filter(([, v]) => v)
								.map(([k]) => (PERMS[k] ? t(PERMS[k] as never) : k))
								.join(', ') || '—'}
						</dd>
					</div>
					{#if a.source_repo}
						<div><dt>{t('store.source')}</dt><dd><a href={a.source_repo} target="_blank" rel="noreferrer">{a.source_repo.replace(/^https:\/\/(www\.)?/, '')}</a></dd></div>
					{/if}
				</dl>
			{:else if query.trim()}
				{#if results.length}
					<div class="grid">{#each results as a (a.id)}{@render row(a)}{/each}</div>
				{:else if store_state.loaded}
					<p class="empty">{t('store.no_results')}</p>
				{/if}
			{:else if !store_state.loaded}
				{@render skeleton()}
			{:else if section}
				<div class="grid">{#each (section === 'new' ? newest : latest) as a (a.id)}{@render row(a)}{/each}</div>
			{:else if pane === 'discover'}
				<div class="section">
					<h3>{t('store.new')}</h3>
					<button class="see-all" onclick={() => see_all('new')}>{t('store.see_all')}</button>
				</div>
				<div class="grid">{#each fresh as a (a.id)}{@render row(a)}{/each}</div>
				<div class="section">
					<h3>{t('store.recently_updated')}</h3>
					<button class="see-all" onclick={() => see_all('updated')}>{t('store.see_all')}</button>
				</div>
				<div class="grid">{#each updated as a (a.id)}{@render row(a)}{/each}</div>
				<h3>{t('store.all_apps', { n: apps.length })}</h3>
				<div class="grid">{#each alpha as a (a.id)}{@render row(a)}{/each}</div>
			{:else if pane === 'categories' && !category}
				<div class="cats">
					{#each categories as c (c)}
						<button class="cat" onclick={() => ((category = c), content_el?.scrollTo({ top: 0 }))}>
							<span class="cat-icon" style:--tint={CATS[c]?.[2] ?? '#8e8e93'}><i class="f7-icons">{CATS[c]?.[1] ?? 'app'}</i></span>
							<span class="text"><b>{cat_name(c)}</b><small>{t('store.n_apps', { n: apps.filter((a) => primary(a) === c).length })}</small></span>
						</button>
					{/each}
				</div>
			{:else if pane === 'categories' && category}
				<div class="grid">{#each alpha.filter((a) => primary(a) === category) as a (a.id)}{@render row(a)}{/each}</div>
			{:else if pane === 'updates'}
				{#if updates.length}
					<div class="list">
						{#each updates as a (a.id)}
							<div class="update" role="button" tabindex="0" onclick={() => show(a)} onkeydown={(e) => e.key === 'Enter' && show(a)}>
								{@render icon(a, 52)}
								<span class="text">
									<b>{title(a)}</b>
									<small>{a.installed} → {a.version} · {format_date(a.updated_at)}</small>
								</span>
								{@render button(a)}
							</div>
						{/each}
					</div>
				{:else}
					<p class="empty">{t('store.up_to_date')}</p>
				{/if}
			{/if}
		</div>
	</section>
</div>

{#if store_state.alert}
	<div class="alert-layer" role="presentation">
		<div class="alert" role="alertdialog" aria-label={store_state.alert.title}>
			<b>{store_state.alert.title}</b>
			<p>{store_state.alert.message}</p>
			<button class="alert-btn" onclick={() => (store_state.alert = null)}>{t('installer.ok')}</button>
		</div>
	</div>
{/if}

<style>
	.store {
		position: relative;
		display: grid;
		grid-template-columns: 220px minmax(0, 1fr);
		height: 100%;
		min-height: 0;
	}

	.f7-icons {
		font-size: inherit;
		line-height: 1;
		font-style: normal;
	}

	/* ---- sidebar (as in Settings) ---- */

	.sidebar {
		display: flex;
		flex-direction: column;
		margin: var(--zd-sidebar-inset);
		margin-right: 0;
		padding: 0 10px 10px;
		border-radius: var(--zd-radius-sidebar);
		background: var(--zd-sidebar-bg);
		backdrop-filter: blur(30px) saturate(1.4);
		box-shadow: 0 0 0 0.5px var(--zd-hairline);
		min-height: 0;
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
		margin-bottom: 10px;
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

	nav {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.item {
		display: flex;
		align-items: center;
		justify-content: flex-start;
		gap: 9px;
		height: 30px;
		padding: 0 8px;
		border-radius: 8px;
		font-size: 13px;
		color: var(--zd-text);

		i {
			width: 18px;
			font-size: 16px;
			color: var(--zd-text-secondary);
			text-align: center;
		}

		&.active {
			background: var(--zd-selected);
			font-weight: 600;
		}
	}

	.badge {
		margin-left: auto;
		min-width: 18px;
		height: 18px;
		padding: 0 5px;
		border-radius: 9px;
		font-size: 11px;
		font-weight: 600;
		line-height: 18px;
		text-align: center;
		color: #fff;
		background: var(--zd-red);
	}

	/* ---- main ---- */

	.main {
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
		gap: 8px;
		padding: 0 28px;

		h1 {
			flex: 1;
			min-width: 0;
			font-size: 15px;
			font-weight: 600;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.back {
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		margin-left: -8px;
		border-radius: 50%;
		font-size: 15px;
		color: var(--zd-text-secondary);

		&:hover {
			background: var(--zd-hover);
		}
	}

	.refresh {
		margin: 0 -8px 0 0;

		&:disabled {
			opacity: 0.5;
		}
		.spinning {
			animation: turn 900ms linear infinite;
		}
	}

	@keyframes turn {
		to {
			transform: rotate(360deg);
		}
	}

	.content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		padding: 0 28px 32px;
	}

	h3 {
		margin: 22px 0 10px;
		font-size: 17px;
		font-weight: 700;

		&:first-child {
			margin-top: 4px;
		}
	}

	.section {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 12px;
		margin: 22px 0 10px;

		&:first-child {
			margin-top: 4px;
		}

		h3 {
			margin: 0;
		}
	}

	.see-all {
		flex: none;
		font-size: 13px;
		color: var(--system-color-primary);

		&:hover {
			text-decoration: underline;
		}
	}

	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
		column-gap: 24px;
	}

	.row,
	.update {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
		padding: 10px 0;
		border-bottom: 0.5px solid var(--zd-hairline);
		cursor: default;
	}

	.text {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;

		b {
			font-size: 13px;
			font-weight: 600;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}

		small {
			font-size: 12px;
			color: var(--zd-text-secondary);
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.ghost {
		.icon {
			background: var(--zd-hover);
		}

		b,
		small {
			display: block;
			height: 10px;
			border-radius: 5px;
			background: var(--zd-hover);
		}

		b {
			width: 50%;
		}

		small {
			width: 80%;
		}
	}

	.icon {
		flex: none;
		width: var(--s);
		height: var(--s);
		border-radius: calc(var(--s) * 0.225);
		overflow: hidden;
		background: var(--zd-hover);
		box-shadow: 0 0 0 0.5px var(--zd-hairline);

		img {
			display: block;
			width: 100%;
			height: 100%;
			object-fit: cover;
		}
	}

	/* the App Store's capsule button */
	.get {
		flex: none;
		min-width: 64px;
		height: 26px;
		padding: 0 14px;
		border-radius: 13px;
		font-size: 13px;
		font-weight: 700;
		color: var(--system-color-primary);
		background: var(--zd-hover);
		transition: transform 160ms var(--zd-spring);

		&:active:not(:disabled) {
			transform: scale(0.95);
		}

		&:disabled {
			opacity: 0.45;
		}

		&.wide {
			font-weight: 600;
		}
	}

	.ring {
		flex: none;
		display: grid;
		place-items: center;
		width: 64px;
		height: 26px;
		color: var(--system-color-primary);

		svg {
			overflow: visible;
		}

		circle {
			fill: none;
			stroke-width: 2.4;
		}

		.track {
			stroke: var(--zd-hover);
		}

		.fill {
			stroke: currentColor;
			stroke-linecap: round;
			stroke-dasharray: 62.8;
			stroke-dashoffset: calc(62.8 * (1 - var(--p)));
			transform: rotate(-90deg);
			transform-origin: center;
			transition: stroke-dashoffset 200ms linear;
		}

		rect {
			fill: currentColor;
		}

		&.spin .fill {
			animation: spin 900ms linear infinite;
		}
	}

	/* ---- app page ---- */

	.hero {
		display: flex;
		gap: 22px;
		align-items: center;
		padding: 6px 0 18px;
	}

	.hero-text {
		min-width: 0;

		h2 {
			margin: 0;
			font-size: 24px;
			font-weight: 700;
		}

		p {
			margin: 4px 0 0;
			font-size: 14px;
			color: var(--zd-text-secondary);
		}
	}

	.hero-actions {
		margin-top: 14px;
	}

	.strip {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		border-block: 0.5px solid var(--zd-hairline);

		div {
			display: flex;
			flex-direction: column;
			align-items: center;
			gap: 3px;
			min-width: 0;
			padding: 10px 6px;

			& + div {
				border-left: 0.5px solid var(--zd-hairline);
			}
		}

		small {
			font-size: 10px;
			font-weight: 600;
			text-transform: uppercase;
			color: var(--zd-text-tertiary);
		}

		b {
			max-width: 100%;
			font-size: 13px;
			font-weight: 600;
			color: var(--zd-text-secondary);
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.shots {
		display: flex;
		gap: 14px;
		margin: 20px -28px 0;
		padding: 0 28px;
		overflow-x: auto;
		scrollbar-width: none;

		img {
			flex: none;
			width: 480px;
			max-width: 80%;
			height: auto;
			aspect-ratio: 320 / 170;
			border-radius: 14px;
			background: #000;
			image-rendering: pixelated;
			box-shadow: 0 0 0 0.5px var(--zd-hairline);
		}
	}

	.description {
		margin: 20px 0 0;
		font-size: 13px;
		line-height: 1.55;
		white-space: pre-line;
	}

	.info {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
		gap: 14px 24px;
		margin: 0;

		div {
			min-width: 0;
		}

		dt {
			font-size: 11px;
			color: var(--zd-text-tertiary);
		}

		dd {
			margin: 3px 0 0;
			font-size: 13px;
			overflow-wrap: anywhere;
		}

		a {
			color: var(--system-color-primary);
		}
	}

	.cats {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
		gap: 10px;
	}

	.cat {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 12px;
		border-radius: 14px;
		text-align: left;
		background: var(--zd-hover);
		transition: transform 160ms var(--zd-spring);

		&:active {
			transform: scale(0.98);
		}
	}

	.cat-icon {
		flex: none;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border-radius: 9px;
		font-size: 19px;
		color: #fff;
		background: var(--tint);
	}

	.empty,
	.err {
		margin: 40px 0;
		text-align: center;
		font-size: 13px;
		color: var(--zd-text-secondary);
	}

	.err {
		margin: 6px 0 10px;
		color: var(--zd-red);
	}

	.get.queued {
		font-weight: 500;
		color: var(--zd-text-secondary);
	}

	/* a failed install: a macOS-style alert over the window, not a line that pushes the page down */
	.alert-layer {
		position: absolute;
		inset: 0;
		z-index: 40;
		display: grid;
		place-items: center;
		padding: 16px;
		background: hsla(0, 0%, 0%, 0.12);
		animation: fade-in 160ms ease;
	}

	.alert {
		width: min(260px, 100%);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		padding: 22px 18px 18px;
		border-radius: 22px;
		background: var(--zd-window-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 20px 60px hsla(0, 0%, 0%, 0.3);
		text-align: center;

		b {
			font-size: 13px;
			font-weight: 700;
			line-height: 1.35;
			overflow-wrap: anywhere;
		}

		p {
			margin: 0;
			font-size: 11px;
			line-height: 1.45;
			color: var(--zd-text-secondary);
			overflow-wrap: anywhere;
		}
	}

	.alert-btn {
		width: 100%;
		height: 28px;
		margin-top: 10px;
		border-radius: 14px;
		font-size: 13px;
		font-weight: 500;
		color: var(--system-color-primary-contrast);
		background: var(--system-color-primary);
	}

	@keyframes fade-in {
		from {
			opacity: 0;
		}
	}

	@keyframes spin {
		to {
			transform: rotate(270deg);
		}
	}

	.tabs {
		display: none;
		flex-direction: column;
		gap: 10px;
		margin-bottom: 8px;
	}

	.seg {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 2px;
		padding: 2px;
		border-radius: 10px;
		background: var(--zd-hover);

		button {
			height: 26px;
			border-radius: 8px;
			font-size: 12px;
			font-weight: 500;
			color: var(--zd-text-secondary);
			white-space: nowrap;

			&.on {
				color: var(--zd-text);
				background: var(--zd-window-bg);
				box-shadow: 0 1px 3px hsla(0, 0%, 0%, 0.12);
			}
		}
	}

	@container (max-width: 560px) {
		.tabs {
			display: flex;
		}

		.store {
			grid-template-columns: minmax(0, 1fr);
		}

		.sidebar {
			display: none;
		}

		.titlebar {
			padding-left: 92px;
		}

		.content {
			padding: 0 16px 24px;
		}

		.shots {
			margin-inline: -16px;
			padding-inline: 16px;
		}

		.strip {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
</style>
