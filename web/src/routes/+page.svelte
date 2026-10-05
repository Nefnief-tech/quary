<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Runner, type Server } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import TimeChart from '$lib/components/TimeChart.svelte';

	let servers = $state<Server[]>([]);
	let runners = $state<Runner[]>([]);
	let error = $state('');
	let creating = $state(false);
	let showCreate = $state(false);

	let name = $state('');
	let runnerId = $state('paper');
	let mcVersion = $state('');
	let memoryMb = $state(2048);

	const statusStyle: Record<string, string> = {
		running: 'bg-emerald-400/10 text-emerald-400 ring-emerald-400/20',
		starting: 'bg-amber-400/10 text-amber-300 ring-amber-400/20',
		stopping: 'bg-amber-400/10 text-amber-300 ring-amber-400/20',
		installing: 'bg-sky-400/10 text-sky-300 ring-sky-400/20',
		offline: 'bg-zinc-500/10 text-zinc-400 ring-zinc-500/20',
		crashed: 'bg-rose-400/10 text-rose-300 ring-rose-400/20',
		suspended: 'bg-rose-400/10 text-rose-300 ring-rose-400/20'
	};

	async function refresh() {
		try {
			servers = await api.get<Server[]>('/api/v1/servers');
		} catch (err) {
			const status = (err as { status?: number }).status;
			if (status === 401) {
				goto('/login');
				return;
			}
			error = String(err);
		}
	}

	let refreshTimer: ReturnType<typeof setInterval> | undefined;

	onMount(async () => {
		await auth.fetch();
		if (!auth.user) {
			goto('/login');
			return;
		}
		await refresh();
		refreshTimer = setInterval(refresh, 5000);
		runners = await api.get<Runner[]>('/api/v1/runners').catch(() => []);
		const preferred = runners.find((r) => r.id === runnerId) ?? runners[0];
		if (preferred) {
			runnerId = preferred.id;
			mcVersion = preferred.versions.find((v) => v.stable)?.mc_version ?? '';
		}
	});

	onDestroy(() => clearInterval(refreshTimer));

	const selectedRunner = $derived(runners.find((r) => r.id === runnerId));
	const runningCount = $derived(servers.filter((s) => s.status === 'running').length);

	// chart points per server: CPU % and RAM % over the last five minutes
	function chartPoints(s: Server) {
		const hist = s.history ?? [];
		return hist.map((h) => ({
			t: new Date(h.t).getTime(),
			values: [
				h.cpu_percent,
				h.memory_limit_mb > 0 ? (h.memory_used_mb / h.memory_limit_mb) * 100 : 0
			]
		}));
	}
	function lastPct(s: Server): { cpu: number; ram: number } {
		const hist = s.history ?? [];
		const h = hist[hist.length - 1];
		if (!h) return { cpu: 0, ram: 0 };
		return {
			cpu: h.cpu_percent,
			ram: h.memory_limit_mb > 0 ? (h.memory_used_mb / h.memory_limit_mb) * 100 : 0
		};
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		creating = true;
		try {
			await api.post('/api/v1/servers', {
				name,
				node_id: '',
				runner: runnerId,
				mc_version: mcVersion,
				memory_mb: memoryMb
			});
			showCreate = false;
			name = '';
			await refresh();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head>
	<title>Your servers - Quarry</title>
</svelte:head>

<div class="mb-8 flex flex-wrap items-center justify-between gap-4">
	<div>
		<h1 class="text-xl font-semibold tracking-tight text-zinc-50">Servers</h1>
		<p class="mt-1 text-sm text-zinc-500">
			{servers.length} total, {runningCount} running
		</p>
	</div>
	<button class="btn btn-primary" onclick={() => (showCreate = !showCreate)}>
		{showCreate ? 'Cancel' : 'New server'}
	</button>
</div>

{#if showCreate}
	<form onsubmit={create} class="card mb-8 space-y-5 p-5">
		<div class="grid grid-cols-1 gap-4 md:grid-cols-4">
			<label class="block">
				<span class="label">Name</span>
				<input class="input" bind:value={name} placeholder="my-server" required />
			</label>
			<label class="block">
				<span class="label">Server software</span>
				<select
					class="select"
					bind:value={runnerId}
					onchange={() => {
						const r = runners.find((x) => x.id === runnerId);
						mcVersion = r?.versions.find((v) => v.stable)?.mc_version ?? '';
					}}
				>
					{#each runners as r (r.id)}
						<option value={r.id}>{r.id}</option>
					{/each}
				</select>
			</label>
			<label class="block">
				<span class="label">Minecraft version</span>
				<select class="select" bind:value={mcVersion}>
					{#each selectedRunner?.versions.filter((v) => v.stable).slice(0, 40) ?? [] as v (v.mc_version)}
						<option value={v.mc_version}>{v.mc_version} (Java {v.java_version})</option>
					{/each}
				</select>
			</label>
			<label class="block">
				<span class="label">Memory (MiB)</span>
				<input type="number" min="512" step="512" class="input" bind:value={memoryMb} />
			</label>
		</div>
		{#if error}
			<p class="rounded-md border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
				{error}
			</p>
		{/if}
		<div class="flex items-center gap-4">
			<button type="submit" disabled={creating || !mcVersion} class="btn btn-primary">
				{creating ? 'Creating…' : 'Create and install'}
			</button>
			<p class="text-xs text-zinc-600">
				Creating the server accepts the Minecraft EULA. Installation runs in the background.
			</p>
		</div>
	</form>
{/if}

<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
	{#each servers as server (server.id)}
		<a href="/servers/{server.id}" class="card card-hover group block p-4">
			<div class="flex items-center gap-3">
				<img src="/favicon.svg" alt="" class="h-9 w-9 rounded-lg border border-zinc-800" />
				<div class="min-w-0 flex-1">
					<p class="truncate text-sm font-medium text-zinc-100">{server.name}</p>
					<p class="mono text-xs text-zinc-500">{server.runner} · {server.mc_version}</p>
				</div>
				<span class={`badge ring-1 ${statusStyle[server.status] ?? statusStyle.offline}`}>
					{#if server.status === 'running'}
						<span class="status-dot">
							<span class="absolute inset-0 rounded-full bg-emerald-400/40" style="animation: status-ping 2s cubic-bezier(0,0,0.2,1) infinite;"></span>
							<span class="relative h-2 w-2 rounded-full bg-emerald-400"></span>
						</span>
					{/if}
					{server.status}
				</span>
			</div>
			{#if (server.history?.length ?? 0) >= 2}
				{@const pct = lastPct(server)}
				<div class="mt-3">
					<div class="mb-1 flex items-center gap-3 text-[10px] uppercase tracking-wider text-zinc-600">
						<span class="flex items-center gap-1">
							<span class="h-1 w-2.5 rounded-full bg-emerald-400"></span>
							cpu <span class="mono normal-case text-zinc-400">{pct.cpu.toFixed(0)}%</span>
						</span>
						<span class="flex items-center gap-1">
							<span class="h-1 w-2.5 rounded-full bg-sky-400"></span>
							ram <span class="mono normal-case text-zinc-400">{pct.ram.toFixed(0)}%</span>
						</span>
						<span class="ml-auto normal-case">last 5 min</span>
					</div>
					<TimeChart
						points={chartPoints(server)}
						series={[
							{ color: '#34d399' },
							{ color: '#38bdf8' }
						]}
						height={36}
					/>
				</div>
			{/if}
			<div class="mt-3 flex items-center gap-2">
				<span class="chip">{server.ip}:{server.port}</span>
				<span class="chip">{server.memory_mb} MiB</span>
				<span class="ml-auto text-xs text-zinc-600 transition group-hover:text-zinc-300">Open</span>
			</div>
		</a>
	{:else}
		<div class="card p-10 text-center">
			<p class="text-sm text-zinc-400">No servers yet.</p>
			<p class="mt-1 text-sm text-zinc-600">
				Create one above: pick a runner and a Minecraft version to start.
			</p>
		</div>
	{/each}
</div>
