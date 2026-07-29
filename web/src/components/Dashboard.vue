<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Setting, Document, Download, Upload, Delete, Plus, Refresh, Lock, Cloudy, SwitchButton,
} from '@element-plus/icons-vue'
import VChart from 'vue-echarts'
import 'echarts'
import {
  listNodes, createNode, updatePolicy, deleteNode,
  changePassword, getEvents, getInstallCommand, upgradeNode,
  getTunnel, enableTunnel, disableTunnel,
} from '../api'

const nodes = ref([])
const latestVersion = ref('')
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

// password
const showPassword = ref(false)
const pwForm = ref({ old: '', new: '', confirm: '' })

// tunnel
const showTunnel = ref(false)
const tunnel = ref({ enabled: false, url: '', installed: false, logs: [] })
const logBox = ref(null)
let tunnelTimer = null

let timer = null

function isOutdated(row) {
  return !!(row.version && latestVersion.value && row.version !== latestVersion.value)
}

async function load() {
  loading.value = true
  try {
    const d = await listNodes()
    nodes.value = d.nodes || []
    latestVersion.value = d.latest_version || ''
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
  const p = row.policy || {}
  policyForm.value = {
    id: row.id,
    enabled: p.enabled !== false,
    interval_sec: p.interval_sec || 1800,
    size_mb: p.size_mb || 50,
    size_min_mb: p.size_min_mb || 0,
    size_max_mb: p.size_max_mb || 0,
  }
  showPolicy.value = true
}

async function savePolicy() {
  const f = policyForm.value
  if (f.size_max_mb > 0 && f.size_max_mb < f.size_min_mb) {
    ElMessage.warning('随机区间 max 必须 ≥ min')
    return
  }
  try {
    await updatePolicy(f.id, {
      enabled: f.enabled,
      interval_sec: f.interval_sec,
      size_mb: f.size_mb,
      size_min_mb: f.size_min_mb,
      size_max_mb: f.size_max_mb,
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

// 24h hourly aggregation for the chart
const chartOption = computed(() => {
  const now = Math.floor(Date.now() / 1000)
  const bytes = Array(24).fill(0)
  const okCnt = Array(24).fill(0)
  const failCnt = Array(24).fill(0)
  for (const e of events.value) {
    const ago = Math.floor((now - e.ts) / 3600)
    if (ago >= 0 && ago < 24) {
      const idx = 23 - ago
      bytes[idx] += e.bytes
      if (e.status === 'ok') okCnt[idx]++
      else failCnt[idx]++
    }
  }
  const xLabels = bytes.map((_, i) => (i === 23 ? '现在' : `-${23 - i}h`))
  return {
    tooltip: {
      trigger: 'axis',
      formatter: (p) => {
        const i = p[0].dataIndex
        return `${xLabels[i]}<br/>上行：${formatBytes(bytes[i])}<br/>成功 ${okCnt[i]} / 失败 ${failCnt[i]}`
      },
    },
    grid: { left: 56, right: 16, top: 16, bottom: 28 },
    xAxis: { type: 'category', data: xLabels, axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', axisLabel: { formatter: (v) => formatBytes(v), fontSize: 10 } },
    series: [{
      type: 'line', smooth: true, data: bytes,
      areaStyle: { opacity: 0.2 }, lineStyle: { width: 2 }, itemStyle: { color: '#409eff' },
    }],
  }
})

async function openInstallCmd(row) {
  try {
    installCmd.value = await getInstallCommand(row.id)
    showCmd.value = true
  } catch (e) {
    ElMessage.error('获取安装命令失败')
  }
}

async function upgrade(row) {
  try {
    await ElMessageBox.confirm(
      `升级节点「${row.name}」到 ${latestVersion.value}？agent 下次心跳后自动下载替换并重启。`,
      '确认升级',
      { type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await upgradeNode(row.id)
    ElMessage.success('已下发升级指令')
    load()
  } catch (e) {
    ElMessage.error('下发失败')
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
    if (tunnel.value.enabled) await disableTunnel()
    else await enableTunnel()
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
function formatDuration(ms) {
  if (!ms) return '—'
  if (ms < 1000) return ms + ' ms'
  return (ms / 1000).toFixed(1) + ' s'
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
      <span class="actions">
        <el-tooltip content="Cloudflare Tunnel" placement="bottom">
          <el-button text :class="{ active: tunnel.enabled }" @click="openTunnel"><el-icon><Cloudy /></el-icon></el-button>
        </el-tooltip>
        <el-tooltip content="修改密码" placement="bottom">
          <el-button text @click="showPassword = true"><el-icon><Lock /></el-icon></el-button>
        </el-tooltip>
        <el-tooltip content="退出" placement="bottom">
          <el-button text @click="logout"><el-icon><SwitchButton /></el-icon></el-button>
        </el-tooltip>
      </span>
    </header>

    <main class="content">
      <div class="toolbar">
        <el-button type="primary" @click="showNew = true"><el-icon><Plus /></el-icon><span>新建节点</span></el-button>
        <el-button :loading="loading" @click="load"><el-icon><Refresh /></el-icon><span>刷新</span></el-button>
        <span v-if="latestVersion" class="muted small">最新版本 {{ latestVersion }}</span>
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
        <el-table-column label="版本" width="100">
          <template #default="{ row }">
            <el-tooltip v-if="isOutdated(row)" :content="`过时，最新 ${latestVersion}`" placement="top">
              <span class="outdated">{{ row.version || '—' }}</span>
            </el-tooltip>
            <span v-else>{{ row.version || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="累计上行" min-width="110">
          <template #default="{ row }">{{ formatBytes(row.bytes_up) }}</template>
        </el-table-column>
        <el-table-column prop="upload_count" label="次数" width="80" />
        <el-table-column label="策略" min-width="190">
          <template #default="{ row }">
            <template v-if="row.policy">
              {{ row.policy.interval_sec }}s /
              <template v-if="row.policy.size_max_mb > row.policy.size_min_mb">
                {{ row.policy.size_min_mb }}~{{ row.policy.size_max_mb }}MB(随机)
              </template>
              <template v-else>{{ row.policy.size_mb }}MB</template>
              <el-tag size="small" :type="row.policy.enabled ? 'success' : 'danger'" style="margin-left:4px">
                {{ row.policy.enabled ? '启用' : '暂停' }}
              </el-tag>
            </template>
          </template>
        </el-table-column>
        <el-table-column label="最后心跳" width="170">
          <template #default="{ row }">{{ formatTime(row.last_seen_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="240">
          <template #default="{ row }">
            <el-tooltip content="策略" placement="top">
              <el-button size="small" circle @click="openPolicy(row)"><el-icon><Setting /></el-icon></el-button>
            </el-tooltip>
            <el-tooltip content="日志(24h曲线)" placement="top">
              <el-button size="small" circle @click="openEvents(row)"><el-icon><Document /></el-icon></el-button>
            </el-tooltip>
            <el-tooltip content="安装命令" placement="top">
              <el-button size="small" circle @click="openInstallCmd(row)"><el-icon><Download /></el-icon></el-button>
            </el-tooltip>
            <el-tooltip :content="row.pending_upgrade ? '升级中…' : (isOutdated(row) ? `升级到 ${latestVersion}` : '升级')" placement="top">
              <el-button size="small" circle :type="isOutdated(row) ? 'warning' : ''" :loading="!!row.pending_upgrade" @click="upgrade(row)"><el-icon><Upload /></el-icon></el-button>
            </el-tooltip>
            <el-tooltip content="删除" placement="top">
              <el-button size="small" circle type="danger" @click="remove(row)"><el-icon><Delete /></el-icon></el-button>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>
    </main>

    <!-- new node -->
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

    <!-- policy -->
    <el-dialog v-model="showPolicy" title="编辑策略" width="480">
      <el-form v-if="policyForm" label-width="100px">
        <el-form-item label="启用上传">
          <el-switch v-model="policyForm.enabled" />
        </el-form-item>
        <el-form-item label="间隔">
          <el-input-number v-model="policyForm.interval_sec" :min="10" :step="30" /> 秒
        </el-form-item>
        <el-form-item label="固定大小">
          <el-input-number v-model="policyForm.size_mb" :min="1" :step="1" /> MB
        </el-form-item>
        <el-form-item label="随机区间">
          <el-input-number v-model="policyForm.size_min_mb" :min="0" :step="1" size="small" />
          ~
          <el-input-number v-model="policyForm.size_max_mb" :min="0" :step="1" size="small" /> MB
          <div class="muted small">max &gt; min 时每次随机；否则用上面的固定大小</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" @click="savePolicy">保存</el-button>
        <el-button @click="showPolicy = false">取消</el-button>
      </template>
    </el-dialog>

    <!-- events: chart + detail -->
    <el-dialog v-model="showEvents" :title="`上传日志（近 24 小时）· ${eventsNodeName}`" width="820" @close="events = []">
      <div v-loading="eventsLoading">
        <v-chart v-if="events.length" :option="chartOption" autoresize style="height:240px" />
        <el-table :data="events" size="small" max-height="280" empty-text="近 24 小时无上传记录" style="margin-top:8px">
          <el-table-column label="时间" width="170">
            <template #default="{ row }">{{ formatTime(row.ts) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag size="small" :type="row.status === 'ok' ? 'success' : 'danger'">
                {{ row.status === 'ok' ? '成功' : '失败' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="大小">
            <template #default="{ row }">
              {{ row.status === 'ok' ? formatBytes(row.bytes) : (row.error || '失败') }}
            </template>
          </el-table-column>
          <el-table-column label="用时" width="100">
            <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
          </el-table-column>
        </el-table>
      </div>
    </el-dialog>

    <!-- install command -->
    <el-dialog v-model="showCmd" title="安装命令" width="600">
      <p class="muted">在目标 VPS 以 root 粘贴执行（可随时在此重新查看）：</p>
      <el-input type="textarea" :model-value="installCmd" :rows="3" readonly />
      <el-button text type="primary" @click="copyText(installCmd)">复制命令</el-button>
    </el-dialog>

    <!-- password -->
    <el-dialog v-model="showPassword" title="修改密码" width="420">
      <el-form label-width="100px">
        <el-form-item label="旧密码"><el-input v-model="pwForm.old" type="password" show-password /></el-form-item>
        <el-form-item label="新密码"><el-input v-model="pwForm.new" type="password" show-password placeholder="至少 6 位" /></el-form-item>
        <el-form-item label="确认新密码"><el-input v-model="pwForm.confirm" type="password" show-password /></el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" @click="savePassword">保存</el-button>
        <el-button @click="showPassword = false">取消</el-button>
      </template>
    </el-dialog>

    <!-- tunnel -->
    <el-dialog v-model="showTunnel" title="Cloudflare Tunnel" width="680" @close="closeTunnel">
      <div class="tunnel-row">
        <span>状态：</span>
        <el-tag :type="tunnel.enabled ? 'success' : 'info'" size="small">{{ tunnel.enabled ? '已启用' : '未启用' }}</el-tag>
        <span class="muted" style="margin-left:8px">{{ tunnel.installed ? 'cloudflared 已安装' : 'cloudflared 未安装' }}</span>
        <el-button size="small" :type="tunnel.enabled ? 'danger' : 'primary'" style="margin-left:auto" @click="toggleTunnel">
          {{ tunnel.enabled ? '关闭 Tunnel' : '启用 Tunnel' }}
        </el-button>
      </div>
      <div v-if="tunnel.url" class="tunnel-url">
        Tunnel 地址：<a :href="tunnel.url" target="_blank">{{ tunnel.url }}</a>
        <el-button text type="primary" size="small" @click="copyText(tunnel.url)">复制</el-button>
      </div>
      <div v-else-if="tunnel.enabled" class="muted" style="margin-top:8px">正在等待分配 trycloudflare 地址……</div>
      <p class="muted" style="margin-top:12px">安装 / 连接日志：</p>
      <div ref="logBox" class="tunnel-log">
        <div v-for="(l, i) in tunnel.logs" :key="i" class="log-line">{{ l }}</div>
        <div v-if="!tunnel.logs.length" class="muted">（暂无日志）</div>
      </div>
    </el-dialog>
  </div>
</template>

<style scoped>
.dashboard { min-height: 100vh; background: #f5f7fa; }
.topbar {
  display: flex; justify-content: space-between; align-items: center;
  height: 56px; padding: 0 20px; background: #fff; border-bottom: 1px solid #eee;
}
.title { font-weight: 600; }
.actions .el-button.active { color: #67c23a; }
.content { max-width: 1280px; margin: 0 auto; padding: 20px; }
.toolbar { margin-bottom: 12px; display: flex; gap: 8px; align-items: center; }
.toolbar .el-button span { margin-left: 4px; }
.muted { color: #909399; }
.small { font-size: 12px; }
.outdated { color: #e6a23c; font-weight: 600; }
.tunnel-row { display: flex; align-items: center; gap: 4px; }
.tunnel-url { margin-top: 10px; padding: 8px 10px; background: #f0f9eb; border-radius: 4px; word-break: break-all; }
.tunnel-url a { color: #409eff; }
.tunnel-log {
  background: #1e1e1e; color: #d4d4d4; font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px; line-height: 1.5; padding: 10px; border-radius: 4px; max-height: 240px; overflow-y: auto;
}
.log-line { white-space: pre-wrap; word-break: break-all; }
</style>
