export function usePolledList<T>(fetcher: () => Promise<T[] | null>, intervalMs = 5000) {
	let items = $state<T[] | null>(null);

	async function load() {
		items = await fetcher();
	}

	$effect(() => {
		load();
		const interval = setInterval(load, intervalMs);
		return () => clearInterval(interval);
	});

	return {
		get items() {
			return items;
		},
		/** Fetch now, e.g. right after a create or delete, instead of waiting for the next poll. */
		refresh: load
	};
}
