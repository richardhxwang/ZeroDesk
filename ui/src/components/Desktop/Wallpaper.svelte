<script lang="ts">
	import { elevation } from '🍎/actions';
	import { preferences, WALLPAPER_TONES } from '🍎/state/preferences.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';

	// By default the desktop uses the device's own Linux desktop wallpaper,
	// served by the agent; until it has loaded (or if there is none) a plain
	// tone shows. Settings › Appearance can pick a plain tone instead.
	let loaded = $state(false);
	const src = '/api/wallpaper';
	const device = $derived(preferences.wallpaper === 'device' || !(preferences.wallpaper in WALLPAPER_TONES));
	const tone = $derived(device ? undefined : WALLPAPER_TONES[preferences.wallpaper as keyof typeof WALLPAPER_TONES][preferences.theme.scheme === 'dark' ? 1 : 0]);
</script>

{#if session.authed && device}
	<img {src} alt="" aria-hidden="true" class="probe" onload={() => (loaded = true)} />
{/if}

<div
	class="wallpaper"
	class:loaded={loaded && device}
	style:background-image={loaded && device ? `url(${src})` : 'none'}
	style:background-color={tone}
	use:elevation={'wallpaper'}
></div>
<div class="dim" use:elevation={'wallpaper'}></div>

<style>
	.probe {
		display: none;
	}

	.wallpaper,
	.dim {
		position: fixed;
		inset: 0;
	}

	.wallpaper {
		background-color: var(--zd-wallpaper);
		background-size: cover;
		background-position: center;
		opacity: 1;
		transition: background-color 260ms ease;

		&.loaded {
			animation: wallpaper-in 600ms var(--zd-ease-out);
		}
	}

	.dim {
		pointer-events: none;
		background: hsla(0, 0%, 6%, 0);
		transition: background-color 260ms ease;
	}

	:global(body.dark) .dim {
		background: hsla(0, 0%, 6%, 0.38);
	}

	@keyframes wallpaper-in {
		from {
			opacity: 0;
			transform: scale(1.02);
		}
		to {
			opacity: 1;
			transform: scale(1);
		}
	}
</style>
