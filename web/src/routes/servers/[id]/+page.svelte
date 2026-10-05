<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Runner, type Server } from '$lib/api';
	import TimeChart from '$lib/components/TimeChart.svelte';

	let server = $state<Server | null>(null);
	let error = $state('');
	let tab = $state<'console' | 'files' | 'players' | 'plugins' | 'map' | 'settings'>('console');

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
		{
			project_id: string;
			slug: string;
			title: string;
			author: string;
			description: string;
			downloads: number;
			icon_url: string;
		}[]
	>([]);
	let pluginSearchBusy = $state(false);
	let pluginSearchDone = $state(false);
	let installingId = $state('');
	let pluginNote = $state('');

	const hasMarket = $derived(server?.runner === 'paper' || server?.runner === 'fabric');

	// players state
	let players = $state<{ names: string[]; online: number; max: number } | null>(null);
	let bans = $state<{ id: string; name: string; ip: string; reason: string; created_at: string }[]>(
		[]
	);
	let kickFor = $state('');
	let banFor = $state('');
	let banReason = $state('');
	let banIpAddr = $state('');
	let banIpReason = $state('');
	let busyPlayer = $state('');
	let playerNote = $state('');

	async function loadBans() {
		try {
			bans = await api.get<typeof bans>(`/api/v1/servers/${id}/bans`);
		} catch {
			bans = [];
		}
	}

	function openPlayers() {
		tab = 'players';
		loadBans();
		loadWhitelist();
	}

	async function playerAction(action: 'kick' | 'ban' | 'ban_ip' | 'op' | 'deop', name: string, ip = '') {
		busyPlayer = action + (name || ip);
		playerNote = '';
		error = '';
		try {
			await api.post(`/api/v1/servers/${id}/players/action`, {
				action,
				name,
				ip,
				reason: action === 'ban' ? banReason : action === 'ban_ip' ? banIpReason : ''
			});
			playerNote =
				action === 'op' ? `Opped ${name}.`
				: action === 'deop' ? `Deopped ${name}.`
				: action === 'kick' ? `Kicked ${name}.`
				: action === 'ban' ? `Banned ${name}.`
				: `Banned IP ${ip}.`;
			banFor = '';
			banReason = '';
			if (action === 'ban' || action === 'ban_ip') await loadBans();
		} catch (err) {
			playerNote = err instanceof Error ? err.message : String(err);
		} finally {
			busyPlayer = '';
		}
	}

	async function unban(banId: string) {
		playerNote = '';
		try {
			await api.del(`/api/v1/servers/${id}/bans/${banId}`);
			await loadBans();
		} catch (err) {
			playerNote = err instanceof Error ? err.message : String(err);
		}
	}

	// whitelist state
	let whitelist = $state<{ enabled: boolean; names: string[] }>({ enabled: false, names: [] });
	let whitelistAddName = $state('');
	let busyWhitelist = $state(false);

	async function loadWhitelist() {
		try {
			whitelist = await api.get<typeof whitelist>(`/api/v1/servers/${id}/whitelist`);
		} catch {
			/* console read failed; leave current state */
		}
	}

	async function whitelistAdd(e: SubmitEvent) {
		e.preventDefault();
		if (!whitelistAddName) return;
		busyWhitelist = true;
		playerNote = '';
		try {
			await api.post(`/api/v1/servers/${id}/whitelist`, { name: whitelistAddName });
			whitelistAddName = '';
			await loadWhitelist();
		} catch (err) {
			playerNote = err instanceof Error ? err.message : String(err);
		} finally {
			busyWhitelist = false;
		}
	}

	async function whitelistRemove(name: string) {
		busyWhitelist = true;
		try {
			await api.del(`/api/v1/servers/${id}/whitelist?name=${encodeURIComponent(name)}`);
			await loadWhitelist();
		} catch (err) {
			playerNote = err instanceof Error ? err.message : String(err);
		} finally {
			busyWhitelist = false;
		}
	}

	async function whitelistToggle() {
		busyWhitelist = true;
		try {
			await api.post(`/api/v1/servers/${id}/whitelist/toggle`, { enabled: !whitelist.enabled });
			await loadWhitelist();
		} catch (err) {
			playerNote = err instanceof Error ? err.message : String(err);
		} finally {
			busyWhitelist = false;
		}
	}

	// version switcher state
	let runners = $state<Runner[]>([]);
	let vRunner = $state('');
	let vVersion = $state('');
	let switchBusy = $state(false);

	async function switchVersion() {
		if (!vRunner || !vVersion) return;
		if (
			!confirm(
				`Switch to ${vRunner} ${vVersion}? Worlds and configs are kept; the jar is replaced and the server must be started afterwards.`
			)
		)
			return;
		switchBusy = true;
		error = '';
		try {
			const res = await api.post<{ notes: string[]; server: Server }>(
				`/api/v1/servers/${id}/version`,
				{ runner: vRunner, mc_version: vVersion }
			);
			server = res.server;
			settingsNotes = res.notes;
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			switchBusy = false;
		}
	}

	// playit.gg expose state
	let exposeBusy = $state(false);
	let exposeNote = $state('');
	let exposeAddrInput = $state('');

	function applyExpose(ex: Server['expose']) {
		if (server) server = { ...server, expose: ex };
	}

	async function startExpose() {
		exposeBusy = true;
		exposeNote = '';
		try {
			const res = await api.post<{ expose: Server['expose'] }>(`/api/v1/servers/${id}/expose`, {});
			applyExpose(res.expose);
		} catch (err) {
			exposeNote = err instanceof Error ? err.message : String(err);
		} finally {
			exposeBusy = false;
		}
	}

	async function checkClaim() {
		exposeBusy = true;
		exposeNote = '';
		try {
			const res = await api.post<{ expose: Server['expose'] }>(
				`/api/v1/servers/${id}/expose/claim`,
				{}
			);
			applyExpose(res.expose);
			if (res.expose?.active) {
				exposeNote =
					'Tunnel claimed and agent started. Create the tunnel in your playit dashboard, then save the address it gave you.';
			} else {
				exposeNote = 'Not claimed yet — open the claim link first, then try again.';
			}
		} catch (err) {
			exposeNote = err instanceof Error ? err.message : String(err);
		} finally {
			exposeBusy = false;
		}
	}

	async function stopExpose() {
		if (!confirm('Stop the tunnel and remove its agent? The public address will stop working.')) return;
		exposeBusy = true;
		try {
			const res = await api.del<{ expose: Server['expose'] }>(`/api/v1/servers/${id}/expose`);
			applyExpose(res.expose);
			exposeNote = '';
		} catch (err) {
			exposeNote = err instanceof Error ? err.message : String(err);
		} finally {
			exposeBusy = false;
		}
	}

	async function saveExposeAddress(e: SubmitEvent) {
		e.preventDefault();
		exposeBusy = true;
		exposeNote = '';
		try {
			const res = await api.post<{ expose: Server['expose'] }>(
				`/api/v1/servers/${id}/expose/address`,
				{ address: exposeAddrInput }
			);
			applyExpose(res.expose);
			exposeAddrInput = '';
		} catch (err) {
			exposeNote = err instanceof Error ? err.message : String(err);
		} finally {
			exposeBusy = false;
		}
	}

	function fullRender() {
		if (!ws || ws.readyState !== WebSocket.OPEN) {
			error = 'console is not connected, retrying automatically';
			scheduleReconnect();
			return;
		}
		ws.send('bluemap update');
		playerNote = 'Map update started. Watch the console for progress.';
	}

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
			pluginNote = err instanceof Error ? err.message : String(err);
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
		if (!pluginSearchDone) loadPlugins();
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
			vRunner = server.runner;
			vVersion = server.mc_version;
		}
		if (runners.length === 0) {
			runners = await api.get<Runner[]>('/api/v1/runners').catch(() => []);
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
		offline: 'bg-zinc-500/15 text-zinc-400 ring-zinc-500/30',
		crashed: 'bg-rose-500/15 text-rose-300 ring-rose-500/30',
		suspended: 'bg-rose-500/15 text-rose-300 ring-rose-500/30'
	};

	const memPct = $derived(
		stats && stats.memory_limit_mb > 0
			? Math.min(100, (stats.memory_used_mb / stats.memory_limit_mb) * 100)
			: 0
	);
	const cpuPct = $derived(stats ? Math.min(100, stats.cpu_percent) : 0);

	// activity: rolling five-minute chart series [cpu %, ram %, players % of
	// slot max]. Seeded from the server's history, then live WS frames push.
	const ACTIVITY_WINDOW = 5 * 60 * 1000;
	let activity = $state<{ t: number; values: number[] }[]>([]);
	let lastPlayersCount = $state(0);
	let lastPlayersMax = $state(0);

	function playersPct(count: number, max: number): number {
		return max > 0 ? (count / max) * 100 : Math.min(100, count * 5);
	}

	function seedActivity(s: Server) {
		activity = (s.history ?? []).map((h) => ({
			t: new Date(h.t).getTime(),
			values: [
				h.cpu_percent,
				h.memory_limit_mb > 0 ? (h.memory_used_mb / h.memory_limit_mb) * 100 : 0,
				playersPct(h.players, 20)
			]
		}));
	}

	function pushActivity(cpu: number, memUsed: number, memLimit: number) {
		const now = Date.now();
		activity = [
			...activity.filter((p) => now - p.t < ACTIVITY_WINDOW),
			{
				t: now,
				values: [
					Math.min(100, cpu),
					memLimit > 0 ? (memUsed / memLimit) * 100 : 0,
					playersPct(lastPlayersCount, lastPlayersMax)
				]
			}
		];
	}

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
		players = await api.get<typeof players>(`/api/v1/servers/${id}/players`).catch(() => null);
		seedActivity(server);
		// Flush the DOM so the console element exists, then create the
		// terminal *before* opening the socket — deterministic ordering.
		// All tab panels stay mounted (hidden via CSS), so the terminal is
		// created exactly once and survives tab switches.
		await tick();
		if (consoleEl && !term) await initTerminal(consoleEl);
		connectConsole();
	});

	async function initTerminal(el: HTMLDivElement) {
		const [{ Terminal }, { FitAddon }] = await Promise.all([
			import('@xterm/xterm'),
			import('@xterm/addon-fit')
		]);
		if (term) return; // panels stay mounted; the terminal is created once
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
			term.writeln('connected');
		} catch (e) {
			term.writeln('connected (history unavailable: ' + String(e).slice(0, 60) + ')');
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
				term?.writeln('connected');
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
				} else if (frame.type === 'players' && frame.players) {
					players = frame.players;
					lastPlayersCount = frame.players.online ?? 0;
					lastPlayersMax = frame.players.max ?? 0;
				} else if (frame.type === 'stats' && frame.stats) {
					stats = frame.stats;
					pushActivity(
						frame.stats.cpu_percent,
						frame.stats.memory_used_mb,
						frame.stats.memory_limit_mb
					);
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
			error = 'console is not connected, retrying automatically';
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
	<title>{server?.name ?? "Server"} - Quarry</title>
</svelte:head>

{#if server}
	<div class="mb-6 flex flex-wrap items-center gap-3">
		<a href="/" class="text-sm text-zinc-600 transition hover:text-zinc-300">Servers</a>
		<span class="text-zinc-700">/</span>
		<h1 class="text-xl font-semibold tracking-tight text-zinc-50">{server.name}</h1>
		<span class={`badge ring-1 ${statusStyle[server.status] ?? statusStyle.offline}`}>
			{#if server.status === 'running'}
				<span class="status-dot">
					<span class="absolute inset-0 rounded-full bg-emerald-400/40" style="animation: status-ping 2s cubic-bezier(0,0,0.2,1) infinite;"></span>
					<span class="relative h-2 w-2 rounded-full bg-emerald-400"></span>
				</span>
			{/if}
			{server.status}
		</span>
		{#if statusDetail}<span class="text-xs text-zinc-600">{statusDetail}</span>{/if}
		<span class="chip ml-auto">{server.ip}:{server.port}</span>
	</div>

	{#if error}
		<p class="mb-4 rounded-lg border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
			{error}
		</p>
	{/if}

	<!-- Content grid: tabs + panels left, live stats sidebar right -->
	<div class="grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(0,1fr)_17rem]">
		<div class="min-w-0">
			<!-- Power row -->
			<div class="card mb-4 flex flex-wrap items-center gap-2 p-3">
				<button
					class="btn btn-primary"
					disabled={!['offline', 'crashed'].includes(server.status)}
					onclick={() => power('start')}><i class="ph ph-play"></i>Start</button
				>
				<button
					class="btn btn-secondary"
					disabled={!['running', 'starting', 'stopping'].includes(server.status)}
					onclick={() => power('stop')}><i class="ph ph-stop"></i>Stop</button
				>
				<button
					class="btn btn-secondary"
					disabled={!['running', 'crashed'].includes(server.status)}
					onclick={() => power('restart')}><i class="ph ph-arrows-clockwise"></i>Restart</button
				>
				<button
					class="btn btn-danger"
					disabled={!['running', 'starting', 'stopping', 'crashed'].includes(server.status)}
					onclick={() => power('kill')}><i class="ph ph-x"></i>Kill</button
				>
				<span class="ml-auto flex items-center gap-2 text-xs text-zinc-500">
					<span
						class={`h-2 w-2 rounded-full ${wsState === 'live' ? 'animate-pulse bg-emerald-400' : wsState === 'connecting' ? 'animate-pulse bg-amber-400' : 'bg-rose-500'}`}
					></span>
					{wsState === 'live'
						? 'live'
						: wsState === 'connecting'
							? 'connecting…'
							: 'reconnecting…'}
				</span>
			</div>

			<div class="mb-4 flex items-center gap-3">
				<div class="tabs">
					<button class={`tab ${tab === 'console' ? 'tab-active' : ''}`} onclick={() => (tab = 'console')}>
						Console
						{#if players && players.online > 0}
							<span class="ml-1.5 rounded-full bg-emerald-500/15 px-1.5 text-[10px] font-semibold text-emerald-300">
								{players.online}
							</span>
						{/if}
					</button>
					<button class={`tab ${tab === 'players' ? 'tab-active' : ''}`} onclick={openPlayers}>
						Players
					</button>
					<button class={`tab ${tab === 'files' ? 'tab-active' : ''}`} onclick={() => (tab = 'files')}>
						Files
					</button>
					{#if hasMarket}
						<button class={`tab ${tab === 'plugins' ? 'tab-active' : ''}`} onclick={openPlugins}>
							Plugins
						</button>
					{/if}
					{#if server.map_url}
						<button class={`tab ${tab === 'map' ? 'tab-active' : ''}`} onclick={() => (tab = 'map')}>
							Map
						</button>
					{/if}
					<button class={`tab ${tab === 'settings' ? 'tab-active' : ''}`} onclick={openSettings}>
						Settings
					</button>
				</div>
			</div>

			<!-- Console panel: stays mounted so tab switches never clear it -->
			<div class={tab === 'console' ? '' : 'hidden'}>
				<div class="card overflow-hidden">
			<div class="flex items-center justify-between border-b border-white/5 bg-white/[0.02] px-4 py-2">
				<span class="text-[11px] font-semibold uppercase tracking-widest text-zinc-500">
					{server.name}: console
				</span>
				<button
					class="rounded px-2 py-0.5 text-[11px] text-zinc-500 transition hover:bg-white/5 hover:text-zinc-300"
					onclick={() => clearConsole()}
				>
					clear
				</button>
			</div>
			<div bind:this={consoleEl} class="h-[26rem] w-full bg-[#0b1120] p-2"></div>
			<form
				onsubmit={sendLine}
				class="flex items-center gap-2 border-t border-white/5 bg-white/[0.02] px-4 py-2.5"
			>
				<span class="font-mono text-sm {wsState === 'live' ? 'text-emerald-400' : 'text-zinc-600'}">$</span>
				<input
					class="flex-1 bg-transparent font-mono text-sm text-zinc-100 outline-none placeholder:text-zinc-600"
					placeholder={wsState === 'live'
						? 'type a server command, e.g. list or say hello'
						: 'console reconnecting…'}
					bind:value={inputLine}
				/>
				<button
					type="submit"
					disabled={wsState !== 'live' || !inputLine}
					class="rounded-md bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-300 transition hover:bg-zinc-700 disabled:cursor-not-allowed disabled:opacity-40"
				>
					Send ⏎
				</button>
			</form>
		</div>
	</div>

	<div class={tab === 'files' ? '' : 'hidden'}>
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<div class="card p-5">
				<div class="mb-2 flex items-center gap-2 text-sm">
					<button class="text-zinc-400 transition hover:text-zinc-200" onclick={() => loadFiles('')}>
						root
					</button>
					{#if filePath}
						<span class="font-mono text-zinc-300">/{filePath}</span>
					{/if}
				</div>
				<ul class="max-h-96 overflow-y-auto font-mono text-sm">
					{#each files as f (f.name)}
						<li class="group flex items-center gap-2 border-b border-zinc-800/50 py-1.5">
							{#if f.is_dir}
								<button
									class="text-sky-300 transition hover:underline"
									onclick={() => loadFiles(filePath ? `${filePath}/${f.name}` : f.name)}
								>
									{f.name}/
								</button>
							{:else}
								<button
									class="text-zinc-200 transition hover:text-emerald-300 hover:underline"
									onclick={() => openFile(f.name)}
								>
									{f.name}
								</button>
							{/if}
							<span class="ml-auto text-xs text-zinc-600">{f.size} B</span>
							<button
								class="text-xs text-rose-400/70 opacity-0 transition group-hover:opacity-100 hover:text-rose-300"
								onclick={() => deleteFile(f.name, f.is_dir)}>del</button
							>
						</li>
					{:else}
						<li class="py-1.5 text-zinc-500">empty directory</li>
					{/each}
				</ul>
			</div>
			<div class="card p-5">
				{#if fileEditing}
					<div class="mb-2 flex items-center gap-2 text-sm">
						<span class="font-mono text-zinc-300">{fileEditing}</span>
						<button class="btn btn-primary ml-auto px-3 py-1.5 text-xs" onclick={saveFile}>Save</button>
						<button
							class="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-500"
							onclick={() => (fileEditing = '')}>Close</button
						>
					</div>
					<textarea
						class="h-80 w-full rounded-lg border border-zinc-700/80 bg-zinc-950 p-3 font-mono text-xs text-zinc-100 outline-none focus:border-emerald-500"
						bind:value={fileContent}
						spellcheck="false"
					></textarea>
				{:else}
					<p class="text-sm text-zinc-500">Select a file to view or edit it.</p>
				{/if}
			</div>
		</div>
	</div>

	<div class={tab === 'players' ? '' : 'hidden'}>
		<div class="space-y-4">
			{#if playerNote}
				<p class="rounded-md border border-sky-400/20 bg-sky-400/5 px-3 py-2 text-sm text-sky-200">
					{playerNote}
				</p>
			{/if}
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<!-- Online players -->
				<div class="card p-5 lg:col-span-2">
					<div class="mb-3 flex items-center justify-between">
						<h3 class="text-sm font-medium text-zinc-300">Online</h3>
						<span class="mono text-xs text-zinc-500">
							{players ? `${players.online} / ${players.max || '?'}` : 'waiting for first poll'}
						</span>
					</div>
					{#if server.status !== 'running'}
						<p class="py-6 text-center text-sm text-zinc-500">
							The server is {server.status}. Players appear here once it is running.
						</p>
					{:else if !players}
						<p class="py-6 text-center text-sm text-zinc-500">Waiting for the next player poll…</p>
					{:else if players.names.length === 0}
						<p class="py-6 text-center text-sm text-zinc-500">Nobody is online right now.</p>
					{:else}
						<ul class="divide-y divide-zinc-800/50">
							{#each players.names as name (name)}
								<li class="py-1">
									<div class="group flex items-center gap-3 rounded-md px-1 py-1.5">
										<!-- skin head: browser-fetched from mc-heads.net, offline servers fall back to Steve -->
										<img
											src={`https://mc-heads.net/avatar/${name}/32`}
											alt=""
											loading="lazy"
											referrerpolicy="no-referrer"
											class="h-7 w-7 rounded-md border border-zinc-800 bg-zinc-900 [image-rendering:pixelated]"
										/>
										<span class="font-mono text-sm text-zinc-200">{name}</span>
										<span class="ml-auto flex items-center gap-1 opacity-0 transition group-hover:opacity-100">
											<button
												class="rounded-md px-2 py-1 text-xs text-zinc-400 transition hover:bg-zinc-800 hover:text-zinc-100"
												disabled={busyPlayer === 'kick' + name}
												onclick={() => playerAction('kick', name)}
												title="Kick from the server"
											>
												<i class="ph ph-boot"></i> Kick
											</button>
											<button
												class="rounded-md px-2 py-1 text-xs text-zinc-400 transition hover:bg-zinc-800 hover:text-amber-300"
												disabled={busyPlayer === 'op' + name}
												onclick={() => playerAction('op', name)}
												title="Grant operator"
											>
												<i class="ph ph-shield-star"></i> Op
											</button>
											<button
												class="rounded-md px-2 py-1 text-xs text-zinc-400 transition hover:bg-zinc-800 hover:text-rose-300"
												onclick={() => (banFor = banFor === name ? '' : name)}
												title="Ban player"
											>
												<i class="ph ph-prohibit"></i> Ban
											</button>
										</span>
									</div>
									{#if banFor === name}
										<form
											class="mx-1 mb-2 flex items-center gap-2 rounded-lg border border-rose-500/25 bg-rose-500/[0.06] px-3 py-2"
											onsubmit={(e) => {
												e.preventDefault();
												playerAction('ban', name);
											}}
										>
											<input
												class="input flex-1 py-1.5 text-sm"
												placeholder={`Reason for banning ${name} (optional)`}
												bind:value={banReason}
											/>
											<button
												type="submit"
												disabled={busyPlayer === 'ban' + name}
												class="btn btn-danger px-3 py-1.5 text-xs">Ban {name}</button
											>
											<button
												type="button"
												class="px-2 py-1.5 text-xs text-zinc-500 transition hover:text-zinc-300"
												onclick={() => (banFor = '')}>Cancel</button
											>
										</form>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
					<p class="mt-3 border-t border-zinc-800/60 pt-3 text-xs text-zinc-600">
						The list refreshes every 10 seconds while this page is open.
					</p>
				</div>

				<!-- Bans -->
				<div class="space-y-4">
					<div class="card p-5">
						<h3 class="mb-3 text-sm font-medium text-zinc-300">Ban an IP address</h3>
						<form
							class="flex items-center gap-2"
							onsubmit={(e) => {
								e.preventDefault();
								playerAction('ban_ip', '', banIpAddr);
							}}
						>
							<input
								class="input flex-1 font-mono text-sm"
								placeholder="203.0.113.7"
								bind:value={banIpAddr}
								required
							/>
							<input
								class="input w-32 text-sm"
								placeholder="reason"
								bind:value={banIpReason}
							/>
							<button
								type="submit"
								disabled={busyPlayer === 'ban_ip' + banIpAddr}
								class="btn btn-danger px-3 py-1.5 text-xs">Ban</button
							>
						</form>
					</div>
					<div class="card p-5">
						<h3 class="mb-3 flex items-center justify-between text-sm font-medium text-zinc-300">
							Bans
							<span class="mono text-xs text-zinc-500">{bans.length}</span>
						</h3>
						<ul class="space-y-1">
							{#each bans as b (b.id)}
								<li class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-zinc-800/50">
									<i class="ph {b.name ? 'ph-prohibit' : 'ph-globe-hemisphere-west'} text-zinc-600"></i>
									<span class="truncate font-mono text-xs text-zinc-300">{b.name || b.ip}</span>
									{#if b.reason}
										<span class="truncate text-xs text-zinc-600" title={b.reason}>{b.reason}</span>
									{/if}
									<span class="mono ml-auto shrink-0 text-[10px] text-zinc-600">{b.created_at}</span>
									<button
										class="shrink-0 text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-emerald-300"
										onclick={() => unban(b.id)}
										title="Lift ban"
									>
										<i class="ph ph-lock-open text-sm"></i>
									</button>
								</li>
							{:else}
								<li class="px-2 py-1.5 text-xs text-zinc-600">No bans issued from the panel.</li>
							{/each}
						</ul>
						<p class="mt-3 border-t border-zinc-800/60 pt-3 text-xs text-zinc-600">
							Bans issued in-game do not appear here; the server's own ban list stays authoritative.
						</p>
					</div>

					<div class="card p-5">
						<h3 class="mb-3 flex items-center justify-between text-sm font-medium text-zinc-300">
							Whitelist
							<button
								class={`rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ring-1 transition ${whitelist.enabled ? 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30' : 'bg-zinc-500/15 text-zinc-400 ring-zinc-500/30'}`}
								disabled={busyWhitelist}
								onclick={whitelistToggle}
								title={whitelist.enabled ? 'Turn whitelist off' : 'Turn whitelist on'}
							>
								{whitelist.enabled ? 'on' : 'off'}
							</button>
						</h3>
						<form class="mb-3 flex items-center gap-2" onsubmit={whitelistAdd}>
							<input
								class="input flex-1 py-1.5 font-mono text-sm"
								placeholder="player name"
								bind:value={whitelistAddName}
								required
							/>
							<button type="submit" disabled={busyWhitelist} class="btn btn-secondary px-3 py-1.5 text-xs">
								Add
							</button>
						</form>
						<ul class="space-y-1">
							{#each whitelist.names as name (name)}
								<li class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-zinc-800/50">
									<i class="ph ph-list-checks text-zinc-600"></i>
									<span class="truncate font-mono text-xs text-zinc-300">{name}</span>
									<button
										class="ml-auto text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-rose-300"
										onclick={() => whitelistRemove(name)}
										title="Remove from whitelist"
									>
										<i class="ph ph-trash text-sm"></i>
									</button>
								</li>
							{:else}
								<li class="px-2 py-1.5 text-xs text-zinc-600">
									Nobody whitelisted. With the whitelist on, only listed players can join.
								</li>
							{/each}
						</ul>
						<p class="mt-3 border-t border-zinc-800/60 pt-3 text-xs text-zinc-600">
							Whitelist state is read from the server; names apply immediately.
						</p>
					</div>
				</div>
			</div>
		</div>
	</div>

	<div class={tab === 'map' ? '' : 'hidden'}>
		<div class="space-y-3">
			<div class="flex flex-wrap items-center gap-3">
				<span class="chip font-mono">{server.map_url}</span>
				<a href={server.map_url} target="_blank" rel="noreferrer" class="btn btn-secondary px-3 py-1.5 text-xs">
					<i class="ph ph-arrow-square-out"></i> Open map in new tab
				</a>
				<button class="btn btn-secondary px-3 py-1.5 text-xs" onclick={fullRender}>
					<i class="ph ph-cube-transparent"></i> Update map
				</button>
				<span class="text-xs text-zinc-600">
					The map renders as the world is explored; "Update map" processes everything changed.
				</span>
			</div>
			<div class="card overflow-hidden">
				<iframe
					src={server.map_url}
					title="Server map"
					class="h-[32rem] w-full border-0 bg-[#0b1120]"
				></iframe>
			</div>
		</div>
	</div>

	<div class={tab === 'plugins' ? '' : 'hidden'}>
		<div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
			<!-- Explorer: search + results -->
			<div class="space-y-4 lg:col-span-2">
				<form onsubmit={searchPlugins} class="flex gap-2">
					<div class="relative flex-1">
						<i class="ph ph-magnifying-glass pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600"></i>
						<input
							class="input pl-9"
							placeholder={`Search Modrinth for ${server.mc_version} plugins`}
							bind:value={pluginQuery}
						/>
					</div>
					<button type="submit" disabled={pluginSearchBusy} class="btn btn-primary">
						{pluginSearchBusy ? 'Searching' : 'Search'}
					</button>
				</form>

				{#if pluginNote}
					<p class="rounded-md border border-emerald-400/20 bg-emerald-400/5 px-3 py-2 text-sm text-emerald-300">
						{pluginNote}
					</p>
				{/if}

				{#if pluginSearchBusy}
					<div class="space-y-3">
						{#each Array(4) as _, i (i)}
							<div class="card flex animate-pulse gap-4 p-4">
								<div class="h-12 w-12 shrink-0 rounded-lg bg-zinc-800"></div>
								<div class="flex-1 space-y-2 py-1">
									<div class="h-3 w-1/3 rounded bg-zinc-800"></div>
									<div class="h-3 w-2/3 rounded bg-zinc-800/60"></div>
								</div>
							</div>
						{/each}
					</div>
				{:else if pluginSearchDone}
					{#if pluginResults.length}
						<p class="text-xs text-zinc-600">
							{pluginResults.length} results for {server.mc_version} ({server.runner})
						</p>
					{/if}
					<div class="grid grid-cols-1 gap-3">
						{#each pluginResults as hit (hit.project_id)}
							<div class="card flex gap-4 p-4">
								{#if hit.icon_url}
									<img
										src={hit.icon_url}
										alt=""
										class="h-12 w-12 shrink-0 rounded-lg border border-zinc-800 object-cover"
									/>
								{:else}
									<div
										class="flex h-12 w-12 shrink-0 items-center justify-center rounded-lg border border-zinc-800 bg-zinc-900 text-lg font-semibold text-zinc-500"
									>
										{hit.title.charAt(0).toUpperCase()}
									</div>
								{/if}
								<div class="min-w-0 flex-1">
									<div class="flex items-baseline gap-2">
										<p class="truncate text-sm font-medium text-zinc-100">{hit.title}</p>
										<span class="shrink-0 text-xs text-zinc-600">{hit.author}</span>
									</div>
									<p class="mt-0.5 line-clamp-2 text-sm leading-snug text-zinc-500">
										{hit.description}
									</p>
									<p class="mono mt-1.5 text-xs text-zinc-600">
										<i class="ph ph-download-simple"></i>
										{hit.downloads.toLocaleString()}
									</p>
								</div>
								<div class="flex shrink-0 flex-col items-end justify-center">
									<button
										class="btn btn-primary px-3 py-1.5 text-xs"
										disabled={installingId === hit.project_id}
										onclick={() => installPlugin(hit.project_id)}
									>
										{installingId === hit.project_id ? 'Installing' : 'Install'}
									</button>
									<a
										href="https://modrinth.com/plugin/{hit.slug}"
										target="_blank"
										rel="noreferrer"
										class="mt-1.5 text-[11px] text-zinc-600 transition hover:text-zinc-400"
									>
										Modrinth page
									</a>
								</div>
							</div>
						{:else}
							<div class="card p-8 text-center">
								<p class="text-sm text-zinc-400">
									{pluginQuery ? `No results for "${pluginQuery}".` : 'Type above to search Modrinth.'}
								</p>
								<p class="mt-1 text-xs text-zinc-600">
									Results are filtered to plugins that support {server.mc_version} on {server.runner}.
								</p>
							</div>
						{/each}
					</div>
				{:else}
					<div class="card p-8 text-center">
						<i class="ph ph-squares-four mb-3 block text-2xl text-zinc-700"></i>
						<p class="text-sm text-zinc-400">Browse and install plugins for this server.</p>
						<p class="mt-1 text-xs text-zinc-600">
							Search Modrinth. Results are filtered to {server.mc_version} on {server.runner}, installs
							land in the plugins directory.
						</p>
					</div>
				{/if}
			</div>

			<!-- Installed -->
			<div>
				<div class="card p-4">
					<h3 class="mb-3 text-sm font-medium text-zinc-300">
						Installed
						<span class="ml-1 text-zinc-600">{plugins.length}</span>
					</h3>
					<ul class="space-y-1">
						{#each plugins as p (p.name)}
							<li class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-zinc-800/50">
								<i class="ph ph-cube text-zinc-600"></i>
								<span class="truncate font-mono text-xs text-zinc-300">{p.name}</span>
								<button
									class="ml-auto text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-rose-300"
									onclick={() => deletePlugin(p.name)}
									title="Remove"
								>
									<i class="ph ph-trash text-sm"></i>
								</button>
							</li>
						{:else}
							<li class="px-2 py-1.5 text-xs text-zinc-600">
								Nothing installed yet.
							</li>
						{/each}
					</ul>
					<p class="mt-3 border-t border-zinc-800/60 pt-3 text-xs text-zinc-600">
						Plugins load at server start. Restart to apply changes.
					</p>
				</div>
			</div>
		</div>
	</div>

	<div class={tab === 'settings' ? '' : 'hidden'}>
		<div class="space-y-6">
			{#if settingsNotes.length}
				<p class="rounded-lg border border-emerald-900/50 bg-emerald-950/30 px-3 py-2 text-sm text-emerald-300">
					{settingsNotes.join(' · ')}
				</p>
			{/if}

			<form onsubmit={saveSettings} class="card p-6">
				<h3 class="mb-4 text-xs font-bold uppercase tracking-widest text-zinc-500">General</h3>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-zinc-400">Name</span>
						<input
							class="w-full rounded-lg border border-zinc-700/80 bg-zinc-950 px-3 py-2 text-zinc-100 outline-none focus:border-emerald-500"
							bind:value={settings.name}
							required
						/>
					</label>
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-zinc-400">Memory (MiB)</span>
						<input
							type="number"
							min="512"
							step="512"
							class="input"
							bind:value={settings.memory_mb}
						/>
					</label>
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-zinc-400">CPU (100 = one core)</span>
						<input
							type="number"
							min="50"
							max="3200"
							step="50"
							class="input"
							bind:value={settings.cpu_percent}
						/>
					</label>
				</div>
				<div class="mt-4 flex items-center gap-4">
					<button type="submit" disabled={settingsBusy} class="btn btn-primary">
						{settingsBusy ? 'Saving…' : 'Save changes'}
					</button>
					<p class="text-xs text-zinc-600">Memory and CPU changes apply on the next start.</p>
				</div>
			</form>

			<div class="card p-6">
				<h3 class="mb-1 text-xs font-bold uppercase tracking-widest text-zinc-500">Network</h3>
				<p class="mb-3 text-xs text-zinc-600">
					Current address: <span class="font-mono text-zinc-400">{server.ip}:{server.port}</span>.
					Move the server to any free allocation (stop the server first).
				</p>
				<ul class="grid grid-cols-1 gap-2 md:grid-cols-3">
					{#each allocs as a (a.id)}
						<div
							class={`flex items-center gap-2 rounded-lg border px-3 py-2 font-mono text-sm ${a.is_current ? 'border-emerald-700/60 bg-emerald-950/20 text-emerald-300' : a.in_use_by ? 'border-zinc-800 bg-zinc-950/60 text-zinc-600' : 'border-zinc-800 bg-zinc-950 text-zinc-300'}`}
						>
							<span>{a.ip}:{a.port}</span>
							{#if a.is_current}
								<span class="ml-auto text-[10px] uppercase tracking-wide">current</span>
							{:else if a.in_use_by}
								<span class="ml-auto text-[10px] uppercase tracking-wide">in use</span>
							{:else}
								<button
									class="ml-auto rounded bg-zinc-800 px-2 py-0.5 text-[11px] text-zinc-200 transition hover:bg-zinc-700"
									onclick={() => movePort(a.id, a.ip, a.port)}>move here</button
								>
							{/if}
						</div>
					{:else}
						<p class="text-sm text-zinc-500">
							No allocations on this node — add a range in the Admin area.
						</p>
					{/each}
				</ul>
			</div>

			<div class="card p-6">
				<h3 class="mb-1 text-xs font-bold uppercase tracking-widest text-zinc-500">Version</h3>
				<p class="mb-4 text-xs text-zinc-600">
					Switch the server software or Minecraft version. Worlds, configs and ports are kept;
					the server jar is replaced and the server must be started again.
				</p>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-zinc-400">Server software</span>
						<select
							class="select"
							bind:value={vRunner}
							onchange={() => {
								const r = runners.find((x) => x.id === vRunner);
								vVersion = r?.versions.find((v) => v.stable)?.mc_version ?? '';
							}}
						>
							{#each runners as r (r.id)}
								<option value={r.id}>{r.id}</option>
							{/each}
						</select>
					</label>
					<label class="block">
						<span class="mb-1.5 block text-sm font-medium text-zinc-400">Minecraft version</span>
						<select class="select" bind:value={vVersion}>
							{#each runners.find((r) => r.id === vRunner)?.versions.filter((v) => v.stable).slice(0, 40) ?? [] as v (v.mc_version)}
								<option value={v.mc_version}>{v.mc_version} (Java {v.java_version})</option>
							{/each}
						</select>
					</label>
					<div class="flex items-end">
						<button
							class="btn btn-secondary"
							disabled={switchBusy || (vRunner === server.runner && vVersion === server.mc_version)}
							onclick={switchVersion}
						>
							{switchBusy ? 'Switching…' : 'Switch version'}
						</button>
					</div>
				</div>
			</div>

			<div class="rounded-xl border border-rose-500/20 bg-rose-500/[0.04] p-6">
				<h3 class="mb-1 text-xs font-bold uppercase tracking-widest text-rose-400">Danger zone</h3>
				<p class="mb-3 text-xs text-zinc-500">
					Deleting removes the server and wipes its worlds and files from the node.
				</p>
				<button
					class="btn btn-danger"
					disabled={['running', 'starting', 'stopping'].includes(server.status)}
					onclick={deleteServer}
				>
					Delete server
				</button>
			</div>
		</div>
	</div>
	</div>

		<!-- Live stats sidebar -->
		<aside class="space-y-3">
			<div class="card p-4">
				<h3 class="mb-3 text-[11px] font-bold uppercase tracking-widest text-zinc-500">Live</h3>
				<div class="space-y-4">
					<div>
						<div class="flex items-center justify-between text-xs text-zinc-500">
							<span class="flex items-center gap-1.5">
								<span class="h-1 w-2.5 rounded-full bg-emerald-400"></span> CPU
							</span>
							<span class="mono text-zinc-300">{stats ? stats.cpu_percent.toFixed(1) : '0.0'}%</span>
						</div>
						<div class="mt-1.5">
							<TimeChart
								points={activity.map((p) => ({ t: p.t, values: [p.values[0]] }))}
								series={[{ color: '#34d399' }]}
								height={34}
							/>
						</div>
					</div>
					<div>
						<div class="flex items-center justify-between text-xs text-zinc-500">
							<span class="flex items-center gap-1.5">
								<span class="h-1 w-2.5 rounded-full bg-sky-400"></span> RAM
							</span>
							<span class="mono text-zinc-300">
								{stats ? stats.memory_used_mb.toFixed(0) : '0'} / {stats ? stats.memory_limit_mb.toFixed(0) : '0'} MiB
							</span>
						</div>
						<div class="mt-1.5">
							<TimeChart
								points={activity.map((p) => ({ t: p.t, values: [p.values[1]] }))}
								series={[{ color: '#38bdf8' }]}
								height={34}
							/>
						</div>
					</div>
					<div>
						<div class="flex items-center justify-between text-xs text-zinc-500">
							<span class="flex items-center gap-1.5">
								<span class="h-1 w-2.5 rounded-full bg-violet-400"></span> Players
							</span>
							<span class="mono text-zinc-300">
								{players ? players.online : 0}{players && players.max ? ` / ${players.max}` : ''}
							</span>
						</div>
						<div class="mt-1.5">
							<TimeChart
								points={activity.map((p) => ({ t: p.t, values: [p.values[2]] }))}
								series={[{ color: '#a78bfa' }]}
								height={34}
							/>
						</div>
					</div>
				</div>
				<p class="mt-3 border-t border-zinc-800/60 pt-2.5 text-[10px] text-zinc-600">last 5 minutes</p>
			</div>

			{#if server.map_url}
				<div class="card p-4">
					<h3 class="mb-2 text-[11px] font-bold uppercase tracking-widest text-zinc-500">Map</h3>
					<a
						href={server.map_url}
						target="_blank"
						rel="noreferrer"
						class="text-xs text-sky-300 transition hover:text-sky-200"
					>
						<i class="ph ph-map-trifold"></i> Open the live map
					</a>
				</div>
			{/if}

			<div class="card p-4">
				<h3 class="mb-2 text-[11px] font-bold uppercase tracking-widest text-zinc-500">Public access</h3>
				{#if server.expose?.active}
					{#if server.expose.address}
						<p class="font-mono text-xs break-all text-emerald-300">{server.expose.address}</p>
						<p class="mt-1.5 text-[10px] text-zinc-600">Players join via this playit.gg address from anywhere.</p>
					{:else}
						<p class="text-xs text-emerald-300"><i class="ph ph-check-circle"></i> Tunnel active</p>
						<p class="mt-1.5 text-[10px] text-zinc-600">
							Find your tunnel's address in the playit dashboard and save it here.
						</p>
					{/if}
					<form class="mt-2 flex items-center gap-1.5" onsubmit={saveExposeAddress}>
						<input
							class="input flex-1 py-1 text-xs"
							placeholder="my-tunnel.playit.gg"
							bind:value={exposeAddrInput}
						/>
						<button type="submit" disabled={exposeBusy} class="btn btn-secondary px-2.5 py-1 text-xs">Save</button>
					</form>
					<button
						class="mt-2 text-[11px] text-zinc-600 transition hover:text-rose-300"
						disabled={exposeBusy}
						onclick={stopExpose}
					>
						<i class="ph ph-plug"></i> Stop tunnel
					</button>
				{:else if server.expose?.pending && server.expose.claim_url}
					<a
						href={server.expose.claim_url}
						target="_blank"
						rel="noreferrer"
						class="text-xs break-all text-amber-300 transition hover:text-amber-200"
					>
						<i class="ph ph-link-simple-break"></i> Claim the tunnel
					</a>
					<p class="mt-1.5 text-[10px] text-zinc-600">
						Open the link, sign in to playit.gg, add a TCP tunnel pointing at
						<span class="font-mono text-zinc-500">127.0.0.1:{server.port}</span>, then come back and check.
					</p>
					<button
						class="btn btn-secondary mt-2 w-full px-3 py-1.5 text-xs"
						disabled={exposeBusy}
						onclick={checkClaim}
					>
						{exposeBusy ? 'Checking…' : 'I claimed it — check'}
					</button>
				{:else}
					<button class="btn btn-secondary w-full px-3 py-1.5 text-xs" disabled={exposeBusy} onclick={startExpose}>
						<i class="ph ph-globe-hemisphere-west"></i> {exposeBusy ? 'Setting up…' : 'Expose via playit.gg'}
					</button>
					<p class="mt-1.5 text-[10px] text-zinc-600">
						Free public tunnel on any Minecraft version — no port forwarding, works on every runner.
					</p>
				{/if}
				{#if exposeNote}
					<p class="mt-2 text-[10px] text-zinc-500">{exposeNote}</p>
				{/if}
			</div>
		</aside>
	</div>
{/if}

