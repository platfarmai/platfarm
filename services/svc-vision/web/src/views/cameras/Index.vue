<template>
  <router-view />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useCameraStore } from '@/stores'

const cameraStore = useCameraStore()

onMounted(() => {
  cameraStore.fetchCameras()
})
</script>