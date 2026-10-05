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
		return `rounded-md px-3 py-1.5 text-sm transition ${
			active ? 'bg-zinc-800 text-zinc-50' : 'text-zinc-400 hover:text-zinc-200'
		}`;
	}
</script>

<div class="flex min-h-full flex-col">
	<header class="sticky top-0 z-20 border-b border-zinc-800/80 bg-zinc-950/90 backdrop-blur">
		<div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-5">
			<a href="/" class="flex items-center gap-2">
				<img src="/favicon.svg" alt="" class="h-6 w-6" />
				<span class="text-sm font-semibold tracking-tight text-zinc-50">Quarry</span>
			</a>
			<nav class="flex items-center gap-1">
				<a href="/" class={linkClass(page.url.pathname === '/')}>Servers</a>
				{#if isAdmin}
					<a href="/admin" class={linkClass(page.url.pathname === '/admin')}>Admin</a>
				{/if}
			</nav>
			<div class="ml-auto flex items-center gap-2.5">
				{#if auth.user}
					<span
						class="flex h-7 w-7 items-center justify-center rounded-md bg-zinc-800 text-[11px] font-semibold text-zinc-300"
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
	<main class="mx-auto w-full max-w-6xl flex-1 px-5 py-8">
		{@render children?.()}
	</main>
</div>
