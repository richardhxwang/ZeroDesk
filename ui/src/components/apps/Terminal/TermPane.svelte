<script lang="ts" module>
	import type { ITheme } from '@xterm/xterm';

	/** What the window needs from a pane: focus it, search in it. */
	export type PaneApi = {
		focus(): void;
		/** search as the query is typed, or step to the next/previous match */
		find(query: string, step?: 'next' | 'prev'): void;
		has_selection(): boolean;
		clear_find(): void;
	};

	export const BUILTIN: Record<'light' | 'dark', ITheme> = {
		light: { background: '#fafafa', foreground: '#1c1c1e', cursor: '#1c1c1e', selectionBackground: '#1c1c1e22', black: '#1c1c1e', brightBlack: '#6e6a66' },
		dark: { background: '#1e1e20', foreground: '#ececec', cursor: '#ececec', selectionBackground: '#ffffff26', black: '#2b2a29', brightBlack: '#8e8a85' },
	};
</script>

<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import '@xterm/xterm/css/xterm.css';
	import { api, ws_url } from '🍎/lib/api.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { preferences } from '🍎/state/preferences.svelte.ts';
	import { font_family_of, load_presets, presets, theme_of, variant_of } from '🍎/state/presets.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';

	const {
		focused,
		onfocus,
		onexit,
		ontitle,
		onresults,
		register,
		onshortcut,
	}: {
		focused: boolean;
		onfocus: () => void;
		/** the shell ended */
		onexit: () => void;
		ontitle: (title: string) => void;
		/** search results changed: index of the current match (-1: none) and count */
		onresults: (index: number, count: number) => void;
		register: (api: PaneApi | null) => void;
		/** a window shortcut was pressed inside the terminal; true when handled */
		onshortcut: (e: KeyboardEvent) => boolean;
	} = $props();

	let host = $state<HTMLElement>();
	let term = $state<import('@xterm/xterm').Terminal>();
	let ws: WebSocket | undefined;
	let ro: ResizeObserver | undefined;
	let refit: () => void = () => {};
	let search: import('@xterm/addon-search').SearchAddon | undefined;
	// set on destroy: the xterm modules load asynchronously and may arrive after the pane is gone
	let destroyed = false;
	/** the term this pane's search addon last searched (it continues from a selection only for that term) */
	let searched: string | undefined;
	/** the last press in this pane held Alt: an Alt-drag makes a block (column) selection */
	let block_drag = false;

	// Terminal programs align text by character columns, so this is the one
	// place that uses a monospaced face; a chosen font goes in front of these.
	const MONO = '"SF Mono", ui-monospace, Menlo, "Noto Sans Mono CJK SC", monospace';

	let theme = $state<ITheme>(BUILTIN[preferences.theme.scheme]);

	// colours: a scheme with light and dark variants follows Appearance, like the built-in one
	$effect(() => {
		const id = preferences.term_theme;
		const scheme = preferences.theme.scheme;
		const fallback = BUILTIN[scheme];
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

	$effect(() => {
		if (focused && term) term.focus();
	});

	/* ---- OSC 52: programs (vim, tmux) copying to the computer's clipboard ---- */

	// The Clipboard API needs a secure page; http://cardputerzero.local is
	// not one, and a copy outside a click is refused. Then the text waits
	// here and one click on Copy puts it on the clipboard.
	let pending_copy = $state('');

	function decode_b64(b64: string): string {
		const bin = atob(b64);
		return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
	}

	function osc52(data: string): boolean {
		const b64 = data.slice(data.indexOf(';') + 1);
		if (!b64 || b64 === '?') return true; // reading the clipboard is not offered
		let text: string;
		try {
			text = decode_b64(b64);
		} catch {
			return true;
		}
		if (window.isSecureContext && navigator.clipboard) {
			navigator.clipboard.writeText(text).catch(() => (pending_copy = text));
		} else {
			pending_copy = text;
		}
		return true;
	}

	function copy_pending() {
		const ta = document.createElement('textarea');
		ta.value = pending_copy;
		ta.style.position = 'fixed';
		ta.style.opacity = '0';
		document.body.append(ta);
		ta.select();
		document.execCommand('copy');
		ta.remove();
		pending_copy = '';
		term?.focus();
	}

	/* ---- dropping files: upload to ~/Downloads and type their paths ---- */

	let dragging = $state(false);
	let upload_note = $state('');

	const quote = (p: string) => `'${p.replaceAll("'", `'\\''`)}'`;

	async function drop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		const files = [...(e.dataTransfer?.files ?? [])];
		if (!files.length) return;
		const dir = `${session.home}/Downloads`;
		try {
			// never overwrite: "name 2.ext" when the name is taken
			const taken = new Set(
				(await api<{ entries: { name: string }[] }>(`/api/fs/list?path=${encodeURIComponent(dir)}`).catch(() => ({ entries: [] }))).entries.map(
					(x) => x.name,
				),
			);
			const form = new FormData();
			for (const f of files) {
				let name = f.name;
				const dot = name.lastIndexOf('.') > 0 ? name.lastIndexOf('.') : name.length;
				for (let i = 2; taken.has(name); i++) name = `${f.name.slice(0, dot)} ${i}${f.name.slice(dot)}`;
				taken.add(name);
				form.append('file', f, name);
			}
			upload_note = t('term.uploading', { name: files.length === 1 ? files[0].name : String(files.length) });
			const res = await api<{ saved: string[] }>(`/api/fs/upload?dir=${encodeURIComponent(dir)}`, { method: 'POST', body: form });
			upload_note = '';
			term?.paste(res.saved.map(quote).join(' ') + ' ');
			term?.focus();
		} catch {
			upload_note = t('term.upload_failed');
			setTimeout(() => (upload_note = ''), 3000);
		}
	}

	onMount(async () => {
		const [{ Terminal }, { FitAddon }, { SearchAddon }, { WebLinksAddon }, { Unicode11Addon }, { ImageAddon }] = await Promise.all([
			import('@xterm/xterm'),
			import('@xterm/addon-fit'),
			import('@xterm/addon-search'),
			import('@xterm/addon-web-links'),
			import('@xterm/addon-unicode11'),
			import('@xterm/addon-image'),
		]);
		if (destroyed || !host) return;
		const tm = new Terminal({
			fontFamily: MONO,
			fontSize: preferences.term_font_size,
			lineHeight: 1.25,
			cursorStyle: preferences.term_cursor,
			cursorBlink: preferences.term_cursor_blink,
			// search highlights and Unicode 11 widths are "proposed" APIs
			allowProposedApi: true,
			scrollback: 5000,
			theme,
		});
		const fit = new FitAddon();
		tm.loadAddon(fit);
		search = new SearchAddon();
		tm.loadAddon(search);
		// ⌘-click (Ctrl-click elsewhere) opens a link, as in Ghostty
		tm.loadAddon(new WebLinksAddon((e, uri) => (e.metaKey || e.ctrlKey) && window.open(uri, '_blank', 'noopener')));
		tm.loadAddon(new Unicode11Addon());
		tm.unicode.activeVersion = '11';
		tm.loadAddon(new ImageAddon({ sixelSupport: true, iipSupport: true }));
		tm.parser.registerOscHandler(52, osc52);
		tm.open(host!);
		fit.fit();

		search.onDidChangeResults(({ resultIndex, resultCount }) => onresults(resultIndex, resultCount));
		tm.attachCustomKeyEventHandler((e) => !(e.type === 'keydown' && onshortcut(e)));
		tm.textarea?.addEventListener('focus', onfocus);

		const enc = new TextEncoder();
		ws = new WebSocket(ws_url('/ws/term'));
		ws.binaryType = 'arraybuffer';
		const send_size = () => ws?.readyState === 1 && ws.send(JSON.stringify({ t: 'resize', cols: tm.cols, rows: tm.rows }));
		ws.onopen = () => {
			send_size();
			if (focused) tm.focus();
		};
		ws.onmessage = (e) => tm.write(new Uint8Array(e.data as ArrayBuffer));
		ws.onclose = () => onexit();
		tm.onData((d) => ws?.readyState === 1 && ws.send(enc.encode(d)));
		tm.onTitleChange(ontitle);

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
		term = tm;

		const decorations = () => ({
			matchBackground: '#8e8e9380',
			activeMatchBackground: '#ffd60a',
			matchOverviewRuler: '#8e8e93',
			activeMatchColorOverviewRuler: '#ffd60a',
		});
		register({
			focus: () => tm.focus(),
			has_selection: () => tm.hasSelection(),
			find: (q, step) => {
				if (!search) return;
				if (!q) {
					searched = undefined;
					return search.clearDecorations();
				}
				// while typing, stay on the match that still fits; Enter moves on
				const opts = { decorations: decorations(), incremental: !step };
				// Next/Previous from a selection the person made: the addon moves on from
				// a selection only for the term it searched last, otherwise it starts at
				// the selection. Give it the highlights and that term without touching the
				// selection (addon-search 0.15, pinned; these members are private).
				// A block selection dragged right to left ends at its lower left corner, and
				// the addon moves on from there, inside the block. Make it a plain selection
				// from the block's top left to its bottom right first; a move replaces the
				// selection with the match anyway.
				if (step && block_drag && tm.hasSelection()) {
					const { start, end } = tm.getSelectionPosition()!;
					const l = Math.min(start.x, end.x);
					const r = Math.max(start.x, end.x);
					tm.select(l, start.y, (end.y - start.y) * tm.cols + r - l);
				}
				if (step && searched !== q && tm.hasSelection()) {
					const addon = search as unknown as {
						_highlightAllMatches(term: string, o: typeof opts): void;
						_cachedSearchTerm?: string;
						_lastSearchOptions?: typeof opts;
					};
					addon._highlightAllMatches(q, opts);
					addon._cachedSearchTerm = q;
					addon._lastSearchOptions = opts;
				}
				const go = () => (step === 'prev' ? search!.findPrevious(q, opts) : search!.findNext(q, opts));
				// The addon does not wrap when it starts on the last row going up (or the
				// first going down). A move that found nothing has cleared the selection,
				// so a second try starts from the far end and wraps.
				if (!go() && step) go();
				searched = q;
				// any search replaces the selection: the block fix is for the drag's own one only
				block_drag = false;
			},
			clear_find: () => {
				searched = undefined;
				// output re-runs the search 200 ms later (addon-search 0.15, private
				// field); left queued, it would search the cleared term and clear the
				// selection, or select a match, after this pane has given up the find bar
				clearTimeout((search as unknown as { _highlightTimeout?: ReturnType<typeof setTimeout> })?._highlightTimeout);
				search?.clearDecorations();
				tm.clearSelection();
			},
		});
	});

	onDestroy(() => {
		destroyed = true;
		register(null);
		ro?.disconnect();
		if (ws) {
			ws.onclose = null;
			ws.close();
		}
		term?.dispose();
	});
</script>

<div
	class="pane"
	class:focused
	class:dragging
	style:background={theme.background}
	role="presentation"
	onpointerdown={(e) => ((block_drag = e.altKey), onfocus())}
	ondragover={(e) => {
		if (e.dataTransfer?.types.includes('Files')) {
			e.preventDefault();
			dragging = true;
		}
	}}
	ondragleave={() => (dragging = false)}
	ondrop={drop}
>
	<div class="host" bind:this={host}></div>
	{#if pending_copy}
		<div class="note">
			<span>{t('term.copy_ready')}</span>
			<button onclick={copy_pending}>{t('term.copy')}</button>
		</div>
	{:else if upload_note}
		<div class="note"><span>{upload_note}</span></div>
	{/if}
</div>

<style>
	.pane {
		position: relative;
		width: 100%;
		height: 100%;
		min-width: 0;
		min-height: 0;
		transition: background-color 260ms ease;

		/* the focused pane is plain; the others step back a little */
		&:not(.focused) .host {
			opacity: 0.72;
		}

		&.dragging::after {
			content: '';
			position: absolute;
			inset: 4px;
			border-radius: 8px;
			box-shadow: inset 0 0 0 2px var(--zd-text-secondary);
			pointer-events: none;
		}
	}

	/* the margins are the element's inset, not padding: FitAddon sizes the grid
	   from this box, and with padding it can count one row too many */
	.host {
		position: absolute;
		inset: 8px 4px 4px 12px;
		transition: opacity 160ms ease;

		:global(.xterm-viewport) {
			scrollbar-width: none;
		}
	}

	.note {
		position: absolute;
		right: 12px;
		bottom: 12px;
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 8px 8px 8px 14px;
		border-radius: 10px;
		background: var(--zd-glass);
		backdrop-filter: blur(20px);
		box-shadow:
			0 0 0 0.5px var(--zd-glass-border),
			0 6px 20px hsla(0, 0%, 0%, 0.18);
		font-size: 12px;
		color: var(--zd-text);

		button {
			height: 24px;
			padding: 0 12px;
			border-radius: 6px;
			background: var(--zd-control-bg);
			box-shadow: var(--zd-control-shadow);
			font-weight: 500;
		}
	}
</style>
