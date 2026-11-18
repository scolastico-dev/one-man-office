// A local protocol stand-in, not a publisher editor. Exercises the real plugin
// launcher, HTTP assets, WebSocket lifetime and an ordinary descendant process.
const http = require('node:http');
const { createHash } = require('node:crypto');
const { spawn } = require('node:child_process');
const args = process.argv.slice(2);
const flag = name => args.includes(name) ? args[args.indexOf(name) + 1] : undefined;
const codeServer = args.includes('--bind-addr');
const port = Number(codeServer ? flag('--bind-addr').split(':')[1] : flag('--port'));
const host = codeServer ? flag('--bind-addr').split(':')[0] : flag('--host');
const folder = codeServer ? args.at(-1) : flag('--default-folder');
// Pinned OpenVSCode strips the explicit flag's trailing slash, but defaults
// to '/' when omitted. An explicit '/' therefore produces an empty base and
// a malformed browser WebSocket URL. Refuse that launch in routine fixtures.
const suppliedBase = flag('--server-base-path');
if (!codeServer && suppliedBase === '/') throw new Error('OpenVSCode root base must omit --server-base-path');
const base = codeServer ? '/' : suppliedBase ?? '/';
if (host !== '127.0.0.1' || (codeServer ? flag('--auth') !== 'none' : !args.includes('--without-connection-token'))) throw new Error('unsafe editor arguments');
const child = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], { stdio: 'inherit' });
console.log('FIXTURE_PID:' + process.pid);
console.log('FIXTURE_CHILD:' + child.pid);
const server = http.createServer((req, res) => {
  if (req.headers.authorization || /omo_proxy_|omo-token\./.test(req.headers.cookie || '')) throw new Error('proxy credential reached editor');
  if (req.url === '/healthz') return res.end('ready');
  if (!req.url.startsWith(base)) { res.statusCode = 404; return res.end('wrong base'); }
  if (req.url.endsWith('asset.js')) {
    res.setHeader('Content-Type', 'text/javascript');
    return res.end(`document.querySelector('#asset').textContent='Asset loaded';
      const ws = new WebSocket(location.origin.replace(/^http/,'ws')+location.pathname+'socket');
      ws.onopen=()=>ws.send('connected');
      ws.onmessage=e=>document.querySelector('#socket').textContent=e.data;
      window.roundTrip=()=>new Promise(resolve=>{ws.addEventListener('message',e=>resolve(e.data),{once:true});ws.send('still connected')});
      window.disconnect=()=>ws.close();`);
  }
  const escape = s => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;');
  res.setHeader('Content-Type', 'text/html');
  res.end('<!doctype html><title>Editor stand-in</title><h1>Editor stand-in</h1><pre id="folder">'+escape(folder)+'</pre><p id="asset"></p><p id="socket"></p><script src="asset.js"></script>');
});
server.on('upgrade', (req, socket) => {
  if (!req.url.startsWith(base) || req.headers.authorization || /omo_proxy_/.test(req.headers.cookie || '')) return socket.destroy();
  const accept = createHash('sha1').update(req.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+accept+'\r\n\r\n');
  socket.on('error', () => {});
  socket.on('data', data => {
    if ((data[0] & 15) === 8) { socket.end(Buffer.from([0x88, 0])); return; }
    const text = Buffer.from('connected');
    socket.write(Buffer.concat([Buffer.from([0x81, text.length]), text]));
  });
  socket.on('end', () => socket.end());
});
server.listen(port, host, () => console.log(codeServer ? 'HTTP server listening on http://127.0.0.1:'+port : 'Server bound to 127.0.0.1:'+port+' (fixture)'));
