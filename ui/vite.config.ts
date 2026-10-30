import path from 'node:path'
import { defineConfig } from 'vite'
import react, { reactCompilerPreset } from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'

const target = 'http://127.0.0.1:6459'
export default defineConfig(({ mode }) => ({
  base: '/',
  build: { rolldownOptions: { output: { codeSplitting: { groups: [{ name: 'chart-math', test: /node_modules\/(d3-|victory-vendor)/ }, { name: 'chart-utils', test: /node_modules\/es-toolkit/ }] } } } },
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true }), react(), babel({ presets: [reactCompilerPreset()] }), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src'), ...(mode === 'profile' ? { 'react-dom/client': 'react-dom/profiling' } : {}) } },
  server: { port: 5173, strictPort: true, proxy: Object.fromEntries(['/api', '/mcp', '/login', '/setup', '/register', '/oauth', '/logout', '/previews'].map((p) => [p, { target }])) },
}))
