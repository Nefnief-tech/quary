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

	// create form state
	let name = $state('');
	let runnerId = $state('paper');
	let mcVersion = $state('');
	let memoryMb = $state(2048);

	const statusColor: Record<string, string> = {
		running: 'bg-emerald-500',
		starting: 'bg-amber-400',
		stopping: 'bg-amber-400',
		installing: 'bg-sky-400',
		offline: 'bg-slate-500',
		crashed: 'bg-rose-500',
		suspended: 'bg-rose-400'
	};

	async function refresh() {
		try {
			servers = await api.get<Server[]>('/api/v1/servers');
		} catch (err) {
			if (err instanceof Error && 'status' in err && (err as { status: number }).status === 401) {
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
		runners = await api.get<Runner[]>('/api/v1/runners').catch(() => []);
		const preferred = runners.find((r) => r.id === runnerId) ?? runners[0];
		if (preferred) {
			runnerId = preferred.id;
			mcVersion = preferred.versions.find((v) => v.stable)?.mc_version ?? '';
		}
		// Refresh periodically so statuses stay live-ish without WS on the list.
		refreshTimer = setInterval(refresh, 5000);
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
	<h1 class="text-xl font-semibold text-slate-100">Your servers</h1>
	{#if auth.user?.role_name === 'admin' || auth.user}
		<button
			class="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-500"
			onclick={() => (showCreate = !showCreate)}
		>
			{showCreate ? 'Cancel' : 'Create server'}
		</button>
	{/if}
</div>

{#if showCreate}
	<form
		onsubmit={create}
		class="mb-6 grid grid-cols-1 gap-4 rounded-lg border border-slate-800 bg-slate-900/60 p-4 md:grid-cols-4"
	>
		<label class="block">
			<span class="mb-1 block text-sm text-slate-400">Name</span>
			<input
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
				bind:value={name}
				required
			/>
		</label>
		<label class="block">
			<span class="mb-1 block text-sm text-slate-400">Runner</span>
			<select
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
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
			<span class="mb-1 block text-sm text-slate-400">Minecraft version</span>
			<select
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
				bind:value={mcVersion}
			>
				{#each selectedRunner?.versions.filter((v) => v.stable).slice(0, 40) ?? [] as v (v.mc_version)}
					<option value={v.mc_version}>{v.mc_version} (Java {v.java_version})</option>
				{/each}
			</select>
		</label>
		<label class="block">
			<span class="mb-1 block text-sm text-slate-400">Memory (MiB)</span>
			<input
				type="number"
				min="512"
				step="512"
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100"
				bind:value={memoryMb}
			/>
		</label>
		<div class="md:col-span-4">
			{#if error}<p class="mb-2 text-sm text-rose-400">{error}</p>{/if}
			<button
				type="submit"
				disabled={creating || !mcVersion}
				class="rounded bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
			>
				{creating ? 'Creating…' : 'Create & install'}
			</button>
			<p class="mt-2 text-xs text-slate-500">
				Creating the server accepts the Minecraft EULA on your behalf. Installation downloads the
				jar and runs in the background.
			</p>
		</div>
	</form>
{/if}

<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
	{#each servers as server (server.id)}
		<a
			href="/servers/{server.id}"
			class="block rounded-lg border border-slate-800 bg-slate-900/60 p-4 transition hover:border-slate-600"
		>
			<div class="flex items-center gap-2">
				<span class={`inline-block h-2.5 w-2.5 rounded-full ${statusColor[server.status] ?? 'bg-slate-500'}`}
				></span>
				<span class="font-medium text-slate-100">{server.name}</span>
				<span class="ml-auto text-xs uppercase tracking-wide text-slate-500">{server.status}</span>
			</div>
			<p class="mt-2 text-sm text-slate-400">
				{server.runner} · {server.mc_version} · {server.ip}:{server.port}
			</p>
			<p class="text-xs text-slate-500">{server.memory_mb} MiB · Java runner image</p>
		</a>
	{:else}
		<p class="text-slate-500">No servers yet. Create one to get started.</p>
	{/each}
</div>
