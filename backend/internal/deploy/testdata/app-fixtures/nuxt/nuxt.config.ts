export default defineNuxtConfig({
  compatibilityDate: "2025-01-01",
  telemetry: false,
  runtimeConfig: { public: { apiUrl: process.env.PUBLIC_API_URL || "" } },
})
