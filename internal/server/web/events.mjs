// Creates the dashboard's event connection and bounded recovery loop.
export function createEventStream({onFleet, onDisconnect, onUnauthorized}) {
  let source = null;
  let timer = null;
  let sessionCheck = null;
  let generation = 0;
  let failures = 0;
  let healthySince = null;

  function stop() {
    generation++;
    source?.close(); source = null;
    clearTimeout(timer); timer = null;
    sessionCheck?.abort(); sessionCheck = null;
    failures = 0;
    healthySince = null;
  }

  function schedule(run, minimumDelay = 0) {
    if (run !== generation || timer !== null) return;
    const ceiling = Math.min(60000, 2000 * 2 ** failures);
    const delay = Math.max(minimumDelay, ceiling / 2 + Math.random() * ceiling / 2);
    failures = Math.min(failures + 1, 5);
    timer = setTimeout(() => { timer = null; void retry(run); }, delay);
  }

  async function retry(run) {
    if (run !== generation) return;
    const check = new AbortController();
    sessionCheck = check;
    const timeout = setTimeout(() => check.abort(), 10000);
    let minimumDelay = 0;
    try {
      const response = await fetch('/api/session', {signal: check.signal, redirect: 'manual', cache: 'no-store'});
      if (run !== generation) return;
      if (response.status === 401 || response.status === 403 || response.type === 'opaqueredirect' || response.redirected ||
          (response.status >= 300 && response.status < 400) || (response.ok && response.status !== 204)) {
        stop(); onUnauthorized();
        return;
      }
      if (response.status === 204) {
        connect(run);
        return;
      }
      const retryAfter = response.headers.get('Retry-After');
      const requested = /^\d+$/.test(retryAfter || '') ? Number(retryAfter) * 1000 : Date.parse(retryAfter) - Date.now();
      if (Number.isFinite(requested)) minimumDelay = Math.max(0, Math.min(requested, 3600000));
    } catch {
      // Network failures and timeouts share the same retry budget.
    } finally {
      clearTimeout(timeout);
      if (sessionCheck === check) sessionCheck = null;
    }
    schedule(run, minimumDelay);
  }

  function connect(run) {
    if (run !== generation) return;
    const current = new EventSource('/api/events');
    source = current;
    const failed = () => {
      if (run !== generation || source !== current) return;
      source = null;
      current.close();
      if (healthySince !== null && Date.now() - healthySince >= 60000) failures = 0;
      healthySince = null;
      onDisconnect();
      schedule(run);
    };
    current.addEventListener('machines', event => {
      if (run !== generation || source !== current) return;
      try {
        const fleet = JSON.parse(event.data);
        if (!Array.isArray(fleet?.machines) || !Number.isFinite(Date.parse(fleet.server_time))) {
          failed();
          return;
        }
        onFleet(fleet);
        healthySince ??= Date.now();
      } catch { failed(); }
    });
    current.addEventListener('error', failed);
  }

  return {
    start() { stop(); connect(generation); },
    stop,
  };
}
