import axios from 'axios'

const api = axios.create({ baseURL: '' })

export function setToken(token) {
  if (token) api.defaults.headers.common['Authorization'] = 'Bearer ' + token
  else delete api.defaults.headers.common['Authorization']
}

export async function login(password) {
  const { data } = await api.post('/api/login', { password })
  return data.token
}

export async function listNodes() {
  const { data } = await api.get('/api/nodes')
  return data.nodes
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
