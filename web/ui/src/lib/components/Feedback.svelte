<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { answerConfirm, confirmState, dismiss, toasts } from '$lib/stores/feedback.svelte';
</script>

<Modal
	open={confirmState.request !== null}
	title={confirmState.request?.title}
	onclose={() => answerConfirm(false)}
>
	<p class="confirm-message">{confirmState.request?.message}</p>
	<div class="form-actions">
		<button class="btn" onclick={() => answerConfirm(false)}>Cancel</button>
		<button class="btn btn-danger" onclick={() => answerConfirm(true)}>
			{confirmState.request?.confirmLabel ?? 'Confirm'}
		</button>
	</div>
</Modal>

<div class="toasts" aria-live="polite">
	{#each toasts as t (t.id)}
		<div class="toast toast--{t.kind}" role={t.kind === 'error' ? 'alert' : 'status'}>
			<span>{t.message}</span>
			<button class="toast-close" onclick={() => dismiss(t.id)} aria-label="Dismiss">✕</button>
		</div>
	{/each}
</div>

<style>
	.confirm-message {
		margin: 0 0 16px;
		overflow-wrap: anywhere;
	}

	.toasts {
		position: fixed;
		right: 16px;
		bottom: 16px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		z-index: 200;
		max-width: min(420px, calc(100vw - 32px));
	}

	.toast {
		display: flex;
		align-items: flex-start;
		gap: 12px;
		padding: 10px 14px;
		border-radius: 6px;
		font-size: 0.85rem;
		color: var(--color-accent-contrast);
		box-shadow: 0 8px 24px color-mix(in srgb, black 20%, transparent);
	}
	.toast span {
		flex: 1;
		overflow-wrap: anywhere;
	}
	.toast--success {
		background: #2e7d32;
	}
	.toast--error {
		background: #c10015;
	}

	.toast-close {
		background: transparent;
		border: none;
		color: inherit;
		cursor: pointer;
		opacity: 0.8;
		padding: 0;
	}
</style>
