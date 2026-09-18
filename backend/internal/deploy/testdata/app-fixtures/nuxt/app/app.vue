<script setup lang="ts">
const config = useRuntimeConfig()
</script>
<template>
  <h1>{{ config.public.apiUrl }}</h1>
</template>
