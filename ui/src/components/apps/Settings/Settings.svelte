<script lang="ts">
	import { cached, revalidate, store } from '🍎/lib/swr.svelte.ts';
	import { data } from '🍎/state/app_preload.ts';
	import { as_admin, Cancelled } from '🍎/state/admin.svelte.ts';
	import 'framework7-icons/css/framework7-icons.css';
	import { onDestroy, onMount, tick } from 'svelte';
	import { api, post } from '🍎/lib/api.ts';
	import { apps, settings_nav } from '🍎/state/apps.svelte.ts';
	import { t, type MsgKey } from '🍎/state/i18n.svelte.ts';
	import { LANGS, locale_of, preferences, WALLPAPER_TONES, type Appearance, type Lang, type WallpaperTone } from '🍎/state/preferences.svelte.ts';
	import { logout } from '🍎/state/session.svelte.ts';
	import AppIcon from '../../SVG/AppIcon.svelte';
	import Segmented from '../common/Segmented.svelte';
	import TerminalPane from './TerminalPane.svelte';

	// System Settings, laid out like macOS: a floating sidebar (search, the
	// device card, panes with colour tiles) and grouped inset cards on the
	// right. Device settings use the same files and commands as the
	// CardputerZero launcher (agent /api/settings, /api/wifi/*).

	type Pane = 'wifi' | 'network' | 'battery' | 'appearance' | 'terminal' | 'display' | 'sound' | 'ports' | 'datetime' | 'general';
	type NetDev = { device: string; type: string; state: string; connection: string; ip?: string };
	type State = {
		hostname: string;
		display: { brightness: number; screen_on: boolean; timeout: number };
		sound: { volume: number; muted: boolean };
		wifi: { enabled: boolean };
		network: NetDev[] | null;
		ports: { ext5v: boolean; grove5v: boolean };
		battery: Record<string, number | string> | null;
		time: { now: string; ntp?: boolean; synced?: boolean; timezone?: string };
		adb: boolean;
		port80: boolean;
	};
	type Net = { ssid: string; signal: number; security: string; in_use: boolean; known: boolean };

	const PANES: { id: Pane; label: MsgKey; icon: string; tint: string; group: number }[] = [
		{ id: 'wifi', label: 'settings.wifi', icon: 'wifi', tint: '#0a84ff', group: 0 },
		{ id: 'network', label: 'settings.network', icon: 'globe', tint: '#0a84ff', group: 0 },
		{ id: 'battery', label: 'settings.battery', icon: 'battery_100', tint: '#34c759', group: 0 },
		{ id: 'general', label: 'settings.general', icon: 'gear_alt_fill', tint: '#8e8e93', group: 1 },
		{ id: 'appearance', label: 'settings.appearance', icon: 'circle_lefthalf_fill', tint: '#3a3a3c', group: 1 },
		{ id: 'terminal', label: 'settings.terminal', icon: 'chevron_left_slash_chevron_right', tint: '#3a3a3c', group: 1 },
		{ id: 'display', label: 'settings.display', icon: 'sun_max_fill', tint: '#0a84ff', group: 1 },
		{ id: 'sound', label: 'settings.sound', icon: 'speaker_3_fill', tint: '#ff2d55', group: 1 },
		{ id: 'ports', label: 'settings.ports', icon: 'bolt_fill', tint: '#ff9500', group: 2 },
		{ id: 'datetime', label: 'settings.datetime', icon: 'clock_fill', tint: '#8e8e93', group: 2 },
	];
	const TIMEOUTS = [0, 10, 30, 60, 300];

	let pane = $state<Pane>('wifi');
	$effect(() => {
		const want = settings_nav.pane as Pane;
		if (!want || !PANES.some((p) => p.id === want)) return;
		pane = want;
		query = '';
		show_list = false;
		settings_nav.pane = '';
	});
	const WALLPAPERS: ('device' | WallpaperTone)[] = ['device', ...(Object.keys(WALLPAPER_TONES) as WallpaperTone[])];
	let query = $state('');
	// last known state paints the window at once; a refresh follows (lib/swr)
	let s = $state<State | null>(cached<State>('settings') ?? null);
	let error = $state('');
	let busy = $state<string | null>(null);

	const shown = $derived(query.trim() ? PANES.filter((p) => t(p.label).toLowerCase().includes(query.trim().toLowerCase())) : PANES);
	const current = $derived(PANES.find((p) => p.id === pane)!);
	const wifi_dev = $derived(s?.network?.find((d) => d.type === 'wifi'));
	const eth_dev = $derived(s?.network?.find((d) => d.type === 'ethernet'));

	async function load() {
		try {
			s = (await revalidate('settings', data.settings)) as State;
			error = '';
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function set(key: string, value: unknown) {
		busy = key;
		try {
			await as_admin((password) => post('/api/settings', { key, value, password }));
			error = '';
		} catch (e) {
			if (!(e instanceof Cancelled)) error = (e as Error).message;
		}
		busy = null;
		await load();
	}

	// sliders: live value while dragging, one write when released
	let brightness = $state(100);
	let volume = $state(50);
	let editing = false; // a slider is being dragged: the periodic refresh must not move it
	$effect(() => {
		if (!s || busy || editing) return;
		brightness = s.display.brightness;
		volume = s.sound.volume;
	});

	// ---- Wi-Fi ----
	let nets = $state<Net[]>(cached<Net[]>('wifi') ?? []);
	let scanning = $state(false);
	let joining = $state<string | null>(null);
	let pw_for = $state<string | null>(null);
	let password = $state('');
	let wifi_error = $state('');

	async function scan(rescan = false) {
		if (!s?.wifi.enabled) return;
		scanning = true;
		try {
			nets = rescan ? await api<Net[]>('/api/wifi/networks?rescan=1') : ((await revalidate('wifi', data.wifi)) as Net[]);
			if (rescan) store('wifi', nets);
		} catch {
			/* keep the last list */
		}
		scanning = false;
	}

	async function join(n: Net, pw = '') {
		if (n.in_use) return;
		if (!pw && !n.known && n.security && n.security !== '--') {
			pw_for = n.ssid;
			password = '';
			wifi_error = '';
			await tick();
			document.querySelector<HTMLInputElement>('.settings input.pw')?.focus();
			return;
		}
		joining = n.ssid;
		wifi_error = '';
		try {
			await post('/api/wifi/connect', { ssid: n.ssid, password: pw });
			pw_for = null;
		} catch (e) {
			wifi_error = (e as Error).message;
		}
		joining = null;
		await Promise.all([load(), scan()]);
	}

	async function disconnect() {
		await post('/api/wifi/disconnect', {}).catch((e) => (wifi_error = (e as Error).message));
		await Promise.all([load(), scan()]);
	}

	// ---- date & time ----
	let manual = $state('');
	function local_input(iso: string) {
		const d = new Date(iso);
		const p = (n: number) => String(n).padStart(2, '0');
		return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
	}

	// ---- general ----
	let confirm_restart = $state(false);

	// ---- ZeroDesk › signed-in browsers and names ----
	type Browser = { id: string; method: string; user_agent: string; ip: string; created_at: string; last_seen: string; current: boolean };
	let browsers = $state<Browser[]>([]);
	let names = $state<string[]>([]);
	// what the device reported; null until read, or when it cannot be read
	let pair_confirm = $state<'code' | 'ok' | null>(null);
	let name_draft = $state('');
	let confirm_reset = $state(false);

	async function load_access() {
		const n = ++pair_seq;
		try {
			const [b, h, p] = await Promise.all([
				api<{ sessions: Browser[] }>('/api/sessions'),
				api<{ hosts: string[] }>('/api/hosts'),
				api<{ confirm: 'code' | 'ok' }>('/api/pairing'),
			]);
			browsers = b.sessions;
			names = h.hosts ?? [];
			if (n === pair_seq) pair_confirm = p.confirm; // a later read or save decides otherwise
		} catch (e) {
			error = (e as Error).message;
			if (n === pair_seq) await read_pair_confirm();
		}
	}
	$effect(() => {
		if (pane === 'general') void load_access();
	});

	/** "Chrome on macOS" from a user agent, enough to tell browsers apart */
	function browser_label(ua: string): string {
		const name = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : '';
		const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
		if (name && os) return t('settings.browser_on', { name, os });
		return name || os || t('settings.browser_unknown');
	}

	async function sign_out_browser(b: Browser) {
		if (b.current) return void logout();
		try {
			await api(`/api/sessions/${encodeURIComponent(b.id)}`, { method: 'DELETE' });
		} catch (e) {
			error = (e as Error).message;
		}
		await load_access();
	}

	async function reset_access() {
		confirm_reset = false;
		try {
			await post('/api/sessions/reset', {});
		} catch (e) {
			error = (e as Error).message;
		}
		await load_access();
	}

	// The page only ever shows what the device reports. After every save,
	// whether it seemed to work or not, the setting is read back; if that
	// fails too the control is locked and the page says it cannot tell.
	let pair_saving = $state(false);
	// Every read and save takes a number; only the newest one's answer is
	// shown, so a slow earlier read cannot overwrite a later one.
	let pair_seq = 0;
	async function read_pair_confirm() {
		const n = ++pair_seq;
		let v: 'code' | 'ok' | null;
		try {
			v = (await api<{ confirm: 'code' | 'ok' }>('/api/pairing')).confirm;
		} catch {
			v = null;
		}
		if (n === pair_seq) pair_confirm = v;
	}
	async function set_pair_confirm(v: 'code' | 'ok') {
		if (pair_saving || pair_confirm === null || v === pair_confirm) return;
		pair_saving = true;
		pair_seq++; // any read already on its way is now out of date
		try {
			await api('/api/pairing', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ confirm: v }) });
		} catch (e) {
			error = (e as Error).message;
		}
		await read_pair_confirm();
		pair_saving = false;
	}

	async function save_names(list: string[]) {
		try {
			names = ((await api<{ hosts: string[] }>('/api/hosts', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ hosts: list }) })).hosts) ?? [];
			name_draft = '';
		} catch (e) {
			error = (e as Error).message;
		}
	}
	let restarting = $state(false);
	async function restart() {
		restarting = true;
		await post('/api/system/reboot', {}).catch(() => {});
	}

	// ---- refresh ----
	// narrow window: the sidebar becomes an overlay opened from the title bar
	let root_width = $state(800);
	const narrow = $derived(root_width < 520);
	let show_list = $state(false);

	let timer: ReturnType<typeof setInterval> | undefined;
	$effect(() => {
		const p = pane;
		if (p === 'wifi') scan();
		if (p === 'datetime' && s) manual = local_input(s.time.now);
	});
	onMount(() => {
		load().then(() => scan());
		timer = setInterval(() => {
			if (apps.active !== 'settings' || busy || joining) return;
			load();
			if (pane === 'wifi') scan();
		}, 8000);
	});
	onDestroy(() => clearInterval(timer));

	// ---- formatting ----
	const fmt_status = (v: string) =>
		({ Charging: t('settings.charging'), Discharging: t('settings.discharging'), Full: t('settings.charged'), 'Not charging': t('settings.not_charging') })[v] ?? v;
	const HEALTH: Partial<Record<Lang, Record<string, string>>> = {
		zh: { Good: '良好', Overheat: '过热', Dead: '损坏', 'Over voltage': '过压', Cold: '过冷' },
		ja: { Good: '良好', Overheat: '過熱', Dead: '故障', 'Over voltage': '過電圧', Cold: '低温' },
	};
	const fmt_health = (v: string) => HEALTH[preferences.lang]?.[v] ?? v;
	// Linux's Etc/GMT-8 means UTC+8 (POSIX signs are inverted)
	const fmt_tz = (z = '') => {
		const m = /^Etc\/GMT([+-])(\d+)$/.exec(z);
		return m ? `UTC${m[1] === '-' ? '+' : '−'}${m[2]}` : z;
	};
	const fmt_timeout = (n: number) => (n === 0 ? t('settings.never') : n < 60 ? `${n}s` : `${n / 60}min`);
	const bars = (signal: number) => (signal >= 70 ? 3 : signal >= 45 ? 2 : 1);
	const num = (v: number | string | undefined) => (typeof v === 'number' ? v : Number(v ?? NaN));
</script>

{#snippet tile(icon: string, tint: string, size = 20)}
	<span class="tile" style:--tint={tint} style:--s="{size}px"><i class="f7-icons">{icon}</i></span>
{/snippet}

{#snippet toggle(on: boolean, label: string, onchange: (v: boolean) => void, disabled = false)}
	<button class="switch" class:on role="switch" aria-checked={on} aria-label={label} {disabled} onclick={() => onchange(!on)}><span></span></button>
{/snippet}

{#snippet wifi_glyph(signal: number)}
	<span class="wifi-bars b{bars(signal)}" aria-hidden="true"><i class="f7-icons back">wifi</i><i class="f7-icons front">wifi</i></span>
{/snippet}

<div class="settings" class:narrow class:show-list={show_list} bind:clientWidth={root_width}>
	<aside class="sidebar">
		<div class="lights-band app-window-drag-handle"></div>
		<label class="search">
			<i class="f7-icons">search</i>
			<input bind:value={query} placeholder={t('settings.search')} />
		</label>
		<button class="device" class:active={pane === 'general' && !query} onclick={() => ((pane = 'general'), (show_list = false))}>
			<AppIcon id="device" size={42} />
			<span class="device-text"><b>{s?.hostname ?? 'CardputerZero'}</b><small>Cardputer Zero</small></span>
		</button>
		<nav class="panes">
			{#each shown as p, i (p.id)}
				{#if i > 0 && shown[i - 1].group !== p.group}<span class="gap"></span>{/if}
				<button class="item" class:active={pane === p.id} onclick={() => ((pane = p.id), (show_list = false))}>
					{@render tile(p.icon, p.tint)}
					<span>{t(p.label)}</span>
					{#if p.id === 'terminal' && !preferences.seen_terminal_settings}<span class="new-dot" aria-hidden="true"></span>{/if}
				</button>
			{/each}
		</nav>
	</aside>

	<section class="detail">
		<header class="titlebar app-window-drag-handle">
			{#if narrow}
				<button class="list-btn" aria-label={t('app.settings')} onclick={() => (show_list = true)}><i class="f7-icons">sidebar_left</i></button>
			{/if}
			<h1>{t(current.label)}</h1>
		</header>

		<div class="content">
			{#if pane === 'terminal'}
				<TerminalPane />
			{:else if !s}
				<!-- first open without any data yet: the final layout, values blank -->
				<div class="hero">
					{@render tile(current.icon, current.tint, 44)}
					<div class="hero-text"><b>{t(current.label)}</b></div>
				</div>
				<div class="card skeleton" aria-busy="true">
					{#each [0, 1, 2, 3] as i (i)}<div class="row"></div>{/each}
				</div>
				{#if error}<p class="foot">{error}</p>{/if}
			{:else if pane === 'wifi'}
				<div class="hero">
					{@render tile('wifi', '#0a84ff', 44)}
					<div class="hero-text">
						<b>Wi-Fi</b>
					</div>
					{@render toggle(s.wifi.enabled, 'Wi-Fi', (v) => set('wifi', v).then(() => scan(true)), busy === 'wifi')}
				</div>
				{#if s.wifi.enabled}
					{@const cur = nets.find((n) => n.in_use)}
					{#if cur}
						<div class="card">
							<div class="row">
								{@render wifi_glyph(cur.signal)}
								<span class="grow"><b class="name">{cur.ssid}</b><small class="ok">● {t('settings.wifi_connected')}</small></span>
								<button class="btn" onclick={disconnect}>{t('settings.wifi_disconnect')}</button>
							</div>
						</div>
					{/if}
					<h2>
						{t('settings.wifi_other')}
						<button class="icon-btn" aria-label="Refresh" class:spin={scanning} onclick={() => scan(true)}><i class="f7-icons">arrow_clockwise</i></button>
					</h2>
					<div class="card">
						{#each nets.filter((n) => !n.in_use) as n (n.ssid)}
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<div class="row net" role="button" tabindex="-1" onclick={() => pw_for !== n.ssid && join(n)}>
								{@render wifi_glyph(n.signal)}
								<span class="grow name">{n.ssid}</span>
								{#if joining === n.ssid}<small class="muted">{t('settings.wifi_joining')}</small>{/if}
								{#if n.security && n.security !== '--'}<i class="f7-icons lock">lock_fill</i>{/if}
							</div>
							{#if pw_for === n.ssid}
								<form class="row pw-row" onsubmit={(e) => (e.preventDefault(), join(n, password))}>
									<input class="pw field" type="password" bind:value={password} placeholder={t('settings.wifi_password')} autocomplete="off" />
									<button type="button" class="btn" onclick={() => (pw_for = null)}>{t('settings.cancel')}</button>
									<button class="btn primary" disabled={!password || joining === n.ssid}>{t('settings.wifi_join')}</button>
								</form>
							{/if}
						{:else}
							<div class="row"><span class="muted">{t('settings.wifi_searching')}</span></div>
						{/each}
					</div>
					{#if wifi_error}<p class="err">{wifi_error}</p>{/if}
				{:else}
					<p class="empty-note">{t('settings.wifi_off')}</p>
				{/if}
			{:else if pane === 'network'}
				<div class="card">
					<div class="row">
						{@render tile('wifi', '#0a84ff', 28)}
						<span class="grow"><b class="name">Wi-Fi</b><small class={wifi_dev?.state === 'connected' ? 'ok' : 'muted'}>● {wifi_dev?.state === 'connected' ? t('settings.connected') : t('settings.not_connected')}</small></span>
						<span class="value">{wifi_dev?.ip ?? ''}</span>
					</div>
					<div class="row">
						{@render tile('globe', '#0a84ff', 28)}
						<span class="grow"><b class="name">{t('settings.ethernet')}</b><small class={eth_dev?.state === 'connected' ? 'ok' : 'muted'}>● {eth_dev?.state === 'connected' ? t('settings.connected') : t('settings.not_connected')}</small></span>
						<span class="value">{eth_dev?.ip ?? ''}</span>
					</div>
				</div>
			{:else if pane === 'battery'}
				{@const b = s.battery}
				{#if b}
					<div class="hero battery">
						<span class="cell" style:--level="{num(b.capacity)}%" class:charging={b.status === 'Charging'}><span></span></span>
						<div class="hero-text">
							<b class="big">{num(b.capacity)}%</b>
							<small>{fmt_status(String(b.status))}</small>
						</div>
					</div>
					<div class="card">
						<div class="row"><span class="grow">{t('settings.battery_health')}</span><span class="value">{fmt_health(String(b.health))}</span></div>
						{#if b.charge_full && b.charge_full_design}
							<div class="row"><span class="grow">{t('settings.capacity')}</span><span class="value">{Math.round((num(b.charge_full) / num(b.charge_full_design)) * 100)}%</span></div>
						{/if}
						<div class="row"><span class="grow">{t('settings.cycles')}</span><span class="value">{b.cycle_count}</span></div>
						<div class="row"><span class="grow">{t('settings.temperature')}</span><span class="value">{(num(b.temp) / 10).toFixed(1)} °C</span></div>
						<div class="row"><span class="grow">{t('settings.voltage')}</span><span class="value">{(num(b.voltage_now) / 1e6).toFixed(2)} V</span></div>
					</div>
				{/if}
			{:else if pane === 'appearance'}
				<div class="card">
					<div class="row">
						<span class="grow">{t('settings.appearance')}</span>
						<Segmented
							options={[
								{ value: 'auto' as Appearance, label: t('settings.auto') },
								{ value: 'light' as Appearance, label: t('settings.light') },
								{ value: 'dark' as Appearance, label: t('settings.dark') },
							]}
							value={preferences.appearance}
							onchange={(v) => (preferences.appearance = v)}
						/>
					</div>
					<div class="row">
						<span class="grow">{t('settings.language')}</span>
						<label class="popup">
							<select value={preferences.lang} aria-label={t('settings.language')} onchange={(e) => (preferences.lang = e.currentTarget.value as Lang)}>
								{#each LANGS as l (l.value)}<option value={l.value}>{l.label}</option>{/each}
							</select>
							<i class="f7-icons" aria-hidden="true">chevron_up_chevron_down</i>
						</label>
					</div>
					<div class="row">
						<span class="grow">{t('settings.motion')}</span>
						{@render toggle(preferences.reduced_motion, t('settings.motion'), (v) => (preferences.reduced_motion = v))}
					</div>
				</div>
				{#if preferences.appearance === 'auto'}<p class="foot">{t('settings.auto_note')}</p>{/if}
				<h2>{t('settings.wallpaper')}</h2>
				<div class="walls">
					{#each WALLPAPERS as w (w)}
						<button class="wall" class:on={preferences.wallpaper === w} onclick={() => (preferences.wallpaper = w)}>
							{#if w === 'device'}
								<span class="chip device-wall" style:background-image="url(/api/wallpaper)"></span>
							{:else}
								<span class="chip" style:background={WALLPAPER_TONES[w][preferences.theme.scheme === 'dark' ? 1 : 0]}></span>
							{/if}
							<small>{w === 'device' ? t('settings.wallpaper_device') : t(`settings.tone_${w}` as MsgKey)}</small>
						</button>
					{/each}
				</div>
			{:else if pane === 'display'}
				<div class="card">
					<div class="row tall">
						<span class="label">{t('settings.brightness')}</span>
						<div class="slider-row">
							<i class="f7-icons end">sun_min_fill</i>
							<input
								type="range"
								min="10"
								max="100"
								bind:value={brightness}
								style:--v="{((brightness - 10) / 90) * 100}%"
								oninput={() => (editing = true)}
								onchange={() => ((editing = false), set('brightness', brightness))}
							/>
							<i class="f7-icons end">sun_max_fill</i>
						</div>
					</div>
					<div class="row">
						<span class="grow">{t('settings.screen_off')}</span>
						<select class="field select" value={s.display.timeout} onchange={(e) => set('timeout', Number((e.currentTarget as HTMLSelectElement).value))}>
							{#each TIMEOUTS as n (n)}<option value={n}>{fmt_timeout(n)}</option>{/each}
						</select>
					</div>
				</div>
				{#if !s.display.screen_on}<p class="foot">{t('settings.screen_is_off')}</p>{/if}
			{:else if pane === 'sound'}
				<div class="card">
					<div class="row tall">
						<span class="label">{t('settings.volume')}</span>
						<div class="slider-row">
							<i class="f7-icons end">speaker_fill</i>
							<input
								type="range"
								min="0"
								max="100"
								bind:value={volume}
								style:--v="{volume}%"
								disabled={s.sound.muted}
								oninput={() => (editing = true)}
								onchange={() => ((editing = false), set('volume', volume))}
							/>
							<i class="f7-icons end">speaker_3_fill</i>
						</div>
					</div>
					<div class="row">
						<span class="grow">{t('settings.mute')}</span>
						{@render toggle(s.sound.muted, t('settings.mute'), (v) => set('mute', v), busy === 'mute')}
					</div>
				</div>
			{:else if pane === 'ports'}
				<div class="card">
					<div class="row">
						<span class="grow">{t('settings.ext5v')}</span>
						{@render toggle(s.ports.ext5v, t('settings.ext5v'), (v) => set('ext5v', v), busy === 'ext5v')}
					</div>
					<div class="row">
						<span class="grow">{t('settings.grove5v')}</span>
						{@render toggle(s.ports.grove5v, t('settings.grove5v'), (v) => set('grove5v', v), busy === 'grove5v')}
					</div>
				</div>
			{:else if pane === 'datetime'}
				<div class="card">
					<div class="row">
						<span class="grow">{t('settings.auto_time')}{#if s.time.ntp && s.time.synced}<small class="muted">{t('settings.synced')}</small>{/if}</span>
						{@render toggle(!!s.time.ntp, t('settings.auto_time'), (v) => set('ntp', v), busy === 'ntp')}
					</div>
					<div class="row">
						<span class="grow">{t('settings.datetime')}</span>
						{#if s.time.ntp}
							<span class="value">{new Date(s.time.now).toLocaleString(locale_of())}</span>
						{:else}
							<input class="field" type="datetime-local" bind:value={manual} />
							<button class="btn" disabled={busy === 'time'} onclick={() => set('time', manual.replace('T', ' '))}>{t('settings.set')}</button>
						{/if}
					</div>
					<div class="row"><span class="grow">{t('settings.timezone')}</span><span class="value">{fmt_tz(s.time.timezone)}</span></div>
				</div>
			{:else if pane === 'general'}
				<div class="hero">
					<AppIcon id="device" size={64} />
					<div class="hero-text">
						<b class="big">{s.hostname}</b>
						<small>Cardputer Zero</small>
					</div>
				</div>
				<div class="card">
					<div class="row"><span class="grow">{t('settings.name')}</span><span class="value">{s.hostname}</span></div>
					<div class="row">
						<span class="grow">{t('settings.adb')}</span>
						{@render toggle(s.adb, t('settings.adb'), (v) => set('adb', v), busy === 'adb')}
					</div>
					<div class="row">
						<span class="grow">{t('settings.web_address')}</span>
						{#if s.port80}
							<span class="value">{s.hostname}.local</span>
						{:else}
							<button class="btn" disabled={busy === 'port80'} onclick={() => set('port80', true)}>{t('settings.turn_on')}</button>
						{/if}
					</div>
				</div>
				<h2>{t('settings.zerodesk')}</h2>
				<div class="card">
					<div class="row">
						<span class="grow">{t('settings.session')}</span>
						<button class="btn" onclick={logout}>{t('settings.logout')}</button>
					</div>
					<div class="row">
						<span class="grow">{t('settings.pair_confirm')}<small class="muted">{pair_confirm === null ? t('settings.pair_confirm_unknown') : pair_confirm === 'code' ? t('settings.pair_confirm_code_note') : t('settings.pair_confirm_ok_note')}</small></span>
						{#if pair_confirm !== null}
							<!-- shown only when the device said which mode it uses -->
							<span class="pair-mode" inert={pair_saving}>
								<Segmented
									options={[
										{ value: 'code', label: t('settings.pair_confirm_code') },
										{ value: 'ok', label: t('settings.pair_confirm_ok') },
									]}
									value={pair_confirm}
									onchange={(v) => set_pair_confirm(v as 'code' | 'ok')}
								/>
							</span>
						{:else}
							<button class="btn" onclick={read_pair_confirm}>{t('desktop.retry')}</button>
						{/if}
					</div>
				</div>
				<h2>{t('settings.browsers')}</h2>
				<div class="card">
					{#each browsers as b (b.id)}
						<div class="row browser">
							<span class="grow">
								<span>{browser_label(b.user_agent)}{#if b.current}<span class="tag">{t('settings.this_browser')}</span>{/if}</span>
								<small class="muted">{b.ip} · {new Date(b.last_seen).toLocaleString(locale_of(), { dateStyle: 'medium', timeStyle: 'short' })}</small>
							</span>
							<button class="btn" onclick={() => sign_out_browser(b)}>{t('settings.logout')}</button>
						</div>
					{/each}
					<div class="row">
						{#if confirm_reset}
							<span class="grow muted">{t('settings.reset_access_confirm')}</span>
							<button class="btn" onclick={() => (confirm_reset = false)}>{t('settings.cancel')}</button>
							<button class="btn danger" onclick={reset_access}>{t('settings.reset_access')}</button>
						{:else}
							<span class="grow">{t('settings.reset_access_label')}</span>
							<button class="btn" onclick={() => (confirm_reset = true)}>{t('settings.reset_access')}</button>
						{/if}
					</div>
				</div>
				<h2>{t('settings.names')}</h2>
				<div class="card">
					<div class="row"><span class="grow">{s.hostname}.local</span></div>
					{#each names as n (n)}
						<div class="row">
							<span class="grow">{n}</span>
							<button class="btn" onclick={() => save_names(names.filter((x) => x !== n))}>{t('settings.remove')}</button>
						</div>
					{/each}
					<form class="row" onsubmit={(e) => (e.preventDefault(), name_draft.trim() && save_names([...names, name_draft]))}>
						<!-- Add appears only once there is something to add, inside the field so nothing moves -->
						<span class="add-field grow">
							<input class="field" placeholder={t('settings.name_placeholder')} aria-label={t('settings.names')} bind:value={name_draft} />
							<button class="add" type="submit" class:shown={!!name_draft.trim()} tabindex={name_draft.trim() ? 0 : -1} aria-hidden={!name_draft.trim()}>{t('settings.add')}</button>
						</span>
					</form>
				</div>
				<div class="restart-bar">
					{#if restarting}
						<span class="muted">{t('settings.restarting')}</span>
					{:else if confirm_restart}
						<span class="muted">{t('settings.restart_confirm')}</span>
						<button class="btn" onclick={() => (confirm_restart = false)}>{t('settings.cancel')}</button>
						<button class="btn danger" onclick={restart}>{t('settings.restart_now')}</button>
					{:else}
						<button class="btn" onclick={() => (confirm_restart = true)}>{t('settings.restart')}</button>
					{/if}
				</div>
			{/if}
			{#if error && s}<p class="err">{error}</p>{/if}
		</div>
	</section>
</div>

<style>
	.settings {
		position: relative;
		display: grid;
		grid-template-columns: 236px minmax(0, 1fr);
		height: 100%;
		min-height: 0;
	}

	.f7-icons {
		font-size: inherit;
		line-height: 1;
		font-style: normal;
	}

	/* ---- sidebar ---- */

	.sidebar {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: var(--zd-sidebar-inset);
		margin-right: 0;
		padding: 0 10px 10px;
		border-radius: var(--zd-radius-sidebar);
		background: var(--zd-sidebar-bg);
		backdrop-filter: blur(30px) saturate(1.4);
		box-shadow: 0 0 0 0.5px var(--zd-hairline);
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
	}

	.lights-band {
		height: calc(var(--zd-titlebar) - var(--zd-sidebar-inset));
		flex: none;
	}

	.search {
		flex: none;
		display: flex;
		align-items: center;
		gap: 6px;
		height: 28px;
		padding: 0 10px;
		margin-bottom: 8px;
		border-radius: 14px;
		background: var(--zd-hover);
		color: var(--zd-text-tertiary);
		font-size: 13px;

		input {
			flex: 1;
			min-width: 0;
			border: 0;
			outline: 0;
			background: none;
			font: inherit;
			color: var(--zd-text);
		}
	}

	.device {
		flex: none;
		justify-content: flex-start;
		gap: 10px;
		padding: 6px 8px;
		margin-bottom: 6px;
		border-radius: 10px;
		text-align: left;

		&.active {
			background: var(--zd-selected);
		}
	}

	.device-text {
		display: flex;
		flex-direction: column;
		min-width: 0;

		b {
			font-size: 13px;
			font-weight: 600;
		}

		small {
			font-size: 11px;
			color: var(--zd-text-secondary);
		}
	}

	.panes {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.gap {
		height: 12px;
	}

	.item {
		justify-content: flex-start;
		gap: 9px;
		height: 30px;
		padding: 0 8px;
		border-radius: 8px;
		font-size: 13px;

		&.active {
			background: var(--zd-selected);
			font-weight: 600;
		}
	}

	/* a macOS pop-up button: the native list (works on phones), our own look */
	.popup {
		position: relative;
		display: inline-flex;
		align-items: center;

		select {
			appearance: none;
			height: 24px;
			padding: 0 26px 0 10px;
			border: 0;
			outline: 0;
			border-radius: 6px;
			background: var(--zd-control-bg);
			box-shadow: var(--zd-control-shadow);
			font: inherit;
			font-size: 13px;
			color: var(--zd-text);
			cursor: default;

			&:focus-visible {
				box-shadow:
					var(--zd-control-shadow),
					0 0 0 2px var(--zd-hairline-strong);
			}
		}

		i {
			position: absolute;
			right: 8px;
			font-size: 10px;
			font-weight: 600;
			color: var(--zd-text-secondary);
			pointer-events: none;
		}
	}

	.new-dot {
		width: 6px;
		height: 6px;
		margin-left: auto;
		margin-right: 2px;
		border-radius: 50%;
		background: var(--zd-text-tertiary);
	}

	.walls {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(46px, 1fr));
		gap: 12px 8px;
		padding: 4px 2px;
	}

	.wall {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		padding: 0;
		min-width: 0;

		.chip {
			width: 100%;
			aspect-ratio: 16 / 10;
			border-radius: 8px;
			background-size: cover;
			background-position: center;
			background-color: var(--zd-wallpaper);
			box-shadow: inset 0 0 0 0.5px var(--zd-hairline-strong);
			transition: box-shadow 180ms ease;
		}

		&.on .chip {
			box-shadow:
				0 0 0 2px var(--zd-window-bg),
				0 0 0 4px var(--zd-text);
		}

		/* longer names (グラファイト) wrap rather than being cut off */
		small {
			max-width: 100%;
			font-size: 11px;
			line-height: 1.25;
			text-align: center;
			overflow-wrap: anywhere;
			color: var(--zd-text-secondary);
		}
	}

	.tile {
		flex: none;
		width: var(--s);
		height: var(--s);
		display: grid;
		place-items: center;
		border-radius: calc(var(--s) * 0.24);
		background: linear-gradient(to bottom, color-mix(in srgb, var(--tint) 80%, white), var(--tint));
		box-shadow: inset 0 0 0 0.5px hsla(0, 0%, 0%, 0.12);
		color: #fff;

		:global(body.dark) & {
			box-shadow: inset 0 0 0 0.5px hsla(0, 0%, 100%, 0.18);
		}
		font-size: calc(var(--s) * 0.6);
	}

	/* ---- detail ---- */

	.detail {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}

	.titlebar {
		flex: none;
		height: var(--zd-titlebar);
		display: flex;
		align-items: center;
		padding: 0 24px;

		h1 {
			font-size: 15px;
			font-weight: 600;
		}
	}

	.content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
		padding: 4px 24px 28px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.skeleton .row {
		min-height: 44px;
	}

	.hero {
		display: flex;
		align-items: center;
		gap: 14px;
		padding: 4px 4px 6px;

		.hero-text {
			flex: 1;
			display: flex;
			flex-direction: column;
			gap: 2px;
			min-width: 0;

			b {
				font-size: 15px;
				font-weight: 600;
			}

			.big {
				font-size: 20px;
				font-weight: 700;
			}

			small {
				font-size: 12px;
				color: var(--zd-text-secondary);
			}
		}
	}

	h2 {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 6px 4px -4px;
		font-size: 13px;
		font-weight: 600;
	}

	.card {
		border-radius: 12px;
		background: var(--zd-hover);
		box-shadow: inset 0 0 0 0.5px var(--zd-hairline);
		padding: 0 14px;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 12px;
		min-height: 44px;
		padding: 6px 0;
		font-size: 13px;

		& + .row {
			box-shadow: 0 -0.5px 0 var(--zd-hairline);
		}

		&.tall {
			flex-direction: column;
			align-items: stretch;
			gap: 8px;
			padding: 12px 0;
		}

		&.net {
			cursor: default;
		}
	}

	.pair-mode[inert] {
		opacity: 0.6;
	}

	.row.browser {
		padding: 10px 0;

		.grow {
			gap: 3px;
		}

		.tag {
			margin-left: 8px;
			color: var(--zd-text-secondary);
			font-size: 11px;
		}
	}

	.grow {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 1px;

		small {
			font-size: 11px;
		}
	}

	.name {
		font-weight: 500;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.value,
	.muted {
		color: var(--zd-text-secondary);
		font-variant-numeric: tabular-nums;
	}

	.ok {
		color: var(--zd-green);
	}

	.lock {
		font-size: 12px;
		color: var(--zd-text-tertiary);
	}

	.foot,
	.empty-note {
		margin: -4px 4px 0;
		font-size: 11px;
		color: var(--zd-text-tertiary);
	}

	.empty-note {
		margin-top: 30px;
		text-align: center;
		font-size: 13px;
	}

	.err {
		font-size: 12px;
		color: var(--zd-red);
		margin: 0 4px;
	}

	/* Wi-Fi strength: a dim full glyph with the strong part on top, clipped by bars */
	.wifi-bars {
		position: relative;
		width: 18px;
		height: 16px;
		flex: none;
		font-size: 16px;

		i {
			position: absolute;
			inset: 0;
			text-align: center;
		}

		.back {
			opacity: 0.25;
		}

		&.b1 .front {
			clip-path: inset(62% 0 0 0);
		}

		&.b2 .front {
			clip-path: inset(32% 0 0 0);
		}
	}

	/* ---- controls ---- */

	.btn {
		height: 24px;
		padding: 0 12px;
		border-radius: 7px;
		background: var(--zd-control-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 0.5px 1.5px hsla(0, 0%, 0%, 0.12);
		font-size: 13px;
		color: var(--zd-text);

		&:disabled {
			opacity: 0.5;
		}

		&.primary {
			background: var(--system-color-primary);
			color: var(--system-color-light, #fff);
			box-shadow: none;
		}

		&.danger {
			background: var(--zd-red);
			color: #fff;
			box-shadow: none;
		}
	}

	:global(body.dark) .settings .btn.primary {
		color: #1d1d1f;
	}

	.icon-btn {
		width: 22px;
		height: 22px;
		border-radius: 11px;
		color: var(--zd-text-secondary);
		font-size: 13px;

		&:hover {
			background: var(--zd-hover);
		}

		&.spin i {
			animation: spin 0.9s linear infinite;
		}
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.add-field {
		position: relative;
		flex-direction: row;

		.field {
			width: 100%;
			padding-right: 64px;
		}

		.add {
			position: absolute;
			top: 3px;
			right: 3px;
			height: 20px;
			padding: 0 10px;
			border-radius: 5px;
			background: var(--zd-hover);
			color: var(--zd-text);
			font-size: 12px;
			font-weight: 500;
			opacity: 0;
			transform: scale(0.92);
			pointer-events: none;
			transition:
				opacity 0.14s ease,
				transform 0.14s ease;

			&.shown {
				opacity: 1;
				transform: none;
				pointer-events: auto;
			}
		}
	}

	.field {
		height: 26px;
		padding: 0 8px;
		border: 0;
		border-radius: 7px;
		outline: 0;
		background: var(--zd-control-bg);
		box-shadow: 0 0 0 0.5px var(--zd-hairline-strong);
		font: inherit;
		font-size: 13px;
		color: var(--zd-text);

		&:focus {
			box-shadow: 0 0 0 3px color-mix(in srgb, var(--system-color-primary) 30%, transparent);
		}
	}

	.select {
		appearance: none;
		padding-right: 26px;
		background-image: linear-gradient(45deg, transparent 50%, currentColor 50%), linear-gradient(135deg, currentColor 50%, transparent 50%);
		background-position:
			calc(100% - 13px) 11px,
			calc(100% - 9px) 11px;
		background-size:
			4px 4px,
			4px 4px;
		background-repeat: no-repeat;
	}

	.pw-row {
		gap: 8px;

		.pw {
			flex: 1;
			min-width: 0;
		}
	}

	.slider-row {
		display: flex;
		align-items: center;
		gap: 10px;

		.end {
			font-size: 14px;
			color: var(--zd-text-tertiary);
		}

		input {
			flex: 1;
			min-width: 0;
		}
	}

	.label {
		font-size: 13px;
	}

	input[type='range'] {
		appearance: none;
		height: 22px;
		background: transparent;

		&::-webkit-slider-runnable-track {
			height: 4px;
			border-radius: 2px;
			background: linear-gradient(to right, var(--system-color-primary) var(--v), var(--zd-hairline-strong) var(--v));
		}

		&::-webkit-slider-thumb {
			appearance: none;
			width: 20px;
			height: 20px;
			margin-top: -8px;
			border-radius: 50%;
			background: #fff;
			box-shadow:
				0 0 0 0.5px hsla(0, 0%, 0%, 0.18),
				0 1px 4px hsla(0, 0%, 0%, 0.25);
		}

		&::-moz-range-track {
			height: 4px;
			border-radius: 2px;
			background: linear-gradient(to right, var(--system-color-primary) var(--v), var(--zd-hairline-strong) var(--v));
		}

		&::-moz-range-thumb {
			width: 20px;
			height: 20px;
			border: 0;
			border-radius: 50%;
			background: #fff;
			box-shadow: 0 1px 4px hsla(0, 0%, 0%, 0.25);
		}

		&:disabled {
			opacity: 0.4;
		}
	}

	.switch {
		flex: none;
		width: 38px;
		height: 22px;
		padding: 2px;
		justify-content: flex-start;
		border-radius: 11px;
		background: var(--zd-hairline-strong);
		transition: background-color 200ms ease;

		span {
			width: 18px;
			height: 18px;
			border-radius: 9px;
			background: #fff;
			box-shadow: 0 1px 3px hsla(0, 0%, 0%, 0.25);
			transition: transform 380ms var(--zd-spring);
		}

		&.on {
			background: var(--zd-green);

			span {
				transform: translateX(16px);
			}
		}

		&:disabled {
			opacity: 0.6;
		}
	}

	/* battery cell drawn with CSS */
	.cell {
		position: relative;
		width: 54px;
		height: 26px;
		padding: 3px;
		border-radius: 7px;
		box-shadow: inset 0 0 0 1.5px var(--zd-text-tertiary);

		&::after {
			content: '';
			position: absolute;
			right: -5px;
			top: 9px;
			width: 3px;
			height: 8px;
			border-radius: 0 2px 2px 0;
			background: var(--zd-text-tertiary);
		}

		span {
			display: block;
			width: var(--level);
			height: 100%;
			border-radius: 4px;
			background: var(--zd-text);
		}

		&.charging span {
			background: var(--zd-green);
		}
	}

	.restart-bar {
		display: flex;
		justify-content: flex-end;
		align-items: center;
		gap: 8px;
		margin-top: 4px;
		font-size: 13px;
	}

	@container (max-width: 600px) {
		.settings {
			grid-template-columns: 180px minmax(0, 1fr);
		}

		.content {
			padding: 4px 14px 20px;
		}
	}

	.list-btn {
		flex: none;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		margin-right: 10px;
		border-radius: 50%;
		font-size: 17px;
		color: var(--zd-text-secondary);
		background: var(--zd-glass);
		box-shadow:
			inset 0 0 0 0.5px var(--zd-glass-border),
			0 1px 4px hsla(0, 0%, 0%, 0.08);
	}

	.settings.narrow {
		grid-template-columns: minmax(0, 1fr);

		.sidebar {
			display: none;
			position: absolute;
			inset: 0;
			z-index: 6;
			margin: var(--zd-sidebar-inset);
		}

		&.show-list .sidebar {
			display: flex;
		}

		.titlebar {
			padding-left: 92px;
		}

		.content {
			padding: 4px 14px 20px;
		}

	}
</style>
