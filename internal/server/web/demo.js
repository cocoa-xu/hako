const GiB = 1024 ** 3;
const previewStartedAt = Date.now();
const configurations = [
  {name: 'mini-build-01', chip: 'M6', cores: [4, 6, 2], cpu: 486.2, memory: 64, disk: 926, available: 612, uptime: 12, load: [4.82, 3.16, 2.91]},
  {name: 'mini-build-02', chip: 'M4 Pro', cores: [8, 4], cpu: 812.5, memory: 48, disk: 926, available: 284, uptime: 8, load: [9.24, 8.12, 6.47], pressure: 'warning'},
  {name: 'mini-runner-01', chip: 'M4', cores: [4, 6], cpu: 142.8, memory: 24, disk: 460, available: 186, uptime: 21, load: [1.42, 1.08, 0.94]},
  {name: 'mini-runner-02', chip: 'M2 Pro', cores: [8, 4], cpu: 1021.4, memory: 16, disk: 460, available: 92, uptime: 6, load: [10.21, 9.38, 7.82], pressure: 'critical'},
  {name: 'mini-lab-01', chip: 'M2', cores: [4, 4], cpu: 48.6, memory: 16, disk: 228, available: 8.6, uptime: 3, load: [0.48, 0.62, 0.51]},
  {name: 'mini-lab-02', chip: 'M1', cores: [4, 4], memory: 8, disk: 228, available: 104, uptime: 2, load: [0.72, 0.58, 0.41], unprivileged: true, memoryUnavailable: true},
  {name: 'mini-spare-01', chip: 'M4', cores: [4, 6], cpu: 12.4, memory: 16, disk: 228, available: 192, uptime: 5, load: [0.12, 0.18, 0.14], staleSeconds: 42 * 60},
  {name: 'mini-archive-01', chip: 'M1', cores: [4, 4], cpu: 21.1, memory: 16, disk: 460, available: 312, uptime: 37, load: [0.21, 0.31, 0.26], staleSeconds: 26 * 3600},
];

function sampleMachine(config, index, now) {
  const suffix = String(index + 1).padStart(2, '0');
  const bootedAt = previewStartedAt - config.uptime * 86400000;
  const reportedAt = config.staleSeconds ? previewStartedAt - config.staleSeconds * 1000 : now - (index * 3 + 2) * 1000;
  const timestamp = new Date(reportedAt).toISOString();
  const startup = new Date(bootedAt + 60000).toISOString();
  const ok = data => ({status: 'ok', observed_at: timestamp, data});
  const unavailable = (status, error) => ({status, observed_at: timestamp, error});
  const pressure = config.pressure || 'normal';
  const memory = config.memory * GiB;
  const compressed = pressure === 'critical' ? 0.28 : pressure === 'warning' ? 0.16 : 0.04;
  const active = pressure === 'normal' ? 0.32 : 0.52;
  const swap = pressure === 'critical' ? 6.2 : pressure === 'warning' ? 1.4 : 0;
  const cores = config.cores.reduce((sum, count) => sum + count, 0);
  return {
    received_at: timestamp,
    snapshot: {
      schema_version: 1,
      device_id: `demo-${suffix}`,
      reported_at: timestamp,
      agent: {version: '0.1.0-demo', started_at: startup, uid: config.unprivileged ? 501 : 0},
      hardware: {
        ...ok({
          model_name: 'Mac mini', model_id: `DEMO-${config.chip.replaceAll(' ', '-')}`,
          model_number: 'Sample configuration', chip: `Apple ${config.chip}`, architecture: 'arm64',
          physical_cores: cores, logical_cores: cores,
          core_groups: config.cores.map((count, i) => ({name: ['Performance', 'Efficiency', 'Super'][i], physical_cores: count, logical_cores: count})),
          memory_bytes: memory, serial_number: `DEMO0000${suffix}`,
          hardware_uuid: `00000000-0000-4000-8000-0000000000${suffix}`,
          provisioning_id: `DEMO-DEVICE-${suffix}`,
          firmware: 'Sample firmware', os_loader: 'Sample loader', activation_lock: 'disabled',
        }),
        observed_at: startup,
      },
      system: ok({
        hostname: config.name, os_name: 'macOS', os_version: index < 4 ? '15.5' : '14.7.5', os_build: 'DEMO',
        booted_at: new Date(bootedAt).toISOString(), uptime_seconds: Math.floor((reportedAt - bootedAt) / 1000),
        load_average: config.load, console_user: index === 4 ? 'demo' : '',
      }),
      cpu: config.cpu === undefined ? unavailable('error', 'Sample CPU collection failure.') : ok({usage_percent: config.cpu, logical_cores: cores}),
      memory: config.memoryUnavailable ? unavailable('error', 'Sample collection failure.') : ok({
        free_bytes: Math.round(memory * (1 - active - compressed - 0.15)),
        active_bytes: Math.round(memory * active), inactive_bytes: Math.round(memory * 0.04),
        speculative_bytes: Math.round(memory * 0.01), wired_bytes: Math.round(memory * 0.1),
        compressed_bytes: Math.round(memory * compressed), pressure,
        swap_total_bytes: Math.ceil(swap) * GiB, swap_used_bytes: Math.round(swap * GiB),
      }),
      storage: ok({
        path: '/System/Volumes/Data', total_bytes: config.disk * GiB,
        free_bytes: Math.round((config.available + 2) * GiB), available_bytes: Math.round(config.available * GiB),
      }),
      network: ok([
        {name: 'en0', mac: `02:00:00:00:00:${suffix}`, addresses: [`192.0.2.${index + 11}/24`], up: true, loopback: false},
        {name: 'en1', mac: `02:00:00:00:01:${suffix}`, addresses: [], up: false, loopback: false},
        {name: 'lo0', addresses: ['127.0.0.1/8', '::1/128'], up: true, loopback: true},
      ]),
      security: {
        remote_login: config.unprivileged ? unavailable('permission_denied', 'Sample agent running without root.') : ok(index !== 6),
        filevault: ok(index !== 7), auto_login: ok(''),
      },
      lldp: unavailable('unsupported', 'Not collected.'),
      session_apps: unavailable('unsupported', 'Not collected.'),
    },
  };
}

export function demoFleet(now = Date.now()) {
  return {machines: configurations.map((config, index) => sampleMachine(config, index, now)), server_time: new Date(now).toISOString()};
}
