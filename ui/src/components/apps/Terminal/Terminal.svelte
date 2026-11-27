<script lang="ts">
	import { onDestroy, tick } from 'svelte';
	import { close_app, open_settings } from '🍎/state/apps.svelte.ts';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { session } from '🍎/state/session.svelte.ts';
	import { IS_MAC, terminal } from '🍎/state/terminal.svelte.ts';
	import Toolbar from '../common/Toolbar.svelte';
	import ToolButton from '../common/ToolButton.svelte';
	import { close, new_pane, panes_of, split, type Layout } from './layout.ts';
	import TermPane, { type PaneApi } from './TermPane.svelte';

	// The Terminal window: tabs, each holding one or more panes in splits
	// (as in Ghostty), and a find bar for the focused pane. Every pane is its
	// own shell on the CardputerZero.

	type Tab = { id: number; layout: Layout; focused: number };

	const first = new_pane();
	let tabs = $state<Tab[]>([{ id: first, layout: { pane: first }, focused: first }]);
	let current = $state(0);
	let titles = $state<Record<number, string>>({});
	const apis = new Map<number, PaneApi>();

	const tab = $derived(tabs[current]);
	const fallback_title = `${session.user}@${session.hostname}`;
	const title_of = (pane: number) => titles[pane] || fallback_title;
	/** A tab shows the folder, as macOS Terminal does: "user@host: ~/src/app" -> "app". */
	const tab_label = (pane: number) => {
		const s = title_of(pane);
		const where = s.includes(':') ? s.slice(s.indexOf(':') + 1).trim() : s;
		return where === '~' || !where.includes('/') ? where : where.slice(where.lastIndexOf('/') + 1) || '/';
	};

	function focus_pane(tab_i: number, pane: number) {
		tabs[tab_i].focused = pane;
		tick().then(() => apis.get(pane)?.focus());
	}

	function new_tab() {
		const pane = new_pane();
		tabs.push({ id: pane, layout: { pane }, focused: pane });
		current = tabs.length - 1;
		find_open = false;
	}

	function split_focused(dir: 'row' | 'col') {
		const r = split(tab.layout, tab.focused, dir);
		tab.layout = r.layout;
		focus_pane(current, r.pane);
	}

	/** Remove a pane (its shell ended or it was closed); an empty tab goes, and the last one closes the window. */
	function remove_pane(tab_id: number, pane: number) {
		const i = tabs.findIndex((x) => x.id === tab_id);
		if (i < 0) return;
		const layout = close(tabs[i].layout, pane);
		delete titles[pane];
		if (!layout) {
			tabs.splice(i, 1);
			if (!tabs.length) return close_app('terminal');
			current = Math.min(current, tabs.length - 1);
			focus_pane(current, tabs[current].focused);
			return;
		}
		tabs[i].layout = layout;
		if (tabs[i].focused === pane) focus_pane(i, panes_of(layout)[0]);
	}

	function close_tab(i: number) {
		tabs.splice(i, 1);
		if (!tabs.length) return close_app('terminal');
		current = Math.min(current, tabs.length - 1);
		focus_pane(current, tabs[current].focused);
	}

	/* ---- find ---- */

	let find_open = $state(false);
	let query = $state('');
	let result = $state({ index: -1, count: 0 });
	let find_input = $state<HTMLInputElement>();

	function open_find() {
		find_open = true;
		tick().then(() => find_input?.select());
	}

	function close_find() {
		find_open = false;
		apis.get(tab.focused)?.clear_find();
		apis.get(tab.focused)?.focus();
	}

	function find(step?: 'next' | 'prev') {
		apis.get(tab.focused)?.find(query, step);
	}

	/** Window shortcuts, also when the terminal has the keyboard. */
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

{#snippet node(tab_i: number, tab_id: number, l: Layout)}
	{#if 'pane' in l}
		{@const pane = l.pane}
		<TermPane
			focused={tab_i === current && tabs[tab_i].focused === pane}
			onfocus={() => (tabs[tab_i].focused = pane)}
			onexit={() => remove_pane(tab_id, pane)}
			ontitle={(s) => (titles[pane] = s)}
			onresults={(index, count) => tab_i === current && tabs[tab_i].focused === pane && (result = { index, count })}
			register={(a) => (a ? apis.set(pane, a) : apis.delete(pane))}
			onshortcut={shortcut}
		/>
	{:else}
		<div class="split {l.dir}">
			<div class="cell">{@render node(tab_i, tab_id, l.a)}</div>
			<span class="divider"></span>
			<div class="cell">{@render node(tab_i, tab_id, l.b)}</div>
		</div>
	{/if}
{/snippet}

<div class="terminal">
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
			{#each tabs as tb, i (tb.id)}
				<div class="tab" class:on={i === current} role="tab" tabindex="0" aria-selected={i === current} onclick={() => ((current = i), focus_pane(i, tb.focused))} onkeydown={(e) => e.key === 'Enter' && (current = i)}>
					<button class="x" aria-label={t('term.close_tab')} onclick={(e) => (e.stopPropagation(), close_tab(i))}>
						<svg viewBox="0 0 10 10" width="8" height="8" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
					</button>
					<span class="label" title={title_of(tb.focused)}>{tab_label(tb.focused)}</span>
				</div>
			{/each}
		</div>
	{/if}

	<div class="body">
		{#each tabs as tb, i (tb.id)}
			<div class="tab-body" class:hidden={i !== current}>{@render node(i, tb.id, tb.layout)}</div>
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
						else shortcut(e);
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
		display: flex;

		&.hidden {
			visibility: hidden;
			pointer-events: none;
		}
	}

	.split {
		flex: 1;
		min-width: 0;
		min-height: 0;
		display: flex;

		&.col {
			flex-direction: column;
		}
	}

	.cell {
		flex: 1;
		min-width: 0;
		min-height: 0;
		display: flex;
	}

	.divider {
		flex: none;
		background: var(--zd-hairline-strong);

		.row > & {
			width: 1px;
		}
		.col > & {
			height: 1px;
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
