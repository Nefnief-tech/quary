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

<h1 class="mb-6 text-xl font-semibold text-slate-100">Admin</h1>
{#if error}<p class="mb-3 text-sm text-rose-400">{error}</p>{/if}

<section class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-400">Nodes</h2>
	<div class="mb-3 flex gap-2">
		<input
			class="rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
			placeholder="new node name"
			bind:value={newNodeName}
		/>
		<button
			class="rounded bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-500"
			onclick={() => createNode}>Create node</button
		>
	</div>
	<div class="space-y-3">
		{#each nodes as node (node.id)}
			<div class="rounded-lg border border-slate-800 bg-slate-900/60 p-4">
				<div class="flex flex-wrap items-center gap-2">
					<span
						class={`inline-block h-2.5 w-2.5 rounded-full ${node.status === 'online' ? 'bg-emerald-500' : 'bg-slate-500'}`}
					></span>
					<span class="font-medium text-slate-100">{node.name}</span>
					<span class="text-xs uppercase tracking-wide text-slate-500">{node.status}</span>
					<span class="text-xs text-slate-500">
						{node.hostname || '—'} · {node.daemon_version || 'never enrolled'}
					</span>
					<button
						class="ml-auto rounded border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:border-slate-500"
						onclick={() => rotateToken(node)}>Rotate token</button
					>
				</div>
				{#if mintedToken && mintedNodeId === node.id}
					<p class="mt-3 rounded bg-slate-800 p-2 font-mono text-xs break-all text-emerald-300">
						{mintedToken}
						<span class="mt-1 block text-slate-400">
							Shown once — set QUARRY_ENROLLMENT_TOKEN on the daemon and restart it.
						</span>
					</p>
				{/if}
				<form class="mt-3 flex flex-wrap items-center gap-2 text-sm" onsubmit={(e) => addAllocations(node, e)}>
					<span class="text-slate-400">Allocations:</span>
					<input
						class="w-32 rounded border border-slate-700 bg-slate-800 px-2 py-1 text-slate-100"
						bind:value={allocIp}
					/>
					<input
						type="number"
						class="w-20 rounded border border-slate-700 bg-slate-800 px-2 py-1 text-slate-100"
						bind:value={allocFrom}
					/>
					<span class="text-slate-500">–</span>
					<input
						type="number"
						class="w-20 rounded border border-slate-700 bg-slate-800 px-2 py-1 text-slate-100"
						bind:value={allocTo}
					/>
					<button type="submit" class="rounded bg-slate-700 px-3 py-1 text-slate-100 hover:bg-slate-600">
						Add range
					</button>
				</form>
			</div>
		{:else}
			<p class="text-slate-500">No nodes yet.</p>
		{/each}
	</div>
</section>

<section class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-400">Users</h2>
	<div class="rounded-lg border border-slate-800 bg-slate-900/60">
		<table class="w-full text-sm">
			<tbody>
				{#each users as user (user.id)}
					<tr class="border-b border-slate-800/60">
						<td class="px-4 py-2 text-slate-100">{user.username}</td>
						<td class="px-4 py-2 text-slate-400">{user.role_name}</td>
						<td class="px-4 py-2">
							{#if user.disabled}
								<span class="text-rose-400">disabled</span>
							{:else}
								<span class="text-emerald-400">active</span>
							{/if}
						</td>
						<td class="px-4 py-2 text-right">
							{#if user.id !== auth.user?.id}
								<button
									class="rounded border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:border-slate-500"
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
	<form class="mt-3 flex flex-wrap gap-2" onsubmit={createUser}>
		<input
			class="rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
			placeholder="username"
			bind:value={newUser.username}
			required
		/>
		<input
			class="rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
			placeholder="password"
			bind:value={newUser.password}
			required
		/>
		<select
			class="rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100"
			bind:value={newUser.role}
		>
			<option value="user">user</option>
			<option value="admin">admin</option>
		</select>
		<button type="submit" class="rounded bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-500">
			Create user
		</button>
	</form>
</section>
