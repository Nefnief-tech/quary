<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import { auth } from '$lib/auth.svelte';

	let { children } = $props();

	$effect(() => {
		auth.fetch();
	});

	const isAdmin = $derived(auth.user?.role_name === 'admin');
	const initials = $derived(
		(auth.user?.username ?? '?')
			.split(/[-_.]/)
			.map((p) => p[0])
			.slice(0, 2)
			.join('')
			.toUpperCase()
	);
</script>

<div class="flex h-full flex-col bg-slate-950">
	<header class="sticky top-0 z-20 border-b border-slate-800/80 bg-slate-950/80 backdrop-blur">
		<div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
			<a href="/" class="group flex items-center gap-2.5">
				<img src="/favicon.svg" alt="" class="h-7 w-7" />
				<span class="text-[15px] font-semibold tracking-tight text-slate-100 group-hover:text-white">
					Quarry
				</span>
			</a>
			<nav class="flex items-center gap-1 text-sm">
				<a
					href="/"
					class="rounded-md px-3 py-1.5 transition {page.url.pathname === '/'
						? 'bg-slate-800/80 text-slate-100'
						: 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'}"
				>
					Servers
				</a>
				{#if isAdmin}
					<a
						href="/admin"
						class="rounded-md px-3 py-1.5 transition {page.url.pathname === '/admin'
							? 'bg-slate-800/80 text-slate-100'
							: 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'}"
					>
						Admin
					</a>
				{/if}
			</nav>
			<div class="ml-auto flex items-center gap-3">
				{#if auth.user}
					<span
						class="flex h-8 w-8 items-center justify-center rounded-full bg-emerald-600/20 text-xs font-bold text-emerald-300 ring-1 ring-emerald-600/40"
						title={auth.user.username}
					>
						{initials}
					</span>
					<button
						class="rounded-md border border-slate-700/80 px-3 py-1.5 text-sm text-slate-300 transition hover:border-slate-500 hover:text-slate-100"
						onclick={() => auth.logout()}
					>
						Sign out
					</button>
				{:else}
					<a
						href="/login"
						class="rounded-md bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-emerald-500"
					>
						Log in
					</a>
				{/if}
			</div>
		</div>
	</header>
	<main class="mx-auto w-full max-w-6xl flex-1 px-4 py-8">
		{@render children?.()}
	</main>
	<footer class="border-t border-slate-800/60 py-4">
		<p class="text-center text-xs text-slate-600">
			Quarry — open-source Minecraft server management
		</p>
	</footer>
</div>
