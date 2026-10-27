'use strict';

const $ = id => document.getElementById(id);
let machines = [];
let selectedID = null;
let clockOffset = 0;
let connected = false;

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined && text !== null) element.textContent = text;
  if (className) element.className = className;
  return element;
}
function bytes(value, digits = 1) {
  if (!Number.isFinite(value)) return 'Unavailable';
  if (value < 1024) return `${value} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let n = value / 1024, i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${Number(n.toFixed(digits))} ${units[i]}`;
}
function age(when) {
  const seconds = Math.max(0, (Date.now() + clockOffset - new Date(when)) / 1000);
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
function online(machine) { return Date.now() + clockOffset - new Date(machine.received_at) < 90000; }
function dot(isOnline) { return node('span', null, `dot${isOnline ? '' : ' offline'}`); }
function stateLabel(section) {
  const names = {unsupported: 'Not collected', permission_denied: 'Requires root', error: 'Unavailable'};
  const label = node('span', names[section?.status] || 'Unavailable', 'field-state');
  label.title = section?.error || 'No observation is available.';
  return label;
}
function data(section) { return section?.status === 'ok' && section.data !== undefined ? section.data : null; }
function field(list, label, value, mono = false) {
  const row = node('div');
  const dt = node('dt', label), dd = node('dd', null, mono ? 'mono' : '');
  if (value instanceof Node) dd.append(value); else dd.textContent = value || 'Unavailable';
  row.append(dt, dd); list.append(row);
}
function clearFields(id, rows) {
  const list = $(id); list.replaceChildren();
  for (const row of rows) field(list, ...row);
}
function boolValue(section) { return data(section) === null ? stateLabel(section) : (section.data ? 'On' : 'Off'); }
function displayTime(value) { return new Date(value).toLocaleString(undefined, {dateStyle: 'medium', timeStyle: 'short'}); }
function uptime(seconds) {
  const days = Math.floor(seconds / 86400), hours = Math.floor(seconds % 86400 / 3600), minutes = Math.floor(seconds % 3600 / 60);
  return days ? `${days}d ${hours}h` : `${hours}h ${minutes}m`;
}
function primaryAddress(snapshot) {
  for (const iface of data(snapshot.network) || []) {
    if (!iface.up || iface.loopback) continue;
    const address = iface.addresses.find(value => /^\d+\./.test(value));
    if (address) return address.split('/')[0];
  }
  return 'No IPv4 address';
}
function matchingMachines() {
  const query = $('search').value.toLowerCase().trim();
  return machines.filter(({snapshot: s}) => {
    const system = data(s.system), hw = data(s.hardware);
    return [system?.hostname, hw?.model_name, hw?.chip, ...(data(s.network) || []).flatMap(i => i.addresses)].join(' ').toLowerCase().includes(query);
  });
}
function renderList() {
  const filtered = matchingMachines();
  if (filtered.length && !filtered.some(m => m.snapshot.device_id === selectedID)) selectedID = filtered[0].snapshot.device_id;
  const focusedDevice = document.activeElement?.dataset.deviceId;
  const rows = $('machine-rows'); rows.replaceChildren();
  for (const machine of filtered) {
    const s = machine.snapshot, hw = data(s.hardware), system = data(s.system), storage = data(s.storage);
    const tr = node('tr', null, s.device_id === selectedID ? 'selected' : '');
    const first = node('td'), button = node('button', null, 'machine-button');
    button.type = 'button'; button.setAttribute('aria-pressed', String(s.device_id === selectedID));
    button.dataset.deviceId = s.device_id;
    const glyph = node('span', null, 'machine-glyph'); glyph.setAttribute('aria-hidden', 'true');
    const icon = node('img'); icon.src = '/machine.svg'; icon.alt = ''; icon.width = 20; icon.height = 20; glyph.append(icon);
    const name = node('span'); name.append(node('span', system?.hostname || 'Unknown machine', 'machine-title'), node('span', `${hw?.model_name || 'Mac'} · ${primaryAddress(s)}`, 'machine-meta'));
    button.append(glyph, name);
    button.addEventListener('click', () => { selectedID = s.device_id; render(); });
    first.append(button);
    const memory = node('td', hw ? bytes(hw.memory_bytes) : 'Unavailable');
    const pressure = data(s.memory)?.pressure;
    memory.append(node('span', pressure && pressure !== 'unknown' ? `${pressure[0].toUpperCase()}${pressure.slice(1)} pressure` : 'Pressure unavailable', 'machine-meta'));
    const status = node('span', null, 'row-status'); status.append(dot(online(machine)), document.createTextNode(online(machine) ? 'Online' : 'No recent report'));
    const statusCell = node('td'); statusCell.append(status);
    tr.append(first, node('td', hw?.chip || 'Unavailable'), memory, node('td', storage ? bytes(storage.available_bytes) : 'Unavailable'), node('td', system?.os_version || 'Unavailable'), statusCell);
    rows.append(tr);
    if (focusedDevice === s.device_id) button.focus({preventScroll: true});
  }
  $('machine-count').textContent = machines.length;
  $('empty-state').hidden = machines.length !== 0;
  $('no-results').hidden = machines.length === 0 || filtered.length !== 0;
  const available = machines.filter(online).length;
  $('fleet-status').replaceChildren(dot(connected && available > 0), document.createTextNode(connected ? `${available} online · ${machines.length} total` : 'Receiver unavailable'));
  const newest = machines.reduce((latest, m) => Math.max(latest, new Date(m.received_at).getTime()), 0);
  $('updated').textContent = newest ? `Last report ${age(newest)}` : 'Waiting for the first report';
}
function metric(value, label) {
  const wrap = node('div'), parts = value.split(' '), number = node('div', parts[0], 'metric');
  if (parts[1]) number.append(node('span', parts[1], 'unit'));
  wrap.append(number, node('p', label, 'metric-label'));
  return wrap;
}
function barPiece(fraction, className) {
  const piece = node('div', null, `bar-piece ${className}`);
  piece.style.width = `${Math.max(0, Math.min(1, fraction)) * 100}%`;
  return piece;
}
function renderMemory(s, hw) {
  const box = $('memory-content'), m = data(s.memory); box.replaceChildren();
  $('memory-pressure').textContent = m && m.pressure !== 'unknown' ? `${m.pressure[0].toUpperCase()}${m.pressure.slice(1)} pressure` : 'Pressure unavailable';
  $('memory-pressure').className = `small-status ${m?.pressure || ''}`;
  if (!m || !hw) { box.append(stateLabel(!m ? s.memory : s.hardware)); return; }
  box.append(metric(bytes(hw.memory_bytes), 'Memory installed'));
  const bar = node('div', null, 'bar'), legend = node('div', null, 'legend');
  let remaining = 1;
  for (const [label, value, color] of [['Active', m.active_bytes, 'active'], ['Wired', m.wired_bytes, 'wired'], ['Compressed', m.compressed_bytes, 'compressed']]) {
    const fraction = Math.min(remaining, value / hw.memory_bytes); remaining -= fraction;
    bar.append(barPiece(fraction, color));
    const item = node('span', null, 'legend-item'); item.append(node('span', null, `swatch ${color}`), document.createTextNode(`${label} ${bytes(value)}`)); legend.append(item);
  }
  const other = node('span', null, 'legend-item'); other.append(node('span', null, 'swatch'), document.createTextNode('Other / free')); legend.append(other);
  box.append(bar, legend, node('p', `${bytes(m.swap_used_bytes)} swap used · ${bytes(m.free_bytes)} free pages`, 'metric-note'));
}
function renderStorage(s) {
  const box = $('storage-content'), disk = data(s.storage); box.replaceChildren();
  if (!disk) { box.append(stateLabel(s.storage)); return; }
  const used = Math.max(0, disk.total_bytes - disk.free_bytes);
  const bar = node('div', null, 'bar'); bar.append(barPiece(used / disk.total_bytes, 'used'));
  const scale = node('div', null, 'storage-scale'); scale.append(node('span', `${bytes(used)} used`), node('span', `${bytes(disk.total_bytes)} total`));
  box.append(metric(bytes(disk.available_bytes), 'Available on the startup data volume'), bar, scale, node('p', disk.path, 'metric-note'));
}
function renderNetwork(s) {
  const box = $('network-content'); box.replaceChildren();
  const interfaces = data(s.network);
  if (!interfaces) { box.append(stateLabel(s.network)); return; }
  const visible = interfaces.filter(i => $('show-all-interfaces').checked || (i.up && !i.loopback && i.addresses.length > 0 && i.mac));
  if (!visible.length) { box.append(node('p', 'No active interfaces with a hardware address. Select “Show all interfaces” to inspect the rest.', 'unavailable')); return; }
  const table = node('table', null, 'network-table'), head = node('thead'), row = node('tr');
  for (const label of ['Interface', 'MAC address', 'IP address', 'State']) { const th = node('th', label); th.scope = 'col'; row.append(th); }
  head.append(row); table.append(head);
  const body = node('tbody');
  for (const iface of visible) {
    const row = node('tr'), addresses = node('td');
    for (const address of iface.addresses) addresses.append(node('span', address, 'address'));
    if (!iface.addresses.length) addresses.textContent = 'No address';
    row.append(node('td', iface.name), node('td', iface.mac || 'Not applicable', 'mac'), addresses, node('td', iface.up ? 'Up' : 'Down'));
    body.append(row);
  }
  table.append(body); box.append(table);
}
function renderDetail() {
  const machine = matchingMachines().find(m => m.snapshot.device_id === selectedID);
  $('detail').hidden = !machine;
  if (!machine) return;
  const s = machine.snapshot, hw = data(s.hardware), system = data(s.system);
  $('detail-name').textContent = system?.hostname || 'Unknown machine';
  $('detail-subtitle').textContent = [hw?.model_name, hw?.chip, hw?.memory_bytes ? bytes(hw.memory_bytes) : null].filter(Boolean).join(' · ');
  const isOnline = online(machine);
  $('detail-status').className = `status-pill${isOnline ? '' : ' stale'}`;
  $('detail-status').replaceChildren(dot(isOnline), document.createTextNode(isOnline ? 'Online' : `Last seen ${age(machine.received_at)}`));
  renderMemory(s, hw); renderStorage(s);
  clearFields('system-fields', system ? [
    ['macOS', `${system.os_version}${system.os_extra ? ` ${system.os_extra}` : ''} (${system.os_build})`],
    ['Uptime', uptime(system.uptime_seconds)], ['Load average', system.load_average.map(v => v.toFixed(2)).join(' / ') + '  (1 / 5 / 15 min)'],
    ['Console user', system.console_user || 'No active user'],
  ] : [['System status', stateLabel(s.system)]]);
  clearFields('security-fields', [
    ['Remote Login', boolValue(s.security.remote_login)], ['FileVault', boolValue(s.security.filevault)],
    ['Automatic login', data(s.security.auto_login) === null ? stateLabel(s.security.auto_login) : (s.security.auto_login.data || 'Off')],
    ['Activation Lock at startup', hw?.activation_lock === 'enabled' ? 'On' : hw?.activation_lock === 'disabled' ? 'Off' : 'Unavailable'],
  ]);
  renderNetwork(s);
  $('device-model').textContent = hw?.model_id || '';
  $('inventory-time').textContent = s.hardware?.observed_at ? `Inventory collected at agent startup · ${displayTime(s.hardware.observed_at)}` : 'Inventory unavailable';
  clearFields('hardware-fields', hw ? [
    ['Model', hw.model_name], ['Model number', hw.model_number, true], ['Chip', hw.chip],
    ['CPU cores', `${hw.physical_cores} physical · ${hw.logical_cores} logical`],
    ['Core groups', hw.core_groups?.length ? hw.core_groups.map(g => `${g.physical_cores} ${g.name}`).join(' · ') : 'Not reported'],
  ] : [['Hardware status', stateLabel(s.hardware)]]);
  clearFields('firmware-fields', hw ? [['Architecture', hw.architecture, true], ['System firmware', hw.firmware, true], ['OS loader', hw.os_loader, true], ['Installed memory', bytes(hw.memory_bytes)]] : []);
  clearFields('identifier-fields', hw ? [['Serial number', hw.serial_number, true], ['Hardware UUID', hw.hardware_uuid, true], ['Provisioning UDID', hw.provisioning_id, true], ['Hako device ID', s.device_id, true]] : []);
  $('agent-info').textContent = `Agent ${s.agent.version} · ${s.agent.uid === 0 ? 'Collected as root' : `Collected as user ${s.agent.uid}`} · Reported ${age(machine.received_at)}`;
}
function render() { renderList(); renderDetail(); }
async function refresh() {
  try {
    const response = await fetch('/api/machines', {cache: 'no-store'});
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const result = await response.json();
    machines = result.machines;
    clockOffset = new Date(result.server_time).getTime() - Date.now();
    if (!machines.some(m => m.snapshot.device_id === selectedID)) selectedID = machines[0]?.snapshot.device_id || null;
    connected = true; $('connection-error').hidden = true;
    render();
  } catch (error) {
    connected = false;
    $('connection-error').textContent = 'The local receiver is unavailable. Showing the last received data.';
    $('connection-error').hidden = false;
    render();
  } finally { window.setTimeout(refresh, 5000); }
}
$('search').addEventListener('input', render);
$('show-all-interfaces').addEventListener('change', renderDetail);
document.querySelectorAll('.detail-tabs a').forEach(link => link.addEventListener('click', () => {
  document.querySelectorAll('.detail-tabs a').forEach(other => other.classList.toggle('active', other === link));
}));
refresh();
