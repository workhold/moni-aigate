import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue(), { name: 'retain-embed-placeholder', generateBundle() { this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' }) } }],
  server: { port: 5173, strictPort: true, proxy: { '/api': 'http://127.0.0.1:8080', '/v1': 'http://127.0.0.1:8080', '/health': 'http://127.0.0.1:8080' } },
  build: { target: 'es2022', sourcemap: false, chunkSizeWarningLimit: 1100,
    rollupOptions: { output: { manualChunks: { vue: ['vue', 'vue-router'], elements: ['element-plus'] } } }
  },
})
