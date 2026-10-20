import { api, ApiError, seg } from '$lib/services/api';
export interface QueueData {
	vhost: string;
	name: string;

	messages: number;
	messages_ready: number;
	messages_unacked: number;
	messages_persistent: number;
	messages_total: number;

	consumers: number;

	durable: boolean;
	auto_delete: boolean;
	exclusive: boolean;
	arguments?: Record<string, unknown>;

	dead_letter_exchange?: string;
	dead_letter_routing_key?: string;

	message_ttl?: number;
	max_length?: number; // AKA QLL

	max_priority?: number;

	state: string;

	owner_connection: string;
	persistence_enabled: boolean;
}

export async function getQueues(): Promise<QueueData[] | null> {
	try {
		const response = await api.get('/api/queues');
		const data = await response.json();
		return Array.isArray(data?.queues) ? data.queues : [];
	} catch (err) {
		console.error('Failed to fetch: ', err);
	}
	return null;
}

export interface CreateQueueForm {
	name: string;
	vhost: string;
	durable: boolean;
	auto_delete: boolean;
	max_length: number | null;
	message_ttl: number | null;
	dead_letter_exchange: string;
	dead_letter_routing_key: string;
}

/** Message returned by "get messages" (models.MessageDTO). */
export interface MessageData {
	id: string;
	payload_text?: string;
	properties: Record<string, unknown>;
	delivery_tag: number;
	redelivered: boolean;
}

export async function createQueue(form: CreateQueueForm): Promise<void> {
	// Optional settings are only sent when set, matching models.CreateQueueRequest.
	const body: Record<string, unknown> = {
		passive: false,
		durable: form.durable,
		auto_delete: form.auto_delete,
		arguments: {}
	};
	if (form.max_length && form.max_length > 0) body.max_length = form.max_length;
	if (form.message_ttl && form.message_ttl > 0) body.message_ttl = form.message_ttl;
	if (form.dead_letter_exchange) body['x-dead-letter-exchange'] = form.dead_letter_exchange;
	if (form.dead_letter_routing_key)
		body['x-dead-letter-routing-key'] = form.dead_letter_routing_key;

	await api.post(`/api/queues/${seg(form.vhost)}/${seg(form.name)}`, body);
}

export async function deleteQueue(vhost: string, name: string): Promise<void> {
	await api.delete(`/api/queues/${seg(vhost)}/${seg(name)}`);
}

/** Takes one message off the queue and acknowledges it. Resolves to null when the queue is empty. */
export async function getMessage(vhost: string, name: string): Promise<MessageData | null> {
	let response: Response;
	try {
		response = await api.post(`/api/queues/${seg(vhost)}/${seg(name)}/get`, {
			ack_mode: 'ack',
			message_count: 1
		});
	} catch (err) {
		// The API answers an empty queue with 404 "No messages in queue".
		if (err instanceof ApiError && err.status === 404) return null;
		throw err;
	}
	const data = await response.json();
	return Array.isArray(data?.messages) && data.messages.length > 0 ? data.messages[0] : null;
}
