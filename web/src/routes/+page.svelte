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
		running: 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30',
		starting: 'bg-amber-500/15 text-amber-300 ring-amber-500/30',
		stopping: 'bg-amber-500/15 text-amber-300 ring-amber-500/30',
		installing: 'bg-sky-500/15 text-sky-300 ring-sky-500/30',
		offline: 'bg-slate-500/15 text-slate-400 ring-slate-500/30',
		crashed: 'bg-rose-500/15 text-rose-300 ring-rose-500/30',
		suspended: 'bg-rose-500/15 text-rose-300 ring-rose-500/30'
	};
	const pulse = 'relative inline-flex h-2 w-2';

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

<div class="mb-6 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-bold tracking-tight text-slate-50">Servers</h1>
		<p class="mt-0.5 text-sm text-slate-500">
			{servers.length} server{servers.length === 1 ? '' : 's'} ·
			{servers.filter((s) => s.status === 'running').length} running
		</p>
	</div>
	<button
		class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white shadow-lg shadow-emerald-950/40 transition hover:bg-emerald-500"
		onclick={() => (showCreate = !showCreate)}
	>
		{showCreate ? 'Cancel' : '+ Create server'}
	</button>
</div>

{#if showCreate}
	<form
		onsubmit={create}
		class="mb-8 space-y-4 rounded-xl border border-slate-800 bg-slate-900/70 p-5"
	>
		<div class="grid grid-cols-1 gap-4 md:grid-cols-4">
			<label class="block">
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Name</span>
				<input
					class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
					bind:value={name}
					placeholder="my-server"
					required
				/>
			</label>
			<label class="block">
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Server software</span>
				<select
					class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
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
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Minecraft version</span>
				<select
					class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
					bind:value={mcVersion}
				>
					{#each selectedRunner?.versions.filter((v) => v.stable).slice(0, 40) ?? [] as v (v.mc_version)}
						<option value={v.mc_version}>{v.mc_version} — Java {v.java_version}</option>
					{/each}
				</select>
			</label>
			<label class="block">
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Memory (MiB)</span>
				<input
					type="number"
					min="512"
					step="512"
					class="w-full rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
					bind:value={memoryMb}
				/>
			</label>
		</div>
		{#if error}
			<p class="rounded-lg border border-rose-900/60 bg-rose-950/40 px-3 py-2 text-sm text-rose-300">
				{error}
			</p>
		{/if}
		<div class="flex items-center gap-4">
			<button
				type="submit"
				disabled={creating || !mcVersion}
				class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-emerald-500 disabled:opacity-50"
			>
				{creating ? 'Creating…' : 'Create & install'}
			</button>
			<p class="text-xs text-slate-600">
				Accepts the Minecraft EULA on your behalf. The jar downloads and installs in the background.
			</p>
		</div>
	</form>
{/if}

<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
	{#each servers as server (server.id)}
		<a
			href="/servers/{server.id}"
			class="group rounded-xl border border-slate-800 bg-slate-900/70 p-5 transition hover:-translate-y-0.5 hover:border-emerald-700/60 hover:shadow-xl hover:shadow-emerald-950/20"
		>
			<div class="flex items-center gap-3">
				<img src="/favicon.svg" alt="" class="h-8 w-8 rounded-lg" />
				<div>
					<p class="font-semibold text-slate-100 group-hover:text-white">{server.name}</p>
					<p class="text-xs text-slate-500">{server.runner} · {server.mc_version}</p>
				</div>
				<span
					class={`ml-auto inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wide ring-1 ${statusStyle[server.status] ?? statusStyle.offline}`}
				>
					<span class={pulse}>
						<span class={`absolute inline-flex h-full w-full rounded-full ${server.status === 'running' ? 'animate-ping bg-emerald-400/60' : ''}`}></span>
						<span class={`relative inline-flex h-2 w-2 rounded-full ${server.status === 'running' ? 'bg-emerald-400' : 'bg-current'}`}></span>
					</span>
					{server.status}
				</span>
			</div>
			<div class="mt-4 flex items-center gap-2 text-xs text-slate-500">
				<span class="rounded bg-slate-800/80 px-2 py-1 font-mono">{server.ip}:{server.port}</span>
				<span class="rounded bg-slate-800/80 px-2 py-1">{server.memory_mb} MiB</span>
				<span class="ml-auto text-slate-600 transition group-hover:text-emerald-400">Open →</span>
			</div>
		</a>
	{:else}
		<div class="rounded-xl border border-dashed border-slate-800 p-12 text-center">
			<p class="text-slate-400">No servers yet.</p>
			<p class="mt-1 text-sm text-slate-600">
				Create one above — pick a runner, a Minecraft version, and you're mining.
			</p>
		</div>
	{/each}
</div>
