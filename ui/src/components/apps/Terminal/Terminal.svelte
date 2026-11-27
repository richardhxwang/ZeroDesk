<script lang="ts">
	import { onDestroy, tick, untrack } from 'svelte';
	import { close_app, open_settings } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import { IS_MAC, terminal } from '🍎/state/terminal.svelte.ts';
	import Toolbar from '../common/Toolbar.svelte';
	import ToolButton from '../common/ToolButton.svelte';
	import { close, neighbour, new_pane, panes_of, split, tile, type Layout } from './layout.ts';
	import TermPane, { type PaneApi } from './TermPane.svelte';

	// The Terminal window: tabs, each holding one or more panes in splits
	// (as in Ghostty), and a find bar for the focused pane. Every pane is its
	// own shell on the CardputerZero.
	//
	// Tabs and panes are always referred to by id, never by position: closing
	// a tab moves the others, and a callback holding an index would then act
	// on the wrong one.

	type Tab = { id: number; layout: Layout; focused: number };

	const first = new_pane();
	let tabs = $state<Tab[]>([{ id: first, layout: { pane: first }, focused: first }]);
	let current_id = $state(first);
	let titles = $state<Record<number, string>>({});
	const apis = new Map<number, PaneApi>();

	const tab = $derived(tabs.find((x) => x.id === current_id) ?? tabs[0]);
	const fallback_title = `${session.user}@${session.hostname}`;
	const title_of = (pane: number) => titles[pane] || fallback_title;
	/** A tab shows the folder, as macOS Terminal does: "user@host: ~/src/app" -> "app". */
	const tab_label = (pane: number) => {
		const s = title_of(pane);
		const where = s.includes(':') ? s.slice(s.indexOf(':') + 1).trim() : s;
		return where === '~' || !where.includes('/') ? where : where.slice(where.lastIndexOf('/') + 1) || '/';
	};

	const tab_by_id = (id: number) => tabs.find((x) => x.id === id);

	/** Make `pane` the tab's focused pane; the keyboard moves only when that tab is the one on screen. */
	function focus_pane(tab_id: number, pane: number) {
		const tb = tab_by_id(tab_id);
		if (!tb) return;
		tb.focused = pane;
		if (tab_id === current_id) tick().then(() => apis.get(pane)?.focus());
	}

	function select_tab(id: number) {
		const tb = tab_by_id(id);
		if (!tb) return;
		current_id = id;
		focus_pane(id, tb.focused);
	}

	function new_tab() {
		const pane = new_pane();
		tabs.push({ id: pane, layout: { pane }, focused: pane });
		current_id = pane;
		end_find();
	}

	function split_focused(dir: 'row' | 'col') {
		const r = split(tab.layout, tab.focused, dir);
		tab.layout = r.layout;
		focus_pane(tab.id, r.pane);
	}

	/** Close a tab and forget its panes; the last one closes the window. */
	function remove_tab(id: number) {
		const i = tabs.findIndex((x) => x.id === id);
		if (i < 0) return;
		for (const p of panes_of(tabs[i].layout)) delete titles[p];
		tabs.splice(i, 1);
		if (!tabs.length) return close_app('terminal');
		// only closing the tab on screen changes which tab is on screen
		if (id === current_id) select_tab(tabs[Math.min(i, tabs.length - 1)].id);
	}

	/**
	 * The tab's × button. A click does not take the keyboard (mousedown is
	 * prevented), so it stays where it was, the find bar included; only when the
	 * keyboard was on the × itself (Tab, then Enter) does it go back to the tab on screen.
	 */
	function close_tab_button(e: Event, id: number) {
		e.stopPropagation();
		const had_keyboard = e.currentTarget === document.activeElement;
		remove_tab(id);
		if (had_keyboard && tabs.length) focus_pane(tab.id, tab.focused);
	}

	/** Remove a pane (its shell ended or it was closed); an empty tab goes. */
	function remove_pane(tab_id: number, pane: number) {
		const tb = tab_by_id(tab_id);
		if (!tb) return;
		const layout = close(tb.layout, pane);
		if (!layout) return remove_tab(tab_id);
		const heir = tb.focused === pane ? neighbour(tb.layout, pane) : null;
		delete titles[pane];
		tb.layout = layout;
		if (heir !== null) focus_pane(tab_id, heir);
	}

	/* ---- find: it belongs to one pane at a time ---- */

	let find_open = $state(false);
	let query = $state('');
	let result = $state({ index: -1, count: 0 });
	let find_input = $state<HTMLInputElement>();
	/** the pane the query, highlights and count belong to */
	let find_pane = $state<number | null>(null);

	// A click into a pane: xterm clears the selection when the button comes up,
	// which would drop the match a search had just selected on focus.
	let pointer_held = false;
	function after_pointer(run: () => void) {
		if (!pointer_held) return void tick().then(run);
		addEventListener('pointerup', () => setTimeout(run), { once: true, capture: true });
	}
	const on_pointer_up = () => (pointer_held = false);
	addEventListener('pointerup', on_pointer_up);
	addEventListener('pointercancel', on_pointer_up);
	onDestroy(() => {
		removeEventListener('pointerup', on_pointer_up);
		removeEventListener('pointercancel', on_pointer_up);
	});

	// The focused pane changed (a click, another tab, a split, a pane closed):
	// clear the old pane's highlights and search the new one.
	$effect(() => {
		const target = find_open ? tab.focused : null;
		untrack(() => {
			if (target === find_pane) return;
			if (find_pane !== null) apis.get(find_pane)?.clear_find();
			find_pane = target;
			result = { index: -1, count: 0 };
			if (target !== null && query) after_pointer(() => target === find_pane && apis.get(target)?.find(query));
		});
	});

	function open_find() {
		find_open = true;
		tick().then(() => find_input?.select());
	}

	function end_find() {
		if (find_pane !== null) apis.get(find_pane)?.clear_find();
		find_open = false;
		find_pane = null;
		result = { index: -1, count: 0 };
	}

	function close_find() {
		end_find();
		apis.get(tab.focused)?.focus();
	}

	function find(step?: 'next' | 'prev') {
		if (find_pane !== null) apis.get(find_pane)?.find(query, step);
	}

	/** Window shortcuts, wherever the keyboard is in the window (a pane, the find bar, a button). */
	function shortcut(e: KeyboardEvent): boolean {
		const mod = IS_MAC ? e.metaKey && !e.ctrlKey : e.ctrlKey && e.shiftKey;
		const k = e.key.toLowerCase();
		let run: (() => void) | undefined;
		if (mod && k === 'f') run = open_find;
		else if (mod && k === 'd') run = () => split_focused(e.shiftKey && IS_MAC ? 'col' : 'row');
		else if (!IS_MAC && mod && k === 'e') run = () => split_focused('col');
		else if ((IS_MAC ? e.altKey && !e.metaKey && e.code === 'KeyT' : mod && k === 't')) run = new_tab;
		if (!run) return false;
		e.preventDefault();
		run();
		return true;
	}

	terminal.commands = {
		new_tab,
		split: split_focused,
		close_pane: () => remove_pane(tab.id, tab.focused),
		find: open_find,
	};
	onDestroy(() => (terminal.commands = null));
</script>


<!-- keydown bubbles here from every control in the window; a pane that handled a shortcut has already prevented it -->
<div class="terminal" role="presentation" onkeydown={(e) => !e.defaultPrevented && shortcut(e)} onpointerdowncapture={() => (pointer_held = true)}>
	<Toolbar title={t('app.terminal')} subtitle={title_of(tab.focused)}>
		{#snippet trailing()}
			<ToolButton label={t('term.find')} onclick={open_find} pressed={find_open}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<circle cx="8.6" cy="8.6" r="5" fill="none" stroke="currentColor" stroke-width="1.5" />
					<path d="M12.4 12.4l4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
				</svg>
			</ToolButton>
			<ToolButton label={t('term.split_right')} onclick={() => split_focused('row')}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<rect x="2.8" y="4" width="14.4" height="12" rx="2.4" fill="none" stroke="currentColor" stroke-width="1.4" />
					<path d="M10 4v12" stroke="currentColor" stroke-width="1.4" />
				</svg>
			</ToolButton>
			<ToolButton label={t('term.new_tab')} onclick={new_tab}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<path d="M10 4.5v11M4.5 10h11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
				</svg>
			</ToolButton>
			<ToolButton label={t('terminal.appearance')} onclick={() => open_settings('terminal')}>
				<svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
					<rect x="2.8" y="4" width="14.4" height="12" rx="2.4" fill="none" stroke="currentColor" stroke-width="1.4" />
					<path d="M5.8 8.2l2.2 1.8-2.2 1.8M9.8 12h4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
				</svg>
			</ToolButton>
		{/snippet}
	</Toolbar>

	{#if tabs.length > 1}
		<div class="tabs" role="tablist">
			{#each tabs as tb (tb.id)}
				<div class="tab" class:on={tb.id === current_id} role="tab" tabindex="0" aria-selected={tb.id === current_id} onclick={() => select_tab(tb.id)} onkeydown={(e) => e.target === e.currentTarget && e.key === 'Enter' && select_tab(tb.id)}>
					<button class="x" aria-label={t('term.close_tab')} onmousedown={(e) => e.preventDefault()} onclick={(e) => close_tab_button(e, tb.id)}>
						<svg viewBox="0 0 10 10" width="8" height="8" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
					</button>
					<span class="label" title={title_of(tb.focused)}>{tab_label(tb.focused)}</span>
				</div>
			{/each}
		</div>
	{/if}

	<div class="body">
		{#each tabs as tb (tb.id)}
			{@const t_ = tile(tb.layout)}
			<div class="tab-body" class:hidden={tb.id !== current_id}>
				<!-- One keyed list for all panes: a split or close moves panes, never recreates their shells. -->
				{#each t_.panes as { pane, rect } (pane)}
					<div class="cell" style:left="{rect.x * 100}%" style:top="{rect.y * 100}%" style:width="{rect.w * 100}%" style:height="{rect.h * 100}%">
						<TermPane
							focused={tb.id === current_id && tb.focused === pane}
							onfocus={() => (tb.focused = pane)}
							onexit={() => remove_pane(tb.id, pane)}
							ontitle={(s) => (titles[pane] = s)}
							onresults={(index, count) => pane === find_pane && (result = { index, count })}
							register={(a) => {
								if (!a) return void apis.delete(pane);
								apis.set(pane, a);
								// a pane that starts while the find bar is on it (a split) is searched once it can be
								if (pane === find_pane && query) a.find(query);
							}}
							onshortcut={shortcut}
						/>
					</div>
				{/each}
				{#each t_.dividers as d}
					<span class="divider {d.dir}" style:left="{d.rect.x * 100}%" style:top="{d.rect.y * 100}%" style:width={d.dir === 'col' ? `${d.rect.w * 100}%` : null} style:height={d.dir === 'row' ? `${d.rect.h * 100}%` : null}></span>
				{/each}
			</div>
		{/each}

		{#if find_open}
			<div class="find" role="search">
				<input
					bind:this={find_input}
					bind:value={query}
					placeholder={t('term.find')}
					spellcheck="false"
					oninput={() => find()}
					onkeydown={(e) => {
						if (e.key === 'Enter') find(e.shiftKey ? 'prev' : 'next');
						else if (e.key === 'Escape') close_find();
					}}
				/>
				<span class="count">{query ? (result.count ? `${result.index + 1}/${result.count}` : t('term.no_matches')) : ''}</span>
				<button aria-label={t('term.find_prev')} onclick={() => find('prev')} disabled={!result.count}>
					<svg viewBox="0 0 12 12" width="10" height="10" aria-hidden="true"><path d="M3 7.5l3-3 3 3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
				</button>
				<button aria-label={t('term.find_next')} onclick={() => find('next')} disabled={!result.count}>
					<svg viewBox="0 0 12 12" width="10" height="10" aria-hidden="true"><path d="M3 4.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
				</button>
				<button aria-label={t('term.find_close')} onclick={close_find}>
					<svg viewBox="0 0 10 10" width="8" height="8" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
				</button>
			</div>
		{/if}
	</div>
</div>

<style>
	.terminal {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}

	/* macOS tab bar: equal-width tabs, the close button shows on hover */
	.tabs {
		flex: none;
		display: flex;
		gap: 1px;
		height: 30px;
		padding: 0 8px 6px;
		background: var(--zd-toolbar-bg);
		box-shadow: 0 0.5px 0 var(--zd-hairline);
	}

	.tab {
		position: relative;
		flex: 1;
		min-width: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 0 26px;
		border-radius: 7px;
		font-size: 12px;
		color: var(--zd-text-secondary);
		cursor: default;

		&:hover {
			background: var(--zd-hover);
		}

		&.on {
			background: var(--zd-selected);
			color: var(--zd-text);
			font-weight: 500;
		}

		.label {
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}

		.x {
			position: absolute;
			left: 6px;
			width: 16px;
			height: 16px;
			border-radius: 4px;
			opacity: 0;
			transition: opacity 120ms ease;

			&:hover {
				background: var(--zd-hover);
			}
		}

		&:hover .x {
			opacity: 1;
		}
	}

	.body {
		position: relative;
		flex: 1;
		min-height: 0;
	}

	.tab-body {
		position: absolute;
		inset: 0;

		&.hidden {
			visibility: hidden;
			pointer-events: none;
		}
	}

	.cell {
		position: absolute;
		display: flex;
	}

	.divider {
		position: absolute;
		z-index: 1;
		background: var(--zd-hairline-strong);
		pointer-events: none;

		&.row {
			width: 1px;
			translate: -0.5px 0;
		}
		&.col {
			height: 1px;
			translate: 0 -0.5px;
		}
	}

	.find {
		position: absolute;
		top: 10px;
		right: 14px;
		z-index: 2;
		display: flex;
		align-items: center;
		gap: 4px;
		height: 34px;
		padding: 0 6px 0 4px;
		border-radius: 10px;
		background: var(--zd-glass);
		backdrop-filter: blur(20px);
		box-shadow:
			0 0 0 0.5px var(--zd-glass-border),
			0 6px 20px hsla(0, 0%, 0%, 0.18);

		input {
			width: 180px;
			height: 26px;
			padding: 0 9px;
			border: 0;
			outline: 0;
			border-radius: 6px;
			background: var(--zd-control-bg);
			box-shadow: var(--zd-control-shadow);
			font: inherit;
			font-size: 12px;
			color: var(--zd-text);
		}

		.count {
			min-width: 52px;
			padding: 0 4px;
			font-size: 11px;
			color: var(--zd-text-secondary);
			text-align: center;
			font-variant-numeric: tabular-nums;
			white-space: nowrap;
		}

		button {
			width: 24px;
			height: 24px;
			border-radius: 6px;
			color: var(--zd-text-secondary);

			&:hover:not(:disabled) {
				background: var(--zd-hover);
				color: var(--zd-text);
			}

			&:disabled {
				opacity: 0.35;
			}
		}
	}
</style>
