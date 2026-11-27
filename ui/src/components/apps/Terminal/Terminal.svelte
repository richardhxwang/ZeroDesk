<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import type { ITheme } from '@xterm/xterm';
	import '@xterm/xterm/css/xterm.css';
	import { ws_url } from '🍎/lib/api.ts';
	import { open_settings } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { preferences } from '🍎/state/preferences.svelte.ts';
	import { font_family_of, load_presets, presets, theme_of, variant_of } from '🍎/state/presets.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import Toolbar from '../common/Toolbar.svelte';
	import ToolButton from '../common/ToolButton.svelte';

	let host = $state<HTMLElement>();
	let title = $state(`${session.user}@${session.hostname}`);
	let term = $state<import('@xterm/xterm').Terminal>();
	let ws: WebSocket | undefined;
	let ro: ResizeObserver | undefined;
	let refit: () => void = () => {};

	const builtin: Record<'light' | 'dark', ITheme> = {
		light: { background: '#fafafa', foreground: '#1c1c1e', cursor: '#1c1c1e', selectionBackground: '#1c1c1e22', black: '#1c1c1e', brightBlack: '#6e6a66' },
		dark: { background: '#1e1e20', foreground: '#ececec', cursor: '#ececec', selectionBackground: '#ffffff26', black: '#2b2a29', brightBlack: '#8e8a85' },
	};
	// Terminal programs align text by character columns, so this is the one
	// place that uses a monospaced face; a chosen font goes in front of these.
	const MONO = '"SF Mono", ui-monospace, Menlo, "Noto Sans Mono CJK SC", monospace';

	let theme = $state<ITheme>(builtin[preferences.theme.scheme]);

	// colours: a scheme with light and dark variants follows Appearance, like the built-in one
	$effect(() => {
		const id = preferences.term_theme;
		const scheme = preferences.theme.scheme;
		const fallback = builtin[scheme];
		if (id === 'zerodesk') {
			theme = fallback;
			return;
		}
		let live = true;
		load_presets()
			.then(() => {
				const th = presets.themes.find((p) => p.id === id);
				if (!th?.installed) throw new Error('not downloaded');
				return theme_of(id, variant_of(th, scheme));
			})
			.then((th) => live && (theme = th))
			.catch(() => live && (theme = fallback));
		return () => (live = false);
	});

	$effect(() => {
		if (term) term.options.theme = theme;
	});

	$effect(() => {
		if (!term) return;
		term.options.fontSize = preferences.term_font_size;
		term.options.cursorStyle = preferences.term_cursor;
		term.options.cursorBlink = preferences.term_cursor_blink;
		refit();
	});

	// font: system, one installed on this computer, or a downloaded one
	$effect(() => {
		const choice = preferences.term_font;
		const local = preferences.term_font_local.trim();
		if (!term) return;
		let live = true;
		const apply = (family: string) => {
			if (!live || !term) return;
			term.options.fontFamily = family;
			refit();
		};
		if (choice === 'local' && local) apply(`"${local.replaceAll('"', '')}", ${MONO}`);
		else if (choice === 'system' || choice === 'local') apply(MONO);
		else
			load_presets()
				.then(() => {
					const font = presets.fonts.find((f) => f.id === choice);
					return font?.installed ? font_family_of(font).then((family) => `"${family}", ${MONO}`) : MONO;
				})
				.catch(() => MONO)
				.then(apply);
		return () => (live = false);
	});

	onMount(async () => {
		const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]);
		const t = new Terminal({
			fontFamily: MONO,
			fontSize: preferences.term_font_size,
			lineHeight: 1.25,
			cursorStyle: preferences.term_cursor,
			cursorBlink: preferences.term_cursor_blink,
			allowProposedApi: false,
			scrollback: 2000,
			theme,
		});
		const fit = new FitAddon();
		t.loadAddon(fit);
		t.open(host!);
		fit.fit();

		const enc = new TextEncoder();
		ws = new WebSocket(ws_url('/ws/term'));
		ws.binaryType = 'arraybuffer';
		const send_size = () => ws?.readyState === 1 && ws.send(JSON.stringify({ t: 'resize', cols: t.cols, rows: t.rows }));
		ws.onopen = () => {
			send_size();
			t.focus();
		};
		ws.onmessage = (e) => t.write(new Uint8Array(e.data as ArrayBuffer));
		ws.onclose = () => t.write('\r\n\x1b[2m[closed]\x1b[0m\r\n');
		t.onData((d) => ws?.readyState === 1 && ws.send(enc.encode(d)));
		t.onTitleChange((s) => (title = s));

		let frame = 0;
		refit = () => {
			cancelAnimationFrame(frame);
			frame = requestAnimationFrame(() => {
				fit.fit();
				send_size();
			});
		};
		ro = new ResizeObserver(() => refit());
		ro.observe(host!);
		term = t;
	});

	onDestroy(() => {
		ro?.disconnect();
		ws?.close();
		term?.dispose();
	});
</script>

<div class="terminal">
	<Toolbar title={t('app.terminal')} subtitle={title}>
		{#snippet trailing()}
			<ToolButton label={t('terminal.appearance')} onclick={() => open_settings('terminal')}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<rect x="2.8" y="4" width="14.4" height="12" rx="2.4" fill="none" stroke="currentColor" stroke-width="1.4" />
					<path d="M5.8 8.2l2.2 1.8-2.2 1.8M9.8 12h4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
				</svg>
			</ToolButton>
		{/snippet}
	</Toolbar>
	<div class="host" bind:this={host} style:background={theme.background}></div>
</div>

<style>
	.terminal {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}

	.host {
		flex: 1;
		min-height: 0;
		padding: 8px 4px 4px 12px;
		transition: background-color 260ms ease;

		:global(.xterm-viewport) {
			scrollbar-width: none;
		}
	}
</style>
