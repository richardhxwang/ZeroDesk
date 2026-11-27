// The open Terminal window's commands, for the menu bar's Shell menu.
// The Terminal sets them when it opens and clears them when it closes.

export type TerminalCommands = {
	new_tab(): void;
	split(dir: 'row' | 'col'): void;
	close_pane(): void;
	find(): void;
};

export const terminal = $state<{ commands: TerminalCommands | null }>({ commands: null });

/** ⌘ on a Mac, Ctrl+Shift elsewhere (plain Ctrl belongs to the shell). */
export const IS_MAC = /Mac|iPhone|iPad/.test(navigator.platform);
