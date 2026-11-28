// What each app needs before its window can paint complete: its code chunk
// and its first data. Both are fetched on idle after the desktop loads and
// again (if stale) when the pointer reaches the app's icon.
import { api } from '🍎/lib/api.ts';
import { decode, icon_url, learn_device_icons, type DeviceAppIcon } from '🍎/lib/icons.svelte.ts';
import { age, cached, prefetch, revalidate } from '🍎/lib/swr.svelte.ts';
import { ART } from '🍎/components/SVG/AppIcon.svelte';
import { OFFICIAL_ART } from '🍎/components/SVG/DeviceIcon.svelte';
import { icon_look, preferences } from '🍎/state/preferences.svelte.ts';
import { load_web_apps, web_apps } from '🍎/state/apps.svelte.ts';
import { list_audio } from '🍎/components/apps/Music/library.ts';
import { session } from '🍎/state/session.svelte.ts';

// ---- code ----
/** modules already loaded, so a window renders them in its first frame */
export const loaded: Record<string, { default: unknown }> = {};
const remember =
	<T extends { default: unknown }>(key: string, f: () => Promise<T>) =>
	() =>
		f().then((m) => ((loaded[key] = m), m));

export const app_loaders = {
	device: remember('device', () => import('../components/apps/Device/Device.svelte')),
	desktop: remember('desktop', () => import('../components/apps/LinuxDesktop/LinuxDesktop.svelte')),
	finder: remember('finder', () => import('../components/apps/Finder/Finder.svelte')),
	terminal: remember('terminal', () => import('../components/apps/Terminal/Terminal.svelte')),
	about: remember('about', () => import('../components/apps/About/About.svelte')),
	settings: remember('settings', () => import('../components/apps/Settings/Settings.svelte')),
	appstore: remember('appstore', () => import('../components/apps/AppStore/AppStore.svelte')),
	music: remember('music', () => import('../components/apps/Music/Music.svelte')),
	piano: remember('piano', () => import('../components/apps/Piano/Piano.svelte')),
	recorder: remember('recorder', () => import('../components/apps/Recorder/Recorder.svelte')),
	zagent: remember('zagent', () => import('../components/apps/Zagent/Zagent.svelte')),
	web: remember('web', () => import('../components/apps/WebApp/WebApp.svelte')),
	dev: remember('dev', () => import('../components/apps/DeviceApp/DeviceApp.svelte')),
} as const;

const loader_for = (id: string) =>
	(app_loaders as Record<string, () => Promise<unknown>>)[id.startsWith('web:') ? 'web' : id.startsWith('dev:') ? 'dev' : id];

export function preload_code(id: string) {
	loader_for(id)?.().catch(() => {});
}

// ---- icons ----
export type DeviceApp = DeviceAppIcon & { name: string; terminal: boolean; hasIcon: boolean; categories?: string[] };

/** Icon URL a device app is drawn with in the Dock and Apps. */
export function device_icon_src(a: DeviceApp, mode = icon_look()): string | undefined {
	return OFFICIAL_ART.has(a.id) ? icon_url(`off-${a.id}-${mode === 'dark' ? 'dark' : 'light'}`) : a.icon;
}

function builtin_icon_srcs(mode = icon_look()): string[] {
	const m = mode === 'dark' ? 'dark' : 'light';
	return [...new Set(Object.values(ART))].map((n) => icon_url(`${n}-${m}`)).concat(icon_url('trash-empty'), icon_url('trash-full'));
}

/** Decode every Dock and Apps icon, so opening Apps paints them all at once. */
export function preload_icons(apps: DeviceApp[] = cached<DeviceApp[]>('apps') ?? []) {
	for (const src of builtin_icon_srcs()) decode(src);
	for (const a of apps) {
		const src = device_icon_src(a);
		if (src) decode(src);
	}
}

/** Zagent speaks English and Chinese; other ZeroDesk languages get English. */
export function zagent_lang(lang: string): 'en' | 'zh' {
	return lang === 'zh' ? 'zh' : 'en';
}

// ---- data (keys are shared with the apps that read them) ----
export const data = {
	settings: () => api('/api/settings'),
	wifi: () => api('/api/wifi/networks'),
	music: () => list_audio('~/Music'),
	recordings: () => list_audio('~/Recordings', 1),
	peaks: (path: string) => () => api<{ duration: number; peaks: number[] }>(`/api/audio/peaks?path=${encodeURIComponent(path)}`),
	zagent: () => api(`/api/zagent/settings?lang=${zagent_lang(preferences.lang)}`),
	apps: () => api<DeviceApp[]>('/api/apps'),
	fs: (path: string) => () => api(`/api/fs/list?path=${encodeURIComponent(path)}`),
};

export function preload_data(id: string) {
	switch (id) {
		case 'settings':
			prefetch('settings', data.settings);
			prefetch('wifi', data.wifi, 15000);
			break;
		case 'music':
			prefetch('music', data.music, 15000);
			break;
		case 'recorder': {
			// the list, then the waveform of the newest memo (the one the window opens on)
			const warm = (list: { path: string; modified: number }[] = []) => {
				const newest = [...list].sort((a, b) => b.modified - a.modified)[0];
				if (newest) prefetch(`peaks:${newest.path}`, data.peaks(newest.path), Infinity);
			};
			if (age('recordings') < 15000) warm(cached('recordings'));
			else revalidate('recordings', data.recordings).then(warm).catch(() => {});
			break;
		}
		case 'zagent':
			prefetch('zagent', data.zagent, 15000);
			break;
		case 'finder':
			prefetch(`fs:${session.home}`, data.fs(session.home), 5000);
			break;
		case 'launchpad':
			prefetch('apps', data.apps, 30000);
			break;
	}
}

/** Pointer reached an app's icon (Dock, Apps): get it ready. */
export function preload_app(id: string) {
	preload_code(id);
	preload_data(id);
}

// the app list from this tab's last visit tells the Dock how to draw icons right away
learn_device_icons(cached<DeviceApp[]>('apps') ?? []);

/** After the desktop has painted: the app list and icons, every app's code, and the cheap data. */
export function preload_everything() {
	revalidate('apps', data.apps)
		.then((list) => {
			learn_device_icons(list);
			preload_icons(list);
		})
		.catch(() => {});
	// web-app icons (Apps shows them too)
	load_web_apps().then(() => {
		const m = icon_look() === 'dark' ? 'dark' : 'light';
		for (const id of Object.keys(web_apps)) decode(`/api/webapps/${encodeURIComponent(id)}/icon?scheme=${m}`);
	});
	const idle = (f: () => void) =>
		'requestIdleCallback' in window ? requestIdleCallback(f, { timeout: 2000 }) : setTimeout(f, 300);
	idle(() => {
		for (const id of Object.keys(app_loaders)) preload_code(id);
		for (const id of ['launchpad', 'settings', 'finder', 'music', 'recorder', 'zagent']) preload_data(id);
	});
}
