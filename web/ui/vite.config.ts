import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	server: {
		proxy: {
			// Broker web API for `npm run dev`; override with OTTERMQ_API_URL when the broker
			// listens elsewhere (e.g. OTTERMQ_API_URL=http://localhost:3100 npm run dev).
			'/api': process.env.OTTERMQ_API_URL ?? 'http://localhost:3000'
		}
	},
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// Static single-page app: the broker serves build/ and falls back to index.html for
			// client-side routes such as /queues (see web/server.go).
			adapter: adapter({ fallback: 'index.html' })
		})
	]
});
