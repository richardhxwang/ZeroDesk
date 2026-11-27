<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { ApiError, post } from '🍎/lib/api.ts';
	import { download_blob, stamp } from '🍎/lib/download.ts';
	import type { ScreenStatus } from '🍎/lib/screen.ts';
	import { apps, close_app, open_app, type DeviceWindow } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import ScreenTools from '../common/ScreenTools.svelte';
	import ScreenView from '../common/ScreenView.svelte';
	import Toolbar from '../common/Toolbar.svelte';
	import ToolButton from '../common/ToolButton.svelte';

	// A device app running on the computer: its own copy on a virtual screen,
	// independent of what the Zero itself is showing.
	const { window_id, app }: { window_id: string; app: DeviceWindow } = $props();

	let session = $state<string | null>(null);
	let error = $state('');
	let status = $state<ScreenStatus>('connecting');
	let show_keys = $state(false);
	let view = $state<ScreenView>();
	let ended = $state(false);
	let poll: ReturnType<typeof setInterval> | undefined;

	// The window can close while the app is still starting. A closed window
	// stays mounted through its close animation, and reopening it then brings
	// the same component back, so "closed" is whether it is closed right now.
	let destroyed = false;
	const closed = () => destroyed || !apps.open[window_id];

	onMount(async () => {
		try {
			const s = await post<{ id: string }>('/api/vapps/start', { id: app.app });
			if (destroyed) {
				post('/api/vapps/stop', { id: s.id }).catch(() => {});
				return;
			}
			session = s.id;
			// notice when the app quits by itself
			poll = setInterval(async () => {
				try {
					const list = await (await fetch('/api/vapps')).json();
					const mine = list.find((x: { id: string; exited: boolean }) => x.id === session);
					if (!mine || mine.exited) {
						ended = true;
						clearInterval(poll);
					}
				} catch {
					/* ignore */
				}
			}, 2000);
			requestAnimationFrame(() => view?.focus());
		} catch (e) {
			// apps that cannot run on a virtual screen (administrator, statically
			// linked) open on the Zero itself, in the Cardputer window
			if (e instanceof ApiError && e.status === 409) {
				// closed meanwhile: do not start it on the Zero; reopened, the toolbar's button still can
				if (closed()) error = (e as Error).message;
				else await open_on_device();
				return;
			}
			error = (e as Error).message;
		}
	});

	onDestroy(() => {
		destroyed = true;
		clearInterval(poll);
		if (session) post('/api/vapps/stop', { id: session }).catch(() => {});
	});

	async function screenshot() {
		const blob = await view?.snapshot();
		if (blob) download_blob(blob, `${app.name}-${stamp()}.png`);
	}

	async function open_on_device() {
		try {
			await post('/api/apps/open', { id: app.app });
			// closed meanwhile: the window id may already belong to a new window of this app
			if (closed()) return;
			open_app('device');
			close_app(window_id);
		} catch (e) {
			if (!destroyed) error = (e as Error).message;
		}
	}

	const subtitle = $derived(
		error ? error : ended ? t('vapp.ended') : status === 'live' ? t('vapp.on_computer') : t('device.connecting'),
	);
	// narrow window: icon-only buttons so the title keeps its room
	let win_w = $state(800);
</script>

<div class="device-app" bind:clientWidth={win_w}>
	<Toolbar title={app.name} {subtitle}>
		{#snippet trailing()}
			<ScreenTools bind:show_keys can_capture={status === 'live'} on_screenshot={screenshot} />
			<ToolButton label={t('vapp.open_on_zero')} onclick={open_on_device} text={win_w >= 560}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<rect x="3" y="4.5" width="14" height="11" rx="2.2" fill="none" stroke="currentColor" stroke-width="1.4" />
					<rect x="6" y="7" width="8" height="4.5" rx="1" fill="currentColor" />
				</svg>
			</ToolButton>
		{/snippet}
	</Toolbar>
	{#if session}
		<ScreenView bind:this={view} path="/ws/vapp?id={session}" {show_keys} pointer bind:status />
	{:else}
		<div class="placeholder">{error || t('device.connecting')}</div>
	{/if}
</div>

<style>
	.device-app {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}

	.placeholder {
		flex: 1;
		display: grid;
		place-items: center;
		color: var(--zd-text-secondary);
		font-size: 13px;
		padding: 24px;
		text-align: center;
	}
</style>
