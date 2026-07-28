<script setup>
import { ref } from 'vue'
import Login from './components/Login.vue'
import Dashboard from './components/Dashboard.vue'
import { setToken } from './api'

// The token is restored from localStorage inside api.js at module load, so there
// is no onMounted race: the first API call after a hard reload is authenticated.
const token = ref(localStorage.getItem('tk_token') || '')

function onLogin(t) {
  token.value = t
  setToken(t) // setToken persists to localStorage + sets the axios header
}
</script>

<template>
  <Login v-if="!token" @logged-in="onLogin" />
  <Dashboard v-else />
</template>
