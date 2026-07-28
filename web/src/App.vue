<script setup>
import { ref, onMounted } from 'vue'
import Login from './components/Login.vue'
import Dashboard from './components/Dashboard.vue'
import { setToken } from './api'

const token = ref(localStorage.getItem('tk_token') || '')
onMounted(() => {
  if (token.value) setToken(token.value)
})

function onLogin(t) {
  token.value = t
  localStorage.setItem('tk_token', t)
  setToken(t)
}
</script>

<template>
  <Login v-if="!token" @logged-in="onLogin" />
  <Dashboard v-else />
</template>
