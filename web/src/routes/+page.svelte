<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Runner, type Server } from '$lib/api';
	import { auth } from '$lib/auth.svelte';

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
		running: 'bg-emerald-400/10 text-emerald-300 ring-emerald-400/25',
		starting: 'bg-amber-400/10 text-amber-300 ring-amber-400/25',
		stopping: 'bg-amber-400/10 text-amber-300 ring-amber-400/25',
		installing: 'bg-sky-400/10 text-sky-300 ring-sky-400/25',
		offline: 'bg-slate-400/10 text-slate-400 ring-slate-400/20',
		crashed: 'bg-rose-400/10 text-rose-300 ring-rose-400/25',
		suspended: 'bg-rose-400/10 text-rose-300 ring-rose-400/25'
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
	<title>Your servers — Quarry</title>
</svelte:head>

<div class="mb-8 flex flex-wrap items-end justify-between gap-4">
	<div>
		<h1 class="text-3xl font-bold tracking-tight text-white">Servers</h1>
		<p class="mt-1.5 text-sm text-slate-500">
			{servers.length} total · <span class="text-emerald-400">{runningCount} running</span>
		</p>
	</div>
	<button class="btn btn-primary" onclick={() => (showCreate = !showCreate)}>
		{showCreate ? 'Cancel' : '+ New server'}
	</button>
</div>

{#if showCreate}
	<form onsubmit={create} class="card mb-8 space-y-5 p-6">
		<div class="grid grid-cols-1 gap-5 md:grid-cols-4">
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
						<option value={v.mc_version}>{v.mc_version} — Java {v.java_version}</option>
					{/each}
				</select>
			</label>
			<label class="block">
				<span class="label">Memory (MiB)</span>
				<input type="number" min="512" step="512" class="input" bind:value={memoryMb} />
			</label>
		</div>
		{#if error}
			<p class="rounded-lg border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
				{error}
			</p>
		{/if}
		<div class="flex items-center gap-4">
			<button type="submit" disabled={creating || !mcVersion} class="btn btn-primary">
				{creating ? 'Creating…' : 'Create & install'}
			</button>
			<p class="text-xs text-slate-600">
				Accepts the Minecraft EULA on your behalf. The jar downloads in the background.
			</p>
		</div>
	</form>
{/if}

<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
	{#each servers as server (server.id)}
		<a
			href="/servers/{server.id}"
			class="card card-hover group block p-5"
		>
			<div class="flex items-center gap-3.5">
				<img
					src="/favicon.svg"
					alt=""
					class="h-10 w-10 rounded-xl ring-1 ring-white/10 transition group-hover:ring-emerald-400/40"
				/>
				<div class="min-w-0">
					<p class="truncate font-semibold text-slate-100">{server.name}</p>
					<p class="text-xs text-slate-500">{server.runner} · {server.mc_version}</p>
				</div>
				<span class={`badge ml-auto ring-1 ${statusStyle[server.status] ?? statusStyle.offline}`}>
					{#if server.status === 'running'}
						<span class="status-dot"><span class="absolute inset-0 rounded-full bg-emerald-400/50" style="animation: status-ping 1.8s cubic-bezier(0,0,0.2,1) infinite;"></span><span class="relative h-2 w-2 rounded-full bg-emerald-400"></span></span>
					{:else}
						<span class="h-2 w-2 rounded-full bg-current opacity-70"></span>
					{/if}
					{server.status}
				</span>
			</div>
			<div class="mt-5 flex items-center gap-2 text-xs">
				<span class="chip">{server.ip}:{server.port}</span>
				<span class="chip">{server.memory_mb} MiB</span>
				<span class="ml-auto text-slate-600 transition group-hover:translate-x-0.5 group-hover:text-emerald-400">
					Open →
				</span>
			</div>
		</a>
	{:else}
		<div class="rounded-2xl border border-dashed border-slate-700/50 p-16 text-center">
			<p class="text-slate-400">No servers yet.</p>
			<p class="mt-1.5 text-sm text-slate-600">
				Create one above — pick a runner, a version, and you're mining.
			</p>
		</div>
	{/each}
</div>
