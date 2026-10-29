import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({
  base: './', plugins: [react()],
  build: { outDir: 'dist-preview', emptyOutDir: true, rolldownOptions: { input: 'local-preview.html', output: { entryFileNames: 'assets/[name]-[hash].js', chunkFileNames: 'assets/[name]-[hash].js', assetFileNames: 'assets/[name]-[hash][extname]' } } },
});
