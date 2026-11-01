<script setup lang="ts">
defineProps<{ eyebrow?: string; title: string; description?: string }>()
</script>
<template>
  <header class="page-header">
    <div><div v-if="eyebrow" class="eyebrow">{{ eyebrow }}</div><h1>{{ title }}</h1><p v-if="description">{{ description }}</p></div>
    <div class="page-actions"><slot /></div>
  </header>
</template>
