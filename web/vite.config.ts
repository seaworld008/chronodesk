import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET ?? 'http://localhost:8081',
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (/[\\/]node_modules[\\/](react|react-dom|react-router|react-router-dom)[\\/]/.test(id)) {
            return 'vendor'
          }
          if (id.includes('/node_modules/@mui/')) {
            return 'ui'
          }
          if (
            id.includes('/node_modules/react-admin/') ||
            id.includes('/node_modules/ra-')
          ) {
            return 'admin'
          }
        },
      },
    },
    chunkSizeWarningLimit: 1000,
  },
})
