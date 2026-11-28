<script lang="ts">
	import type { MenuItem } from '🍎/lib/menus.ts';
	import { menubar_state } from '🍎/state/menubar.svelte.ts';
	import Menu from './Menu.svelte';

	const { items, on_pick }: { items: MenuItem[]; on_pick?: () => void } = $props();

	/** the item whose submenu is open */
	let sub = $state<number | null>(null);
	/** the submenu opens to the left when there is no room on the right */
	let flip = $state(false);
	const checks = $derived(items.some((i) => i.checked !== undefined));

	function run(item: MenuItem, i: number) {
		if (item.disabled) return;
		if (item.submenu) {
			sub = sub === i ? null : i; // a click opens it too (touch screens have no hover)
			return;
		}
		menubar_state.active = '';
		on_pick?.();
		item.action?.();
	}

	function hover(e: MouseEvent, item: MenuItem, i: number) {
		sub = item.submenu && !item.disabled ? i : null;
		if (sub !== null) {
			const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
			flip = r.right + 220 > innerWidth;
		}
	}
</script>

<section class="menu" role="menu">
	{#each items as item, i}
		<div class="row">
			<button
				class="item"
				class:open={sub === i}
				role="menuitem"
				aria-haspopup={item.submenu ? 'menu' : undefined}
				aria-expanded={item.submenu ? sub === i : undefined}
				disabled={item.disabled}
				onclick={() => run(item, i)}
				onmouseenter={(e) => hover(e, item, i)}
			>
				{#if checks}
					<span class="check" aria-hidden="true">
						{#if item.checked}<svg viewBox="0 0 12 12" width="10" height="10"><path d="M2 6.5 5 9.5 10 3" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>{/if}
					</span>
				{/if}
				<span class="label">{item.label}</span>
				{#if item.shortcut}<span class="shortcut">{item.shortcut}</span>{/if}
				{#if item.submenu}
					<svg class="chevron" viewBox="0 0 8 12" width="6" height="9" aria-hidden="true"><path d="M2 1.5 6.5 6 2 10.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" /></svg>
				{/if}
			</button>
			{#if item.submenu && sub === i}
				<div class="submenu" class:flip>
					<Menu items={item.submenu} {on_pick} />
				</div>
			{/if}
		</div>
		{#if item.break_after}
			<div class="divider"></div>
		{/if}
	{/each}
</section>

<style>
	.menu {
		min-width: 220px;
		width: max-content;
		padding: 5px;
		border-radius: var(--zd-radius-panel);
		background: var(--zd-glass);
		backdrop-filter: blur(40px) saturate(1.8);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 0 0 0.5px hsla(0, 0%, 0%, 0.12),
			0 10px 30px hsla(0, 0%, 0%, 0.18);
		animation: menu-in 160ms var(--zd-ease-out);
		transform-origin: top left;
	}

	.row {
		position: relative;
	}

	.submenu {
		position: absolute;
		top: -5px;
		left: calc(100% + 4px);

		&.flip {
			left: auto;
			right: calc(100% + 4px);
		}
	}

	/* a submenu is as wide as its items, like macOS */
	.submenu > :global(.menu) {
		min-width: 140px;
	}

	.item {
		display: flex;
		align-items: center;
		gap: 28px;
		width: 100%;
		height: 24px;
		padding: 0 10px;
		border-radius: var(--zd-radius-control);
		font-size: 13px;
		color: var(--zd-text);
		white-space: nowrap;

		&:disabled {
			color: var(--zd-text-tertiary);
		}

		&:not(:disabled):hover,
		&.open {
			background: var(--system-color-primary);
			color: var(--system-color-primary-contrast);

			.shortcut {
				color: inherit;
				opacity: 0.8;
			}
		}
	}

	.label {
		flex: 1;
		text-align: left;
	}

	.check {
		flex: none;
		width: 10px;
		margin-right: -20px; /* sits in the gap before the label */
		display: grid;
		place-items: center;
	}

	.chevron {
		flex: none;
		opacity: 0.7;
	}

	.shortcut {
		color: var(--zd-text-tertiary);
		letter-spacing: 0.06em;
	}

	.divider {
		height: 1px;
		margin: 5px 10px;
		background: var(--zd-hairline-strong);
	}

	@keyframes menu-in {
		from {
			opacity: 0;
			transform: translateY(-4px) scale(0.98);
		}
	}
</style>
