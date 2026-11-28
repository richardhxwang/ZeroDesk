<script lang="ts">
	import { api, api_error } from '🍎/lib/api.ts';
	import { revalidate } from '🍎/lib/swr.svelte.ts';
	import { install_progress, install_step } from '🍎/lib/install_progress.ts';
	import { data } from '🍎/state/app_preload.ts';
	import { as_admin, Cancelled } from '🍎/state/admin.svelte.ts';
	import { format_bytes, t } from '🍎/state/i18n.svelte.ts';
	import { close_installer, installer } from '🍎/state/installer.svelte.ts';
	import { notify } from '🍎/state/notifications.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import FileGlyph from '../apps/Finder/FileGlyph.svelte';
	import AppIcon from '../SVG/AppIcon.svelte';

	// A .deb opens like a disk image on a Mac (support.apple.com/guide/mac-help/mh35835):
	// "Opening…" while it is copied over and read, then a plain window with
	// the app and an Applications alias side by side. Dragging the app onto
	// Applications installs it: the authorization panel, then Finder's copy
	// progress. A package already installed asks before replacing it, like
	// Finder does for an item that already exists.

	type Info = {
		package: string;
		name: string;
		version: string;
		description: string;
		size_kb: number;
		installed?: string;
		has_icon: boolean;
	};
	type Phase = 'opening' | 'ready' | 'replace' | 'installing' | 'restarting' | 'error';

	let phase = $state<Phase>('opening');
	let path = $state('');
	let info = $state<Info | null>(null);
	let pct = $state<number | null>(null);
	let error = $state('');
	let log = $state('');
	let progress = $state<number | null>(null);
	let authorizing = $state(false);
	/** the device took the password; the install itself has not printed yet */
	let accepted = $state(false);

	// dragging the app icon
	let drag = $state<{ x: number; y: number; dx: number; dy: number } | null>(null);
	let over_apps = $state(false);
	let returning = $state(false);
	let app_el = $state<HTMLElement>();
	let apps_el = $state<HTMLElement>();

	// window position (the title bar drags it)
	let pos = $state({ x: 0, y: 0 });
	let move: { x: number; y: number } | null = null;

	const file_name = $derived(installer.file?.name ?? path.split('/').pop() ?? '');
	const title = $derived(info?.name ?? file_name.replace(/\.deb$/i, ''));
	const last_line = $derived(
		log
			.split('\n')
			.map((l) => l.trim())
			.filter((l) => l && !l.startsWith('[exit]') && !/^((pm|dl)status|status|processing):/.test(l))
			.pop() ?? '',
	);

	$effect(() => {
		if (installer.open) open();
	});

	async function open() {
		info = null;
		error = '';
		log = '';
		pos = { x: 0, y: 0 };
		phase = 'opening';
		pct = null;
		try {
			path = installer.file ? await copy(installer.file) : (installer.path ?? '');
			pct = null;
			info = await api<Info>(`/api/deb/info?path=${encodeURIComponent(path)}`);
			phase = 'ready';
		} catch {
			fail(t('installer.not_package', { name: file_name }));
		}
	}

	/** Copy a dropped file into ~/Downloads on the Zero, with progress. */
	function copy(file: File): Promise<string> {
		return new Promise((resolve, reject) => {
			const fd = new FormData();
			fd.append('file', file, file.name);
			const xhr = new XMLHttpRequest();
			pct = 0;
			xhr.upload.onprogress = (e) => e.lengthComputable && (pct = e.loaded / e.total);
			xhr.onloadend = () => {
				if (xhr.status !== 200) return reject(new Error(xhr.responseText || `HTTP ${xhr.status}`));
				const saved = (JSON.parse(xhr.responseText).saved ?? []) as string[];
				saved[0] ? resolve(saved[0]) : reject(new Error('upload failed'));
			};
			xhr.open('POST', `/api/fs/upload?dir=${encodeURIComponent(`${session.home}/Downloads`)}`);
			xhr.send(fd);
		});
	}

	function fail(msg: string) {
		phase = 'error';
		error = msg;
	}

	function dropped_on_apps() {
		if (info?.installed) phase = 'replace';
		else install();
	}

	async function install() {
		// the progress panel appears once the password is accepted, as on a Mac
		phase = 'ready';
		authorizing = true;
		log = '';
		progress = null;
		try {
			const res = await as_admin(
				async (password) => {
					// the moment the password is entered, the progress panel takes over
					if (password) {
						phase = 'installing';
						accepted = false;
					}
					const r = await fetch('/api/deb/install', {
						method: 'POST',
						headers: { 'Content-Type': 'application/json' },
						body: JSON.stringify({ path, password }),
					});
					if (!r.ok) {
						phase = 'ready';
						throw await api_error(r);
					}
					phase = 'installing';
					accepted = true;
					return r;
				},
				{ title: t('admin.install_title'), action: t('admin.install_ok') },
			);
			const reader = res.body!.getReader();
			const dec = new TextDecoder();
			for (;;) {
				const { value, done } = await reader.read();
				if (done) break;
				log += dec.decode(value, { stream: true });
				// apt's status lines: dependencies downloading, then dpkg's steps
				progress = install_progress(log, false);
			}
		} catch (e) {
			authorizing = false;
			if (e instanceof Cancelled) {
				phase = 'ready';
				return;
			}
			log += `\n${(e as Error).message}`;
		}
		authorizing = false;
		if (!/\[exit\] 0\s*$/.test(log)) return fail(last_line || t('installer.error_title'));
		progress = 1;
		// ZeroDesk updating itself restarts a few seconds after dpkg
		if (info?.package === 'zerodesk-cp0') return restart();
		notify('finder', title, t('installer.done', { name: title }));
		// the new app shows up in Applications and Launchpad
		revalidate('apps', data.apps).catch(() => {});
		window.dispatchEvent(new Event('zerodesk-apps-changed'));
		info = info && { ...info, installed: info.version };
		phase = 'ready';
	}

	async function restart() {
		phase = 'restarting';
		await new Promise((r) => setTimeout(r, 7000));
		for (let i = 0; i < 60; i++) {
			try {
				if ((await fetch('/api/session', { cache: 'no-store' })).ok) return location.reload();
			} catch {
				/* not up yet */
			}
			await new Promise((r) => setTimeout(r, 1000));
		}
		location.reload();
	}

	// --- dragging the app onto Applications ---------------------------------

	function hit(el: HTMLElement | undefined, x: number, y: number) {
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return x >= r.left - 8 && x <= r.right + 8 && y >= r.top - 8 && y <= r.bottom + 8;
	}

	function drag_start(e: PointerEvent) {
		if (phase !== 'ready' || authorizing || e.button !== 0) return;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		drag = { x: e.clientX, y: e.clientY, dx: 0, dy: 0 };
		returning = false;
	}

	function drag_move(e: PointerEvent) {
		if (!drag) return;
		drag = { ...drag, dx: e.clientX - drag.x, dy: e.clientY - drag.y };
		over_apps = hit(apps_el, e.clientX, e.clientY);
	}

	function drag_end(e: PointerEvent) {
		if (!drag) return;
		const moved = Math.hypot(drag.dx, drag.dy) > 4;
		const on_apps = over_apps;
		over_apps = false;
		if (moved && on_apps) {
			drag = null;
			dropped_on_apps();
			return;
		}
		// not dropped on Applications: slide back, like Finder
		returning = true;
		drag = drag && { ...drag, dx: 0, dy: 0 };
		setTimeout(() => ((drag = null), (returning = false)), 260);
	}

	function key(e: KeyboardEvent) {
		if (!installer.open) return;
		if (e.key === 'Escape' && (phase === 'ready' || phase === 'error')) close_installer();
	}
</script>

<svelte:window
	onkeydown={key}
	onpointermove={(e) => move && (pos = { x: pos.x + e.movementX, y: pos.y + e.movementY })}
	onpointerup={() => (move = null)}
/>

{#if installer.open}
	<div class="layer">
		<section class="window" style:translate="{pos.x}px {pos.y}px" role="dialog" aria-label={title}>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<header onpointerdown={(e) => e.button === 0 && (move = { x: e.clientX, y: e.clientY })}>
				<div class="lights">
					<button class="close" aria-label={t('installer.stop')} disabled={phase === 'installing' || phase === 'restarting'} onclick={close_installer}></button>
					<span></span>
					<span></span>
				</div>
				<h2><FileGlyph name="x.deb" dir={false} size={15} /><span>{title}</span></h2>
			</header>

			<div class="content" class:dim={phase !== 'ready'}>
				{#if phase !== 'opening'}
					<div class="item">
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							bind:this={app_el}
							class="drag"
							class:dragging={!!drag && !returning}
							class:returning
							style:translate={drag ? `${drag.dx}px ${drag.dy}px` : null}
							onpointerdown={drag_start}
							onpointermove={drag_move}
							onpointerup={drag_end}
							onpointercancel={drag_end}
						>
							{#if info?.has_icon}
								<img src="/api/deb/icon?path={encodeURIComponent(path)}" alt="" width="96" height="96" draggable="false" />
							{:else}
								<FileGlyph name={file_name} dir={false} size={96} />
							{/if}
						</div>
						<span class="label">{title}</span>
						{#if info}<span class="sub">{t('installer.version', { v: info.version })}{info.size_kb ? ` · ${format_bytes(info.size_kb * 1024)}` : ''}</span>{/if}
						<!-- where the package came from: a local file is not from the App Store -->
						<span class="sub source" title={installer.file ? file_name : path}>{installer.file ? t('installer.from_computer', { name: file_name }) : t('installer.from_device', { path: path.replace(/^\/home\/[^/]+/, '~') })}</span>
					</div>

					<svg class="arrow" viewBox="0 0 64 16" width="64" height="16" aria-hidden="true">
						<path d="M2 8h56m-8-6 8 6-8 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
					</svg>

					<div class="item">
						<div bind:this={apps_el} class="target" class:over={over_apps}>
							<AppIcon id="launchpad" size={96} />
							<span class="alias" aria-hidden="true">
								<svg viewBox="0 0 12 12" width="12" height="12"><path d="M3.5 8.5 8 4m0 0H4.6M8 4v3.4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
							</span>
						</div>
						<span class="label">{t('installer.applications')}</span>
						<span class="sub">&nbsp;</span>
					</div>
				{/if}
			</div>

			{#if phase === 'opening'}
				<div class="panel">
					<FileGlyph name={file_name} dir={false} size={32} />
					<div class="col">
						<p>{t('installer.opening', { name: file_name })}</p>
						<div class="bar" class:busy={pct === null}><span style:width={pct === null ? null : `${pct * 100}%`}></span></div>
					</div>
				</div>
			{:else if phase === 'installing' || phase === 'restarting'}
				<div class="panel">
					{#if info?.has_icon}
						<img src="/api/deb/icon?path={encodeURIComponent(path)}" alt="" width="32" height="32" />
					{:else}
						<FileGlyph name={file_name} dir={false} size={32} />
					{/if}
					<div class="col">
						<p>{phase === 'restarting' ? t('installer.restarting') : t('installer.installing', { name: title })}</p>
						<div class="bar" class:busy={phase === 'restarting' || progress === null}><span style:width="{(progress ?? 0) * 100}%"></span></div>
						<small>{phase === 'restarting' ? '' : install_step(log) || last_line || t(accepted ? 'installer.preparing' : 'installer.verifying')}</small>
					</div>
				</div>
			{:else if phase === 'replace' && info}
				<div class="panel alert">
					<div class="col">
						<p><b>{t('installer.replace_title', { name: title, v: info.installed ?? '' })}</b></p>
						<p class="muted">{t('installer.replace_body', { v: info.version })}</p>
						<div class="buttons">
							<button class="zd-btn" onclick={() => (phase = 'ready')}>{t('installer.stop')}</button>
							<button class="zd-btn primary" onclick={install}>{t('installer.replace')}</button>
						</div>
					</div>
				</div>
			{:else if phase === 'error'}
				<div class="panel alert">
					<div class="col">
						<p><b>{t('installer.error_title')}</b></p>
						<p class="muted">{error}</p>
						<div class="buttons">
							<button class="zd-btn primary" onclick={() => (info ? (phase = 'ready') : close_installer())}>{t('installer.ok')}</button>
						</div>
					</div>
				</div>
			{/if}
		</section>
	</div>
{/if}

<style>
	.layer {
		position: fixed;
		inset: 0;
		z-index: 9400;
		display: grid;
		place-items: center;
		padding: 16px;
		pointer-events: none;
	}

	.window {
		position: relative;
		width: min(560px, 100%);
		border-radius: var(--zd-radius-window, 24px);
		background: var(--zd-window-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 22px 70px hsla(0, 0%, 0%, 0.3);
		pointer-events: auto;
		animation: open 240ms var(--zd-spring);
		overflow: hidden;
	}

	header {
		position: relative;
		display: flex;
		align-items: center;
		height: 40px;
		padding: 0 16px;
		cursor: default;
		user-select: none;
		touch-action: none;
	}

	.lights {
		display: flex;
		gap: 8px;

		button,
		span {
			width: 12px;
			height: 12px;
			border-radius: 50%;
			background: var(--zd-hover);
			box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
		}

		.close {
			background: #ff5f57;
			box-shadow: inset 0 0 0 0.5px hsla(0, 0%, 0%, 0.15);

			&:disabled {
				background: var(--zd-hover);
				box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			}
		}
	}

	h2 {
		position: absolute;
		left: 50%;
		translate: -50% 0;
		display: flex;
		align-items: center;
		gap: 6px;
		max-width: calc(100% - 140px);
		margin: 0;
		font-size: 13px;
		font-weight: 700;

		span {
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.content {
		display: flex;
		align-items: flex-start;
		justify-content: center;
		gap: 36px;
		min-height: 220px;
		padding: 32px 32px 40px;
		transition: opacity 200ms ease;

		&.dim {
			opacity: 0.45;
		}
	}

	.item {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		width: 150px;
	}

	.drag,
	.target {
		display: grid;
		place-items: center;
		width: 112px;
		height: 112px;
		border-radius: 22px;
	}

	.drag {
		cursor: grab;
		touch-action: none;
		position: relative;
		z-index: 2;

		img {
			width: 96px;
			height: 96px;
			border-radius: 21px;
			pointer-events: none;
		}

		&.dragging {
			cursor: grabbing;
			opacity: 0.85;
			scale: 1.04;
		}

		&.returning {
			transition: translate 240ms var(--zd-spring);
		}
	}

	.target {
		position: relative;
		transition:
			background 120ms ease,
			scale 200ms var(--zd-spring);

		&.over {
			background: var(--zd-selection, var(--zd-hover));
			scale: 1.06;
		}
	}

	.alias {
		position: absolute;
		left: 10px;
		bottom: 10px;
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
		border-radius: 5px;
		color: #1d1d1f;
		background: #fff;
		box-shadow: 0 0 0 0.5px hsla(0, 0%, 0%, 0.25);
	}

	.arrow {
		margin-top: 48px;
		color: var(--zd-text-tertiary);
	}

	.label {
		max-width: 100%;
		font-size: 13px;
		font-weight: 500;
		text-align: center;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.sub {
		font-size: 11px;
		color: var(--zd-text-secondary);
		white-space: nowrap;
	}

	.source {
		max-width: 200px;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	/* Finder's progress and alert panels sit over the window's content */
	.panel {
		position: absolute;
		left: 50%;
		top: 50%;
		translate: -50% -38%;
		display: flex;
		align-items: flex-start;
		gap: 14px;
		width: min(400px, calc(100% - 40px));
		padding: 18px 20px;
		border-radius: 20px;
		background: var(--zd-window-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 14px 44px hsla(0, 0%, 0%, 0.25);
		animation: open 220ms var(--zd-spring);

		img {
			border-radius: 7px;
		}

		&.alert {
			text-align: left;
		}
	}

	.col {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;

		p {
			margin: 0;
			font-size: 13px;
			line-height: 1.4;
		}

		.muted {
			font-size: 12px;
			color: var(--zd-text-secondary);
		}

		small {
			min-height: 14px;
			font-size: 11px;
			color: var(--zd-text-secondary);
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.bar {
		height: 6px;
		border-radius: 3px;
		background: var(--zd-hover);
		overflow: hidden;

		span {
			display: block;
			height: 100%;
			border-radius: 3px;
			background: var(--system-color-primary);
			transition: width 300ms ease;
		}

		&.busy span {
			width: 35%;
			animation: indeterminate 1.1s ease-in-out infinite;
		}
	}

	.buttons {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
		margin-top: 6px;
	}

	.zd-btn {
		height: 28px;
		min-width: 76px;
		padding: 0 14px;
		border-radius: 14px;
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
		font-size: 13px;
		font-weight: 500;
		color: var(--zd-text);
		white-space: nowrap;

		&.primary {
			background: var(--system-color-primary);
			color: var(--system-color-primary-contrast);
		}
	}

	@keyframes indeterminate {
		from {
			transform: translateX(-100%);
		}
		to {
			transform: translateX(290%);
		}
	}

	@keyframes open {
		from {
			opacity: 0;
			scale: 0.96;
		}
	}
</style>
