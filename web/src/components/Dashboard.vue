<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listNodes, createNode, updatePolicy, deleteNode,
  changePassword, getEvents, getInstallCommand,
  getTunnel, enableTunnel, disableTunnel,
} from '../api'

const nodes = ref([])
const loading = ref(false)

// new node
const showNew = ref(false)
const newName = ref('')
const created = ref(null)

// policy
const showPolicy = ref(false)
const policyForm = ref(null)

// events
const showEvents = ref(false)
const events = ref([])
const eventsLoading = ref(false)
const eventsNodeName = ref('')

// install command
const showCmd = ref(false)
const installCmd = ref('')

// change password
const showPassword = ref(false)
const pwForm = ref({ old: '', new: '', confirm: '' })

// tunnel
const showTunnel = ref(false)
const tunnel = ref({ enabled: false, url: '', installed: false, logs: [] })
const logBox = ref(null)
let tunnelTimer = null

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
    await ElMessageBox.confirm(`删除节点「${row.name}」？其累计数据将被清除。`, '确认', { type: 'warning' })
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

async function openEvents(row) {
  eventsNodeName.value = row.name
  showEvents.value = true
  eventsLoading.value = true
  try {
    events.value = await getEvents(row.id)
  } catch (e) {
    ElMessage.error('加载日志失败')
  } finally {
    eventsLoading.value = false
  }
}

async function openInstallCmd(row) {
  try {
    installCmd.value = await getInstallCommand(row.id)
    showCmd.value = true
  } catch (e) {
    ElMessage.error('获取安装命令失败')
  }
}

async function savePassword() {
  if (pwForm.value.new.length < 6) {
    ElMessage.warning('新密码至少 6 位')
    return
  }
  if (pwForm.value.new !== pwForm.value.confirm) {
    ElMessage.warning('两次新密码不一致')
    return
  }
  try {
    await changePassword(pwForm.value.old, pwForm.value.new)
    ElMessage.success('密码已修改，下次登录用新密码')
    showPassword.value = false
    pwForm.value = { old: '', new: '', confirm: '' }
  } catch (e) {
    ElMessage.error(e.response?.status === 401 ? '旧密码错误' : '修改失败')
  }
}

async function loadTunnel() {
  try {
    tunnel.value = await getTunnel()
    await nextTick()
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  } catch (e) { /* ignore */ }
}
function openTunnel() {
  showTunnel.value = true
  loadTunnel()
  tunnelTimer = setInterval(loadTunnel, 1000)
}
function closeTunnel() {
  showTunnel.value = false
  if (tunnelTimer) { clearInterval(tunnelTimer); tunnelTimer = null }
}
async function toggleTunnel() {
  try {
    if (tunnel.value.enabled) {
      await disableTunnel()
    } else {
      await enableTunnel()
    }
    loadTunnel()
  } catch (e) {
    ElMessage.error('操作失败')
  }
}
function copyText(text) {
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
onUnmounted(() => {
  clearInterval(timer)
  if (tunnelTimer) clearInterval(tunnelTimer)
})
</script>

<template>
  <div class="dashboard">
    <header class="topbar">
      <span class="title">Traffic Keeper</span>
      <span>
        <el-button text @click="openTunnel">Cloudflare Tunnel</el-button>
        <el-button text @click="showPassword = true">修改密码</el-button>
        <el-button text @click="logout">退出</el-button>
      </span>
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
              <el-tag size="small" :type="row.policy.enabled ? 'success' : 'danger'" style="margin-left: 4px">
                {{ row.policy.enabled ? '启用' : '暂停' }}
              </el-tag>
            </template>
          </template>
        </el-table-column>
        <el-table-column label="最后心跳" width="180">
          <template #default="{ row }">{{ formatTime(row.last_seen_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="320">
          <template #default="{ row }">
            <el-button size="small" @click="openPolicy(row)">策略</el-button>
            <el-button size="small" @click="openEvents(row)">日志</el-button>
            <el-button size="small" @click="openInstallCmd(row)">安装命令</el-button>
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
        <el-input type="textarea" :model-value="created.install_command" :rows="3" readonly />
        <el-button text type="primary" @click="copyText(created.install_command)">复制命令</el-button>
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

    <!-- events dialog -->
    <el-dialog v-model="showEvents" :title="`上传日志（近 3 天）· ${eventsNodeName}`" width="680">
      <el-table :data="events" v-loading="eventsLoading" size="small" max-height="420" empty-text="近 3 天无上传记录">
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ formatTime(row.ts) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'ok' ? 'success' : 'danger'">
              {{ row.status === 'ok' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="大小 / 错误">
          <template #default="{ row }">
            {{ row.status === 'ok' ? formatBytes(row.bytes) : (row.error || '失败') }}
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- install command dialog -->
    <el-dialog v-model="showCmd" title="安装命令" width="600">
      <p class="muted">在目标 VPS 以 root 粘贴执行（可随时在此重新查看）：</p>
      <el-input type="textarea" :model-value="installCmd" :rows="3" readonly />
      <el-button text type="primary" @click="copyText(installCmd)">复制命令</el-button>
    </el-dialog>

    <!-- change password dialog -->
    <el-dialog v-model="showPassword" title="修改密码" width="420">
      <el-form label-width="100px">
        <el-form-item label="旧密码">
          <el-input v-model="pwForm.old" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="pwForm.new" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="pwForm.confirm" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" @click="savePassword">保存</el-button>
        <el-button @click="showPassword = false">取消</el-button>
      </template>
    </el-dialog>

    <!-- cloudflare tunnel dialog -->
    <el-dialog v-model="showTunnel" title="Cloudflare Tunnel" width="680" @close="closeTunnel">
      <div class="tunnel-row">
        <span>状态：</span>
        <el-tag :type="tunnel.enabled ? 'success' : 'info'" size="small">
          {{ tunnel.enabled ? '已启用' : '未启用' }}
        </el-tag>
        <span class="muted" style="margin-left:8px">{{ tunnel.installed ? 'cloudflared 已安装' : 'cloudflared 未安装' }}</span>
        <el-button
          size="small"
          :type="tunnel.enabled ? 'danger' : 'primary'"
          style="margin-left:auto"
          @click="toggleTunnel"
        >
          {{ tunnel.enabled ? '关闭 Tunnel' : '启用 Tunnel' }}
        </el-button>
      </div>
      <div v-if="tunnel.url" class="tunnel-url">
        Tunnel 地址：<a :href="tunnel.url" target="_blank">{{ tunnel.url }}</a>
        <el-button text type="primary" size="small" @click="copyText(tunnel.url)">复制</el-button>
      </div>
      <div v-else-if="tunnel.enabled" class="muted" style="margin-top:8px">
        正在等待分配 trycloudflare 地址……
      </div>
      <p class="muted" style="margin-top:12px">安装 / 连接日志：</p>
      <div ref="logBox" class="tunnel-log">
        <div v-for="(l, i) in tunnel.logs" :key="i" class="log-line">{{ l }}</div>
        <div v-if="!tunnel.logs.length" class="muted">（暂无日志，点「启用 Tunnel」开始）</div>
      </div>
      <p class="muted" style="margin-top:8px;font-size:12px">
        启用后 master 自动安装 cloudflared 并建立 quick tunnel；agent 的上传会自动改走此 tunnel（加密，避开国际链路 RST），
        控制面（心跳/策略）仍走直连。关闭弹窗即停止刷新日志。
      </p>
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
  max-width: 1200px;
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
}
.tunnel-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.tunnel-url {
  margin-top: 10px;
  padding: 8px 10px;
  background: #f0f9eb;
  border-radius: 4px;
  word-break: break-all;
}
.tunnel-url a {
  color: #409eff;
}
.tunnel-log {
  background: #1e1e1e;
  color: #d4d4d4;
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  padding: 10px;
  border-radius: 4px;
  max-height: 240px;
  overflow-y: auto;
}
.log-line {
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
