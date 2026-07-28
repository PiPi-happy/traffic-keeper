<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { listNodes, createNode, updatePolicy, deleteNode } from '../api'

const nodes = ref([])
const loading = ref(false)

const showNew = ref(false)
const newName = ref('')
const created = ref(null)

const showPolicy = ref(false)
const policyForm = ref(null)

let timer = null

async function load() {
  loading.value = true
  try {
    nodes.value = await listNodes()
  } catch (e) {
    ElMessage.error('加载失败')
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!newName.value.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  try {
    created.value = await createNode(newName.value.trim())
    newName.value = ''
    load()
  } catch (e) {
    ElMessage.error('创建失败')
  }
}

function openPolicy(row) {
  const p = row.policy || { enabled: true, interval_sec: 1800, size_mb: 50 }
  policyForm.value = { id: row.id, enabled: p.enabled, interval_sec: p.interval_sec, size_mb: p.size_mb }
  showPolicy.value = true
}

async function savePolicy() {
  const f = policyForm.value
  try {
    await updatePolicy(f.id, {
      enabled: f.enabled,
      interval_sec: f.interval_sec,
      size_mb: f.size_mb,
    })
    showPolicy.value = false
    ElMessage.success('已保存')
    load()
  } catch (e) {
    ElMessage.error('保存失败：' + (e.response?.data || e.message))
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`删除节点「${row.name}」？其累计数据将被清除。`, '确认', {
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteNode(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e) {
    ElMessage.error('删除失败')
  }
}

function copy(text) {
  navigator.clipboard.writeText(text)
  ElMessage.success('已复制到剪贴板')
}

function logout() {
  localStorage.removeItem('tk_token')
  location.reload()
}

function formatBytes(b) {
  if (!b) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(b) / Math.log(1024))
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i]
}

function formatTime(t) {
  return t ? new Date(t * 1000).toLocaleString() : '—'
}

onMounted(() => {
  load()
  timer = setInterval(load, 15000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="dashboard">
    <header class="topbar">
      <span class="title">Traffic Keeper</span>
      <el-button text @click="logout">退出</el-button>
    </header>

    <main class="content">
      <div class="toolbar">
        <el-button type="primary" @click="showNew = true">新建节点</el-button>
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>

      <el-table :data="nodes" v-loading="loading" stripe>
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'" size="small">
              {{ row.online ? '在线' : '离线' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="累计上行" min-width="110">
          <template #default="{ row }">{{ formatBytes(row.bytes_up) }}</template>
        </el-table-column>
        <el-table-column prop="upload_count" label="次数" width="80" />
        <el-table-column label="策略" min-width="170">
          <template #default="{ row }">
            <template v-if="row.policy">
              {{ row.policy.interval_sec }}s / {{ row.policy.size_mb }}MB
              <el-tag
                size="small"
                :type="row.policy.enabled ? 'success' : 'danger'"
                style="margin-left: 4px"
              >
                {{ row.policy.enabled ? '启用' : '暂停' }}
              </el-tag>
            </template>
          </template>
        </el-table-column>
        <el-table-column label="最后心跳" width="180">
          <template #default="{ row }">{{ formatTime(row.last_seen_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button size="small" @click="openPolicy(row)">策略</el-button>
            <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </main>

    <!-- new node dialog -->
    <el-dialog v-model="showNew" title="新建节点" width="600" @close="created = null">
      <el-form v-if="!created">
        <el-form-item label="名称">
          <el-input v-model="newName" placeholder="如 vps-hk-01" @keyup.enter="create" />
        </el-form-item>
      </el-form>
      <div v-else>
        <p class="muted">安装命令 —— 在目标 VPS 上以 root 粘贴执行即可：</p>
        <el-input
          type="textarea"
          :model-value="created.install_command"
          :rows="3"
          readonly
        />
        <el-button text type="primary" @click="copy(created.install_command)">
          复制命令
        </el-button>
      </div>
      <template #footer>
        <el-button v-if="!created" type="primary" @click="create">生成命令</el-button>
        <el-button @click="showNew = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- policy dialog -->
    <el-dialog v-model="showPolicy" title="编辑策略" width="460">
      <el-form v-if="policyForm" label-width="100px">
        <el-form-item label="启用上传">
          <el-switch v-model="policyForm.enabled" />
        </el-form-item>
        <el-form-item label="间隔 (秒)">
          <el-input-number v-model="policyForm.interval_sec" :min="10" :step="30" />
        </el-form-item>
        <el-form-item label="包大小 (MB)">
          <el-input-number v-model="policyForm.size_mb" :min="1" :step="10" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" @click="savePolicy">保存</el-button>
        <el-button @click="showPolicy = false">取消</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.dashboard {
  min-height: 100vh;
  background: #f5f7fa;
}
.topbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  height: 56px;
  padding: 0 20px;
  background: #fff;
  border-bottom: 1px solid #eee;
}
.title {
  font-weight: 600;
}
.content {
  max-width: 1100px;
  margin: 0 auto;
  padding: 20px;
}
.toolbar {
  margin-bottom: 12px;
  display: flex;
  gap: 8px;
}
.muted {
  color: #909399;
  margin: 0 0 8px;
}
</style>
