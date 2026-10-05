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

<div class="grid min-h-[calc(100vh-8rem)] grid-cols-1 overflow-hidden rounded-2xl border border-white/5 shadow-2xl shadow-black/40 lg:grid-cols-2">
	<!-- Brand panel -->
	<div
		class="grid-bg relative hidden flex-col justify-between p-12 lg:flex"
		style="background:
			radial-gradient(ellipse 90% 55% at 12% -5%, rgba(16, 185, 129, 0.16), transparent 60%),
			radial-gradient(ellipse 55% 45% at 95% 105%, rgba(56, 189, 248, 0.08), transparent 65%),
			linear-gradient(160deg, #0a1120 0%, #060a12 100%);"
	>
		<div class="flex items-center gap-3">
			<img src="/favicon.svg" alt="" class="h-11 w-11 drop-shadow-[0_0_18px_rgba(16,185,129,0.35)]" />
			<span class="text-2xl font-bold tracking-tight text-white">Quarry</span>
		</div>
		<div>
			<p class="mb-3 inline-flex items-center gap-2 rounded-full border border-emerald-400/20 bg-emerald-400/10 px-3 py-1 text-xs font-medium text-emerald-300">
				<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
				self-hosted · open source
			</p>
			<h1 class="max-w-md text-4xl font-bold leading-[1.1] tracking-tight text-white">
				Run Minecraft servers,<br />
				<span class="bg-gradient-to-r from-emerald-300 to-teal-400 bg-clip-text text-transparent">
					without the busywork.
				</span>
			</h1>
			<ul class="mt-9 space-y-4 text-[15px] text-slate-400">
				<li class="flex items-center gap-3">
					<span class="flex h-6 w-6 items-center justify-center rounded-md bg-emerald-400/10 text-xs text-emerald-300 ring-1 ring-emerald-400/25">✦</span>
					Vanilla, Paper &amp; Fabric — one click from the version catalog
				</li>
				<li class="flex items-center gap-3">
					<span class="flex h-6 w-6 items-center justify-center rounded-md bg-emerald-400/10 text-xs text-emerald-300 ring-1 ring-emerald-400/25">✦</span>
					Live console, stats, plugins and file editing in the browser
				</li>
				<li class="flex items-center gap-3">
					<span class="flex h-6 w-6 items-center justify-center rounded-md bg-emerald-400/10 text-xs text-emerald-300 ring-1 ring-emerald-400/25">✦</span>
					Hardened containers on hardware you own
				</li>
			</ul>
		</div>
		<p class="text-xs text-slate-600">MIT licensed · no paywalled features · your hardware, your rules</p>
	</div>

	<!-- Form panel -->
	<div class="flex items-center justify-center bg-[#05070d] p-10">
		<form onsubmit={submit} class="w-full max-w-sm space-y-5">
			<div class="lg:hidden">
				<img src="/favicon.svg" alt="" class="mb-5 h-11 w-11" />
			</div>
			<div>
				<h2 class="text-2xl font-bold tracking-tight text-white">Welcome back</h2>
				<p class="mt-1.5 text-sm text-slate-500">Log in to manage your servers.</p>
			</div>
			<label class="block">
				<span class="label">Username</span>
				<input class="input" bind:value={username} autocomplete="username" required />
			</label>
			<label class="block">
				<span class="label">Password</span>
				<input type="password" class="input" bind:value={password} autocomplete="current-password" required />
			</label>
			{#if error}
				<p class="rounded-lg border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
					{error}
				</p>
			{/if}
			<button type="submit" disabled={busy} class="btn btn-primary w-full py-2.5">
				{busy ? 'Signing in…' : 'Log in'}
			</button>
			<p class="text-center text-xs text-slate-600">
				First run? The first account registered becomes the admin.
			</p>
		</form>
	</div>
</div>
