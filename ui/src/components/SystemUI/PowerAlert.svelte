<script lang="ts">
	import AppIcon from '../SVG/AppIcon.svelte';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { power, confirm_power } from '🍎/state/power.svelte.ts';
	import { preferences } from '🍎/state/preferences.svelte.ts';
	import { fade } from 'svelte/transition';

	// The confirmation for ZeroDesk menu › Restart… / Shut Down…, laid out
	// like macOS's, and the screen shown while ZeroDesk restarts or is off.
	// The off screen shows the launcher entry's own screen (agent/devicescreen.go)
	// with the S key picked out, in the device's words: it speaks Chinese or
	// English.
	const device_zh = $derived(preferences.lang === 'zh');
</script>

{#snippet with_key(text: string)}
	{@const [before, after] = text.split('{key}')}
	{before}<kbd>S</kbd>{after}
{/snippet}

<svelte:window
	onkeydown={(e) => {
		if (!power.ask || power.busy) return;
		if (e.key === 'Escape') power.ask = null;
		else if (e.key === 'Enter') confirm_power();
	}}
/>

{#if power.ask}
	<div class="alert-layer" role="presentation" transition:fade={{ duration: 120 }}>
		<div class="alert" role="alertdialog" aria-label={t(power.ask === 'restart' ? 'power.restart_title' : 'power.shutdown_title')}>
			<AppIcon id="device" size={56} />
			<b>{t(power.ask === 'restart' ? 'power.restart_title' : 'power.shutdown_title')}</b>
			{#if power.ask === 'shutdown'}<p>{@render with_key(t('power.shutdown_body'))}</p>{/if}
			{#if power.error}<p class="err">{power.error}</p>{/if}
			<div class="alert-buttons">
				<button class="alert-btn" disabled={power.busy} onclick={() => ((power.ask = null), (power.error = ''))}>{t('admin.cancel')}</button>
				<button class="alert-btn primary" disabled={power.busy} onclick={confirm_power}>
					{t(power.ask === 'restart' ? 'power.restart' : 'power.shutdown')}
				</button>
			</div>
		</div>
	</div>
{/if}

{#if power.phase}
	<div class="screen" role="status" transition:fade={{ duration: 400 }}>
		{#if power.phase === 'restarting'}
			<span class="spinner" aria-hidden="true"></span>
			<b>{t('power.restarting')}</b>
		{:else}
			<b>{t('power.off')}</b>
			<div class="device" aria-hidden="true">
				<div class="dev-head">
					<span class="dev-name"><AppIcon id="device" size={20} />ZeroDesk</span>
					<span class="dev-pill"><i></i>{device_zh ? '未运行' : 'Not running'}</span>
				</div>
				<div class="dev-addr">
					<small>{device_zh ? '在电脑浏览器中打开' : 'Open in a browser on your computer'}</small>
					<span>http://{location.host}</span>
				</div>
				<div class="dev-keys">
					<span class="dev-key">ESC</span><span class="dev-label">{device_zh ? '退出' : 'Exit'}</span>
					<span class="dev-key hot">S</span><span class="dev-label hot">{device_zh ? '启动' : 'Start'}</span>
				</div>
			</div>
			<ol>
				<li><span class="n">1</span>{t('power.off_step1')}</li>
				<li><span class="n">2</span><span>{@render with_key(t('power.off_step2'))}</span></li>
			</ol>
		{/if}
	</div>
{/if}

<style>
	.alert-layer {
		position: fixed;
		inset: 0;
		z-index: 9500;
		display: grid;
		place-items: center;
		padding: 16px;
		background: hsla(0, 0%, 0%, 0.12);
	}

	.alert {
		width: min(260px, 100%);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		padding: 22px 18px 18px;
		border-radius: 22px;
		background: var(--zd-window-bg);
		box-shadow:
			0 0 0 0.5px var(--zd-hairline-strong),
			0 20px 60px hsla(0, 0%, 0%, 0.3);
		text-align: center;

		b {
			margin-top: 4px;
			font-size: 13px;
			font-weight: 700;
			line-height: 1.35;
		}

		p {
			margin: 0;
			font-size: 11px;
			line-height: 1.45;
			text-wrap: balance;
			color: var(--zd-text-secondary);

			&.err {
				color: var(--zd-red);
			}
		}
	}

	.alert-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
		width: 100%;
		margin-top: 10px;
	}

	.alert-btn {
		height: 28px;
		border-radius: 14px;
		font-size: 13px;
		font-weight: 500;
		color: var(--zd-text);
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
		white-space: nowrap;

		&.primary {
			color: var(--system-color-primary-contrast);
			background: var(--system-color-primary);
		}

		&:disabled {
			opacity: 0.6;
		}
	}

	kbd {
		display: inline-grid;
		place-items: center;
		min-width: 1.6em;
		height: 1.6em;
		margin: 0 0.2em;
		padding: 0 0.35em;
		border-radius: 0.35em;
		font: inherit;
		font-weight: 600;
		line-height: 1;
		vertical-align: 0.05em;
		color: var(--zd-text);
		background: var(--zd-control-bg);
		box-shadow: var(--zd-control-shadow);
	}

	/* like a Mac between shutting down and starting: black and quiet */
	.screen {
		position: fixed;
		inset: 0;
		z-index: 9600;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 24px;
		padding: 0 24px;
		background: #000;
		color: hsla(0, 0%, 100%, 0.88);
		text-align: center;

		b {
			font-size: 17px;
			font-weight: 600;
		}

		kbd {
			color: #000;
			background: hsla(0, 0%, 100%, 0.92);
			box-shadow: none;
		}
	}

	/* the launcher entry's screen, 320 × 170 like the real one */
	.device {
		width: min(320px, 100%);
		aspect-ratio: 320 / 170;
		display: flex;
		flex-direction: column;
		justify-content: space-between;
		padding: 14px 14px 12px 16px;
		border-radius: 10px;
		background: #0e0e10;
		box-shadow:
			0 0 0 6px #2a2a2d,
			0 0 0 7px hsla(0, 0%, 100%, 0.12);
		text-align: left;
		font-size: 12px;
	}

	.dev-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.dev-name {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: 16px;
		font-weight: 600;
		color: #f2f2f3;
	}

	.dev-pill {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 10px;
		border-radius: 10px;
		background: #28282c;
		color: #f2f2f3;
		white-space: nowrap;

		i {
			width: 6px;
			height: 6px;
			border-radius: 50%;
			background: #ff453a;
		}
	}

	.dev-addr {
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;

		small {
			font-size: 11px;
			color: #8e8e93;
		}

		span {
			font-size: 15px;
			color: #f2f2f3;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.dev-keys {
		display: flex;
		align-items: center;
		gap: 7px;
	}

	.dev-key {
		display: grid;
		place-items: center;
		min-width: 24px;
		height: 20px;
		padding: 0 7px;
		border-radius: 5px;
		background: #3a3a3e;
		color: #f2f2f3;
		font-size: 11px;

		&.hot {
			margin-left: 11px;
			background: #fff;
			color: #000;
			font-weight: 700;
			box-shadow: 0 0 0 3px hsla(0, 0%, 100%, 0.25);
		}
	}

	.dev-label {
		color: #8e8e93;

		&.hot {
			color: #f2f2f3;
			font-weight: 600;
		}
	}

	ol {
		display: flex;
		flex-direction: column;
		gap: 10px;
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: 14px;
		line-height: 1.5;
		text-align: left;
		color: hsla(0, 0%, 100%, 0.75);

		li {
			display: flex;
			align-items: center;
			gap: 10px;
		}
	}

	.n {
		flex: none;
		display: grid;
		place-items: center;
		width: 20px;
		height: 20px;
		border-radius: 50%;
		box-shadow: inset 0 0 0 1px hsla(0, 0%, 100%, 0.35);
		font-size: 11px;
		font-weight: 600;
	}

	.spinner {
		width: 20px;
		height: 20px;
		border-radius: 50%;
		border: 2px solid hsla(0, 0%, 100%, 0.2);
		border-top-color: hsla(0, 0%, 100%, 0.85);
		animation: spin 900ms linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
