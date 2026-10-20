import { api, seg } from '$lib/services/api';
export interface UserData {
	id: number;
	username: string;
	has_password: boolean;
	role: string;
}

export interface PermissionData {
	username: string;
	vhost: string;
}

export interface UserCreateRequest {
	username: string;
	password: string;
	confirm_password: string;
	role: number;
}

// fetch users
export async function getUsers(): Promise<UserData[] | null> {
	try {
		const response = await api.get('/api/admin/users');
		const data = await response.json();
		return Array.isArray(data?.users) ? data.users : [];
	} catch (err) {
		console.error('Failed to fetch', err);
	}
	return null;
}

// add user
export async function addUser(req: UserCreateRequest): Promise<void> {
	await api.post('/api/admin/users', req);
}

export async function deleteUser(username: string): Promise<void> {
	await api.delete(`/api/admin/users/${seg(username)}`);
}

export async function changePassword(
	username: string,
	password: string,
	confirmPassword: string
): Promise<void> {
	await api.put(`/api/admin/users/${seg(username)}/password`, {
		password,
		confirm_password: confirmPassword
	});
}

// fetch permissions
export async function getPermission(): Promise<PermissionData[] | null> {
	try {
		const response = await api.get('/api/admin/permissions');
		const data = await response.json();
		return Array.isArray(data?.permissions) ? data.permissions : [];
	} catch (err) {
		console.error('Failed to fetch', err);
	}
	return null;
}

export async function grantPermission(vhost: string, username: string): Promise<void> {
	await api.put(`/api/admin/permissions/${seg(vhost)}/${seg(username)}`);
}

export async function revokePermission(vhost: string, username: string): Promise<void> {
	await api.delete(`/api/admin/permissions/${seg(vhost)}/${seg(username)}`);
}
