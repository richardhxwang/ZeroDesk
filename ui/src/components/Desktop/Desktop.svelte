<script lang="ts">
	import { onMount } from 'svelte';
	import { load_web_apps } from '🍎/state/apps.svelte.ts';
	import { preload_everything } from '🍎/state/app_preload.ts';
	import { check_session, session } from '🍎/state/session.svelte.ts';
	import Dock from '../Dock/Dock.svelte';
	import Launchpad from '../Launchpad/Launchpad.svelte';
	import Banners from '../SystemUI/Banners.svelte';
	import DropToInstall from '../SystemUI/DropToInstall.svelte';
	import Installer from '../SystemUI/Installer.svelte';
	import PasswordSheet from '../SystemUI/PasswordSheet.svelte';
	import PowerAlert from '../SystemUI/PowerAlert.svelte';
	import Login from '../SystemUI/Login.svelte';
	import TopBar from '../TopBar/TopBar.svelte';
	import ContextMenu from './ContextMenu.svelte';
	import ContextMenuHost from './ContextMenuHost.svelte';
	import Wallpaper from './Wallpaper.svelte';
	import PhoneShell from '../Phone/PhoneShell.svelte';
	import { phone } from '🍎/state/phone.svelte.ts';
	import WindowsArea from './Window/WindowsArea.svelte';

	let main_el = $state<HTMLElement>();

	onMount(check_session);
	$effect(() => {
		if (session.authed) {
			load_web_apps();
			preload_everything();
		}
	});
</script>

<div bind:this={main_el} class="container">
	<Wallpaper />
	{#if session.authed}
		{#if phone.active}
			<PhoneShell />
		{:else}
			<main>
				<TopBar />
				<WindowsArea />
				<Launchpad />
				<Dock />
			</main>
		{/if}
		<Banners />
		<DropToInstall />
		<Installer />
		<PasswordSheet />
		<PowerAlert />
		<ContextMenu target_element={main_el} />
		<ContextMenuHost />
	{:else if session.authed === false}
		<Login />
	{/if}
</div>

<style>
	.container {
		height: 100%;
		width: 100%;
	}

	main {
		height: 100%;
		width: 100%;
		display: grid;
		grid-template-rows: auto 1fr auto;
	}
</style>
