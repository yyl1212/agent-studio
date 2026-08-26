import http from 'node:http'

const server = http.createServer((request, response) => {
  if (request.method === 'GET' && request.url === '/healthz') {
    response.writeHead(200, { 'Content-Type': 'application/json' })
    response.end('{"status":"ok"}')
    return
  }
  if (request.method !== 'POST' || request.url !== '/slow') {
    response.writeHead(404)
    response.end()
    return
  }
  const timer = setTimeout(() => {
    if (response.destroyed) return
    response.writeHead(200, { 'Content-Type': 'application/json' })
    response.end('{"ok":true}')
  }, 30_000)
  const clear = () => clearTimeout(timer)
  request.once('aborted', clear)
  response.once('close', clear)
})

server.listen(8090, '127.0.0.1')

const shutdown = () => server.close(() => process.exit(0))
process.once('SIGTERM', shutdown)
process.once('SIGINT', shutdown)
