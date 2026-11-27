import type { ITheme } from '@xterm/xterm';
import { api, post } from '🍎/lib/api.ts';

// Downloadable Terminal presets (agent/presets.go). The list, with a few
// colours per scheme for the tiles, comes from the device; a preset's files
// are downloaded to the device the first time someone picks it, and from then
// on every browser that signs in to it can use them.

export type Mode = 'light' | 'dark';

export type PresetTheme = {
	id: string;
	name: string;
	/**
	 * Per variant: background, foreground, red, green, yellow, blue, magenta,
	 * cyan. A family with both variants follows Appearance.
	 */
	preview: Partial<Record<Mode, string[]>>;
	size: number;
	installed: boolean;
};

/** The variant of a scheme to show for the current appearance. */
export function variant_of(th: Pick<PresetTheme, 'preview'>, scheme: Mode): Mode {
	return th.preview[scheme] ? scheme : scheme === 'dark' ? 'light' : 'dark';
}

export type PresetFont = {
	id: string;
	name: string;
	family: string;
	/** has the Powerline and icon glyphs that prompt themes use */
	nerd: boolean;
	license: string;
	size: number;
	installed: boolean;
};

export const presets = $state({
	themes: [] as PresetTheme[],
	fonts: [] as PresetFont[],
	loaded: false,
	/** ids being downloaded */
	busy: {} as Record<string, boolean>,
	/** id → message of the last failed download */
	failed: {} as Record<string, string>,
});

let loading: Promise<void> | null = null;

export function load_presets(force = false): Promise<void> {
	if (loading && !force) return loading;
	loading = api<{ themes: PresetTheme[]; fonts: PresetFont[] }>('/api/presets')
		.then((d) => {
			presets.themes = d.themes;
			presets.fonts = d.fonts;
			presets.loaded = true;
		})
		.catch((e) => {
			loading = null;
			throw e;
		});
	return loading;
}

function find(id: string): PresetTheme | PresetFont | undefined {
	return presets.themes.find((p) => p.id === id) ?? presets.fonts.find((p) => p.id === id);
}

/** Download a preset to the device. Resolves true once it can be used. */
export async function get_preset(id: string): Promise<boolean> {
	const p = find(id);
	if (!p) return false;
	if (p.installed) return true;
	presets.busy[id] = true;
	delete presets.failed[id];
	try {
		await post(`/api/presets/${encodeURIComponent(id)}`, {});
		p.installed = true;
		return true;
	} catch (e) {
		presets.failed[id] = (e as Error).message;
		return false;
	} finally {
		delete presets.busy[id];
	}
}

export async function remove_preset(id: string): Promise<void> {
	const p = find(id);
	await api(`/api/presets/${encodeURIComponent(id)}`, { method: 'DELETE' });
	if (p) p.installed = false;
	themes.delete(`${id}/light`);
	themes.delete(`${id}/dark`);
	fonts.delete(id);
}

/* ---- colour schemes ---- */

const themes = new Map<string, Promise<ITheme>>();

/** The xterm.js theme of a downloaded scheme's variant (Windows Terminal format on disk). */
export function theme_of(id: string, mode: Mode): Promise<ITheme> {
	const key = `${id}/${mode}`;
	let t = themes.get(key);
	if (!t) {
		t = api<Record<string, string>>(`/api/presets/${encodeURIComponent(id)}/${mode}.json`).then((c) => ({
			background: c.background,
			foreground: c.foreground,
			cursor: c.cursorColor ?? c.foreground,
			cursorAccent: c.background,
			selectionBackground: c.selectionBackground,
			black: c.black,
			red: c.red,
			green: c.green,
			yellow: c.yellow,
			blue: c.blue,
			magenta: c.purple,
			cyan: c.cyan,
			white: c.white,
			brightBlack: c.brightBlack,
			brightRed: c.brightRed,
			brightGreen: c.brightGreen,
			brightYellow: c.brightYellow,
			brightBlue: c.brightBlue,
			brightMagenta: c.brightPurple,
			brightCyan: c.brightCyan,
			brightWhite: c.brightWhite,
		}));
		t.catch(() => themes.delete(key));
		themes.set(key, t);
	}
	return t;
}

/* ---- fonts ---- */

const fonts = new Map<string, Promise<string>>();

/** Register a downloaded font with the page; resolves to its CSS family name. */
export function font_family_of(font: PresetFont): Promise<string> {
	let f = fonts.get(font.id);
	if (!f) {
		// a family name of our own, so a copy installed on the computer is not shadowed
		const family = `ZeroDesk ${font.name}`;
		const ext = font.nerd ? 'ttf' : 'woff2';
		const faces = [400, 700].map(
			(weight) =>
				new FontFace(family, `url(/api/presets/${encodeURIComponent(font.id)}/${weight}.${ext})`, {
					weight: String(weight),
					display: 'block',
				}),
		);
		f = Promise.all(faces.map((face) => face.load())).then((loaded) => {
			for (const face of loaded) document.fonts.add(face);
			return family;
		});
		f.catch(() => fonts.delete(font.id));
		fonts.set(font.id, f);
	}
	return f;
}

/**
 * Whether a font installed on this computer answers to `family`. Browsers
 * cannot list local fonts, so compare text widths against the generic
 * monospace face: an unknown name falls back to it and measures the same.
 */
export function local_font_available(family: string): boolean {
	const name = family.trim();
	if (!name) return false;
	const ctx = document.createElement('canvas').getContext('2d');
	if (!ctx) return true;
	const sample = 'mmmmmmmmmmlli10WQ@{}';
	return ['monospace', 'serif'].some((base) => {
		ctx.font = `20px ${base}`;
		const plain = ctx.measureText(sample).width;
		ctx.font = `20px "${name.replaceAll('"', '')}", ${base}`;
		return ctx.measureText(sample).width !== plain;
	});
}
