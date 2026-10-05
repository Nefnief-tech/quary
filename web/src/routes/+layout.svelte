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

	function linkClass(active: boolean) {
		return `rounded-lg px-3 py-1.5 text-sm transition ${
			active
				? 'bg-white/10 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.06)]'
				: 'text-slate-400 hover:bg-white/5 hover:text-slate-200'
		}`;
	}
</script>

<div class="flex min-h-full flex-col">
	<header class="sticky top-0 z-20 border-b border-white/5 bg-[#05070d]/70 backdrop-blur-xl">
		<div class="mx-auto flex h-16 max-w-6xl items-center gap-8 px-5">
			<a href="/" class="group flex items-center gap-2.5">
				<img src="/favicon.svg" alt="" class="h-8 w-8 transition group-hover:scale-105" />
				<span class="text-[15px] font-bold tracking-tight text-white">Quarry</span>
			</a>
			<nav class="flex items-center gap-1.5">
				<a href="/" class={linkClass(page.url.pathname === '/')}>Servers</a>
				{#if isAdmin}
					<a href="/admin" class={linkClass(page.url.pathname === '/admin')}>Admin</a>
				{/if}
			</nav>
			<div class="ml-auto flex items-center gap-3">
				{#if auth.user}
					<span
						class="flex h-9 w-9 items-center justify-center rounded-full bg-gradient-to-b from-emerald-400/30 to-emerald-600/30 text-xs font-bold text-emerald-300 ring-1 ring-emerald-400/30"
						title={auth.user.username}
					>
						{initials}
					</span>
					<button class="btn btn-ghost" onclick={() => auth.logout()}>Sign out</button>
				{:else}
					<a href="/login" class="btn btn-primary">Log in</a>
				{/if}
			</div>
		</div>
	</header>
	<main class="mx-auto w-full max-w-6xl flex-1 px-5 py-9">
		{@render children?.()}
	</main>
	<footer class="border-t border-white/5 py-5">
		<p class="text-center text-xs text-slate-600">
			Quarry · open-source Minecraft server management
		</p>
	</footer>
</div>
