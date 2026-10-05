import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			// Dev: forward API and WebSocket traffic to the panel.
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: true,
				ws: true
			}
		}
	}
});
