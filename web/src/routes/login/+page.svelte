<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		busy = true;
		try {
			await api.post('/api/v1/auth/login', { username, password });
			goto('/');
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'login failed';
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Log in — Quarry</title>
</svelte:head>

<div class="mx-auto mt-16 max-w-sm">
	<h1 class="mb-6 text-center text-2xl font-semibold text-slate-100">Log in to Quarry</h1>
	<form onsubmit={submit} class="space-y-4 rounded-lg border border-slate-800 bg-slate-900/60 p-6">
		<label class="block">
			<span class="mb-1 block text-sm text-slate-400">Username</span>
			<input
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
				bind:value={username}
				autocomplete="username"
				required
			/>
		</label>
		<label class="block">
			<span class="mb-1 block text-sm text-slate-400">Password</span>
			<input
				type="password"
				class="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 text-slate-100 outline-none focus:border-emerald-500"
				bind:value={password}
				autocomplete="current-password"
				required
			/>
		</label>
		{#if error}
			<p class="text-sm text-rose-400">{error}</p>
		{/if}
		<button
			type="submit"
			disabled={busy}
			class="w-full rounded bg-emerald-600 px-3 py-2 font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
		>
			{busy ? '…' : 'Log in'}
		</button>
	</form>
</div>
