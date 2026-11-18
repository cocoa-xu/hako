import assert from 'node:assert/strict';
import test from 'node:test';
import {createEventStream} from '../web/events.mjs';

function response(status, extra = {}) {
  return {status, ok: status >= 200 && status < 300, type: 'basic', redirected: false, headers: new Headers(), ...extra};
}

async function settle() {
  for (let i = 0; i < 4; i++) await Promise.resolve();
}

function fixture(t) {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 100000});
  t.mock.method(Math, 'random', () => 0);
  const sources = [];
  const previousEventSource = globalThis.EventSource;
  globalThis.EventSource = class {
    listeners = new Map();
    closed = false;
    constructor(url) { assert.equal(url, '/api/events'); sources.push(this); }
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    close() { this.closed = true; }
    emit(name, data) { this.listeners.get(name)?.({data}); }
  };
  t.after(() => {
    if (previousEventSource === undefined) delete globalThis.EventSource;
    else globalThis.EventSource = previousEventSource;
  });
  const request = t.mock.method(globalThis, 'fetch', async () => response(204));
  const received = [];
  let disconnected = 0, unauthorized = 0;
  const stream = createEventStream({
    onFleet: fleet => received.push(fleet),
    onDisconnect: () => disconnected++,
    onUnauthorized: () => unauthorized++,
  });
  t.after(() => stream.stop());
  return {
    stream, sources, request, received,
    disconnected: () => disconnected,
    unauthorized: () => unauthorized,
    current: () => sources.at(-1),
    deliver() { sources.at(-1).emit('machines', JSON.stringify({machines: [], server_time: new Date().toISOString()})); },
    async tick(milliseconds) { t.mock.timers.tick(milliseconds); await settle(); },
  };
}

test('a healthy dashboard reuses one stream without session polling', async t => {
  const f = fixture(t);
  f.stream.start();
  f.deliver();
  await f.tick(86400000);
  assert.equal(f.sources.length, 1);
  assert.equal(f.received.length, 1);
  assert.equal(f.request.mock.callCount(), 0);
});

test('repeated short connections close native retries and back off despite receiving data', async t => {
  const f = fixture(t);
  f.stream.start();
  for (const delay of [1000, 2000, 4000, 8000, 16000, 30000, 30000]) {
    const count = f.sources.length;
    f.deliver();
    f.current().emit('error');
    assert.equal(f.current().closed, true);
    await f.tick(delay - 1);
    assert.equal(f.sources.length, count);
    await f.tick(1);
    assert.equal(f.sources.length, count + 1);
    assert.equal(f.request.mock.callCount(), count);
  }
  for (const call of f.request.mock.calls) {
    assert.equal(call.arguments[0], '/api/session');
    assert.equal(call.arguments[1].redirect, 'manual');
    assert.equal(call.arguments[1].cache, 'no-store');
  }
});

test('a fatal stream error recovers after temporary session failures', async t => {
  const f = fixture(t);
  f.request.mock.mockImplementationOnce(async () => response(502));
  f.stream.start();
  f.current().closed = true;
  f.current().emit('error');
  await f.tick(1000);
  assert.equal(f.sources.length, 1);
  await f.tick(2000);
  assert.equal(f.sources.length, 2);
  assert.equal(f.request.mock.callCount(), 2);
});

test('five-minute session outage does not create a fast request loop', async t => {
  const f = fixture(t);
  f.request.mock.mockImplementation(async () => response(503));
  f.stream.start();
  f.current().emit('error');
  for (let second = 0; second < 300; second++) await f.tick(1000);
  assert.ok(f.request.mock.callCount() >= 5);
  assert.ok(f.request.mock.callCount() <= 15);
  assert.equal(f.sources.length, 1);
});

for (const [name, result] of [
  ['unauthorized', response(401)],
  ['forbidden', response(403)],
  ['redirect', response(302)],
  ['opaque redirect', response(0, {type: 'opaqueredirect'})],
  ['followed redirect', response(204, {redirected: true})],
  ['login HTML', response(200)],
]) {
  test(`session ${name} stops automatic retries`, async t => {
    const f = fixture(t);
    f.request.mock.mockImplementation(async () => result);
    f.stream.start();
    f.current().emit('error');
    await f.tick(1000);
    await f.tick(3600000);
    assert.equal(f.unauthorized(), 1);
    assert.equal(f.request.mock.callCount(), 1);
    assert.equal(f.sources.length, 1);
  });
}

test('stopping cancels pending timers and ignores callbacks from an old stream', async t => {
  const f = fixture(t);
  f.stream.start();
  const old = f.current();
  old.emit('error');
  f.stream.stop();
  await f.tick(1000);
  assert.equal(f.request.mock.callCount(), 0);
  f.stream.start();
  old.emit('error');
  old.emit('machines', JSON.stringify({machines: [], server_time: new Date().toISOString()}));
  await f.tick(60000);
  assert.equal(f.disconnected(), 1);
  assert.equal(f.sources.length, 2);
  assert.equal(f.received.length, 0);
  assert.equal(f.request.mock.callCount(), 0);
});

test('restarting aborts a session check and ignores its late response', async t => {
  const f = fixture(t);
  let finish, signal;
  f.request.mock.mockImplementation((_url, options) => {
    signal = options.signal;
    return new Promise(resolve => { finish = resolve; });
  });
  f.stream.start();
  f.current().emit('error');
  await f.tick(1000);
  assert.equal(signal.aborted, false);
  f.stream.start();
  assert.equal(signal.aborted, true);
  finish(response(401));
  await settle();
  await f.tick(60000);
  assert.equal(f.unauthorized(), 0);
  assert.equal(f.sources.length, 2);
  assert.equal(f.current().closed, false);
  assert.equal(f.request.mock.callCount(), 1);
});

test('session checks time out and back off before retrying', async t => {
  const f = fixture(t);
  const signals = [];
  f.request.mock.mockImplementation((_url, {signal}) => {
    signals.push(signal);
    return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('aborted')), {once: true}));
  });
  f.stream.start();
  f.current().emit('error');
  await f.tick(1000);
  f.current().emit('error');
  await f.tick(9999);
  assert.equal(f.request.mock.callCount(), 1);
  await f.tick(1);
  assert.equal(signals[0].aborted, true);
  assert.equal(f.request.mock.callCount(), 1);
  await f.tick(1999);
  assert.equal(f.request.mock.callCount(), 1);
  await f.tick(1);
  assert.equal(f.request.mock.callCount(), 2);
  assert.equal(f.sources.length, 1);
});

test('session Retry-After delays the next attempt', async t => {
  const f = fixture(t);
  f.request.mock.mockImplementationOnce(async () => response(429, {headers: new Headers({'Retry-After': '120'})}));
  f.stream.start();
  f.current().emit('error');
  await f.tick(1000);
  await f.tick(119999);
  assert.equal(f.request.mock.callCount(), 1);
  await f.tick(1);
  assert.equal(f.request.mock.callCount(), 2);
  assert.equal(f.sources.length, 2);
});

test('session Retry-After supports dates and limits excessive delays', async t => {
  const f = fixture(t);
  f.request.mock.mockImplementationOnce(async () => response(503, {headers: new Headers({'Retry-After': new Date(Date.now() + 120000).toUTCString()})}));
  f.request.mock.mockImplementationOnce(async () => response(503, {headers: new Headers({'Retry-After': '99999999'})}), 1);
  f.stream.start();
  f.current().emit('error');
  await f.tick(1000);
  await f.tick(119999);
  assert.equal(f.request.mock.callCount(), 1);
  await f.tick(1);
  assert.equal(f.request.mock.callCount(), 2);
  await f.tick(3599999);
  assert.equal(f.request.mock.callCount(), 2);
  await f.tick(1);
  assert.equal(f.request.mock.callCount(), 3);
  assert.equal(f.sources.length, 2);
});

test('a sustained healthy stream resets the reconnect delay', async t => {
  const f = fixture(t);
  f.stream.start();
  f.current().emit('error');
  await f.tick(1000);
  f.current().emit('error');
  await f.tick(2000);
  f.deliver();
  await f.tick(60000);
  f.current().emit('error');
  await f.tick(1000);
  assert.equal(f.sources.length, 4);
});

test('invalid fleet events close the stream without delivering corrupt data', async t => {
  const f = fixture(t);
  f.stream.start();
  f.current().emit('machines', '{');
  assert.equal(f.current().closed, true);
  await f.tick(1000);
  f.current().emit('machines', JSON.stringify({machines: null, server_time: 'invalid'}));
  assert.equal(f.current().closed, true);
  assert.equal(f.received.length, 0);
});
