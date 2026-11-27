// Panes of one Terminal tab, as in Ghostty: a split holds two children side
// by side ('row') or one above the other ('col'), and splits nest.

export type Layout = { pane: number } | { dir: 'row' | 'col'; a: Layout; b: Layout };

let next_id = 1;
export const new_pane = (): number => next_id++;

export function panes_of(l: Layout): number[] {
	return 'pane' in l ? [l.pane] : [...panes_of(l.a), ...panes_of(l.b)];
}

/** Put a new pane next to `target`; returns the new layout and the new pane's id. */
export function split(l: Layout, target: number, dir: 'row' | 'col'): { layout: Layout; pane: number } {
	const pane = new_pane();
	const walk = (n: Layout): Layout =>
		'pane' in n ? (n.pane === target ? { dir, a: n, b: { pane } } : n) : { dir: n.dir, a: walk(n.a), b: walk(n.b) };
	return { layout: walk(l), pane };
}

/** Remove a pane; its sibling takes the split's place. Null when it was the last one. */
export function close(l: Layout, target: number): Layout | null {
	if ('pane' in l) return l.pane === target ? null : l;
	const a = close(l.a, target);
	const b = close(l.b, target);
	if (!a) return b;
	if (!b) return a;
	return { dir: l.dir, a, b };
}
