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
    <div class="login-card">
      <div class="brand">
        <div class="logo">T</div>
        <div>
          <div class="title">Traffic Keeper</div>
          <div class="subtitle">VPS 上行流量保活管控平台</div>
        </div>
      </div>
      <el-input
        v-model="password"
        type="password"
        placeholder="管理密码"
        show-password
        size="large"
        @keyup.enter="submit"
      />
      <el-button type="primary" size="large" :loading="loading" class="submit" @click="submit">
        登录
      </el-button>
    </div>
  </div>
</template>

<style scoped>
.login-wrap {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: var(--tk-bg-soft);
  padding: 24px;
}
.login-card {
  width: 380px;
  background: var(--tk-bg);
  border-radius: var(--tk-radius-lg);
  box-shadow: var(--tk-shadow-md);
  padding: 40px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 28px;
}
.logo {
  width: 40px;
  height: 40px;
  border-radius: var(--tk-radius-btn);
  background: var(--tk-blue);
  color: #fff;
  font-weight: 800;
  font-size: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.title {
  font-size: 20px;
  font-weight: 700;
  color: var(--tk-text-strong);
  letter-spacing: -0.02em;
}
.subtitle {
  font-size: 13px;
  color: var(--tk-text-muted);
  margin-top: 2px;
}
.submit {
  width: 100%;
  margin-top: 16px;
}
</style>
