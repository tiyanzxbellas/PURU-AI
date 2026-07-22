import { createServer } from 'node:http';
import { config } from './config.js';

export function startHealthServer() {
  const server = createServer((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      status: 'ok',
      bot: 'PURU-AI',
      running: true,
      timestamp: new Date().toISOString(),
    }));
  });

  server.listen(config.port, config.hostname, () => {
    console.log(`Health server running on ${config.hostname}:${config.port}`);
  });

  return server;
}
