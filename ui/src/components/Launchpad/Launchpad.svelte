<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { elevation } from '🍎/actions';
	import { decode_all, icon_url, is_decoded, learn_device_icons } from '🍎/lib/icons.svelte.ts';
	import { cached, revalidate } from '🍎/lib/swr.svelte.ts';
	import { data, device_icon_src, preload_app, type DeviceApp } from '🍎/state/app_preload.ts';
	import { ART } from '🍎/components/SVG/AppIcon.svelte';
	import { icon_look } from '🍎/state/preferences.svelte.ts';
	import AppIcon from '🍎/components/SVG/AppIcon.svelte';
	import DeviceIcon from '🍎/components/SVG/DeviceIcon.svelte';
	import { load_web_apps, native_of, open_device_app, web_apps } from '🍎/state/apps.svelte.ts';
	import { t, type MsgKey } from '🍎/state/i18n.svelte.ts';
	import { DRAG_MIME, launchpad, recents, note_recent } from '🍎/state/launchpad.svelte.ts';

	// macOS "Apps": a full-screen layer over a blurred desktop. A search field
	// centred at the top, category buttons under it, then the app grid centred
	// on screen (recently used first, then A–Z). Click opens the app; drag an
	// icon onto the Dock to keep it there; click empty space or Esc to close.

	type Cat = 'radio' | 'media' | 'utilities' | 'ai' | 'security' | 'other';
	const CAT_LABEL: Record<Cat, MsgKey> = {
		radio: 'apps.cat.radio',
		media: 'apps.cat.media',
		utilities: 'apps.cat.utilities',
		ai: 'apps.cat.ai',
		security: 'apps.cat.security',
		other: 'apps.cat.other',
	};

	// Most apps here ship without Categories=, so the official ones are mapped
	// by hand to the CardputerZero App Store categories.
	const KNOWN: Record<string, Cat> = {
		'cap-lora-1262': 'radio',
		'cap-lora-1262-gps': 'radio',
		'cap-cc1101-subg-chat': 'radio',
		'cap-cc1101-nfc': 'radio',
		'ir-chat': 'radio',
		'ir-remote': 'radio',
		music: 'media',
		camera_app: 'media',
		auracam: 'media',
		cardbili: 'media',
		recorder: 'media',
		piano: 'media',
		files: 'utilities',
		compass: 'utilities',
		factory_test: 'utilities',
		'keyboard-guide': 'utilities',
		AdvanceWebUI: 'utilities',
		zclaw: 'ai',
		bruce_cardputerzero: 'security',
	};

	function category_of(a: DeviceApp): Cat {
		if (KNOWN[a.id]) return KNOWN[a.id];
		const c = (a.categories ?? []).join(';');
		if (/AudioVideo|Audio|Video|Graphics|Photography|Game/i.test(c)) return 'media';
		if (/Network|Radio|HamRadio|Telephony/i.test(c)) return 'radio';
		if (/Security/i.test(c)) return 'security';
		if (/Utility|System|Settings|Development/i.test(c)) return 'utilities';
		return 'other';
	}

	// device apps plus apps that only exist as a ZeroDesk web UI (docs/APP-SPEC.md)
	function build(a: DeviceApp[]): DeviceApp[] {
		const web_only: DeviceApp[] = Object.values(web_apps)
			.filter((w) => !w.device_app)
			.map((w) => ({ id: `web:${w.id}`, name: w.name, terminal: false, hasIcon: true }) as DeviceApp);
		return [...a.filter((x) => !x.terminal), ...web_only].sort((x, y) => x.name.localeCompare(y.name));
	}

	/** what each tile is drawn with, so the grid can wait until all are decoded */
	function icon_srcs(l: DeviceApp[]): string[] {
		const m = icon_look() === 'dark' ? 'dark' : 'light';
		return l
			.map((a) =>
				a.id.startsWith('web:')
					? `/api/webapps/${encodeURIComponent(a.id.slice(4))}/icon?scheme=${m}`
					: native_of[a.id]
						? icon_url(`${ART[native_of[a.id]] ?? 'settings'}-${m}`)
						: a.hasIcon
							? device_icon_src(a)
							: undefined,
			)
			.filter((x): x is string => !!x);
	}

	// last known list (this tab), so Apps opens complete
	let list = $state<DeviceApp[]>(build(cached<DeviceApp[]>('apps') ?? []));
	let icons_ready = $state(false);
	let query = $state('');
	let cat = $state<Cat | null>(null);
	let search_el = $state<HTMLInputElement>();

	const cats = $derived((Object.keys(CAT_LABEL) as Cat[]).filter((c) => list.some((a) => category_of(a) === c)));

	const filtered = $derived(
		list.filter(
			(a) =>
				(!cat || category_of(a) === cat) &&
				(!query.trim() || a.name.toLowerCase().includes(query.trim().toLowerCase())),
		),
	);

	// first row: recently opened (only when not searching / filtering)
	const recent_apps = $derived(
		query.trim() || cat
			? []
			: recents.ids.map((id) => list.find((a) => a.id === id)).filter((a): a is DeviceApp => !!a).slice(0, 7),
	);

	$effect(() => {
		if (!launchpad.open) return;
		query = '';
		cat = null;
		// only the open state drives this; reading list here would re-run it on every refresh
		untrack(() => {
			// every icon decoded before the grid shows: they appear together, never one by one
			const want = icon_srcs(list);
			icons_ready = list.length > 0 && want.every(is_decoded);
			if (!icons_ready && list.length) decode_all(want, 3000).then(() => (icons_ready = true));
			Promise.all([revalidate('apps', data.apps), load_web_apps(true)])
				.then(([a]) => {
					learn_device_icons(a);
					const next = build(a);
					return decode_all(icon_srcs(next), 3000).then(() => {
						list = next;
						icons_ready = true;
					});
				})
				.catch(() => {});
		});
		tick().then(() => search_el?.focus());
	});

	function close() {
		launchpad.open = false;
	}

	function launch(a: DeviceApp) {
		close();
		note_recent(a.id);
		open_device_app({ id: a.id, name: a.name });
	}

	function on_drag_start(e: DragEvent, a: DeviceApp) {
		e.dataTransfer?.setData(DRAG_MIME, JSON.stringify({ id: a.id, name: a.name }));
		e.dataTransfer!.effectAllowed = 'copy';
		// let the Dock take the drop
		requestAnimationFrame(() => (launchpad.dragging = true));
	}

	function on_drag_end() {
		launchpad.dragging = false;
		close();
	}

	function on_key(e: KeyboardEvent) {
		if (!launchpad.open) return;
		if (e.key === 'Escape') close();
		if (e.key === 'Enter' && filtered.length) launch(filtered[0]);
	}

	function on_outside(e: PointerEvent) {
		if (!launchpad.open) return;
		const target = e.target as HTMLElement;
		// the Dock's own Apps button toggles the panel itself
		// clicks on empty space close it, like macOS; the Dock's Apps button toggles it itself
		if (!target.closest('.tile, .search, .chip') && !target.closest('.dock-open-app-button.launchpad') && !target.closest('.dock-el')) close();
	}
</script>

<svelte:window onkeydown={on_key} onpointerdown={on_outside} />

{#snippet tile(a: DeviceApp)}
	<button
		class="tile"
		draggable={!a.id.startsWith('web:')}
		onpointerenter={() => preload_app(native_of[a.id] ?? (a.id.startsWith('web:') ? a.id : `dev:${a.id}`))}
		onclick={() => launch(a)}
		ondragstart={(e) => on_drag_start(e, a)}
		ondragend={on_drag_end}
	>
		<span class="icon">
			{#if a.id.startsWith('web:')}
				<AppIcon id={a.id} size={80} />
			{:else if native_of[a.id]}
				<AppIcon id={native_of[a.id]} size={80} />
			{:else if a.hasIcon}
				<DeviceIcon id={a.id} size={80} />
			{:else}
				<span class="fallback">{a.name.slice(0, 1)}</span>
			{/if}
		</span>
		<span class="label">{a.name}</span>
	</button>
{/snippet}

{#if launchpad.open}
	<div class="apps" class:dragging={launchpad.dragging} use:elevation={'launchpad'} role="dialog" aria-label={t('app.launchpad')}>
		<div class="top">
			<label class="search">
				<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">
					<circle cx="10.5" cy="10.5" r="6.5" fill="none" stroke="currentColor" stroke-width="2" />
					<path d="M15.5 15.5l5 5" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
				</svg>
				<input bind:this={search_el} bind:value={query} placeholder={t('launchpad.search')} spellcheck="false" />
			</label>
			{#if cats.length > 1}
				<nav class="chips">
					<button class="chip" class:on={!cat} onclick={() => (cat = null)}>{t('apps.all')}</button>
					{#each cats as c (c)}
						<button class="chip" class:on={cat === c} onclick={() => (cat = cat === c ? null : c)}>{t(CAT_LABEL[c])}</button>
					{/each}
				</nav>
			{/if}
		</div>

		<div class="scroll">
			<div class="page" style:visibility={icons_ready ? 'visible' : 'hidden'}>
				{#if recent_apps.length}
					<div class="grid">
						{#each recent_apps as a (a.id)}{@render tile(a)}{/each}
					</div>
					<div class="rule"></div>
				{/if}
				<div class="grid">
					{#each filtered as a (a.id)}{@render tile(a)}{/each}
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	.apps {
		position: fixed;
		inset: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		background: hsla(0, 0%, 100%, 0.18);
		backdrop-filter: blur(48px) saturate(1.6);
		color: #1d1d1f;
		animation: fade-in 260ms var(--zd-ease-out);
		transition: opacity 180ms ease;

		&.dragging {
			opacity: 0;
			pointer-events: none;
		}
	}

	:global(body.dark) .apps {
		background: hsla(0, 0%, 0%, 0.28);
		color: #f5f5f7;
	}

	.top {
		flex: none;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 16px;
		padding: 60px 24px 8px;
	}

	.search {
		display: flex;
		align-items: center;
		gap: 8px;
		width: min(340px, calc(100vw - 48px));
		height: 36px;
		padding: 0 14px;
		border-radius: 18px;
		background: hsla(0, 0%, 100%, 0.42);
		box-shadow:
			inset 0 0 0 0.5px hsla(0, 0%, 100%, 0.7),
			0 1px 6px hsla(0, 0%, 0%, 0.08);
		color: hsla(0, 0%, 10%, 0.55);

		input {
			flex: 1;
			min-width: 0;
			border: 0;
			outline: 0;
			background: none;
			font-size: 14px;
			color: #1d1d1f;

			&::placeholder {
				color: hsla(0, 0%, 10%, 0.5);
			}
		}
	}

	:global(body.dark) .search {
		background: hsla(0, 0%, 100%, 0.12);
		box-shadow:
			inset 0 0 0 0.5px hsla(0, 0%, 100%, 0.16),
			0 1px 6px hsla(0, 0%, 0%, 0.2);
		color: hsla(0, 0%, 100%, 0.55);

		input {
			color: #f5f5f7;

			&::placeholder {
				color: hsla(0, 0%, 100%, 0.5);
			}
		}
	}

	.chips {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: 8px;
	}

	.chip {
		height: 28px;
		padding: 0 14px;
		border-radius: 14px;
		font-size: 13px;
		color: inherit;
		opacity: 0.8;
		transition:
			background-color 140ms ease,
			opacity 140ms ease;

		&:hover {
			opacity: 1;
			background: hsla(0, 0%, 50%, 0.14);
		}

		&.on {
			opacity: 1;
			font-weight: 600;
			background: hsla(0, 0%, 100%, 0.55);
			box-shadow: 0 1px 4px hsla(0, 0%, 0%, 0.1);
		}
	}

	:global(body.dark) .chip.on {
		background: hsla(0, 0%, 100%, 0.18);
	}

	/* The scroll area ends above the Dock, so the last row is never drawn
	   behind it; the bottom edge fades out to show there is more below. */
	.scroll {
		flex: 1;
		width: 100%;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		padding: 28px 24px 32px;
		margin-bottom: calc(var(--zd-dock-height, 92px) + 8px);
		mask-image: linear-gradient(to bottom, #000 calc(100% - 28px), transparent);
	}

	.page {
		width: min(980px, 100%);
		margin: 0 auto;
	}

	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(118px, 1fr));
		gap: 22px 8px;
		justify-items: center;
	}

	.rule {
		height: 1px;
		margin: 24px 40px;
		background: hsla(0, 0%, 50%, 0.25);
	}

	.tile {
		width: 112px;
		flex-direction: column;
		gap: 8px;
		padding: 4px 2px;
		border-radius: 14px;

		&:active .icon {
			transform: scale(0.92);
		}
	}

	.icon {
		width: 80px;
		height: 80px;
		display: grid;
		place-items: center;
		transition: transform 260ms var(--zd-spring);

		.fallback {
			font-size: 30px;
			font-weight: 600;
		}
	}

	.label {
		max-width: 100%;
		font-size: 12px;
		line-height: 1.25;
		text-shadow: 0 1px 2px hsla(0, 0%, 0%, 0.12);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	@keyframes fade-in {
		from {
			opacity: 0;
			transform: scale(1.04);
		}
	}
</style>
