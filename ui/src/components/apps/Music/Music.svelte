<script lang="ts">
	import { cached, revalidate } from '🍎/lib/swr.svelte.ts';
	import { data } from '🍎/state/app_preload.ts';
	import { icon_url } from '🍎/lib/icons.svelte.ts';
	import Glyph from '../common/Glyph.svelte';
	import { onDestroy, tick } from 'svelte';
	import { post } from '🍎/lib/api.ts';
	import { apps } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { fmt_time, list_audio, upload_files, type Track } from './library.ts';

	// Layout follows Music on macOS 26/27 (docs/macos-reference.md): a floating
	// sidebar, a large title over the song table, and the player as a floating
	// glass capsule at the bottom of the content. Audio streams from the Zero's
	// ~/Music and plays on the computer.

	// last known library paints at once; a refresh follows (lib/swr)
	let tracks = $state<Track[]>(cached<Track[]>('music') ?? []);
	let loading = $state(!tracks.length);
	let view = $state<'songs' | 'recent'>('songs');
	let query = $state('');
	let selected = $state<string | null>(null);
	let drag_over = $state(false);
	let upload_pct = $state<number | null>(null);
	let list_el = $state<HTMLElement>();
	let search_el = $state<HTMLInputElement>();

	const audio = new Audio();
	audio.preload = 'metadata';
	let current = $state<Track | null>(null);
	let playing = $state(false);
	let position = $state(0);
	let duration = $state(0);
	let volume = $state(0.8);
	let scrubbing = $state(false);
	let shuffle = $state(false);
	let repeat = $state<'off' | 'all' | 'one'>('off');
	audio.volume = 0.8;

	const shown = $derived.by(() => {
		const q = query.trim().toLowerCase();
		const list = q ? tracks.filter((x) => x.title.toLowerCase().includes(q)) : tracks;
		return view === 'recent' ? [...list].sort((a, b) => b.modified - a.modified).slice(0, 50) : list;
	});

	async function load() {
		loading = !tracks.length;
		try {
			tracks = (await revalidate('music', data.music)) as Track[];
		} finally {
			loading = false;
		}
	}

	function play(track: Track) {
		if (current?.path !== track.path) {
			current = track;
			audio.src = `/api/fs/get?path=${encodeURIComponent(track.path)}`;
		}
		audio.play().catch(() => (playing = false));
	}

	function toggle() {
		if (!current) {
			const first = shown.find((x) => x.path === selected) ?? shown[0];
			if (first) play(first);
			return;
		}
		if (audio.paused) audio.play();
		else audio.pause();
	}

	function step(dir: 1 | -1, from_end = false) {
		if (!current) return;
		if (dir === -1 && audio.currentTime > 3) return void (audio.currentTime = 0);
		if (from_end && repeat === 'one') {
			audio.currentTime = 0;
			return void audio.play();
		}
		let next: Track | undefined;
		if (shuffle && shown.length > 1) {
			const others = shown.filter((x) => x.path !== current!.path);
			next = others[Math.floor(Math.random() * others.length)];
		} else {
			const i = shown.findIndex((x) => x.path === current!.path);
			next = shown[i + dir] ?? (repeat === 'all' ? shown[dir === 1 ? 0 : shown.length - 1] : undefined);
		}
		if (next) {
			selected = next.path;
			play(next);
		} else {
			audio.pause();
			audio.currentTime = 0;
		}
	}

	audio.addEventListener('play', () => (playing = true));
	audio.addEventListener('pause', () => (playing = false));
	audio.addEventListener('ended', () => step(1, true));
	audio.addEventListener('loadedmetadata', () => {
		duration = audio.duration;
		if (current && !current.duration) {
			const p = current.path;
			tracks = tracks.map((x) => (x.path === p ? { ...x, duration: audio.duration } : x));
		}
	});
	audio.addEventListener('timeupdate', () => {
		if (!scrubbing) position = audio.currentTime;
	});

	function seek_input(e: Event) {
		position = +(e.currentTarget as HTMLInputElement).value;
	}
	function seek_commit() {
		audio.currentTime = position;
		scrubbing = false;
	}

	async function move_to_trash(track: Track) {
		await post('/api/fs/op', { op: 'trash', path: track.path });
		window.dispatchEvent(new Event('zerodesk-trash-changed'));
		if (current?.path === track.path) {
			audio.pause();
			current = null;
		}
		tracks = tracks.filter((x) => x.path !== track.path);
	}

	async function on_key(e: KeyboardEvent) {
		if ((e.target as HTMLElement).closest('input')) return;
		const i = shown.findIndex((x) => x.path === selected);
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			const n = shown[Math.max(0, Math.min(shown.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))];
			if (n) {
				selected = n.path;
				await tick();
				list_el?.querySelector('.row.selected')?.scrollIntoView({ block: 'nearest' });
			}
		} else if (e.key === ' ') {
			e.preventDefault();
			toggle();
		} else if (e.key === 'Enter' && i >= 0) {
			play(shown[i]);
		} else if ((e.key === 'Backspace' || e.key === 'Delete') && e.metaKey && i >= 0) {
			move_to_trash(shown[i]);
		} else if (e.key === 'ArrowRight' && e.metaKey) {
			step(1);
		} else if (e.key === 'ArrowLeft' && e.metaKey) {
			step(-1);
		} else if (e.key === 'f' && e.metaKey) {
			e.preventDefault();
			search_el?.focus();
		}
	}

	function add(files: File[]) {
		if (!files.length) return;
		upload_files('~/Music', files, (p) => (upload_pct = p)).finally(() => {
			upload_pct = null;
			load();
		});
	}

	function on_drop(e: DragEvent) {
		e.preventDefault();
		drag_over = false;
		add([...(e.dataTransfer?.files ?? [])]);
	}

	function pick_files() {
		const input = document.createElement('input');
		input.type = 'file';
		input.multiple = true;
		input.accept = 'audio/*';
		input.onchange = () => add([...(input.files ?? [])]);
		input.click();
	}

	// pick up files added elsewhere (Finder, the Zero) when the window comes back to front
	$effect(() => {
		if (apps.active === 'music') load();
	});
	onDestroy(() => {
		audio.pause();
		audio.src = '';
	});

	const F7: Record<string, string> = {
		clock: 'clock',
		note: 'music_note',
		prev: 'backward_fill',
		next: 'forward_fill',
		play: 'play_fill',
		pause: 'pause_fill',
		volume: 'speaker_2_fill',
	};

	const title = $derived(view === 'recent' ? t('music.recent') : t('music.songs'));
</script>

{#snippet glyph(name: string, size = 18)}
	<Glyph name={F7[name] ?? name} size={Math.round(size * 0.9)} />
{/snippet}

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="music" tabindex="0" role="application" aria-label={t('app.music')} onkeydown={on_key}>
	<aside class="sidebar">
		<div class="lights-band app-window-drag-handle"></div>
		<button class="item" onclick={() => search_el?.focus()}>
			<span class="ic">{@render glyph('search')}</span><span>{t('music.search')}</span>
		</button>
		<p class="section">{t('music.library')}</p>
		<button class="item" class:active={view === 'recent'} onclick={() => (view = 'recent')}>
			<span class="ic">{@render glyph('clock')}</span><span>{t('music.recent')}</span>
		</button>
		<button class="item" class:active={view === 'songs'} onclick={() => (view = 'songs')}>
			<span class="ic">{@render glyph('note')}</span><span>{t('music.songs')}</span>
		</button>
		<span class="grow"></span>
		<div class="source">
			<img src={icon_url('cardputer-light')} alt="" width="32" height="32" />
			<span class="source-text"><b>CardputerZero</b><small>~/Music</small></span>
		</div>
	</aside>

	<section
		class="main"
		class:drag-over={drag_over}
		aria-label={title}
		ondragover={(e) => {
			if (!e.dataTransfer?.types.includes('Files')) return;
			e.preventDefault();
			drag_over = true;
		}}
		ondragleave={(e) => !(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node) && (drag_over = false)}
		ondrop={on_drop}
	>
		<header class="bar app-window-drag-handle">
			<button class="glass round" aria-label={t('music.add')} onclick={pick_files}>{@render glyph('plus', 16)}</button>
			<label class="glass search">
				{@render glyph('search', 15)}
				<input bind:this={search_el} placeholder={t('music.search')} bind:value={query} spellcheck="false" />
			</label>
		</header>

		<div class="scroll" bind:this={list_el}>
			<h1>{title}</h1>
			{#if shown.length}
				<div class="header-row">
					<span>{t('music.title')}</span>
					<span>{t('music.kind')}</span>
					<span class="r">{t('music.time')}</span>
				</div>
				{#each shown as track (track.path)}
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<div
						class="row"
						class:selected={selected === track.path}
						class:now={current?.path === track.path}
						role="row"
						tabindex="-1"
						draggable="true"
						ondragstart={(e) => e.dataTransfer?.setData('text/uri-list', location.origin + `/api/fs/get?path=${encodeURIComponent(track.path)}&download=1`)}
						onclick={() => (selected = track.path)}
						ondblclick={() => play(track)}
					>
						<span class="c-title">
							<span class="eq" aria-hidden="true">
								{#if current?.path === track.path}
									<span class:on={playing}></span><span class:on={playing}></span><span class:on={playing}></span>
								{/if}
							</span>
							<span class="name">{track.title}</span>
						</span>
						<span class="c-sub">{track.ext.toUpperCase()}</span>
						<span class="c-sub r tnum">{track.duration ? fmt_time(track.duration) : ''}</span>
					</div>
				{/each}
			{:else if !loading}
				<div class="empty">
					<span class="empty-glyph">{@render glyph(query ? 'search' : 'note', 88)}</span>
					<p>{query ? t('music.no_results') : t('music.empty')}</p>
				</div>
			{/if}
		</div>

		<!-- the player: a floating glass capsule, as in Music on macOS 26/27 -->
		<div class="player">
			<div class="transport">
				<button class="pb" class:on={shuffle} aria-label="Shuffle" aria-pressed={shuffle} onclick={() => (shuffle = !shuffle)}>{@render glyph('shuffle', 17)}</button>
				<button class="pb" aria-label="Previous" disabled={!current} onclick={() => step(-1)}>{@render glyph('prev', 20)}</button>
				<button class="pb big" aria-label={playing ? 'Pause' : 'Play'} disabled={!shown.length && !current} onclick={toggle}>
					{@render glyph(playing ? 'pause' : 'play', 24)}
				</button>
				<button class="pb" aria-label="Next" disabled={!current} onclick={() => step(1)}>{@render glyph('next', 20)}</button>
				<button
					class="pb"
					class:on={repeat !== 'off'}
					aria-label="Repeat"
					onclick={() => (repeat = repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off')}
				>
					{@render glyph('repeat', 17)}
					{#if repeat === 'one'}<i class="one">1</i>{/if}
				</button>
			</div>

			<div class="lcd">
				{#if current}
					<span class="art" aria-hidden="true">{@render glyph('note', 16)}</span>
					<div class="lcd-body">
						<span class="lcd-title">{current.title}</span>
						<input
							class="scrub"
							type="range"
							min="0"
							max={duration || 0}
							step="0.1"
							value={position}
							style:--p="{duration ? (position / duration) * 100 : 0}%"
							onpointerdown={() => (scrubbing = true)}
							oninput={seek_input}
							onchange={seek_commit}
							aria-label="Position"
						/>
						<span class="times tnum"><span>{fmt_time(position)}</span><span>-{fmt_time(Math.max(0, duration - position))}</span></span>
					</div>
				{:else}
					<span class="idle" aria-hidden="true">{@render glyph('note', 22)}</span>
				{/if}
			</div>

			<div class="vol">
				{@render glyph('volume', 18)}
				<input
					type="range"
					min="0"
					max="1"
					step="0.01"
					bind:value={volume}
					oninput={() => (audio.volume = volume)}
					style:--p="{volume * 100}%"
					aria-label={t('piano.volume')}
				/>
			</div>
		</div>

		{#if upload_pct !== null}
			<div class="progress"><span style:width="{upload_pct}%"></span></div>
		{/if}
	</section>
</div>

<style>
	.music {
		--tint: #fa2d48;
		display: grid;
		grid-template-columns: 228px minmax(0, 1fr);
		height: 100%;
		min-height: 0;
		outline: none;
	}

	/* floating sidebar, concentric with the window corner */
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
	}

	.lights-band {
		height: calc(var(--zd-titlebar) - var(--zd-sidebar-inset));
		flex: none;
	}

	.item {
		justify-content: flex-start;
		gap: 10px;
		height: 32px;
		padding: 0 10px;
		border-radius: 10px;
		font-size: 13px;
		color: var(--zd-text);
		transition: background-color 120ms ease;

		.ic {
			display: grid;
			place-items: center;
			width: 20px;
			color: var(--zd-text-secondary);
		}

		&:hover {
			background: var(--zd-hover);
		}

		&.active {
			background: var(--zd-selected);
			color: var(--tint);
			font-weight: 600;

			.ic {
				color: var(--tint);
			}
		}
	}

	.section {
		margin: 14px 10px 4px;
		font-size: 11px;
		font-weight: 600;
		color: var(--zd-text-tertiary);
	}

	.grow {
		flex: 1;
	}

	.source {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 6px 4px 0;

		img {
			flex: none;
		}
	}

	.source-text {
		display: flex;
		flex-direction: column;
		min-width: 0;
		font-size: 12px;
		line-height: 1.3;

		b {
			font-weight: 600;
		}

		small {
			font-size: 11px;
			color: var(--zd-text-secondary);
		}
	}

	.main {
		position: relative;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;

		&.drag-over::after {
			content: '';
			position: absolute;
			inset: var(--zd-titlebar) 12px 12px;
			border-radius: var(--zd-radius-sidebar);
			box-shadow: inset 0 0 0 2px var(--tint);
			pointer-events: none;
		}
	}

	.bar {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		z-index: 2;
		height: var(--zd-titlebar);
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 12px;
		padding: 0 14px;
	}

	.glass {
		height: 36px;
		border-radius: 18px;
		background: var(--zd-glass);
		backdrop-filter: blur(24px) saturate(1.6);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 0 0 0.5px var(--zd-hairline),
			0 2px 8px hsla(0, 0%, 0%, 0.06);
		color: var(--zd-text-secondary);
	}

	.round {
		width: 36px;
		display: grid;
		place-items: center;
		color: var(--zd-text);
	}

	.search {
		display: flex;
		align-items: center;
		gap: 8px;
		width: min(280px, 46%);
		padding: 0 14px;

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

	.scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		/* content scrolls under the floating bar and the player */
		padding: calc(var(--zd-titlebar) + 4px) 20px 96px;
	}

	h1 {
		margin: 0 4px 14px;
		font-size: 26px;
		font-weight: 700;
		letter-spacing: -0.3px;
	}

	.header-row,
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 72px 64px;
		align-items: center;
		gap: 12px;
		padding: 0 12px;
	}

	.header-row {
		height: 26px;
		font-size: 11px;
		font-weight: 500;
		color: var(--zd-text-secondary);
		box-shadow: 0 0.5px 0 var(--zd-hairline);
		margin-bottom: 2px;
	}

	.r {
		text-align: right;
	}

	.row {
		height: 32px;
		border-radius: 8px;
		font-size: 13px;
		cursor: default;

		&:nth-child(even of .row) {
			background: hsla(0, 0%, 50%, 0.045);
		}

		&.now .name {
			color: var(--tint);
			font-weight: 600;
		}

		&.selected {
			background: var(--zd-selected);
		}
	}

	.c-title {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}

	.eq {
		width: 14px;
		height: 12px;
		flex: none;
		display: flex;
		align-items: flex-end;
		gap: 2px;
		color: var(--tint);

		span {
			width: 3px;
			height: 4px;
			border-radius: 1px;
			background: currentColor;

			&.on {
				animation: eq 0.9s ease-in-out infinite alternate;
			}

			&:nth-child(2).on {
				animation-delay: -0.3s;
			}

			&:nth-child(3).on {
				animation-delay: -0.6s;
			}
		}
	}

	@keyframes eq {
		to {
			height: 12px;
		}
	}

	.name {
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.c-sub {
		color: var(--zd-text-secondary);
		white-space: nowrap;
	}

	.tnum {
		font-variant-numeric: tabular-nums;
	}

	.empty {
		height: calc(100% - 60px);
		min-height: 220px;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 18px;
		color: var(--zd-text-tertiary);

		p {
			font-size: 26px;
			font-weight: 700;
			color: var(--zd-text-secondary);
		}
	}

	.player {
		position: absolute;
		left: 20px;
		right: 20px;
		bottom: 16px;
		z-index: 2;
		height: 54px;
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) 150px;
		align-items: center;
		gap: 16px;
		padding: 0 18px 0 12px;
		border-radius: 27px;
		background: var(--zd-glass);
		backdrop-filter: blur(30px) saturate(1.8);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 0 0 0.5px var(--zd-hairline),
			0 6px 20px hsla(0, 0%, 0%, 0.1);
	}

	.transport {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.pb {
		position: relative;
		width: 34px;
		height: 34px;
		border-radius: 50%;
		color: var(--zd-text-secondary);
		transition:
			background-color 120ms ease,
			transform 160ms var(--zd-spring);

		&.big {
			width: 40px;
			height: 40px;
			color: var(--zd-text);
		}

		&.on {
			color: var(--tint);
		}

		&:hover:not(:disabled) {
			background: var(--zd-hover);
		}

		&:active:not(:disabled) {
			transform: scale(0.88);
		}

		&:disabled {
			color: var(--zd-text-tertiary);
		}

		.one {
			position: absolute;
			right: 4px;
			bottom: 5px;
			font-size: 8px;
			font-style: normal;
			font-weight: 700;
		}
	}

	.lcd {
		min-width: 0;
		height: 42px;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 10px;
	}

	.idle {
		color: var(--zd-text-tertiary);
	}

	.art {
		width: 38px;
		height: 38px;
		flex: none;
		display: grid;
		place-items: center;
		border-radius: 8px;
		background: linear-gradient(#ff6b81, var(--tint));
		color: #fff;
	}

	.lcd-body {
		flex: 1;
		min-width: 0;
		max-width: 380px;
		display: grid;
		grid-template-columns: 1fr;
		row-gap: 2px;
	}

	.lcd-title {
		font-size: 12px;
		font-weight: 600;
		text-align: center;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.times {
		display: flex;
		justify-content: space-between;
		font-size: 9px;
		color: var(--zd-text-tertiary);
	}

	input[type='range'] {
		appearance: none;
		height: 3px;
		border-radius: 2px;
		background: linear-gradient(to right, var(--zd-text-secondary) var(--p), var(--zd-hairline-strong) var(--p));
		cursor: pointer;

		&::-webkit-slider-thumb {
			appearance: none;
			width: 10px;
			height: 10px;
			border-radius: 50%;
			background: #fff;
			box-shadow: 0 0.5px 2px hsla(0, 0%, 0%, 0.35);
			opacity: 0;
			transition: opacity 120ms ease;
		}

		&:hover::-webkit-slider-thumb,
		&:active::-webkit-slider-thumb {
			opacity: 1;
		}
	}

	.vol {
		display: flex;
		align-items: center;
		gap: 10px;
		color: var(--zd-text-secondary);

		input {
			flex: 1;
			min-width: 0;
		}
	}

	.progress {
		position: absolute;
		left: 40px;
		right: 40px;
		bottom: 8px;
		height: 3px;
		border-radius: 2px;
		background: var(--zd-hairline-strong);

		span {
			display: block;
			height: 100%;
			border-radius: 2px;
			background: var(--tint);
			transition: width 120ms linear;
		}
	}

	@container (max-width: 640px) {
		.music {
			grid-template-columns: minmax(0, 1fr);
		}

		.sidebar,
		.vol,
		.pb:first-child,
		.transport .pb:last-child {
			display: none;
		}

		.player {
			grid-template-columns: auto minmax(0, 1fr);
			left: 12px;
			right: 12px;
		}

		.header-row span:nth-child(2),
		.row .c-sub:nth-child(2) {
			visibility: hidden;
		}
	}
</style>
