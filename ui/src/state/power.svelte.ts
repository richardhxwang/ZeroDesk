import { post } from '🍎/lib/api.ts';

/**
 * Restart… and Shut Down… in ZeroDesk's menu (agent/power.go). They act on
 * ZeroDesk, not on the CardputerZero.
 *   ask:   the confirmation alert that is open
 *   phase: what the screen shows once the agent has accepted
 */
export const power = $state({
	ask: null as 'restart' | 'shutdown' | null,
	phase: null as 'restarting' | 'off' | null,
	busy: false,
	error: '',
});

export async function confirm_power() {
	const action = power.ask;
	if (!action || power.busy) return;
	power.busy = true;
	power.error = '';
	try {
		await post('/api/zerodesk/power', { action });
	} catch (e) {
		power.error = (e as Error).message;
		power.busy = false;
		return;
	}
	power.busy = false;
	power.ask = null;
	power.phase = action === 'restart' ? 'restarting' : 'off';
	wait_until_back(action === 'restart' ? 1000 : 3000);
}

/**
 * Reloads the page once ZeroDesk answers again: after a restart, or when it is
 * started again from the device after Shut Down.
 */
async function wait_until_back(every: number) {
	await new Promise((r) => setTimeout(r, 1200));
	for (;;) {
		try {
			const res = await fetch('/api/session', { credentials: 'same-origin', cache: 'no-store' });
			if (res.ok) {
				location.reload();
				return;
			}
		} catch {
			/* still down */
		}
		await new Promise((r) => setTimeout(r, every));
	}
}
