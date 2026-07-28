<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { login } from '../api'

const emit = defineEmits(['logged-in'])
const password = ref('')
const loading = ref(false)

async function submit() {
  if (!password.value) return
  loading.value = true
  try {
    const t = await login(password.value)
    emit('logged-in', t)
  } catch (e) {
    ElMessage.error('登录失败：' + (e.response?.status === 401 ? '密码错误' : e.message))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <h2>Traffic Keeper</h2>
      <el-input
        v-model="password"
        type="password"
        placeholder="管理密码"
        show-password
        size="large"
        @keyup.enter="submit"
      />
      <el-button
        type="primary"
        size="large"
        :loading="loading"
        style="width: 100%; margin-top: 16px"
        @click="submit"
      >
        登录
      </el-button>
    </el-card>
  </div>
</template>

<style scoped>
.login-wrap {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: #f5f7fa;
}
.login-card {
  width: 360px;
}
.login-card h2 {
  text-align: center;
  margin: 0 0 20px;
}
</style>
