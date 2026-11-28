import { api_error, ApiError } from '🍎/lib/api.ts';
import { install_progress } from '🍎/lib/install_progress.ts';
import { revalidate } from '🍎/lib/swr.svelte.ts';
import { as_admin, Cancelled } from '🍎/state/admin.svelte.ts';
import { data } from '🍎/state/app_preload.ts';
import { t } from '🍎/state/i18n.svelte.ts';
import { notify } from '🍎/state/notifications.svelte.ts';

// The App Store's catalogue and running downloads, kept outside the window
// so closing it does not lose an install in progress.

export type StoreApp = {
	id: string;
	package: string;
	title: string;
	summary: string;
	description: string;
	locales?: Record<string, { title?: string; summary?: string; description?: string }>;
	categories: string[];
	author: string;
	version: string;
	published_at: string;
	updated_at: string;
	license: string;
	source_repo: string;
	permissions?: Record<string, boolean>;
	icon: string;
	screenshots?: string[];
	size: number;
	installed?: string;
	update?: boolean;
	desktop_id?: string;
};

/** queued: waiting its turn (one install at a time); auth: asking for the password */
export type StoreTask = { pct: number; phase: 'queued' | 'auth' | 'waiting' | 'progress' };

export const store_state = $state({
	loaded: false,
	apps: [] as StoreApp[],
	tasks: {} as Record<string, StoreTask>,
	/** an install that failed, shown as an alert by the App Store window */
	alert: null as { title: string; message: string } | null,
});

// The Zero installs one package at a time (dpkg holds a system-wide lock), so
// the App Store queues the rest, like the Mac App Store's "Waiting". The queue
// lives here, outside the window, so closing the window does not stop it.

type Job = { app: StoreApp; name: string };
const queue: Job[] = [];
/** apps cancelled while waiting for another device's install to finish */
const cancelled = new Set<string>();
let draining = false;

/** Get or update an app: start now, or wait for the ones before it. */
export function enqueue(app: StoreApp, name: string) {
	if (store_state.tasks[app.id]) return;
	store_state.tasks[app.id] = { pct: 0, phase: 'queued' };
	queue.push({ app, name });
	void drain();
}

/** Take a waiting app out of the queue (one that has started cannot be stopped). */
export function cancel_queued(id: string) {
	if (store_state.tasks[id]?.phase !== 'queued') return;
	const i = queue.findIndex((j) => j.app.id === id);
	if (i >= 0) queue.splice(i, 1);
	else cancelled.add(id); // its turn came but the Zero is busy: stop retrying
	delete store_state.tasks[id];
}

async function drain() {
	if (draining) return;
	draining = true;
	try {
		for (let job = queue.shift(); job; job = queue.shift()) await install(job);
	} finally {
		draining = false;
	}
}

const BUSY = 409; // another install on the Zero, e.g. from another browser
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function install({ app: a, name }: Job) {
	const set = (t: StoreTask) => (store_state.tasks[a.id] = t);
	try {
		let res: Response | null = null;
		while (!res) {
			try {
				res = await as_admin(
					async (password) => {
						set({ pct: 0, phase: password ? 'waiting' : 'auth' });
						const r = await fetch('/api/store/install', {
							method: 'POST',
							headers: { 'Content-Type': 'application/json' },
							body: JSON.stringify({ id: a.id, password }),
						});
						if (!r.ok) throw await api_error(r);
						set({ pct: 0, phase: 'waiting' });
						return r;
					},
					{ title: t('admin.install_title'), action: t('admin.install_ok'), app: { name, icon: a.icon }, source: 'App Store', short: t(a.update ? 'store.update' : 'phone.install') },
				);
			} catch (e) {
				// busy elsewhere: keep waiting (unless the user cancelled meanwhile)
				if (!(e instanceof ApiError && e.status === BUSY)) throw e;
				set({ pct: 0, phase: 'queued' });
				await sleep(3000);
				if (cancelled.delete(a.id)) throw new Cancelled('cancelled');
			}
		}
		const reader = res.body!.getReader();
		const dec = new TextDecoder();
		let log = '';
		for (;;) {
			const { value, done } = await reader.read();
			if (done) break;
			log += dec.decode(value, { stream: true });
			const p = install_progress(log, true);
			set({ pct: p ?? 0, phase: p === null ? 'waiting' : 'progress' });
		}
		const exit = /\[exit\] (.*)\s*$/.exec(log.trim())?.[1];
		if (exit !== '0') throw new Error(exit || t('store.failed'));
		notify('appstore', name, t('store.installed_note', { name }));
		revalidate('apps', data.apps).catch(() => {});
		window.dispatchEvent(new Event('zerodesk-apps-changed'));
	} catch (e) {
		if (!(e instanceof Cancelled)) store_state.alert = { title: t('store.failed_title', { name }), message: (e as Error).message };
	} finally {
		cancelled.delete(a.id);
		delete store_state.tasks[a.id];
		window.dispatchEvent(new Event('zerodesk-store-changed'));
	}
}
