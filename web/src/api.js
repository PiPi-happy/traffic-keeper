import axios from 'axios'

const api = axios.create({ baseURL: '' })

// Restore the saved token at module load so the very first request after a hard
// reload already carries auth. We can't rely on App.vue's onMounted: child
// components (Dashboard) mount before their parent, so Dashboard's first load()
// would fire before the parent sets the token header.
const _saved = localStorage.getItem('tk_token')
if (_saved) api.defaults.headers.common['Authorization'] = 'Bearer ' + _saved

// token 过期/无效（API 返回 401）时自动回登录页，而不是卡住或刷"加载失败"。
// 仅当本机已存 token 时触发——登录请求本身的 401 是密码错误，不跳转。
let redirecting = false
api.interceptors.response.use(
  (resp) => resp,
  (err) => {
    if (err.response && err.response.status === 401 && localStorage.getItem('tk_token') && !redirecting) {
      redirecting = true
      localStorage.removeItem('tk_token')
      location.reload()              // App.vue 见无 token 即显示 Login
      return new Promise(() => {})   // 挂起，阻止调用方 catch 弹"加载失败"
    }
    return Promise.reject(err)
  },
)

export function setToken(token) {
  if (token) {
    api.defaults.headers.common['Authorization'] = 'Bearer ' + token
    localStorage.setItem('tk_token', token)
  } else {
    delete api.defaults.headers.common['Authorization']
    localStorage.removeItem('tk_token')
  }
}

export async function login(password) {
  const { data } = await api.post('/api/login', { password })
  return data.token
}

export async function listNodes() {
  const { data } = await api.get('/api/nodes')
  return data // { nodes, latest_version }
}

export async function getDashboard() {
  const { data } = await api.get('/api/dashboard')
  return data
}

export async function createNode(name) {
  const { data } = await api.post('/api/nodes', { name })
  return data
}

export async function updatePolicy(id, policy) {
  const { data } = await api.put(`/api/nodes/${id}/policy`, policy)
  return data
}

export async function deleteNode(id) {
  await api.delete(`/api/nodes/${id}`)
}

export async function changePassword(oldPw, newPw) {
  await api.post('/api/password', { old: oldPw, new: newPw })
}

export async function getEvents(id) {
  const { data } = await api.get(`/api/nodes/${id}/events`)
  return data.events
}

export async function getInstallCommand(id) {
  const { data } = await api.get(`/api/nodes/${id}/install-command`)
  return data.install_command
}

export async function upgradeNode(id) {
  await api.post(`/api/nodes/${id}/upgrade`)
}

export async function getTunnel() {
  const { data } = await api.get('/api/tunnel')
  return data
}

export async function enableTunnel() {
  await api.post('/api/tunnel')
}

export async function disableTunnel() {
  await api.post('/api/tunnel/disable')
}

export async function testTunnelEdge() {
  await api.post('/api/tunnel/edge/test')
}

export async function applyTunnelEdge(payload) {
  // payload: { ip } and/or { mode: 'off'|'auto'|'manual' }
  const { data } = await api.post('/api/tunnel/edge/apply', payload)
  return data
}

export async function getGhProxy() {
  const { data } = await api.get('/api/gh-proxy')
  return data.gh_proxy
}

export async function setGhProxy(proxy) {
  await api.post('/api/gh-proxy', { gh_proxy: proxy })
}
