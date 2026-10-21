<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import deleteIcon from '$lib/icons/delete.svg?raw';
	import keyIcon from '$lib/icons/key.svg?raw';
	import { usePolledList } from '$lib/pooling.svelte';
	import { errorMessage } from '$lib/services/api';
	import type { PermissionData, UserCreateRequest } from '$lib/stores/admin.svelte';
	import {
		addUser,
		changePassword,
		deleteUser,
		getPermission,
		getUsers,
		grantPermission,
		revokePermission
	} from '$lib/stores/admin.svelte';
	import { auth } from '$lib/stores/auth.svelte';
	import { confirmAction, notify } from '$lib/stores/feedback.svelte';
	import { vhosts } from '$lib/stores/vhosts.svelte';

	const users = usePolledList(getUsers);
	const permissions = usePolledList(getPermission);

	// Role IDs as seeded in internal/persistdb/roles.go.
	const roleOptions = [
		{ label: 'Admin', value: 1 },
		{ label: 'User', value: 2 },
		{ label: 'Guest', value: 3 }
	];

	// --- Add user ---

	function emptyUser(): UserCreateRequest {
		return { username: '', password: '', confirm_password: '', role: 2 };
	}

	let showAddUser = $state(false);
	let newUser = $state<UserCreateRequest>(emptyUser());
	let addUserError = $state('');
	const passwordsDiffer = $derived(
		newUser.confirm_password !== '' && newUser.password !== newUser.confirm_password
	);

	function openAddUser() {
		newUser = emptyUser();
		addUserError = '';
		showAddUser = true;
	}

	async function submitAddUser(e: SubmitEvent) {
		e.preventDefault();
		addUserError = '';
		try {
			await addUser(newUser);
			showAddUser = false;
			notify('success', `User "${newUser.username}" created`);
			await users.refresh();
		} catch (err) {
			addUserError = errorMessage(err, 'Failed to add the user');
		}
	}

	// --- Change password ---

	let pwTarget = $state<string | null>(null);
	let pw = $state({ password: '', confirm: '' });
	let pwError = $state('');
	const pwDiffer = $derived(pw.confirm !== '' && pw.password !== pw.confirm);

	function openChangePassword(username: string) {
		pwTarget = username;
		pw = { password: '', confirm: '' };
		pwError = '';
	}

	async function submitChangePassword(e: SubmitEvent) {
		e.preventDefault();
		if (!pwTarget) return;
		pwError = '';
		try {
			await changePassword(pwTarget, pw.password, pw.confirm);
			notify('success', `Password for "${pwTarget}" updated`);
			pwTarget = null;
		} catch (err) {
			pwError = errorMessage(err, 'Failed to change the password');
		}
	}

	// --- Delete user ---

	async function removeUser(username: string) {
		const ok = await confirmAction({
			title: 'Delete user',
			message: `Delete user "${username}"? Their vhost permissions are removed too.`,
			confirmLabel: 'Delete'
		});
		if (!ok) return;
		try {
			await deleteUser(username);
			notify('success', `User "${username}" deleted`);
			await Promise.all([users.refresh(), permissions.refresh()]);
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to delete the user'));
		}
	}

	// --- Grant / revoke permission ---

	let showGrant = $state(false);
	let grant = $state({ username: '', vhost: '' });
	let grantError = $state('');

	function openGrant() {
		grant = { username: users.items?.[0]?.username ?? '', vhost: vhosts.selected };
		grantError = '';
		showGrant = true;
	}

	async function submitGrant(e: SubmitEvent) {
		e.preventDefault();
		grantError = '';
		try {
			await grantPermission(grant.vhost, grant.username);
			showGrant = false;
			notify('success', `Granted "${grant.username}" access to "${grant.vhost}"`);
			await permissions.refresh();
		} catch (err) {
			grantError = errorMessage(err, 'Failed to grant the permission');
		}
	}

	async function revoke(p: PermissionData) {
		const ok = await confirmAction({
			title: 'Revoke permission',
			message: `Remove "${p.username}"'s access to vhost "${p.vhost}"?`,
			confirmLabel: 'Revoke'
		});
		if (!ok) return;
		try {
			await revokePermission(p.vhost, p.username);
			notify('success', 'Permission revoked');
			await permissions.refresh();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to revoke the permission'));
		}
	}
</script>

<div class="page-head">
	<h1>Admin</h1>
</div>

<section>
	<div class="page-head">
		<h2>Users</h2>
		<button class="btn btn-primary" onclick={openAddUser}>Add user</button>
	</div>
	<div class="table-card">
		<table>
			<thead>
				<tr>
					<th>Username</th>
					<th>Role</th>
					<th>Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each users.items ?? [] as u (u.id)}
					<tr>
						<td>{u.username}</td>
						<td><span class="role role--{u.role}">{u.role}</span></td>
						<td class="action">
							<button
								class="row-action"
								aria-label="Change password for {u.username}"
								title="Change password"
								onclick={() => openChangePassword(u.username)}
							>
								<!-- static, build-time-bundled icon -->
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html keyIcon}
							</button>
							<button
								class="row-action"
								aria-label="Delete user {u.username}"
								title={u.username === auth.username
									? 'You cannot delete the user you are logged in as'
									: 'Delete user'}
								disabled={u.username === auth.username}
								onclick={() => removeUser(u.username)}
							>
								<!-- static, build-time-bundled icon -->
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html deleteIcon}
							</button>
						</td>
					</tr>
				{:else}
					<tr class="empty-row">
						<td colspan="3">{users.items === null ? 'Loading…' : 'No users'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</section>

<section>
	<div class="page-head">
		<h2>Permissions</h2>
		<button class="btn btn-primary" onclick={openGrant} disabled={!users.items?.length}>
			Grant permission
		</button>
	</div>
	<div class="table-card">
		<table>
			<thead>
				<tr>
					<th>User</th>
					<th>VHost</th>
					<th>Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each permissions.items ?? [] as p (p.vhost + p.username)}
					<tr>
						<td>{p.username}</td>
						<td>{p.vhost}</td>
						<td class="action">
							<button
								class="row-action"
								aria-label="Revoke {p.username}'s access to {p.vhost}"
								title="Revoke"
								onclick={() => revoke(p)}
							>
								<!-- static, build-time-bundled icon -->
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html deleteIcon}
							</button>
						</td>
					</tr>
				{:else}
					<tr class="empty-row">
						<td colspan="3">{permissions.items === null ? 'Loading…' : 'No permissions'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</section>

<Modal open={showAddUser} title="Add user" onclose={() => (showAddUser = false)}>
	<form class="form" onsubmit={submitAddUser}>
		{#if addUserError}<p class="form-error">{addUserError}</p>{/if}
		<label>
			Username
			<input type="text" bind:value={newUser.username} required autocomplete="off" />
		</label>
		<label>
			Password
			<input type="password" bind:value={newUser.password} required autocomplete="new-password" />
		</label>
		<label>
			Confirm password
			<input
				type="password"
				bind:value={newUser.confirm_password}
				required
				autocomplete="new-password"
			/>
			{#if passwordsDiffer}<span class="form-hint mismatch">Passwords don't match</span>{/if}
		</label>
		<label>
			Role
			<select bind:value={newUser.role}>
				{#each roleOptions as r (r.value)}
					<option value={r.value}>{r.label}</option>
				{/each}
			</select>
		</label>
		<div class="form-actions">
			<button type="button" class="btn" onclick={() => (showAddUser = false)}>Cancel</button>
			<button
				type="submit"
				class="btn btn-primary"
				disabled={!newUser.username || !newUser.password || passwordsDiffer}>Add</button
			>
		</div>
	</form>
</Modal>

<Modal
	open={pwTarget !== null}
	title="Change password for “{pwTarget}”"
	onclose={() => (pwTarget = null)}
>
	<form class="form" onsubmit={submitChangePassword}>
		{#if pwError}<p class="form-error">{pwError}</p>{/if}
		<label>
			New password
			<input type="password" bind:value={pw.password} required autocomplete="new-password" />
		</label>
		<label>
			Confirm password
			<input type="password" bind:value={pw.confirm} required autocomplete="new-password" />
			{#if pwDiffer}<span class="form-hint mismatch">Passwords don't match</span>{/if}
		</label>
		<div class="form-actions">
			<button type="button" class="btn" onclick={() => (pwTarget = null)}>Cancel</button>
			<button type="submit" class="btn btn-primary" disabled={!pw.password || pwDiffer}>Save</button
			>
		</div>
	</form>
</Modal>

<Modal open={showGrant} title="Grant permission" onclose={() => (showGrant = false)}>
	<form class="form" onsubmit={submitGrant}>
		{#if grantError}<p class="form-error">{grantError}</p>{/if}
		<label>
			User
			<select bind:value={grant.username} required>
				{#each users.items ?? [] as u (u.id)}
					<option value={u.username}>{u.username}</option>
				{/each}
			</select>
		</label>
		<label>
			Virtual host
			<select bind:value={grant.vhost} required>
				{#each vhosts.items as v (v.name)}
					<option value={v.name}>{v.name}</option>
				{/each}
			</select>
		</label>
		<div class="form-actions">
			<button type="button" class="btn" onclick={() => (showGrant = false)}>Cancel</button>
			<button type="submit" class="btn btn-primary" disabled={!grant.username || !grant.vhost}
				>Grant</button
			>
		</div>
	</form>
</Modal>

<style>
	section {
		margin-top: 24px;
	}
	h2 {
		font-size: 22px;
		font-weight: 500;
		color: var(--color-text);
		margin: 0;
	}
	.role {
		font-size: 0.72rem;
		font-weight: 600;
		padding: 2px 8px;
		border-radius: 4px;
		background: color-mix(in srgb, var(--color-accent) 16%, transparent);
	}
	.role--admin {
		background: color-mix(in srgb, #c10015 16%, transparent);
	}
	.mismatch {
		color: #c10015;
	}
</style>
