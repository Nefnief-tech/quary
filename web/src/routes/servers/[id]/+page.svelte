<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/state';
	import { api, type Server } from '$lib/api';

	let server = $state<Server | null>(null);
	let error = $state('');
	let tab = $state<'console' | 'files'>('console');

	// console state
	let consoleEl: HTMLDivElement | null = $state(null);
	let inputLine = $state('');
	let statusDetail = $state('');
	let stats = $state<{ cpu_percent: number; memory_used_mb: number; memory_limit_mb: number } | null>(
		null
	);

	// files state
	let files = $state<{ name: string; size: number; is_dir: boolean; modified: string }[]>([]);
	let filePath = $state('');
	let fileContent = $state('');
	let fileEditing = $state('');

	const id = $derived(page.params.id ?? '');
	let ws: WebSocket | null = null;
	let term: import('@xterm/xterm').Terminal | null = null;

	const statusColor: Record<string, string> = {
		running: 'bg-emerald-500',
		starting: 'bg-amber-400',
		stopping: 'bg-amber-400',
		installing: 'bg-sky-400',
		offline: 'bg-slate-500',
		crashed: 'bg-rose-500',
		suspended: 'bg-rose-400'
	};

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

		// Console over WebSocket with a short-lived signed token.
		const t = await api.get<{ token: string; scrollback: string[] }>(
			`/api/v1/servers/${id}/console/token`
		);
		if (consoleEl) {
			const { Terminal } = await import('@xterm/xterm');
			const { FitAddon } = await import('@xterm/addon-fit');
			term = new Terminal({
				convertEol: true,
				fontFamily: 'ui-monospace, monospace',
				theme: { background: '#020617' }
			});
			const fit = new FitAddon();
			term.loadAddon(fit);
			term.open(consoleEl);
			fit.fit();
			for (const line of t.scrollback ?? []) term.writeln(line);
		}

		const proto = location.protocol === 'https:' ? 'wss' : 'ws';
		ws = new WebSocket(`${proto}://${location.host}/api/v1/servers/${id}/console?token=${t.token}`);
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
		ws?.close();
		term?.dispose();
	});

	async function sendLine(e: SubmitEvent) {
		e.preventDefault();
		if (!inputLine || !ws) return;
		ws.send(inputLine);
		inputLine = '';
	}
</script>

<svelte:head>
	<title>{server?.name ?? 'Server'} — Quarry</title>
</svelte:head>

{#if server}
	<div class="mb-4 flex flex-wrap items-center gap-3">
		<a href="/" class="text-sm text-slate-400 hover:text-slate-200">← Servers</a>
		<h1 class="text-xl font-semibold text-slate-100">{server.name}</h1>
		<span class={`inline-block h-2.5 w-2.5 rounded-full ${statusColor[server.status] ?? 'bg-slate-500'}`}></span>
		<span class="text-xs uppercase tracking-wide text-slate-500">{server.status}</span>
		{#if statusDetail}<span class="text-xs text-slate-500">{statusDetail}</span>{/if}
		<span class="ml-auto text-sm text-slate-400">{server.ip}:{server.port}</span>
	</div>

	<div class="mb-4 flex flex-wrap items-center gap-2">
		<button
			class="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-40"
			disabled={!['offline', 'crashed'].includes(server.status)}
			onclick={() => power('start')}>Start</button
		>
		<button
			class="rounded bg-amber-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-amber-500 disabled:opacity-40"
			disabled={!['running', 'starting', 'stopping'].includes(server.status)}
			onclick={() => power('stop')}>Stop</button
		>
		<button
			class="rounded bg-slate-700 px-3 py-1.5 text-sm font-medium text-slate-100 hover:bg-slate-600 disabled:opacity-40"
			disabled={!['running', 'crashed'].includes(server.status)}
			onclick={() => power('restart')}>Restart</button
		>
		<button
			class="rounded bg-rose-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-rose-600 disabled:opacity-40"
			disabled={!['running', 'starting', 'stopping', 'crashed'].includes(server.status)}
			onclick={() => power('kill')}>Kill</button
		>
		{#if stats}
			<span class="ml-auto text-xs text-slate-500">
				CPU {stats.cpu_percent.toFixed(1)}% · MEM {stats.memory_used_mb.toFixed(0)}/
				{stats.memory_limit_mb.toFixed(0)} MiB
			</span>
		{/if}
	</div>

	{#if error}<p class="mb-3 text-sm text-rose-400">{error}</p>{/if}

	<div class="mb-3 flex gap-2 text-sm">
		<button
			class={`rounded px-3 py-1.5 ${tab === 'console' ? 'bg-slate-700 text-slate-100' : 'text-slate-400 hover:text-slate-200'}`}
			onclick={() => (tab = 'console')}
		>
			Console
		</button>
		<button
			class={`rounded px-3 py-1.5 ${tab === 'files' ? 'bg-slate-700 text-slate-100' : 'text-slate-400 hover:text-slate-200'}`}
			onclick={() => (tab = 'files')}
		>
			Files
		</button>
	</div>

	{#if tab === 'console'}
		<div class="rounded-lg border border-slate-800 bg-slate-900/60 p-3">
			<div bind:this={consoleEl} class="h-96 w-full"></div>
			<form onsubmit={sendLine} class="mt-3 flex gap-2">
				<input
					class="flex-1 rounded border border-slate-700 bg-slate-800 px-3 py-2 font-mono text-sm text-slate-100 outline-none focus:border-emerald-500"
					placeholder="Type a server command (e.g. list, say hello)…"
					bind:value={inputLine}
				/>
				<button
					type="submit"
					class="rounded bg-slate-700 px-4 py-2 text-sm text-slate-100 hover:bg-slate-600"
				>
					Send
				</button>
			</form>
		</div>
	{:else}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<div class="rounded-lg border border-slate-800 bg-slate-900/60 p-3">
				<div class="mb-2 flex items-center gap-2 text-sm">
					<button class="text-slate-400 hover:text-slate-200" onclick={() => loadFiles('')}>root</button>
					{#if filePath}
						<span class="font-mono text-slate-300">/{filePath}</span>
					{/if}
				</div>
				<ul class="max-h-96 overflow-y-auto font-mono text-sm">
					{#each files as f (f.name)}
						<li class="flex items-center gap-2 border-b border-slate-800/60 py-1.5">
							{#if f.is_dir}
								<button
									class="text-sky-300 hover:underline"
									onclick={() => loadFiles(filePath ? `${filePath}/${f.name}` : f.name)}
								>
									{f.name}/
								</button>
							{:else}
								<button class="text-slate-200 hover:underline" onclick={() => openFile(f.name)}>
									{f.name}
								</button>
							{/if}
							<span class="ml-auto text-xs text-slate-500">{f.size} B</span>
							<button
								class="text-xs text-rose-400 hover:text-rose-300"
								onclick={() => deleteFile(f.name, f.is_dir)}>del</button
							>
						</li>
					{:else}
						<li class="py-1.5 text-slate-500">empty directory</li>
					{/each}
				</ul>
			</div>
			<div class="rounded-lg border border-slate-800 bg-slate-900/60 p-3">
				{#if fileEditing}
					<div class="mb-2 flex items-center gap-2 text-sm text-slate-300">
						<span class="font-mono">{fileEditing}</span>
						<button
							class="ml-auto rounded bg-emerald-600 px-3 py-1 text-xs font-medium text-white hover:bg-emerald-500"
							onclick={saveFile}>Save</button
						>
					</div>
					<textarea
						class="h-80 w-full rounded border border-slate-700 bg-slate-800 p-2 font-mono text-xs text-slate-100 outline-none focus:border-emerald-500"
						bind:value={fileContent}
					></textarea>
				{:else}
					<p class="text-sm text-slate-500">Select a file to view or edit it.</p>
				{/if}
			</div>
		</div>
	{/if}
{/if}
