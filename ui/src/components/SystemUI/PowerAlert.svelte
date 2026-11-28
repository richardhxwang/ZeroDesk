<script lang="ts">
	import AppIcon from '../SVG/AppIcon.svelte';
	import { t } from '🍎/state/i18n.svelte.ts';
	import { power, confirm_power } from '🍎/state/power.svelte.ts';
	import { fade } from 'svelte/transition';

	// The confirmation for ZeroDesk menu › Restart… / Shut Down…, laid out
	// like macOS's, and the screen shown while ZeroDesk restarts or is off.
</script>

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
			{#if power.ask === 'shutdown'}<p>{t('power.shutdown_body')}</p>{/if}
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
			<p>{t('power.off_body')}</p>
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

	/* like a Mac between shutting down and starting: black, one line */
	.screen {
		position: fixed;
		inset: 0;
		z-index: 9600;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 12px;
		padding: 0 24px;
		background: #000;
		color: hsla(0, 0%, 100%, 0.88);
		text-align: center;

		b {
			font-size: 15px;
			font-weight: 600;
		}

		p {
			margin: 0;
			font-size: 13px;
			line-height: 1.5;
			color: hsla(0, 0%, 100%, 0.5);
		}
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
