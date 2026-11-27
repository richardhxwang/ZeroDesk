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

export type Rect = { x: number; y: number; w: number; h: number };

/**
 * Lay the panes out flat, as fractions of the tab. The Terminal renders them
 * in one keyed list, so splitting or closing a pane moves the others instead
 * of recreating them (which would end their shells).
 */
export function tile(l: Layout): { panes: { pane: number; rect: Rect }[]; dividers: { dir: 'row' | 'col'; rect: Rect }[] } {
	const panes: { pane: number; rect: Rect }[] = [];
	const dividers: { dir: 'row' | 'col'; rect: Rect }[] = [];
	const walk = (n: Layout, r: Rect) => {
		if ('pane' in n) return void panes.push({ pane: n.pane, rect: r });
		if (n.dir === 'row') {
			const w = r.w / 2;
			walk(n.a, { ...r, w });
			walk(n.b, { ...r, x: r.x + w, w });
			dividers.push({ dir: 'row', rect: { x: r.x + w, y: r.y, w: 0, h: r.h } });
		} else {
			const h = r.h / 2;
			walk(n.a, { ...r, h });
			walk(n.b, { ...r, y: r.y + h, h });
			dividers.push({ dir: 'col', rect: { x: r.x, y: r.y + h, w: r.w, h: 0 } });
		}
	};
	walk(l, { x: 0, y: 0, w: 1, h: 1 });
	return { panes, dividers };
}

/**
 * The pane that takes the focus when `target` closes: the nearest one in its
 * sibling subtree (the pane that grows into its space), not the first pane of the tab.
 */
export function neighbour(l: Layout, target: number): number | null {
	if ('pane' in l) return null;
	const in_a = panes_of(l.a).includes(target);
	if (!in_a && !panes_of(l.b).includes(target)) return null;
	const child = in_a ? l.a : l.b;
	if ('pane' in child) {
		const sib = panes_of(in_a ? l.b : l.a);
		return in_a ? sib[0] : sib[sib.length - 1];
	}
	return neighbour(child, target);
}
