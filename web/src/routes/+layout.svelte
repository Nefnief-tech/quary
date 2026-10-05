<script lang="ts">
	import '../app.css';
	import { auth } from '$lib/auth.svelte';

	let { children } = $props();

	$effect(() => {
		auth.fetch();
	});

	const isAdmin = $derived(auth.user?.role_name === 'admin');
</script>

<div class="flex h-full flex-col">
	<header class="border-b border-slate-800 bg-slate-900/60">
		<div class="mx-auto flex max-w-6xl items-center gap-6 px-4 py-3">
			<a href="/" class="flex items-center gap-2 font-semibold text-slate-100">
				<span class="inline-block h-5 w-5 rounded bg-emerald-500"></span>
				Quarry
			</a>
			<nav class="flex items-center gap-4 text-sm text-slate-400">
				<a href="/" class="hover:text-slate-100">Servers</a>
				{#if isAdmin}
					<a href="/admin" class="hover:text-slate-100">Admin</a>
				{/if}
			</nav>
			<div class="ml-auto flex items-center gap-3 text-sm">
				{#if auth.user}
					<span class="text-slate-400">{auth.user.username}</span>
					<button
						class="rounded border border-slate-700 px-2 py-1 text-slate-300 hover:border-slate-500 hover:text-slate-100"
						onclick={() => auth.logout()}
					>
						Log out
					</button>
				{:else}
					<a href="/login" class="text-slate-300 hover:text-slate-100">Log in</a>
				{/if}
			</div>
		</div>
	</header>
	<main class="mx-auto w-full max-w-6xl flex-1 px-4 py-6">
		{children}
	</main>
</div>
