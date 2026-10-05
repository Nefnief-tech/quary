<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Server } from '$lib/api';

	let server = $state<Server | null>(null);
	let error = $state('');
	let tab = $state<'console' | 'files' | 'plugins' | 'settings'>('console');

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

	// plugins state
	let plugins = $state<{ name: string; size: number }[]>([]);
	let pluginQuery = $state('');
	let pluginResults = $state<
		{ project_id: string; title: string; author: string; description: string; downloads: number }[]
	>([]);
	let pluginSearchBusy = $state(false);
	let pluginSearchDone = $state(false);
	let installingId = $state('');
	let pluginNote = $state('');

	const hasMarket = $derived(server?.runner === 'paper' || server?.runner === 'fabric');

	async function loadPlugins() {
		if (!hasMarket) return;
		try {
			plugins = await api.get<typeof plugins>(`/api/v1/servers/${id}/plugins`);
		} catch {
			plugins = [];
		}
	}

	async function searchPlugins(e?: SubmitEvent) {
		e?.preventDefault();
		pluginSearchBusy = true;
		pluginNote = '';
		try {
			const res = await api.get<{
				hits: typeof pluginResults;
			}>(`/api/v1/servers/${id}/plugins/search?q=${encodeURIComponent(pluginQuery)}`);
			pluginResults = res.hits;
			pluginSearchDone = true;
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			pluginSearchBusy = false;
		}
	}

	async function installPlugin(projectId: string) {
		installingId = projectId;
		pluginNote = '';
		try {
			const res = await api.post<{ filename: string; hint: string }>(
				`/api/v1/servers/${id}/plugins/install`,
				{ project_id: projectId }
			);
			pluginNote = `Installed ${res.filename}. ${res.hint}.`;
			await loadPlugins();
		} catch (err) {
			pluginNote = err instanceof Error ? err.message : String(err);
		} finally {
			installingId = '';
		}
	}

	async function deletePlugin(name: string) {
		if (!confirm(`Remove ${name}?`)) return;
		await api.del(`/api/v1/servers/${id}/plugins?name=${encodeURIComponent(name)}`);
		await loadPlugins();
	}

	function openPlugins() {
		tab = 'plugins';
		loadPlugins();
	}

	// settings state
	let settings = $state({ name: '', memory_mb: 0, cpu_percent: 0 });
	let settingsBusy = $state(false);
	let settingsNotes = $state<string[]>([]);
	let allocs = $state<{ id: string; ip: string; port: number; in_use_by: string | null; is_current: boolean }[]>([]);
	let movingTo = $state('');

	async function openSettings() {
		tab = 'settings';
		if (server) {
			settings = { name: server.name, memory_mb: server.memory_mb, cpu_percent: server.cpu_percent };
		}
		try {
			allocs = await api.get<typeof allocs>(`/api/v1/servers/${id}/allocations`);
		} catch {
			allocs = [];
		}
	}

	async function saveSettings(e: SubmitEvent) {
		e.preventDefault();
		settingsBusy = true;
		error = '';
		try {
			const res = await api.patch<{
				notes: string[];
				name: string;
				memory_mb: number;
				cpu_percent: number;
				status: string;
			}>(`/api/v1/servers/${id}`, {
				name: settings.name,
				memory_mb: Number(settings.memory_mb),
				cpu_percent: Number(settings.cpu_percent)
			});
			server = { ...server!, name: res.name, memory_mb: res.memory_mb, cpu_percent: res.cpu_percent };
			settingsNotes = res.notes;
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			settingsBusy = false;
		}
	}

	async function movePort(allocId: string, ip: string, port: number) {
		if (!confirm(`Move this server to ${ip}:${port}? The server must be restarted afterwards.`)) return;
		error = '';
		try {
			const res = await api.patch<{ notes: string[] }>(`/api/v1/servers/${id}`, { allocation_id: allocId });
			settingsNotes = res.notes;
			if (server) server = { ...server, ip, port };
			await openSettings();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function deleteServer() {
		if (!confirm(`Delete ${server?.name} and wipe its data? This cannot be undone.`)) return;
		try {
			await api.del(`/api/v1/servers/${id}`);
			goto('/');
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	const id = $derived(page.params.id ?? '');
	let ws: WebSocket | null = null;
	let term: import('@xterm/xterm').Terminal | null = null;
	let fit: import('@xterm/addon-fit').FitAddon | null = null;
	let resizeObs: ResizeObserver | null = null;
	let destroyed = false;
	let wsRetry = 0;
	let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

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
		connectConsole();
	});

	// The console div is inside {#if server}, so it only exists after the
	// server loads — and again on every Console-tab switch. This effect
	// (re)binds the terminal whenever a fresh element appears.
	let termBoundTo: HTMLDivElement | null = null;
	$effect(() => {
		const el = consoleEl;
		if (el && el !== termBoundTo && tab === 'console') {
			termBoundTo = el;
			void initTerminal(el);
		}
	});

	async function initTerminal(el: HTMLDivElement) {
		const { Terminal } = await import('@xterm/xterm');
		const { FitAddon } = await import('@xterm/addon-fit');
		term?.dispose();
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
		term.open(el);
		fit.fit();
		resizeObs?.disconnect();
		resizeObs = new ResizeObserver(() => fit?.fit());
		resizeObs.observe(el);

		// Replay history; the live stream keeps flowing through ws.onmessage.
		try {
			const t = await api.get<{ token: string; scrollback: string[] }>(
				`/api/v1/servers/${id}/console/token`
			);
			for (const line of t.scrollback ?? []) term.writeln(line);
			term.writeln('— connected —');
		} catch {
			term.writeln('— connected (history unavailable) —');
		}
	}

	// connectConsole mints a fresh token and opens the console WebSocket.
	// On drop it retries with capped backoff so panel/daemon restarts heal
	// without user action; each reconnect replays fresh scrollback.
	async function connectConsole() {
		if (destroyed) return;
		wsState = 'connecting';
		wsRetry = Math.min(wsRetry + 1, 5);
		try {
			const t = await api.get<{ token: string; scrollback: string[] }>(
				`/api/v1/servers/${id}/console/token`
			);
			const proto = location.protocol === 'https:' ? 'wss' : 'ws';
			const sock = new WebSocket(
				`${proto}://${location.host}/api/v1/servers/${id}/console?token=${t.token}`
			);
			ws = sock;
			sock.onopen = () => {
				wsState = 'live';
				wsRetry = 0;
				// Fresh connection: replay the hub's scrollback once.
				term?.writeln('');
				term?.writeln(`— connected —`);
				for (const line of t.scrollback ?? []) term?.writeln(line);
			};
			sock.onclose = () => {
				if (ws === sock) {
					wsState = 'down';
					ws = null;
					scheduleReconnect();
				}
			};
			sock.onerror = () => {};
			sock.onmessage = (ev) => {
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
		} catch {
			wsState = 'down';
			scheduleReconnect();
		}
	}

	function scheduleReconnect() {
		if (destroyed) return;
		const delay = Math.min(1000 * 2 ** wsRetry, 10000);
		reconnectTimer = setTimeout(connectConsole, delay);
	}

	onDestroy(() => {
		destroyed = true;
		clearTimeout(reconnectTimer);
		resizeObs?.disconnect();
		ws?.close();
		term?.dispose();
	});

	async function sendLine(e: SubmitEvent) {
		e.preventDefault();
		if (!inputLine) return;
		if (!ws || ws.readyState !== WebSocket.OPEN) {
			error = 'console is not connected — retrying automatically…';
			scheduleReconnect();
			return;
		}
		ws.send(inputLine);
		inputLine = '';
		error = '';
	}

	function clearConsole() {
		term?.clear();
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
		{#if hasMarket}
			<button
				class={`rounded-lg px-4 py-2 font-medium transition ${tab === 'plugins' ? 'bg-slate-800 text-slate-100 ring-1 ring-slate-700' : 'text-slate-400 hover:text-slate-200'}`}
				onclick={openPlugins}
			>
				Plugins
			</button>
		{/if}
		<button
			class={`rounded-lg px-4 py-2 font-medium transition ${tab === 'settings' ? 'bg-slate-800 text-slate-100 ring-1 ring-slate-700' : 'text-slate-400 hover:text-slate-200'}`}
			onclick={openSettings}
		>
			Settings
		</button>
		{#if tab === 'console'}
			<span class="ml-auto flex items-center gap-2 text-xs text-slate-500">
				<span
					class={`h-2 w-2 rounded-full ${wsState === 'live' ? 'animate-pulse bg-emerald-400' : wsState === 'connecting' ? 'animate-pulse bg-amber-400' : 'bg-rose-500'}`}
				></span>
				{wsState === 'live'
					? 'live'
					: wsState === 'connecting'
						? 'connecting…'
						: 'reconnecting…'}
			</span>
		{/if}
	</div>

	{#if tab === 'console'}
		<div class="overflow-hidden rounded-xl border border-slate-800">
			<div
				class="flex items-center justify-between border-b border-slate-800 bg-slate-900/80 px-3 py-1.5"
			>
				<span class="text-[11px] font-semibold uppercase tracking-widest text-slate-500">
					{server.name} — console
				</span>
				<button
					class="rounded px-2 py-0.5 text-[11px] text-slate-500 transition hover:bg-slate-800 hover:text-slate-300"
					onclick={() => clearConsole()}
				>
					clear
				</button>
			</div>
			<div bind:this={consoleEl} class="h-[26rem] w-full bg-slate-950 p-2"></div>
			<form
				onsubmit={sendLine}
				class="flex items-center gap-2 border-t border-slate-800 bg-slate-900/80 px-3 py-2.5"
			>
				<span class="font-mono text-sm {wsState === 'live' ? 'text-emerald-400' : 'text-slate-600'}">$</span>
				<input
					class="flex-1 bg-transparent font-mono text-sm text-slate-100 outline-none placeholder:text-slate-600"
					placeholder={wsState === 'live'
						? 'type a server command — e.g. list, say hello, time set day'
						: 'console reconnecting…'}
					bind:value={inputLine}
				/>
				<button
					type="submit"
					disabled={wsState !== 'live' || !inputLine}
					class="rounded-md bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-300 transition hover:bg-slate-700 disabled:cursor-not-allowed disabled:opacity-40"
				>
					Send ⏎
				</button>
			</form>
		</div>
	{:else if tab === 'files'}
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
	{:else if tab === 'plugins'}
		<div class="space-y-6">
			<form onsubmit={searchPlugins} class="flex gap-2">
				<input
					class="flex-1 rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-emerald-500"
					placeholder="Search Modrinth for {server.mc_version} plugins…"
					bind:value={pluginQuery}
				/>
				<button
					type="submit"
					disabled={pluginSearchBusy}
					class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-emerald-500 disabled:opacity-50"
				>
					{pluginSearchBusy ? '…' : 'Search'}
				</button>
			</form>

			{#if pluginNote}
				<p class="rounded-lg border border-emerald-900/50 bg-emerald-950/30 px-3 py-2 text-sm text-emerald-300">
					{pluginNote}
				</p>
			{/if}

			{#if pluginSearchDone}
				<div class="rounded-xl border border-slate-800 bg-slate-900/70">
					{#each pluginResults as hit (hit.project_id)}
						<div class="flex items-center gap-3 border-b border-slate-800/60 p-4 last:border-0">
							<div class="min-w-0 flex-1">
								<p class="truncate font-medium text-slate-100">{hit.title}</p>
								<p class="truncate text-xs text-slate-500">
									{hit.author} · {hit.downloads.toLocaleString()} downloads — {hit.description}
								</p>
							</div>
							<button
								class="shrink-0 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-emerald-500 disabled:opacity-40"
								disabled={installingId === hit.project_id}
								onclick={() => installPlugin(hit.project_id)}
							>
								{installingId === hit.project_id ? 'Installing…' : 'Install'}
							</button>
						</div>
					{:else}
						<p class="p-4 text-sm text-slate-500">No results.</p>
					{/each}
				</div>
			{/if}

			<div>
				<h3 class="mb-2 text-xs font-bold uppercase tracking-widest text-slate-500">
					Installed ({plugins.length})
				</h3>
				<div class="rounded-xl border border-slate-800 bg-slate-900/70 font-mono text-sm">
					{#each plugins as p (p.name)}
						<div class="group flex items-center gap-3 border-b border-slate-800/60 px-4 py-2.5 last:border-0">
							<span class="text-slate-200">{p.name}</span>
							<span class="ml-auto text-xs text-slate-600">{p.size} B</span>
							<button
								class="text-xs text-rose-400/70 transition hover:text-rose-300"
								onclick={() => deletePlugin(p.name)}>remove</button
							>
						</div>
					{:else}
						<p class="px-4 py-3 text-slate-500">
							Nothing installed yet. Search above — plugins land in the server's plugins directory;
							restart the server to load them.
						</p>
					{/each}
				</div>
			</div>
		</div>
	{:else}
		<div class="space-y-6">
			{#if settingsNotes.length}
				<p class="rounded-lg border border-emerald-900/50 bg-emerald-950/30 px-3 py-2 text-sm text-emerald-300">
					{settingsNotes.join(' · ')}
				</p>
			{/if}

			<form onsubmit={saveSettings} class="rounded-xl border border-slate-800 bg-slate-900/70 p-5">
				<h3 class="mb-4 text-xs font-bold uppercase tracking-widest text-slate-500">General</h3>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-slate-400">Name</span>
						<input
							class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
							bind:value={settings.name}
							required
						/>
					</label>
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-slate-400">Memory (MiB)</span>
						<input
							type="number"
							min="512"
							step="512"
							class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
							bind:value={settings.memory_mb}
						/>
					</label>
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-slate-400">CPU (100 = one core)</span>
						<input
							type="number"
							min="50"
							max="3200"
							step="50"
							class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
							bind:value={settings.cpu_percent}
						/>
					</label>
				</div>
				<div class="mt-4 flex items-center gap-4">
					<button
						type="submit"
						disabled={settingsBusy}
						class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-emerald-500 disabled:opacity-50"
					>
						{settingsBusy ? 'Saving…' : 'Save changes'}
					</button>
					<p class="text-xs text-slate-600">Memory and CPU changes apply on the next start.</p>
				</div>
			</form>

			<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-5">
				<h3 class="mb-1 text-xs font-bold uppercase tracking-widest text-slate-500">Network</h3>
				<p class="mb-3 text-xs text-slate-600">
					Current address: <span class="font-mono text-slate-400">{server.ip}:{server.port}</span>.
					Move the server to any free allocation (stop the server first).
				</p>
				<ul class="grid grid-cols-1 gap-2 md:grid-cols-3">
					{#each allocs as a (a.id)}
						<div
							class={`flex items-center gap-2 rounded-lg border px-3 py-2 font-mono text-sm ${a.is_current ? 'border-emerald-700/60 bg-emerald-950/20 text-emerald-300' : a.in_use_by ? 'border-slate-800 bg-slate-950/60 text-slate-600' : 'border-slate-800 bg-slate-950 text-slate-300'}`}
						>
							<span>{a.ip}:{a.port}</span>
							{#if a.is_current}
								<span class="ml-auto text-[10px] uppercase tracking-wide">current</span>
							{:else if a.in_use_by}
								<span class="ml-auto text-[10px] uppercase tracking-wide">in use</span>
							{:else}
								<button
									class="ml-auto rounded bg-slate-800 px-2 py-0.5 text-[11px] text-slate-200 transition hover:bg-slate-700"
									onclick={() => movePort(a.id, a.ip, a.port)}>move here</button
								>
							{/if}
						</div>
					{:else}
						<p class="text-sm text-slate-500">
							No allocations on this node — add a range in the Admin area.
						</p>
					{/each}
				</ul>
			</div>

			<div class="rounded-xl border border-rose-900/50 bg-rose-950/20 p-5">
				<h3 class="mb-1 text-xs font-bold uppercase tracking-widest text-rose-400">Danger zone</h3>
				<p class="mb-3 text-xs text-slate-500">
					Deleting removes the server and wipes its worlds and files from the node.
				</p>
				<button
					class="rounded-lg bg-rose-700/80 px-4 py-2 text-sm font-semibold text-white transition hover:bg-rose-600 disabled:opacity-40"
					disabled={['running', 'starting', 'stopping'].includes(server.status)}
					onclick={deleteServer}
				>
					Delete server
				</button>
			</div>
		</div>
	{/if}
{/if}

