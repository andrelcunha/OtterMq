import { goto } from '$app/navigation';
import { resolve } from '$app/paths';
import { auth, logout } from '$lib/stores/auth.svelte';

/** Error thrown for a non-2xx API response; carries the HTTP status. */
export class ApiError extends Error {
	constructor(
		message: string,
		public readonly status: number
	) {
		super(message);
		this.name = 'ApiError';
	}
}

export async function apiFetch(url: string, options: RequestInit = {}): Promise<Response> {
	const headers = new Headers(options.headers || {});
	if (auth.token) {
		headers.set('Authorization', `Bearer ${auth.token}`);
	}
	headers.set('Content-type', 'application/json');

	const response = await fetch(url, {
		...options,
		headers
	});

	// An expired or invalid token: drop it and send the user back to the login page,
	// instead of leaving every table silently empty. The login call itself returns 401
	// for bad credentials, which the login page reports on its own.
	if (response.status === 401 && auth.token && !url.endsWith('/api/login')) {
		logout();
		goto(resolve('/login'));
	}

	if (!response.ok) {
		let message = `API error: ${response.status}`;
		try {
			const data = await response.json();
			if (data?.error) {
				message = data.error;
			}
		} catch {
			// body wasn't JSON (or was empty) -- keep the generic message
		}
		throw new ApiError(message, response.status);
	}

	return response;
}

function withBody(method: string, body: unknown): RequestInit {
	return body === undefined ? { method } : { method, body: JSON.stringify(body) };
}

export const api = {
	get: (url: string) => apiFetch(url),
	post: (url: string, body?: unknown) => apiFetch(url, withBody('POST', body)),
	put: (url: string, body?: unknown) => apiFetch(url, withBody('PUT', body)),
	delete: (url: string, body?: unknown) => apiFetch(url, withBody('DELETE', body))
};

/** Encodes a path segment (vhost, queue, exchange or user name), e.g. "/" -> "%2F". */
export const seg = encodeURIComponent;

/** Message of an error thrown by the api helpers, or a fallback. */
export function errorMessage(err: unknown, fallback = 'Request failed'): string {
	return err instanceof Error && err.message ? err.message : fallback;
}
