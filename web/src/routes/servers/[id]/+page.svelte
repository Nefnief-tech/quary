<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/state';
	import { api, type Server } from '$lib/api';

	let server = $state<Server | null>(null);
	let error = $state('');
	let tab = $state<'console' | 'files'>('console');

	let consoleEl: HTMLDivElement | null = $state(null);
	let inputLine = $state('');
	let statusDetail = $state('');
	let stats = $state<{ cpu_percent: number; memory_used_mb: number; memory_limit_mb: number } | null>(
		null
	);
	let wsState = $state<'connecting' | 'live' | 'down'>('connecting');

	let files = $state<{ name: string; size: number; is_dir: boolean; modified: string }[]>([]);
	let filePath = $state('');
	let fileContent = $state('');
	let fileEditing = $state('');

	const id = $derived(page.params.id ?? '');
	let ws: WebSocket | null = null;
	let term: import('@xterm/xterm').Terminal | null = null;
	let fit: import('@xterm/addon-fit').FitAddon | null = null;
	let resizeObs: ResizeObserver | null = null;

	const statusStyle: Record<string, string> = {
		running: 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30',
		starting: 'bg-amber-500/15 text-amber-300 ring-amber-500/30',
		stopping: 'bg-amber-500/15 text-amber-300 ring-amber-500/30',
		installing: 'bg-sky-500/15 text-sky-300 ring-sky-500/30',
		offline: 'bg-slate-500/15 text-slate-400 ring-slate-500/30',
		crashed: 'bg-rose-500/15 text-rose-300 ring-rose-500/30',
		suspended: 'bg-rose-500/15 text-rose-300 ring-rose-500/30'
	};

	const memPct = $derived(
		stats && stats.memory_limit_mb > 0
			? Math.min(100, (stats.memory_used_mb / stats.memory_limit_mb) * 100)
			: 0
	);
	const cpuPct = $derived(stats ? Math.min(100, stats.cpu_percent) : 0);

	async function loadFiles(path = '') {
		try {
			files = await api.get<typeof files>(
				`/api/v1/servers/${id}/files?path=${encodeURIComponent(path)}`
			);
			filePath = path;
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function openFile(name: string) {
		const path = filePath ? `${filePath}/${name}` : name;
		const res = await api.get<{ content: string }>(
			`/api/v1/servers/${id}/files/content?path=${encodeURIComponent(path)}`
		);
		fileContent = res.content;
		fileEditing = path;
		tab = 'files';
	}

	async function saveFile() {
		if (!fileEditing) return;
		await api.put(`/api/v1/servers/${id}/files/content`, {
			path: fileEditing,
			content: fileContent,
			create_dirs: true
		});
		fileEditing = '';
		await loadFiles(filePath);
	}

	async function deleteFile(name: string, isDir: boolean) {
		const path = filePath ? `${filePath}/${name}` : name;
		if (!confirm(`Delete ${path}?`)) return;
		await api.del(
			`/api/v1/servers/${id}/files?path=${encodeURIComponent(path)}&recursive=${isDir}`
		);
		await loadFiles(filePath);
	}

	function power(action: string) {
		api
			.post(`/api/v1/servers/${id}/power`, { action })
			.catch((e) => (error = e instanceof Error ? e.message : String(e)));
	}

	onMount(async () => {
		server = await api.get<Server>(`/api/v1/servers/${id}`);
		loadFiles('');

		const t = await api.get<{ token: string; scrollback: string[] }>(
			`/api/v1/servers/${id}/console/token`
		);
		if (consoleEl) {
			const { Terminal } = await import('@xterm/xterm');
			const { FitAddon } = await import('@xterm/addon-fit');
			term = new Terminal({
				convertEol: true,
				fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
				fontSize: 12.5,
				theme: {
					background: '#0b1120',
					foreground: '#cbd5e1',
					cursor: '#34d399',
					green: '#34d399',
					yellow: '#fbbf24',
					red: '#fb7185'
				}
			});
			fit = new FitAddon();
			term.loadAddon(fit);
			term.open(consoleEl);
			fit.fit();
			resizeObs = new ResizeObserver(() => fit?.fit());
			resizeObs.observe(consoleEl);
			for (const line of t.scrollback ?? []) term.writeln(line);
		}

		const proto = location.protocol === 'https:' ? 'wss' : 'ws';
		wsState = 'connecting';
		ws = new WebSocket(`${proto}://${location.host}/api/v1/servers/${id}/console?token=${t.token}`);
		ws.onopen = () => (wsState = 'live');
		ws.onclose = () => (wsState = 'down');
		ws.onerror = () => (wsState = 'down');
		ws.onmessage = (ev) => {
			const frame = JSON.parse(ev.data);
			if (frame.type === 'console') {
				term?.writeln(frame.line as string);
			} else if (frame.type === 'status') {
				if (server) server = { ...server, status: frame.status };
				statusDetail = frame.detail ?? '';
			} else if (frame.type === 'stats' && frame.stats) {
				stats = frame.stats;
			} else if (frame.type === 'install') {
				statusDetail = `install: ${frame.step} ${Math.round(frame.percent ?? 0)}%`;
			}
		};
	});

	onDestroy(() => {
		resizeObs?.disconnect();
		ws?.close();
		term?.dispose();
	});

	async function sendLine(e: SubmitEvent) {
		e.preventDefault();
		if (!inputLine || !ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(inputLine);
		inputLine = '';
	}
</script>

<svelte:head>
	<title>{server?.name ?? 'Server'} — Quarry</title>
</svelte:head>

{#if server}
	<div class="mb-5 flex flex-wrap items-center gap-3">
		<a href="/" class="text-sm text-slate-500 transition hover:text-slate-300">← Servers</a>
		<h1 class="text-2xl font-bold tracking-tight text-slate-50">{server.name}</h1>
		<span
			class={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ring-1 ${statusStyle[server.status] ?? statusStyle.offline}`}
		>
			{server.status}
		</span>
		{#if statusDetail}<span class="text-xs text-slate-500">{statusDetail}</span>{/if}
		<span class="ml-auto rounded bg-slate-900 px-2.5 py-1 font-mono text-xs text-slate-400 ring-1 ring-slate-800">
			{server.ip}:{server.port}
		</span>
	</div>

	<!-- Power + stats row -->
	<div class="mb-6 grid grid-cols-1 gap-4 md:grid-cols-3">
		<div class="flex flex-wrap items-center gap-2 rounded-xl border border-slate-800 bg-slate-900/70 p-4">
			<button
				class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white shadow-lg shadow-emerald-950/40 transition hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-30"
				disabled={!['offline', 'crashed'].includes(server.status)}
				onclick={() => power('start')}>▶ Start</button
			>
			<button
				class="rounded-lg bg-amber-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-amber-500 disabled:cursor-not-allowed disabled:opacity-30"
				disabled={!['running', 'starting', 'stopping'].includes(server.status)}
				onclick={() => power('stop')}>■ Stop</button
			>
			<button
				class="rounded-lg bg-slate-700 px-4 py-2 text-sm font-semibold text-slate-100 transition hover:bg-slate-600 disabled:cursor-not-allowed disabled:opacity-30"
				disabled={!['running', 'crashed'].includes(server.status)}
				onclick={() => power('restart')}>⟳ Restart</button
			>
			<button
				class="rounded-lg bg-rose-700/80 px-4 py-2 text-sm font-semibold text-white transition hover:bg-rose-600 disabled:cursor-not-allowed disabled:opacity-30"
				disabled={!['running', 'starting', 'stopping', 'crashed'].includes(server.status)}
				onclick={() => power('kill')}>✕ Kill</button
			>
		</div>
		<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-4">
			<div class="flex items-center justify-between text-xs font-medium uppercase tracking-wide text-slate-500">
				<span>CPU</span><span>{stats ? stats.cpu_percent.toFixed(1) : '–'}%</span>
			</div>
			<div class="mt-2.5 h-2 overflow-hidden rounded-full bg-slate-800">
				<div
					class="h-full rounded-full bg-gradient-to-r from-emerald-600 to-emerald-400 transition-all duration-500"
					style="width: {cpuPct}%"
				></div>
			</div>
		</div>
		<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-4">
			<div class="flex items-center justify-between text-xs font-medium uppercase tracking-wide text-slate-500">
				<span>Memory</span>
				<span>
					{stats ? stats.memory_used_mb.toFixed(0) : '–'} /
					{stats ? stats.memory_limit_mb.toFixed(0) : '–'} MiB
				</span>
			</div>
			<div class="mt-2.5 h-2 overflow-hidden rounded-full bg-slate-800">
				<div
					class="h-full rounded-full bg-gradient-to-r from-sky-600 to-sky-400 transition-all duration-500"
					style="width: {memPct}%"
				></div>
			</div>
		</div>
	</div>

	{#if error}
		<p class="mb-4 rounded-lg border border-rose-900/60 bg-rose-950/40 px-3 py-2 text-sm text-rose-300">
			{error}
		</p>
	{/if}

	<div class="mb-4 flex items-center gap-2 text-sm">
		<button
			class={`rounded-lg px-4 py-2 font-medium transition ${tab === 'console' ? 'bg-slate-800 text-slate-100 ring-1 ring-slate-700' : 'text-slate-400 hover:text-slate-200'}`}
			onclick={() => (tab = 'console')}
		>
			Console
		</button>
		<button
			class={`rounded-lg px-4 py-2 font-medium transition ${tab === 'files' ? 'bg-slate-800 text-slate-100 ring-1 ring-slate-700' : 'text-slate-400 hover:text-slate-200'}`}
			onclick={() => (tab = 'files')}
		>
			Files
		</button>
		{#if tab === 'console'}
			<span class="ml-auto flex items-center gap-2 text-xs text-slate-500">
				<span
					class={`h-2 w-2 rounded-full ${wsState === 'live' ? 'animate-pulse bg-emerald-400' : wsState === 'connecting' ? 'bg-amber-400' : 'bg-rose-500'}`}
				></span>
				{wsState === 'live' ? 'live' : wsState === 'connecting' ? 'connecting…' : 'disconnected'}
			</span>
		{/if}
	</div>

	{#if tab === 'console'}
		<div class="overflow-hidden rounded-xl border border-slate-800">
			<div bind:this={consoleEl} class="h-[26rem] w-full bg-slate-950 p-2"></div>
			<form
				onsubmit={sendLine}
				class="flex items-center gap-2 border-t border-slate-800 bg-slate-900/80 px-3 py-2.5"
			>
				<span class="font-mono text-sm text-emerald-400">$</span>
				<input
					class="flex-1 bg-transparent font-mono text-sm text-slate-100 outline-none placeholder:text-slate-600"
					placeholder="type a server command — e.g. list, say hello, time set day"
					bind:value={inputLine}
				/>
				<button
					type="submit"
					class="rounded-md bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-300 transition hover:bg-slate-700"
				>
					Send ⏎
				</button>
			</form>
		</div>
	{:else}
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-4">
				<div class="mb-2 flex items-center gap-2 text-sm">
					<button class="text-slate-400 transition hover:text-slate-200" onclick={() => loadFiles('')}>
						root
					</button>
					{#if filePath}
						<span class="font-mono text-slate-300">/{filePath}</span>
					{/if}
				</div>
				<ul class="max-h-96 overflow-y-auto font-mono text-sm">
					{#each files as f (f.name)}
						<li class="group flex items-center gap-2 border-b border-slate-800/50 py-1.5">
							{#if f.is_dir}
								<button
									class="text-sky-300 transition hover:underline"
									onclick={() => loadFiles(filePath ? `${filePath}/${f.name}` : f.name)}
								>
									{f.name}/
								</button>
							{:else}
								<button
									class="text-slate-200 transition hover:text-emerald-300 hover:underline"
									onclick={() => openFile(f.name)}
								>
									{f.name}
								</button>
							{/if}
							<span class="ml-auto text-xs text-slate-600">{f.size} B</span>
							<button
								class="text-xs text-rose-400/70 opacity-0 transition group-hover:opacity-100 hover:text-rose-300"
								onclick={() => deleteFile(f.name, f.is_dir)}>del</button
							>
						</li>
					{:else}
						<li class="py-1.5 text-slate-500">empty directory</li>
					{/each}
				</ul>
			</div>
			<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-4">
				{#if fileEditing}
					<div class="mb-2 flex items-center gap-2 text-sm">
						<span class="font-mono text-slate-300">{fileEditing}</span>
						<button
							class="ml-auto rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-emerald-500"
							onclick={saveFile}>Save</button
						>
						<button
							class="rounded-lg border border-slate-700 px-3 py-1.5 text-xs text-slate-300 transition hover:border-slate-500"
							onclick={() => (fileEditing = '')}>Close</button
						>
					</div>
					<textarea
						class="h-80 w-full rounded-lg border border-slate-700/80 bg-slate-950 p-3 font-mono text-xs text-slate-100 outline-none focus:border-emerald-500"
						bind:value={fileContent}
						spellcheck="false"
					></textarea>
				{:else}
					<p class="text-sm text-slate-500">Select a file to view or edit it.</p>
				{/if}
			</div>
		</div>
	{/if}
{/if}
