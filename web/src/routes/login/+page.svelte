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
	<title>Log in - Quarry</title>
</svelte:head>

<div class="mx-auto mt-20 w-full max-w-sm">
	<div class="mb-8 flex items-center gap-3">
		<img src="/favicon.svg" alt="" class="h-9 w-9" />
		<div>
			<p class="text-lg font-semibold tracking-tight text-zinc-50">Quarry</p>
			<p class="text-xs text-zinc-500">Minecraft server management</p>
		</div>
	</div>

	<form onsubmit={submit} class="space-y-4">
		<label class="block">
			<span class="label">Username</span>
			<input class="input" bind:value={username} autocomplete="username" required />
		</label>
		<label class="block">
			<span class="label">Password</span>
			<input type="password" class="input" bind:value={password} autocomplete="current-password" required />
		</label>
		{#if error}
			<p class="rounded-md border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
				{error}
			</p>
		{/if}
		<button type="submit" disabled={busy} class="btn btn-primary w-full">
			{busy ? 'Signing in…' : 'Log in'}
		</button>
	</form>

	<p class="mt-6 text-xs text-zinc-600">
		First run? The first account registered becomes the admin.
	</p>
</div>
