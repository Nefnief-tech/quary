<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Node, type User } from '$lib/api';
	import { auth } from '$lib/auth.svelte';

	let nodes = $state<Node[]>([]);
	let servers = $state<{ id: string; name: string }[]>([]);
	let users = $state<User[]>([]);
	let error = $state('');
	let newNodeName = $state('');
	let mintedToken = $state('');
	let mintedNodeId = $state('');
	let allocIp = $state('0.0.0.0');
	let allocFrom = $state(25565);
	let allocTo = $state(25575);
	let newUser = $state({ username: '', password: '', role: 'user' });

	// handshake router settings (admin)
	let routerCfg = $state({ enabled: false, port: 25565, default_server_id: '' });
	let routerBusy = $state(false);
	let routerNote = $state('');

	async function loadRouter() {
		try {
			routerCfg = await api.get<typeof routerCfg>('/api/v1/settings/router');
		} catch {
			/* admin-only */
		}
	}

	async function saveRouter(e: SubmitEvent) {
		e.preventDefault();
		routerBusy = true;
		routerNote = '';
		try {
			routerCfg = await api.put<typeof routerCfg>('/api/v1/settings/router', {
				enabled: routerCfg.enabled,
				port: Number(routerCfg.port),
				default_server_id: routerCfg.default_server_id
			});
			routerNote = routerCfg.enabled
				? 'Router on. Map domains to servers from each server’s Public access card.'
				: 'Router disabled.';
		} catch (err) {
			routerNote = err instanceof Error ? err.message : String(err);
		} finally {
			routerBusy = false;
		}
	}

	// frp tunnel provider settings (admin)
	let frp = $state({ enabled: false, server_addr: '', server_port: 7000, token_set: false });
	let frpTokenInput = $state('');
	let frpBusy = $state(false);
	let frpNote = $state('');

	async function loadFrp() {
		try {
			const res = await api.get<{ frp: typeof frp }>('/api/v1/settings/expose');
			frp = res.frp;
		} catch {
			/* admin-only; ignore when not admin */
		}
	}

	async function saveFrp(e: SubmitEvent) {
		e.preventDefault();
		frpBusy = true;
		frpNote = '';
		try {
			const res = await api.put<{ frp: typeof frp }>('/api/v1/settings/expose', {
				frp: {
					enabled: frp.enabled,
					server_addr: frp.server_addr,
					server_port: Number(frp.server_port),
					token: frpTokenInput
				}
			});
			frp = res.frp;
			frpTokenInput = '';
			frpNote = frp.enabled
				? 'frp enabled. Servers can now be exposed via the Public access card.'
				: 'frp disabled.';
		} catch (err) {
			frpNote = err instanceof Error ? err.message : String(err);
		} finally {
			frpBusy = false;
		}
	}

	async function refresh() {
		nodes = await api.get<Node[]>('/api/v1/nodes');
		users = await api.get<User[]>('/api/v1/users');
		servers = await api.get<{ id: string; name: string }[]>('/api/v1/servers').catch(() => []);
	}

	onMount(async () => {
		await auth.fetch();
		if (!auth.user || auth.user.role_name !== 'admin') {
			goto('/login');
			return;
		}
		await refresh();
		loadFrp();
		loadRouter();
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
	<title>Admin - Quarry</title>
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
			class="input w-56"
			placeholder="new node name"
			bind:value={newNodeName}
		/>
		<button class="btn btn-primary" onclick={() => createNode()}>Create node</button>
	</div>
	<div class="space-y-3">
		{#each nodes as node (node.id)}
			<div class="card p-5">
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
					<button class="btn btn-secondary ml-auto px-3 py-1.5 text-xs" onclick={() => rotateToken(node)}>
						Rotate token
					</button>
				</div>
				{#if mintedToken && mintedNodeId === node.id}
					<div class="mt-3 rounded-lg border border-emerald-400/20 bg-emerald-400/5 p-3">
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
						class="input w-32 px-2.5 py-1.5 font-mono text-xs"
						bind:value={allocIp}
					/>
					<input
						type="number"
						class="input w-20 px-2.5 py-1.5 font-mono text-xs"
						bind:value={allocFrom}
					/>
					<span class="text-slate-600">–</span>
					<input
						type="number"
						class="input w-20 px-2.5 py-1.5 font-mono text-xs"
						bind:value={allocTo}
					/>
					<button type="submit" class="btn btn-secondary px-3 py-1.5 text-xs">Add range</button>
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
	<div class="card overflow-hidden">
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
								<button class="btn btn-secondary px-3 py-1.5 text-xs" onclick={() => setUserDisabled(user, !user.disabled)}>
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
		<input class="input" placeholder="username"
			bind:value={newUser.username}
			required
		/>
		<input class="input" placeholder="password"
			bind:value={newUser.password}
			required
		/>
		<select class="select w-auto" bind:value={newUser.role}>
			<option value="user">user</option>
			<option value="admin">admin</option>
		</select>
		<button type="submit" class="btn btn-primary">Create user</button>
	</form>
</section>

<section class="mb-10">
	<h2 class="mb-3 text-xs font-bold uppercase tracking-widest text-slate-500">Tunnel provider (frp)</h2>
	<form onsubmit={saveFrp} class="card space-y-4 p-5">
		<p class="text-sm text-slate-400">
			Let servers be exposed through an frp server: your own VPS running
			<span class="font-mono text-slate-300">frps</span>, or any hosted frp fleet.
			The token is stored encrypted.
		</p>
		<label class="flex items-center gap-2.5">
			<input type="checkbox" class="h-4 w-4 accent-emerald-500" bind:checked={frp.enabled} />
			<span class="text-sm text-slate-200">Enable frp tunnels</span>
		</label>
		<div class="grid grid-cols-1 gap-4 md:grid-cols-4">
			<label class="block md:col-span-2">
				<span class="label">frps address</span>
				<input class="input" placeholder="mc-relay.example.com" bind:value={frp.server_addr} />
			</label>
			<label class="block">
				<span class="label">frps port</span>
				<input type="number" min="1" max="65535" class="input" bind:value={frp.server_port} />
			</label>
			<label class="block">
				<span class="label">auth token {#if frp.token_set}<span class="text-xs text-slate-500">(saved)</span>{/if}</span>
				<input type="password" class="input" placeholder={frp.token_set ? 'leave empty to keep' : 'token'} bind:value={frpTokenInput} />
			</label>
		</div>
		<div class="flex items-center gap-4">
			<button type="submit" class="btn btn-primary" disabled={frpBusy}>
				{frpBusy ? 'Saving…' : 'Save settings'}
			</button>
			{#if frpNote}<p class="text-xs text-slate-500">{frpNote}</p>{/if}
		</div>
	</form>
</section>

<section class="mb-10">
	<h2 class="mb-3 text-xs font-bold uppercase tracking-widest text-slate-500">Handshake router</h2>
	<form onsubmit={saveRouter} class="card space-y-4 p-5">
		<p class="text-sm text-slate-400">
			One port for every server: the relay reads the Minecraft handshake and routes
			each connection by the hostname the player typed. Map domains per server;
			players join <span class="font-mono text-slate-300">your-domain:{routerCfg.port}</span>.
			Servers must not use the router port themselves.
		</p>
		<label class="flex items-center gap-2.5">
			<input type="checkbox" class="h-4 w-4 accent-emerald-500" bind:checked={routerCfg.enabled} />
			<span class="text-sm text-slate-200">Enable handshake router</span>
		</label>
		<div class="grid grid-cols-1 gap-4 md:grid-cols-4">
			<label class="block">
				<span class="label">Router port</span>
				<input type="number" min="1024" max="65535" class="input" bind:value={routerCfg.port} />
			</label>
			<label class="block md:col-span-3">
				<span class="label">Default server (unmatched hostnames)</span>
				<select class="select" bind:value={routerCfg.default_server_id}>
					<option value="">refuse unknown hostnames</option>
					{#each servers as srv (srv.id)}
						<option value={srv.id}>{srv.name}</option>
					{/each}
				</select>
			</label>
		</div>
		<div class="flex items-center gap-4">
			<button type="submit" class="btn btn-primary" disabled={routerBusy}>
				{routerBusy ? 'Saving…' : 'Save settings'}
			</button>
			{#if routerNote}<p class="text-xs text-slate-500">{routerNote}</p>{/if}
		</div>
	</form>
</section>
