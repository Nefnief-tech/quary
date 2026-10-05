// Thin API client. Session cookies ride along automatically (same-origin,
// proxy in dev); the panel sets HttpOnly cookies.

export interface User {
	id: string;
	username: string;
	email: string | null;
	role_name: string;
	disabled: boolean;
}

export interface Server {
	id: string;
	name: string;
	owner_id: string;
	node_id: string;
	runner: string;
	mc_version: string;
	status: string;
	memory_mb: number;
	cpu_percent: number;
	ip: string;
	port: number;
	/** Browser-reachable BlueMap webserver address; empty when no map. */
	map_url?: string;
	created_at: string;
}

export interface Runner {
	id: string;
	versions: { mc_version: string; stable: boolean; java_version: number }[];
}

export interface Node {
	id: string;
	name: string;
	description: string;
	status: string;
	daemon_version: string;
	hostname: string;
	docker_version: string;
	os: string;
	arch: string;
	last_seen_at: string | null;
	has_active_token: boolean;
}

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const res = await fetch(path, {
		method,
		headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
		body: body !== undefined ? JSON.stringify(body) : undefined,
		credentials: 'same-origin'
	});
	let data: unknown = null;
	try {
		data = await res.json();
	} catch {
		/* empty body */
	}
	if (!res.ok) {
		const message =
			data && typeof data === 'object' && 'error' in data
				? String((data as { error: unknown }).error)
				: res.statusText;
		throw new ApiError(res.status, message);
	}
	return data as T;
}

export const api = {
	get: <T>(path: string) => request<T>('GET', path),
	post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
	put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
	patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
	del: <T>(path: string) => request<T>('DELETE', path)
};
