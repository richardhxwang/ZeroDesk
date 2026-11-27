import { apps_config } from '🍎/configs/apps/apps-config';
import { api } from '🍎/lib/api.ts';
import { create_app_config, type AppConfig } from '🍎/helpers/create-app-config.ts';
import { t } from '🍎/state/i18n.svelte.ts';
import { launchpad, pinned } from '🍎/state/launchpad.svelte.ts';

/** Built-in windows (Cardputer, Finder, …). */
export type BuiltinID = keyof typeof apps_config;
/** A window id: a built-in, `dev:<device app id>` for a device app running on the computer,
 * or `web:<app id>` for an app's own web UI (docs/APP-SPEC.md). */
export type AppID = string;

export const builtin_ids = Object.keys(apps_config) as BuiltinID[];
const record = <T>(v: T) => Object.fromEntries(builtin_ids.map((id) => [id, v])) as Record<AppID, T>;

/** Device apps currently open as their own window on the computer. */
export type DeviceWindow = { app: string; name: string };
export const device_windows = $state<Record<string, DeviceWindow>>({});

export const is_device_window = (id: AppID) => id.startsWith('dev:');

/** Apps that ship a ZeroDesk web UI (GET /api/webapps). */
export type WebAppInfo = {
	id: string;
	name: string;
	description?: string;
	width: number;
	height: number;
	min_width: number;
	min_height: number;
	resizable: boolean;
	titlebar: 'standard' | 'app';
	permissions: string[];
	backend: boolean;
	/** the device app (.desktop id) this window replaces */
	device_app?: string;
};
export const web_apps = $state<Record<string, WebAppInfo>>({});
/** open web-app windows; value = title set by the page (zd.window.setTitle) */
export const web_windows = $state<Record<string, { title?: string }>>({});
export const is_web_window = (id: AppID) => id.startsWith('web:');

let web_loading: Promise<void> | null = null;
export function load_web_apps(force = false): Promise<void> {
	if (web_loading && !force) return web_loading;
	web_loading = api<WebAppInfo[]>('/api/webapps')
		.then((list) => {
			for (const k of Object.keys(web_apps)) if (!list.some((a) => a.id === k)) delete web_apps[k];
			for (const a of list) web_apps[a.id] = a;
		})
		.catch(() => {
			web_loading = null;
		});
	return web_loading;
}

export const web_for_device = (device_id: string) => Object.values(web_apps).find((a) => a.device_app === device_id);

/** Window operations an embedded page may ask for (move by drag, resize, zoom). */
export const window_controls: Record<AppID, { move_by(dx: number, dy: number): void; resize(w: number, h: number): void; zoom(): void }> = {};

const device_config = create_app_config({
	title: 'app.device',
	width: 880,
	height: 560,
	min_width: 480,
	min_height: 340,
});

export function config_of(id: AppID): Required<Pick<AppConfig, 'resizable' | 'expandable' | 'width' | 'height' | 'min_width' | 'min_height'>> & AppConfig {
	if (is_web_window(id)) {
		const a = web_apps[id.slice(4)];
		if (a) {
			return create_app_config({
				title: 'app.device',
				width: a.width,
				height: a.height,
				min_width: a.min_width,
				min_height: a.min_height,
				resizable: a.resizable,
				expandable: a.resizable,
			});
		}
	}
	return (apps_config as Record<string, ReturnType<typeof create_app_config>>)[id] ?? device_config;
}

export function title_of(id: AppID): string {
	if (is_web_window(id)) return web_windows[id]?.title || web_apps[id.slice(4)]?.name || '';
	if (is_device_window(id)) {
		const app = id.slice(4);
		return device_windows[id]?.name ?? pinned.apps.find((a) => a.id === app)?.name ?? '';
	}
	return t(config_of(id).title);
}

/** Device apps that have a native window here: desktop-file id -> built-in window. */
export const native_of: Record<string, BuiltinID> = {
	files: 'finder',
	...Object.fromEntries(
		Object.entries(apps_config as Record<string, AppConfig>)
			.filter(([, c]) => c.native_for)
			.map(([id, c]) => [c.native_for!, id as BuiltinID]),
	),
};

/** Open a device app: its native window when there is one, otherwise a virtual screen. */
export function open_device_app(app: { id: string; name: string }) {
	if (is_web_window(app.id)) return open_web_app(app.id.slice(4));
	const native = native_of[app.id];
	if (native) return open_app(native);
	const web = web_for_device(app.id);
	if (web) return open_web_app(web.id);
	const wid = `dev:${app.id}`;
	device_windows[wid] = { app: app.id, name: app.name };
	open_app(wid);
}

/** Open an app's web UI in its own window. */
export function open_web_app(id: string) {
	const wid = `web:${id}`;
	if (!web_windows[wid]) web_windows[wid] = {};
	open_app(wid);
}

export const apps = $state({
	open: { ...record(false), device: true } as Record<AppID, boolean>,
	/** minimized windows stay open but are hidden into the dock */
	minimized: record(false) as Record<AppID, boolean>,

	active: 'device' as AppID | '',

	/** Maximum zIndex for the active app. Starts at -2 so the first focus becomes 0. */
	active_z_index: -2,
	z_indices: record(0) as Record<AppID, number>,

	is_being_dragged: false as boolean,
	fullscreen: record(false) as Record<AppID, boolean>,
});

export function open_app(id: AppID) {
	// opening an app (from the Dock, Finder, …) takes focus away from Launchpad, like macOS
	launchpad.open = false;
	apps.open[id] = true;
	apps.minimized[id] = false;
	apps.active = id;
}

/** The Settings pane to show when Settings next looks (set by open_settings). */
export const settings_nav = $state({ pane: '' });

/** Open Settings at one pane, e.g. 'terminal' from the Terminal's toolbar. */
export function open_settings(pane: string) {
	settings_nav.pane = pane;
	open_app('settings');
}

export function close_app(id: AppID) {
	apps.open[id] = false;
	apps.minimized[id] = false;
	apps.fullscreen[id] = false;
	if (is_device_window(id)) {
		// let the close animation finish before the window record goes away
		setTimeout(() => !apps.open[id] && delete device_windows[id], 400);
	}
	if (is_web_window(id)) {
		setTimeout(() => !apps.open[id] && delete web_windows[id], 400);
	}
	if (apps.active === id) {
		// focus the top-most remaining window, like macOS
		const next = open_ids()
			.filter((a) => apps.open[a] && !apps.minimized[a])
			.sort((a, b) => apps.z_indices[b] - apps.z_indices[a])[0];
		apps.active = next ?? '';
	}
}

export function minimize_app(id: AppID) {
	apps.minimized[id] = true;
	if (apps.active === id) {
		const next = open_ids()
			.filter((a) => a !== id && apps.open[a] && !apps.minimized[a])
			.sort((a, b) => apps.z_indices[b] - apps.z_indices[a])[0];
		apps.active = next ?? '';
	}
}

/** Every window id that can be open right now. */
export function open_ids(): AppID[] {
	return [...builtin_ids.filter((id) => id !== 'launchpad'), ...Object.keys(device_windows), ...Object.keys(web_windows)];
}
