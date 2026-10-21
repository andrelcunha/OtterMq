<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import deleteIcon from '$lib/icons/delete.svg?raw';
	import { usePolledList } from '$lib/pooling.svelte';
	import { errorMessage } from '$lib/services/api';
	import { confirmAction, notify } from '$lib/stores/feedback.svelte';
	import type { CreateQueueForm, MessageData, QueueData } from '$lib/stores/queues.svelte';
	import { createQueue, deleteQueue, getMessage, getQueues } from '$lib/stores/queues.svelte';
	import { vhosts } from '$lib/stores/vhosts.svelte';
	import { stateColor } from '$lib/utils';

	const queues = usePolledList(getQueues);

	// Only the selected vhost's queues, like the rest of the UI.
	const visible = $derived((queues.items ?? []).filter((q) => q.vhost === vhosts.selected));

	interface FeatureBadge {
		label: string;
		test: (q: QueueData) => boolean;
	}

	const featureBadges: FeatureBadge[] = [
		{ label: 'D', test: (q) => q.durable },
		{ label: 'AD', test: (q) => q.auto_delete },
		{ label: 'Args', test: (q) => q.arguments !== undefined }
	];

	// --- Selection and "get message" ---

	let selected = $state<string | null>(null);
	let lastMessage = $state<MessageData | null>(null);
	let queueWasEmpty = $state(false);
	let fetching = $state(false);

	function select(name: string) {
		if (selected !== name) {
			lastMessage = null;
			queueWasEmpty = false;
		}
		selected = name;
	}

	// Switching vhost clears a selection that belongs to the previous one.
	$effect(() => {
		void vhosts.selected;
		selected = null;
		lastMessage = null;
	});

	async function fetchMessage() {
		if (!selected) return;
		fetching = true;
		try {
			lastMessage = await getMessage(vhosts.selected, selected);
			queueWasEmpty = lastMessage === null;
			await queues.refresh();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to get a message'));
		} finally {
			fetching = false;
		}
	}

	// --- Create ---

	function emptyForm(): CreateQueueForm {
		return {
			name: '',
			vhost: vhosts.selected,
			durable: true,
			auto_delete: false,
			max_length: null,
			message_ttl: null,
			dead_letter_exchange: '',
			dead_letter_routing_key: ''
		};
	}

	let showCreate = $state(false);
	let form = $state<CreateQueueForm>(emptyForm());
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
			await createQueue(form);
			showCreate = false;
			notify('success', `Queue "${form.name}" created`);
			await queues.refresh();
		} catch (err) {
			createError = errorMessage(err, 'Failed to create the queue');
		} finally {
			creating = false;
		}
	}

	// --- Delete ---

	async function remove(q: QueueData) {
		const ok = await confirmAction({
			title: 'Delete queue',
			message: `Delete queue "${q.name}" in vhost "${q.vhost}"? Its ${q.messages_total} message(s) are lost.`,
			confirmLabel: 'Delete'
		});
		if (!ok) return;
		try {
			await deleteQueue(q.vhost, q.name);
			if (selected === q.name) selected = null;
			notify('success', `Queue "${q.name}" deleted`);
			await queues.refresh();
		} catch (err) {
			notify('error', errorMessage(err, 'Failed to delete the queue'));
		}
	}
</script>

<div class="page-head">
	<h1>Queues</h1>
	<button class="btn btn-primary" onclick={openCreate}>Add queue</button>
</div>

<div class="table-card">
	<table>
		<thead>
			<tr>
				<th>Virtual Host</th>
				<th>Name</th>
				<th>State</th>
				<th>Ready</th>
				<th>Unacked</th>
				<th>Total</th>
				<th>Consumers</th>
				<th>Features</th>
				<th>Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each visible as q (q.vhost + q.name)}
				<tr
					class="selectable"
					class:selected={selected === q.name}
					onclick={() => select(q.name)}
					aria-selected={selected === q.name}
				>
					<td>{q.vhost}</td>
					<td><button class="link" onclick={() => select(q.name)}>{q.name}</button></td>
					<td class="state"
						><span class="small-square small-square--{stateColor(q.state)}"></span> {q.state}</td
					>
					<td class="num">{q.messages_ready}</td>
					<td class="num">{q.messages_unacked}</td>
					<td class="num">{q.messages_total}</td>
					<td class="num">{q.consumers}</td>
					<td class="features">
						<div class="badges">
							{#each featureBadges.filter((b) => b.test(q)) as badge (badge.label)}
								<span class="badge">{badge.label}</span>
							{/each}
						</div>
					</td>
					<td class="action">
						<button
							class="row-action"
							aria-label="Delete queue {q.name}"
							title="Delete queue"
							onclick={(e) => {
								e.stopPropagation();
								remove(q);
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
					<td colspan="9">
						{queues.items === null ? 'Loading…' : `No queues in vhost "${vhosts.selected}"`}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

{#if selected}
	<section class="panel">
		<h2>Queue “{selected}”</h2>
		<div class="get-row">
			<button class="btn btn-primary" onclick={fetchMessage} disabled={fetching}>
				{fetching ? 'Getting…' : 'Get message'}
			</button>
			<span class="form-hint">Takes one message off the queue and acknowledges it.</span>
		</div>
		{#if lastMessage}
			<div class="message">
				<div class="message-meta">
					<span>ID: {lastMessage.id}</span>
					{#if lastMessage.redelivered}<span>redelivered</span>{/if}
				</div>
				<pre>{lastMessage.payload_text ?? ''}</pre>
			</div>
		{:else if queueWasEmpty}
			<p class="form-hint">The queue is empty.</p>
		{/if}
	</section>
{/if}

<Modal open={showCreate} title="Add queue" onclose={() => (showCreate = false)}>
	<form class="form" onsubmit={submitCreate}>
		{#if createError}<p class="form-error">{createError}</p>{/if}
		<label>
			Name
			<input type="text" bind:value={form.name} required />
		</label>
		<label>
			Virtual host
			<select bind:value={form.vhost}>
				{#each vhosts.items as v (v.name)}
					<option value={v.name}>{v.name}</option>
				{/each}
			</select>
		</label>
		<div class="form-row">
			<label class="check"><input type="checkbox" bind:checked={form.durable} /> Durable</label>
			<label class="check"
				><input type="checkbox" bind:checked={form.auto_delete} /> Auto delete</label
			>
		</div>
		<div class="form-row">
			<label>
				Max length
				<input type="number" min="1" bind:value={form.max_length} placeholder="unlimited" />
			</label>
			<label>
				Message TTL (ms)
				<input type="number" min="1" bind:value={form.message_ttl} placeholder="none" />
			</label>
		</div>
		<details>
			<summary>Dead-lettering</summary>
			<div class="form dlx">
				<label>
					Dead-letter exchange
					<input type="text" bind:value={form.dead_letter_exchange} />
					<span class="form-hint">Receives expired, rejected and overflowing messages.</span>
				</label>
				<label>
					Dead-letter routing key
					<input type="text" bind:value={form.dead_letter_routing_key} />
					<span class="form-hint">Defaults to the message's own routing key.</span>
				</label>
			</div>
		</details>
		<div class="form-actions">
			<button type="button" class="btn" onclick={() => (showCreate = false)}>Cancel</button>
			<button type="submit" class="btn btn-primary" disabled={creating || !form.name}>
				{creating ? 'Creating…' : 'Create'}
			</button>
		</div>
	</form>
</Modal>

<style>
	.features {
		text-align: center;
	}
	.features .badges {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 4px;
	}

	.features .badge {
		font-size: 0.62rem;
		font-weight: 500;
		padding: 2px 6px;
		border-radius: 4px;
		background: color-mix(in srgb, var(--color-surface-raised) 14%, transparent);
		opacity: 0.85;
	}

	.get-row {
		display: flex;
		align-items: center;
		gap: 12px;
		flex-wrap: wrap;
	}

	.message {
		border: 1px solid var(--color-border);
		border-radius: 4px;
	}
	.message-meta {
		display: flex;
		gap: 16px;
		padding: 6px 12px;
		font-size: 0.75rem;
		color: var(--color-text-muted);
		border-bottom: 1px solid var(--color-border);
	}
	.message pre {
		margin: 0;
		padding: 12px;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		max-height: 320px;
		overflow-y: auto;
	}

	details summary {
		cursor: pointer;
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--color-text-muted);
	}
	.dlx {
		margin-top: 10px;
	}
</style>
