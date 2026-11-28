import { t } from '🍎/state/i18n.svelte.ts';
import { notify } from '🍎/state/notifications.svelte.ts';
export type SysInfo = {
	hostname: string;
	os: string;
	kernel: string;
	uptimeSec: number;
	load: [number, number, number];
	memTotalMB: number;
	memAvailMB: number;
	diskTotal: number;
	diskFree: number;
	tempC: number;
	battery?: Record<string, string>;
	addrs: { iface: string; addr: string }[];
	time: string;
};

export const session = $state({
	/** null = not checked yet */
	authed: null as boolean | null,
	sys: null as SysInfo | null,
	home: '/home/pi',
	hostname: 'CardputerZero',
	user: 'pi',
});

let timer: ReturnType<typeof setInterval> | undefined;

async function refresh_sys() {
	try {
		const res = await fetch('/api/sys', { credentials: 'same-origin' });
		if (res.status === 401) {
			session.authed = false;
			return;
		}
		if (res.ok) session.sys = await res.json();
	} catch {
		/* offline: keep the last values */
	}
}

export async function check_session() {
	try {
		const res = await fetch('/api/session', { credentials: 'same-origin' });
		session.authed = res.ok;
		if (res.ok) {
			const info = await res.json();
			session.home = info.home || session.home;
			session.hostname = info.hostname || session.hostname;
			session.user = info.user || session.user;
		}
	} catch {
		session.authed = false;
	}
	if (session.authed) {
		start_polling();
		// signed in on the way to Zagent's page: go on there (fixed path)
		if (new URLSearchParams(location.search).get('after') === 'zagent') location.replace('/api/zagent/handoff');
	}
}

/** Battery / clock / memory in the menu bar. Slow polling: this runs on a 1 GHz device. */
export function start_polling() {
	refresh_sys();
	clearInterval(timer);
	timer = setInterval(() => {
		if (document.visibilityState === 'visible') refresh_sys();
	}, 10_000);
}

export async function login(token: string): Promise<'ok' | 'wrong' | 'offline' | 'too_many'> {
	try {
		const res = await fetch('/api/login', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ token }),
		});
		if (res.status === 401) return 'wrong';
		if (res.status === 429) return 'too_many';
		if (!res.ok) return 'offline';
		await check_session();
		return 'ok';
	} catch {
		return 'offline';
	}
}

/** Signs this browser out on the device. If the device could not record it,
 *  the browser stays signed in and says so: the session would otherwise
 *  come back after the device restarts. */
export async function logout() {
	let error = '';
	try {
		const res = await fetch('/api/logout', { method: 'POST', credentials: 'same-origin' });
		if (res.ok || res.status === 401) {
			session.authed = false;
			return;
		}
		error = (await res.json().catch(() => ({})))?.error || res.statusText;
	} catch {
		error = t('login.offline');
	}
	notify('settings', t('settings.logout_failed'), error);
}

export type PairState = 'waiting' | 'approved' | 'expired' | 'denied' | 'unavailable';

/** busy: another computer is waiting on the device; too_many: this one asked too often */
export type PairStart = { id: string; code: string; expiresIn: number; confirm?: 'code' | 'ok' } | { refused: 'busy' | 'too_many' | 'cooldown'; retryIn: number };

/** replace: this browser's own waiting request, which it may replace */
export async function pair_start(lang: string, replace = ''): Promise<PairStart | null> {
	try {
		const res = await fetch('/api/pair/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ lang, replace }),
		});
		if (res.status === 409 || res.status === 429) {
			const body = await res.json().catch(() => ({}));
			const refused = res.status === 409 ? 'busy' : body.reason === 'cooldown' ? 'cooldown' : 'too_many';
			return { refused, retryIn: Number(body.retryIn) || 30 };
		}
		return res.ok ? await res.json() : null;
	} catch {
		return null;
	}
}

export async function pair_status(id: string): Promise<PairState | 'offline'> {
	try {
		const res = await fetch(`/api/pair/status?id=${encodeURIComponent(id)}`, { credentials: 'same-origin' });
		if (!res.ok) return 'offline';
		const { state } = await res.json();
		if (state === 'approved') await check_session();
		return state;
	} catch {
		return 'offline';
	}
}

export function pair_cancel(id: string) {
	navigator.sendBeacon?.(`/api/pair/cancel?id=${encodeURIComponent(id)}`);
}
