'use strict';

const $ = id => document.getElementById(id);
let machines = [];
let selectedID = null;
let clockOffset = 0;
let connected = false;
const isDemo = new URLSearchParams(window.location.search).get('demo') === '1';
let demoFleet = null;

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
    return [system?.hostname, system?.os_version, hw?.model_name, hw?.chip, ...(data(s.network) || []).flatMap(i => i.addresses)].join(' ').toLowerCase().includes(query);
  });
}
function coreConfiguration(hardware) {
  if (!hardware) return 'Core configuration unavailable';
  const groups = (hardware.core_groups || []).filter(group => Number.isInteger(group.physical_cores) && group.physical_cores > 0);
  const labels = new Map([['efficiency', 'E'], ['performance', 'P'], ['super', 'S']]);
  const parts = [];
  for (const [name, label] of labels) {
    const count = groups.filter(group => group.name.toLowerCase() === name).reduce((sum, group) => sum + group.physical_cores, 0);
    if (count) parts.push(`${count}${label}`);
  }
  for (const group of groups) {
    if (!labels.has(group.name.toLowerCase())) parts.push(`${group.physical_cores} ${group.name}`);
  }
  if (parts.length) return parts.join(' / ');
  if (!hardware.physical_cores) return 'Core configuration unavailable';
  return hardware.logical_cores > hardware.physical_cores ? `${hardware.physical_cores}C / ${hardware.logical_cores}T` : `${hardware.physical_cores} cores`;
}
function cpuUsage(section) {
  const cpu = data(section);
  if (!cpu) return stateLabel(section);
  if (!Number.isFinite(cpu.usage_percent) || cpu.usage_percent < 0 || !Number.isInteger(cpu.logical_cores) || cpu.logical_cores <= 0 || cpu.usage_percent > cpu.logical_cores * 100) {
    return node('span', 'Unavailable', 'field-state');
  }
  const value = node('span', `${cpu.usage_percent.toFixed(1)}%`, 'cpu-usage');
  value.title = `100% = one logical core. Total capacity: ${cpu.logical_cores * 100}%.`;
  return value;
}
function cpuCell(snapshot, hardware) {
  const cell = node('td'), cpu = data(snapshot.cpu), value = cpuUsage(snapshot.cpu);
  const summary = node('div', null, 'usage-summary');
  if (value.className === 'cpu-usage') {
    const bar = node('div', null, 'bar');
    bar.append(barPiece(cpu.usage_percent / (cpu.logical_cores * 100), 'active'));
    bar.setAttribute('role', 'progressbar');
    bar.setAttribute('aria-label', 'CPU usage');
    bar.setAttribute('aria-valuemin', '0');
    bar.setAttribute('aria-valuemax', String(cpu.logical_cores * 100));
    bar.setAttribute('aria-valuenow', String(cpu.usage_percent));
    bar.setAttribute('aria-valuetext', `${cpu.usage_percent.toFixed(1)}% of ${cpu.logical_cores * 100}% total capacity`);
    bar.title = value.title;
    summary.append(bar);
  }
  const values = node('span', null, 'usage-values');
  values.title = [hardware?.chip, ...(hardware?.core_groups || []).map(group => `${group.physical_cores} ${group.name}`)].filter(Boolean).join(' · ');
  values.append(value, document.createTextNode(` · ${coreConfiguration(hardware)}`));
  summary.append(values);
  cell.append(summary);
  return cell;
}
function renderList() {
  const filtered = matchingMachines();
  if (filtered.length && !filtered.some(m => m.snapshot.device_id === selectedID)) selectedID = filtered[0].snapshot.device_id;
  const focusedDevice = document.activeElement?.dataset.deviceId;
  const rows = $('machine-rows'); rows.replaceChildren();
  for (const machine of filtered) {
    const s = machine.snapshot, hw = data(s.hardware), system = data(s.system);
    const tr = node('tr', null, s.device_id === selectedID ? 'selected' : '');
    const first = node('td'), button = node('button', null, 'machine-button');
    button.type = 'button'; button.setAttribute('aria-pressed', String(s.device_id === selectedID));
    button.dataset.deviceId = s.device_id;
    const isOnline = online(machine), presence = dot(isOnline);
    presence.classList.add('machine-presence');
    presence.title = isOnline ? `Online · Last report ${age(machine.received_at)}` : `No recent report · Last seen ${age(machine.received_at)}`;
    presence.setAttribute('role', 'img');
    presence.setAttribute('aria-label', presence.title);
    const version = system?.os_version ? `macOS ${system.os_version}${system.os_extra ? ` ${system.os_extra}` : ''}` : 'macOS unavailable';
    const name = node('span');
    name.append(
      node('span', system?.hostname || 'Unknown machine', 'machine-title'),
      node('span', `${hw?.model_name || 'Mac'} · ${version}`, 'machine-meta'),
      node('span', primaryAddress(s), 'machine-address'),
    );
    button.append(presence, name);
    button.addEventListener('click', () => { selectedID = s.device_id; render(); });
    first.append(button);
    tr.append(first, cpuCell(s, hw), memoryCell(s, hw), storageCell(s.storage));
    rows.append(tr);
    if (focusedDevice === s.device_id) button.focus({preventScroll: true});
  }
  $('machine-count').textContent = machines.length;
  $('empty-state').hidden = machines.length !== 0;
  $('no-results').hidden = machines.length === 0 || filtered.length !== 0;
  const available = machines.filter(online).length;
  $('fleet-status').replaceChildren(dot(connected && available > 0), document.createTextNode(connected ? `${available} online · ${machines.length} total` : 'Receiver unavailable'));
  const newest = machines.reduce((latest, m) => Math.max(latest, new Date(m.received_at).getTime()), 0);
  $('updated').textContent = isDemo ? 'Sample data' : newest ? `Last report ${age(newest)}` : 'Waiting for the first report';
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
function memorySegments(memory) {
  return [['Active', memory.active_bytes, 'active'], ['Wired', memory.wired_bytes, 'wired'], ['Compressed', memory.compressed_bytes, 'compressed']];
}
function usageSummary(used, free, bar, explanation) {
  const gigabytes = value => `${(value / 1e9).toFixed(1)} GB`;
  const summary = node('div', null, 'usage-summary');
  const description = `${gigabytes(used)} used / ${gigabytes(free)} free`;
  summary.title = `${description}. ${explanation}`;
  bar.setAttribute('aria-hidden', 'true');
  const values = node('span', null, 'usage-values');
  values.setAttribute('aria-label', description);
  values.append(node('span', gigabytes(used)), document.createTextNode(` / ${gigabytes(free)}`));
  summary.append(bar, values);
  return summary;
}
function memoryCell(snapshot, hardware) {
  const cell = node('td'), memory = data(snapshot.memory), total = hardware?.memory_bytes;
  if (!memory || !hardware) {
    cell.append(stateLabel(!memory ? snapshot.memory : snapshot.hardware));
    return cell;
  }
  const segments = memorySegments(memory);
  if (!Number.isFinite(total) || total <= 0 || ![memory.free_bytes, ...segments.map(([, value]) => value)].every(value => Number.isFinite(value) && value >= 0)) {
    cell.append(node('span', 'Unavailable', 'field-state'));
    return cell;
  }
  const free = Math.min(total, memory.free_bytes), used = total - free;
  const bar = node('div', null, 'bar');
  let remaining = used;
  for (const [, value, color] of segments) {
    const amount = Math.min(remaining, value);
    bar.append(barPiece(amount / total, color));
    remaining -= amount;
  }
  bar.append(barPiece(remaining / total, 'other-used'));
  const summary = usageSummary(used, free, bar, 'Used includes cached and inactive memory; free is the reported free pages.');
  if (memory.pressure && memory.pressure !== 'unknown') summary.title += ` Memory pressure: ${memory.pressure}.`;
  cell.append(summary);
  return cell;
}
function storageCell(section) {
  const cell = node('td'), storage = data(section);
  if (!storage) {
    cell.append(stateLabel(section));
    return cell;
  }
  const total = storage.total_bytes;
  if (!Number.isFinite(total) || total <= 0 || !Number.isFinite(storage.free_bytes) || storage.free_bytes < 0) {
    cell.append(node('span', 'Unavailable', 'field-state'));
    return cell;
  }
  const free = Math.min(total, storage.free_bytes), used = total - free;
  const bar = node('div', null, 'bar');
  bar.append(barPiece(used / total, 'used'));
  cell.append(usageSummary(used, free, bar, 'Startup data volume; free is the filesystem-reported free space.'));
  return cell;
}
function renderMemory(s, hw) {
  const box = $('memory-content'), m = data(s.memory); box.replaceChildren();
  $('memory-pressure').textContent = m && m.pressure !== 'unknown' ? `${m.pressure[0].toUpperCase()}${m.pressure.slice(1)} pressure` : 'Pressure unavailable';
  $('memory-pressure').className = `small-status ${m?.pressure || ''}`;
  if (!m || !hw) { box.append(stateLabel(!m ? s.memory : s.hardware)); return; }
  box.append(metric(bytes(hw.memory_bytes), 'Memory installed'));
  const bar = node('div', null, 'bar'), legend = node('div', null, 'legend');
  let remaining = 1;
  for (const [label, value, color] of memorySegments(m)) {
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
    ['Uptime', uptime(system.uptime_seconds)], ['CPU usage', cpuUsage(s.cpu)], ['Load average', system.load_average.map(v => v.toFixed(2)).join(' / ') + '  (1 / 5 / 15 min)'],
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
    let result;
    if (isDemo) {
      demoFleet ||= (await import('./demo.js')).demoFleet;
      result = demoFleet();
    } else {
      const response = await fetch('/api/machines', {cache: 'no-store'});
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      result = await response.json();
    }
    machines = result.machines;
    clockOffset = new Date(result.server_time).getTime() - Date.now();
    if (!machines.some(m => m.snapshot.device_id === selectedID)) selectedID = machines[0]?.snapshot.device_id || null;
    connected = true; $('connection-error').hidden = true;
    render();
  } catch (error) {
    connected = false;
    $('connection-error').textContent = isDemo ? 'The demo could not load. Refresh to try again.' : 'The local receiver is unavailable. Showing the last received data.';
    $('connection-error').hidden = false;
    render();
  } finally { window.setTimeout(refresh, 5000); }
}
$('search').addEventListener('input', render);
$('show-all-interfaces').addEventListener('change', renderDetail);
document.querySelectorAll('.detail-tabs a').forEach(link => link.addEventListener('click', () => {
  document.querySelectorAll('.detail-tabs a').forEach(other => other.classList.toggle('active', other === link));
}));
if (isDemo) {
  document.title = 'Hako · Demo';
  $('workspace-label').textContent = 'Demo workspace';
  $('fleet-label').textContent = 'DEMO FLEET';
  $('fleet-mode').textContent = 'Back to live';
  $('fleet-mode').href = '/';
}
refresh();
