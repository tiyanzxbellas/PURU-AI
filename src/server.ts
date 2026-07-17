import { createServer } from 'node:http';

const PORT = 3000;

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

  server.listen(PORT, () => {
    console.log(`Health server running on port ${PORT}`);
  });

  return server;
}
