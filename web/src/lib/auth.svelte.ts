import { api, type User } from '$lib/api';

// Svelte 5 rune-based auth state.
class Auth {
	user = $state<User | null>(null);
	loaded = $state(false);

	async fetch() {
		try {
			this.user = await api.get<User>('/api/v1/auth/me');
		} catch {
			this.user = null;
		}
		this.loaded = true;
		return this.user;
	}

	async logout() {
		try {
			await api.post('/api/v1/auth/logout');
		} finally {
			this.user = null;
		}
	}
}

export const auth = new Auth();
