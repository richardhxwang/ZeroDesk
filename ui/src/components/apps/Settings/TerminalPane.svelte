<script lang="ts">
	import { onMount } from 'svelte';
	import { format_bytes, t } from '🍎/state/i18n.svelte.ts';
	import { preferences, type CursorStyle } from '🍎/state/preferences.svelte.ts';
	import { font_family_of, get_preset, load_presets, local_font_available, presets, remove_preset, variant_of, type Mode, type PresetFont } from '🍎/state/presets.svelte.ts';
	import Segmented from '../common/Segmented.svelte';

	// Settings › Terminal. Colour schemes and fonts that are not on the device
	// yet sit among the others with a small cloud mark; picking one downloads
	// it (agent/presets.go) and applies it.

	const MONO = '"SF Mono", ui-monospace, Menlo, "Noto Sans Mono CJK SC", monospace';
	// the built-in scheme, drawn like the others: bg, fg, red, green, yellow, blue, magenta, cyan
	const ZERODESK: Record<Mode, string[]> = {
		light: ['#fafafa', '#1c1c1e', '#cd3131', '#00bc00', '#949800', '#0451a5', '#bc05bc', '#0598bc'],
		dark: ['#1e1e20', '#ececec', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db'],
	};

	let error = $state('');

	onMount(() => {
		preferences.seen_terminal_settings = true;
		load_presets().catch((e) => (error = (e as Error).message));
	});

	// the variant the Terminal shows right now
	const colors = $derived.by(() => {
		const scheme = preferences.theme.scheme;
		const th = presets.themes.find((p) => p.id === preferences.term_theme);
		return (th && th.preview[variant_of(th, scheme)]) || ZERODESK[scheme];
	});

	// the preview uses the same font the Terminal will
	let preview_family = $state(MONO);
	$effect(() => {
		const choice = preferences.term_font;
		const local = preferences.term_font_local.trim();
		const font = presets.fonts.find((f) => f.id === choice);
		let live = true;
		if (choice === 'local' && local) preview_family = `"${local.replaceAll('"', '')}", ${MONO}`;
		else if (font?.installed) font_family_of(font).then((f) => live && (preview_family = `"${f}", ${MONO}`), () => live && (preview_family = MONO));
		else preview_family = MONO;
		return () => (live = false);
	});

	const local_missing = $derived(
		preferences.term_font === 'local' && !!preferences.term_font_local.trim() && !local_font_available(preferences.term_font_local),
	);

	async function pick_theme(id: string) {
		if (presets.busy[id]) return;
		if (id === 'zerodesk' || (await get_preset(id))) preferences.term_theme = id;
	}

	async function pick_font(id: string) {
		if (presets.busy[id]) return;
		if (id === 'system' || id === 'local' || (await get_preset(id))) preferences.term_font = id;
	}

	async function remove_font(f: PresetFont) {
		try {
			await remove_preset(f.id);
		} catch (e) {
			presets.failed[f.id] = (e as Error).message;
		}
	}

	const step_size = (d: number) => (preferences.term_font_size = Math.min(20, Math.max(10, preferences.term_font_size + d)));
</script>

{#snippet swatch(preview: Partial<Record<Mode, string[]>>)}
	{#each ['light', 'dark'] as const as mode (mode)}
		{@const c = preview[mode]}
		{#if c}
			<span class="half {mode}" class:whole={!preview.light || !preview.dark} style:background={c[0]}>
				<span class="bars">
					<i style:background={c[5]}></i><i style:background={c[3]}></i><i style:background={c[6]}></i>
					<i class="long" style:background={c[1]}></i>
				</span>
			</span>
		{/if}
	{/each}
{/snippet}

{#snippet cloud(id: string, installed: boolean)}
	{#if presets.busy[id]}
		<span class="spinner" aria-label="…"></span>
	{:else if !installed}
		<i class="f7-icons cloud" aria-hidden="true">cloud_download</i>
	{/if}
{/snippet}

<div class="preview" style:background={colors[0]} style:color={colors[1]} style:font-family={preview_family} style:font-size="{preferences.term_font_size}px">
	<div class="line">
		<span class="seg" style:background={colors[5]} style:color={colors[0]}>&nbsp;~/zerodesk&nbsp;</span><span style:color={colors[5]}
			>{''}</span
		><span style:color={colors[3]}>&nbsp;main</span>&nbsp;<span style:color={colors[1]}>❯</span>&nbsp;ls
	</div>
	<div class="line">
		<span style:color={colors[5]}>apps</span>&nbsp;&nbsp;<span style:color={colors[6]}>docs</span>&nbsp;&nbsp;<span style:color={colors[3]}
			>deploy.sh</span
		>&nbsp;&nbsp;<span style:color={colors[4]}>README.md</span>
	</div>
	<div class="line">
		<span style:color={colors[2]}>✗</span>&nbsp;<span style:color={colors[7]}>0.3s</span>&nbsp;<span style:color={colors[1]}>❯</span>&nbsp;<span
			class="cursor {preferences.term_cursor}"
			style:--c={colors[1]}>&nbsp;</span
		>
	</div>
</div>

<h2>{t('settings.term_colors')}</h2>
<div class="themes">
	<button class="theme" class:on={preferences.term_theme === 'zerodesk'} onclick={() => pick_theme('zerodesk')}>
		<span class="swatch">{@render swatch(ZERODESK)}</span>
		<span class="cap"><b>ZeroDesk</b></span>
	</button>
	{#each presets.themes as th (th.id)}
		<button class="theme" class:on={preferences.term_theme === th.id} onclick={() => pick_theme(th.id)} title={presets.failed[th.id] ?? th.name}>
			<span class="swatch">
				{@render swatch(th.preview)}
				{@render cloud(th.id, th.installed)}
			</span>
			<span class="cap"
				><b>{th.name}</b>{#if presets.failed[th.id]}<small class="err">{t('settings.download_failed')}</small>{/if}</span
			>
		</button>
	{:else}
		{#if !presets.loaded}{#each [0, 1, 2, 3, 4, 5, 6] as i (i)}<span class="theme ghost"><span class="swatch"></span></span>{/each}{/if}
	{/each}
</div>

<h2>{t('settings.term_font')}</h2>
<div class="card">
	<button class="row pick" onclick={() => pick_font('system')}>
		<span class="grow"><span class="name">{t('settings.term_font_system')}</span></span>
		{#if preferences.term_font === 'system'}<i class="f7-icons check">checkmark_alt</i>{/if}
	</button>
	{#each presets.fonts as f (f.id)}
		<div class="row pick" role="button" tabindex="0" onclick={() => pick_font(f.id)} onkeydown={(e) => e.key === 'Enter' && pick_font(f.id)}>
			<span class="grow">
				<span class="name">{f.name}</span>
				{#if presets.failed[f.id]}<small class="err">{t('settings.download_failed')}</small>
				{:else if f.nerd}<small>{t('settings.term_nerd')}</small>{/if}
			</span>
			{#if f.installed && f.nerd && preferences.term_font !== f.id}
				<button class="link" onclick={(e) => (e.stopPropagation(), remove_font(f))}>{t('settings.remove')}</button>
			{/if}
			{#if !f.installed && !presets.busy[f.id]}<span class="size">{format_bytes(f.size)}</span>{/if}
			{@render cloud(f.id, f.installed)}
			{#if preferences.term_font === f.id}<i class="f7-icons check">checkmark_alt</i>{/if}
		</div>
	{/each}
	<div class="row pick local" role="button" tabindex="0" onclick={() => pick_font('local')} onkeydown={(e) => e.key === 'Enter' && pick_font('local')}>
		<span class="grow">
			<span class="name">{t('settings.term_font_local')}</span>
			<input
				bind:value={preferences.term_font_local}
				placeholder={t('settings.term_font_local_ph')}
				spellcheck="false"
				autocomplete="off"
				onfocus={() => (preferences.term_font = 'local')}
			/>
			{#if local_missing}<small>{t('settings.term_font_missing')}</small>{/if}
		</span>
		{#if preferences.term_font === 'local'}<i class="f7-icons check">checkmark_alt</i>{/if}
	</div>
</div>

<div class="card">
	<div class="row">
		<span class="grow">{t('settings.term_size')}</span>
		<div class="stepper">
			<button aria-label="-" onclick={() => step_size(-1)} disabled={preferences.term_font_size <= 10}>−</button>
			<span class="num">{preferences.term_font_size}</span>
			<button aria-label="+" onclick={() => step_size(1)} disabled={preferences.term_font_size >= 20}>+</button>
		</div>
	</div>
	<div class="row">
		<span class="grow">{t('settings.term_cursor')}</span>
		<Segmented
			options={[
				{ value: 'block' as CursorStyle, label: t('settings.cursor_block') },
				{ value: 'bar' as CursorStyle, label: t('settings.cursor_bar') },
				{ value: 'underline' as CursorStyle, label: t('settings.cursor_underline') },
			]}
			value={preferences.term_cursor}
			onchange={(v) => (preferences.term_cursor = v)}
		/>
	</div>
	<div class="row">
		<span class="grow">{t('settings.term_blink')}</span>
		<button
			class="switch"
			class:on={preferences.term_cursor_blink}
			role="switch"
			aria-checked={preferences.term_cursor_blink}
			aria-label={t('settings.term_blink')}
			onclick={() => (preferences.term_cursor_blink = !preferences.term_cursor_blink)}><span></span></button
		>
	</div>
</div>

<p class="foot">{t('settings.download_note')}</p>
{#if error}<p class="foot err">{error}</p>{/if}

<style>
	/* Settings' content column is a flex column that scrolls: keep every block at its own height */
	.preview,
	h2,
	.themes,
	.card,
	.foot {
		flex: none;
	}

	.preview {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 14px 16px;
		border-radius: 12px;
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline);
		line-height: 1.35;
		white-space: nowrap;
		overflow: hidden;
		transition:
			background-color 260ms ease,
			color 260ms ease;
	}

	.line {
		overflow: hidden;
		text-overflow: clip;
	}

	.seg {
		border-radius: 3px 0 0 3px;
	}

	.cursor {
		display: inline-block;
		width: 0.6em;

		&.block {
			background: var(--c);
		}
		&.bar {
			box-shadow: inset 2px 0 0 var(--c);
		}
		&.underline {
			box-shadow: inset 0 -2px 0 var(--c);
		}
	}

	h2 {
		margin: 6px 4px -4px;
		font-size: 13px;
		font-weight: 600;
	}

	.themes {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
		gap: 14px 12px;
		padding: 4px 2px;
	}

	.theme {
		display: flex;
		flex-direction: column;
		/* buttons centre their content: keep swatches level when a name wraps */
		justify-content: flex-start;
		align-items: stretch;
		gap: 6px;
		min-width: 0;
		padding: 0;
		text-align: left;

		.swatch {
			position: relative;
			height: 58px;
			border-radius: 9px;
			overflow: hidden;
			box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			transition: box-shadow 180ms ease;

			/* light variant on the left, dark on the right, split on a slant */
			.half {
				position: absolute;
				inset: 0;

				&.light:not(.whole) {
					clip-path: polygon(0 0, 64% 0, 40% 100%, 0 100%);
				}
				&.dark:not(.whole) {
					clip-path: polygon(64% 0, 100% 0, 100% 100%, 40% 100%);

					.bars {
						left: auto;
						right: 9px;
					}
				}
			}

			/* the hairline sits above the halves */
			&::after {
				content: '';
				position: absolute;
				inset: 0;
				border-radius: inherit;
				box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
				pointer-events: none;
			}
		}

		&.on .swatch {
			box-shadow:
				0 0 0 2px var(--zd-window-bg),
				0 0 0 4px var(--zd-text);
		}

		&.ghost .swatch {
			background: var(--zd-hover);
		}

		.bars {
			position: absolute;
			left: 10px;
			width: 46px;
			top: 14px;
			display: flex;
			flex-wrap: wrap;
			gap: 5px;

			i {
				height: 5px;
				width: 12px;
				border-radius: 3px;
			}

			.long {
				width: 44px;
				opacity: 0.85;
			}
		}

		.cap {
			display: flex;
			flex-direction: column;
			gap: 1px;
			padding: 0 2px;
			min-width: 0;

			/* one line: names are kept short enough to fit, never cut off */
			b {
				font-size: 12px;
				font-weight: 500;
				line-height: 1.3;
				white-space: nowrap;
			}

			small {
				font-size: 11px;
				color: var(--zd-text-tertiary);
			}
		}
	}

	/* the "not on the device yet" mark: present, quiet */
	.cloud {
		position: absolute;
		right: 7px;
		bottom: 6px;
		font-size: 13px;
		color: #fff;
		opacity: 0.7;
		mix-blend-mode: difference;
	}

	.row .cloud {
		position: static;
		mix-blend-mode: normal;
		color: var(--zd-text-tertiary);
		opacity: 1;
	}

	.spinner {
		position: absolute;
		right: 7px;
		bottom: 7px;
		width: 12px;
		height: 12px;
		border-radius: 50%;
		border: 1.5px solid hsla(0, 0%, 50%, 0.35);
		border-top-color: var(--zd-text);
		animation: spin 800ms linear infinite;

		.row & {
			position: static;
		}
	}

	@keyframes spin {
		to {
			transform: rotate(1turn);
		}
	}

	.card {
		border-radius: 12px;
		background: var(--zd-hover);
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline);
		padding: 0 14px;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 12px;
		width: 100%;
		min-height: 44px;
		padding: 6px 0;
		font-size: 13px;
		text-align: left;
		justify-content: flex-start;

		& + .row {
			box-shadow: 0 -0.5px 0 var(--zd-hairline);
		}

		&.pick {
			cursor: default;
		}
	}

	.grow {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;

		small {
			font-size: 11px;
			color: var(--zd-text-tertiary);
		}
	}

	.name {
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.local input {
		margin-top: 4px;
		width: min(100%, 280px);
		height: 26px;
		padding: 0 9px;
		/* no browser default inset border */
		border: 0;
		outline: 0;
		border-radius: 6px;
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
		font: inherit;
		font-size: 12px;
		color: var(--zd-text);
		transition: box-shadow 160ms ease;

		&:focus {
			box-shadow:
				var(--zd-control-shadow),
				0 0 0 2px var(--zd-hairline-strong);
		}
	}

	.size {
		font-size: 11px;
		color: var(--zd-text-tertiary);
		font-variant-numeric: tabular-nums;
	}

	.check {
		font-size: 14px;
		font-weight: 600;
	}

	.link {
		font-size: 12px;
		color: var(--zd-text-secondary);
		padding: 2px 4px;
	}

	.stepper {
		display: flex;
		align-items: center;
		gap: 2px;
		border-radius: 7px;
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);

		button {
			width: 28px;
			height: 24px;
			font-size: 15px;

			&:disabled {
				opacity: 0.35;
			}
		}

		.num {
			min-width: 24px;
			text-align: center;
			font-size: 12px;
			font-variant-numeric: tabular-nums;
		}
	}

	.switch {
		flex: none;
		width: 38px;
		height: 22px;
		padding: 2px;
		justify-content: flex-start;
		border-radius: 11px;
		background: var(--zd-hairline-strong);
		transition: background-color 200ms ease;

		span {
			width: 18px;
			height: 18px;
			border-radius: 9px;
			background: #fff;
			box-shadow: 0 1px 3px hsla(0, 0%, 0%, 0.25);
			transition: transform 380ms var(--zd-spring);
		}

		&.on {
			background: var(--zd-green);

			span {
				transform: translateX(16px);
			}
		}
	}

	.foot {
		margin: -4px 4px 0;
		font-size: 11px;
		color: var(--zd-text-tertiary);
	}

	.err {
		color: var(--zd-red) !important;
	}
</style>
