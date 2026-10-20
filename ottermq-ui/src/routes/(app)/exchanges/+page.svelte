<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import deleteIcon from '$lib/icons/delete.svg?raw';
	import { usePolledList } from '$lib/pooling.svelte';
	import { errorMessage } from '$lib/services/api';
	import type { BindingData, CreateExchangeForm, ExchangeData } from '$lib/stores/exchanges.svelte';
	import {
		DEFAULT_EXCHANGE,
		EXCHANGE_TYPES,
		addBinding,
		createExchange,
		deleteExchange,
		getBindings,
		getExchanges,
		isBuiltinExchange,
		publishMessage,
		removeBinding
	} from '$lib/stores/exchanges.svelte';
	import { confirmAction, notify } from '$lib/stores/feedback.svelte';
	import { vhosts } from '$lib/stores/vhosts.svelte';

	const exchanges = usePolledList(getExchanges);

	// Only the selected vhost's exchanges, like the rest of the UI.
	const visible = $derived((exchanges.items ?? []).filter((e) => e.vhost === vhosts.selected));

	// --- Selection ---

	let selected = $state<string | null>(null);
	let bindings = $state<BindingData[]>([]);

	// The default exchange routes to every queue by name implicitly; listing or
	// editing its bindings would be misleading.
	const isDefault = $derived(selected === DEFAULT_EXCHANGE);

	async function loadBindings() {
		if (!selected || isDefault) {
			bindings = [];
			return;
		}
		try {
			bindings = await getBindings(vhosts.selected, selected);
		} catch (err) {
			bindings = [];
			notify('error', errorMessage(err, 'Failed to load bindings'));
		}
	}

	function select(name: string) {
		selected = name;
		loadBindings();
	}

	// Switching vhost clears a selection that belongs to the previous one.
	$effect(() => {
		void vhosts.selected;
		selected = null;
		bindings = [];
	});

	// --- Create ---

	function emptyForm(): CreateExchangeForm {
		return { name: '', vhost: vhosts.selected, type: 'direct', durable: true, auto_delete: false };
	}

	let showCreate = $state(false);
	let form = $state<CreateExchangeForm>(emptyForm());
	let createError = $state('');
	let creating = $state(false);

	function openCreate() {
		form = emptyForm();
		createError = '';
		showCreate = true;
	}

	async function submitCreate(e: SubmitEvent) {
		e.preventDefault();
		createError = '';
		creating = true;
		try {
			await createExchange(form);
			showCreate = false;
			notify('success', `Exchange "${form.name}" created`);
			await exchanges.refresh();
		} catch (err) {
			createError = errorMessage(err, 'Failed to create the exchange');
		} finally {
			creating = false;
		}
	}

	// --- Delete ---

	async function remove(ex: ExchangeData) {
		const ok = await confirmAction({
			title: 'Delete exchange',
			message: `Delete exchange "${ex.name}" in vhost "${ex.vhost}" and all of its bindings?`,
			confirmLabel: 'Delete'
		});
		if (!ok) return;
		try {
			await deleteExchange(ex.vhost, ex.name);
			if (selected === ex.name) selected = null;
			notify('success', `Exchange "${ex.name}" deleted`);
			await exchanges.refresh();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to delete the exchange'));
		}
	}

	// --- Bindings ---

	let bindQueue = $state('');
	let bindKey = $state('');

	async function submitBinding(e: SubmitEvent) {
		e.preventDefault();
		if (!selected) return;
		try {
			await addBinding(vhosts.selected, selected, bindQueue, bindKey);
			notify('success', `Bound "${bindQueue}" to "${selected}"`);
			bindQueue = '';
			bindKey = '';
			await loadBindings();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to add the binding'));
		}
	}

	async function unbind(b: BindingData) {
		const ok = await confirmAction({
			title: 'Remove binding',
			message: `Unbind queue "${b.destination}" (routing key "${b.routing_key}") from "${b.source}"?`,
			confirmLabel: 'Unbind'
		});
		if (!ok) return;
		try {
			await removeBinding(vhosts.selected, b);
			notify('success', 'Binding removed');
			await loadBindings();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to remove the binding'));
		}
	}

	// --- Publish ---

	let pubKey = $state('');
	let pubPayload = $state('');
	let pubPersistent = $state(false);
	let publishing = $state(false);

	async function submitPublish(e: SubmitEvent) {
		e.preventDefault();
		if (!selected) return;
		publishing = true;
		try {
			await publishMessage(vhosts.selected, selected, pubKey, pubPayload, pubPersistent);
			notify('success', `Message published to "${selected}"`);
			pubPayload = '';
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to publish the message'));
		} finally {
			publishing = false;
		}
	}
</script>

<div class="page-head">
	<h1>Exchanges</h1>
	<button class="btn btn-primary" onclick={openCreate}>Add exchange</button>
</div>

<div class="table-card">
	<table>
		<thead>
			<tr>
				<th>Vhost</th>
				<th>Name</th>
				<th>Type</th>
				<th>Durable</th>
				<th>Auto Delete</th>
				<th>Internal</th>
				<th>Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each visible as e (e.vhost + e.name)}
				<tr
					class="selectable"
					class:selected={selected === e.name}
					onclick={() => select(e.name)}
					aria-selected={selected === e.name}
				>
					<td>{e.vhost}</td>
					<td><button class="link" onclick={() => select(e.name)}>{e.name}</button></td>
					<td>{e.type}</td>
					<td>{e.durable}</td>
					<td>{e.auto_delete}</td>
					<td>{e.internal}</td>
					<td class="action">
						<button
							class="row-action"
							aria-label="Delete exchange {e.name}"
							title={isBuiltinExchange(e.name)
								? 'Built-in exchanges cannot be deleted'
								: 'Delete exchange'}
							disabled={isBuiltinExchange(e.name)}
							onclick={(ev) => {
								ev.stopPropagation();
								remove(e);
							}}
						>
							<!-- deleteIcon is a static, build-time-bundled asset, not user/API data -->
							<!-- eslint-disable-next-line svelte/no-at-html-tags -->
							{@html deleteIcon}
						</button>
					</td>
				</tr>
			{:else}
				<tr class="empty-row">
					<td colspan="7">
						{exchanges.items === null ? 'Loading…' : `No exchanges in vhost "${vhosts.selected}"`}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

{#if selected}
	<section class="panel">
		<h2>Exchange “{selected}”</h2>

		{#if isDefault}
			<p class="form-hint">
				The default exchange routes each message to the queue named by its routing key. It has no
				bindings to manage.
			</p>
		{:else}
			<h3>Bindings</h3>
			<div class="table-card">
				<table>
					<thead>
						<tr>
							<th>Queue</th>
							<th>Routing key</th>
							<th>Actions</th>
						</tr>
					</thead>
					<tbody>
						{#each bindings as b (b.properties_key + b.destination)}
							<tr>
								<td>{b.destination}</td>
								<td>{b.routing_key}</td>
								<td class="action">
									<button class="btn" onclick={() => unbind(b)}>Unbind</button>
								</td>
							</tr>
						{:else}
							<tr class="empty-row"><td colspan="3">No bindings</td></tr>
						{/each}
					</tbody>
				</table>
			</div>
			<form class="form inline" onsubmit={submitBinding}>
				<label>
					Queue
					<input type="text" bind:value={bindQueue} required />
				</label>
				<label>
					Routing key
					<input type="text" bind:value={bindKey} />
				</label>
				<button type="submit" class="btn btn-primary" disabled={!bindQueue}>Bind</button>
			</form>
		{/if}

		<h3>Publish message</h3>
		<form class="form" onsubmit={submitPublish}>
			<label>
				Routing key
				<input type="text" bind:value={pubKey} />
			</label>
			<label>
				Payload
				<textarea rows="4" bind:value={pubPayload} required></textarea>
			</label>
			<label class="check">
				<input type="checkbox" bind:checked={pubPersistent} /> Persistent (delivery mode 2)
			</label>
			<div class="form-actions">
				<button type="submit" class="btn btn-primary" disabled={publishing || !pubPayload}>
					{publishing ? 'Publishing…' : 'Publish'}
				</button>
			</div>
		</form>
	</section>
{/if}

<Modal open={showCreate} title="Add exchange" onclose={() => (showCreate = false)}>
	<form class="form" onsubmit={submitCreate}>
		{#if createError}<p class="form-error">{createError}</p>{/if}
		<label>
			Name
			<input type="text" bind:value={form.name} required />
		</label>
		<div class="form-row">
			<label>
				Virtual host
				<select bind:value={form.vhost}>
					{#each vhosts.items as v (v.name)}
						<option value={v.name}>{v.name}</option>
					{/each}
				</select>
			</label>
			<label>
				Type
				<select bind:value={form.type}>
					{#each EXCHANGE_TYPES as t (t)}
						<option value={t}>{t}</option>
					{/each}
				</select>
			</label>
		</div>
		<div class="form-row">
			<label class="check"><input type="checkbox" bind:checked={form.durable} /> Durable</label>
			<label class="check"
				><input type="checkbox" bind:checked={form.auto_delete} /> Auto delete</label
			>
		</div>
		<div class="form-actions">
			<button type="button" class="btn" onclick={() => (showCreate = false)}>Cancel</button>
			<button type="submit" class="btn btn-primary" disabled={creating || !form.name}>
				{creating ? 'Creating…' : 'Create'}
			</button>
		</div>
	</form>
</Modal>

<style>
	h3 {
		font-size: 0.95rem;
		font-weight: 600;
		margin: 6px 0 0;
	}
	.panel .table-card {
		margin-top: 0;
	}
	.form.inline {
		flex-direction: row;
		align-items: flex-end;
		flex-wrap: wrap;
	}
	.form.inline label {
		flex: 1;
		min-width: 160px;
	}
</style>
