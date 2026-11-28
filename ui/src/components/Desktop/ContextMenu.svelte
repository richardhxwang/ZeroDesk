<script lang="ts">
	import { elevation } from '🍎/actions';
	import Menu from '🍎/components/TopBar/Menu.svelte';
	import { language_menu, type MenuItem } from '🍎/lib/menus.ts';
	import { open_app } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { preferences, toggle_scheme } from '🍎/state/preferences.svelte.ts';

	const { target_element }: { target_element?: HTMLElement } = $props();

	let x = $state(0);
	let y = $state(0);
	let visible = $state(false);

	const items: MenuItem[] = $derived([
		{ label: t('app.finder'), action: () => open_app('finder') },
		{ label: t('app.terminal'), action: () => open_app('terminal') },
		{ label: t('app.launchpad'), action: () => open_app('launchpad'), break_after: true },
		{ label: preferences.theme.scheme === 'dark' ? t('menu.light') : t('menu.dark'), action: toggle_scheme },
		language_menu(true),
		{ label: t('menu.settings'), action: () => open_app('settings') },
	]);

	function on_context(e: MouseEvent) {
		const target = e.target as HTMLElement;
		// Only the empty desktop gets our menu; inside windows the browser's own menu is kept.
		if (!target_element?.contains(target) || target.closest('.window, header, .dock-container')) {
			visible = false;
			return;
		}
		e.preventDefault();
		x = Math.min(e.clientX, innerWidth - 240);
		y = Math.min(e.clientY, innerHeight - 220);
		visible = true;
	}
</script>

<svelte:body oncontextmenu={on_context} onclick={() => (visible = false)} />

{#if visible}
	<div class="container" style:transform="translate({x}px, {y}px)" use:elevation={'context-menu'}>
		<Menu {items} />
	</div>
{/if}

<style>
	.container {
		position: fixed;
		top: 0;
		left: 0;
	}
</style>
