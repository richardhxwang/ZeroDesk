<script lang="ts">
	import { cached, revalidate } from '🍎/lib/swr.svelte.ts';
	import { data } from '🍎/state/app_preload.ts';
	import Glyph from '../common/Glyph.svelte';
	import { onMount, tick } from 'svelte';
	import { api, ApiError, post } from '🍎/lib/api.ts';
	import { format_bytes, format_date, t, type MsgKey } from '🍎/state/i18n.svelte.ts';
	import Toolbar from '../common/Toolbar.svelte';
	import ToolButton from '../common/ToolButton.svelte';
	import { show_context_menu } from '🍎/state/contextmenu.svelte.ts';
	import { APPS_LOCATION, FILE_MIME, finder_nav, TRASH_LOCATION } from '🍎/state/finder_nav.svelte.ts';
	import AppsView from './AppsView.svelte';
	import FileGlyph from './FileGlyph.svelte';
	import TrashView from './TrashView.svelte';
	import QuickLook from './QuickLook.svelte';
	import { fetch_device_file, IMAGE_EXT, use_as_wallpaper } from '🍎/lib/wallpapers.ts';

	type Entry = { name: string; path: string; dir: boolean; size: number; modified: string; hidden?: boolean };

	import { session } from '🍎/state/session.svelte.ts';
	import { install_path, installer, is_package } from '🍎/state/installer.svelte.ts';

	const HOME = session.home;
	const PLACE_GLYPH: Record<string, string> = {
		apps: 'square_grid_2x2',
		home: 'house',
		desk: 'desktopcomputer',
		doc: 'doc',
		down: 'arrow_down_circle',
		pic: 'photo',
		music: 'music_note',
	};
	const places: { key: MsgKey; path: string; icon: 'apps' | 'home' | 'desk' | 'doc' | 'down' | 'pic' | 'music' }[] = [
		{ key: 'finder.applications', path: APPS_LOCATION, icon: 'apps' },
		{ key: 'finder.home', path: HOME, icon: 'home' },
		{ key: 'finder.desktop', path: `${HOME}/Desktop`, icon: 'desk' },
		{ key: 'finder.documents', path: `${HOME}/Documents`, icon: 'doc' },
		{ key: 'finder.downloads', path: `${HOME}/Downloads`, icon: 'down' },
		{ key: 'finder.pictures', path: `${HOME}/Pictures`, icon: 'pic' },
		{ key: 'finder.music', path: `${HOME}/Music`, icon: 'music' },
	];
	const devices: { key: MsgKey; path: string }[] = [
		{ key: 'finder.root', path: '/' },
		{ key: 'finder.usb', path: '/media' },
	];

	let path = $state(HOME);
	// narrow window: no sidebar and only the name column, like a narrow Finder window
	let finder_width = $state(800);
	const narrow = $derived(finder_width < 520);
	let entries = $state<Entry[]>([]);
	let loading = $state(false);
	let error = $state('');
	let selected = $state<string | null>(null);
	let renaming = $state<string | null>(null);
	let rename_value = $state('');
	let show_hidden = $state(false);
	let back = $state<string[]>([]);
	let fwd = $state<string[]>([]);
	let drag_over = $state(false);
	let upload_pct = $state<number | null>(null);
	let confirm_delete = $state<Entry | null>(null);
	let preview = $state<Entry | null>(null);
	let file_input = $state<HTMLInputElement>();
	let direction = $state<1 | -1>(1);

	const visible = $derived(show_hidden ? entries : entries.filter((e) => !e.hidden));
	const sel = $derived(entries.find((e) => e.path === selected) ?? null);
	const crumbs = $derived(
		path === '/' ? ['/'] : ['/', ...path.split('/').filter(Boolean)].map((_, i, a) => (i === 0 ? '/' : '/' + a.slice(1, i + 1).join('/'))),
	);
	const folder_name = $derived(
		path === APPS_LOCATION ? t('finder.applications') : path === TRASH_LOCATION ? t('trash.title') : path === '/' ? '/' : path.split('/').pop()!,
	);

	const is_virtual = (p: string) => p === APPS_LOCATION || p === TRASH_LOCATION;
	let trash_view = $state<TrashView>();
	let trash_count = $state(0);
	let search = $state('');

	async function load(p: string) {
		if (is_virtual(p)) {
			path = p;
			entries = [];
			selected = null;
			error = '';
			return;
		}
		error = '';
		// a folder seen before (or preloaded) paints at once; the listing refreshes behind it
		type Listing = { path: string; entries: Entry[] };
		const known = cached<Listing>(`fs:${p}`);
		if (known) {
			path = known.path;
			entries = known.entries;
			selected = null;
		}
		loading = !known;
		try {
			const res = (await revalidate(`fs:${p}`, data.fs(p))) as Listing;
			if (known && known.path !== path) return; // navigated away meanwhile
			path = res.path;
			entries = res.entries;
			if (!known) selected = null;
		} catch (e) {
			error = e instanceof ApiError ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function go(p: string, push = true) {
		if (p === path) return;
		if (push) {
			back = [...back, path];
			fwd = [];
		}
		direction = p.length >= path.length ? 1 : -1;
		load(p);
	}

	function go_back() {
		const prev = back.at(-1);
		if (!prev) return;
		back = back.slice(0, -1);
		fwd = [path, ...fwd];
		direction = -1;
		load(prev);
	}

	function go_fwd() {
		const next = fwd[0];
		if (!next) return;
		fwd = fwd.slice(1);
		back = [...back, path];
		direction = 1;
		load(next);
	}

	function open(e: Entry) {
		if (e.dir) return go(e.path);
		if (is_package(e.name)) return install_path(e.path);
		preview = e;
	}

	function join(dir: string, name: string) {
		return dir === '/' ? `/${name}` : `${dir}/${name}`;
	}

	async function new_folder() {
		let name = t('finder.untitled');
		let i = 2;
		while (entries.some((e) => e.name === name)) name = `${t('finder.untitled')} ${i++}`;
		try {
			await post('/api/fs/op', { op: 'mkdir', path: join(path, name) });
			await load(path);
			selected = join(path, name);
			start_rename();
		} catch (e) {
			error = String((e as Error).message);
		}
	}

	async function start_rename() {
		if (!sel) return;
		renaming = sel.path;
		rename_value = sel.name;
		await tick();
		const input = document.querySelector<HTMLInputElement>('.finder .rename');
		input?.focus();
		const dot = sel.dir ? -1 : rename_value.lastIndexOf('.');
		input?.setSelectionRange(0, dot > 0 ? dot : rename_value.length);
	}

	async function commit_rename() {
		const target = entries.find((e) => e.path === renaming);
		renaming = null;
		const name = rename_value.trim();
		if (!target || !name || name === target.name || name.includes('/')) return;
		try {
			await post('/api/fs/op', { op: 'rename', path: target.path, to: join(path, name) });
			await load(path);
			selected = join(path, name);
		} catch (e) {
			error = String((e as Error).message);
		}
	}

	/** ⌘⌫ / toolbar / menu: move to Trash, like macOS. Falls back to asking for
	 *  a permanent delete when the item is on another filesystem (USB stick). */
	async function move_to_trash(target: Entry) {
		error = '';
		try {
			await post('/api/fs/op', { op: 'trash', path: target.path });
			await load(path);
			window.dispatchEvent(new Event('zerodesk-trash-changed'));
		} catch (e) {
			if (e instanceof ApiError && e.status === 409) confirm_delete = target;
			else error = String((e as Error).message);
		}
	}

	async function set_wallpaper(e: Entry) {
		try {
			await use_as_wallpaper(await fetch_device_file(e.path));
		} catch (err) {
			error = String((err as Error).message);
		}
	}

	function row_menu(ev: MouseEvent, e: Entry) {
		selected = e.path;
		show_context_menu(ev, [
			{ label: t('apps.open'), action: () => open(e) },
			...(e.dir ? [] : [{ label: t('finder.quick_look'), action: () => (preview = e) }]),
			...(!e.dir && IMAGE_EXT.test(e.name) ? [{ label: t('finder.set_wallpaper'), action: () => set_wallpaper(e) }] : []),
			{ label: t('finder.rename'), action: () => start_rename(), break_after: true },
			...(e.dir ? [] : [{ label: t('finder.download'), action: () => download(e), break_after: true }]),
			{ label: t('apps.move_to_trash'), action: () => move_to_trash(e) },
		]);
	}

	function row_drag(ev: DragEvent, e: Entry) {
		ev.dataTransfer?.setData(FILE_MIME, e.path);
		ev.dataTransfer?.setData('text/plain', e.name);
		ev.dataTransfer!.effectAllowed = 'move';
	}

	// The Dock's Trash icon (or anything else) can steer Finder.
	let last_nav = finder_nav.seq;
	$effect(() => {
		if (finder_nav.seq !== last_nav) {
			last_nav = finder_nav.seq;
			if (finder_nav.target) go(finder_nav.target);
		}
	});

	function on_external_trash() {
		if (path === TRASH_LOCATION) trash_view?.load();
		else if (!is_virtual(path)) load(path);
	}

	async function do_delete() {
		const target = confirm_delete;
		confirm_delete = null;
		if (!target) return;
		try {
			await post('/api/fs/op', { op: 'delete', path: target.path });
			await load(path);
		} catch (e) {
			error = String((e as Error).message);
		}
	}

	function download(e: Entry) {
		const a = document.createElement('a');
		a.href = `/api/fs/get?download=1&path=${encodeURIComponent(e.path)}`;
		a.download = e.name;
		a.click();
	}

	/** Upload with progress (XHR, streamed to disk on the device). */
	function upload(files: File[]) {
		if (!files.length) return;
		const fd = new FormData();
		for (const f of files) fd.append('file', f, (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name);
		const xhr = new XMLHttpRequest();
		upload_pct = 0;
		xhr.upload.onprogress = (e) => e.lengthComputable && (upload_pct = Math.round((e.loaded / e.total) * 100));
		xhr.onloadend = () => {
			upload_pct = null;
			if (xhr.status !== 200) error = xhr.responseText || `HTTP ${xhr.status}`;
			load(path);
		};
		xhr.open('POST', `/api/fs/upload?dir=${encodeURIComponent(path)}`);
		xhr.send(fd);
	}

	function on_drop(e: DragEvent) {
		e.preventDefault();
		drag_over = false;
		upload([...(e.dataTransfer?.files ?? [])]);
	}

	function on_keydown(e: KeyboardEvent) {
		if (renaming || preview || installer.open || confirm_delete || is_virtual(path)) return;
		const list = visible;
		const idx = list.findIndex((x) => x.path === selected);
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			const n = Math.max(0, Math.min(list.length - 1, idx + (e.key === 'ArrowDown' ? 1 : -1)));
			selected = list[n]?.path ?? null;
			document.querySelector(`.finder [data-path="${CSS.escape(selected ?? '')}"]`)?.scrollIntoView({ block: 'nearest' });
		} else if (e.key === 'Enter' && sel) {
			e.preventDefault();
			start_rename();
		} else if (e.key === ' ' && sel && !sel.dir) {
			e.preventDefault();
			preview = sel;
		} else if ((e.key === 'Backspace' && e.metaKey) || e.key === 'Delete') {
			if (sel) move_to_trash(sel);
		} else if (e.key === 'ArrowUp' && e.metaKey) {
			go(crumbs.at(-2) ?? '/');
		} else if ((e.key === 'ArrowDown' && e.metaKey) || (e.key === 'o' && e.metaKey)) {
			if (sel) open(sel);
		}
	}

	onMount(() => {
		load(finder_nav.target || HOME);
		last_nav = finder_nav.seq;
		window.addEventListener('zerodesk-trash-changed', on_external_trash);
		return () => window.removeEventListener('zerodesk-trash-changed', on_external_trash);
	});
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="finder" class:narrow tabindex="0" role="application" onkeydown={on_keydown} bind:clientWidth={finder_width}>
	<aside class="sidebar">
		<div class="sidebar-pad app-window-drag-handle"></div>
		<p class="section">{t('finder.favorites')}</p>
		{#each places as p}
			<button class="place" class:active={path === p.path} onclick={() => go(p.path)}>
				<Glyph name={PLACE_GLYPH[p.icon] ?? 'folder'} size={15} />
				<span>{t(p.key)}</span>
			</button>
		{/each}
		<p class="section">{t('finder.device')}</p>
		{#each devices as d}
			<button class="place" class:active={path === d.path} onclick={() => go(d.path)}>
				<!-- framework7-icons has no drive glyph -->
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<rect x="2.8" y="5.5" width="14.4" height="9" rx="2" fill="none" stroke="currentColor" stroke-width="1.4" />
					<circle cx="14" cy="10" r="1" fill="currentColor" />
				</svg>
				<span>{t(d.key)}</span>
			</button>
		{/each}
	</aside>

	<section
		class="main"
		class:drag-over={drag_over}
		role="region"
		ondragover={(e) => {
			if (is_virtual(path) || !e.dataTransfer?.types.includes('Files')) return;
			e.preventDefault();
			drag_over = true;
		}}
		ondragleave={(e) => !(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node) && (drag_over = false)}
		ondrop={on_drop}
	>
		<Toolbar
			inset={narrow ? 92 : 12}
			title={folder_name}
			subtitle={narrow ? '' : path === TRASH_LOCATION ? t('finder.items', { n: trash_count }) : path === APPS_LOCATION ? '' : t('finder.items', { n: visible.length })}
		>
			{#snippet leading()}
				<ToolButton label="←" onclick={go_back} disabled={!back.length}>
					<Glyph name="chevron_left" size={15} />
				</ToolButton>
				<ToolButton label="→" onclick={go_fwd} disabled={!fwd.length}>
					<Glyph name="chevron_right" size={15} />
				</ToolButton>
			{/snippet}
			{#snippet trailing()}
				{#if path === TRASH_LOCATION}
					<ToolButton label={t('trash.empty_action')} onclick={() => trash_view?.ask_empty()} disabled={!trash_count} text>
						<Glyph name="trash" size={15} />
					</ToolButton>
				{:else if path === APPS_LOCATION}
					<label class="search">
						<Glyph name="search" size={12} />
						<input bind:value={search} placeholder={t('launchpad.search')} spellcheck="false" />
					</label>
				{:else}
				{#if sel && !sel.dir}
					{#if sel.name.toLowerCase().endsWith('.deb')}
						<ToolButton label={t('finder.install')} onclick={() => sel && install_path(sel.path)} text>
							<Glyph name="arrow_down_to_line" size={14} />
						</ToolButton>
					{/if}
					<ToolButton label={t('finder.download')} onclick={() => sel && download(sel)}>
						<Glyph name="arrow_down_to_line" size={15} />
					</ToolButton>
				{/if}
				{#if sel}
					<ToolButton label={t('apps.move_to_trash')} onclick={() => sel && move_to_trash(sel)}>
						<Glyph name="trash" size={15} />
					</ToolButton>
				{/if}
				<ToolButton label={t('finder.new_folder')} onclick={new_folder}>
					<Glyph name="folder_badge_plus" size={16} />
				</ToolButton>
				<ToolButton label={t('finder.upload')} onclick={() => file_input?.click()}>
					<Glyph name="arrow_up_to_line" size={15} />
				</ToolButton>
				<ToolButton label={t('finder.hidden')} pressed={show_hidden} onclick={() => (show_hidden = !show_hidden)}>
					<Glyph name="eye" size={16} />
				</ToolButton>
				{/if}
			{/snippet}
		</Toolbar>

		<input type="file" multiple hidden bind:this={file_input} onchange={(e) => upload([...((e.currentTarget as HTMLInputElement).files ?? [])])} />

		{#if path === APPS_LOCATION}
			<AppsView query={search} />
		{:else if path === TRASH_LOCATION}
			<TrashView bind:this={trash_view} bind:count={trash_count} />
		{:else}
		<div class="header-row">
			<span class="c-name">{t('finder.name')}</span>
			<span class="c-date">{t('finder.modified')}</span>
			<span class="c-size">{t('finder.size')}</span>
		</div>

		{#key path}
			<div class="list" class:loading style:--dir={direction}>
				{#each visible as e (e.path)}
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<div
						class="row"
						class:selected={selected === e.path}
						data-path={e.path}
						role="row"
						tabindex="-1"
						draggable={renaming !== e.path}
						onclick={() => (selected = e.path)}
						ondblclick={() => open(e)}
						oncontextmenu={(ev) => row_menu(ev, e)}
						ondragstart={(ev) => row_drag(ev, e)}
					>
						<span class="c-name">
							<FileGlyph name={e.name} dir={e.dir} />
							{#if renaming === e.path}
								<input
									class="rename"
									bind:value={rename_value}
									onkeydown={(ev) => {
										ev.stopPropagation();
										if (ev.key === 'Enter') commit_rename();
										if (ev.key === 'Escape') renaming = null;
									}}
									onblur={commit_rename}
								/>
							{:else}
								<span class="name">{e.name}</span>
							{/if}
						</span>
						<span class="c-date">{format_date(e.modified)}</span>
						<span class="c-size">{e.dir ? '—' : format_bytes(e.size)}</span>
					</div>
				{:else}
					{#if !loading}
						<p class="empty">{error || t('finder.empty')}</p>
					{/if}
				{/each}
			</div>
		{/key}

		<footer class="pathbar">
			{#each crumbs as c, i}
				{#if i > 0}<span class="sep">›</span>{/if}
				<button onclick={() => go(c)}>{c === '/' ? '/' : c.split('/').pop()}</button>
			{/each}
		</footer>
		{/if}

		{#if drag_over}
			<div class="drop-hint"><span>{t('finder.drop')}</span></div>
		{/if}
		{#if upload_pct !== null}
			<div class="progress">
				<span>{t('finder.uploading', { p: upload_pct })}</span>
				<span class="bar"><span style:width="{upload_pct}%"></span></span>
			</div>
		{/if}
	</section>

	{#if confirm_delete}
		<div class="sheet-backdrop">
			<div class="sheet">
				<FileGlyph name={confirm_delete.name} dir={confirm_delete.dir} size={44} />
				<h3>{t('finder.confirm_delete', { name: confirm_delete.name })}</h3>
				<p>{t('finder.confirm_delete_hint')}</p>
				<div class="buttons">
					<button class="btn" onclick={() => (confirm_delete = null)}>{t('finder.cancel')}</button>
					<button class="btn destructive" onclick={do_delete}>{t('finder.delete')}</button>
				</div>
			</div>
		</div>
	{/if}

	{#if preview}
		<QuickLook entry={preview} on_close={() => (preview = null)} on_download={() => preview && download(preview)} />
	{/if}

</div>

<style>
	.finder {
		position: relative;
		height: 100%;
		min-height: 0;
		display: grid;
		grid-template-columns: 188px 1fr;
		outline: none;
	}

	.search {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 180px;
		height: 28px;
		padding: 0 10px;
		border-radius: 8px;
		background: var(--zd-hover);
		color: var(--zd-text-tertiary);

		input {
			flex: 1;
			min-width: 0;
			border: 0;
			outline: 0;
			background: none;
			font-size: 13px;
			color: var(--zd-text);
		}
	}

	.sidebar {
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 0 10px 12px;
		background: var(--zd-sidebar-bg);
		backdrop-filter: blur(30px);
		box-shadow: inset -0.5px 0 0 var(--zd-hairline-strong);
		overflow-y: auto;
	}

	.sidebar-pad {
		height: var(--zd-titlebar);
		flex: none;
	}

	.section {
		margin: 12px 8px 4px;
		font-size: 11px;
		font-weight: 600;
		color: var(--zd-text-tertiary);
	}

	.place {
		justify-content: flex-start;
		gap: 8px;
		height: 28px;
		padding: 0 8px;
		border-radius: var(--zd-radius-control);
		font-size: 13px;
		color: var(--zd-text);
		transition: background-color 120ms ease;

		svg {
			color: var(--zd-text-secondary);
			flex: none;
		}

		span {
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}

		&:hover {
			background: var(--zd-hover);
		}

		&.active {
			background: var(--zd-selected);
		}
	}

	.main {
		position: relative;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}

	.header-row,
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 170px 84px;
		align-items: center;
		gap: 12px;
		padding: 0 16px;
	}

	.finder.narrow {
		grid-template-columns: minmax(0, 1fr);

		.sidebar,
		.c-date,
		.c-size {
			display: none;
		}

		.header-row,
		.row {
			grid-template-columns: minmax(0, 1fr);
		}
	}

	.header-row {
		height: 28px;
		flex: none;
		font-size: 11px;
		font-weight: 500;
		color: var(--zd-text-secondary);
		box-shadow: 0 0.5px 0 var(--zd-hairline);
	}

	.list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 4px 8px 8px;
		animation: list-in 300ms var(--zd-ease-out);
		transition: opacity 200ms ease;

		&.loading {
			opacity: 0.5;
		}
	}

	.row {
		height: 30px;
		padding: 0 8px;
		border-radius: var(--zd-radius-control);
		font-size: 13px;
		transition: background-color 80ms ease;

		&:nth-child(even) {
			background: hsla(0, 0%, 50%, 0.035);
		}

		&.selected {
			background: var(--system-color-primary);
			color: var(--system-color-primary-contrast);

			.c-date,
			.c-size {
				color: inherit;
				opacity: 0.75;
			}
		}
	}

	.c-name {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}

	.name {
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.c-date,
	.c-size {
		color: var(--zd-text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.c-size {
		text-align: right;
		font-variant-numeric: tabular-nums;
	}

	.rename {
		flex: 1;
		min-width: 0;
		height: 22px;
		padding: 0 6px;
		border: 0;
		border-radius: 4px;
		background: var(--zd-window-bg);
		color: var(--zd-text);
		box-shadow: 0 0 0 2px hsla(var(--system-color-primary-hsl), 0.35);
	}

	.empty {
		padding: 60px 0;
		text-align: center;
		color: var(--zd-text-tertiary);
	}

	.pathbar {
		flex: none;
		display: flex;
		align-items: center;
		gap: 2px;
		height: 28px;
		padding: 0 12px;
		font-size: 11px;
		color: var(--zd-text-secondary);
		box-shadow: 0 -0.5px 0 var(--zd-hairline);
		overflow: hidden;
		white-space: nowrap;

		button {
			padding: 2px 5px;
			border-radius: 4px;

			&:hover {
				background: var(--zd-hover);
				color: var(--zd-text);
			}
		}

		.sep {
			color: var(--zd-text-tertiary);
		}
	}

	.drop-hint {
		position: absolute;
		inset: var(--zd-titlebar) 8px 8px;
		display: grid;
		place-items: center;
		border-radius: var(--zd-radius-panel);
		background: hsla(var(--system-color-primary-hsl), 0.05);
		box-shadow: inset 0 0 0 2px hsla(var(--system-color-primary-hsl), 0.3);
		pointer-events: none;
		animation: fade 160ms ease;

		span {
			padding: 6px 12px;
			border-radius: 8px;
			background: var(--zd-glass);
			backdrop-filter: blur(20px);
			font-weight: 500;
		}
	}

	.progress {
		position: absolute;
		right: 16px;
		bottom: 40px;
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 12px;
		border-radius: var(--zd-radius-panel);
		background: var(--zd-glass);
		backdrop-filter: blur(30px);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 6px 20px hsla(0, 0%, 0%, 0.15);
		font-size: 12px;
		font-variant-numeric: tabular-nums;
		animation: rise 300ms var(--zd-spring);

		.bar {
			width: 120px;
			height: 4px;
			border-radius: 2px;
			background: var(--zd-hover);
			overflow: hidden;

			span {
				display: block;
				height: 100%;
				background: var(--system-color-primary);
				transition: width 200ms ease;
			}
		}
	}

	.sheet-backdrop {
		position: absolute;
		inset: 0;
		z-index: 30;
		display: flex;
		justify-content: center;
		background: hsla(0, 0%, 0%, 0.08);
		animation: fade 160ms ease;
	}

	.sheet {
		margin-top: var(--zd-titlebar);
		width: 280px;
		height: max-content;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		padding: 20px 20px 16px;
		border-radius: 0 0 var(--zd-radius-window) var(--zd-radius-window);
		background: var(--zd-window-bg);
		box-shadow: 0 12px 40px hsla(0, 0%, 0%, 0.25);
		text-align: center;
		animation: sheet-down 340ms var(--zd-spring);

		h3 {
			margin-top: 8px;
			font-size: 13px;
			font-weight: 600;
			word-break: break-all;
		}

		p {
			font-size: 12px;
			color: var(--zd-text-secondary);
		}

		.buttons {
			margin-top: 12px;
			width: 100%;
			display: grid;
			grid-template-columns: 1fr 1fr;
			gap: 8px;
		}
	}

	:global(.finder .btn),
	:global(.zd-btn) {
		height: 28px;
		border-radius: var(--zd-radius-control);
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
		font-size: 13px;
		font-weight: 500;
		transition: transform 200ms var(--zd-spring);

		&:active {
			transform: scale(0.97);
		}
	}

	.btn.destructive {
		background: var(--zd-red);
		color: #fff;
	}

	@keyframes list-in {
		from {
			opacity: 0;
			transform: translateX(calc(var(--dir) * 12px));
		}
	}

	@keyframes fade {
		from {
			opacity: 0;
		}
	}

	@keyframes rise {
		from {
			opacity: 0;
			transform: translateY(8px);
		}
	}

	@keyframes sheet-down {
		from {
			transform: translateY(-100%);
		}
	}
</style>
