<script setup lang="ts">
defineProps<{ message: string }>()
defineEmits<{ retry: [] }>()
</script>
<template>
  <el-alert v-if="message" :closable="false" type="error" show-icon class="error-notice">
    <template #title>{{ message }}</template>
    <template #default><el-button link type="danger" @click="$emit('retry')">重新加载</el-button></template>
  </el-alert>
</template>
