import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { builtinCatalogPlugin } from './builtin-catalog-plugin.ts'

// https://vite.dev/config/
export default defineConfig({
  plugins: [builtinCatalogPlugin(), react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
})
