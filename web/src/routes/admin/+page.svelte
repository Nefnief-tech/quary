<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Node, type User } from '$lib/api';
	import { auth } from '$lib/auth.svelte';

	let nodes = $state<Node[]>([]);
	let users = $state<User[]>([]);
	let error = $state('');
	let newNodeName = $state('');
	let mintedToken = $state('');
	let mintedNodeId = $state('');
	let allocIp = $state('0.0.0.0');
	let allocFrom = $state(25565);
	let allocTo = $state(25575);
	let newUser = $state({ username: '', password: '', role: 'user' });

	async function refresh() {
		nodes = await api.get<Node[]>('/api/v1/nodes');
		users = await api.get<User[]>('/api/v1/users');
	}

	onMount(async () => {
		await auth.fetch();
		if (!auth.user || auth.user.role_name !== 'admin') {
			goto('/login');
			return;
		}
		await refresh();
	});

	async function createNode() {
		error = '';
		try {
			const res = await api.post<{ id: string; enrollment_token: string }>('/api/v1/nodes', {
				name: newNodeName,
				description: ''
			});
			mintedToken = res.enrollment_token;
			mintedNodeId = res.id;
			newNodeName = '';
			await refresh();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function rotateToken(node: Node) {
		const res = await api.post<{ enrollment_token: string }>(`/api/v1/nodes/${node.id}/token`);
		mintedToken = res.enrollment_token;
		mintedNodeId = node.id;
	}

	async function addAllocations(node: Node, e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			await api.post(`/api/v1/nodes/${node.id}/allocations`, {
				ip: allocIp,
				port_from: Number(allocFrom),
				port_to: Number(allocTo)
			});
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function createUser(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			await api.post('/api/v1/users', newUser);
			newUser = { username: '', password: '', role: 'user' };
			await refresh();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		}
	}

	async function setUserDisabled(user: User, disabled: boolean) {
		await api.post(`/api/v1/users/${user.id}/disabled`, { disabled });
		await refresh();
	}
</script>

<svelte:head>
	<title>Admin — Quarry</title>
</svelte:head>

<h1 class="mb-6 text-2xl font-bold tracking-tight text-slate-50">Admin</h1>
{#if error}
	<p class="mb-4 rounded-lg border border-rose-900/60 bg-rose-950/40 px-3 py-2 text-sm text-rose-300">
		{error}
	</p>
{/if}

<section class="mb-10">
	<h2 class="mb-3 text-xs font-bold uppercase tracking-widest text-slate-500">Nodes</h2>
	<div class="mb-4 flex gap-2">
		<input
			class="w-56 rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-emerald-500"
			placeholder="new node name"
			bind:value={newNodeName}
		/>
		<button
			class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-emerald-500"
			onclick={() => createNode()}>Create node</button
		>
	</div>
	<div class="space-y-3">
		{#each nodes as node (node.id)}
			<div class="rounded-xl border border-slate-800 bg-slate-900/70 p-5">
				<div class="flex flex-wrap items-center gap-3">
					<span
						class={`h-2.5 w-2.5 rounded-full ${node.status === 'online' ? 'animate-pulse bg-emerald-400' : 'bg-slate-600'}`}
					></span>
					<span class="font-semibold text-slate-100">{node.name}</span>
					<span
						class={`rounded-full px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide ring-1 ${node.status === 'online' ? 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30' : 'bg-slate-500/15 text-slate-400 ring-slate-500/30'}`}
					>
						{node.status}
					</span>
					<span class="text-xs text-slate-500">
						{node.hostname || 'never enrolled'}
						{#if node.daemon_version}· daemon {node.daemon_version}{/if}
						{#if node.os}· {node.os}/{node.arch}{/if}
					</span>
					<button
						class="ml-auto rounded-lg border border-slate-700 px-3 py-1.5 text-xs text-slate-300 transition hover:border-slate-500 hover:text-slate-100"
						onclick={() => rotateToken(node)}>Rotate enrollment token</button
					>
				</div>
				{#if mintedToken && mintedNodeId === node.id}
					<div class="mt-3 rounded-lg border border-emerald-900/50 bg-emerald-950/30 p-3">
						<p class="font-mono text-xs break-all text-emerald-300">{mintedToken}</p>
						<p class="mt-1.5 text-xs text-slate-500">
							Shown once. Set it as <code class="text-slate-400">QUARRY_ENROLLMENT_TOKEN</code> on
							the daemon and restart it.
						</p>
					</div>
				{/if}
				<form
					class="mt-4 flex flex-wrap items-center gap-2 border-t border-slate-800/60 pt-4 text-sm"
					onsubmit={(e) => addAllocations(node, e)}
				>
					<span class="text-slate-500">Allocations:</span>
					<input
						class="w-32 rounded-lg border border-slate-700/80 bg-slate-950 px-2.5 py-1.5 font-mono text-xs text-slate-100 outline-none focus:border-emerald-500"
						bind:value={allocIp}
					/>
					<input
						type="number"
						class="w-20 rounded-lg border border-slate-700/80 bg-slate-950 px-2.5 py-1.5 font-mono text-xs text-slate-100 outline-none focus:border-emerald-500"
						bind:value={allocFrom}
					/>
					<span class="text-slate-600">–</span>
					<input
						type="number"
						class="w-20 rounded-lg border border-slate-700/80 bg-slate-950 px-2.5 py-1.5 font-mono text-xs text-slate-100 outline-none focus:border-emerald-500"
						bind:value={allocTo}
					/>
					<button
						type="submit"
						class="rounded-lg bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-200 transition hover:bg-slate-700"
					>
						Add range
					</button>
				</form>
			</div>
		{:else}
			<div class="rounded-xl border border-dashed border-slate-800 p-10 text-center text-slate-500">
				No nodes yet. Create one, then point a daemon at this panel with the enrollment token.
			</div>
		{/each}
	</div>
</section>

<section>
	<h2 class="mb-3 text-xs font-bold uppercase tracking-widest text-slate-500">Users</h2>
	<div class="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/70">
		<table class="w-full text-sm">
			<tbody>
				{#each users as user (user.id)}
					<tr class="border-b border-slate-800/60 last:border-0">
						<td class="px-5 py-3 font-medium text-slate-100">{user.username}</td>
						<td class="px-5 py-3">
							<span
								class={`rounded-full px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide ring-1 ${user.role_name === 'admin' ? 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30' : 'bg-slate-500/15 text-slate-400 ring-slate-500/30'}`}
							>
								{user.role_name}
							</span>
						</td>
						<td class="px-5 py-3">
							{#if user.disabled}
								<span class="text-rose-400">disabled</span>
							{:else}
								<span class="text-slate-500">active</span>
							{/if}
						</td>
						<td class="px-5 py-3 text-right">
							{#if user.id !== auth.user?.id}
								<button
									class="rounded-lg border border-slate-700 px-3 py-1.5 text-xs text-slate-300 transition hover:border-slate-500 hover:text-slate-100"
									onclick={() => setUserDisabled(user, !user.disabled)}
								>
									{user.disabled ? 'Enable' : 'Disable'}
								</button>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
	<form class="mt-4 flex flex-wrap gap-2" onsubmit={createUser}>
		<input
			class="rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-emerald-500"
			placeholder="username"
			bind:value={newUser.username}
			required
		/>
		<input
			class="rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-emerald-500"
			placeholder="password"
			bind:value={newUser.password}
			required
		/>
		<select
			class="rounded-lg border border-slate-700/80 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-emerald-500"
			bind:value={newUser.role}
		>
			<option value="user">user</option>
			<option value="admin">admin</option>
		</select>
		<button
			type="submit"
			class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-emerald-500"
		>
			Create user
		</button>
	</form>
</section>
