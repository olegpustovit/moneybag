import path from 'node:path'
import { defineConfig } from 'vitest/config'
import react, { reactCompilerPreset } from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import tailwindcss from '@tailwindcss/vite'
import { FontaineTransform } from 'fontaine'
import { tanstackRouter } from '@tanstack/router-plugin/vite'

// Tooling only: lets a dev server proxy to a seeded local binary. The app itself never reads this.
const target = process.env.MONEYBAG_DEV_TARGET ?? 'http://127.0.0.1:6459'
export default defineConfig(({ mode }) => ({
  base: '/',
  test: { environment: 'jsdom', include: ['tests/*.component.test.tsx'] },
  build: { rolldownOptions: { input: { main: path.resolve(import.meta.dirname, 'index.html'), auth: path.resolve(import.meta.dirname, 'src/auth.css') }, } },
  plugins: [FontaineTransform.vite({ fallbacks: ['Arial'], fallbackName: name => `${name} Fallback`, skipFontFaceGeneration: name => name !== 'Geist Fallback' }), tanstackRouter({ target: 'react', autoCodeSplitting: true }), react(), babel({ presets: [reactCompilerPreset()] }), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src'), ...(mode === 'profile' ? { 'react-dom/client': 'react-dom/profiling' } : {}) } },
  server: { port: 5173, strictPort: true, proxy: Object.fromEntries(['/api', '/mcp', '/login', '/setup', '/register', '/oauth', '/logout', '/settings/invite', '/settings/revoke', '/settings/password', '/settings/agents', '/secure', '/confirm'].map((p) => [p, { target, headers: { origin: target } }])) },
}))
