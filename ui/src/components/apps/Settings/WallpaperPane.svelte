<script lang="ts">
	import { show_context_menu } from '🍎/state/contextmenu.svelte.ts';
	import { t, type MsgKey } from '🍎/state/i18n.svelte.ts';
	import { preferences, WALLPAPER_TONES, type Wallpaper, type WallpaperTone } from '🍎/state/preferences.svelte.ts';
	import { custom_url, DEVICE_WALLPAPER, fetch_device_file, remove_custom_wallpaper, set_url, use_as_wallpaper, WALLPAPER_SETS } from '🍎/lib/wallpapers.ts';
	import { onMount, type Snippet } from 'svelte';
	import DevicePicturePicker from './DevicePicturePicker.svelte';

	// Settings › Appearance › Wallpaper, after macOS's Wallpaper pane: the
	// built-in pictures (each with a light and a dark version, shown split on a
	// slant), the device's own wallpaper, the user's picture, and plain colours.
	// "Add Photo…" takes a picture from this computer or from the device.

	const TONES = Object.keys(WALLPAPER_TONES) as WallpaperTone[];
	const dark = $derived(preferences.theme.scheme === 'dark');

	let has_custom = $state(false);
	let busy = $state(false);
	let error = $state('');
	let picking = $state(false);
	let file_input = $state<HTMLInputElement>();

	onMount(async () => {
		try {
			const res = await fetch(custom_url(preferences.wallpaper_rev), { method: 'HEAD', credentials: 'same-origin' });
			has_custom = res.ok;
		} catch {
			has_custom = false;
		}
		if (!has_custom && preferences.wallpaper === 'custom') preferences.wallpaper = 'device';
	});

	function choose(w: Wallpaper) {
		preferences.wallpaper = w;
	}

	async function use_picture(source: () => Promise<Blob>) {
		busy = true;
		error = '';
		try {
			await use_as_wallpaper(await source());
			has_custom = true;
		} catch {
			error = t('settings.wallpaper_failed');
		}
		busy = false;
	}

	function add_menu(e: MouseEvent) {
		const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
		show_context_menu(new MouseEvent('contextmenu', { clientX: r.left, clientY: r.bottom + 4 }), [
			{ label: t('settings.from_computer'), action: () => file_input?.click() },
			{ label: t('settings.from_device'), action: () => (picking = true) },
		]);
	}

	function custom_menu(e: MouseEvent) {
		show_context_menu(e, [{ label: t('settings.remove'), action: remove }]);
	}

	async function remove() {
		try {
			await remove_custom_wallpaper();
			has_custom = false;
			if (preferences.wallpaper === 'custom') preferences.wallpaper = 'device';
		} catch (e) {
			error = (e as Error).message;
		}
	}
</script>

{#snippet thumb(w: Wallpaper, label: string, art: Snippet)}
	<button class="pic" class:on={preferences.wallpaper === w} onclick={() => choose(w)} oncontextmenu={w === 'custom' ? custom_menu : undefined}>
		<span class="frame">{@render art()}</span>
		<small>{label}</small>
	</button>
{/snippet}

<h2>
	<span class="grow">{t('settings.wallpaper')}</span>
	<button class="btn" disabled={busy} onclick={add_menu}>
		{#if busy}<span class="spinner"></span>{/if}{t('settings.add_photo')}
	</button>
</h2>
<div class="pics">
	{#snippet device_art()}<span class="half whole" style:background-image="url({DEVICE_WALLPAPER})"></span>{/snippet}
	{@render thumb('device', t('settings.wallpaper_device'), device_art)}
	{#if has_custom}
		{#snippet custom_art()}<span class="half whole" style:background-image="url({custom_url(preferences.wallpaper_rev)})"></span>{/snippet}
		{@render thumb('custom', t('settings.wallpaper_custom'), custom_art)}
	{/if}
	{#each WALLPAPER_SETS as id (id)}
		{#snippet set_art()}
			<span class="half light" style:background-image="url({set_url(id, 'light')})"></span>
			<span class="half dark" style:background-image="url({set_url(id, 'dark')})"></span>
		{/snippet}
		{@render thumb(id, t(`settings.wall_${id}` as MsgKey), set_art)}
	{/each}
</div>
{#if error}<p class="foot err">{error}</p>{/if}

<h2>{t('settings.wallpaper_colors')}</h2>
<div class="walls">
	{#each TONES as w (w)}
		<button class="wall" class:on={preferences.wallpaper === w} onclick={() => choose(w)}>
			<span class="chip" style:background={WALLPAPER_TONES[w][dark ? 1 : 0]}></span>
			<small>{t(`settings.tone_${w}` as MsgKey)}</small>
		</button>
	{/each}
</div>

<input
	type="file"
	accept="image/jpeg,image/png,image/webp,image/gif,image/avif,image/bmp"
	hidden
	bind:this={file_input}
	onchange={(e) => {
		const f = e.currentTarget.files?.[0];
		e.currentTarget.value = '';
		if (f) use_picture(async () => f);
	}}
/>

{#if picking}
	<DevicePicturePicker
		on_cancel={() => (picking = false)}
		on_choose={(path) => {
			picking = false;
			use_picture(() => fetch_device_file(path));
		}}
	/>
{/if}

<style>
	h2,
	.pics,
	.walls,
	.foot {
		flex: none;
	}

	h2 {
		display: flex;
		align-items: center;
		gap: 12px;
		margin: 6px 4px -4px;
		font-size: 13px;
		font-weight: 600;

		.grow {
			flex: 1;
			min-width: 0;
		}
	}

	.btn {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 24px;
		padding: 0 12px;
		border-radius: 7px;
		background: var(--zd-control-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 0.5px 1.5px hsla(0, 0%, 0%, 0.12);
		font-size: 13px;
		font-weight: 400;
		white-space: nowrap;
		color: var(--zd-text);

		&:disabled {
			opacity: 0.5;
		}
	}

	.spinner {
		width: 11px;
		height: 11px;
		border-radius: 50%;
		border: 1.5px solid var(--zd-hairline-strong);
		border-top-color: var(--zd-text);
		animation: spin 800ms linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.pics {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
		gap: 14px 12px;
		padding: 4px 2px;
	}

	.pic {
		display: flex;
		flex-direction: column;
		justify-content: flex-start;
		align-items: stretch;
		gap: 6px;
		min-width: 0;
		padding: 0;

		.frame {
			position: relative;
			aspect-ratio: 16 / 10;
			border-radius: 9px;
			overflow: hidden;
			background: var(--zd-wallpaper);
			box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			transition: box-shadow 180ms ease;

			/* the hairline sits above the picture */
			&::after {
				content: '';
				position: absolute;
				inset: 0;
				border-radius: inherit;
				box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			}
		}

		.half {
			position: absolute;
			inset: 0;
			background-size: cover;
			background-position: center;

			&.light {
				clip-path: polygon(0 0, 64% 0, 40% 100%, 0 100%);
			}
			&.dark {
				clip-path: polygon(64% 0, 100% 0, 100% 100%, 40% 100%);
			}
		}

		&.on .frame {
			box-shadow:
				0 0 0 2px var(--zd-window-bg),
				0 0 0 4px var(--zd-text);
		}

		small {
			font-size: 11px;
			line-height: 1.25;
			text-align: center;
			overflow-wrap: anywhere;
			color: var(--zd-text-secondary);
		}
	}

	.walls {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(46px, 1fr));
		gap: 12px 8px;
		padding: 4px 2px;
	}

	.wall {
		display: flex;
		flex-direction: column;
		justify-content: flex-start;
		align-items: center;
		gap: 6px;
		padding: 0;
		min-width: 0;

		.chip {
			width: 100%;
			aspect-ratio: 16 / 10;
			border-radius: 8px;
			box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			transition: box-shadow 180ms ease;
		}

		&.on .chip {
			box-shadow:
				0 0 0 2px var(--zd-window-bg),
				0 0 0 4px var(--zd-text);
		}

		/* longer names (グラファイト) wrap rather than being cut off */
		small {
			max-width: 100%;
			font-size: 11px;
			line-height: 1.25;
			text-align: center;
			overflow-wrap: anywhere;
			color: var(--zd-text-secondary);
		}
	}

	.foot {
		margin: -4px 4px 0;
		font-size: 11px;
		color: var(--zd-text-tertiary);

		&.err {
			color: var(--zd-red);
		}
	}
</style>
