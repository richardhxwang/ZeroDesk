import { api, api_error, ApiError } from '🍎/lib/api.ts';
import { preferences } from '🍎/state/preferences.svelte.ts';
import { session } from '🍎/state/session.svelte.ts';

/**
 * Built-in wallpapers: original vector art from design/wallpaper-gen, one
 * light and one dark version each (ui/public/wallpapers/<id>-<scheme>.svg).
 */
export const WALLPAPER_SETS = ['dunes', 'ridges', 'meadow', 'topo', 'circles', 'keys'] as const;
export type WallpaperSet = (typeof WALLPAPER_SETS)[number];

export function is_set(id: string): id is WallpaperSet {
	return (WALLPAPER_SETS as readonly string[]).includes(id);
}

export function set_url(id: WallpaperSet, scheme: 'light' | 'dark'): string {
	return `/wallpapers/${id}-${scheme}.svg`;
}

export const DEVICE_WALLPAPER = '/api/wallpaper';

export function custom_url(rev: number): string {
	return `/api/wallpaper/custom?v=${rev}`;
}

/** The long edge a picture is scaled down to before it is sent to the device. */
const MAX_EDGE = 2560;

/**
 * Decodes the picture here (the device never decodes images), scales it so the
 * long edge is at most MAX_EDGE and encodes it as WebP, or JPEG where the
 * browser cannot write WebP.
 */
async function prepare(source: Blob): Promise<Blob> {
	let bmp: ImageBitmap;
	try {
		bmp = await createImageBitmap(source);
	} catch {
		throw new Error('unsupported image');
	}
	const k = Math.min(1, MAX_EDGE / Math.max(bmp.width, bmp.height));
	const w = Math.max(1, Math.round(bmp.width * k));
	const h = Math.max(1, Math.round(bmp.height * k));
	const canvas = document.createElement('canvas');
	canvas.width = w;
	canvas.height = h;
	const ctx = canvas.getContext('2d')!;
	ctx.imageSmoothingQuality = 'high';
	ctx.drawImage(bmp, 0, 0, w, h);
	bmp.close();
	const encode = (type: string, q: number) => new Promise<Blob | null>((ok) => canvas.toBlob(ok, type, q));
	const webp = await encode('image/webp', 0.86);
	if (webp?.type === 'image/webp') return webp;
	const jpeg = await encode('image/jpeg', 0.88);
	if (!jpeg) throw new Error('could not encode the image');
	return jpeg;
}

/** Stores a picture as the custom wallpaper; returns its revision. */
export async function upload_wallpaper(source: Blob): Promise<number> {
	const body = await prepare(source);
	const res = await fetch('/api/wallpaper/custom', { method: 'PUT', credentials: 'same-origin', body });
	if (res.status === 401) {
		session.authed = false;
		throw new ApiError(401, 'unauthorized');
	}
	if (!res.ok) throw await api_error(res);
	const { rev } = (await res.json()) as { rev: number };
	return rev;
}

/** A picture from the device's files, read through Finder's API. */
export async function fetch_device_file(path: string): Promise<Blob> {
	const res = await fetch(`/api/fs/get?path=${encodeURIComponent(path)}`, { credentials: 'same-origin' });
	if (!res.ok) throw await api_error(res);
	return res.blob();
}

/** Stores the picture and shows it on the desktop. */
export async function use_as_wallpaper(source: Blob): Promise<void> {
	preferences.wallpaper_rev = await upload_wallpaper(source);
	preferences.wallpaper = 'custom';
}

export async function remove_custom_wallpaper(): Promise<void> {
	await api('/api/wallpaper/custom', { method: 'DELETE' });
}

/** Image files a browser can show as a background. */
export const IMAGE_EXT = /\.(jpe?g|png|webp|gif|avif|bmp)$/i;
