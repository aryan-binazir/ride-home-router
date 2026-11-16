const http = require('http');
const zlib = require('zlib');
const fs = require('fs');
const ready = JSON.parse(fs.readFileSync(process.env.DEV_READY, 'utf8'));
const up = new URL(ready.url);
const LISTEN_HOST = process.env.LISTEN_HOST || '127.0.0.8';
const port = +up.port + (+process.env.PORT_OFFSET || 0);
const mine = `http://${LISTEN_HOST}:${port}`;
const MODE = process.env.STATIC_CC || 'cf';
const CC = MODE === 'immutable' ? 'public, max-age=31536000, immutable' : MODE === 'revalidate' ? 'max-age=0' : 'max-age=14400';
const LM = 'Mon, 16 Nov 2026 11:11:25 GMT';
const COMPRESS = process.env.NO_COMPRESS ? false : true;

http.createServer((req, res) => {
  if (MODE === 'revalidate' && req.url.startsWith('/static/') && req.headers['if-modified-since']) { res.writeHead(304, { 'cache-control': CC, 'last-modified': LM }); res.end(); return; }
  const headers = { ...req.headers, host: up.host };
  for (const h of ['origin', 'referer']) if (headers[h]) headers[h] = headers[h].replace(mine, ready.url);
  delete headers['accept-encoding'];
  const preq = http.request({ host: up.hostname, port: +up.port, method: req.method, path: req.url, headers }, pres => {
    const out = { ...pres.headers };
    if (out.location) out.location = out.location.replace(ready.url, mine);
    if (req.url.startsWith('/static/') && !out['cache-control'] && pres.statusCode === 200) { out['cache-control'] = CC; out['last-modified'] = LM; }
    const type = out['content-type'] || '';
    const compressible = COMPRESS && /text|javascript|json|svg|css/.test(type) && /br/.test(req.headers['accept-encoding'] || '');
    if (!compressible) { res.writeHead(pres.statusCode, out); pres.pipe(res); return; }
    const chunks = [];
    pres.on('data', c => chunks.push(c));
    pres.on('end', () => {
      const body = zlib.brotliCompressSync(Buffer.concat(chunks), { params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 4 } });
      delete out['content-length'];
      out['content-encoding'] = 'br';
      out['vary'] = 'accept-encoding';
      out['content-length'] = body.length;
      res.writeHead(pres.statusCode, out);
      res.end(body);
    });
  });
  preq.on('error', e => { res.writeHead(502); res.end(String(e)); });
  req.pipe(preq);
}).listen(port, LISTEN_HOST, () => console.error('proxy up', CC, COMPRESS));
