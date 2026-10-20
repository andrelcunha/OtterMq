// User feedback shared by every page: toasts for the result of an action, and a
// promise-based confirmation dialog for destructive ones. Both are rendered once,
// by <Feedback /> in the app layout.

export type ToastKind = 'success' | 'error';

export interface Toast {
	id: number;
	kind: ToastKind;
	message: string;
}

export const toasts = $state<Toast[]>([]);

let nextId = 1;

export function notify(kind: ToastKind, message: string, timeoutMs = 4000) {
	const id = nextId++;
	toasts.push({ id, kind, message });
	setTimeout(() => dismiss(id), timeoutMs);
}

export function dismiss(id: number) {
	const i = toasts.findIndex((t) => t.id === id);
	if (i !== -1) toasts.splice(i, 1);
}

export interface ConfirmRequest {
	title: string;
	message: string;
	confirmLabel?: string;
}

export const confirmState = $state<{
	request: ConfirmRequest | null;
	resolve: ((ok: boolean) => void) | null;
}>({ request: null, resolve: null });

/** Shows the confirmation dialog and resolves to true if the user confirms. */
export function confirmAction(request: ConfirmRequest): Promise<boolean> {
	confirmState.resolve?.(false);
	return new Promise((resolve) => {
		confirmState.request = request;
		confirmState.resolve = resolve;
	});
}

export function answerConfirm(ok: boolean) {
	confirmState.resolve?.(ok);
	confirmState.request = null;
	confirmState.resolve = null;
}
