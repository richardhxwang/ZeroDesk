<script lang="ts">
	import { elevation } from '🍎/actions';
	import { custom_url, DEVICE_WALLPAPER, is_set, set_url } from '🍎/lib/wallpapers.ts';
	import { preferences, WALLPAPER_TONES, type WallpaperTone } from '🍎/state/preferences.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import { untrack } from 'svelte';
	import { fade } from 'svelte/transition';

	// The desktop picture: the device's own Linux desktop wallpaper (served by
	// the agent), a built-in picture that follows light and dark, the user's
	// own picture, or a plain tone. A new picture is loaded off-screen first
	// and then fades in over the old one; until anything has loaded (or if it
	// fails) the tone shows.

	const scheme = $derived(preferences.theme.scheme === 'dark' ? 'dark' : 'light');
	const w = $derived(preferences.wallpaper);
	const is_tone = $derived(w in WALLPAPER_TONES);
	const tone = $derived(is_tone ? WALLPAPER_TONES[w as WallpaperTone][scheme === 'dark' ? 1 : 0] : undefined);
	/** the picture wanted now; undefined for a tone */
	const want = $derived.by(() => {
		if (!session.authed || is_tone) return undefined;
		if (is_set(w)) return set_url(w, scheme);
		if (w === 'custom') return custom_url(preferences.wallpaper_rev);
		return DEVICE_WALLPAPER;
	});
	/** built-in pictures have their own dark version and are not dimmed */
	const dim = $derived(!is_tone && !is_set(w));

	/** the picture on screen and a counter that keeps the newest on top */
	let shown = $state<{ src: string; z: number } | null>(null);
	let z = 0;

	$effect(() => {
		const src = want;
		if (!src) {
			shown = null;
			return;
		}
		if (untrack(() => shown?.src) === src) return;
		let live = true;
		const img = new Image();
		img.onload = () => live && (shown = { src, z: ++z });
		img.onerror = () => live && (shown = null);
		img.src = src;
		return () => {
			live = false;
		};
	});

	const duration = $derived(preferences.reduced_motion ? 0 : 420);
</script>

<div class="wallpaper" style:background-color={tone} use:elevation={'wallpaper'}>
	{#key shown?.src}
		{#if shown}
			<div class="picture" style:background-image="url({shown.src})" style:z-index={shown.z} in:fade|global={{ duration }} out:fade|global={{ duration, delay: want ? duration : 0 }}></div>
		{/if}
	{/key}
</div>
<div class="dim" class:on={dim} use:elevation={'wallpaper'}></div>

<style>
	.wallpaper,
	.dim {
		position: fixed;
		inset: 0;
	}

	.wallpaper {
		background-color: var(--zd-wallpaper);
		transition: background-color 260ms ease;
		isolation: isolate;
	}

	.picture {
		position: absolute;
		inset: 0;
		background-size: cover;
		background-position: center;
	}

	.dim {
		pointer-events: none;
		background: hsla(0, 0%, 6%, 0);
		transition: background-color 260ms ease;
	}

	:global(body.dark) .dim.on {
		background: hsla(0, 0%, 6%, 0.38);
	}
</style>
