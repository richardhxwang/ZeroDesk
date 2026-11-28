<script lang="ts">
	import { api } from '🍎/lib/api.ts';
	import { IMAGE_EXT } from '🍎/lib/wallpapers.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import { onMount } from 'svelte';

	// A sheet for picking a picture from the CardputerZero's files (the same
	// listing Finder uses). It opens in ~/Pictures, or the home folder when
	// there is none; only folders and images are listed.

	const { on_choose, on_cancel }: { on_choose: (path: string) => void; on_cancel: () => void } = $props();

	type Entry = { name: string; path: string; dir: boolean; hidden?: boolean };

	let path = $state('');
	let entries = $state<Entry[]>([]);
	let selected = $state<string | null>(null);
	let loading = $state(true);
	let error = $state('');

	const parent = $derived(path && path !== '/' ? path.replace(/\/[^/]+\/?$/, '') || '/' : null);
	const title = $derived(path === session.home ? t('finder.home') : path.split('/').pop() || '/');

	async function open(dir: string): Promise<boolean> {
		loading = true;
		try {
			const res = await api<{ path: string; entries: Entry[] }>(`/api/fs/list?path=${encodeURIComponent(dir)}`);
			path = res.path;
			entries = res.entries.filter((e) => !e.hidden && (e.dir || IMAGE_EXT.test(e.name)));
			selected = null;
			error = '';
			return true;
		} catch (e) {
			error = (e as Error).message;
			return false;
		} finally {
			loading = false;
		}
	}

	onMount(async () => {
		if (!(await open(`${session.home}/Pictures`))) await open(session.home);
	});

	function activate(e: Entry) {
		if (e.dir) open(e.path);
		else on_choose(e.path);
	}
</script>

<svelte:window onkeydown={(e) => (e.key === 'Escape' ? on_cancel() : e.key === 'Enter' && selected && on_choose(selected))} />

<div class="backdrop">
	<div class="sheet" role="dialog" aria-label={t('settings.choose_picture')}>
		<header>
			<button class="nav" disabled={!parent} aria-label={t('settings.back')} onclick={() => parent && open(parent)}>
				<i class="f7-icons">chevron_left</i>
			</button>
			<h3>{title}</h3>
		</header>
		<div class="list" role="listbox" aria-label={title}>
			{#each entries as e (e.path)}
				<button
					class="item"
					class:on={selected === e.path}
					role="option"
					aria-selected={selected === e.path}
					onclick={() => (e.dir ? open(e.path) : (selected = e.path))}
					ondblclick={() => activate(e)}
				>
					<i class="f7-icons glyph" class:folder={e.dir}>{e.dir ? 'folder_fill' : 'photo'}</i>
					<span class="name">{e.name}</span>
					{#if e.dir}<i class="f7-icons more">chevron_right</i>{/if}
				</button>
			{:else}
				{#if !loading}<p class="empty">{error || t('settings.no_pictures')}</p>{/if}
			{/each}
		</div>
		<div class="buttons">
			<button class="zd-btn" onclick={on_cancel}>{t('settings.cancel')}</button>
			<button class="zd-btn primary" disabled={!selected} onclick={() => selected && on_choose(selected)}>{t('settings.choose')}</button>
		</div>
	</div>
</div>

<style>
	.backdrop {
		position: absolute;
		inset: 0;
		z-index: 40;
		display: flex;
		justify-content: center;
		background: hsla(0, 0%, 0%, 0.08);
		animation: fade 160ms ease;
	}

	.sheet {
		margin-top: var(--zd-titlebar);
		width: min(340px, calc(100% - 24px));
		height: max-content;
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 12px 16px 16px;
		border-radius: 0 0 var(--zd-radius-window) var(--zd-radius-window);
		background: var(--zd-window-bg);
		box-shadow: 0 12px 40px hsla(0, 0%, 0%, 0.25);
		animation: sheet-down 340ms var(--zd-spring);
	}

	header {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;

		h3 {
			flex: 1;
			min-width: 0;
			font-size: 13px;
			font-weight: 600;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.nav {
		flex: none;
		width: 26px;
		height: 24px;
		display: grid;
		place-items: center;
		border-radius: 6px;
		font-size: 14px;
		color: var(--zd-text-secondary);

		&:hover:not(:disabled) {
			background: var(--zd-hover);
		}
		&:disabled {
			opacity: 0.35;
		}
	}

	.list {
		height: 220px;
		overflow-y: auto;
		scrollbar-width: none;
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 4px;
		border-radius: 8px;
		background: var(--zd-hover);
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline);

		&::-webkit-scrollbar {
			display: none;
		}
	}

	.item {
		flex: none;
		display: flex;
		align-items: center;
		gap: 8px;
		height: 28px;
		padding: 0 8px;
		border-radius: 6px;
		font-size: 13px;
		text-align: left;

		&.on {
			background: var(--zd-selected);
		}

		.glyph {
			flex: none;
			font-size: 15px;
			color: var(--zd-text-secondary);

			&.folder {
				color: var(--zd-text-tertiary);
			}
		}

		.name {
			flex: 1;
			min-width: 0;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}

		.more {
			flex: none;
			font-size: 11px;
			color: var(--zd-text-tertiary);
		}
	}

	.empty {
		margin: auto;
		font-size: 12px;
		color: var(--zd-text-tertiary);
	}

	.buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}

	.zd-btn {
		height: 28px;
		border-radius: var(--zd-radius-control);
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
		font-size: 13px;
		font-weight: 500;
		transition: transform 200ms var(--zd-spring);

		&:active:not(:disabled) {
			transform: scale(0.97);
		}

		&:disabled {
			opacity: 0.6;
		}

		&.primary {
			background: var(--system-color-primary);
			color: var(--system-color-primary-contrast);
		}
	}

	@keyframes fade {
		from {
			opacity: 0;
		}
	}

	@keyframes sheet-down {
		from {
			transform: translateY(-100%);
		}
	}
</style>
