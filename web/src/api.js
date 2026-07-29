import axios from 'axios'

const api = axios.create({ baseURL: '' })

// Restore the saved token at module load so the very first request after a hard
// reload already carries auth. We can't rely on App.vue's onMounted: child
// components (Dashboard) mount before their parent, so Dashboard's first load()
// would fire before the parent sets the token header.
const _saved = localStorage.getItem('tk_token')
if (_saved) api.defaults.headers.common['Authorization'] = 'Bearer ' + _saved

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

export async function getGhProxy() {
  const { data } = await api.get('/api/gh-proxy')
  return data.gh_proxy
}

export async function setGhProxy(proxy) {
  await api.post('/api/gh-proxy', { gh_proxy: proxy })
}
