<script lang="ts">
	import { onMount, tick } from 'svelte';

	// Chat with Zagent from ZeroDesk. The agent relays to the device's own
	// web service (zagent-web, /api/zagent/web/*), which talks to the runtime
	// in the same conversation the device shows.

	let { lang = 'zh' }: { lang?: string } = $props();
	const L = (en: string, zh: string) => (lang === 'zh' ? zh : en);

	type Step = { kind: 'tool' | 'thinking'; name?: string; detail?: string; output?: string; text?: string };
	type Approval = { id: string; tool: string; summary: string; answer?: string };
	type Item = { role: 'user' | 'agent'; text: string; steps: Step[]; approvals: Approval[]; streaming?: boolean; error?: string };

	let items = $state<Item[]>([]);
	let draft = $state('');
	let busy = $state(false);
	let relay = '';
	let controller: AbortController | null = null;
	let scroller: HTMLDivElement;

	const headers = { 'Content-Type': 'application/json', 'X-Zagent': '1' };

	// null: checking; false: zagent-web does not answer (not installed or stopped).
	let available = $state<boolean | null>(null);
	async function check() {
		available = null;
		try {
			const res = await fetch('/api/zagent/web/status');
			available = res.ok;
		} catch {
			available = false;
		}
	}
	onMount(check);

	async function scroll_down() {
		await tick();
		if (scroller) scroller.scrollTop = scroller.scrollHeight;
	}

	function handle(reply: Item, ev: any) {
		switch (ev.type) {
			case 'relay': relay = ev.id; break;
			case 'chunk': reply.text += ev.content ?? ''; break;
			case 'thinking': {
				const last = reply.steps[reply.steps.length - 1];
				if (last && last.kind === 'thinking') last.text += ev.content ?? '';
				else reply.steps.push({ kind: 'thinking', text: ev.content ?? '' });
				break;
			}
			case 'tool_call': {
				const args = ev.args ?? {};
				const detail = ['command', 'query', 'url', 'path', 'file_path', 'pattern', 'prompt', 'name']
					.map((k) => args[k]).find((v) => typeof v === 'string') ?? '';
				reply.steps.push({ kind: 'tool', name: ev.name ?? 'tool', detail: String(detail).slice(0, 160) });
				break;
			}
			case 'tool_result': {
				const step = [...reply.steps].reverse().find((s) => s.kind === 'tool' && s.name === ev.name && !s.output);
				if (step) step.output = String(ev.output ?? '').slice(0, 600);
				break;
			}
			case 'approval_request': reply.approvals.push({ id: ev.request_id, tool: ev.tool ?? '', summary: ev.arguments_summary ?? '' }); break;
			case 'done': if (ev.full_response) reply.text = ev.full_response; break;
			case 'error': reply.error = ev.message ?? L('Failed', '失败'); break;
			case 'aborted': reply.error = L('Interrupted', '已中断'); break;
		}
	}

	async function send() {
		const message = draft.trim();
		if (!message || busy || !available) return;
		draft = '';
		items.push({ role: 'user', text: message, steps: [], approvals: [] });
		items.push({ role: 'agent', text: '', steps: [], approvals: [], streaming: true });
		const reply = items[items.length - 1];
		busy = true; relay = '';
		controller = new AbortController();
		scroll_down();
		try {
			const res = await fetch('/api/zagent/web/chat', {
				method: 'POST', headers, body: JSON.stringify({ message }), signal: controller.signal,
			});
			if (!res.ok || !res.body) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`);
			const reader = res.body.getReader();
			const decoder = new TextDecoder();
			let buffer = '';
			for (;;) {
				const { value, done } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				let cut;
				while ((cut = buffer.indexOf('\n\n')) >= 0) {
					const line = buffer.slice(0, cut).split('\n').find((l) => l.startsWith('data: '));
					buffer = buffer.slice(cut + 2);
					if (line) { handle(reply, JSON.parse(line.slice(6))); scroll_down(); }
				}
			}
		} catch (e) {
			reply.error = (e as Error).name === 'AbortError' ? L('Interrupted', '已中断') : (e as Error).message;
		} finally {
			reply.streaming = false; busy = false; controller = null;
		}
	}

	async function stop() {
		if (relay) await fetch('/api/zagent/web/chat/stop', { method: 'POST', headers, body: JSON.stringify({ relay }) }).catch(() => {});
		controller?.abort();
	}

	async function answer(a: Approval, decision: string) {
		a.answer = decision;
		await fetch('/api/zagent/web/chat/approve', {
			method: 'POST', headers, body: JSON.stringify({ relay, request_id: a.id, decision }),
		}).catch(() => {});
	}

	function keydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); busy ? undefined : send(); }
	}
</script>

<div class="chat">
	<div class="transcript" bind:this={scroller}>
		{#if !items.length}
			<div class="empty">
				{#if available === false}
					<h2>{L('Zagent is not running', 'Zagent 没有运行')}</h2>
					<button class="btn" onclick={check}>{L('Retry', '重试')}</button>
				{:else if available}
					<h2>{L('What can I help with?', '有什么可以帮你？')}</h2>
				{/if}
			</div>
		{/if}
		{#each items as item, i (i)}
			{#if item.role === 'user'}
				<div class="user">{item.text}</div>
			{:else}
				<div class="agent">
					{#if item.steps.length}
						<details class="process">
							<summary><i class="f7-icons">chevron_right</i>{L('Process', '过程')} · {item.steps.length}</summary>
							<div class="steps">
								{#each item.steps as s, j (j)}
									<div class="step">
										<b>{s.kind === 'thinking' ? L('Thinking', '思考') : s.name}</b>{#if s.detail} · {s.detail}{/if}
										{#if s.output || s.text}<span class="out">{s.output ?? s.text}</span>{/if}
									</div>
								{/each}
							</div>
						</details>
					{/if}
					{#each item.approvals as a (a.id)}
						{#if a.answer}
							<div class="step">{a.answer === 'deny' ? L('Denied', '已拒绝') : L('Allowed', '已允许')} · {a.tool}</div>
						{:else}
							<div class="approval">
								<div class="what">{L('Needs your approval', '需要你确认')} · {a.tool}</div>
								<div class="detail">{a.summary}</div>
								<div class="choices">
									<button class="btn primary" onclick={() => answer(a, 'approve')}>{L('Allow', '允许')}</button>
									<button class="btn" onclick={() => answer(a, 'always')}>{L('Always allow', '总是允许')}</button>
									<button class="btn plain" onclick={() => answer(a, 'deny')}>{L('Deny', '拒绝')}</button>
								</div>
							</div>
						{/if}
					{/each}
					{#if item.text}
						<div class="text" class:caret={item.streaming}>{item.text}</div>
					{:else if item.streaming}
						<span class="working"><i></i><i></i><i></i></span>
					{/if}
					{#if item.error}<div class="err">{item.error}</div>{/if}
				</div>
			{/if}
		{/each}
	</div>
	<form class="composer" onsubmit={(e) => { e.preventDefault(); busy ? stop() : send(); }}>
		<textarea rows="1" bind:value={draft} onkeydown={keydown} disabled={!available} placeholder={L('Message Zagent', '发消息给 Zagent')}></textarea>
		<button class="send" type="submit" disabled={!available || (!busy && !draft.trim())} aria-label={busy ? L('Stop', '停止') : L('Send', '发送')}>
			<i class="f7-icons">{busy ? 'stop_fill' : 'arrow_up'}</i>
		</button>
	</form>
</div>

<style>
	.chat { display: flex; flex-direction: column; height: 100%; min-height: 0; }
	.transcript {
		flex: 1; min-height: 0; overflow-y: auto; scrollbar-width: none;
		padding: 20px 28px 12px; display: flex; flex-direction: column; gap: 18px;
	}
	.empty { margin: auto; text-align: center; display: flex; flex-direction: column; align-items: center; gap: 12px; }
	.empty h2 { margin: 0; font-size: 18px; font-weight: 600; color: var(--zd-text); }
	.user {
		align-self: flex-end; max-width: 78%; padding: 8px 14px; border-radius: 16px 16px 5px 16px;
		background: var(--zd-hover); white-space: pre-wrap; overflow-wrap: anywhere; font-size: 13px;
	}
	.agent { font-size: 13px; line-height: 1.55; overflow-wrap: anywhere; }
	.text { white-space: pre-wrap; }
	.caret::after {
		content: ''; display: inline-block; width: 6px; height: 13px; margin-left: 3px; vertical-align: -2px;
		background: var(--zd-text); border-radius: 1px; animation: blink 1s steps(2) infinite;
	}
	@keyframes blink { 50% { opacity: 0; } }
	.working { display: inline-flex; gap: 4px; padding: 4px 0; }
	.working i { width: 6px; height: 6px; border-radius: 50%; background: var(--zd-text-tertiary); animation: breathe 1.2s ease-in-out infinite; }
	.working i:nth-child(2) { animation-delay: 0.15s; }
	.working i:nth-child(3) { animation-delay: 0.3s; }
	@keyframes breathe { 0%, 100% { opacity: 0.3; transform: scale(0.85); } 50% { opacity: 1; transform: none; } }
	.process { margin-bottom: 8px; }
	.process summary {
		list-style: none; cursor: pointer; display: inline-flex; align-items: center; gap: 4px;
		color: var(--zd-text-tertiary); font-size: 12px;
	}
	.process summary::-webkit-details-marker { display: none; }
	.process summary i { font-size: 10px; transition: transform 160ms; }
	.process[open] summary i { transform: rotate(90deg); }
	.steps { margin: 8px 0 0 4px; padding-left: 12px; border-left: 1px solid var(--zd-hairline-strong); display: flex; flex-direction: column; gap: 6px; }
	.step { font-size: 12px; color: var(--zd-text-secondary); margin-bottom: 6px; }
	.step b { color: var(--zd-text); font-weight: 500; }
	.out { display: block; color: var(--zd-text-tertiary); white-space: pre-wrap; max-height: 96px; overflow: hidden; }
	.approval {
		border: 1px solid var(--zd-hairline-strong); border-radius: 10px; padding: 14px 16px; margin-bottom: 8px;
		background: var(--zd-control-bg);
	}
	.what { font-weight: 500; margin-bottom: 2px; }
	.detail { color: var(--zd-text-secondary); font-size: 12px; margin-bottom: 12px; }
	.choices { display: flex; gap: 8px; flex-wrap: wrap; }
	.btn {
		height: 26px; padding: 0 12px; border-radius: 7px; border: 0; font-size: 12px; cursor: pointer;
		background: var(--zd-hover); color: var(--zd-text);
	}
	.btn.primary { background: var(--zd-text); color: var(--zd-window-bg); }
	.btn.plain { background: none; color: var(--zd-text-secondary); }
	.err { color: var(--zd-red); font-size: 12px; margin-top: 6px; }
	.composer {
		display: flex; align-items: flex-end; gap: 8px; margin: 0 20px 16px; padding: 6px 6px 6px 14px;
		border-radius: 18px; border: 1px solid var(--zd-hairline-strong); background: var(--zd-control-bg);
	}
	textarea {
		flex: 1; min-height: 20px; max-height: 140px; resize: none; border: 0; outline: 0; background: none;
		font: inherit; font-size: 13px; line-height: 1.5; padding: 4px 0; color: var(--zd-text); field-sizing: content;
	}
	textarea::placeholder { color: var(--zd-text-tertiary); }
	textarea:disabled { cursor: default; }
	.send {
		width: 28px; height: 28px; border-radius: 50%; border: 0; flex: none; cursor: pointer;
		background: var(--zd-text); color: var(--zd-window-bg); display: grid; place-items: center; font-size: 13px;
	}
	.send:disabled { opacity: 0.25; cursor: default; }
	.f7-icons { font-style: normal; line-height: 1; }
</style>
