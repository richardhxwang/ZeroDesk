<script lang="ts">
	import { locale_of, preferences } from '🍎/state/preferences.svelte.ts';
	import { create_interval } from '🍎/state/interval.svelte';

	const time = create_interval(1000);

	const label = $derived.by(() => {
		const d = new Date(time.value);
		const lang = preferences.lang;
		const weekday = d.toLocaleDateString(locale_of(), { weekday: 'short' });
		// as the macOS menu bar writes it in each language
		const date =
			lang === 'zh'
				? `${d.getMonth() + 1}月${d.getDate()}日 ${weekday}`
				: lang === 'ja'
					? `${d.getMonth() + 1}月${d.getDate()}日(${weekday})`
					: `${weekday} ${d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })}`;
		const clock = d.toLocaleTimeString(locale_of(), { hour: 'numeric', minute: '2-digit' });
		return { date, clock };
	});
</script>

<!-- a phone-width window shows the clock only -->
<span class="time"><span class="date">{label.date}</span>{label.clock}</span>

<style>
	.time {
		font-variant-numeric: tabular-nums;
		white-space: pre;
	}

	.date {
		margin-right: 0.55em;
	}

	@media (max-width: 520px) {
		.date {
			display: none;
		}
	}
</style>
