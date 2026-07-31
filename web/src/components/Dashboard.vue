<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Settings, FileText, Download, Upload, Trash2, Plus, RefreshCw, Lock, Cloud, LogOut, Globe,
  LayoutDashboard, Server,
} from 'lucide-vue-next'
import VChart from 'vue-echarts'
import 'echarts'
import {
  listNodes, createNode, updatePolicy, deleteNode,
  changePassword, getEvents, getInstallCommand, upgradeNode,
  getTunnel, enableTunnel, disableTunnel,
  testTunnelEdge, applyTunnelEdge,
  getGhProxy, setGhProxy, getDashboard,
} from '../api'

const activeView = ref('dashboard') // 'dashboard' | 'nodes'

const nodes = ref([])
const latestVersion = ref('')
const loading = ref(false)

// dashboard aggregate (from /api/dashboard)
const dash = ref(null)
const dashLoading = ref(false)

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

// gh proxy config
const showGhProxy = ref(false)
const ghProxyForm = ref('')

function isOutdated(row) {
  return !!(row.version && latestVersion.value && row.version !== latestVersion.value)
}

async function loadNodes() {
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

async function loadDashboard() {
  dashLoading.value = true
  try {
    dash.value = await getDashboard()
    if (dash.value.latest_version) latestVersion.value = dash.value.latest_version
  } catch (e) {
    ElMessage.error('加载仪表盘失败')
  } finally {
    dashLoading.value = false
  }
}

// manual refresh only (no auto-refresh) — refreshes whichever view is active.
async function load() {
  if (activeView.value === 'dashboard') return loadDashboard()
  return loadNodes()
}

function switchView(v) {
  if (activeView.value === v) return
  activeView.value = v
  load()
}

async function create() {
  if (!newName.value.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  try {
    created.value = await createNode(newName.value.trim())
    newName.value = ''
    loadNodes()
  } catch (e) {
    ElMessage.error('创建失败')
  }
}

function openPolicy(row) {
  const p = row.policy || {}
  const isSizeRandom = p.size_max_mb > p.size_min_mb
  const isIntervalRandom = p.interval_max_sec > p.interval_min_sec
  policyForm.value = {
    id: row.id,
    enabled: p.enabled !== false,
    interval_sec: p.interval_sec || 1800,
    interval_min_sec: p.interval_min_sec || 0,
    interval_max_sec: p.interval_max_sec || 0,
    size_mb: p.size_mb || 50,
    size_min_mb: p.size_min_mb || 0,
    size_max_mb: p.size_max_mb || 0,
    trafficType: isSizeRandom ? 'random' : 'fixed',
    intervalType: isIntervalRandom ? 'random' : 'fixed',
  }
  showPolicy.value = true
}

async function savePolicy() {
  const f = policyForm.value
  if (f.trafficType === 'random' && !(f.size_max_mb > f.size_min_mb)) {
    ElMessage.warning('流量随机区间需 max > min')
    return
  }
  if (f.intervalType === 'random' && !(f.interval_max_sec > f.interval_min_sec)) {
    ElMessage.warning('间隔随机区间需 max > min')
    return
  }
  try {
    await updatePolicy(f.id, {
      enabled: f.enabled,
      interval_sec: f.interval_sec,
      interval_min_sec: f.intervalType === 'fixed' ? 0 : f.interval_min_sec,
      interval_max_sec: f.intervalType === 'fixed' ? 0 : f.interval_max_sec,
      size_mb: f.size_mb,
      size_min_mb: f.trafficType === 'fixed' ? 0 : f.size_min_mb,
      size_max_mb: f.trafficType === 'fixed' ? 0 : f.size_max_mb,
    })
    showPolicy.value = false
    ElMessage.success('已保存')
    loadNodes()
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
    loadNodes()
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
      backgroundColor: 'rgba(15,23,42,0.92)',
      borderWidth: 0,
      textStyle: { color: '#e2e8f0', fontSize: 12 },
      padding: [8, 12],
      formatter: (p) => {
        const i = p[0].dataIndex
        return `${xLabels[i]}<br/>上行：${formatBytes(bytes[i])}<br/>成功 ${okCnt[i]} / 失败 ${failCnt[i]}`
      },
    },
    grid: { left: 56, right: 16, top: 16, bottom: 28 },
    xAxis: {
      type: 'category', data: xLabels,
      axisLine: { lineStyle: { color: '#e2e8f0' } },
      axisTick: { show: false },
      axisLabel: { fontSize: 10, color: '#6b7280' },
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#f1f5f9' } },
      axisLabel: { fontSize: 10, color: '#6b7280', formatter: (v) => formatBytes(v) },
    },
    series: [{
      type: 'line', smooth: true, data: bytes,
      symbol: 'circle', symbolSize: 4,
      areaStyle: { opacity: 0.15 },
      lineStyle: { width: 2, color: '#2563eb' },
      itemStyle: { color: '#2563eb' },
    }],
  }
})

// platform-wide 24h trend (from /api/dashboard hourly buckets)
const dashChart = computed(() => {
  const rows = (dash.value && dash.value.hourly) || []
  const curHour = Math.floor(Date.now() / 1000 / 3600) * 3600
  const map = {}
  for (const r of rows) map[r.hour] = (map[r.hour] || 0) + r.bytes
  const data = []
  const labels = []
  for (let i = 23; i >= 0; i--) {
    data.push(map[curHour - i * 3600] || 0)
    labels.push(i === 0 ? '现在' : `-${i}h`)
  }
  return {
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(15,23,42,0.92)',
      borderWidth: 0,
      textStyle: { color: '#e2e8f0', fontSize: 12 },
      padding: [8, 12],
      formatter: (p) => `${labels[p[0].dataIndex]}<br/>上行：${formatBytes(p[0].data)}`,
    },
    grid: { left: 56, right: 16, top: 16, bottom: 28 },
    xAxis: {
      type: 'category', data: labels,
      axisLine: { lineStyle: { color: '#e2e8f0' } },
      axisTick: { show: false },
      axisLabel: { fontSize: 10, color: '#6b7280' },
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: '#f1f5f9' } },
      axisLabel: { fontSize: 10, color: '#6b7280', formatter: (v) => formatBytes(v) },
    },
    series: [{
      type: 'line', smooth: true, data,
      symbol: 'circle', symbolSize: 4,
      areaStyle: { opacity: 0.15 },
      lineStyle: { width: 2, color: '#2563eb' },
      itemStyle: { color: '#2563eb' },
    }],
  }
})

const successRatePct = computed(() => {
  const d = dash.value
  if (!d) return '—'
  const tot = (d.ok || 0) + (d.fail || 0)
  if (!tot) return '—'
  return (d.ok / tot * 100).toFixed(1) + '%'
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
    loadNodes()
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

async function openGhProxy() {
  try {
    ghProxyForm.value = await getGhProxy()
  } catch (e) { /* ignore */ }
  showGhProxy.value = true
}
async function saveGhProxy() {
  try {
    await setGhProxy(ghProxyForm.value.trim())
    ElMessage.success('已保存')
    showGhProxy.value = false
  } catch (e) {
    ElMessage.error(e.response?.status === 400 ? '地址必须以 http:// 或 https:// 开头' : '保存失败')
  }
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
async function doEdgeTest() {
  try {
    await testTunnelEdge()
    ElMessage.success('已开始测速，结果随轮询刷新')
  } catch (e) {
    ElMessage.error('测速失败')
  }
}
async function applyEdgeIP(ip) {
  try {
    await applyTunnelEdge({ ip })
    ElMessage.success('已应用 ' + ip + '（重启 cloudflared 生效）')
  } catch (e) {
    ElMessage.error('应用失败：' + (e.response?.data || e.message))
  }
}
async function setEdgeMode(mode) {
  try {
    await applyTunnelEdge({ mode })
    ElMessage.success(mode === 'off' ? '已关闭优选' : '已切到手动优选')
  } catch (e) {
    ElMessage.error('设置失败')
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
})
onUnmounted(() => {
  if (tunnelTimer) clearInterval(tunnelTimer)
})
</script>

<template>
  <div class="dashboard">
    <!-- sidebar -->
    <aside class="sidebar">
      <div class="brand">
        <div class="logo">T</div>
        <span class="brand-name">Traffic Keeper</span>
      </div>
      <nav class="nav">
        <div class="nav-item" :class="{ active: activeView === 'dashboard' }" @click="switchView('dashboard')">
          <LayoutDashboard :size="16" :stroke-width="1.5" /><span>仪表盘</span>
        </div>
        <div class="nav-item" :class="{ active: activeView === 'nodes' }" @click="switchView('nodes')">
          <Server :size="16" :stroke-width="1.5" /><span>节点管理</span>
        </div>
      </nav>
      <div class="sidebar-foot">
        <span v-if="latestVersion" class="muted small">最新版本 {{ latestVersion }}</span>
      </div>
    </aside>

    <!-- main -->
    <div class="main">
      <header class="topbar">
        <div class="topbar-title">{{ activeView === 'dashboard' ? '仪表盘' : '节点管理' }}</div>
        <div class="topbar-actions">
          <el-tooltip content="Cloudflare Tunnel" placement="bottom">
            <el-button text :class="{ 'tunnel-on': tunnel.enabled }" @click="openTunnel">
              <Cloud :size="16" :stroke-width="1.5" />
            </el-button>
          </el-tooltip>
          <el-tooltip content="加速源" placement="bottom">
            <el-button text @click="openGhProxy"><Globe :size="16" :stroke-width="1.5" /></el-button>
          </el-tooltip>
          <el-tooltip content="修改密码" placement="bottom">
            <el-button text @click="showPassword = true"><Lock :size="16" :stroke-width="1.5" /></el-button>
          </el-tooltip>
          <el-tooltip content="退出" placement="bottom">
            <el-button text @click="logout"><LogOut :size="16" :stroke-width="1.5" /></el-button>
          </el-tooltip>
        </div>
      </header>

      <main class="content">
        <!-- dashboard view -->
        <div v-if="activeView === 'dashboard'" v-loading="dashLoading">
          <div class="kpi-row">
            <div class="kpi-card">
              <div class="kpi-label">在线节点</div>
              <div class="kpi-value">{{ dash?.online ?? 0 }}<span class="kpi-sub"> / {{ dash?.total ?? 0 }}</span></div>
            </div>
            <div class="kpi-card">
              <div class="kpi-label">累计上行</div>
              <div class="kpi-value">{{ formatBytes(dash?.bytes_up || 0) }}</div>
            </div>
            <div class="kpi-card">
              <div class="kpi-label">上传次数</div>
              <div class="kpi-value">{{ dash?.uploads || 0 }}</div>
            </div>
            <div class="kpi-card">
              <div class="kpi-label">平均速率 (近1h)</div>
              <div class="kpi-value sm">{{ formatBytes(Math.round(dash?.rate_per_sec || 0)) }}/s</div>
            </div>
          </div>

          <div class="dash-card">
            <div class="dash-card-head">
              <div class="dash-card-title">24 小时上行趋势</div>
              <div class="muted small">成功 {{ dash?.ok || 0 }} · 失败 {{ dash?.fail || 0 }} · 成功率 {{ successRatePct }}</div>
            </div>
            <v-chart :option="dashChart" autoresize style="height:280px" />
          </div>

          <div class="dash-card">
            <div class="dash-card-title" style="margin-bottom:12px">各地区节点分布</div>
            <el-table :data="dash?.regions || []" size="small" empty-text="暂无节点">
              <el-table-column label="地区" width="140">
                <template #default="{ row }">{{ row.country === '?' ? '未知' : (row.country || '—') }}</template>
              </el-table-column>
              <el-table-column prop="nodes" label="节点数" width="120" />
              <el-table-column label="累计上行">
                <template #default="{ row }">{{ formatBytes(row.bytes_up) }}</template>
              </el-table-column>
            </el-table>
          </div>
        </div>

        <!-- nodes view -->
        <div v-else>
          <div class="table-toolbar">
            <el-button type="primary" @click="showNew = true">
              <Plus :size="15" :stroke-width="1.5" /><span>新建节点</span>
            </el-button>
            <el-button :loading="loading" @click="load">
              <RefreshCw :size="15" :stroke-width="1.5" /><span>刷新</span>
            </el-button>
          </div>
          <div class="table-card">
            <el-table :data="nodes" v-loading="loading" stripe>
              <el-table-column prop="name" label="名称" min-width="130" />
              <el-table-column label="状态" width="90">
                <template #default="{ row }">
                  <el-tag :type="row.online ? 'success' : 'info'" size="small">
                    {{ row.online ? '在线' : '离线' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="地区" width="80">
                <template #default="{ row }">{{ row.country || '—' }}</template>
              </el-table-column>
              <el-table-column label="版本" width="110">
                <template #default="{ row }">
                  <el-tooltip v-if="isOutdated(row)" :content="`过时，最新 ${latestVersion}`" placement="top">
                    <span class="outdated">{{ row.version || '—' }}</span>
                  </el-tooltip>
                  <span v-else>{{ row.version || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="累计上行" min-width="120">
                <template #default="{ row }">{{ formatBytes(row.bytes_up) }}</template>
              </el-table-column>
              <el-table-column prop="upload_count" label="次数" width="90" />
              <el-table-column label="策略" min-width="230">
                <template #default="{ row }">
                  <template v-if="row.policy">
                    <template v-if="row.policy.interval_max_sec > row.policy.interval_min_sec">
                      {{ row.policy.interval_min_sec }}~{{ row.policy.interval_max_sec }}s(随机)
                    </template>
                    <template v-else>{{ row.policy.interval_sec }}s</template>
                    /
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
              <el-table-column label="操作" width="230">
                <template #default="{ row }">
                  <el-tooltip content="策略" placement="top">
                    <el-button size="small" circle @click="openPolicy(row)"><Settings :size="15" :stroke-width="1.5" /></el-button>
                  </el-tooltip>
                  <el-tooltip content="日志(24h曲线)" placement="top">
                    <el-button size="small" circle @click="openEvents(row)"><FileText :size="15" :stroke-width="1.5" /></el-button>
                  </el-tooltip>
                  <el-tooltip content="安装命令" placement="top">
                    <el-button size="small" circle @click="openInstallCmd(row)"><Download :size="15" :stroke-width="1.5" /></el-button>
                  </el-tooltip>
                  <el-tooltip :content="row.pending_upgrade ? '升级中…' : (isOutdated(row) ? `升级到 ${latestVersion}` : '升级')" placement="top">
                    <el-button size="small" circle :type="isOutdated(row) ? 'warning' : ''" :loading="!!row.pending_upgrade" @click="upgrade(row)">
                      <Upload :size="15" :stroke-width="1.5" />
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="删除" placement="top">
                    <el-button size="small" circle type="danger" @click="remove(row)"><Trash2 :size="15" :stroke-width="1.5" /></el-button>
                  </el-tooltip>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </main>
    </div>

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
      <el-form v-if="policyForm" label-width="96px">
        <el-form-item label="启用上传">
          <el-switch v-model="policyForm.enabled" />
        </el-form-item>
        <el-form-item label="间隔类型">
          <el-segmented
            v-model="policyForm.intervalType"
            :options="[{ label: '固定', value: 'fixed' }, { label: '随机', value: 'random' }]"
          />
        </el-form-item>
        <el-form-item v-if="policyForm.intervalType === 'fixed'" label="固定间隔">
          <el-input-number v-model="policyForm.interval_sec" :min="10" :step="30" /> 秒
        </el-form-item>
        <el-form-item v-else label="随机间隔">
          <el-input-number v-model="policyForm.interval_min_sec" :min="0" :step="30" size="small" />
          <span style="margin: 0 8px">~</span>
          <el-input-number v-model="policyForm.interval_max_sec" :min="0" :step="30" size="small" /> 秒
          <div class="muted small">每次上传后，在 min~max 秒间随机等待</div>
        </el-form-item>
        <el-form-item label="流量类型">
          <el-segmented
            v-model="policyForm.trafficType"
            :options="[{ label: '固定', value: 'fixed' }, { label: '随机', value: 'random' }]"
          />
        </el-form-item>
        <el-form-item v-if="policyForm.trafficType === 'fixed'" label="固定大小">
          <el-input-number v-model="policyForm.size_mb" :min="1" :step="1" /> MB
        </el-form-item>
        <el-form-item v-else label="随机区间">
          <el-input-number v-model="policyForm.size_min_mb" :min="0" :step="1" size="small" />
          <span style="margin: 0 8px">~</span>
          <el-input-number v-model="policyForm.size_max_mb" :min="0" :step="1" size="small" /> MB
          <div class="muted small">每次上传在 min~max 间随机</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" @click="savePolicy">保存</el-button>
        <el-button @click="showPolicy = false">取消</el-button>
      </template>
    </el-dialog>

    <!-- events -->
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

      <!-- 优选 Edge IP -->
      <div style="margin-top:16px; padding-top:12px; border-top:1px solid var(--tk-border)">
        <div style="display:flex; align-items:center; gap:8px; margin-bottom:8px">
          <span style="font-weight:600">优选 Edge IP</span>
          <el-segmented :model-value="tunnel.edge_mode || 'off'" :options="[{ label: '关', value: 'off' }, { label: '手动', value: 'manual' }]" @update:model-value="setEdgeMode" size="small" />
          <el-button size="small" type="primary" :loading="tunnel.probe_running" @click="doEdgeTest" style="margin-left:auto">
            {{ tunnel.probe_running ? '测速中…' : '立即测速' }}
          </el-button>
        </div>
        <div class="muted small">
          当前 edge：<b>{{ tunnel.current_edge || '—' }}</b><span v-if="tunnel.current_latency_ms">（{{ tunnel.current_latency_ms }}ms）</span>
          <span style="margin-left:8px">配置：<b>{{ tunnel.configured_edge || '自动(默认)' }}</b></span>
        </div>
        <el-table v-if="tunnel.probe_results && tunnel.probe_results.length" :data="tunnel.probe_results.slice(0, 10)" size="small" style="margin-top:8px" max-height="220">
          <el-table-column label="IP" prop="ip" min-width="120" />
          <el-table-column label="延迟" width="90"><template #default="{ row }">{{ row.latency_ms ? row.latency_ms + 'ms' : '—' }}</template></el-table-column>
          <el-table-column label="抖动" width="80"><template #default="{ row }">{{ row.jitter_ms ? row.jitter_ms + 'ms' : '—' }}</template></el-table-column>
          <el-table-column label="丢包" width="80">
            <template #default="{ row }">
              <span :style="{ color: row.loss_pct >= 100 ? 'var(--tk-red)' : (row.loss_pct > 0 ? 'var(--tk-orange)' : '') }">{{ row.loss_pct.toFixed(0) }}%</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="80">
            <template #default="{ row }">
              <el-button size="small" link type="primary" :disabled="row.loss_pct >= 100" @click="applyEdgeIP(row.ip)">应用</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-else class="muted small" style="margin-top:8px">点「立即测速」找出对国内最优的 CF edge IP，再「应用」让 cloudflared 走它（应用会重启 cloudflared、换 trycloudflare 地址，agent 会自动跟随）。</div>
        <div v-if="tunnel.edge_history && tunnel.edge_history.length" class="muted small" style="margin-top:8px">
          近期切换：
          <span v-for="(h, i) in tunnel.edge_history.slice(-5).reverse()" :key="i" style="margin-right:6px">{{ h.from || '∅' }}→{{ h.to || '∅' }}</span>
        </div>
      </div>

      <p class="muted" style="margin-top:12px">安装 / 连接日志：</p>
      <div ref="logBox" class="tunnel-log">
        <div v-for="(l, i) in tunnel.logs" :key="i" class="log-line">{{ l }}</div>
        <div v-if="!tunnel.logs.length" class="muted">（暂无日志）</div>
      </div>
    </el-dialog>

    <!-- gh proxy config -->
    <el-dialog v-model="showGhProxy" title="GitHub 加速源" width="500">
      <p class="muted">用于 agent 自升级 / 首次安装时下载 GitHub releases。留空则中国 agent 也直连（可能失败）。</p>
      <el-input v-model="ghProxyForm" placeholder="https://gh-proxy.org" />
      <div class="muted small" style="margin-top:8px">仅对上报地区为 CN 的 agent 生效；海外 agent 始终直连。</div>
      <template #footer>
        <el-button type="primary" @click="saveGhProxy">保存</el-button>
        <el-button @click="showGhProxy = false">取消</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  min-height: 100vh;
  background: var(--tk-bg-soft);
}

/* sidebar */
.sidebar {
  width: 220px;
  background: var(--tk-slate-800);
  color: #cbd5e1;
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}
.sidebar .brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 8px 20px;
}
.sidebar .logo {
  width: 32px;
  height: 32px;
  border-radius: var(--tk-radius-btn);
  background: var(--tk-blue);
  color: #fff;
  font-weight: 800;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.brand-name {
  font-size: 15px;
  font-weight: 600;
  color: #fff;
  letter-spacing: -0.01em;
}
.nav {
  flex: 1;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: var(--tk-radius-btn);
  font-size: 14px;
  color: #94a3b8;
  cursor: pointer;
  transition: all var(--tk-transition);
}
.nav-item:hover {
  background: rgba(255, 255, 255, 0.05);
  color: #cbd5e1;
}
.nav-item.active {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
  font-weight: 500;
}
.sidebar-foot {
  padding: 8px;
}

/* main */
.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.topbar {
  height: 60px;
  background: var(--tk-bg);
  border-bottom: 1px solid var(--tk-border);
  padding: 0 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-shrink: 0;
}
.topbar-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--tk-text-strong);
}
.topbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tunnel-on {
  color: var(--tk-green) !important;
}

/* content — widened (was max-width 1200px) so the table breathes */
.content {
  flex: 1;
  width: 100%;
  max-width: 1600px;
  margin: 0 auto;
  padding: 24px;
}

/* KPI */
.kpi-row {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
  margin-bottom: 20px;
}
.kpi-card {
  background: var(--tk-bg);
  border-radius: var(--tk-radius-card);
  box-shadow: var(--tk-shadow-sm);
  padding: 18px 20px;
}
.kpi-label {
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--tk-text-muted);
  margin-bottom: 8px;
}
.kpi-value {
  font-size: 26px;
  font-weight: 700;
  color: var(--tk-text-strong);
  letter-spacing: -0.02em;
}
.kpi-value.sm {
  font-size: 20px;
}
.kpi-sub {
  font-size: 15px;
  font-weight: 500;
  color: var(--tk-text-muted);
}

/* dashboard cards */
.dash-card {
  background: var(--tk-bg);
  border-radius: var(--tk-radius-card);
  box-shadow: var(--tk-shadow-sm);
  padding: 18px 20px;
  margin-bottom: 20px;
}
.dash-card-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.dash-card-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--tk-text-strong);
}

/* table toolbar (new/refresh moved here from the topbar) */
.table-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
}
.table-toolbar .el-button span {
  margin-left: 4px;
}

/* table card */
.table-card {
  background: var(--tk-bg);
  border-radius: var(--tk-radius-card);
  box-shadow: var(--tk-shadow-sm);
  padding: 8px 4px;
}

.muted {
  color: var(--tk-text-muted);
}
.small {
  font-size: 12px;
}
.outdated {
  color: var(--tk-orange);
  font-weight: 600;
}

/* tunnel */
.tunnel-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.tunnel-url {
  margin-top: 10px;
  padding: 8px 10px;
  background: rgba(34, 197, 94, 0.08);
  border-radius: var(--tk-radius-btn);
  word-break: break-all;
}
.tunnel-url a {
  color: var(--tk-blue);
}
.tunnel-log {
  background: var(--tk-slate-900);
  color: #d4d4d4;
  font-family: var(--tk-mono);
  font-size: 12px;
  line-height: 1.5;
  padding: 10px;
  border-radius: var(--tk-radius-btn);
  max-height: 240px;
  overflow-y: auto;
}
.log-line {
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 768px) {
  .kpi-row {
    grid-template-columns: repeat(2, 1fr);
  }
  .sidebar {
    display: none;
  }
}
</style>
