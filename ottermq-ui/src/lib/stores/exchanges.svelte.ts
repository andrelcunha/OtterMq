import { api, seg } from '$lib/services/api';
export interface ExchangeData {
	vhost: string;
	name: string;
	type: string;
	durable: boolean;
	auto_delete: boolean;
	internal: boolean;
}

export async function getExchanges(): Promise<ExchangeData[] | null> {
	try {
		const response = await api.get('/api/exchanges');
		const data = await response.json();
		return Array.isArray(data?.exchanges) ? data.exchanges : [];
	} catch (err) {
		console.error('Failed to fetch: ', err);
	}
	return null;
}

/** Name the API uses for the nameless default exchange. */
export const DEFAULT_EXCHANGE = '(AMQP default)';

/** Exchange types the broker accepts (the headers type isn't implemented yet). */
export const EXCHANGE_TYPES = ['direct', 'fanout', 'topic'] as const;

/** The default exchange and the predeclared amq.* exchanges can't be deleted. */
export function isBuiltinExchange(name: string): boolean {
	return name === DEFAULT_EXCHANGE || name.startsWith('amq.');
}

export interface CreateExchangeForm {
	name: string;
	vhost: string;
	type: string;
	durable: boolean;
	auto_delete: boolean;
}

export interface BindingData {
	source: string;
	vhost: string;
	destination: string;
	destination_type: string;
	routing_key: string;
	arguments?: Record<string, unknown>;
	properties_key: string;
}

export async function createExchange(form: CreateExchangeForm): Promise<void> {
	await api.post(`/api/exchanges/${seg(form.vhost)}/${seg(form.name)}`, {
		type: form.type,
		passive: false,
		durable: form.durable,
		auto_delete: form.auto_delete,
		no_wait: false,
		arguments: {}
	});
}

export async function deleteExchange(vhost: string, name: string): Promise<void> {
	await api.delete(`/api/exchanges/${seg(vhost)}/${seg(name)}`);
}

export async function getBindings(vhost: string, exchange: string): Promise<BindingData[]> {
	const response = await api.get(`/api/exchanges/${seg(vhost)}/${seg(exchange)}/bindings/source`);
	const data = await response.json();
	return Array.isArray(data?.bindings) ? data.bindings : [];
}

export async function addBinding(
	vhost: string,
	exchange: string,
	queue: string,
	routingKey: string
): Promise<void> {
	await api.post('/api/bindings', {
		vhost,
		source: exchange,
		destination: queue,
		routing_key: routingKey,
		arguments: {}
	});
}

export async function removeBinding(vhost: string, binding: BindingData): Promise<void> {
	await api.delete('/api/bindings', {
		vhost,
		source: binding.source,
		destination: binding.destination,
		routing_key: binding.routing_key,
		arguments: binding.arguments ?? {}
	});
}

export async function publishMessage(
	vhost: string,
	exchange: string,
	routingKey: string,
	payload: string,
	persistent: boolean
): Promise<void> {
	await api.post(`/api/exchanges/${seg(vhost)}/${seg(exchange)}/publish`, {
		routing_key: routingKey,
		payload,
		content_type: 'text/plain',
		content_encoding: 'utf-8',
		delivery_mode: persistent ? 2 : 1
	});
}
