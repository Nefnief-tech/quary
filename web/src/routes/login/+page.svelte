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

<div class="grid min-h-[calc(100vh-7rem)] grid-cols-1 overflow-hidden rounded-xl border border-slate-800/80 lg:grid-cols-2">
	<!-- Brand panel -->
	<div
		class="relative hidden flex-col justify-between bg-slate-900 p-10 lg:flex"
		style="background:
			radial-gradient(ellipse 80% 60% at 20% 0%, rgba(16,185,129,0.14), transparent),
			radial-gradient(ellipse 60% 50% at 90% 100%, rgba(16,185,129,0.08), transparent),
			#0c1222;"
	>
		<div class="flex items-center gap-3">
			<img src="/favicon.svg" alt="" class="h-10 w-10" />
			<span class="text-xl font-bold tracking-tight text-slate-50">Quarry</span>
		</div>
		<div>
			<h1 class="max-w-md text-3xl font-bold leading-tight text-slate-50">
				Run Minecraft servers,<br />
				<span class="text-emerald-400">without the busywork.</span>
			</h1>
			<ul class="mt-8 space-y-3 text-sm text-slate-400">
				<li class="flex items-center gap-3">
					<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
					Vanilla, Paper &amp; Fabric — one click from the version catalog
				</li>
				<li class="flex items-center gap-3">
					<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
					Live console, stats and file editing in the browser
				</li>
				<li class="flex items-center gap-3">
					<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
					Hardened containers on your own hardware
				</li>
			</ul>
		</div>
		<p class="text-xs text-slate-600">Open source · MIT · self-hosted</p>
	</div>

	<!-- Form panel -->
	<div class="flex items-center justify-center bg-slate-950 p-8">
		<form onsubmit={submit} class="w-full max-w-sm space-y-5">
			<div class="lg:hidden">
				<img src="/favicon.svg" alt="" class="mb-4 h-10 w-10" />
			</div>
			<div>
				<h2 class="text-2xl font-bold tracking-tight text-slate-50">Welcome back</h2>
				<p class="mt-1 text-sm text-slate-500">Log in to manage your servers.</p>
			</div>
			<label class="block">
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Username</span>
				<input
					class="w-full rounded-lg border border-slate-700/80 bg-slate-900 px-3.5 py-2.5 text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-500 focus:ring-2 focus:ring-emerald-500/20"
					bind:value={username}
					autocomplete="username"
					required
				/>
			</label>
			<label class="block">
				<span class="mb-1.5 block text-sm font-medium text-slate-400">Password</span>
				<input
					type="password"
					class="w-full rounded-lg border border-slate-700/80 bg-slate-900 px-3.5 py-2.5 text-slate-100 outline-none transition placeholder:text-slate-600 focus:border-emerald-500 focus:ring-2 focus:ring-emerald-500/20"
					bind:value={password}
					autocomplete="current-password"
					required
				/>
			</label>
			{#if error}
				<p class="rounded-lg border border-rose-900/60 bg-rose-950/40 px-3 py-2 text-sm text-rose-300">
					{error}
				</p>
			{/if}
			<button
				type="submit"
				disabled={busy}
				class="w-full rounded-lg bg-emerald-600 px-3.5 py-2.5 font-semibold text-white shadow-lg shadow-emerald-950/40 transition hover:bg-emerald-500 disabled:opacity-50"
			>
				{busy ? 'Signing in…' : 'Log in'}
			</button>
			<p class="text-center text-xs text-slate-600">
				First time? The first account registered becomes the admin.
			</p>
		</form>
	</div>
</div>
