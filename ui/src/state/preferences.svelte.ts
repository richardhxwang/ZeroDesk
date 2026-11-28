import { persisted } from './persisted.svelte.ts';

/** Daytime 07:00–19:00 is light, the rest of the day dark. */
function scheme_for_time(d = new Date()): 'light' | 'dark' {
	const h = d.getHours();
	return h >= 7 && h < 19 ? 'light' : 'dark';
}

export type Scheme = 'light' | 'dark';
export type Appearance = 'auto' | 'light' | 'dark';
export type Lang = 'zh' | 'en' | 'ja';

/** The interface languages, each named in itself, in the order they cycle. */
export const LANGS: { value: Lang; label: string }[] = [
	{ value: 'zh', label: '简体中文' },
	{ value: 'en', label: 'English' },
	{ value: 'ja', label: '日本語' },
];

/** The locale for dates and numbers in the current language. */
export function locale_of(lang: Lang = preferences.lang): string {
	return { zh: 'zh-CN', en: 'en-US', ja: 'ja-JP' }[lang] ?? 'en-US';
}
export type CursorStyle = 'block' | 'bar' | 'underline';

/** Desktop colours used instead of the device's wallpaper: [light, dark]. */
export const WALLPAPER_TONES = {
	graphite: ['#c9cacc', '#1c1c1e'],
	stone: ['#d4d1cb', '#262523'],
	sand: ['#dccfb9', '#2a251e'],
	clay: ['#d2ab95', '#2e221d'],
	sage: ['#b9c3b0', '#1f241e'],
	rose: ['#d8bcb6', '#2b2123'],
} as const;
export type WallpaperTone = keyof typeof WALLPAPER_TONES;

// Keys stay flat: persisted() merges a stored value with these defaults one level deep.
export const preferences = persisted('zerodesk:preferences', {
	reduced_motion: matchMedia('(prefers-reduced-motion)').matches as boolean,
	/** what the user picked; `theme.scheme` is the scheme actually shown */
	appearance: 'auto' as Appearance,
	theme: { scheme: scheme_for_time() as Scheme },
	lang: 'en' as Lang,
	device_scale: 3 as number,
	screen_smooth: true as boolean,
	/** open the Cardputer window (the device's live screen) after signing in */
	open_cardputer: true as boolean,
	/** 'device' (the CardputerZero's own wallpaper) or a plain tone */
	wallpaper: 'device' as 'device' | WallpaperTone,
	/** 'zerodesk' follows light and dark; otherwise a downloaded scheme's id */
	term_theme: 'zerodesk' as string,
	/** 'system', 'local' (term_font_local) or a downloaded font's id */
	term_font: 'system' as string,
	/** the name of a font installed on this computer */
	term_font_local: '' as string,
	term_font_size: 13 as number,
	term_cursor: 'block' as CursorStyle,
	term_cursor_blink: true as boolean,
	/** Terminal settings were opened once (the sidebar dot goes away) */
	seen_terminal_settings: false as boolean,
});

function apply_scheme(next: Scheme) {
	if (preferences.theme.scheme === next) return;
	const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown };
	if (doc.startViewTransition && !preferences.reduced_motion) {
		doc.startViewTransition(() => (preferences.theme.scheme = next));
	} else {
		preferences.theme.scheme = next;
	}
}

$effect.root(() => {
	// follow the appearance choice; in auto mode re-check the clock every minute
	$effect(() => {
		const a = preferences.appearance;
		apply_scheme(a === 'auto' ? scheme_for_time() : a);
		if (a !== 'auto') return;
		const timer = setInterval(() => apply_scheme(scheme_for_time()), 60_000);
		return () => clearInterval(timer);
	});
	$effect(() => {
		const { classList } = document.body;
		classList.remove('light', 'dark');
		classList.add(preferences.theme.scheme);
		document.documentElement.style.colorScheme = preferences.theme.scheme;
	});
	$effect(() => {
		document.documentElement.lang = { zh: 'zh-CN', en: 'en', ja: 'ja' }[preferences.lang] ?? 'en';
	});
});

/** App icons have a light and a dark version, like macOS 26/27; they follow the scheme. */
export function icon_look(): 'default' | 'dark' {
	return preferences.theme.scheme === 'dark' ? 'dark' : 'default';
}

/** Flip between light and dark, leaving auto mode. */
export function toggle_scheme() {
	preferences.appearance = preferences.theme.scheme === 'light' ? 'dark' : 'light';
}
